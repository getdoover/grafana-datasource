package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"github.com/getdoover/doover/pkg/doover"
)

const (
	// fleetWindow is the server's hard cap on a multi-agent query window.
	fleetWindow = 7 * 24 * time.Hour
	// maxFleetWindows bounds how many 7-day windows one panel query walks.
	maxFleetWindows = 5
	// fleetPerAgentLimit is the per-agent message cap per request (server max 1500,
	// but total = agents × per-agent is capped at 50k, so stay conservative).
	fleetPerAgentLimit = 1000
)

// queryFleetLatest renders one channel's current value across many agents as a
// table: one row per agent, stamped with each aggregate's last_updated.
func (d *Datasource) queryFleetLatest(ctx context.Context, qm queryModel) backend.DataResponse {
	agentIDs, err := qm.parseAgentIDs()
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}
	channel := strings.TrimSpace(qm.ChannelName)
	if channel == "" {
		return backend.ErrDataResponse(backend.StatusBadRequest, "channel name is required")
	}
	fields, err := qm.parseFields()
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}

	resp, err := d.client.FetchMultiAgentAggregates(ctx, channel, agentIDs)
	if err != nil {
		return errToDataResponse(err)
	}

	agents := make([]string, 0, len(resp.Results))
	times := make([]time.Time, 0, len(resp.Results))
	values := make(map[string][]*float64, len(fields))
	for _, f := range fields {
		values[f] = make([]*float64, 0, len(resp.Results))
	}

	sort.Slice(resp.Results, func(i, j int) bool { return resp.Results[i].AgentID < resp.Results[j].AgentID })
	for _, r := range resp.Results {
		row := extractRow(flattenAggregate(r.Data), fields)
		agents = append(agents, strconv.FormatUint(r.AgentID, 10))
		ts := r.LastUpdated()
		if ts.IsZero() {
			ts = time.Now()
		}
		times = append(times, ts)
		for _, f := range fields {
			if row != nil {
				values[f] = append(values[f], row[f])
			} else {
				values[f] = append(values[f], nil)
			}
		}
	}

	frame := data.NewFrame(channel,
		data.NewField("agent", nil, agents),
		data.NewField("time", nil, times),
	)
	for _, f := range fields {
		frame.Fields = append(frame.Fields, data.NewField(f, nil, values[f]))
	}
	frame.Meta = &data.FrameMeta{PreferredVisualization: data.VisTypeTable}

	var response backend.DataResponse
	response.Frames = append(response.Frames, frame)
	return response
}

// queryFleetTimeseries renders one field of one channel across many agents,
// one series (frame) per agent. The server enforces a ≤7-day window per request,
// so the query range is chunked; each window is drained via the legacy `next`
// cursor (see DEVLOG on next_cursors).
func (d *Datasource) queryFleetTimeseries(ctx context.Context, qm queryModel, query backend.DataQuery) backend.DataResponse {
	agentIDs, err := qm.parseAgentIDs()
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}
	channel := strings.TrimSpace(qm.ChannelName)
	if channel == "" {
		return backend.ErrDataResponse(backend.StatusBadRequest, "channel name is required")
	}
	fields, err := qm.parseFields()
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}
	if len(fields) != 1 {
		// The multi-agent messages endpoint only reliably supports one field.
		return backend.ErrDataResponse(backend.StatusBadRequest, "fleet timeseries supports exactly one field per query")
	}
	field := fields[0]

	msgs, truncated, err := d.fetchFleetRange(ctx, channel, agentIDs, field, query.TimeRange)
	if err != nil {
		return errToDataResponse(err)
	}

	// Group by owning agent, oldest first within each series.
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	byAgent := make(map[uint64]*series)
	for _, m := range msgs {
		v, ok := lookupPath(m.Data, field)
		if !ok {
			continue
		}
		f, ok := toFloat(v)
		if !ok {
			continue
		}
		s := byAgent[m.Channel.AgentID]
		if s == nil {
			s = &series{values: map[string][]*float64{field: {}}}
			byAgent[m.Channel.AgentID] = s
		}
		val := f
		s.times = append(s.times, doover.TimeFromSnowflake(m.ID))
		s.values[field] = append(s.values[field], &val)
	}

	// Stable frame order: requested agent order, then any others by ID.
	ordered := make([]uint64, 0, len(byAgent))
	seen := make(map[uint64]bool)
	for _, id := range agentIDs {
		if byAgent[id] != nil && !seen[id] {
			ordered = append(ordered, id)
			seen[id] = true
		}
	}
	for id := range byAgent {
		if !seen[id] {
			ordered = append(ordered, id)
		}
	}

	var response backend.DataResponse
	for _, id := range ordered {
		s := byAgent[id]
		agentLabel := strconv.FormatUint(id, 10)
		frame := data.NewFrame(fmt.Sprintf("%s %s", channel, agentLabel),
			data.NewField("time", nil, s.times),
			data.NewField(field, data.Labels{"agent": agentLabel}, s.values[field]),
		)
		response.Frames = append(response.Frames, frame)
	}
	if len(response.Frames) == 0 {
		response.Frames = append(response.Frames, data.NewFrame(channel))
	}
	if truncated {
		response.Frames[0].AppendNotices(data.Notice{
			Severity: data.NoticeSeverityWarning,
			Text: fmt.Sprintf("Result truncated: the range exceeds %d weeks or a window held more data than one drain fetches. Narrow the range or reduce agents.",
				maxFleetWindows),
		})
	}
	return response
}

// fetchFleetRange walks the query range in ≤7-day windows (newest first, since
// panels care most about recent data if truncation strikes) and drains each
// window's pagination.
func (d *Datasource) fetchFleetRange(ctx context.Context, channel string, agentIDs []uint64, field string, tr backend.TimeRange) ([]doover.BatchMessage, bool, error) {
	from := tr.From.Truncate(time.Minute)
	to := tr.To.Truncate(time.Minute).Add(time.Minute)

	windows := chunkWindows(from, to, fleetWindow)
	truncated := len(windows) > maxFleetWindows
	if truncated {
		windows = windows[:maxFleetWindows]
	}

	var all []doover.BatchMessage
	for _, w := range windows {
		before := doover.SnowflakeFromTime(w.to)
		after := doover.SnowflakeFromTime(w.from)
		for page := 0; page < maxPagesPerQuery; page++ {
			resp, err := d.client.FetchMultiAgentMessages(ctx, channel, agentIDs, []string{field}, before, after, fleetPerAgentLimit)
			if err != nil {
				return nil, false, err
			}
			all = append(all, resp.Results...)
			if resp.Next == nil {
				break
			}
			before = *resp.Next
			if page == maxPagesPerQuery-1 {
				truncated = true
			}
		}
	}
	return all, truncated, nil
}

type window struct{ from, to time.Time }

// chunkWindows splits [from, to) into windows of at most width, newest first.
func chunkWindows(from, to time.Time, width time.Duration) []window {
	if !to.After(from) {
		return nil
	}
	var out []window
	end := to
	for end.After(from) {
		start := end.Add(-width)
		if start.Before(from) {
			start = from
		}
		out = append(out, window{from: start, to: end})
		end = start
	}
	return out
}

// lookupPath resolves a dot-joined field path against a raw JSON payload.
// It tries the flattened key first ({"a.b": v}), then walks nested objects
// ({"a": {"b": v}}), so it works for both projected and raw message shapes.
func lookupPath(raw json.RawMessage, path string) (any, bool) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	if v, ok := obj[path]; ok {
		return v, true
	}
	parts := strings.Split(path, ".")
	var cur any = obj
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}
