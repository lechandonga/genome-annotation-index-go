// Package importer orchestrates end-to-end imports: parse one or more local
// annotation files with an optional FASTA reference, collect structured
// rejection reasons and commit accepted features atomically.
package importer

import (
	"fmt"

	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/model"
	"github.com/lechandonga/genome-annotation-index-go/parse"
	"github.com/lechandonga/genome-annotation-index-go/store"
)

// File describes one annotation input.
type File struct {
	Path       string
	SourceName string
}

// Report is the explainable outcome of an import run.
type Report struct {
	Parsed    int
	Rejected  parse.Rejects
	Committed *store.CommitResult
}

// Import parses files, validates every record, and commits all accepted
// features in one atomic batch. When any graph integrity problem is found
// nothing is committed; both parse rejects and graph problems are returned
// so callers can present distinct reasons.
func Import(st *store.Store, files []File, refFASTA string, onConflict graph.OnConflict) (*Report, error) {
	var seqs parse.SequenceSet
	if refFASTA != "" {
		var err error
		seqs, err = parse.ParseFASTAFile(refFASTA)
		if err != nil {
			return nil, fmt.Errorf("loading reference %s: %w", refFASTA, err)
		}
	}

	rep := &Report{}
	var all []model.Feature
	for _, f := range files {
		res, err := parse.ParseFile(f.Path, parse.Options{
			SourceName: f.SourceName,
			Sequences:  seqs,
		})
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", f.Path, err)
		}
		rep.Parsed += len(res.Features)
		rep.Rejected = append(rep.Rejected, res.Rejects...)
		all = append(all, res.Features...)
	}

	if len(rep.Rejected) > 0 {
		// Parse rejections make the batch non-atomic by definition; callers
		// decide whether to fix data or re-run. We do not commit partial data
		// silently.
		return rep, rep.Rejected
	}

	source := ""
	sourcePath := ""
	if len(files) > 0 {
		source = files[0].SourceName
		sourcePath = files[0].Path
		if source == "" {
			source = files[0].Path
		}
	}
	res, err := st.Commit(all, store.CommitOptions{
		Source:     source,
		OnConflict: onConflict,
		SourcePath: sourcePath,
	})
	if err != nil {
		return rep, err
	}
	rep.Committed = res
	return rep, nil
}
