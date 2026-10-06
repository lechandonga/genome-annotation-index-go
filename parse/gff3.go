package parse

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

// ParseGFF3 parses GFF3 (https://github.com/The-Sequence-Ontology/Specifications/blob/master/gff3.md).
//
// Coordinates are 1-based closed and converted to [start-1, end). Feature
// identity comes from the ID attribute; records without an ID receive a
// deterministic synthetic id of the form
//
//	"<source>#line<n>:<type>:<seqid>:<start>:<end>"
//
// so that re-importing identical input yields identical ids (documented in
// docs). Parent and Name are taken from the attribute column; version is
// taken from an optional "version" attribute.
func ParseGFF3(r io.Reader, file string, opts Options) ([]model.Feature, Rejects, error) {
	br := bufio.NewScanner(r)
	br.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var features []model.Feature
	var rejects Rejects
	seenHeader := false
	lineNo := 0
	sourceName := opts.SourceName
	if sourceName == "" {
		sourceName = file
	}

	for br.Scan() {
		lineNo++
		line := strings.TrimRight(br.Text(), "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "##") {
			if lineNo == 1 && line == "##gff-version 3" {
				seenHeader = true
			}
			if strings.HasPrefix(line, "##gff-version") {
				seenHeader = true
				if !strings.HasPrefix(strings.TrimSpace(line), "##gff-version 3") {
					return nil, nil, reject("gff3", file, lineNo, ReasonUnsupportedFormat,
						"only GFF version 3 is supported, got %q", strings.TrimSpace(line))
				}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		f, rec := parseGFF3Line(line, file, lineNo, sourceName, opts.Sequences)
		if rec != nil {
			rejects = append(rejects, rec)
			continue
		}
		features = append(features, f)
	}
	if err := br.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if !seenHeader {
		return nil, nil, reject("gff3", file, lineNo, ReasonBadHeader,
			"missing ##gff-version 3 header")
	}
	return features, rejects, nil
}

func parseGFF3Line(line, file string, lineNo int, sourceName string, seqs SequenceSet) (model.Feature, *Reject) {
	const format = "gff3"
	cols := strings.Split(line, "\t")
	if len(cols) != 9 {
		return model.Feature{}, reject(format, file, lineNo, ReasonSyntax,
			"expected 9 tab-separated columns, got %d", len(cols))
	}
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
		if cols[i] == "" && i != 8 {
			return model.Feature{}, reject(format, file, lineNo, ReasonMissingField,
				"column %d is empty", i+1)
		}
	}

	rawStart, rec := atoiField(format, file, lineNo, cols[3], "start")
	if rec != nil {
		return model.Feature{}, rec
	}
	rawEnd, rec := atoiField(format, file, lineNo, cols[4], "end")
	if rec != nil {
		return model.Feature{}, rec
	}
	iv, err := coord.FromRaw(coord.OneBasedClosed, rawStart, rawEnd)
	if err != nil {
		return model.Feature{}, reject(format, file, lineNo, ReasonBadCoordinateSystem, "%v", err)
	}

	strand, err := model.ParseStrand(cols[6])
	if err != nil {
		return model.Feature{}, reject(format, file, lineNo, ReasonBadStrand, "%v", err)
	}
	if cols[7] != "." && cols[7] != "0" && cols[7] != "1" && cols[7] != "2" {
		return model.Feature{}, reject(format, file, lineNo, ReasonBadPhase,
			"phase must be 0, 1, 2 or '.', got %q", cols[7])
	}

	attrs, err := parseGFF3Attributes(cols[8])
	if err != nil {
		return model.Feature{}, reject(format, file, lineNo, ReasonSyntax, "%v", err)
	}

	id := attrs["ID"]
	if id == "" {
		id = fmt.Sprintf("%s#line%d:%s:%s:%d:%d", sourceName, lineNo, cols[2], cols[0], iv.Start, iv.End)
	}

	f := model.Feature{
		ID:        id,
		Type:      normalizeType(cols[2]),
		SeqID:     cols[0],
		Strand:    strand,
		Interval:  iv,
		ParentIDs: splitMulti(attrs["Parent"]),
		Version:   attrs["version"],
		Attrs:     attrs,
		Source: model.Source{
			Name:   sourceName,
			Format: format,
			File:   file,
		},
		Line: lineNo,
	}
	if rej := validateAndBuild(format, &f, seqs); rej != nil {
		return model.Feature{}, rej
	}
	return f, nil
}

func parseGFF3Attributes(raw string) (map[string]string, error) {
	attrs := map[string]string{}
	if raw == "." || raw == "" {
		return attrs, nil
	}
	for _, pair := range strings.Split(raw, ";") {
		if pair == "" {
			return nil, fmt.Errorf("empty attribute in %q", raw)
		}
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			return nil, fmt.Errorf("attribute %q is not key=value", pair)
		}
		key := pair[:eq]
		value := pair[eq+1:]
		if key == "" {
			return nil, fmt.Errorf("empty attribute key in %q", raw)
		}
		if _, dup := attrs[key]; dup {
			return nil, fmt.Errorf("duplicate attribute key %q", key)
		}
		attrs[key] = value
	}
	return attrs, nil
}

func splitMulti(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeType(t string) model.FeatureType {
	switch strings.ToLower(t) {
	case "gene":
		return model.Gene
	case "transcript", "mrna", "rrna", "trna", "ncrna", "mirna", "lnc_rna":
		return model.Transcript
	case "exon":
		return model.Exon
	case "cds":
		return model.CDS
	default:
		return model.Other
	}
}
