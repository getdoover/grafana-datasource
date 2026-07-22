package doover

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFlattenKeys(t *testing.T) {
	raw := json.RawMessage(`{
		"battery": {"voltage": 12.4, "soc": 88},
		"pump_running": true,
		"site": "north bore",
		"empty_obj": {},
		"arr": [1, 2]
	}`)
	got := FlattenKeys(raw)
	want := []string{"arr", "battery.soc", "battery.voltage", "empty_obj", "pump_running", "site"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FlattenKeys = %v, want %v", got, want)
	}
}

func TestFlattenKeysNonObject(t *testing.T) {
	if got := FlattenKeys(json.RawMessage(`42`)); got != nil {
		t.Errorf("scalar aggregate should yield no keys, got %v", got)
	}
}
