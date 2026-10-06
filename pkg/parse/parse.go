// Package parse implements streaming parsers for the supported annotation
// and reference-sequence formats and normalizes them into model.Feature /
// model.Sequence values.
//
// Parsers never panic on bad input and never silently drop records: every
// unparseable or invalid row becomes a model.Rejection carrying a stable
// RejectKind, the source URI and the 1-based line number.
package parse

import (
	"io"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// Result is the output of parsing one source file.
type Result struct {
	Features   []model.Feature
	Sequences  []model.Sequence
	Rejections []model.Rejection
}

// Format identifies a supported file format.
type Format string

const (
	FormatGFF3  Format = "gff3"
	FormatGTF   Format = "gtf"
	FormatBED   Format = "bed"
	FormatFASTA Format = "fasta"
)

// Options configures a parser invocation.
type Options struct {
	// Format is the explicit format. When empty it is detected from the
	// file name / content sniffing.
	Format Format
	// URI identifies the source for diagnostics and snapshot rebuild.
	URI string
	// Source is the logical source label stored on every record.
	Source string
}

// Parser parses a single byte stream.
type Parser func(r io.Reader, opts Options) Result

// Detect picks a format from a file name. Returns "" when unknown.
func Detect(name string) Format {
	n := name
	for i := 0; i < len(n); i++ {
		if n[i] == '.' {
			ext := n[i:]
			switch ext {
			case ".gff3", ".gff":
				return FormatGFF3
			case ".gtf":
				return FormatGTF
			case ".bed":
				return FormatBED
			case ".fa", ".fasta", ".fna", ".seq":
				return FormatFASTA
			}
		}
	}
	return ""
}

// Parse dispatches to the parser for opts.Format.
func Parse(r io.Reader, opts Options) Result {
	switch opts.Format {
	case FormatGFF3:
		return ParseGFF3(r, opts)
	case FormatGTF:
		return ParseGTF(r, opts)
	case FormatBED:
		return ParseBED(r, opts)
	case FormatFASTA:
		return ParseFASTA(r, opts)
	default:
		return Result{Rejections: []model.Rejection{{
			Kind: model.RejectMalformed, URI: opts.URI,
			Detail: "unknown or unsupported format",
		}}}
	}
}
