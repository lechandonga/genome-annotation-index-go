package parse

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/model"
)

// Format identifiers.
const (
	FormatGFF3 = "gff3"
	FormatGTF  = "gtf"
	FormatBED  = "bed"
	FormatFA   = "fasta"
)

// Result is the outcome of parsing one annotation file.
type Result struct {
	Format   string
	File     string
	Features []model.Feature
	Rejects  Rejects
}

// SniffFormat determines the annotation format from file content and name.
// Content takes precedence (##gff-version, typical GTF attributes); the
// extension is the fallback.
func SniffFormat(r io.Reader, name string) (string, io.Reader, error) {
	br := bufio.NewReader(r)
	head, _ := br.Peek(4096)
	rest := io.MultiReader(br, r)
	text := string(head)

	low := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.Contains(text, "##gff-version"):
		return FormatGFF3, rest, nil
	case strings.Contains(text, "gene_id"):
		return FormatGTF, rest, nil
	case low == ".gff" || low == ".gff3":
		return FormatGFF3, rest, nil
	case low == ".gtf":
		return FormatGTF, rest, nil
	case low == ".bed":
		return FormatBED, rest, nil
	case low == ".fa" || low == ".fasta" || low == ".fna":
		return FormatFA, rest, nil
	}
	// Last resort: first non-comment line of a BED file has 3-12 columns and
	// no '='/'gene_id'; GTF has 9 columns and quoted attributes.
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Count(line, "\t") >= 8 && strings.Contains(line, "\"") {
			return FormatGTF, rest, nil
		}
		if n := len(strings.Fields(line)); n >= 3 && n <= 12 {
			return FormatBED, rest, nil
		}
		break
	}
	return "", rest, fmt.Errorf("%s: %w", name, ErrUnknownFormat)
}

// ErrUnknownFormat is returned when a file's format cannot be determined.
var ErrUnknownFormat = fmt.Errorf("%s", ReasonUnsupportedFormat)

// ParseFile parses an annotation file, detecting its format.
func ParseFile(path string, opts Options) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	format, reader, err := SniffFormat(f, path)
	if err != nil {
		return nil, err
	}
	if format == FormatFA {
		return nil, fmt.Errorf("%s is a FASTA reference; load it with ParseFASTAFile", path)
	}
	return ParseReader(format, reader, filepath.Base(path), opts)
}

// ParseReader parses annotations from an explicit format.
func ParseReader(format string, r io.Reader, file string, opts Options) (*Result, error) {
	res := &Result{Format: format, File: file}
	var err error
	switch format {
	case FormatGFF3:
		res.Features, res.Rejects, err = ParseGFF3(r, file, opts)
	case FormatGTF:
		res.Features, res.Rejects, err = ParseGTF(r, file, opts)
	case FormatBED:
		res.Features, res.Rejects, err = ParseBED(r, file, opts)
	default:
		return nil, reject(format, file, 0, ReasonUnsupportedFormat, "unknown format %q", format)
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ParseFASTAFile loads reference sequence lengths from a FASTA file.
func ParseFASTAFile(path string) (SequenceSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	seqs, _, err := ParseFASTA(f, filepath.Base(path))
	return seqs, err
}
