// Package coord defines the normalized zero-based half-open coordinate
// convention used by every package in this module and provides explicit
// conversions from the coordinate systems used by common annotation formats.
//
// # Internal convention
//
// All features stored in the index use:
//
//   - zero-based counting (the first base of a sequence has position 0);
//   - half-open intervals [Start, End), i.e. Start is inclusive, End exclusive;
//   - length = End - Start;
//   - End == 0 is permitted only for empty features rejected at parse time.
//
// Source conventions:
//
//   - GFF3 / GTF: one-based, closed interval [start, end]  -> [start-1, end);
//   - BED:         zero-based, half-open interval [start, end] (kept as-is);
//   - BED blocks:  blockStart/blockSize are relative to chromStart.
//
// Because every conversion funnels through the same helpers, mixed-format
// imports always end up in one identical coordinate system.
package coord

import "fmt"

// System describes how raw positions in a source file are counted.
type System int

const (
	// ZeroBasedHalfOpen is the BED convention: first base is 0 and the end
	// position is exclusive.
	ZeroBasedHalfOpen System = iota
	// OneBasedClosed is the GFF3/GTF convention: first base is 1 and both
	// endpoints are inclusive.
	OneBasedClosed
)

// Interval is the normalized interval representation.
type Interval struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// NewInterval builds a normalized interval and validates its shape.
func NewInterval(start, end int) (Interval, error) {
	if start < 0 || end < 0 {
		return Interval{}, fmt.Errorf("negative coordinate: [%d,%d)", start, end)
	}
	if end < start {
		return Interval{}, fmt.Errorf("end %d before start %d", end, start)
	}
	return Interval{Start: start, End: end}, nil
}

// Length returns the number of bases covered by the interval.
func (i Interval) Length() int { return i.End - i.Start }

// Empty reports whether the interval covers no bases.
func (i Interval) Empty() bool { return i.End == i.Start }

// Contains reports whether p lies inside the half-open interval.
func (i Interval) Contains(p int) bool { return p >= i.Start && p < i.End }

// Overlaps reports whether two half-open intervals share at least one base.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start < o.End && o.Start < i.End
}

// FromRaw converts a raw interval expressed in sys into the normalized
// convention. rawStart/rawEnd use the raw semantics of sys, including a
// closed end for OneBasedClosed.
func FromRaw(sys System, rawStart, rawEnd int) (Interval, error) {
	switch sys {
	case OneBasedClosed:
		if rawStart < 1 || rawEnd < 1 {
			return Interval{}, fmt.Errorf("1-based coordinate must be >= 1: [%d,%d]", rawStart, rawEnd)
		}
		if rawEnd < rawStart {
			return Interval{}, fmt.Errorf("end %d before start %d", rawEnd, rawStart)
		}
		return Interval{Start: rawStart - 1, End: rawEnd}, nil
	case ZeroBasedHalfOpen:
		return NewInterval(rawStart, rawEnd)
	default:
		return Interval{}, fmt.Errorf("unknown coordinate system %d", sys)
	}
}

// CheckAgainstSequence validates that i fits on a sequence of seqLen bases.
// A sequence length of 0 means the length is unknown and the check is
// skipped; callers that require a known length must validate separately.
func (i Interval) CheckAgainstSequence(seqLen int) error {
	if seqLen < 0 {
		return fmt.Errorf("negative sequence length %d", seqLen)
	}
	if seqLen == 0 {
		return nil
	}
	if i.End > seqLen {
		return fmt.Errorf("interval [start=%d,end=%d) exceeds sequence length %d", i.Start, i.End, seqLen)
	}
	return nil
}
