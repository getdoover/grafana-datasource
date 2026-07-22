package doover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	// MaxTimeseriesLimit is the server's hard cap on results per timeseries request.
	MaxTimeseriesLimit = 1500
	orgHeader          = "X-Doover-Organisation"
)

// StatusError is a non-2xx response from the Doover API.
type StatusError struct {
	Status int
	Path   string
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("doover API %s: status %d: %s", e.Path, e.Status, e.Body)
}

type Config struct {
	BaseURL      string // e.g. https://data.doover.com (no /api suffix)
	OrgID        string // optional; sent as X-Doover-Organisation
	ClientID     string
	ClientSecret string
}

// Client talks to the Doover data API using OAuth2 client-credentials.
// Token refresh is handled transparently; tokens live 1 hour.
type Client struct {
	baseURL     string
	orgID       string
	httpClient  *http.Client
	tokenSource oauth2.TokenSource
}

func NewClient(cfg Config) *Client {
	base := strings.TrimRight(cfg.BaseURL, "/")
	cc := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     base + "/api/oauth2/token",
		AuthStyle:    oauth2.AuthStyleInHeader, // HTTP Basic, as the server expects
	}
	ts := cc.TokenSource(context.Background())
	return &Client{
		baseURL:     base,
		orgID:       cfg.OrgID,
		httpClient:  oauth2.NewClient(context.Background(), ts),
		tokenSource: ts,
	}
}

// CheckAuth forces a token fetch, verifying credentials without touching data.
func (c *Client) CheckAuth(ctx context.Context) error {
	_, err := c.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	return nil
}

// DataPoint is one timeseries result. Value is the message payload flattened with
// dot-joined keys — an object when fields are projected, or a bare scalar.
type DataPoint struct {
	Value     json.RawMessage `json:"value"`
	MessageID uint64          `json:"message_id"`
}

type TimeseriesResponse struct {
	Count   int         `json:"count"`
	Results []DataPoint `json:"results"`
	// Next is the snowflake ID of the oldest returned message when more data
	// remains in the window; pass it as `before` to fetch the next page.
	Next *uint64 `json:"next"`
}

// FetchTimeseries queries one page of per-field history for a channel.
// before/after are snowflake IDs; at least one must be non-zero or the server 400s,
// and a missing bound is auto-filled to exactly one week from the other.
func (c *Client) FetchTimeseries(ctx context.Context, agentID uint64, channelName string, fieldNames []string, before, after uint64, limit int) (*TimeseriesResponse, error) {
	q := url.Values{}
	for _, f := range fieldNames {
		q.Add("field_name", f)
	}
	if before > 0 {
		q.Set("before", strconv.FormatUint(before, 10))
	}
	if after > 0 {
		q.Set("after", strconv.FormatUint(after, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	path := fmt.Sprintf("/api/agents/%d/channels/%s/messages/timeseries", agentID, url.PathEscape(channelName))
	var out TimeseriesResponse
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Channel is the subset of channel metadata the plugin needs for discovery.
type Channel struct {
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// ListChannels lists an agent's channels without aggregates or summaries —
// the cheap form, used to populate the query editor's channel picker.
// Note: the server returns all channels in one response (no pagination).
func (c *Client) ListChannels(ctx context.Context, agentID uint64) ([]Channel, error) {
	q := url.Values{}
	q.Set("include_aggregate", "false")
	q.Set("include_daily_summaries", "false")
	var out []Channel
	path := fmt.Sprintf("/api/agents/%d/channels", agentID)
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Aggregate is a channel's current merged state. Wire shape (from the server's
// ChannelAggregate struct): {data, attachments, last_updated?} where
// last_updated is unix milliseconds.
type Aggregate struct {
	Data          json.RawMessage `json:"data"`
	LastUpdatedMs *int64          `json:"last_updated"`
}

// LastUpdated returns the aggregate's update time, or zero time if unknown.
func (a *Aggregate) LastUpdated() time.Time {
	if a.LastUpdatedMs == nil {
		return time.Time{}
	}
	return time.UnixMilli(*a.LastUpdatedMs).UTC()
}

// FetchAggregate returns a channel's current merged state (latest value per key).
// This is the cheapest read — no history walk — used for the `latest` query type
// and for field-name discovery.
func (c *Client) FetchAggregate(ctx context.Context, agentID uint64, channelName string) (*Aggregate, error) {
	var out Aggregate
	path := fmt.Sprintf("/api/agents/%d/channels/%s/aggregate", agentID, url.PathEscape(channelName))
	if err := c.getJSON(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentAggregate is one row of a multi-agent aggregate response: the aggregate
// fields plus the owning agent's ID.
type AgentAggregate struct {
	AgentID       uint64          `json:"agent_id"`
	Data          json.RawMessage `json:"data"`
	LastUpdatedMs *int64          `json:"last_updated"`
}

func (a *AgentAggregate) LastUpdated() time.Time {
	if a.LastUpdatedMs == nil {
		return time.Time{}
	}
	return time.UnixMilli(*a.LastUpdatedMs).UTC()
}

type BatchAggregateResponse struct {
	Count   int              `json:"count"`
	Results []AgentAggregate `json:"results"`
}

// FetchMultiAgentAggregates returns the current value of one channel across many
// agents — the cheap fleet read (no history walk). Server cap: 500 agents.
func (c *Client) FetchMultiAgentAggregates(ctx context.Context, channelName string, agentIDs []uint64) (*BatchAggregateResponse, error) {
	q := url.Values{}
	for _, id := range agentIDs {
		q.Add("agent_id", strconv.FormatUint(id, 10))
	}
	var out BatchAggregateResponse
	path := fmt.Sprintf("/api/agents/channels/%s/aggregates", url.PathEscape(channelName))
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BatchMessage is one message in a multi-agent response. Unlike the timeseries
// endpoint, Data is the raw (nested, unflattened) message payload; the owning
// agent is identified by Channel.AgentID. The timestamp is encoded in ID.
type BatchMessage struct {
	ID      uint64 `json:"id"`
	Channel struct {
		AgentID uint64 `json:"agent_id"`
		Name    string `json:"name"`
	} `json:"channel"`
	Data json.RawMessage `json:"data"`
}

type BatchMessageResponse struct {
	Count   int            `json:"count"`
	Results []BatchMessage `json:"results"`
	Next    *uint64        `json:"next"`
	// NextCursors maps agent ID (as a decimal string) to the oldest returned
	// snowflake for agents that hit their per-agent limit; the modern pagination
	// signal (Next is legacy-compat).
	NextCursors map[string]uint64 `json:"next_cursors"`
}

// FetchMultiAgentMessages queries one channel's history across many agents.
// Server caps: 500 agents, per-agent limit max 1500, 50k total messages, and the
// before/after window must span at most 7 days or the request 400s.
func (c *Client) FetchMultiAgentMessages(ctx context.Context, channelName string, agentIDs []uint64, fieldNames []string, before, after uint64, perAgentLimit int) (*BatchMessageResponse, error) {
	q := url.Values{}
	for _, id := range agentIDs {
		q.Add("agent_id", strconv.FormatUint(id, 10))
	}
	for _, f := range fieldNames {
		q.Add("field_name", f)
	}
	if before > 0 {
		q.Set("before", strconv.FormatUint(before, 10))
	}
	if after > 0 {
		q.Set("after", strconv.FormatUint(after, 10))
	}
	if perAgentLimit > 0 {
		q.Set("agent_message_limit", strconv.Itoa(perAgentLimit))
	}
	var out BatchMessageResponse
	path := fmt.Sprintf("/api/agents/channels/%s/messages", url.PathEscape(channelName))
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.orgID != "" {
		req.Header.Set(orgHeader, c.orgID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{Status: resp.StatusCode, Path: path, Body: strings.TrimSpace(string(body))}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("doover API %s: decoding response: %w", path, err)
	}
	return nil
}
