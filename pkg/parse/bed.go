package parse

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// ParseBED parses BED3/4/6/12 (0-based half-open coordinates).
func ParseBED(r io.Reader, opts Options) Result {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "track ") || strings.HasPrefix(line, "browser ") {
			continue
		}
		fs, rejs := parseBEDLine(line, opts, lineNo)
		if len(rejs) > 0 {
			res.Rejections = append(res.Rejections, rejs...)
			continue
		}
		res.Features = append(res.Features, fs...)
	}
	if err := sc.Err(); err != nil {
		res.Rejections = append(res.Rejections, model.Rejection{
			Kind: model.RejectMalformed, URI: opts.URI, Line: lineNo, Detail: "read error: " + err.Error(),
		})
	}
	return res
}

func parseBEDLine(line string, opts Options, lineNo int) ([]model.Feature, []model.Rejection) {
	cols := strings.Fields(line)
	n := len(cols)
	if n != 3 && n != 4 && n != 6 && n != 12 {
		return nil, []model.Rejection{*ptrRej(model.RejectMalformed, opts.URI, lineNo,
			"BED requires 3, 4, 6 or 12 columns, got %d", n)}
	}
	start, err := strconv.Atoi(cols[1])
	if err != nil || start < 0 {
		return nil, []model.Rejection{*ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "chromStart must be a non-negative integer, got %q", cols[1])}
	}
	end, err := strconv.Atoi(cols[2])
	if err != nil || end < 0 {
		return nil, []model.Rejection{*ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "chromEnd must be a non-negative integer, got %q", cols[2])}
	}
	if end < start {
		return nil, []model.Rejection{*ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "chromEnd %d precedes chromStart %d", end, start)}
	}
	strand := model.StrandUnknown
	if n >= 6 {
		strand = cols[5]
		if strand != model.StrandPlus && strand != model.StrandMinus && strand != "." {
			return nil, []model.Rejection{*ptrRej(model.RejectBadStrand, opts.URI, lineNo, "strand %q not in + - .", strand)}
		}
	}
	name := ""
	if n >= 4 {
		name = cols[3]
	}
	if n != 12 {
		if name == "" {
			return nil, []model.Rejection{*ptrRej(model.RejectMissingID, opts.URI, lineNo, "BED name column is required for identity")}
		}
		return []model.Feature{{
			ID: name, Version: 1, Type: model.FeatureOther, SeqID: cols[0],
			Start: start, End: end, Strand: strand, Source: opts.Source,
			URI: opts.URI, Line: lineNo, RawType: "bed", Attrs: map[string]string{},
		}}, nil
	}
	// BED12: one transcript with blockCount exons.
	if name == "" {
		return nil, []model.Rejection{*ptrRej(model.RejectMissingID, opts.URI, lineNo, "BED12 name column is required")}
	}
	blockCount, err := strconv.Atoi(cols[9])
	if err != nil || blockCount <= 0 {
		return nil, []model.Rejection{*ptrRej(model.RejectBadCoordinate, opts.URI, lineNo, "blockCount must be a positive integer, got %q", cols[9])}
	}
	blockSizes := strings.TrimSuffix(cols[10], ",")
	blockStarts := strings.TrimSuffix(cols[11], ",")
	sizes := strings.Split(blockSizes, ",")
	starts := strings.Split(blockStarts, ",")
	if len(sizes) != blockCount || len(starts) != blockCount {
		return nil, []model.Rejection{*ptrRej(model.RejectMalformed, opts.URI, lineNo,
			"blockCount=%d but found %d blockSizes / %d blockStarts", blockCount, len(sizes), len(starts))}
	}
	out := []model.Feature{{
		ID: name, Version: 1, Type: model.FeatureTranscript, SeqID: cols[0],
		Start: start, End: end, Strand: strand, Source: opts.Source,
		URI: opts.URI, Line: lineNo, RawType: "bed12", Attrs: map[string]string{},
	}}
	for i := 0; i < blockCount; i++ {
		bs, err1 := strconv.Atoi(sizes[i])
		bst, err2 := strconv.Atoi(starts[i])
		if err1 != nil || bs <= 0 || err2 != nil || bst < 0 {
			return nil, []model.Rejection{*ptrRej(model.RejectBadCoordinate, opts.URI, lineNo,
				"block %d has invalid size %q or start %q", i, sizes[i], starts[i])}
		}
		exStart := start + bst
		exEnd := exStart + bs
		if exEnd > end {
			return nil, []model.Rejection{*ptrRej(model.RejectBadCoordinate, opts.URI, lineNo,
				"block %d [%d,%d) extends past transcript end %d", i, exStart, exEnd, end)}
		}
		out = append(out, model.Feature{
			ID: fmt.Sprintf("exon:%s:%d-%d", name, exStart, exEnd), Version: 1,
			Type: model.FeatureExon, SeqID: cols[0], Start: exStart, End: exEnd,
			Strand: strand, Parents: []string{name}, Source: opts.Source,
			URI: opts.URI, Line: lineNo, RawType: "block", Attrs: map[string]string{"block_index": strconv.Itoa(i)},
		})
	}
	return out, nil
}
