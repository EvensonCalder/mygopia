package index

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mygopia/internal/kopia"
)

func TestSearchEngineFiltersAndSorting(t *testing.T) {
	eng := NewEngine("", nil)

	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)

	srcDocs := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: "/Users/alex/Documents"}
	srcCode := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: "/Users/alex/Projects"}

	eng.SeedRepo("repo-local", "Local Vault", []SearchItem{
		{
			RepoID:         "repo-local",
			RepoName:       "Local Vault",
			Source:         srcDocs,
			Path:           "finance/2026-budget.xlsx",
			Name:           "2026-budget.xlsx",
			Ext:            "xlsx",
			IsDir:          false,
			OnDisk:         true,
			LatestSize:     48 * 1024,
			MaxSize:        52 * 1024,
			Versions:       3,
			SnapshotCount:  3,
			FirstSeen:      t1,
			LastSeen:       t3,
			LatestObjectID: "obj-budget-v3",
		},
		{
			RepoID:         "repo-local",
			RepoName:       "Local Vault",
			Source:         srcDocs,
			Path:           "legacy/deleted-contract.pdf",
			Name:           "deleted-contract.pdf",
			Ext:            "pdf",
			IsDir:          false,
			OnDisk:         false, // deleted in latest snapshot!
			LatestSize:     2 * 1024 * 1024,
			MaxSize:        2 * 1024 * 1024,
			Versions:       1,
			SnapshotCount:  2,
			FirstSeen:      t1,
			LastSeen:       t2,
			LatestObjectID: "obj-contract-v1",
		},
		{
			RepoID:         "repo-local",
			RepoName:       "Local Vault",
			Source:         srcDocs,
			Path:           "finance",
			Name:           "finance",
			IsDir:          true,
			OnDisk:         true,
			Versions:       2,
			SnapshotCount:  3,
			FirstSeen:      t1,
			LastSeen:       t3,
			LatestObjectID: "kdir-finance",
		},
	})

	eng.SeedRepo("repo-cloud", "Cloud S3", []SearchItem{
		{
			RepoID:         "repo-cloud",
			RepoName:       "Cloud S3",
			Source:         srcCode,
			Path:           "mygopia/release.zip",
			Name:           "release.zip",
			Ext:            "zip",
			IsDir:          false,
			OnDisk:         true,
			LatestSize:     25 * 1024 * 1024,
			MaxSize:        25 * 1024 * 1024,
			Versions:       2,
			SnapshotCount:  2,
			FirstSeen:      t2,
			LastSeen:       t3,
			LatestObjectID: "obj-release-v2",
		},
	})

	// 1. Multi-token substring query across single repo
	res := eng.Search(Query{
		RepoID: "repo-local",
		Text:   "finance budget",
	})
	if res.TotalMatched != 1 || res.Items[0].Name != "2026-budget.xlsx" {
		t.Fatalf("expected 2026-budget.xlsx, got %+v", res.Items)
	}

	// 2. DeletedOnly filter surfaces files missing from latest snapshot
	delRes := eng.Search(Query{
		RepoID:      "repo-local",
		DeletedOnly: true,
	})
	if delRes.TotalMatched != 1 || delRes.Items[0].Name != "deleted-contract.pdf" {
		t.Fatalf("expected deleted-contract.pdf, got %+v", delRes.Items)
	}

	// 3. Cross-repository search (RepoID == "") sorted by size descending
	allFiles := eng.Search(Query{
		Kind:     KindFiles,
		SortBy:   SortSize,
		SortDesc: true,
	})
	if allFiles.TotalMatched != 3 {
		t.Fatalf("expected 3 total files across 2 repos, got %d", allFiles.TotalMatched)
	}
	if allFiles.Items[0].Name != "release.zip" || allFiles.Items[2].Name != "2026-budget.xlsx" {
		t.Fatalf("unexpected size desc sort order: %s, %s, %s",
			allFiles.Items[0].Name, allFiles.Items[1].Name, allFiles.Items[2].Name)
	}

	// 4. Include/Exclude extension + MinVersions filters
	extRes := eng.Search(Query{
		IncludeExts: ".xlsx, pdf",
		ExcludeExts: "pdf",
		MinVersions: 2,
	})
	if extRes.TotalMatched != 1 || extRes.Items[0].Ext != "xlsx" {
		t.Fatalf("unexpected extension/version filtered items: %+v", extRes.Items)
	}

	// 5. Folders-only filter & Source filter
	dirRes := eng.Search(Query{
		Kind:         KindFolders,
		SourceFilter: srcDocs.String(),
	})
	if dirRes.TotalMatched != 1 || !dirRes.Items[0].IsDir {
		t.Fatalf("expected 1 folder in srcDocs, got %+v", dirRes.Items)
	}
}

func TestDiskCacheConcurrentSaveAndMerge(t *testing.T) {
	eng := NewEngine(t.TempDir(), nil)
	const repoID = "repo-concurrent"

	// Seed baseline disk cache via saveDiskCache
	if err := eng.saveDiskCache(repoID, map[string][]CachedEntry{
		"kdir-base": {{Name: "a.txt", Size: 10, ObjectID: "obj-a"}},
	}); err != nil {
		t.Fatalf("saveDiskCache: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			oid := fmt.Sprintf("kdir-diff-%d", idx)
			if err := eng.mergeDiskCacheEntries(repoID, map[string][]CachedEntry{
				oid: {{Name: "f.txt", Size: int64(idx), ObjectID: oid}},
			}); err != nil {
				t.Errorf("mergeDiskCacheEntries(%d): %v", idx, err)
			}
		}(i)
		go func() {
			defer wg.Done()
			got := eng.loadDiskCache(repoID)
			if _, ok := got["kdir-base"]; !ok {
				t.Errorf("expected kdir-base to remain present during concurrent merges")
			}
		}()
	}
	wg.Wait()

	final := eng.loadDiskCache(repoID)
	if len(final) != 17 {
		t.Fatalf("expected 17 cached directories (1 base + 16 merged), got %d", len(final))
	}
}

func TestIndexRepoCancelDuringTreeWalkResetsIndexingFlag(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var cancelOnDirFetch atomic.Bool
	cancelOnDirFetch.Store(true)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/sources":
			_ = json.NewEncoder(w).Encode(kopia.SourcesResponse{
				Sources: []*kopia.SourceStatus{
					{Source: kopia.SourceInfo{Host: "host", UserName: "user", Path: "/data"}},
				},
			})
		case "/api/v1/snapshots":
			_ = json.NewEncoder(w).Encode(kopia.SnapshotsResponse{
				Snapshots: []*kopia.Snapshot{
					{ID: "snap-1", RootID: "kdir-root"},
				},
			})
		case "/api/v1/objects/kdir-root":
			_ = json.NewEncoder(w).Encode(kopia.DirManifest{
				StreamType: "kopia:directory",
				Entries: []*kopia.DirEntry{
					{Name: "hello.txt", Type: "f", ObjectID: "obj-1", FileSize: 5},
				},
			})
			if cancelOnDirFetch.CompareAndSwap(true, false) {
				cancel()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})
	cli, err := kopia.NewClient(kopia.ClientConfig{
		BaseURL:       srv.URL,
		Username:      "kopia",
		Password:      "secret",
		ServerCertPEM: certPEM,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	eng := NewEngine("", nil)
	err = eng.IndexRepo(ctx, "repo-cancel", "Cancel Repo", cli)
	if err == nil {
		t.Fatalf("expected canceled IndexRepo to return error")
	}
	st, ok := eng.Stats("repo-cancel")
	if !ok || st.Indexing {
		t.Fatalf("expected Indexing=false after cancellation during tree walk, got ok=%v stats=%+v", ok, st)
	}

	// Verify subsequent IndexRepo call is not permanently wedged by a stuck Indexing=true flag
	if err := eng.IndexRepo(context.Background(), "repo-cancel", "Cancel Repo", cli); err != nil {
		t.Fatalf("expected subsequent IndexRepo to succeed, got: %v", err)
	}
	st2, ok := eng.Stats("repo-cancel")
	if !ok || st2.Indexing || st2.FileCount != 1 {
		t.Fatalf("expected 1 indexed file after retry, got ok=%v stats=%+v", ok, st2)
	}
}

func TestOffHeapHistoryColdStartAndStreaming(t *testing.T) {
	t1 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)

	srcA := kopia.SourceInfo{Host: "mac", UserName: "ev", Path: "/Users/ev/Docs"}
	srcB := kopia.SourceInfo{Host: "mac", UserName: "ev", Path: "/Users/ev/Code"}

	dirs := map[string][]*kopia.DirEntry{
		"kdir-a1": {
			{Name: "report.pdf", Type: "f", ObjectID: "obj-rep-v1", FileSize: 1024, ModTime: kopia.FlexTime{Time: t1}},
			{Name: "old.txt", Type: "f", ObjectID: "obj-old-v1", FileSize: 128, ModTime: kopia.FlexTime{Time: t1}},
		},
		"kdir-a2": {
			{Name: "report.pdf", Type: "f", ObjectID: "obj-rep-v2", FileSize: 2048, ModTime: kopia.FlexTime{Time: t2}},
			{Name: "old.txt", Type: "f", ObjectID: "obj-old-v1", FileSize: 128, ModTime: kopia.FlexTime{Time: t1}},
		},
		"kdir-a3": {
			{Name: "report.pdf", Type: "f", ObjectID: "obj-rep-v3", FileSize: 4096, ModTime: kopia.FlexTime{Time: t3}},
			{Name: "notes.md", Type: "f", ObjectID: "obj-notes-v1", FileSize: 512, ModTime: kopia.FlexTime{Time: t3}},
		},
		"kdir-b1": {
			{Name: "main.go", Type: "f", ObjectID: "obj-main-v1", FileSize: 900, ModTime: kopia.FlexTime{Time: t3}},
		},
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/sources":
			_ = json.NewEncoder(w).Encode(kopia.SourcesResponse{
				Sources: []*kopia.SourceStatus{
					{Source: srcA},
					{Source: srcB},
				},
			})
		case "/api/v1/snapshots":
			path := r.URL.Query().Get("path")
			if path == srcA.Path {
				_ = json.NewEncoder(w).Encode(kopia.SnapshotsResponse{
					Snapshots: []*kopia.Snapshot{
						{ID: "sa-1", RootID: "kdir-a1", StartTime: kopia.FlexTime{Time: t1}},
						{ID: "sa-2", RootID: "kdir-a2", StartTime: kopia.FlexTime{Time: t2}},
						{ID: "sa-3", RootID: "kdir-a3", StartTime: kopia.FlexTime{Time: t3}},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(kopia.SnapshotsResponse{
				Snapshots: []*kopia.Snapshot{
					{ID: "sb-1", RootID: "kdir-b1", StartTime: kopia.FlexTime{Time: t3}},
				},
			})
		default:
			var oid string
			_, _ = fmt.Sscanf(r.URL.Path, "/api/v1/objects/%s", &oid)
			if ents, ok := dirs[oid]; ok {
				_ = json.NewEncoder(w).Encode(kopia.DirManifest{
					StreamType: "kopia:directory",
					Entries:    ents,
				})
				return
			}
			http.NotFound(w, r)
		}
	}))

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	cli, err := kopia.NewClient(kopia.ClientConfig{
		BaseURL:       srv.URL,
		Username:      "kopia",
		Password:      "secret",
		ServerCertPEM: certPEM,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	cacheDir := t.TempDir()
	var midStreamItems atomic.Int32
	var eng1 *Engine
	eng1 = NewEngine(cacheDir, func() {
		if st, ok := eng1.Stats("repo-stream"); ok && st.Indexing {
			r := eng1.Search(Query{RepoID: "repo-stream"})
			if r.TotalMatched > 0 {
				midStreamItems.Add(int32(r.TotalMatched))
			}
		}
	})

	if err := eng1.IndexRepo(context.Background(), "repo-stream", "Stream Vault", cli); err != nil {
		t.Fatalf("IndexRepo: %v", err)
	}
	if midStreamItems.Load() == 0 {
		t.Fatalf("expected live source-by-source catalog streaming while Indexing=true")
	}

	// Verify SearchItem.History is kept off Go heap (len == 0) while ItemHistory loads all 3 versions from bbolt
	res := eng1.Search(Query{RepoID: "repo-stream", Text: "report.pdf"})
	if res.TotalMatched != 1 {
		t.Fatalf("expected report.pdf match, got %d", res.TotalMatched)
	}
	rep := res.Items[0]
	if len(rep.History) != 0 {
		t.Fatalf("expected off-heap History slice to be nil in RAM, got len=%d", len(rep.History))
	}
	hist := eng1.ItemHistory(rep)
	if len(hist) != 3 || hist[0].ObjectID != "obj-rep-v3" || hist[2].ObjectID != "obj-rep-v1" {
		t.Fatalf("unexpected on-demand ItemHistory: %+v", hist)
	}
	eng1.Close()
	srv.Close() // Shut down server completely so eng2 proves 100% offline cache operation!

	eng2 := NewEngine(cacheDir, nil)
	defer eng2.Close()
	eng2.EnsureCatalogLoaded("repo-stream")

	resCold := eng2.Search(Query{RepoID: "repo-stream"})
	if resCold.TotalMatched != 4 {
		t.Fatalf("expected 4 items restored on cold start without server, got %d", resCold.TotalMatched)
	}
	repCold := eng2.Search(Query{RepoID: "repo-stream", Text: "report.pdf"}).Items[0]
	histCold := eng2.ItemHistory(repCold)
	if len(histCold) != 3 || histCold[0].SnapshotID != "sa-3" {
		t.Fatalf("expected 3 persisted history records on cold start, got %+v", histCold)
	}

	// Verify CompareRootsStream works 100% offline from bbolt cache and populates warm L1 diff cache
	var progressCalls int
	sum, err := eng2.CompareRootsStream(context.Background(), "repo-stream", nil, "kdir-a1", "kdir-a3", func(partial DiffSummary) {
		progressCalls++
	})
	if err != nil {
		t.Fatalf("CompareRootsStream offline: %v", err)
	}
	if sum.AddedCount != 1 || sum.ModifiedCount != 1 || sum.DeletedCount != 1 {
		t.Fatalf("unexpected diff counts: %+v", sum)
	}
	if _, ok := eng2.LookupDiff("repo-stream", "kdir-a1", "kdir-a3"); !ok {
		t.Fatalf("expected CompareRootsStream to populate warm L1 diff cache")
	}
	_ = progressCalls
}

func TestDiskBudgetEvictionAndCompaction(t *testing.T) {
	cacheDir := t.TempDir()
	eng := NewEngine(cacheDir, nil)
	defer eng.Close()

	const repoID = "repo-budget"
	rs, err := eng.getOrOpenStore(repoID)
	if err != nil || rs == nil {
		t.Fatalf("expected non-nil repoStore: %v", err)
	}

	makeEntries := func(seed int) []CachedEntry {
		out := make([]CachedEntry, 60)
		for i := range out {
			out[i] = CachedEntry{
				Name:     fmt.Sprintf("document-%04d-%03d.dat", seed, i),
				IsDir:    false,
				Size:     int64(4096 * (i + 1)),
				ModUnix:  time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).UnixNano(),
				ObjectID: fmt.Sprintf("ix%032x%04x", seed*97+i, i),
			}
		}
		return out
	}

	oldBatch := make(map[string][]CachedEntry, 120)
	for i := range 120 {
		oldBatch[fmt.Sprintf("kdir-old-%04d", i)] = makeEntries(i)
	}
	if err := rs.putDirs(oldBatch, 10); err != nil {
		t.Fatalf("putDirs old: %v", err)
	}

	newBatch := make(map[string][]CachedEntry, 8)
	for i := range 8 {
		newBatch[fmt.Sprintf("kdir-new-%04d", i)] = makeEntries(1000 + i)
	}
	if err := rs.putDirs(newBatch, 500); err != nil {
		t.Fatalf("putDirs new: %v", err)
	}

	beforeBytes := eng.DiskUsageBytes()
	if beforeBytes < 128*1024 {
		t.Fatalf("expected initial cache file >= 128 KB, got %d bytes", beforeBytes)
	}

	// Enforce a tight 96 KB total budget and verify oldest ranks are evicted + compacted while rank=1 survives!
	eng.SetMaxCacheBytes(96 * 1024)
	eng.EnforceCacheBudget()
	eng.TrimMemory()

	afterBytes := eng.DiskUsageBytes()
	if afterBytes >= beforeBytes {
		t.Fatalf("expected DiskUsageBytes to shrink after eviction+compaction (%d -> %d)", beforeBytes, afterBytes)
	}
	if _, ok := eng.LookupDirectory(repoID, "kdir-new-0000"); !ok {
		t.Fatalf("expected newest-rank directory kdir-new-0000 to remain cached after budget eviction")
	}
	evictedCount := 0
	for i := range 120 {
		if _, ok := eng.LookupDirectory(repoID, fmt.Sprintf("kdir-old-%04d", i)); !ok {
			evictedCount++
		}
	}
	if evictedCount == 0 {
		t.Fatalf("expected older-rank directories to be evicted when exceeding budget")
	}
}

func BenchmarkLookupDirectoryL1(b *testing.B) {
	eng := NewEngine(b.TempDir(), nil)
	defer eng.Close()
	entries := make([]CachedEntry, 128)
	for i := range entries {
		entries[i] = CachedEntry{
			Name:     fmt.Sprintf("file-%03d.go", i),
			Size:     int64(i * 512),
			ObjectID: fmt.Sprintf("ix%032x", i+1),
		}
	}
	eng.PutDirectory("repo-bench", "kdir-hot", entries)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, ok := eng.LookupDirectory("repo-bench", "kdir-hot")
		if !ok || len(got) != 128 {
			b.Fatal("lookup failed")
		}
	}
}

func BenchmarkLookupDirectoryBboltS2(b *testing.B) {
	eng := NewEngine(b.TempDir(), nil)
	defer eng.Close()
	entries := make([]CachedEntry, 128)
	for i := range entries {
		entries[i] = CachedEntry{
			Name:     fmt.Sprintf("file-%03d.go", i),
			Size:     int64(i * 512),
			ObjectID: fmt.Sprintf("ix%032x", i+1),
		}
	}
	if err := eng.saveDiskCache("repo-bench", map[string][]CachedEntry{"kdir-disk": entries}); err != nil {
		b.Fatal(err)
	}
	// Pin DB open during tight microbenchmark loop (matches active interactive session within 80ms lease)
	rs, err := eng.getOrOpenStore("repo-bench")
	if err != nil {
		b.Fatal(err)
	}
	_, release, err := rs.acquireDB()
	if err != nil {
		b.Fatal(err)
	}
	defer release()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		eng.dirLRU.Remove(eng.dirLRUKey("repo-bench", "kdir-disk"))
		got, ok := eng.LookupDirectory("repo-bench", "kdir-disk")
		if !ok || len(got) != 128 {
			b.Fatal("disk lookup failed")
		}
	}
}

func BenchmarkCompareRootsWarmL1(b *testing.B) {
	eng := NewEngine(b.TempDir(), nil)
	defer eng.Close()
	sum := DiffSummary{
		AddedCount:    12,
		ModifiedCount: 5,
		DeletedCount:  3,
		Entries:       make([]DiffEntry, 20),
	}
	eng.StoreDiff("repo-bench", "kroot-a", "kroot-b", sum)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, ok := eng.LookupDiff("repo-bench", "kroot-a", "kroot-b")
		if !ok || len(got.Entries) != 20 {
			b.Fatal("diff lookup failed")
		}
	}
}
