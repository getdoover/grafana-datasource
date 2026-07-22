// Package doover is a minimal client for the Doover data API (data.doover.com).
package doover

import "time"

// DooverEpochMs is the Doover snowflake epoch: 2025-01-01T00:00:00Z in unix milliseconds.
// Snowflake IDs encode a timestamp as milliseconds-since-this-epoch shifted left 22 bits;
// the low 22 bits carry region/instance/type/random data.
//
// The API's before/after parameters accept ONLY snowflake integers. Plain unix-ms values
// are silently misread as timestamps near 2025-01-01, so all conversions must go through
// these helpers.
const DooverEpochMs = 1735689600000

const timestampShift = 22

// SnowflakeFromTime converts a time to the smallest snowflake ID with that millisecond
// timestamp (low 22 bits zero). Suitable for before/after query bounds.
// Times at or before the Doover epoch return 0.
func SnowflakeFromTime(t time.Time) uint64 {
	ms := t.UnixMilli() - DooverEpochMs
	if ms <= 0 {
		return 0
	}
	return uint64(ms) << timestampShift
}

// TimeFromSnowflake extracts the millisecond timestamp encoded in a snowflake ID.
func TimeFromSnowflake(id uint64) time.Time {
	ms := int64(id>>timestampShift) + DooverEpochMs
	return time.UnixMilli(ms).UTC()
}
