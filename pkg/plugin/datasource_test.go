package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"github.com/getdoover/doover/pkg/doover"
)

func TestQueryDataRespondsPerQuery(t *testing.T) {
	ds := Datasource{}

	resp, err := ds.QueryData(
		context.Background(),
		&backend.QueryDataRequest{
			Queries: []backend.DataQuery{
				{RefID: "A", JSON: []byte(`{"agentId":"not-a-number","channelName":"c","fieldNames":"f"}`)},
			},
		},
	)
	if err != nil {
		t.Error(err)
	}
	if len(resp.Responses) != 1 {
		t.Fatal("QueryData must return a response per query")
	}
	if resp.Responses["A"].Error == nil {
		t.Error("invalid agent ID should produce an error response")
	}
}

func TestExtractSeriesAndRawFrame(t *testing.T) {
	points := []doover.DataPoint{
		// Deliberately out of order; message IDs encode the timestamps.
		{MessageID: 2 << 22, Value: json.RawMessage(`{"a":2,"b":20}`)},
		{MessageID: 1 << 22, Value: json.RawMessage(`{"a":1}`)},
		{MessageID: 3 << 22, Value: json.RawMessage(`{"b":"30.5"}`)},
		{MessageID: 4 << 22, Value: json.RawMessage(`{"c":"not requested"}`)}, // dropped: nothing numeric for a/b
	}

	s := extractSeries([]string{"a", "b"}, points)
	frame := rawFrame("tag_values", []string{"a", "b"}, s)

	if len(frame.Fields) != 3 {
		t.Fatalf("expected time + 2 value fields, got %d", len(frame.Fields))
	}
	if frame.Fields[0].Len() != 3 {
		t.Fatalf("expected 3 rows (one dropped), got %d", frame.Fields[0].Len())
	}
	// Row 0 is the oldest (message ID 1<<22): a=1, b=nil.
	if v := frame.Fields[1].At(0).(*float64); v == nil || *v != 1 {
		t.Errorf("row 0 field a = %v, want 1", v)
	}
	if v := frame.Fields[2].At(0).(*float64); v != nil {
		t.Errorf("row 0 field b = %v, want nil", *v)
	}
	// Row 2 (message ID 3<<22): b parsed from numeric string.
	if v := frame.Fields[2].At(2).(*float64); v == nil || *v != 30.5 {
		t.Errorf("row 2 field b = %v, want 30.5", v)
	}
}

func TestExtractRowFromPayloadScalar(t *testing.T) {
	row := extractRowFromPayload(json.RawMessage(`42.5`), []string{"level"})
	if row == nil || row["level"] == nil || *row["level"] != 42.5 {
		t.Errorf("scalar payload should map to the single requested field, got %v", row)
	}
	if extractRowFromPayload(json.RawMessage(`42.5`), []string{"a", "b"}) != nil {
		t.Error("scalar payload with multiple fields is ambiguous and should be dropped")
	}
}

func TestFlattenAggregate(t *testing.T) {
	flat := flattenAggregate(json.RawMessage(`{"battery":{"voltage":12.4},"ok":true}`))
	if v, ok := flat["battery.voltage"].(float64); !ok || v != 12.4 {
		t.Errorf("nested key should flatten to dot-joined path, got %v", flat)
	}
	if _, ok := flat["ok"]; !ok {
		t.Errorf("top-level key missing from flattened aggregate: %v", flat)
	}
}
