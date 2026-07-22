package plugin

import (
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }

func TestDownsample(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// 6 points over 5 minutes into 2 buckets of 2.5 minutes.
	s := series{values: map[string][]*float64{"a": {}}}
	for i, v := range []float64{1, 2, 3, 10, 20, 30} {
		s.times = append(s.times, base.Add(time.Duration(i)*time.Minute))
		s.values["a"] = append(s.values["a"], fp(v))
	}

	got := downsample(s, []string{"a"}, 2)

	if len(got.times) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(got.times))
	}
	// Bucket 0: minutes 0,1,2 → values 1,2,3.
	if m := got.mean["a"][0]; m == nil || *m != 2 {
		t.Errorf("bucket 0 mean = %v, want 2", m)
	}
	if v := got.min["a"][0]; *v != 1 {
		t.Errorf("bucket 0 min = %v, want 1", *v)
	}
	// Bucket 1: minutes 3,4,5 → values 10,20,30 (minute 5 is the range end boundary).
	if m := got.mean["a"][1]; m == nil || *m != 20 {
		t.Errorf("bucket 1 mean = %v, want 20", m)
	}
	if v := got.max_["a"][1]; *v != 30 {
		t.Errorf("bucket 1 max = %v, want 30", *v)
	}
}

func TestDownsampleSparseField(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	s := series{
		times:  []time.Time{base, base.Add(time.Hour)},
		values: map[string][]*float64{"a": {fp(5), nil}},
	}
	got := downsample(s, []string{"a"}, 2)
	if got.mean["a"][0] == nil || *got.mean["a"][0] != 5 {
		t.Errorf("bucket with data should aggregate, got %v", got.mean["a"][0])
	}
	if got.mean["a"][1] != nil {
		t.Errorf("bucket with only-nil values should stay nil")
	}
}

func TestDownsampleEmpty(t *testing.T) {
	got := downsample(series{}, []string{"a"}, 100)
	if len(got.times) != 0 {
		t.Errorf("empty series should produce no buckets")
	}
}
