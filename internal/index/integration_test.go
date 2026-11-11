package index_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
)

func TestEndToEndKopiaServerAndSearchIndex(t *testing.T) {
	kopiaBin, err := config.ResolveKopiaBinary("")
	if err != nil {
		t.Skipf("kopia binary not installed: %v", err)
	}

	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "config", "repository-test.config")
	logDir := filepath.Join(tmp, "logs")
	repoStorageDir := filepath.Join(tmp, "repo-storage")
	sourceDir := filepath.Join(tmp, "source-data")
	cacheDir := filepath.Join(tmp, "index-cache")
	restoreFile := filepath.Join(tmp, "restored-secret.txt")

	if err := os.MkdirAll(repoStorageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write initial files for Snapshot #1
	if err := os.WriteFile(filepath.Join(sourceDir, "docs", "notes.md"), []byte("version-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "deleted-later.txt"), []byte("ephemeral-secret-payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := kopia.NewServerRunner(kopiaBin, cfgFile, logDir, nil)
	defer runner.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cli, err := runner.Start(ctx)
	if err != nil {
		t.Fatalf("start kopia server: %v", err)
	}

	st0, err := cli.Status(ctx)
	if err != nil {
		t.Fatalf("status before connect: %v", err)
	}
	if st0.Connected {
		t.Fatalf("expected disconnected status on fresh config")
	}

	algos, err := cli.SupportedAlgorithms(ctx)
	if err != nil || algos.DefaultEncryption == "" {
		t.Fatalf("supported algorithms: %v (%+v)", err, algos)
	}

	// Initialize new filesystem repository via REST API
	err = cli.CreateRepo(ctx, kopia.CreateRepositoryRequest{
		ConnectRepositoryRequest: kopia.ConnectRepositoryRequest{
			Storage: kopia.StorageConnection{
				Type: "filesystem",
				Config: map[string]any{
					"path": repoStorageDir,
				},
			},
			Password: "test-master-password-123",
			ClientOptions: kopia.ClientOptions{
				Description: "Mygopia Test Vault",
			},
		},
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	st1, err := cli.Status(ctx)
	if err != nil || !st1.Connected {
		t.Fatalf("expected connected status after CreateRepo: %v (%+v)", err, st1)
	}

	// Update global policy retention & compression
	keepDaily := 14
	err = cli.SetPolicy(ctx, kopia.SourceInfo{}, &kopia.Policy{
		RetentionPolicy: kopia.RetentionPolicy{
			KeepDaily: &keepDaily,
		},
		CompressionPolicy: kopia.CompressionPolicy{
			CompressorName: "zstd",
		},
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	resolved, err := cli.ResolvePolicy(ctx, kopia.SourceInfo{}, nil, 3)
	if err != nil || resolved.Effective == nil || resolved.Effective.RetentionPolicy.KeepDaily == nil || *resolved.Effective.RetentionPolicy.KeepDaily != 14 {
		t.Fatalf("resolve policy mismatch: %v (%+v)", err, resolved)
	}

	// Create source & trigger Snapshot #1
	_, err = cli.CreateSource(ctx, sourceDir, true, &kopia.Policy{})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	waitForSnapshotCount(t, ctx, cli, 1)

	// Modify notes.md and remove deleted-later.txt, then trigger Snapshot #2
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(sourceDir, "docs", "notes.md"), []byte("version-2-longer-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(sourceDir, "deleted-later.txt")); err != nil {
		t.Fatal(err)
	}

	if err := cli.TriggerSnapshot(ctx, nil); err != nil {
		t.Fatalf("trigger snapshot 2: %v", err)
	}
	waitForSnapshotCount(t, ctx, cli, 2)

	// Index repository via DAG search engine
	eng := index.NewEngine(cacheDir, nil)
	if err := eng.IndexRepo(ctx, "test-repo", "Test Vault", cli); err != nil {
		t.Fatalf("IndexRepo: %v", err)
	}

	stats, ok := eng.Stats("test-repo")
	if !ok || stats.SnapshotCount != 2 || stats.DeletedCount != 1 {
		t.Fatalf("unexpected index stats: %+v", stats)
	}

	// Search deleted files only
	delRes := eng.Search(index.Query{
		RepoID:      "test-repo",
		DeletedOnly: true,
	})
	if delRes.TotalMatched != 1 || delRes.Items[0].Name != "deleted-later.txt" || delRes.Items[0].OnDisk {
		t.Fatalf("expected deleted-later.txt in DeletedOnly search, got %+v", delRes)
	}

	// Download the deleted file from Kopia object store and verify exact bytes!
	if err := cli.DownloadObjectToFile(ctx, delRes.Items[0].LatestObjectID, delRes.Items[0].Name, restoreFile); err != nil {
		t.Fatalf("download deleted object: %v", err)
	}
	gotBytes, err := os.ReadFile(restoreFile)
	if err != nil || string(gotBytes) != "ephemeral-secret-payload" {
		t.Fatalf("restored bytes mismatch: %q (err=%v)", string(gotBytes), err)
	}

	// Search multi-version files
	verRes := eng.Search(index.Query{
		RepoID:      "test-repo",
		MinVersions: 2,
		Kind:        index.KindFiles,
	})
	if verRes.TotalMatched != 1 || verRes.Items[0].Path != "docs/notes.md" || verRes.Items[0].Versions != 2 {
		t.Fatalf("expected docs/notes.md with 2 versions, got %+v", verRes)
	}

	// Compare Snapshot #1 vs Snapshot #2 roots via CompareRoots
	srcsResp, err := cli.ListSources(ctx)
	if err != nil || len(srcsResp.Sources) == 0 {
		t.Fatalf("ListSources for diff: %v", err)
	}
	snapsResp, err := cli.ListSnapshots(ctx, srcsResp.Sources[0].Source, true)
	if err != nil || len(snapsResp.Snapshots) < 2 {
		t.Fatalf("ListSnapshots for diff: %v", err)
	}
	diffSum, err := eng.CompareRoots(ctx, "test-repo", cli, snapsResp.Snapshots[0].RootID, snapsResp.Snapshots[len(snapsResp.Snapshots)-1].RootID)
	if err != nil {
		t.Fatalf("CompareRoots: %v", err)
	}
	if diffSum.ModifiedCount != 1 || diffSum.DeletedCount != 1 {
		t.Fatalf("expected 1 modified (docs/notes.md) and 1 deleted (deleted-later.txt) in CompareRoots, got %+v", diffSum)
	}

	// Compare latest Snapshot #2 against unchanged live sourceDir -> 0 differences!
	latestSnap := snapsResp.Snapshots[len(snapsResp.Snapshots)-1]
	cleanLocalDiff, err := eng.CompareRootWithLocalDir(ctx, "test-repo", cli, latestSnap.RootID, index.LocalDiffOptions{
		LocalPath:              sourceDir,
		IgnoreCacheDirectories: true,
	})
	if err != nil {
		t.Fatalf("CompareRootWithLocalDir (clean): %v", err)
	}
	if len(cleanLocalDiff.Entries) != 0 {
		t.Fatalf("expected 0 differences between latest snapshot and untouched local folder, got %+v", cleanLocalDiff.Entries)
	}

	// Stage local working-tree changes + .kopiaignore before next backup and verify CompareRootWithLocalDir!
	if err := os.WriteFile(filepath.Join(sourceDir, ".kopiaignore"), []byte("*.ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "skip-me.ignored"), []byte("should be ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pending-next-backup.txt"), []byte("hello next snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirtyLocalDiff, err := eng.CompareRootWithLocalDir(ctx, "test-repo", cli, latestSnap.RootID, index.LocalDiffOptions{
		LocalPath:              sourceDir,
		DotIgnoreFiles:         []string{".kopiaignore"},
		IgnoreCacheDirectories: true,
	})
	if err != nil {
		t.Fatalf("CompareRootWithLocalDir (dirty): %v", err)
	}
	var sawPending, sawIgnored bool
	for _, de := range dirtyLocalDiff.Entries {
		if de.Path == "pending-next-backup.txt" && de.Kind == index.DiffAdded {
			sawPending = true
		}
		if de.Path == "skip-me.ignored" {
			sawIgnored = true
		}
	}
	if !sawPending || sawIgnored {
		t.Fatalf("expected pending-next-backup.txt Added and skip-me.ignored excluded, got %+v", dirtyLocalDiff.Entries)
	}

	// Exercise CLIHelper maintenance & content stats JSON/raw schemas against live Kopia
	helper := kopia.CLIHelper{KopiaBin: kopiaBin, ConfigFile: cfgFile}
	maintInfo, err := helper.GetMaintenanceInfo(ctx)
	if err != nil || maintInfo.Owner == "" {
		t.Fatalf("GetMaintenanceInfo: %v (%+v)", err, maintInfo)
	}
	contentStats, err := helper.GetContentStats(ctx)
	if err != nil || contentStats.Count <= 0 || contentStats.OriginalBytes <= 0 {
		t.Fatalf("GetContentStats: %v (%+v)", err, contentStats)
	}
	th, err := cli.GetThrottle(ctx)
	if err != nil {
		t.Fatalf("GetThrottle: %v", err)
	}
	th.UploadBytesPerSecond = 512 * 1024
	if err := cli.SetThrottle(ctx, *th); err != nil {
		t.Fatalf("SetThrottle: %v", err)
	}
	thAfter, err := cli.GetThrottle(ctx)
	if err != nil || thAfter.UploadBytesPerSecond != 512*1024 {
		t.Fatalf("GetThrottle after SetThrottle mismatch: %v (%+v)", err, thAfter)
	}

	// Re-run IndexRepo from warm disk cache: ProgressDirs (newly fetched) must be 0!
	eng2 := index.NewEngine(cacheDir, nil)
	if err := eng2.IndexRepo(ctx, "test-repo", "Test Vault", cli); err != nil {
		t.Fatalf("warm IndexRepo: %v", err)
	}
	warmStats, _ := eng2.Stats("test-repo")
	if warmStats.ProgressDirs != 0 {
		t.Fatalf("expected 0 newly fetched directories on warm DAG cache, got %d", warmStats.ProgressDirs)
	}
}

func waitForSnapshotCount(t *testing.T, ctx context.Context, cli *kopia.Client, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		srcs, err := cli.ListSources(ctx)
		if err == nil && len(srcs.Sources) > 0 {
			src := srcs.Sources[0]
			if src.Status == "IDLE" {
				snaps, err := cli.ListSnapshots(ctx, src.Source, true)
				if err == nil && len(snaps.Snapshots) >= want {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d snapshots", want)
}
