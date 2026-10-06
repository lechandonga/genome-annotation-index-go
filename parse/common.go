package parse

import (
	"strconv"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/model"
)

// Options controls parser behavior.
type Options struct {
	// SourceName is the logical source label stored on every feature.
	SourceName string
	// Sequences, when non-nil, enables reference-bound validation: features
	// on unknown sequences or past sequence ends are rejected.
	Sequences SequenceSet
}

// unquoteGTFAttr removes surrounding double quotes from a GTF attribute value.
func unquoteGTFAttr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func atoiField(format, file string, lineNo int, raw, field string) (int, *Reject) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, reject(format, file, lineNo, ReasonBadNumber,
			"field %s is not an integer: %q", field, raw)
	}
	return v, nil
}

// validateAndBuild runs the checks common to every format and produces a
// model.Feature. Coordinate conversion itself happens in the format parser
// because the systems differ.
func validateAndBuild(format string, f *model.Feature, seqs SequenceSet) *Reject {
	if f.SeqID == "" {
		return reject(format, f.Source.File, f.Line, ReasonMissingField, "empty reference sequence id")
	}
	if f.ID == "" {
		return reject(format, f.Source.File, f.Line, ReasonMissingField, "empty feature id")
	}
	if f.Interval.Empty() {
		return reject(format, f.Source.File, f.Line, ReasonEmptyFeature,
			"feature %s has zero length", f.ID)
	}
	if seqs != nil {
		length, ok := seqs[f.SeqID]
		if !ok {
			return reject(format, f.Source.File, f.Line, ReasonUnknownSeqID,
				"feature %s references unknown sequence %q", f.ID, f.SeqID)
		}
		if err := f.Interval.CheckAgainstSequence(length); err != nil {
			return reject(format, f.Source.File, f.Line, ReasonOutOfRange,
				"feature %s: %v", f.ID, err)
		}
	}
	return nil
}

// addReject appends only non-nil rejects and reports whether it did.
func addReject(rs *Rejects, r *Reject) bool {
	if r == nil {
		return false
	}
	*rs = append(*rs, r)
	return true
}
