package plugin

import "time"

// series holds extracted, time-aligned rows: one timestamp per row and one
// nullable value column per field name.
type series struct {
	times  []time.Time
	values map[string][]*float64
}

// aggregated is a downsampled series: per field, the per-bucket mean plus the
// min/max envelope so panels can show the spread the mean hides.
type aggregated struct {
	times           []time.Time
	mean, min, max_ map[string][]*float64
}

// downsample buckets s into at most maxPoints equal time slices. The server has
// no aggregation of its own, so this is the only thing standing between a dense
// channel over a wide range and thousands of raw points per panel.
// Buckets with no data are dropped rather than emitted as gaps.
func downsample(s series, fields []string, maxPoints int) aggregated {
	out := aggregated{
		mean: make(map[string][]*float64, len(fields)),
		min:  make(map[string][]*float64, len(fields)),
		max_: make(map[string][]*float64, len(fields)),
	}
	n := len(s.times)
	if n == 0 || maxPoints < 1 {
		return out
	}

	start := s.times[0]
	span := s.times[n-1].Sub(start)
	bucketWidth := span / time.Duration(maxPoints)
	if bucketWidth <= 0 {
		bucketWidth = time.Millisecond
	}

	bucketOf := func(t time.Time) int {
		b := int(t.Sub(start) / bucketWidth)
		if b >= maxPoints {
			b = maxPoints - 1 // the range end lands exactly on the boundary
		}
		return b
	}

	type acc struct {
		sum, min, max float64
		count         int
	}

	i := 0
	for i < n {
		b := bucketOf(s.times[i])
		accs := make(map[string]*acc, len(fields))
		j := i
		for ; j < n && bucketOf(s.times[j]) == b; j++ {
			for _, f := range fields {
				v := s.values[f][j]
				if v == nil {
					continue
				}
				a := accs[f]
				if a == nil {
					accs[f] = &acc{sum: *v, min: *v, max: *v, count: 1}
					continue
				}
				a.sum += *v
				a.count++
				if *v < a.min {
					a.min = *v
				}
				if *v > a.max {
					a.max = *v
				}
			}
		}

		out.times = append(out.times, start.Add(bucketWidth*time.Duration(b)))
		for _, f := range fields {
			if a := accs[f]; a != nil {
				mean := a.sum / float64(a.count)
				mn, mx := a.min, a.max
				out.mean[f] = append(out.mean[f], &mean)
				out.min[f] = append(out.min[f], &mn)
				out.max_[f] = append(out.max_[f], &mx)
			} else {
				out.mean[f] = append(out.mean[f], nil)
				out.min[f] = append(out.min[f], nil)
				out.max_[f] = append(out.max_[f], nil)
			}
		}
		i = j
	}
	return out
}
