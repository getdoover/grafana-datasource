package plugin

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChunkWindows(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour

	// 16 days → 3 windows, newest first, oldest window partial.
	ws := chunkWindows(base, base.Add(16*24*time.Hour), week)
	if len(ws) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(ws))
	}
	if !ws[0].to.Equal(base.Add(16 * 24 * time.Hour)) {
		t.Errorf("first window should end at the range end")
	}
	if !ws[2].from.Equal(base) {
		t.Errorf("last window should start at the range start")
	}
	for _, w := range ws {
		if w.to.Sub(w.from) > week {
			t.Errorf("window %v exceeds a week", w)
		}
	}

	// Sub-week range → one window.
	if got := len(chunkWindows(base, base.Add(time.Hour), week)); got != 1 {
		t.Errorf("sub-week range should be one window, got %d", got)
	}
	// Empty/inverted range → none.
	if got := len(chunkWindows(base, base, week)); got != 0 {
		t.Errorf("empty range should have no windows, got %d", got)
	}
}

func TestLookupPath(t *testing.T) {
	nested := json.RawMessage(`{"battery": {"voltage": 12.4}}`)
	if v, ok := lookupPath(nested, "battery.voltage"); !ok || v.(float64) != 12.4 {
		t.Errorf("nested walk failed: %v %v", v, ok)
	}
	flat := json.RawMessage(`{"battery.voltage": 12.4}`)
	if v, ok := lookupPath(flat, "battery.voltage"); !ok || v.(float64) != 12.4 {
		t.Errorf("flat key lookup failed: %v %v", v, ok)
	}
	if _, ok := lookupPath(nested, "battery.current"); ok {
		t.Error("missing path should not resolve")
	}
	if _, ok := lookupPath(json.RawMessage(`"scalar"`), "x"); ok {
		t.Error("non-object payload should not resolve")
	}
}

func TestParseAgentIDs(t *testing.T) {
	qm := queryModel{AgentIDs: " 123, 456 ,"}
	ids, err := qm.parseAgentIDs()
	if err != nil || len(ids) != 2 || ids[0] != 123 || ids[1] != 456 {
		t.Errorf("parseAgentIDs = %v, %v", ids, err)
	}
	if _, err := (&queryModel{AgentIDs: "12x"}).parseAgentIDs(); err == nil {
		t.Error("bad agent ID should error")
	}
	if _, err := (&queryModel{AgentIDs: ""}).parseAgentIDs(); err == nil {
		t.Error("empty agent list should error")
	}
}
