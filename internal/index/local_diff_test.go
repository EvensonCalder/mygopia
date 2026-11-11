package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mygopia/internal/kopia"
)

func TestLocalDiffWithIgnoreRulesAndCacheTag(t *testing.T) {
	localDir := t.TempDir()
	baseTime := time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)

	mustWrite := func(rel string, body string, mtime time.Time) {
		t.Helper()
		full := filepath.Join(localDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !mtime.IsZero() {
			if err := os.Chtimes(full, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 1. Unchanged file (same size + same mtime as snapshot)
	mustWrite("keep.txt", "unchanged-body", baseTime)
	// 2. Modified file (same byte length, newer mtime!)
	mustWrite("docs/spec.md", "v2-same-len", baseTime.Add(5*time.Minute))
	// 3. Newly added file on local disk
	mustWrite("src/new_feature.go", "package src\n", baseTime.Add(10*time.Minute))
	// 4. Ignored by root IgnoreRules (*.log, node_modules/)
	mustWrite("debug.log", "ignored log", baseTime)
	mustWrite("node_modules/pkg/index.js", "ignored module", baseTime)
	// 5. Ignored by nested .kopiaignore, with !keep.tmp negation override
	mustWrite("work/.kopiaignore", "*.tmp\n!important.tmp\n", baseTime)
	mustWrite("work/scratch.tmp", "ignored scratch", baseTime)
	mustWrite("work/important.tmp", "kept!", baseTime.Add(2*time.Minute))
	// 6. Ignored by CACHEDIR.TAG when IgnoreCacheDirectories is enabled
	mustWrite("build-cache/CACHEDIR.TAG", cacheDirTagSignature+"\n# cache dir\n", baseTime)
	mustWrite("build-cache/blob.bin", "cached artifact", baseTime)

	src := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: localDir}
	snap := &kopia.Snapshot{
		ID:        "snap-1",
		RootID:    "kroot-1",
		StartTime: kopia.FlexTime{Time: baseTime},
	}

	eng := NewEngine("", nil)
	eng.SeedRepo("repo-1", "Test", []SearchItem{
		{
			RepoID: "repo-1", Source: src, Path: "keep.txt", Name: "keep.txt",
			History: []VersionRecord{{SnapshotID: "snap-1", RootID: "kroot-1", ObjectID: "obj-keep", Size: int64(len("unchanged-body")), ModTime: baseTime}},
		},
		{
			RepoID: "repo-1", Source: src, Path: "docs", Name: "docs", IsDir: true,
			History: []VersionRecord{{SnapshotID: "snap-1", RootID: "kroot-1", ObjectID: "kdir-docs", ModTime: baseTime}},
		},
		{
			RepoID: "repo-1", Source: src, Path: "docs/spec.md", Name: "spec.md",
			History: []VersionRecord{{SnapshotID: "snap-1", RootID: "kroot-1", ObjectID: "obj-spec-v1", Size: int64(len("v1-same-len")), ModTime: baseTime}},
		},
		{
			RepoID: "repo-1", Source: src, Path: "removed.key", Name: "removed.key",
			History: []VersionRecord{{SnapshotID: "snap-1", RootID: "kroot-1", ObjectID: "obj-removed", Size: 64, ModTime: baseTime}},
		},
	})

	sum, err := eng.CompareSeededSnapshotWithLocalDir("repo-1", src, snap, LocalDiffOptions{
		LocalPath:              localDir,
		IgnoreRules:            []string{"*.log", "node_modules/", ".kopiaignore"},
		DotIgnoreFiles:         []string{".kopiaignore"},
		IgnoreCacheDirectories: true,
	})
	if err != nil {
		t.Fatalf("CompareSeededSnapshotWithLocalDir: %v", err)
	}

	byPath := make(map[string]DiffEntry, len(sum.Entries))
	for _, e := range sum.Entries {
		byPath[e.Path] = e
	}

	if _, bad := byPath["keep.txt"]; bad {
		t.Fatalf("unchanged keep.txt should not appear in diff")
	}
	if e, ok := byPath["docs/spec.md"]; !ok || e.Kind != DiffModified || e.OldObjectID != "obj-spec-v1" {
		t.Fatalf("expected docs/spec.md Modified with OldObjectID, got ok=%v %+v", ok, e)
	}
	if e, ok := byPath["removed.key"]; !ok || e.Kind != DiffDeleted || e.OldObjectID != "obj-removed" {
		t.Fatalf("expected removed.key Deleted with OldObjectID, got ok=%v %+v", ok, e)
	}
	if e, ok := byPath["src/new_feature.go"]; !ok || e.Kind != DiffAdded {
		t.Fatalf("expected src/new_feature.go Added, got ok=%v %+v", ok, e)
	}
	if e, ok := byPath["work/important.tmp"]; !ok || e.Kind != DiffAdded {
		t.Fatalf("expected negated !important.tmp to be Added, got ok=%v %+v", ok, e)
	}
	for _, ignoredPath := range []string{"debug.log", "node_modules", "node_modules/pkg/index.js", "work/scratch.tmp", "build-cache", "build-cache/blob.bin"} {
		if _, leaked := byPath[ignoredPath]; leaked {
			t.Fatalf("expected ignored path %q to be excluded from next-backup local diff, got %+v", ignoredPath, byPath[ignoredPath])
		}
	}
}
