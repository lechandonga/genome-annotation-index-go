// Package model defines the unified feature model shared by all parsers,
// the registry and the interval index.
//
// # Coordinate convention
//
// Every feature stored in the system is normalized to a 0-based,
// half-open interval [Start, End) on the forward strand coordinate space.
//
// Parsers are responsible for converting source-specific conventions:
//
//   - GFF3/GTF: 1-based, closed [start, end] -> [start-1, end)
//   - BED:       0-based, half-open [start, end) -> kept as-is
//
// Strand is one of "+", "-" or "." (unstranded). Feature coordinates always
// refer to the forward strand (i.e. the leftmost/lowest genome position comes
// first regardless of feature strand); strand only affects direction-aware
// queries and traversal semantics.
package model

import "fmt"

// Strand values used inside the unified model.
const (
	StrandPlus    = "+"
	StrandMinus   = "-"
	StrandUnknown = "."
)

// FeatureType enumerates the supported hierarchical feature classes.
// Raw types that are not recognized (e.g. "TF_binding_site") are kept as
// FeatureOther and do not participate in parent/child inference, but they
// are still stored and queryable.
type FeatureType string

const (
	FeatureGene       FeatureType = "gene"
	FeatureTranscript FeatureType = "transcript"
	FeatureExon       FeatureType = "exon"
	FeatureCDS        FeatureType = "CDS"
	FeatureSequence   FeatureType = "sequence" // a reference sequence record
	FeatureOther      FeatureType = "other"
)

// Sequence describes a reference sequence. Only identity, length and a
// SHA-256 checksum are indexed; the raw bases are not retained in the index
// (they stay in the source FASTA file).
type Sequence struct {
	ID     string
	Length int
	SHA256 string
	Source string // origin tag, e.g. file path / source name
	URI    string // absolute file URI the sequence was read from
}

// Feature is the unified annotation record.
type Feature struct {
	ID      string
	Version int
	Type    FeatureType
	SeqID   string // reference sequence / chromosome id
	Start   int    // 0-based inclusive
	End     int    // 0-based exclusive
	Strand  string
	Parents []string
	Source  string // logical source name
	URI     string // file URI this feature originated from
	Line    int    // 1-based line number within the source file (0 if N/A)
	RawType string // original type token from the source format
	Attrs   map[string]string
}

// Key returns the stable identity of the feature independent of version.
func (f Feature) Key() string { return f.ID }

func (f Feature) String() string {
	return fmt.Sprintf("%s v%d %s %s:[%d,%d) %s", f.ID, f.Version, f.Type, f.SeqID, f.Start, f.End, f.Strand)
}

// Overlaps reports whether the feature intersects the half-open [start,end).
func (f Feature) Overlaps(start, end int) bool {
	return f.Start < end && start < f.End
}
