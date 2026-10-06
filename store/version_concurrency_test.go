package store

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lechandonga/genome-annotation-index-go/coord"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

func TestVersionEvolutionAcrossCommits(t *testing.T) {
	t.Log("业务: 跨导入版本演进、回滚、重启复现、回滚后再提交")
	st, dir := tmpStore(t)
	defer st.Close()

	commit := func(version string, wantErr bool) {
		f := feat("g1", "chr1", 0, 10, model.Forward, model.Gene)
		f.Version = version
		_, err := st.Commit([]model.Feature{f}, CommitOptions{Source: "v" + version})
		if wantErr && err == nil {
			t.Fatalf("v%s expected rejection", version)
		}
		if !wantErr && err != nil {
			t.Fatalf("v%s unexpected: %v", version, err)
		}
	}
	commit("1", false)
	commit("2", false)
	commit("1", true)

	cur, _ := st.Snapshot().Current("g1")
	if cur.Version != "2" {
		t.Fatalf("current version = %s", cur.Version)
	}

	if _, err := st.Rollback(1); err != nil {
		t.Fatal(err)
	}
	cur, _ = st.Snapshot().Current("g1")
	if cur.Version != "1" {
		t.Fatalf("after rollback current = %s", cur.Version)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(dir, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close() }()
	cur, _ = st2.Snapshot().Current("g1")
	if cur.Version != "1" {
		t.Fatalf("after reopen post-rollback current = %s", cur.Version)
	}

	f := feat("g1", "chr1", 0, 10, model.Forward, model.Gene)
	f.Version = "3"
	if _, err := st2.Commit([]model.Feature{f}, CommitOptions{Source: "v3"}); err != nil {
		t.Fatal(err)
	}
	cur, _ = st2.Snapshot().Current("g1")
	if cur.Version != "3" {
		t.Fatalf("after post-rollback commit current = %s", cur.Version)
	}
}

func TestCommitRejectsDanglingAndDuplicate(t *testing.T) {
	t.Log("业务: 导入期完整性 — 重复id/悬空父级导致整批不提交")
	st, _ := tmpStore(t)
	defer st.Close()
	seedGeneFamily(t, st)

	if _, err := st.Commit(
		[]model.Feature{feat("dup", "chr1", 0, 5, model.Forward, model.Gene),
			feat("dup", "chr1", 5, 8, model.Forward, model.Gene)},
		CommitOptions{Source: "dups"},
	); err == nil {
		t.Fatal("duplicate id batch must be rejected")
	}
	if _, err := st.Commit(
		[]model.Feature{feat("orphanExon", "chr1", 0, 5, model.Forward, model.Exon, "no-such-tx")},
		CommitOptions{Source: "orphan"},
	); err == nil {
		t.Fatal("dangling parent batch must be rejected")
	}
	if st.Revision() != 1 {
		t.Fatalf("failed commits must not advance revision, got %d", st.Revision())
	}
}

// TestConcurrentQueriesDuringSwap validates that readers never observe a
// mixed/partial view while revisions change. A committer goroutine alternates
// between committing an extra family and rolling it back; readers verify that
// the answer is always exactly one of the two known-complete result sets.
func TestConcurrentQueriesDuringSwap(t *testing.T) {
	t.Log("业务: 重建/导入并发 — 读者只看到变更前或变更后的完整视图")
	st, _ := tmpStore(t)
	defer st.Close()
	seedGeneFamily(t, st)

	q := Query{SeqID: "chr1", Region: coord.Interval{Start: 0, End: 1000}}
	base := len(st.Query(q))

	extra := []model.Feature{
		feat("h1", "chr1", 0, 10, model.Forward, model.Gene),
		feat("h2", "chr1", 900, 950, model.Reverse, model.Gene),
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	writerDone := make(chan struct{})

	// Readers: every observed count must be base or base+len(extra).
	readers := 8
	var bad atomic.Int64
	var samples atomic.Int64
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				n := len(st.Query(q))
				samples.Add(1)
				if n != base && n != base+len(extra) {
					bad.Add(1)
				}
			}
		}()
	}

	// Writer alternates commit (revision+1) and rollback to previous rev.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if _, err := st.Commit(extra, CommitOptions{Source: "extra"}); err != nil {
				t.Errorf("commit: %v", err)
				return
			}
			target := st.Revision() - 1
			if _, err := st.Rollback(target); err != nil {
				t.Errorf("rollback: %v", err)
				return
			}
		}
		close(writerDone)
	}()

	<-writerDone
	stop.Store(true)
	wg.Wait()

	if samples.Load() == 0 {
		t.Fatal("no concurrent samples observed")
	}
	if bad.Load() != 0 {
		t.Fatalf("%d/%d queries observed a partial view", bad.Load(), samples.Load())
	}
	t.Logf("  并发用例 校验查询样本=%d, 全部等于完整视图计数之一(%d 或 %d)",
		samples.Load(), base, base+len(extra))

	// Final explicit rebuild while reading.
	var wg2 sync.WaitGroup
	stop2 := atomic.Bool{}
	for i := 0; i < readers; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for !stop2.Load() {
				n := len(st.Query(q))
				if n != base {
					bad.Add(1)
				}
			}
		}()
	}
	if _, err := st.Rebuild(); err != nil {
		t.Fatal(err)
	}
	stop2.Store(true)
	wg2.Wait()
	if bad.Load() != 0 {
		t.Fatalf("queries observed inconsistent view during rebuild")
	}
}
