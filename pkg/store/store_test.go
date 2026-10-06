package store_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/pkg/index"
	"github.com/lechandonga/genome-annotation-index-go/pkg/model"
	"github.com/lechandonga/genome-annotation-index-go/pkg/parse"
	"github.com/lechandonga/genome-annotation-index-go/pkg/persist"
	"github.com/lechandonga/genome-annotation-index-go/pkg/store"
	"github.com/lechandonga/genome-annotation-index-go/pkg/testutil"
)

const fasta = ">chr1\n" +
	"ACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGT\n" // 100 bases

const gffV1 = `##gff-version 3
chr1	s	gene	1	100	.	+	.	ID=g1
chr1	s	mRNA	1	100	.	+	.	ID=t1;Parent=g1
chr1	s	exon	1	50	.	+	.	Parent=t1
`

const gffV2 = `##gff-version 3
chr1	s	gene	1	80	.	+	.	ID=g1
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func seed(t *testing.T, dataDir, idxDir string) *store.Store {
	t.Helper()
	fa := writeFile(t, dataDir, "chr1.fa", fasta)
	gff := writeFile(t, dataDir, "a.gff3", gffV1)
	st, err := store.Open(store.Config{Dir: idxDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportFile(fa, "ref", parse.FormatFASTA); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportFile(gff, "ann", parse.FormatGFF3); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestImportPersistenceRestart(t *testing.T) {
	root := t.TempDir()
	dataDir, idxDir := filepath.Join(root, "data"), filepath.Join(root, "idx")
	st := seed(t, dataDir, idxDir)

	testutil.RunGroup(t, "store.persistence", []testutil.Case{
		{
			Name: "query after import sees normalized data",
			Run: func() string {
				hits := st.Query(index.Query{SeqID: "chr1", Start: 0, End: 100})
				if len(hits) != 3 {
					return testutil.Fail("hits=%d", len(hits))
				}
				return ""
			},
		},
		{
			Name: "reopen process loads identical view",
			Run: func() string {
				st2, err := store.Open(store.Config{Dir: idxDir})
				if err != nil {
					return err.Error()
				}
				if st2.Generation() != st.Generation() {
					return testutil.Fail("generation %d vs %d", st2.Generation(), st.Generation())
				}
				hits := st2.Query(index.Query{SeqID: "chr1", Start: 0, End: 100})
				if len(hits) != 3 {
					return testutil.Fail("hits after reopen=%d", len(hits))
				}
				return ""
			},
		},
		{
			Name: "bad record fails whole import transaction",
			Run: func() string {
				bad := writeFile(t, dataDir, "bad.gff3",
					"chr1\ts\tgene\t1\t9\t.\t+\t.\tID=g2\n"+
						"chr1\ts\tgene\tX\t9\t.\t+\t.\tID=g3\n")
				before := st.Generation()
				rep, err := st.ImportFile(bad, "bad", parse.FormatGFF3)
				if err == nil {
					return "import should have failed"
				}
				if st.Generation() != before {
					return "generation changed despite transaction failure"
				}
				if len(st.Query(index.Query{SeqID: "chr1", Start: 0, End: 100})) != 3 {
					return "partial data became visible"
				}
				if !errors.Is(err, store.ErrRejected) {
					return testutil.Fail("err=%v", err)
				}
				found := false
				for _, r := range rep.Rejections {
					if r.Kind == model.RejectBadCoordinate {
						found = true
					}
				}
				if !found {
					return testutil.Fail("expected bad_coordinate reason, got %+v", rep.Rejections)
				}
				return ""
			},
		},
		{
			Name: "version evolution then rollback is reproducible",
			Run: func() string {
				gff2 := writeFile(t, dataDir, "b.gff3", gffV2)
				if _, err := st.ImportFile(gff2, "ann2", parse.FormatGFF3); err != nil {
					return err.Error()
				}
				cur, ok := st.Snapshot().CurrentFeature("g1")
				if !ok || cur.End != 80 || cur.Version != 2 {
					return testutil.Fail("cur=%+v ok=%v", cur, ok)
				}
				if err := st.Rollback("g1", 1); err != nil {
					return err.Error()
				}
				cur, _ = st.Snapshot().CurrentFeature("g1")
				if cur.End != 100 || cur.Version != 3 {
					return testutil.Fail("post rollback cur=%+v", cur)
				}
				// restart and confirm persisted
				st2, err := store.Open(store.Config{Dir: idxDir})
				if err != nil {
					return err.Error()
				}
				cur, _ = st2.Snapshot().CurrentFeature("g1")
				if cur.End != 100 || cur.Version != 3 {
					return testutil.Fail("after restart cur=%+v", cur)
				}
				return ""
			},
		},
	})
}
func TestCorruptionAndRebuild(t *testing.T) {
	root := t.TempDir()
	dataDir, idxDir := filepath.Join(root, "data"), filepath.Join(root, "idx")
	st := seed(t, dataDir, idxDir)
	before := len(st.Query(index.Query{SeqID: "chr1", Start: 0, End: 100}))

	testutil.RunGroup(t, "store.recovery", []testutil.Case{
		{
			Name: "tampered snapshot fails closed (no wrong results)",
			Run: func() string {
				snapPath, _ := persist.Paths(idxDir)
				data, err := os.ReadFile(snapPath)
				if err != nil {
					return err.Error()
				}
				if len(data) == 0 {
					return "empty snapshot"
				}
				data[len(data)-2] ^= 0xFF
				if err := os.WriteFile(snapPath, data, 0o644); err != nil {
					return err.Error()
				}
				if _, err := store.Open(store.Config{Dir: idxDir}); err == nil {
					return "corrupt index was accepted"
				}
				return ""
			},
		},
		{
			Name: "safe rebuild from recorded sources reproduces the view",
			Run: func() string {
				// The tampered snapshot cannot be read, so recovery starts from
				// the recorded local source files.
				// proper from-sources rebuild
				files := []store.SourceFile{
					{Path: filepath.Join(dataDir, "chr1.fa"), Format: parse.FormatFASTA, Source: "ref"},
					{Path: filepath.Join(dataDir, "a.gff3"), Format: parse.FormatGFF3, Source: "ann"},
				}
				good2, err := store.RebuildFromSources(store.Config{Dir: idxDir}, files)
				if err != nil {
					return err.Error()
				}
				hits := good2.Query(index.Query{SeqID: "chr1", Start: 0, End: 100})
				if len(hits) != before {
					return testutil.Fail("rebuilt hits=%d want %d", len(hits), before)
				}
				return ""
			},
		},
		{
			Name: "in-place rebuild replays ops deterministically",
			Run: func() string {
				n, err := st.Rebuild()
				if err != nil {
					return err.Error()
				}
				if n != 2 {
					return testutil.Fail("replayed ops=%d", n)
				}
				if len(st.Query(index.Query{SeqID: "chr1", Start: 0, End: 100})) != before {
					return "query result changed after rebuild"
				}
				return ""
			},
		},
		{
			Name: "missing source during rebuild fails explicitly",
			Run: func() string {
				missing := filepath.Join(dataDir, "ghost.gff3")
				// craft op pointing at missing file by importing then deleting
				gffPath := writeFile(t, dataDir, "c.gff3", gffV2)
				if _, err := st.ImportFile(gffPath, "ann3", parse.FormatGFF3); err != nil {
					return err.Error()
				}
				if err := os.Remove(gffPath); err != nil {
					return err.Error()
				}
				_ = missing
				if _, err := st.Rebuild(); err == nil {
					return "rebuild should fail when a source disappeared"
				}
				return ""
			},
		},
	})
}

func TestConcurrentQueriesDuringRebuild(t *testing.T) {
	root := t.TempDir()
	dataDir, idxDir := filepath.Join(root, "cdata"), filepath.Join(root, "cidx")
	st := seed(t, dataDir, idxDir)

	testutil.RunGroup(t, "store.concurrent_rebuild", []testutil.Case{{
		Name: "readers see only complete pre/post views during import+rebuild",
		Run: func() string {
			const readers = 16
			var wg sync.WaitGroup
			errCh := make(chan string, readers)
			stop := make(chan struct{})
			for i := 0; i < readers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
						}
						hits := st.Query(index.Query{SeqID: "chr1", Start: 0, End: 90})
						g := st.Generation()
						ids := map[string]bool{}
						for _, h := range hits {
							if ids[h.Feature.ID] {
								select {
								case errCh <- "duplicate feature id in a single view":
								default:
								}
								return
							}
							ids[h.Feature.ID] = true
						}
						// Only two complete views may ever be observed:
						// pre-import {g1,t1,e1} at generation 2, or
						// post-import {g1,t1,e1} (g2 sits at [90,100) and is
						// outside this window) at generation 3. In both cases
						// the observed set and the generation must agree, and
						// the graph must be whole (g2 absent here is correct).
						want := map[string]bool{"g1": true, "t1": true, "exon:t1:0-50": true}
						if len(ids) != len(want) || g != 2 && g != 3 {
							select {
							case errCh <- fmt.Sprintf("torn view: generation=%d ids=%v", g, ids):
							default:
							}
							return
						}
						for id := range want {
							if !ids[id] {
								select {
								case errCh <- fmt.Sprintf("missing %s in view generation=%d", id, g):
								default:
								}
								return
							}
						}
					}
				}()
			}
			gff2 := writeFile(t, dataDir, "d.gff3",
				"chr1\ts\tgene\t91\t100\t.\t+\t.\tID=g2\n")
			if _, err := st.ImportFile(gff2, "ann4", parse.FormatGFF3); err != nil {
				return err.Error()
			}
			if _, err := st.Rebuild(); err != nil {
				return err.Error()
			}
			close(stop)
			wg.Wait()
			close(errCh)
			for msg := range errCh {
				return msg
			}
			return ""
		},
	}})
}
