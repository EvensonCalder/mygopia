package index

import (
	"fmt"
	"sync"
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
