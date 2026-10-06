// Package model defines the normalized feature model that every parser
// produces and the graph/index layers consume.
package model

import (
	"fmt"

	"github.com/lechandonga/genome-annotation-index-go/coord"
)

// FeatureType is the biological kind of a feature.
type FeatureType string

const (
	Gene       FeatureType = "gene"
	Transcript FeatureType = "transcript"
	Exon       FeatureType = "exon"
	CDS        FeatureType = "CDS"
	Other      FeatureType = "other"
)

// Strand is the strand of a feature.
type Strand string

const (
	Forward    Strand = "+"
	Reverse    Strand = "-"
	Unstranded Strand = "."
)

// ParseStrand normalizes raw strand tokens.
func ParseStrand(s string) (Strand, error) {
	switch Strand(s) {
	case Forward, Reverse, "":
		if s == "" {
			return Unstranded, nil
		}
		return Strand(s), nil
	case Unstranded, "?":
		return Unstranded, nil
	default:
		return "", fmt.Errorf("invalid strand %q", s)
	}
}

// Source identifies where an annotation came from. It is stored on every
// feature so query results are explainable.
type Source struct {
	// Name identifies the logical data source (e.g. "ensembl", file stem).
	Name string `json:"name"`
	// Format is the parsed format: gff3 | gtf | bed.
	Format string `json:"format"`
	// File is the actual file name used during import.
	File string `json:"file"`
}

// Feature is the unified representation of one annotation record.
type Feature struct {
	// ID is the stable identity of the feature (GFF3 ID / GTF gene_id etc. /
	// BED name, or a deterministic synthetic id when the format has none).
	ID string
	// Type is the normalized biological type.
	Type FeatureType
	// SeqID is the reference sequence name (chromosome/contig).
	SeqID string
	// Strand of the feature.
	Strand Strand
	// Interval in normalized zero-based half-open coordinates.
	Interval coord.Interval
	// ParentIDs lists parent features declared by the source record
	// (GFF3 Parent / derived GTF hierarchy / BED block relationship).
	ParentIDs []string
	// Version is the declared version of the feature; empty means
	// unversioned. Versions are compared per the rules in the graph package.
	Version string
	// Attrs retains format-specific attributes for traceability.
	Attrs map[string]string
	// Source records where the feature was parsed from.
	Source Source
	// Line is the 1-based record line in the source file.
	Line int
}

// FeatureRef is a compact pointer to a feature returned by queries.
type FeatureRef struct {
	ID      string            `json:"id"`
	Type    FeatureType       `json:"type"`
	SeqID   string            `json:"seq_id"`
	Strand  Strand            `json:"strand"`
	Start   int               `json:"start"`
	End     int               `json:"end"`
	Version string            `json:"version,omitempty"`
	Parents []string          `json:"parents,omitempty"`
	Source  Source            `json:"source"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// Ref converts a Feature into a FeatureRef.
func (f *Feature) Ref() FeatureRef {
	parents := append([]string(nil), f.ParentIDs...)
	return FeatureRef{
		ID:      f.ID,
		Type:    f.Type,
		SeqID:   f.SeqID,
		Strand:  f.Strand,
		Start:   f.Interval.Start,
		End:     f.Interval.End,
		Version: f.Version,
		Parents: parents,
		Source:  f.Source,
		Attrs:   f.Attrs,
	}
}
