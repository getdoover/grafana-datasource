package doover

import (
	"encoding/json"
	"sort"
)

// FlattenKeys returns the dot-joined leaf keys of a JSON object, sorted.
// This mirrors the server's message flattening (flatten_series dot-joins nested
// keys), so keys discovered from a channel aggregate address the same paths the
// timeseries endpoint's field_name parameter expects.
func FlattenKeys(raw json.RawMessage) []string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	var keys []string
	walkKeys("", obj, &keys)
	sort.Strings(keys)
	return keys
}

func walkKeys(prefix string, obj map[string]any, out *[]string) {
	for k, v := range obj {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok && len(child) > 0 {
			walkKeys(key, child, out)
		} else {
			*out = append(*out, key)
		}
	}
}
