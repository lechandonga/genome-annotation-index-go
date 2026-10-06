// Package parse turns annotation files (GFF3, GTF, BED) and reference
// sequences (FASTA) into normalized model.Feature values.
//
// Every malformed, incomplete or out-of-range record is reported as a
// Reject with a machine-readable Reason code; records are never silently
// skipped.
package parse

import (
	"errors"
	"fmt"
)

// Reason is a stable, machine-readable rejection category.
type Reason string

const (
	ReasonSyntax              Reason = "syntax_error"
	ReasonMissingField        Reason = "missing_field"
	ReasonBadNumber           Reason = "bad_number"
	ReasonBadCoordinateSystem Reason = "bad_coordinate_system"
	ReasonOutOfRange          Reason = "out_of_range"
	ReasonBadStrand           Reason = "bad_strand"
	ReasonBadPhase            Reason = "bad_phase"
	ReasonUnknownSeqID        Reason = "unknown_reference"
	ReasonEmptyFeature        Reason = "empty_feature"
	ReasonUnsupportedFormat   Reason = "unsupported_format"
	ReasonDuplicateRecord     Reason = "duplicate_record"
	ReasonBlockInvalid        Reason = "invalid_block"
	ReasonBadHeader           Reason = "bad_header"
)

// Reject describes exactly why one source record could not be normalized.
type Reject struct {
	Format  string
	File    string
	Line    int
	Reason  Reason
	Message string
}

func (r *Reject) Error() string {
	return fmt.Sprintf("%s:%d: %s: %s", r.File, r.Line, r.Reason, r.Message)
}

// AsReject extracts a *Reject from err if present.
func AsReject(err error) (*Reject, bool) {
	var r *Reject
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// Rejects is a collection of per-record rejections returned by a parser.
type Rejects []*Reject

func (rs Rejects) Error() string {
	if len(rs) == 0 {
		return ""
	}
	return fmt.Sprintf("%d record(s) rejected, first: %s", len(rs), rs[0].Error())
}

// Reasons groups rejections by reason code, useful for explainable reports.
func (rs Rejects) Reasons() map[Reason]int {
	out := make(map[Reason]int)
	for _, r := range rs {
		out[r.Reason]++
	}
	return out
}

func reject(format, file string, line int, reason Reason, formatMsg string, args ...any) *Reject {
	return &Reject{
		Format:  format,
		File:    file,
		Line:    line,
		Reason:  reason,
		Message: fmt.Sprintf(formatMsg, args...),
	}
}
