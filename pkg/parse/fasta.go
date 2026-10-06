package parse

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"strings"

	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
)

// ParseFASTA parses reference sequences. It records sequence id, length and
// SHA-256 checksum; raw bases are not retained.
func ParseFASTA(r io.Reader, opts Options) Result {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	lineNo := 0

	var id string
	var h hash.Hash
	var length int
	var started bool

	flush := func() {
		if !started {
			return
		}
		res.Sequences = append(res.Sequences, model.Sequence{
			ID: id, Length: length, SHA256: hex.EncodeToString(h.Sum(nil)),
			Source: opts.Source, URI: opts.URI,
		})
	}

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ">") {
			flush()
			id = strings.Fields(strings.TrimPrefix(line, ">"))[0:1][0]
			if len(strings.Fields(strings.TrimPrefix(line, ">"))) == 0 {
				res.Rejections = append(res.Rejections, model.Rejection{
					Kind: model.RejectMissingField, URI: opts.URI, Line: lineNo, Detail: "FASTA header without sequence id",
				})
				id = ""
				started = false
				continue
			}
			h = sha256.New()
			length = 0
			started = true
			continue
		}
		if strings.HasPrefix(line, ";") {
			continue
		}
		if !started {
			res.Rejections = append(res.Rejections, model.Rejection{
				Kind: model.RejectMalformed, URI: opts.URI, Line: lineNo, Detail: "sequence data before any FASTA header",
			})
			continue
		}
		clean := strings.Map(func(r rune) rune {
			switch r {
			case 'A', 'C', 'G', 'T', 'N', 'a', 'c', 'g', 't', 'n',
				'U', 'u', 'R', 'r', 'Y', 'y', 'K', 'k', 'M', 'm',
				'S', 's', 'W', 'w', 'B', 'b', 'D', 'd', 'H', 'h', 'V', 'v',
				'*', '-', '.':
				return r
			}
			return '�'
		}, line)
		if strings.ContainsRune(clean, '�') {
			res.Rejections = append(res.Rejections, model.Rejection{
				Kind: model.RejectMalformed, URI: opts.URI, Line: lineNo, Detail: "sequence line contains non-IUPAC characters",
			})
			continue
		}
		h.Write([]byte(line))
		length += len(line)
	}
	flush()
	if err := sc.Err(); err != nil {
		res.Rejections = append(res.Rejections, model.Rejection{
			Kind: model.RejectMalformed, URI: opts.URI, Line: lineNo, Detail: "read error: " + err.Error(),
		})
	}
	return res
}
