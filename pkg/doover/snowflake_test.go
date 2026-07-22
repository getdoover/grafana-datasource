package doover

import (
	"testing"
	"time"
)

// Vectors generated with pydoover (pydoover.utils.snowflake, DOOVER_EPOCH=1735689600000)
// using the server's pure-shift form: (unix_ms - DOOVER_EPOCH) << 22.
var vectors = []struct {
	iso       string
	snowflake uint64
}{
	{"2025-01-01T00:00:00Z", 0},
	{"2025-06-15T12:34:56.789Z", 59983986327289856},
	{"2026-07-22T10:00:00Z", 205624914739200000},
}

func TestSnowflakeFromTime(t *testing.T) {
	for _, v := range vectors {
		ts, err := time.Parse(time.RFC3339, v.iso)
		if err != nil {
			t.Fatal(err)
		}
		if got := SnowflakeFromTime(ts); got != v.snowflake {
			t.Errorf("SnowflakeFromTime(%s) = %d, want %d", v.iso, got, v.snowflake)
		}
	}
}

func TestTimeFromSnowflake(t *testing.T) {
	for _, v := range vectors {
		want, err := time.Parse(time.RFC3339, v.iso)
		if err != nil {
			t.Fatal(err)
		}
		if got := TimeFromSnowflake(v.snowflake); !got.Equal(want) {
			t.Errorf("TimeFromSnowflake(%d) = %s, want %s", v.snowflake, got, want)
		}
	}
}

func TestRoundTripIgnoresLowBits(t *testing.T) {
	ts := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	id := SnowflakeFromTime(ts) | 0x3FFFFF // fill region/instance/type/random bits
	if got := TimeFromSnowflake(id); !got.Equal(ts) {
		t.Errorf("round trip with low bits set = %s, want %s", got, ts)
	}
}

func TestPreEpochClampsToZero(t *testing.T) {
	if got := SnowflakeFromTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)); got != 0 {
		t.Errorf("pre-epoch time = %d, want 0", got)
	}
}
