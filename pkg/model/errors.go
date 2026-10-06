package model

import "fmt"

// RejectKind classifies why a raw record or a proposed feature version was
// rejected. Kinds are stable strings so that callers (and tests) can
// distinguish failure causes programmatically.
type RejectKind string

const (
	RejectMalformed      RejectKind = "malformed_record"         // structurally unparsable line / wrong column count
	RejectMissingField   RejectKind = "missing_field"            // required field absent or empty
	RejectBadCoordinate  RejectKind = "bad_coordinate"           // non-numeric or non-canonical coordinate
	RejectBadStrand      RejectKind = "bad_strand"               // strand token outside + - .
	RejectOutOfBounds    RejectKind = "coordinate_out_of_bounds" // interval invalid or past sequence length
	RejectBadAttribute   RejectKind = "bad_attribute"            // attribute section cannot be parsed
	RejectMissingID      RejectKind = "missing_id"               // feature has no stable identity
	RejectDuplicateID    RejectKind = "duplicate_id"             // same ID twice in one import batch
	RejectDanglingRef    RejectKind = "dangling_parent"          // Parent points at an unknown feature
	RejectCircularRef    RejectKind = "circular_reference"       // parent graph contains a cycle
	RejectTypeConflict   RejectKind = "type_conflict"            // same ID redefined with a different feature type
	RejectSeqConflict    RejectKind = "sequence_conflict"        // same sequence id defined inconsistently
	RejectVersionRegress RejectKind = "version_regression"       // proposed version not greater than current
	RejectSnapshot       RejectKind = "snapshot_error"           // index snapshot missing/incomplete/corrupt
	RejectSource         RejectKind = "source_error"             // source file missing/unreadable during rebuild
)

// RejectError carries a machine-readable kind together with human detail and
// the location of the offending record.
type RejectError struct {
	Kind   RejectKind
	URI    string
	Line   int
	Detail string
}

func (e *RejectError) Error() string {
	loc := e.URI
	if e.Line > 0 {
		loc = fmt.Sprintf("%s:%d", e.URI, e.Line)
	}
	if loc == "" {
		return string(e.Kind) + ": " + e.Detail
	}
	return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Detail, loc)
}

// Rejection is one rejected raw record, as reported by a parser/importer.
type Rejection struct {
	Kind   RejectKind `json:"kind"`
	URI    string     `json:"uri"`
	Line   int        `json:"line"`
	Detail string     `json:"detail"`
}

// Error makes Rejection satisfy error for convenience.
func (r Rejection) Error() string {
	e := RejectError{Kind: r.Kind, URI: r.URI, Line: r.Line, Detail: r.Detail}
	return e.Error()
}

func rejection(kind RejectKind, uri string, line int, format string, args ...any) Rejection {
	return Rejection{Kind: kind, URI: uri, Line: line, Detail: fmt.Sprintf(format, args...)}
}
