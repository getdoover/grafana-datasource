package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"github.com/getdoover/doover/pkg/doover"
	"github.com/getdoover/doover/pkg/models"
)

var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ backend.CallResourceHandler   = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// maxPagesPerQuery bounds the pagination drain so a very dense channel over a wide
// time range cannot make a single panel fetch unbounded amounts of data.
// At the server's 1500-per-page cap this allows up to 30k raw points per query.
const maxPagesPerQuery = 20

func NewDatasource(_ context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	cfg, err := models.LoadPluginSettings(settings)
	if err != nil {
		return nil, err
	}
	return &Datasource{
		settings: cfg,
		client: doover.NewClient(doover.Config{
			BaseURL:      cfg.BaseURL,
			OrgID:        cfg.OrgID,
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.Secrets.ClientSecret,
		}),
	}, nil
}

type Datasource struct {
	settings *models.PluginSettings
	client   *doover.Client
}

func (d *Datasource) Dispose() {}

func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	response := backend.NewQueryDataResponse()
	for _, q := range req.Queries {
		response.Responses[q.RefID] = d.query(ctx, q)
	}
	return response, nil
}

type queryModel struct {
	// AgentID is a snowflake, carried as a string end-to-end because snowflakes
	// exceed JavaScript's safe-integer range.
	AgentID     string `json:"agentId"`
	ChannelName string `json:"channelName"`
	// AgentIDs is a comma-separated list of agent snowflakes, used by the
	// fleet-* query types (template variables interpolate as CSV).
	AgentIDs string `json:"agentIds"`
	// FieldNames is comma-separated; keys are dot-joined for nested values (a.b.c).
	FieldNames string `json:"fieldNames"`
	// QueryType is "timeseries" (default), "latest", "fleet-latest" or "fleet-timeseries".
	QueryType string `json:"queryType"`
}

func (qm *queryModel) parseAgentIDs() ([]uint64, error) {
	var ids []uint64
	for _, part := range strings.Split(qm.AgentIDs, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid agent ID %q: must be a numeric Doover agent ID", part)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("at least one agent ID is required")
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("too many agents (%d): the Doover API allows at most 500 per query", len(ids))
	}
	return ids, nil
}

func (qm *queryModel) parseFields() ([]string, error) {
	var fields []string
	for _, f := range strings.Split(qm.FieldNames, ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 {
		return nil, errors.New("at least one field name is required")
	}
	return fields, nil
}

func (qm *queryModel) parse() (agentID uint64, channel string, fields []string, err error) {
	agentID, err = strconv.ParseUint(strings.TrimSpace(qm.AgentID), 10, 64)
	if err != nil {
		return 0, "", nil, fmt.Errorf("invalid agent ID %q: must be a numeric Doover agent ID", qm.AgentID)
	}
	channel = strings.TrimSpace(qm.ChannelName)
	if channel == "" {
		return 0, "", nil, errors.New("channel name is required")
	}
	fields, err = qm.parseFields()
	if err != nil {
		return 0, "", nil, err
	}
	return agentID, channel, fields, nil
}

func (d *Datasource) query(ctx context.Context, query backend.DataQuery) backend.DataResponse {
	var qm queryModel
	if err := json.Unmarshal(query.JSON, &qm); err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("json unmarshal: %v", err))
	}

	// Fleet query types take a list of agents; the rest take a single agent.
	switch qm.QueryType {
	case "fleet-latest":
		return d.queryFleetLatest(ctx, qm)
	case "fleet-timeseries":
		return d.queryFleetTimeseries(ctx, qm, query)
	}

	agentID, channel, fields, err := qm.parse()
	if err != nil {
		return backend.ErrDataResponse(backend.StatusBadRequest, err.Error())
	}

	var frames data.Frames
	switch qm.QueryType {
	case "", "timeseries":
		frames, err = d.queryTimeseries(ctx, agentID, channel, fields, query)
	case "latest":
		var frame *data.Frame
		frame, err = d.queryLatest(ctx, agentID, channel, fields)
		frames = data.Frames{frame}
	default:
		return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("unknown query type %q", qm.QueryType))
	}
	if err != nil {
		return errToDataResponse(err)
	}

	return backend.DataResponse{Frames: frames}
}

func (d *Datasource) queryTimeseries(ctx context.Context, agentID uint64, channel string, fields []string, query backend.DataQuery) (data.Frames, error) {
	points, truncated, err := d.fetchRange(ctx, agentID, channel, fields, query.TimeRange)
	if err != nil {
		return nil, err
	}
	s := extractSeries(fields, points)

	var frame *data.Frame
	maxPoints := int(query.MaxDataPoints)
	if maxPoints > 0 && len(s.times) > maxPoints {
		frame = aggregatedFrame(channel, fields, downsample(s, fields, maxPoints))
	} else {
		frame = rawFrame(channel, fields, s)
	}
	if truncated {
		frame.AppendNotices(data.Notice{
			Severity: data.NoticeSeverityWarning,
			Text: fmt.Sprintf("Result truncated at %d pages (%d raw points): the time range holds more data than one query fetches. Narrow the range.",
				maxPagesPerQuery, len(points)),
		})
	}
	return data.Frames{frame}, nil
}

// queryLatest reads the channel aggregate — the merged current state — and emits
// a single-row frame stamped with the aggregate's last_updated time (falling
// back to evaluation time when the server omits it).
func (d *Datasource) queryLatest(ctx context.Context, agentID uint64, channel string, fields []string) (*data.Frame, error) {
	agg, err := d.client.FetchAggregate(ctx, agentID, channel)
	if err != nil {
		return nil, err
	}
	row := extractRow(flattenAggregate(agg.Data), fields)

	ts := agg.LastUpdated()
	if ts.IsZero() {
		ts = time.Now()
	}
	frame := data.NewFrame(channel, data.NewField("time", nil, []time.Time{ts}))
	for _, f := range fields {
		var v []*float64 = []*float64{nil}
		if row != nil {
			v = []*float64{row[f]}
		}
		frame.Fields = append(frame.Fields, data.NewField(f, nil, v))
	}
	return frame, nil
}

// fetchRange drains the timeseries endpoint across the query window.
// Bounds are snapped outward to the minute so the server's response cache
// (keyed on exact before/after/fields/limit) is reusable across refreshes.
// The server returns newest-first and caps each page at 1500 results; the
// `next` cursor (oldest returned snowflake) pages further back. The returned
// bool reports whether the drain stopped at the page cap with data remaining.
func (d *Datasource) fetchRange(ctx context.Context, agentID uint64, channel string, fields []string, tr backend.TimeRange) ([]doover.DataPoint, bool, error) {
	after := doover.SnowflakeFromTime(tr.From.Truncate(time.Minute))
	before := doover.SnowflakeFromTime(tr.To.Truncate(time.Minute).Add(time.Minute))

	var all []doover.DataPoint
	for page := 0; page < maxPagesPerQuery; page++ {
		resp, err := d.client.FetchTimeseries(ctx, agentID, channel, fields, before, after, doover.MaxTimeseriesLimit)
		if err != nil {
			return nil, false, err
		}
		all = append(all, resp.Results...)
		if resp.Next == nil {
			return all, false, nil
		}
		before = *resp.Next
	}
	return all, true, nil
}

// extractSeries turns raw points into time-aligned rows, oldest first.
// Points contributing nothing numeric for any requested field are dropped.
func extractSeries(fields []string, points []doover.DataPoint) series {
	sort.Slice(points, func(i, j int) bool { return points[i].MessageID < points[j].MessageID })

	s := series{values: make(map[string][]*float64, len(fields))}
	for _, f := range fields {
		s.values[f] = make([]*float64, 0, len(points))
	}
	for _, p := range points {
		row := extractRowFromPayload(p.Value, fields)
		if row == nil {
			continue
		}
		s.times = append(s.times, doover.TimeFromSnowflake(p.MessageID))
		for _, f := range fields {
			s.values[f] = append(s.values[f], row[f])
		}
	}
	return s
}

func rawFrame(channel string, fields []string, s series) *data.Frame {
	frame := data.NewFrame(channel, data.NewField("time", nil, s.times))
	for _, f := range fields {
		frame.Fields = append(frame.Fields, data.NewField(f, nil, s.values[f]))
	}
	return frame
}

// aggregatedFrame emits, per field, the bucket mean under the field's own name
// plus min/max companion series for envelope rendering.
func aggregatedFrame(channel string, fields []string, agg aggregated) *data.Frame {
	frame := data.NewFrame(channel, data.NewField("time", nil, agg.times))
	for _, f := range fields {
		frame.Fields = append(frame.Fields,
			data.NewField(f, nil, agg.mean[f]),
			data.NewField(f+" (min)", nil, agg.min[f]),
			data.NewField(f+" (max)", nil, agg.max_[f]),
		)
	}
	return frame
}

// extractRowFromPayload pulls numeric values for the requested fields out of one
// point's payload. The server flattens messages to dot-joined keys, so the payload
// is usually {"field.name": value}; a bare scalar can appear for single-field data.
// Returns nil if the payload holds nothing numeric for any requested field.
func extractRowFromPayload(raw json.RawMessage, fields []string) map[string]*float64 {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return extractRow(obj, fields)
	}
	// Not an object: a bare scalar is only unambiguous for a single field.
	if len(fields) == 1 {
		var scalar any
		if err := json.Unmarshal(raw, &scalar); err == nil {
			if v, ok := toFloat(scalar); ok {
				return map[string]*float64{fields[0]: &v}
			}
		}
	}
	return nil
}

func extractRow(obj map[string]any, fields []string) map[string]*float64 {
	if obj == nil {
		return nil
	}
	row := make(map[string]*float64, len(fields))
	found := false
	for _, f := range fields {
		if v, ok := toFloat(obj[f]); ok {
			val := v
			row[f] = &val
			found = true
		} else {
			row[f] = nil
		}
	}
	if !found {
		return nil
	}
	return row
}

// flattenAggregate flattens a nested aggregate object to dot-joined keys so the
// same field names work for both aggregate and timeseries queries.
func flattenAggregate(raw json.RawMessage) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	out := make(map[string]any)
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			if child, ok := v.(map[string]any); ok && len(child) > 0 {
				walk(key, child)
			} else {
				out[key] = v
			}
		}
	}
	walk("", obj)
	return out
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// errToDataResponse maps Doover API failures to readable panel errors.
func errToDataResponse(err error) backend.DataResponse {
	var se *doover.StatusError
	if errors.As(err, &se) {
		switch se.Status {
		case 400:
			return backend.ErrDataResponse(backend.StatusBadRequest, fmt.Sprintf("Doover rejected the query: %s", se.Body))
		case 401:
			return backend.ErrDataResponse(backend.StatusUnauthorized, "Doover authentication failed — check the client ID and secret in the datasource settings")
		case 403:
			return backend.ErrDataResponse(backend.StatusForbidden, "Doover denied access — the OAuth client's roles may not cover this agent, or the organisation ID may be wrong")
		case 404:
			return backend.ErrDataResponse(backend.StatusNotFound, "agent or channel not found — check the agent ID and channel name")
		}
	}
	return backend.ErrDataResponse(backend.StatusInternal, err.Error())
}

// CallResource serves the query editor's discovery pickers.
//   GET channels?agentId=N          → [{"label","value"}] channel names
//   GET fields?agentId=N&channel=C  → [{"label","value"}] dot-joined field keys
func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	u, err := url.Parse(req.URL)
	if err != nil {
		return sendJSON(sender, 400, map[string]string{"error": "bad resource URL"})
	}
	params := u.Query()

	switch strings.Trim(u.Path, "/") {
	case "channels":
		agentID, err := strconv.ParseUint(params.Get("agentId"), 10, 64)
		if err != nil {
			return sendJSON(sender, 400, map[string]string{"error": "agentId must be a numeric Doover agent ID"})
		}
		channels, err := d.client.ListChannels(ctx, agentID)
		if err != nil {
			return sendJSON(sender, 502, map[string]string{"error": err.Error()})
		}
		opts := make([]selectOption, 0, len(channels))
		for _, c := range channels {
			if !c.Archived {
				opts = append(opts, selectOption{Label: c.Name, Value: c.Name})
			}
		}
		return sendJSON(sender, 200, opts)

	case "fields":
		agentID, err := strconv.ParseUint(params.Get("agentId"), 10, 64)
		if err != nil {
			return sendJSON(sender, 400, map[string]string{"error": "agentId must be a numeric Doover agent ID"})
		}
		channel := params.Get("channel")
		if channel == "" {
			return sendJSON(sender, 400, map[string]string{"error": "channel is required"})
		}
		agg, err := d.client.FetchAggregate(ctx, agentID, channel)
		if err != nil {
			return sendJSON(sender, 502, map[string]string{"error": err.Error()})
		}
		keys := doover.FlattenKeys(agg.Data)
		opts := make([]selectOption, 0, len(keys))
		for _, k := range keys {
			opts = append(opts, selectOption{Label: k, Value: k})
		}
		return sendJSON(sender, 200, opts)
	}

	return sendJSON(sender, 404, map[string]string{"error": "unknown resource"})
}

type selectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func sendJSON(sender backend.CallResourceResponseSender, status int, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return sender.Send(&backend.CallResourceResponse{
		Status:  status,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    payload,
	})
}

func (d *Datasource) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if d.settings.ClientID == "" || d.settings.Secrets.ClientSecret == "" {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "Client ID and Client Secret are required",
		}, nil
	}
	if err := d.client.CheckAuth(ctx); err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("Authentication with Doover failed: %v", err),
		}, nil
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "Successfully authenticated with the Doover data API",
	}, nil
}
