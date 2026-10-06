package parse

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

// ParseGTF parses GTF (a GFF2 dialect). Coordinates are 1-based closed and
// converted the same way as GFF3.
//
// Identity is taken from gene_id / transcript_id attributes:
//
//   - gene features are required to carry gene_id;
//   - transcript-like features are required to carry gene_id and
//     transcript_id, and become children of "<gene_id>";
//   - exon/CDS features are required to carry gene_id and transcript_id;
//     repeated exons with the same exon_id are represented as separate
//     features keyed by a deterministic coordinate-suffixed id
//     ("<exon_id>#<seqid>:<start>:<end>") because GTF exon_id is commonly
//     reused for multiple discontinuous rows.
//
// An optional "version" attribute supplies the feature version.
func ParseGTF(r io.Reader, file string, opts Options) ([]model.Feature, Rejects, error) {
	br := bufio.NewScanner(r)
	br.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var features []model.Feature
	var rejects Rejects
	lineNo := 0
	sourceName := opts.SourceName
	if sourceName == "" {
		sourceName = file
	}

	for br.Scan() {
		lineNo++
		line := strings.TrimRight(br.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f, rec := parseGTFLine(line, file, lineNo, sourceName, opts.Sequences)
		if rec != nil {
			rejects = append(rejects, rec)
			continue
		}
		features = append(features, f)
	}
	if err := br.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return features, rejects, nil
}

func parseGTFLine(line, file string, lineNo int, sourceName string, seqs SequenceSet) (model.Feature, *Reject) {
	const format = "gtf"
	cols := strings.Split(line, "\t")
	if len(cols) != 9 {
		return model.Feature{}, reject(format, file, lineNo, ReasonSyntax,
			"expected 9 tab-separated columns, got %d", len(cols))
	}
	for i := 0; i < 8; i++ {
		cols[i] = strings.TrimSpace(cols[i])
		if cols[i] == "" {
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
			"frame must be 0, 1, 2 or '.', got %q", cols[7])
	}

	attrs, err := parseGTFAttributes(cols[8])
	if err != nil {
		return model.Feature{}, reject(format, file, lineNo, ReasonSyntax, "%v", err)
	}

	ftype := normalizeType(cols[2])
	geneID := attrs["gene_id"]
	transcriptID := attrs["transcript_id"]

	var id string
	var parents []string
	switch ftype {
	case model.Gene:
		if geneID == "" {
			return model.Feature{}, reject(format, file, lineNo, ReasonMissingField,
				"gene feature requires gene_id")
		}
		id = geneID
	case model.Transcript:
		if geneID == "" || transcriptID == "" {
			return model.Feature{}, reject(format, file, lineNo, ReasonMissingField,
				"transcript feature requires gene_id and transcript_id")
		}
		id = transcriptID
		parents = []string{geneID}
	case model.Exon, model.CDS:
		if geneID == "" || transcriptID == "" {
			return model.Feature{}, reject(format, file, lineNo, ReasonMissingField,
				"%s feature requires gene_id and transcript_id", ftype)
		}
		exonID := attrs["exon_id"]
		if exonID == "" {
			exonID = transcriptID + ".exon"
		}
		id = fmt.Sprintf("%s#%s:%d:%d", exonID, cols[0], iv.Start, iv.End)
		parents = []string{transcriptID}
	default:
		if transcriptID != "" {
			id = fmt.Sprintf("%s#%s:%d:%d", transcriptID, cols[2], iv.Start, iv.End)
			parents = []string{transcriptID}
		} else if geneID != "" {
			id = fmt.Sprintf("%s#%s:%d:%d", geneID, cols[2], iv.Start, iv.End)
			parents = []string{geneID}
		} else {
			id = fmt.Sprintf("%s#line%d:%s:%s:%d:%d", sourceName, lineNo, cols[2], cols[0], iv.Start, iv.End)
		}
	}

	f := model.Feature{
		ID:        id,
		Type:      ftype,
		SeqID:     cols[0],
		Strand:    strand,
		Interval:  iv,
		ParentIDs: parents,
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

func parseGTFAttributes(raw string) (map[string]string, error) {
	attrs := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("missing attribute column")
	}
	rest := raw
	for rest != "" {
		rest = strings.TrimLeft(rest, " \t;")
		if rest == "" {
			break
		}
		sp := strings.IndexAny(rest, " \t")
		if sp < 0 {
			return nil, fmt.Errorf("malformed attribute segment %q", rest)
		}
		key := rest[:sp]
		rest = strings.TrimLeft(rest[sp:], " \t")
		if !strings.HasPrefix(rest, "\"") {
			return nil, fmt.Errorf("attribute %s value must be quoted", key)
		}
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return nil, fmt.Errorf("unterminated quoted value for %s", key)
		}
		value := rest[1 : 1+end]
		rest = rest[1+end+1:]
		if _, dup := attrs[key]; dup {
			return nil, fmt.Errorf("duplicate attribute key %q", key)
		}
		attrs[key] = value
	}
	return attrs, nil
}
