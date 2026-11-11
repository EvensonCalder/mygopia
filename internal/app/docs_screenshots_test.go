package app

import (
	"flag"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
)

var docsScreenshotsDir = flag.String("docs-screenshots", "", "directory to write privacy-free README showcase screenshots")

func writeDocsPNG(t *testing.T, outDir, name string, tt *ui.Tester, forbidden []string) {
	t.Helper()
	for _, txt := range tt.Texts() {
		lower := strings.ToLower(txt)
		for _, bad := range forbidden {
			if bad != "" && strings.Contains(lower, bad) {
				t.Fatalf("privacy check failed in %s: rendered text %q contains host token %q", name, txt, bad)
			}
		}
	}
	img := tt.Image()
	if img == nil || img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Fatalf("empty image for %s", name)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	dst := filepath.Join(outDir, name+".png")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", dst, err)
	}
}

func newDocsShowcaseApp(t *testing.T, lang, themeMode string) *App {
	t.Helper()
	tmp := t.TempDir()

	demoCfgDir := "/Users/alex/Library/Application Support/kopia"
	demoCfgFile := demoCfgDir + "/repository.config"

	cfg := config.DefaultSettings()
	cfg.Language = lang
	cfg.Theme = themeMode
	cfg.AccentColor = config.AccentBlue
	cfg.ConfigDir = demoCfgDir
	cfg.Repositories = []config.RepoProfile{
		{
			ID:          "repo-cloud",
			Name:        "Cloud Vault",
			ConfigFile:  demoCfgFile,
			StorageHint: "s3: studio-backups",
		},
		{
			ID:          "repo-nas",
			Name:        "Home NAS",
			ConfigFile:  demoCfgDir + "/repository-nas.config",
			StorageHint: "sftp: nas.local",
		},
	}
	cfg.ActiveRepoID = "repo-cloud"

	a := New(Options{
		UserDataDir:    tmp,
		CacheDir:       tmp,
		LogDir:         tmp,
		Settings:       &cfg,
		PreferredLangs: []string{lang},
		DisableServers: true,
	})

	// Override all host-probed binary/config fields with deterministic synthetic demo values.
	a.detectedKopia = "/opt/homebrew/bin/kopia"
	a.detectedRclone = "/opt/homebrew/bin/rclone"
	a.syncSettingsDraft()
	a.settingsCfgDir = demoCfgDir
	a.userDataDir = ""

	refNow := time.Date(2026, 5, 10, 14, 30, 0, 0, time.Local)
	srcDocs := kopia.SourceInfo{Host: "macbook", UserName: "alex", Path: "/Users/alex/Documents"}
	srcCode := kopia.SourceInfo{Host: "macbook", UserName: "alex", Path: "/Users/alex/Projects"}
	nextRun := kopia.FlexTime{Time: refNow.Add(30 * time.Minute)}
	endTask := kopia.FlexTime{Time: refNow.Add(-14 * time.Minute)}

	st := kopia.RepoSnapshotState{
		Profile:       cfg.Repositories[0],
		ServerRunning: true,
		ServerAddress: "https://127.0.0.1:54120",
		LastUpdated:   refNow,
		Status: &kopia.StatusResponse{
			Connected:                  true,
			ConfigFile:                 demoCfgFile,
			FormatVersion:              3,
			Hash:                       "BLAKE2B-256-128",
			Encryption:                 "AES256-GCM-HMAC-SHA256",
			Splitter:                   "DYNAMIC-4M-BUZHASH",
			Storage:                    "s3",
			SupportsContentCompression: true,
			Description:                "Alex MacBook",
			Username:                   "alex",
			Hostname:                   "macbook",
		},
		Sources: &kopia.SourcesResponse{
			LocalUsername: "alex",
			LocalHost:     "macbook",
			Sources: []*kopia.SourceStatus{
				{
					Source:           srcDocs,
					Status:           "IDLE",
					NextSnapshotTime: &nextRun,
					LastSnapshot: &kopia.ManifestPreview{
						ID:        "snap-docs-3",
						StartTime: kopia.FlexTime{Time: refNow.Add(-15 * time.Minute)},
						RootEntry: &kopia.DirEntry{
							ObjectID:   "kroot-docs-3",
							DirSummary: &kopia.DirectorySummary{TotalFileSize: 18 * 1024 * 1024 * 1024, TotalFileCount: 14280},
						},
					},
				},
				{
					Source: srcCode,
					Status: "UPLOADING",
					Upload: &kopia.UploadCounters{
						CachedBytes:      360 * 1024 * 1024,
						HashedBytes:      180 * 1024 * 1024,
						EstimatedBytes:   840 * 1024 * 1024,
						CurrentDirectory: "/Users/alex/Projects/webapp/src/components",
					},
					LastSnapshot: &kopia.ManifestPreview{
						ID:        "snap-code-2",
						StartTime: kopia.FlexTime{Time: refNow.Add(-2 * time.Hour)},
						RootEntry: &kopia.DirEntry{
							ObjectID:   "kroot-code-2",
							DirSummary: &kopia.DirectorySummary{TotalFileSize: 6 * 1024 * 1024 * 1024, TotalFileCount: 5180},
						},
					},
				},
			},
		},
		Policies: []*kopia.PolicyListEntry{
			{ID: "pol-global", Target: kopia.SourceInfo{}, Policy: &kopia.Policy{}},
			{ID: "pol-docs", Target: srcDocs, Policy: &kopia.Policy{}},
		},
		Tasks: []kopia.TaskInfo{
			{
				ID:           "task-1",
				Kind:         "Snapshot",
				Description:  "/Users/alex/Projects",
				Status:       "RUNNING",
				StartTime:    kopia.FlexTime{Time: refNow.Add(-20 * time.Second)},
				ProgressInfo: "540 MB / 840 MB",
			},
			{
				ID:           "task-0",
				Kind:         "Snapshot",
				Description:  "/Users/alex/Documents",
				Status:       "SUCCESS",
				StartTime:    kopia.FlexTime{Time: refNow.Add(-15 * time.Minute)},
				EndTime:      &endTask,
				ProgressInfo: "18.0 GB (0 errors)",
			},
		},
		Throttle: &kopia.ThrottleLimits{},
		Maintenance: &kopia.MaintenanceInfo{
			MaintenanceParams: kopia.MaintenanceParams{
				Owner:      "alex@macbook",
				QuickCycle: kopia.MaintenanceCycle{Enabled: true, Interval: int64(time.Hour)},
				FullCycle:  kopia.MaintenanceCycle{Enabled: true, Interval: int64(24 * time.Hour)},
			},
		},
		ContentStats: &kopia.ContentStats{
			Count:         24680,
			OriginalBytes: 24 * 1024 * 1024 * 1024,
			PackedBytes:   14 * 1024 * 1024 * 1024,
		},
	}
	st.Maintenance.Schedule.NextQuickMaintenanceTime = kopia.FlexTime{Time: refNow.Add(45 * time.Minute)}
	st.Maintenance.Schedule.NextFullMaintenanceTime = kopia.FlexTime{Time: refNow.Add(18 * time.Hour)}
	a.SeedRepoState("repo-cloud", st)

	// Deterministic local directory tree for Snapshot Diff (every mtime pinned to refNow offsets).
	localDocsDir := filepath.Join(tmp, "demo-docs")
	localCodeDir := filepath.Join(tmp, "demo-code")
	designsDir := filepath.Join(localDocsDir, "Designs")
	notesDir := filepath.Join(localDocsDir, "Notes")
	for _, d := range []string{designsDir, notesDir, localCodeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	specPath := filepath.Join(designsDir, "system-architecture.pdf")
	_ = os.WriteFile(specPath, make([]byte, 680*1024), 0o644)
	_ = os.Chtimes(specPath, refNow.Add(-4*time.Minute), refNow.Add(-4*time.Minute))

	notePath := filepath.Join(notesDir, "release-plan-v2.md")
	_ = os.WriteFile(notePath, []byte("# Release Plan\n- Verify incremental backups\n- Test archive restore\n"), 0o644)
	_ = os.Chtimes(notePath, refNow.Add(-2*time.Minute), refNow.Add(-2*time.Minute))

	ignorePath := filepath.Join(localDocsDir, ".kopiaignore")
	_ = os.WriteFile(ignorePath, []byte("*.tmp\n.DS_Store\n"), 0o644)
	_ = os.Chtimes(ignorePath, refNow.Add(-8*time.Minute), refNow.Add(-8*time.Minute))

	_ = os.Chtimes(designsDir, refNow.Add(-4*time.Minute), refNow.Add(-4*time.Minute))
	_ = os.Chtimes(notesDir, refNow.Add(-2*time.Minute), refNow.Add(-2*time.Minute))
	_ = os.Chtimes(localDocsDir, refNow.Add(-2*time.Minute), refNow.Add(-2*time.Minute))

	a.localDirOverrides = map[string]string{
		srcDocs.String(): localDocsDir,
		srcCode.String(): localCodeDir,
	}

	snaps := []*kopia.Snapshot{
		{
			ID:          "snap-docs-3",
			StartTime:   kopia.FlexTime{Time: refNow.Add(-15 * time.Minute)},
			RootID:      "kroot-docs-3",
			Pins:        []string{"keep-milestone"},
			Description: "Daily milestone",
			Summary:     &kopia.DirectorySummary{TotalFileSize: 18 * 1024 * 1024 * 1024, TotalFileCount: 14280},
		},
		{
			ID:        "snap-docs-2",
			StartTime: kopia.FlexTime{Time: refNow.Add(-6 * time.Hour)},
			RootID:    "kroot-docs-2",
			Summary:   &kopia.DirectorySummary{TotalFileSize: 17 * 1024 * 1024 * 1024, TotalFileCount: 14190},
		},
		{
			ID:          "snap-docs-1",
			StartTime:   kopia.FlexTime{Time: refNow.AddDate(-1, 0, 0)},
			RootID:      "kroot-docs-1",
			Description: "Annual archive",
			Summary:     &kopia.DirectorySummary{TotalFileSize: 12 * 1024 * 1024 * 1024, TotalFileCount: 9840},
		},
	}
	entries := []*kopia.DirEntry{
		{
			Name:       "Designs",
			Type:       "d",
			ObjectID:   "kdir-designs-01",
			ModTime:    kopia.FlexTime{Time: refNow.Add(-2 * time.Hour)},
			DirSummary: &kopia.DirectorySummary{TotalFileSize: 512 * 1024},
		},
		{
			Name:     "product-roadmap.pdf",
			Type:     "f",
			FileSize: 4820 * 1024,
			ObjectID: "ix8920a1c4f9b0112233445566778899",
			ModTime:  kopia.FlexTime{Time: refNow.Add(-30 * time.Minute)},
		},
	}
	a.SeedSnapshotsAndEntries(srcDocs.String(), snaps, entries)

	a.Engine().SeedRepoWithStats("repo-cloud", "Cloud Vault", []index.SearchItem{
		{
			RepoID:           "repo-cloud",
			RepoName:         "Cloud Vault",
			Source:           srcDocs,
			Path:             "product-roadmap.pdf",
			Name:             "product-roadmap.pdf",
			Ext:              "pdf",
			OnDisk:           true,
			LatestSize:       4820 * 1024,
			MaxSize:          4820 * 1024,
			Versions:         2,
			SnapshotCount:    3,
			FirstSeen:        refNow.AddDate(-1, 0, 0),
			LastSeen:         refNow.Add(-15 * time.Minute),
			LatestObjectID:   "ix8920a1c4f9b0112233445566778899",
			LatestSnapshotID: "snap-docs-3",
			History: []index.VersionRecord{
				{SnapshotID: "snap-docs-3", SnapshotTime: refNow.Add(-15 * time.Minute), ObjectID: "ix8920a1c4f9b011", Size: 4820 * 1024, ModTime: refNow.Add(-30 * time.Minute)},
				{SnapshotID: "snap-docs-2", SnapshotTime: refNow.Add(-6 * time.Hour), ObjectID: "ix7710a1c4f9b000", Size: 4610 * 1024, ModTime: refNow.Add(-7 * time.Hour)},
			},
		},
		{
			RepoID:           "repo-cloud",
			RepoName:         "Cloud Vault",
			Source:           srcDocs,
			Path:             "Designs/system-architecture.pdf",
			Name:             "system-architecture.pdf",
			Ext:              "pdf",
			OnDisk:           true,
			LatestSize:       512 * 1024,
			MaxSize:          512 * 1024,
			Versions:         3,
			SnapshotCount:    3,
			FirstSeen:        refNow.AddDate(-1, 0, 0),
			LastSeen:         refNow.Add(-15 * time.Minute),
			LatestObjectID:   "obj-arch-v3",
			LatestSnapshotID: "snap-docs-3",
			History: []index.VersionRecord{
				{SnapshotID: "snap-docs-3", SnapshotTime: refNow.Add(-15 * time.Minute), ObjectID: "obj-arch-v3", Size: 512 * 1024, ModTime: refNow.Add(-20 * time.Minute)},
				{SnapshotID: "snap-docs-2", SnapshotTime: refNow.Add(-6 * time.Hour), ObjectID: "obj-arch-v2", Size: 448 * 1024, ModTime: refNow.Add(-7 * time.Hour)},
				{SnapshotID: "snap-docs-1", SnapshotTime: refNow.AddDate(-1, 0, 0), ObjectID: "obj-arch-v1", Size: 320 * 1024, ModTime: refNow.AddDate(-1, 0, 0)},
			},
		},
		{
			RepoID:           "repo-cloud",
			RepoName:         "Cloud Vault",
			Source:           srcDocs,
			Path:             "Archive/legacy-credentials.kdbx",
			Name:             "legacy-credentials.kdbx",
			Ext:              "kdbx",
			OnDisk:           false,
			LatestSize:       96 * 1024,
			MaxSize:          96 * 1024,
			Versions:         1,
			SnapshotCount:    2,
			FirstSeen:        refNow.AddDate(-1, 0, 0),
			LastSeen:         refNow.Add(-6 * time.Hour),
			LatestObjectID:   "obj-vault-v1",
			LatestSnapshotID: "snap-docs-2",
			History: []index.VersionRecord{
				{SnapshotID: "snap-docs-2", SnapshotTime: refNow.Add(-6 * time.Hour), ObjectID: "obj-vault-v1", Size: 96 * 1024, ModTime: refNow.Add(-8 * time.Hour)},
			},
		},
	}, 48, refNow.Add(5*time.Second))

	a.runSnapshotDiffAsync()
	return a
}

func hostPrivacyTokens() []string {
	var out []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if len(s) >= 3 && s != "alex" && s != "macbook" && s != "darwin" && s != "users" {
			out = append(out, s)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(home)
		add(filepath.Base(home))
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		add(host)
	}
	add(os.Getenv("USER"))
	add(os.Getenv("USERNAME"))
	add(os.TempDir())
	add("/var/folders/")
	return out
}

func TestGenerateDocsScreenshots(t *testing.T) {
	outDir := strings.TrimSpace(*docsScreenshotsDir)
	if outDir == "" {
		// Still run a fast smoke check in normal `go test` to verify zero privacy leakage!
		a := newDocsShowcaseApp(t, config.LangEN, config.ThemeDark)
		tt := ui.NewTester(a.View, 1200, 760)
		forbidden := hostPrivacyTokens()
		for _, txt := range tt.Texts() {
			lower := strings.ToLower(txt)
			for _, bad := range forbidden {
				if bad != "" && strings.Contains(lower, bad) {
					t.Fatalf("privacy leak in showcase app: %q contains %q", txt, bad)
				}
			}
		}
		return
	}

	forbidden := hostPrivacyTokens()

	type langSpec struct {
		prefix     string
		lang       string
		searchNav  string
		diffNav    string
		settingNav string
	}
	specs := []langSpec{
		{prefix: "en", lang: config.LangEN, searchNav: "Search", diffNav: "Snapshot Diff", settingNav: "Settings"},
		{prefix: "zh", lang: config.LangZhHans, searchNav: "全局搜索", diffNav: "快照对比", settingNav: "应用设置"},
	}

	for _, sp := range specs {
		// 1. Dark theme: Backups, Search + Version Inspector, Snapshot & Local Diff
		aDark := newDocsShowcaseApp(t, sp.lang, config.ThemeDark)
		aDark.desktopRunning = true
		ttDark := ui.NewTester(aDark.View, 1200, 760)
		ttDark.SetDark(true)
		ttDark.Frame()
		writeDocsPNG(t, outDir, sp.prefix+"-backups", ttDark, forbidden)

		if err := ttDark.Click(sp.searchNav); err != nil {
			t.Fatalf("%s click search: %v", sp.prefix, err)
		}
		_ = ttDark.Click("Designs/system-architecture.pdf")
		writeDocsPNG(t, outDir, sp.prefix+"-search", ttDark, forbidden)

		if err := ttDark.Click(sp.diffNav); err != nil {
			t.Fatalf("%s click diff: %v", sp.prefix, err)
		}
		writeDocsPNG(t, outDir, sp.prefix+"-diff", ttDark, forbidden)
		aDark.desktopRunning = false

		// 2. Light theme: Settings (showcasing Light mode, 7 Accent Colors, 7 Languages)
		aLight := newDocsShowcaseApp(t, sp.lang, config.ThemeLight)
		aLight.desktopRunning = true
		ttLight := ui.NewTester(aLight.View, 1200, 760)
		ttLight.SetDark(false)
		if err := ttLight.Click(sp.settingNav); err != nil {
			t.Fatalf("%s click settings: %v", sp.prefix, err)
		}
		writeDocsPNG(t, outDir, sp.prefix+"-settings", ttLight, forbidden)
		aLight.desktopRunning = false
	}
}
