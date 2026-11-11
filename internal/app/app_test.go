package app

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	_ "github.com/egoist/mygo/locales/zh-Hant"
	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
)

var screenshotOutDir = flag.String("screenshots", "", "optional directory to write rendered UI PNG screenshots")

func saveTestPNG(t *testing.T, name string, tt *ui.Tester) {
	t.Helper()
	img := tt.Image()
	if img == nil || img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Fatalf("empty rendered frame for %s", name)
	}
	if *screenshotOutDir == "" {
		return
	}
	if err := os.MkdirAll(*screenshotOutDir, 0o755); err != nil {
		t.Fatalf("mkdir screenshots: %v", err)
	}
	p := filepath.Join(*screenshotOutDir, name+".png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create screenshot %s: %v", p, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode screenshot %s: %v", p, err)
	}
}

func newRichTestApp(t *testing.T, cfg config.Settings, preferredLangs []string) *App {
	t.Helper()
	tmp := t.TempDir()
	cfg.ConfigDir = tmp
	cfg.Repositories = []config.RepoProfile{
		{
			ID:          "repo-primary",
			Name:        "Studio NAS",
			ConfigFile:  tmp + "/repository.config",
			StorageHint: "s3: studio-vault-prod",
		},
		{
			ID:          "repo-offsite",
			Name:        "Offsite B2",
			ConfigFile:  tmp + "/repository-b2.config",
			StorageHint: "b2: offsite-archive",
		},
	}
	cfg.ActiveRepoID = "repo-primary"
	a := New(Options{
		UserDataDir:    tmp,
		CacheDir:       tmp,
		LogDir:         tmp,
		Settings:       &cfg,
		PreferredLangs: preferredLangs,
		DisableServers: true,
	})

	now := time.Date(2026, 11, 9, 1, 15, 0, 0, time.Local)
	srcDocs := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: "/Users/alex/Documents"}
	srcCode := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: "/Users/alex/Projects"}
	nextRun := kopia.FlexTime{Time: now.Add(45 * time.Minute)}
	endTask := kopia.FlexTime{Time: now.Add(-12 * time.Second)}

	st := kopia.RepoSnapshotState{
		Profile:       cfg.Repositories[0],
		ServerRunning: true,
		ServerAddress: "https://127.0.0.1:54120",
		LastUpdated:   now,
		Status: &kopia.StatusResponse{
			Connected:                  true,
			ConfigFile:                 cfg.Repositories[0].ConfigFile,
			FormatVersion:              3,
			Hash:                       "BLAKE2B-256-128",
			Encryption:                 "AES256-GCM-HMAC-SHA256",
			Splitter:                   "DYNAMIC-4M-BUZHASH",
			Storage:                    "s3",
			SupportsContentCompression: true,
			Description:                "Studio Workstation",
			Username:                   "alex",
			Hostname:                   "workstation",
		},
		Sources: &kopia.SourcesResponse{
			LocalUsername: "alex",
			LocalHost:     "workstation",
			Sources: []*kopia.SourceStatus{
				{
					Source:           srcDocs,
					Status:           "IDLE",
					NextSnapshotTime: &nextRun,
					LastSnapshot: &kopia.ManifestPreview{
						ID:        "snap-docs-3",
						StartTime: kopia.FlexTime{Time: now.Add(-15 * time.Minute)},
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
						CachedBytes:      320 * 1024 * 1024,
						HashedBytes:      140 * 1024 * 1024,
						EstimatedBytes:   800 * 1024 * 1024,
						CurrentDirectory: "/Users/alex/Projects/mygopia/internal/index",
					},
					LastSnapshot: &kopia.ManifestPreview{
						ID:        "snap-code-2",
						StartTime: kopia.FlexTime{Time: now.Add(-2 * time.Hour)},
						RootEntry: &kopia.DirEntry{
							ObjectID:   "kroot-code-2",
							DirSummary: &kopia.DirectorySummary{TotalFileSize: 4 * 1024 * 1024 * 1024, TotalFileCount: 3920},
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
				StartTime:    kopia.FlexTime{Time: time.Now().Add(-24 * time.Second)},
				ProgressInfo: "460 MB / 800 MB",
			},
			{
				ID:           "task-0",
				Kind:         "Snapshot",
				Description:  "/Users/alex/Documents",
				Status:       "SUCCESS",
				StartTime:    kopia.FlexTime{Time: now.Add(-15 * time.Minute)},
				EndTime:      &endTask,
				ProgressInfo: "18.0 GB (0 errors)",
			},
		},
		Throttle: &kopia.ThrottleLimits{},
		Maintenance: &kopia.MaintenanceInfo{
			MaintenanceParams: kopia.MaintenanceParams{
				Owner:      "alex@workstation",
				QuickCycle: kopia.MaintenanceCycle{Enabled: true, Interval: int64(time.Hour)},
				FullCycle:  kopia.MaintenanceCycle{Enabled: true, Interval: int64(24 * time.Hour)},
			},
		},
		ContentStats: &kopia.ContentStats{
			Count:         18450,
			OriginalBytes: 22 * 1024 * 1024 * 1024,
			PackedBytes:   13 * 1024 * 1024 * 1024,
		},
		NotificationProfiles: []kopia.NotificationProfile{
			{
				ProfileName:  "ops-webhook",
				MinSeverity:  10,
				MethodConfig: kopia.NotificationMethodConfig{Type: "webhook"},
			},
		},
		ServerLogs: []string{
			"SERVER ADDRESS: https://127.0.0.1:54120",
			"SERVER CERTIFICATE: [pinned]",
			"SERVER PASSWORD: [redacted]",
			"Repository connected (s3://studio-vault-prod)",
		},
	}
	st.Maintenance.Schedule.NextQuickMaintenanceTime = kopia.FlexTime{Time: now.Add(40 * time.Minute)}
	st.Maintenance.Schedule.NextFullMaintenanceTime = kopia.FlexTime{Time: now.Add(18 * time.Hour)}
	a.SeedRepoState("repo-primary", st)

	// Seed deterministic local folders in tmp so headless tests never touch real /Users/... paths
	localDocsDir := filepath.Join(tmp, "local-docs")
	localCodeDir := filepath.Join(tmp, "local-projects")
	for _, d := range []string{filepath.Join(localDocsDir, "Designs"), filepath.Join(localDocsDir, "Drafts"), localCodeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	specPath := filepath.Join(localDocsDir, "Designs", "architecture-spec.pdf")
	if err := os.WriteFile(specPath, make([]byte, 640*1024), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	_ = os.Chtimes(specPath, now.Add(-5*time.Minute), now.Add(-5*time.Minute))
	draftPath := filepath.Join(localDocsDir, "Drafts", "Q4-Release-Checklist.md")
	if err := os.WriteFile(draftPath, []byte("# Q4 Release Checklist\n- Verify Kopia local working-tree diff\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	_ = os.Chtimes(draftPath, now.Add(-2*time.Minute), now.Add(-2*time.Minute))
	_ = os.WriteFile(filepath.Join(localDocsDir, ".kopiaignore"), []byte("*.tmp\n"), 0o644)
	_ = os.WriteFile(filepath.Join(localDocsDir, "scratch.tmp"), []byte("ignored temp file"), 0o644)
	a.localDirOverrides = map[string]string{
		srcDocs.String(): localDocsDir,
		srcCode.String(): localCodeDir,
	}

	snaps := []*kopia.Snapshot{
		{
			ID:          "snap-docs-3",
			StartTime:   kopia.FlexTime{Time: now.Add(-15 * time.Minute)},
			RootID:      "kroot-docs-3",
			Pins:        []string{"do-not-delete"},
			Description: "Pre-release milestone",
			Summary:     &kopia.DirectorySummary{TotalFileSize: 18 * 1024 * 1024 * 1024, TotalFileCount: 14280},
		},
		{
			ID:        "snap-docs-2",
			StartTime: kopia.FlexTime{Time: now.Add(-6 * time.Hour)},
			RootID:    "kroot-docs-2",
			Summary:   &kopia.DirectorySummary{TotalFileSize: 17 * 1024 * 1024 * 1024, TotalFileCount: 14190},
		},
		{
			ID:          "snap-docs-1",
			StartTime:   kopia.FlexTime{Time: now.AddDate(-2, 0, 0)},
			RootID:      "kroot-docs-1",
			Description: "Annual archive baseline",
			Summary:     &kopia.DirectorySummary{TotalFileSize: 12 * 1024 * 1024 * 1024, TotalFileCount: 9840},
		},
	}
	entries := []*kopia.DirEntry{
		{
			Name:       "Contracts",
			Type:       "d",
			ObjectID:   "kdir-contracts-01",
			ModTime:    kopia.FlexTime{Time: now.Add(-2 * time.Hour)},
			DirSummary: &kopia.DirectorySummary{TotalFileSize: 142 * 1024 * 1024},
		},
		{
			Name:       "Design-System",
			Type:       "d",
			ObjectID:   "kdir-design-02",
			ModTime:    kopia.FlexTime{Time: now.Add(-26 * time.Hour)},
			DirSummary: &kopia.DirectorySummary{TotalFileSize: 890 * 1024 * 1024},
		},
		{
			Name:     "2026-Product-Roadmap.pdf",
			Type:     "f",
			FileSize: 4820 * 1024,
			ObjectID: "ix8920a1c4f9b0112233445566778899",
			ModTime:  kopia.FlexTime{Time: now.Add(-30 * time.Minute)},
		},
		{
			Name:     "vault-recovery-notes.md",
			Type:     "f",
			FileSize: 12480,
			ObjectID: "f901122aabbccddeeff001122334455",
			ModTime:  kopia.FlexTime{Time: now.Add(-3 * time.Hour)},
		},
	}
	a.SeedSnapshotsAndEntries(srcDocs.String(), snaps, entries)

	a.Engine().SeedRepo("repo-primary", "Studio NAS", []index.SearchItem{
		{
			RepoID:           "repo-primary",
			RepoName:         "Studio NAS",
			Source:           srcDocs,
			Path:             "2026-Product-Roadmap.pdf",
			Name:             "2026-Product-Roadmap.pdf",
			Ext:              "pdf",
			OnDisk:           true,
			LatestSize:       4820 * 1024,
			MaxSize:          4820 * 1024,
			Versions:         1,
			SnapshotCount:    1,
			FirstSeen:        now.Add(-15 * time.Minute),
			LastSeen:         now.Add(-15 * time.Minute),
			LatestObjectID:   "ix8920a1c4f9b0112233445566778899",
			LatestSnapshotID: "snap-docs-3",
			History: []index.VersionRecord{
				{SnapshotID: "snap-docs-3", SnapshotTime: now.Add(-15 * time.Minute), ObjectID: "ix8920a1c4f9b0112233445566778899", Size: 4820 * 1024, ModTime: now.Add(-30 * time.Minute)},
			},
		},
		{
			RepoID:           "repo-primary",
			RepoName:         "Studio NAS",
			Source:           srcDocs,
			Path:             "Designs/architecture-spec.pdf",
			Name:             "architecture-spec.pdf",
			Ext:              "pdf",
			OnDisk:           true,
			LatestSize:       1024 * 512,
			MaxSize:          1024 * 512,
			Versions:         2,
			SnapshotCount:    3,
			FirstSeen:        now.AddDate(-2, 0, 0),
			LastSeen:         now.Add(-15 * time.Minute),
			LatestObjectID:   "obj-spec-v2",
			LatestSnapshotID: "snap-docs-3",
			History: []index.VersionRecord{
				{SnapshotID: "snap-docs-3", SnapshotTime: now.Add(-15 * time.Minute), ObjectID: "obj-spec-v2", Size: 1024 * 512, ModTime: now.Add(-20 * time.Minute)},
				{SnapshotID: "snap-docs-2", SnapshotTime: now.Add(-6 * time.Hour), ObjectID: "obj-spec-v1", Size: 1024 * 400, ModTime: now.Add(-7 * time.Hour)},
				{SnapshotID: "snap-docs-1", SnapshotTime: now.AddDate(-2, 0, 0), ObjectID: "obj-spec-v0", Size: 1024 * 256, ModTime: now.AddDate(-2, 0, 0)},
			},
		},
		{
			RepoID:           "repo-primary",
			RepoName:         "Studio NAS",
			Source:           srcDocs,
			Path:             "Archive/accidental-delete.key",
			Name:             "accidental-delete.key",
			Ext:              "key",
			OnDisk:           false,
			LatestSize:       2048,
			MaxSize:          2048,
			Versions:         1,
			SnapshotCount:    2,
			FirstSeen:        now.AddDate(-2, 0, 0),
			LastSeen:         now.Add(-6 * time.Hour),
			LatestObjectID:   "obj-key-v1",
			LatestSnapshotID: "snap-docs-2",
			History: []index.VersionRecord{
				{SnapshotID: "snap-docs-2", SnapshotTime: now.Add(-6 * time.Hour), ObjectID: "obj-key-v1", Size: 2048, ModTime: now.Add(-8 * time.Hour)},
				{SnapshotID: "snap-docs-1", SnapshotTime: now.AddDate(-2, 0, 0), ObjectID: "obj-key-v1", Size: 2048, ModTime: now.AddDate(-2, 0, 0)},
			},
		},
	})
	a.runSnapshotDiffAsync()

	return a
}

func extractFmtVerbs(s string) string {
	var verbs []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+1 < len(s) {
			if s[i+1] == '%' {
				i++
				continue
			}
			verbs = append(verbs, '%', s[i+1])
			i++
		}
	}
	return string(verbs)
}

func TestAllTranslationKeysCovered(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var problems []string
	requiredKeys := map[string]string{}
	for k := range zhHansCatalog {
		requiredKeys[k] = "zhHansCatalog"
	}
	for _, acc := range accentOptions {
		requiredKeys[acc.LabelKey] = "accentOptions"
	}

	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fileAST, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(fileAST, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || (ident.Name != "tr" && ident.Name != "trf") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil || key == "" {
				return true
			}
			requiredKeys[key] = name
			return true
		})
	}

	catalogs := allTranslationCatalogs()
	for lang, cat := range catalogs {
		for key, origin := range requiredKeys {
			val, exists := cat[key]
			if !exists || strings.TrimSpace(val) == "" {
				problems = append(problems, lang+" missing "+origin+": "+strconv.Quote(key))
				continue
			}
			if extractFmtVerbs(key) != extractFmtVerbs(val) {
				problems = append(problems, lang+" format verb mismatch for "+strconv.Quote(key)+": got "+strconv.Quote(val))
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("translation catalog issues (%d):\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

func TestHeadlessThemeAndLocalization(t *testing.T) {
	cfg := config.DefaultSettings()
	cfg.Language = config.LangSystem
	cfg.Theme = config.ThemeSystem
	a := newRichTestApp(t, cfg, []string{"zh-CN", "en-US"})

	tt := ui.NewTester(a.View, 1220, 780)
	if !tt.HasText("备份源") || !tt.HasText("全局搜索") || !tt.HasText("快照浏览") {
		t.Fatalf("expected Simplified Chinese navigation labels, got texts: %v", tt.Texts())
	}

	// Verify top bar status pill ("已连接" right after repo name) X position stays pixel-constant when switching sidebar items,
	// and that "全局搜索" only exists as the left-sidebar button (Y > 48), never as a redundant top-bar button (Y < 48).
	searchBtn, okSearchBtn := tt.AccessByRole(ui.RoleButton, "全局搜索")
	if !okSearchBtn || searchBtn.Bounds.Y <= 48 || searchBtn.Bounds.X > 60 {
		t.Fatalf("expected '全局搜索' button only in left sidebar (Y > 48, X <= 60), got %+v (ok=%v)", searchBtn.Bounds, okSearchBtn)
	}
	connRect0, okConn := tt.Find("已连接")
	if !okConn || connRect0.Y > 48 {
		t.Fatalf("expected toolbar status pill '已连接' in top bar, got %+v (ok=%v)", connRect0, okConn)
	}
	for _, tab := range []string{"快照浏览", "任务与日志", "仓库与维护", "应用设置"} {
		if err := tt.Click(tab); err != nil {
			t.Fatalf("click %s: %v", tab, err)
		}
		connRect, ok := tt.Find("已连接")
		if !ok || connRect.X != connRect0.X {
			t.Fatalf("top toolbar jumped when switching to %s: got X=%v (ok=%v), want constant X=%v", tab, connRect.X, ok, connRect0.X)
		}
	}

	// Verify System Theme tracks OS Light -> Dark dynamically (sample lower canvas area below cards).
	tt.SetDark(false)
	tt.Frame()
	lightBg := tt.Image().RGBAAt(600, 720)

	tt.SetDark(true)
	tt.Frame()
	darkBg := tt.Image().RGBAAt(600, 720)

	if lightBg.R <= darkBg.R {
		t.Fatalf("expected light mode canvas (%+v) to be brighter than dark mode canvas (%+v)", lightBg, darkBg)
	}

	// Verify "浅色" / "深色" labels (no "白天" / "黑夜") and Accent Color swatches in Settings.
	if !tt.HasText("外观与语言") || !tt.HasText("浅色") || !tt.HasText("深色") || !tt.HasText("主题色") {
		t.Fatalf("expected Appearance ('浅色'/'深色') & Accent Color ('主题色') in Settings, got: %v", tt.Texts())
	}
	for _, oldTerm := range []string{"白天", "黑夜"} {
		for _, txt := range tt.Texts() {
			if strings.Contains(txt, oldTerm) {
				t.Fatalf("unexpected legacy theme label %q found in %q", oldTerm, txt)
			}
		}
	}

	// Click Violet ("紫罗兰") accent swatch and verify persistence + state update.
	if err := tt.Click("紫罗兰"); err != nil {
		t.Fatalf("click Violet accent swatch: %v", err)
	}
	if a.settings.AccentColor != config.AccentViolet {
		t.Fatalf("expected AccentColor=%q, got %q", config.AccentViolet, a.settings.AccentColor)
	}
	savedCfg, err := config.Load(a.settings.ConfigDir)
	if err != nil || savedCfg.AccentColor != config.AccentViolet {
		t.Fatalf("expected persisted AccentColor=violet, got %+v (err=%v)", savedCfg.AccentColor, err)
	}

	// Switch Display Language via ui.Select dropdown ("界面语言" -> "English", "日本語", "Français", "Deutsch", "Español", "繁體中文").
	selInfo, okSel := tt.AccessByRole(ui.RolePopUpButton, "界面语言")
	if !okSel {
		t.Fatalf("expected Display Language RolePopUpButton to be accessible")
	}
	tt.ClickAt(selInfo.Bounds.X+selInfo.Bounds.W/2, selInfo.Bounds.Y+selInfo.Bounds.H/2)
	if err := tt.Click("English"); err != nil {
		t.Fatalf("select English from language dropdown: %v", err)
	}
	if !tt.HasText("Appearance & Language") || !tt.HasText("Backups") || !tt.HasText("Accent Color") {
		t.Fatalf("expected English UI after selecting English, got: %v", tt.Texts())
	}

	if err := tt.Click("Light"); err != nil {
		t.Fatalf("switch theme to Light: %v", err)
	}
	forcedLightBg := tt.Image().RGBAAt(600, 720)
	if forcedLightBg.R <= darkBg.R {
		t.Fatalf("expected forced Light theme (%+v) to override OS dark mode (%+v)", forcedLightBg, darkBg)
	}

	// Verify multi-language rendering & ResolveLanguage for ja, fr, de, es, zh-Hant.
	langChecks := []struct {
		code    string
		osTag   string
		expect1 string
		expect2 string
	}{
		{config.LangJA, "ja-JP", "外観と言語", "アクセントカラー"},
		{config.LangFR, "fr-FR", "Apparence et langue", "Couleur d’accentuation"},
		{config.LangDE, "de-DE", "Darstellung & Sprache", "Akzentfarbe"},
		{config.LangES, "es-ES", "Apariencia e idioma", "Color de énfasis"},
		{config.LangZhHant, "zh-TW", "外觀與語言", "主題色"},
	}
	for _, lc := range langChecks {
		if got := ResolveLanguage(config.LangSystem, []string{lc.osTag}); got != lc.code {
			t.Fatalf("ResolveLanguage(system, %q) = %q, want %q", lc.osTag, got, lc.code)
		}
		a.settings.Language = lc.code
		a.syncSettingsDraft()
		tt.Frame()
		if !tt.HasText(lc.expect1) || !tt.HasText(lc.expect2) {
			t.Fatalf("expected %s strings (%q, %q), got: %v", lc.code, lc.expect1, lc.expect2, tt.Texts())
		}
	}
}

func TestWorkspaceNavigationAndKopiaLensSearch(t *testing.T) {
	cfg := config.DefaultSettings()
	cfg.Language = config.LangEN
	cfg.Theme = config.ThemeDark
	a := newRichTestApp(t, cfg, []string{"en-US"})

	tt := ui.NewTester(a.View, 1220, 780)

	for _, navLabel := range []string{"Snapshots", "Policies", "Tasks & Logs", "Repository", "Search"} {
		if err := tt.Click(navLabel); err != nil {
			t.Fatalf("navigate to %s: %v", navLabel, err)
		}
	}

	if !tt.HasText("Designs/architecture-spec.pdf") || !tt.HasText("Archive/accidental-delete.key") {
		t.Fatalf("expected seeded search results in table, got texts: %v", tt.Texts())
	}

	// Click a row to open the KopiaLens-style Version History Inspector drawer.
	if err := tt.Click("Designs/architecture-spec.pdf"); err != nil {
		t.Fatalf("select search item row: %v", err)
	}
	if !tt.HasText("Version History") || !tt.HasText("obj-spec-v2") || !tt.HasText("obj-spec-v1") {
		t.Fatalf("expected Version History drawer to open with both versions, got: %v", tt.Texts())
	}

	// Toggle "Deleted only" filter and verify live file disappears while deleted file stays.
	if err := tt.Click("Deleted only"); err != nil {
		t.Fatalf("toggle Deleted only checkbox: %v", err)
	}
	if tt.HasText("Designs/architecture-spec.pdf") {
		t.Fatalf("expected live file to be filtered out when Deleted only is enabled")
	}
	if !tt.HasText("Archive/accidental-delete.key") {
		t.Fatalf("expected deleted file to remain visible when Deleted only is enabled")
	}

	// Open Connect / Create Repository modal from sidebar "Add Repository".
	if err := tt.Click("Add Repository"); err != nil {
		t.Fatalf("open Add Repository modal: %v", err)
	}
	if !tt.HasText("Connect Existing") || !tt.HasText("Create New") {
		t.Fatalf("expected Connect/Create Repository wizard modal, got: %v", tt.Texts())
	}
}

func TestRenderEveryViewAndModal(t *testing.T) {
	// 1. Chinese Light & Dark across all views
	cfgZh := config.DefaultSettings()
	cfgZh.Language = config.LangZhHans
	cfgZh.Theme = config.ThemeLight
	aZh := newRichTestApp(t, cfgZh, []string{"zh-Hans"})
	ttZh := ui.NewTester(aZh.View, 1240, 800)

	saveTestPNG(t, "01_backups_zh_light", ttZh)

	if err := ttZh.Click("快照浏览"); err != nil {
		t.Fatalf("zh snapshots: %v", err)
	}
	saveTestPNG(t, "02_snapshots_zh_light", ttZh)

	if err := ttZh.Click("全局搜索"); err != nil {
		t.Fatalf("zh search: %v", err)
	}
	_ = ttZh.Click("Designs/architecture-spec.pdf")
	saveTestPNG(t, "03_search_inspector_zh_light", ttZh)

	// Switch to Dark theme for remaining Chinese views (enable desktopRunning flag during render to verify desktop-only action buttons visually)
	aZh.settings.Theme = config.ThemeDark
	aZh.desktopRunning = true
	ttZh.Frame()
	saveTestPNG(t, "04_search_inspector_zh_dark", ttZh)

	if err := ttZh.Click("备份源"); err != nil {
		t.Fatalf("zh backups dark: %v", err)
	}
	saveTestPNG(t, "23_backups_zh_dark", ttZh)

	// Render Snapshot Diff view (located right below Global Search; defaults to Latest -> Current Local Folder)
	if err := ttZh.Click("快照对比"); err != nil {
		t.Fatalf("zh snapshot diff: %v", err)
	}
	saveTestPNG(t, "20_snapshot_diff_zh_dark", ttZh)
	_ = ttZh.Click("最新 vs 上一次")
	saveTestPNG(t, "25_snapshot_diff_hist_zh_dark", ttZh)

	// Render Snapshot Browser with Custom Date Range + Year Jumper
	if err := ttZh.Click("快照浏览"); err != nil {
		t.Fatalf("zh snapshots dark: %v", err)
	}
	aZh.snapTimePreset = timePresetCustom
	aZh.snapDateFromText = "2024-01-01"
	aZh.snapDateToText = "2026-12-31"
	ttZh.Frame()
	saveTestPNG(t, "21_snapshots_filters_zh_dark", ttZh)
	aZh.snapTimePreset = timePresetAll
	aZh.snapDateFromText = ""
	aZh.snapDateToText = ""

	// Render Global Search with Custom Date Range + Advanced Filters Drawer
	if err := ttZh.Click("全局搜索"); err != nil {
		t.Fatalf("zh search advanced: %v", err)
	}
	aZh.searchSelectedRow = -1
	aZh.searchTimePreset = timePresetCustom
	aZh.searchDateFromText = "2024-01-01"
	aZh.searchDateToText = "2026-12-31"
	aZh.searchShowAdvanced = true
	aZh.searchDirty = true
	ttZh.Frame()
	saveTestPNG(t, "22_search_date_range_zh_dark", ttZh)
	aZh.searchTimePreset = timePresetAll
	aZh.searchShowAdvanced = false
	aZh.searchDirty = true

	if err := ttZh.Click("备份策略"); err != nil {
		t.Fatalf("zh policies: %v", err)
	}
	saveTestPNG(t, "05_policies_retention_zh_dark", ttZh)
	_ = ttZh.Click("自动计划")
	saveTestPNG(t, "06_policies_schedule_zh_dark", ttZh)

	if err := ttZh.Click("任务与日志"); err != nil {
		t.Fatalf("zh tasks: %v", err)
	}
	_ = ttZh.Click("/Users/alex/Projects")
	saveTestPNG(t, "07_tasks_zh_dark", ttZh)

	if err := ttZh.Click("仓库与维护"); err != nil {
		t.Fatalf("zh repo: %v", err)
	}
	saveTestPNG(t, "08_repo_stats_zh_dark", ttZh)
	_ = ttZh.Click("仓库维护与垃圾回收")
	saveTestPNG(t, "09_repo_maintenance_zh_dark", ttZh)

	if err := ttZh.Click("应用设置"); err != nil {
		t.Fatalf("zh settings: %v", err)
	}
	saveTestPNG(t, "10_settings_zh_dark", ttZh)

	// Connect/Create Wizard Modal in Connect Existing mode (exact match for Screenshot 1), Rclone mode, & Create New + Advanced mode
	aZh.desktopRunning = true
	_ = ttZh.Click("添加仓库")
	saveTestPNG(t, "17_connect_modal_existing_zh_dark", ttZh)
	aZh.connectForm.provider = "rclone"
	ttZh.Frame()
	saveTestPNG(t, "26_connect_rclone_zh_dark", ttZh)
	aZh.connectForm.provider = "filesystem"
	aZh.desktopRunning = false
	_ = ttZh.Click("创建新仓库")
	_ = ttZh.Click("高级加密与分块参数")
	saveTestPNG(t, "11_connect_modal_zh_dark", ttZh)

	// 2. English Light views & Restore modal
	cfgEn := config.DefaultSettings()
	cfgEn.Language = config.LangEN
	cfgEn.Theme = config.ThemeLight
	aEn := newRichTestApp(t, cfgEn, []string{"en-US"})
	aEn.desktopRunning = true
	ttEn := ui.NewTester(aEn.View, 1240, 800)
	saveTestPNG(t, "12_backups_en_light", ttEn)

	_ = ttEn.Click("Snapshot Diff")
	saveTestPNG(t, "24_snapshot_diff_en_light", ttEn)

	_ = ttEn.Click("Snapshots")
	_ = ttEn.Click("Restore…")
	saveTestPNG(t, "13_restore_modal_en_light", ttEn)
	aEn.desktopRunning = false

	// 3. macOS traffic-light inset header alignment & UI Scale presets (Small / Medium / Large) + Collapsed Icon Rail
	cfgScale := config.DefaultSettings()
	cfgScale.Language = config.LangZhHans
	cfgScale.Theme = config.ThemeDark
	aScale := newRichTestApp(t, cfgScale, []string{"zh-Hans"})
	aScale.SetDisplayBounds(1728, 1117)
	ttScale := ui.NewTester(aScale.View, 1240, 800)
	ttScale.SetTitleBar(ui.TitleBar{Height: 46, Left: 76})
	saveTestPNG(t, "19_adaptive_sidebar_zh_dark", ttScale)

	// Collapse sidebar to icon-only rail and render
	ttScale.Key(ui.Cmd, ui.KeyB)
	_ = ttScale.Click("快照浏览")
	saveTestPNG(t, "18_collapsed_icon_rail_zh_dark", ttScale)

	// Expand sidebar again for scale preset screenshots
	ttScale.Key(ui.Cmd, ui.KeyB)
	_ = ttScale.Click("应用设置")
	saveTestPNG(t, "14_settings_macos_bar_medium_zh_dark", ttScale)

	_ = ttScale.Click("大")
	saveTestPNG(t, "15_settings_macos_bar_large_zh_dark", ttScale)

	_ = ttScale.Click("小")
	saveTestPNG(t, "16_settings_macos_bar_small_zh_dark", ttScale)
}

func TestSidebarRailAdaptiveResizeAndModalSpacing(t *testing.T) {
	cfg := config.DefaultSettings()
	cfg.Language = config.LangZhHans
	cfg.Theme = config.ThemeDark
	a := newRichTestApp(t, cfg, []string{"zh-Hans"})
	tt := ui.NewTester(a.View, 1240, 800)
	tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 76})
	tt.Frame()

	// 1. Redundant "工作区" / "Workspace" section header must NOT appear.
	if tt.HasText("工作区") || tt.HasText("Workspace") {
		t.Fatalf("expected redundant Workspace header to be removed, got texts: %v", tt.Texts())
	}

	// 2. Default sidebar width is compact & adaptive (narrower than old 232px).
	initialW := a.sidebarLiveWidth
	if initialW < config.SidebarMinWidth || initialW > 205 {
		t.Fatalf("expected compact adaptive sidebar width in [144, 205], got %v", initialW)
	}

	// 2b. Expanded sidebar pins "应用设置" to the very bottom and gives spacious row pitch (>= 40 DIPs).
	expSettingsRect, okSet := tt.Find("应用设置")
	if !okSet || expSettingsRect.Y < 720 {
		t.Fatalf("expected expanded Settings button pinned at bottom of sidebar (Y >= 720), got %+v (ok=%v)", expSettingsRect, okSet)
	}
	bkpRect, okB1 := tt.Find("备份源")
	snapRect, okB2 := tt.Find("快照浏览")
	if !okB1 || !okB2 || (snapRect.Y-bkpRect.Y) < 40 {
		t.Fatalf("expected spacious vertical row pitch >= 40 DIPs in expanded sidebar, got pitch=%v (bkp=%+v, snap=%+v)", snapRect.Y-bkpRect.Y, bkpRect, snapRect)
	}

	// Record exact (X, Y, H) of every sidebar button in expanded state BEFORE resizing/collapsing.
	findSidebarBtn := func(label string) (ui.Rect, bool) {
		if info, ok := tt.AccessByRole(ui.RoleButton, label); ok {
			return info.Bounds, true
		}
		if info, ok := tt.Access(label); ok {
			return info.Bounds, true
		}
		return tt.Find(label)
	}
	sidebarItems := []string{"备份仓库", "备份源", "快照浏览", "全局搜索", "快照对比", "备份策略", "任务与日志", "仓库与维护", "应用设置"}
	expandedRects := make(map[string]ui.Rect, len(sidebarItems))
	for _, label := range sidebarItems {
		r, ok := findSidebarBtn(label)
		if !ok {
			t.Fatalf("expected expanded sidebar button %q to be accessible", label)
		}
		expandedRects[label] = r
	}

	// 3. Dragging divider hard left clamps smoothly at SidebarMinWidth (144) and NEVER bounces back to 232!
	tt.Press(initialW, 300)
	tt.Move(20, 300)
	tt.Release(20, 300)
	if a.sidebarLiveWidth != config.SidebarMinWidth {
		t.Fatalf("expected dragging hard left to clamp solidly at %v without bouncing back, got %v", config.SidebarMinWidth, a.sidebarLiveWidth)
	}

	// Dragging divider hard right clamps smoothly at SidebarMaxWidth (340).
	tt.Press(a.sidebarLiveWidth, 300)
	tt.Move(520, 300)
	tt.Release(520, 300)
	if a.sidebarLiveWidth != config.SidebarMaxWidth {
		t.Fatalf("expected dragging hard right to clamp solidly at %v, got %v", config.SidebarMaxWidth, a.sidebarLiveWidth)
	}

	// 4. Collapsing sidebar (Cmd+B) shows 52px icon-only rail with ZERO (0.0px) icon position shift!
	tt.Key(ui.Cmd, ui.KeyB)
	if !a.settings.SidebarCollapsed {
		t.Fatalf("expected SidebarCollapsed=true after Cmd+B")
	}
	if tt.HasText("Mygopia") {
		t.Fatalf("expected expanded sidebar title 'Mygopia' to be hidden in icon-only rail mode")
	}
	for _, navText := range sidebarItems {
		r, ok := findSidebarBtn(navText)
		if !ok {
			t.Fatalf("expected icon-only rail button %q to be accessible", navText)
		}
		if r.W > 40 || r.X+r.W > sidebarRailWidth {
			t.Fatalf("expected %q to be a compact icon button inside 52px rail, got rect=%+v", navText, r)
		}
		exp := expandedRects[navText]
		if r.X != exp.X || r.Y != exp.Y || r.H != exp.H {
			t.Fatalf("sidebar icon %q shifted between expanded (%+v) and collapsed (%+v); want 0.0px shift", navText, exp, r)
		}
	}
	settingsRect, _ := tt.Find("应用设置")
	if settingsRect.Y < 720 {
		t.Fatalf("expected Settings icon anchored at bottom of full-height rail (Y >= 720), got %+v", settingsRect)
	}
	// Clicking icon button via its accessible label still switches section cleanly!
	if err := tt.Click("快照浏览"); err != nil {
		t.Fatalf("click icon-only rail item 快照浏览: %v", err)
	}
	if a.activeNav != navSnapshots {
		t.Fatalf("expected activeNav=snapshots after clicking rail icon, got %q", a.activeNav)
	}

	// Expand sidebar again and open "添加仓库" modal to verify vertical spacing (Screenshot 1 fix).
	tt.Key(ui.Cmd, ui.KeyB)
	if err := tt.Click("添加仓库"); err != nil {
		t.Fatalf("click 添加仓库: %v", err)
	}
	segRect, ok1 := tt.Find("连接已有仓库")
	lblRect, ok2 := tt.Find("仓库显示名称")
	if !ok1 || !ok2 {
		t.Fatalf("expected modal elements 连接已有仓库 (%v) and 仓库显示名称 (%v)", ok1, ok2)
	}
	verticalGap := lblRect.Y - (segRect.Y + segRect.H)
	if verticalGap < 12 {
		t.Fatalf("expected >= 12 DIPs gap between Segmented control and Repository Name field, got %v (seg=%+v, lbl=%+v)", verticalGap, segRect, lblRect)
	}
}

func TestUIScaleAndResolutionAdaptation(t *testing.T) {
	// 1. Resolution adaptation curves
	zCompact := ComputeEffectiveZoom(config.ScaleMedium, 1280, 800, 1.0)
	zStandard := ComputeEffectiveZoom(config.ScaleMedium, 1440, 900, 1.0)
	zQHD := ComputeEffectiveZoom(config.ScaleMedium, 2560, 1440, 1.0)
	z4K := ComputeEffectiveZoom(config.ScaleMedium, 3840, 2160, 1.0)
	if !(zCompact < zStandard && zStandard < zQHD && zQHD < z4K) {
		t.Fatalf("expected monotonic resolution adaptation, got compact=%v std=%v qhd=%v 4k=%v", zCompact, zStandard, zQHD, z4K)
	}

	// 2. Small < Medium < Large presets
	zSmall := ComputeEffectiveZoom(config.ScaleSmall, 1728, 1117, 1.0)
	zMed := ComputeEffectiveZoom(config.ScaleMedium, 1728, 1117, 1.0)
	zLarge := ComputeEffectiveZoom(config.ScaleLarge, 1728, 1117, 1.0)
	if !(zSmall < zMed && zMed == 1.0 && zMed < zLarge) {
		t.Fatalf("expected small < medium(1.0) < large, got small=%v med=%v large=%v", zSmall, zMed, zLarge)
	}

	// 3. Interactive Settings UI: Small / Medium / Large, Shortcuts, Launch at Login, and macOS Traffic Light midline
	cfg := config.DefaultSettings()
	cfg.Language = config.LangZhHans
	cfg.Theme = config.ThemeDark
	a := newRichTestApp(t, cfg, []string{"zh-Hans"})
	a.SetDisplayBounds(1728, 1117)

	tt := ui.NewTester(a.View, 1240, 800)
	tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 76})

	// Ensure unnecessary adjectives ("克制" etc.) never appear in UI
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "克制") || strings.Contains(txt, "符合直觉") {
			t.Fatalf("unexpected promotional copy in UI: %q", txt)
		}
	}

	if err := tt.Click("应用设置"); err != nil {
		t.Fatalf("navigate to settings: %v", err)
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "克制") || strings.Contains(txt, "符合直觉") {
			t.Fatalf("unexpected promotional copy in Settings UI: %q", txt)
		}
	}

	// Verify macOS traffic lights (centered at Y=22 window DIPs, X<=76) align with "Mygopia" & toolbar title across all 3 scales!
	assertHeaderAligned := func(scaleName string) {
		t.Helper()
		appTitle, ok := tt.Find("Mygopia")
		if !ok {
			t.Fatalf("[%s] Mygopia title not found", scaleName)
		}
		if appTitle.X < 80 {
			t.Fatalf("[%s] expected Mygopia title inset past macOS traffic lights (X >= 80), got X=%v", scaleName, appTitle.X)
		}
		midY := appTitle.Y + appTitle.H/2
		if midY < 20.5 || midY > 23.5 {
			t.Fatalf("[%s] expected Mygopia title vertically centered on windowBarMid=22, got midY=%v (rect=%+v)", scaleName, midY, appTitle)
		}
	}

	assertHeaderAligned("medium-default")
	medRect, _ := tt.Find("外观与语言")

	// Switch to Large ("大")
	if err := tt.Click("大"); err != nil {
		t.Fatalf("click Large scale: %v", err)
	}
	if a.settings.UIScale != config.ScaleLarge {
		t.Fatalf("expected ScaleLarge, got %q", a.settings.UIScale)
	}
	assertHeaderAligned("large")
	largeRect, _ := tt.Find("外观与语言")
	if largeRect.H <= medRect.H {
		t.Fatalf("expected Large text box height (%v) > Medium (%v)", largeRect.H, medRect.H)
	}

	// Switch to Small ("小")
	if err := tt.Click("小"); err != nil {
		t.Fatalf("click Small scale: %v", err)
	}
	if a.settings.UIScale != config.ScaleSmall {
		t.Fatalf("expected ScaleSmall, got %q", a.settings.UIScale)
	}
	assertHeaderAligned("small")
	smallRect, _ := tt.Find("外观与语言")
	if smallRect.H >= medRect.H {
		t.Fatalf("expected Small text box height (%v) < Medium (%v)", smallRect.H, medRect.H)
	}

	// Keyboard shortcuts: Cmd+0 resets to Medium, Cmd+= steps to Large, Cmd+- steps back to Medium
	tt.Key(ui.Cmd, ui.Key0)
	if a.settings.UIScale != config.ScaleMedium {
		t.Fatalf("expected Cmd+0 to reset scale to Medium, got %q", a.settings.UIScale)
	}
	tt.Key(ui.Cmd, ui.KeyEqual)
	if a.settings.UIScale != config.ScaleLarge {
		t.Fatalf("expected Cmd+= to step scale to Large, got %q", a.settings.UIScale)
	}
	tt.Key(ui.Cmd, ui.KeyMinus)
	if a.settings.UIScale != config.ScaleMedium {
		t.Fatalf("expected Cmd+- to step scale back to Medium, got %q", a.settings.UIScale)
	}

	// Toggle "登录系统时开机自启动" (Launch at system login) and verify persistence to disk
	if a.settings.OpenAtLogin {
		t.Fatalf("expected OpenAtLogin false initially")
	}
	if err := tt.Click("登录系统时开机自启动"); err != nil {
		t.Fatalf("click OpenAtLogin checkbox: %v", err)
	}
	if !a.settings.OpenAtLogin {
		t.Fatalf("expected OpenAtLogin true after clicking checkbox")
	}
	saved, err := config.Load(a.userDataDir)
	if err != nil || !saved.OpenAtLogin {
		t.Fatalf("expected OpenAtLogin persisted to settings.json, got %+v (err=%v)", saved, err)
	}
}

func TestRefreshSnapshotDiffDateRangeAndPagination(t *testing.T) {
	cfg := config.DefaultSettings()
	cfg.Language = config.LangZhHans
	cfg.Theme = config.ThemeDark
	a := newRichTestApp(t, cfg, []string{"zh-Hans"})
	tt := ui.NewTester(a.View, 1260, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 76})
	tt.Frame()

	// 1. Verify Cmd+R and clicking Refresh button work and show "已刷新最新状态" toast.
	a.policyLoadedTarget = "stale-target"
	a.repoDescLoadedID = "stale-repo"
	tt.Key(ui.Cmd, ui.KeyR)
	tt.Frame()
	if a.policyLoadedTarget != "" || a.repoDescLoadedID != "" {
		t.Fatalf("expected Cmd+R to invalidate cached form state")
	}
	if !tt.HasText("已刷新最新状态") {
		t.Fatalf("expected Cmd+R to display '已刷新最新状态' confirmation toast, got texts: %v", tt.Texts())
	}

	// Also verify clicking the toolbar Refresh button directly
	a.policyLoadedTarget = "stale-target-2"
	if err := tt.Click("刷新"); err != nil {
		t.Fatalf("click toolbar Refresh button: %v", err)
	}
	tt.Frame()
	if a.policyLoadedTarget != "" {
		t.Fatalf("expected clicking Refresh button to invalidate cached form state")
	}

	// 2. Verify "快照对比" (Snapshot Diff) is positioned immediately below "全局搜索" (Search) in sidebar
	searchRect, okSearch := tt.Find("全局搜索")
	diffRect, okDiff := tt.Find("快照对比")
	policyRect, okPol := tt.Find("备份策略")
	if !okSearch || !okDiff || !okPol {
		t.Fatalf("expected 全局搜索 (%v), 快照对比 (%v), 备份策略 (%v) in sidebar", okSearch, okDiff, okPol)
	}
	if !(diffRect.Y > searchRect.Y && diffRect.Y < policyRect.Y) {
		t.Fatalf("expected 快照对比 (Y=%v) to sit between 全局搜索 (Y=%v) and 备份策略 (Y=%v)", diffRect.Y, searchRect.Y, policyRect.Y)
	}

	// Navigate to Snapshot Diff and verify the NEW DEFAULT: Latest Snapshot (snap-docs-3) -> Current Local Folder ("当前本地目录（待备份状态）")
	if err := tt.Click("快照对比"); err != nil {
		t.Fatalf("click 快照对比: %v", err)
	}
	if a.activeNav != navDiff {
		t.Fatalf("expected activeNav=diff, got %q", a.activeNav)
	}
	if a.diffBaseSnapID != "snap-docs-3" || a.diffTargetSnapID != diffTargetLocalDir || a.diffActivePreset != diffPresetLocal {
		t.Fatalf("expected default diff pair to be snap-docs-3 -> __LOCAL_DIR__ (diffPresetLocal), got %s -> %s (preset=%d)", a.diffBaseSnapID, a.diffTargetSnapID, a.diffActivePreset)
	}
	if !tt.HasText("当前本地目录（待备份状态）") || !tt.HasText("本地当前 vs 最新") {
		t.Fatalf("expected local working-tree target & preset in Snapshot Diff, got: %v", tt.Texts())
	}
	// Verify local Added (Drafts/Q4-Release-Checklist.md), Changed (Designs/architecture-spec.pdf), Deleted (2026-Product-Roadmap.pdf), and .kopiaignore exclusion (scratch.tmp)
	if !tt.HasText("Drafts/Q4-Release-Checklist.md") || !tt.HasText("Designs/architecture-spec.pdf") || !tt.HasText("2026-Product-Roadmap.pdf") {
		t.Fatalf("expected Added, Changed, and Deleted items vs local folder, got: %v", tt.Texts())
	}
	if tt.HasText("scratch.tmp") {
		t.Fatalf("expected scratch.tmp to be excluded by .kopiaignore during local folder diff")
	}
	if !tt.HasText("+ 新增") || !tt.HasText("~ 修改") || !tt.HasText("- 已删除") {
		t.Fatalf("expected diff change pills (+ 新增, ~ 修改, - 已删除), got: %v", tt.Texts())
	}

	// Verify all 5 quick preset buttons remain aligned on one single horizontal row (never wrapping orphan buttons)
	localPresetRect, okLocalPreset := tt.Find("本地当前 vs 最新")
	oldestRect, okOldest := tt.Find("最新 vs 最早")
	if !okLocalPreset || !okOldest || localPresetRect.Y != oldestRect.Y {
		t.Fatalf("expected all 5 diff preset buttons on one horizontal row, got local=%+v (ok=%v) oldest=%+v (ok=%v)", localPresetRect, okLocalPreset, oldestRect, okOldest)
	}

	// Switch to Latest vs Previous ("最新 vs 上一次": snap-docs-2 -> snap-docs-3) and verify historical snapshot diff
	if err := tt.Click("最新 vs 上一次"); err != nil {
		t.Fatalf("click 最新 vs 上一次 preset: %v", err)
	}
	if a.diffBaseSnapID != "snap-docs-2" || a.diffTargetSnapID != "snap-docs-3" || a.diffActivePreset != diffPresetPrev {
		t.Fatalf("expected snap-docs-2 -> snap-docs-3 after clicking 最新 vs 上一次, got %s -> %s (preset=%d)", a.diffBaseSnapID, a.diffTargetSnapID, a.diffActivePreset)
	}
	if !tt.HasText("2026-Product-Roadmap.pdf") || !tt.HasText("Designs/architecture-spec.pdf") || !tt.HasText("Archive/accidental-delete.key") {
		t.Fatalf("expected Added, Changed, and Deleted items in historical Snapshot Diff table, got: %v", tt.Texts())
	}

	// Filter by Added ("新增") only
	if err := tt.Click("新增"); err != nil {
		t.Fatalf("click 新增 filter in diff view: %v", err)
	}
	if !tt.HasText("2026-Product-Roadmap.pdf") || tt.HasText("Archive/accidental-delete.key") {
		t.Fatalf("expected only added files when 新增 filter is selected, got: %v", tt.Texts())
	}
	// Reset diff filter back to All Changes ("全部变更")
	_ = tt.Click("全部变更")

	// Verify Swap button is gone and chronological Old -> New order is strictly enforced
	if tt.HasText("⇄ 交换") {
		t.Fatalf("expected confusing Swap button to be removed in favor of fixed Old -> New direction")
	}
	// Passing inverted (newer, older) or identical (latest, latest) IDs to openDiffForSource auto-normalizes to Old -> New
	srcDocsInfo := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: "/Users/alex/Documents"}
	a.openDiffForSource(srcDocsInfo, "snap-docs-3", "snap-docs-1")
	tt.Frame()
	if a.diffBaseSnapID != "snap-docs-1" || a.diffTargetSnapID != "snap-docs-3" {
		t.Fatalf("expected inverted IDs to auto-normalize to snap-docs-1 (older) -> snap-docs-3 (newer), got %s -> %s", a.diffBaseSnapID, a.diffTargetSnapID)
	}
	a.openDiffForSource(srcDocsInfo, "snap-docs-3", "snap-docs-3")
	tt.Frame()
	if a.diffBaseSnapID != "snap-docs-2" || a.diffTargetSnapID != "snap-docs-3" {
		t.Fatalf("expected identical latest IDs to auto-normalize to snap-docs-2 -> snap-docs-3, got %s -> %s", a.diffBaseSnapID, a.diffTargetSnapID)
	}

	// Test "最新 vs 7天前", "最新 vs 最早", and "本地当前 vs 最新" presets & single active preset tracking
	if err := tt.Click("最新 vs 7天前"); err != nil {
		t.Fatalf("click 最新 vs 7天前 preset: %v", err)
	}
	if a.diffActivePreset != diffPreset7d {
		t.Fatalf("expected diffActivePreset=diffPreset7d, got %d", a.diffActivePreset)
	}
	if err := tt.Click("最新 vs 最早"); err != nil {
		t.Fatalf("click 最新 vs 最早 preset: %v", err)
	}
	if a.diffBaseSnapID != "snap-docs-1" || a.diffTargetSnapID != "snap-docs-3" || a.diffActivePreset != diffPresetOldest {
		t.Fatalf("expected 最新 vs 最早 to select snap-docs-1 vs snap-docs-3 (preset=%d), got %s vs %s", a.diffActivePreset, a.diffBaseSnapID, a.diffTargetSnapID)
	}
	// Reset back to Latest vs Previous ("最新 vs 上一次": snap-docs-2 vs snap-docs-3) for historical filter checks below
	if err := tt.Click("最新 vs 上一次"); err != nil {
		t.Fatalf("click 最新 vs 上一次 preset: %v", err)
	}
	if a.diffBaseSnapID != "snap-docs-2" || a.diffActivePreset != diffPresetPrev {
		t.Fatalf("expected base snapshot snap-docs-2 and diffPresetPrev, got %s (preset=%d)", a.diffBaseSnapID, a.diffActivePreset)
	}

	// Verify filtering Snapshot Diff by note ("Annual archive") or date immediately selects snap-docs-1 and updates diff
	a.diffSnapSearch = "Annual archive"
	a.applyDiffSnapshotFilterSelection(a.filteredDiffSnapshots())
	tt.Frame()
	if a.diffBaseSnapID != "snap-docs-1" {
		t.Fatalf("expected diffSnapSearch='Annual archive' to automatically select snap-docs-1 as base, got %s", a.diffBaseSnapID)
	}
	if !tt.HasText("1 / 3 个快照") {
		t.Fatalf("expected '1 / 3 个快照' match counter when filtering diff snapshots by note, got: %v", tt.Texts())
	}
	a.diffSnapSearch = ""
	tt.Frame()

	// Verify filtering the bottom Snapshot Diff table by modification date/time string (fixture date 2026-11-09)
	const roadmapModDate = "2026-11-09"
	a.diffFilterQuery = roadmapModDate
	tt.Frame()
	if !tt.HasText("2026-Product-Roadmap.pdf") {
		t.Fatalf("expected diffFilterQuery=%q (date) to match modified timestamp of 2026-Product-Roadmap.pdf", roadmapModDate)
	}
	a.diffFilterQuery = ""
	tt.Frame()

	// Verify activeRefreshStatus reports spinning=true and status text while refreshing or comparing
	a.refreshing = true
	a.refreshPhase = tr("Syncing repository…")
	tt.Frame()
	prof, st := a.activeRepoState()
	if spinning, label := a.activeRefreshStatus(prof, st); !spinning || label != "正在同步仓库…" {
		t.Fatalf("expected activeRefreshStatus spinning=true label='正在同步仓库…', got spinning=%v label=%q", spinning, label)
	}
	if !tt.HasText("正在同步仓库…") {
		t.Fatalf("expected top toolbar to display '正在同步仓库…' pill while refreshing, got: %v", tt.Texts())
	}
	a.refreshing = false
	a.refreshPhase = ""
	tt.Frame()

	// 3. Verify Time Range & Multi-Year Quick Jumper & Lazy Loading in Snapshot Timeline ("快照浏览")
	if err := tt.Click("快照浏览"); err != nil {
		t.Fatalf("click 快照浏览: %v", err)
	}
	// Filter timeline to oldest year (fixture 2024) without scrolling
	const oldestYear = "2024"
	a.snapYearFilter = oldestYear
	tt.Frame()
	if !tt.HasText("Annual archive baseline") {
		t.Fatalf("expected oldest year filter (%s) to show Annual archive baseline snapshot", oldestYear)
	}
	a.snapYearFilter = ""

	// Verify Snapshot Timeline lazy loading ("加载更多" / "展开全部") when > 40 snapshots exist
	manySnaps := make([]*kopia.Snapshot, 0, 95)
	baseNow := time.Now()
	for i := range 95 {
		manySnaps = append(manySnaps, &kopia.Snapshot{
			ID:          "snap-many-" + strconv.Itoa(i),
			StartTime:   kopia.FlexTime{Time: baseNow.Add(-time.Duration(i) * 24 * time.Hour)},
			RootID:      "kroot-many-" + strconv.Itoa(i),
			Description: "BatchSnap-" + strconv.Itoa(i),
			Summary:     &kopia.DirectorySummary{TotalFileSize: 1024 * 1024, TotalFileCount: 10},
		})
	}
	a.snapshots = manySnaps
	a.snapDisplayLimit = defaultSnapshotPageSize // 40
	tt.Frame()
	if !tt.HasText("加载更多（剩余 55 项）") || !tt.HasText("展开全部（95 项）") {
		t.Fatalf("expected lazy-load buttons for 95 snapshots, got: %v", tt.Texts())
	}
	if err := tt.Click("加载更多（剩余 55 项）"); err != nil {
		t.Fatalf("click 加载更多 on snapshot timeline: %v", err)
	}
	if a.snapDisplayLimit != 80 {
		t.Fatalf("expected snapDisplayLimit=80 after clicking Load More once, got %d", a.snapDisplayLimit)
	}

	// 4. Verify Global Search Date Range filtering, Pagination ("上一页 / 下一页"), and Version History lazy loading
	if err := tt.Click("全局搜索"); err != nil {
		t.Fatalf("click 全局搜索: %v", err)
	}
	// Filter search to the 2024-11-09 fixture snapshot date so only items present then match
	const twoYearsAgo = "2024-11-09"
	a.searchTimePreset = timePresetCustom
	a.searchDateFrom, _ = parseFlexibleDate(twoYearsAgo, false)
	a.searchDateTo, _ = parseFlexibleDate(twoYearsAgo, true)
	a.searchDirty = true
	tt.Frame()
	if tt.HasText("2026-Product-Roadmap.pdf") {
		t.Fatalf("expected recent-only file 2026-Product-Roadmap.pdf to be excluded by 2-year-old date range")
	}
	if !tt.HasText("Designs/architecture-spec.pdf") {
		t.Fatalf("expected Designs/architecture-spec.pdf (which existed 2 years ago) to match 2-year-old date range")
	}
	a.resetSearchFilters()
	tt.Frame()

	// Seed 210 search items to verify table pagination ("显示第 1–150 项 / 共 210 项", "第 1 / 2 页", "下一页")
	manyItems := make([]index.SearchItem, 0, 210)
	srcDocs := kopia.SourceInfo{Host: "workstation", UserName: "alex", Path: "/Users/alex/Documents"}
	for i := range 210 {
		manyItems = append(manyItems, index.SearchItem{
			RepoID:           "repo-primary",
			RepoName:         "Studio NAS",
			Source:           srcDocs,
			Path:             "Dataset/item-" + strconv.Itoa(i) + ".parquet",
			Name:             "item-" + strconv.Itoa(i) + ".parquet",
			Ext:              "parquet",
			OnDisk:           true,
			LatestSize:       int64(210-i) * 1024,
			MaxSize:          int64(210-i) * 1024,
			Versions:         1,
			SnapshotCount:    1,
			FirstSeen:        baseNow.Add(-time.Hour),
			LastSeen:         baseNow,
			LatestObjectID:   "obj-many-" + strconv.Itoa(i),
			LatestSnapshotID: "snap-docs-3",
		})
	}
	a.Engine().SeedRepo("repo-primary", "Studio NAS", manyItems)
	a.searchDirty = true
	tt.Frame()

	if !tt.HasText("显示 1–150 / 共 210 项") || !tt.HasText("第 1 / 2 页") {
		t.Fatalf("expected search pagination bar for 210 items, got: %v", tt.Texts())
	}
	if err := tt.Click("下一页"); err != nil {
		t.Fatalf("click 下一页 in search pagination: %v", err)
	}
	if a.searchPageOffset != 150 || !tt.HasText("显示 151–210 / 共 210 项") || !tt.HasText("第 2 / 2 页") {
		t.Fatalf("expected page 2 (151–210 of 210) after clicking Next, got offset=%d texts=%v", a.searchPageOffset, tt.Texts())
	}
}

func TestConnectProvidersRcloneSFTPAndPathExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()

	// 1. Rclone auto-fills detected rclone binary when rcloneExe is blank, and splits/expands rcloneArgs
	rcReq, err := buildConnectRequest(connectFormState{
		provider:         "rclone",
		password:         "secret",
		rcloneRemotePath: "gdrive:kopia-vault",
		rcloneArgs:       `--config "~/My Config/rclone.conf" --transfers=8`,
	}, "/opt/homebrew/bin/rclone")
	if err != nil {
		t.Fatalf("buildConnectRequest rclone: %v", err)
	}
	if rcReq.Storage.Config["rcloneExe"] != "/opt/homebrew/bin/rclone" {
		t.Fatalf("expected auto-detected rcloneExe='/opt/homebrew/bin/rclone', got %#v", rcReq.Storage.Config["rcloneExe"])
	}
	gotArgs, ok := rcReq.Storage.Config["rcloneArgs"].([]string)
	if !ok || len(gotArgs) != 3 || !strings.HasSuffix(gotArgs[1], filepath.Join("My Config", "rclone.conf")) || strings.HasPrefix(gotArgs[1], "~") {
		t.Fatalf("expected quoted & ~-expanded rcloneArgs, got %#v", rcReq.Storage.Config["rcloneArgs"])
	}

	// 2. SFTP validates missing known_hosts unless externalSSH is enabled, and expands ~/ paths
	_, errMissingKH := buildConnectRequest(connectFormState{
		provider:     "sftp",
		password:     "secret",
		sftpHost:     "nas.local",
		sftpUsername: "backup",
		sftpPath:     "/mnt/kopia",
		sftpKeyfile:  "~/.ssh/id_ed25519",
	}, "")
	if errMissingKH == nil {
		t.Fatalf("expected SFTP without knownHostsFile and without externalSSH to return validation error")
	}

	sftpReq, err := buildConnectRequest(connectFormState{
		provider:       "sftp",
		password:       "secret",
		sftpHost:       "nas.local",
		sftpUsername:   "backup",
		sftpPath:       "/mnt/kopia",
		sftpKeyfile:    "~/.ssh/id_ed25519",
		sftpKnownHosts: "~/.ssh/known_hosts",
	}, "")
	if err != nil {
		t.Fatalf("buildConnectRequest sftp: %v", err)
	}
	if home != "" && sftpReq.Storage.Config["keyfile"] != filepath.Join(home, ".ssh", "id_ed25519") {
		t.Fatalf("expected ~ expanded keyfile, got %#v", sftpReq.Storage.Config["keyfile"])
	}

	extSSHReq, err := buildConnectRequest(connectFormState{
		provider:        "sftp",
		password:        "secret",
		sftpHost:        "jump-target",
		sftpUsername:    "ops",
		sftpPath:        "/srv/kopia",
		sftpExternalSSH: true,
		sftpSSHCommand:  "ssh",
		sftpSSHArgs:     "-J bastion",
	}, "")
	if err != nil || extSSHReq.Storage.Config["externalSSH"] != true || extSSHReq.Storage.Config["sshArguments"] != "-J bastion" {
		t.Fatalf("unexpected externalSSH request: %+v (err=%v)", extSSHReq, err)
	}

	// SFTP rejects SSH option injection via leading '-' in host or username
	if _, errOpt := buildConnectRequest(connectFormState{
		provider:        "sftp",
		password:        "secret",
		sftpHost:        "-oProxyCommand=calc",
		sftpUsername:    "ops",
		sftpPath:        "/srv/kopia",
		sftpExternalSSH: true,
	}, ""); errOpt == nil {
		t.Fatalf("expected SFTP host starting with '-' to be rejected")
	}

	// 3. S3 doNotVerifyTLS + sessionToken & Azure SAS Token support
	s3Req, err := buildConnectRequest(connectFormState{
		provider:         "s3",
		password:         "secret",
		s3Bucket:         "minio-local",
		s3Endpoint:       "192.168.1.10:9000",
		s3DoNotVerifyTLS: true,
		s3SessionToken:   "sts-tok",
	}, "")
	if err != nil || s3Req.Storage.Config["doNotVerifyTLS"] != true || s3Req.Storage.Config["sessionToken"] != "sts-tok" {
		t.Fatalf("unexpected s3 request: %+v (err=%v)", s3Req, err)
	}

	azReq, err := buildConnectRequest(connectFormState{
		provider:    "azureBlob",
		password:    "secret",
		azContainer: "vault",
		azAccount:   "corpstorage",
		azSASToken:  "sv=2024-01-01& sig=xyz",
		azDomain:    "blob.core.chinacloudapi.cn",
	}, "")
	if err != nil || azReq.Storage.Config["sasToken"] == "" || azReq.Storage.Config["storageDomain"] != "blob.core.chinacloudapi.cn" {
		t.Fatalf("unexpected azure SAS request: %+v (err=%v)", azReq, err)
	}
}

func TestMenuBarVisibilitySetting(t *testing.T) {
	cfg := config.DefaultSettings()
	cfg.Language = config.LangZhHans
	cfg.Theme = config.ThemeDark
	if !cfg.ShowInMenuBar {
		t.Fatalf("expected DefaultSettings().ShowInMenuBar to be true by default")
	}

	a := newRichTestApp(t, cfg, []string{"zh-CN"})
	tt := ui.NewTester(a.View, 1220, 780)
	tt.SetDark(true)
	tt.Frame()

	if err := tt.Click("应用设置"); err != nil {
		t.Fatalf("click 应用设置: %v", err)
	}
	trayCheckLabel := "在系统托盘显示图标"
	if runtime.GOOS == "darwin" {
		trayCheckLabel = "在菜单栏显示图标"
	}
	if !tt.HasText(trayCheckLabel) || !tt.HasText("关闭主窗口时保持在后台运行") {
		t.Fatalf("expected %q & background checkboxes in Settings, got: %v", trayCheckLabel, tt.Texts())
	}
	if !a.settings.ShowInMenuBar {
		t.Fatalf("expected a.settings.ShowInMenuBar=true initially")
	}

	// Uncheck tray/menu-bar checkbox and verify immediate state + persisted config.
	if err := tt.Click(trayCheckLabel); err != nil {
		t.Fatalf("click %s to disable: %v", trayCheckLabel, err)
	}
	if a.settings.ShowInMenuBar {
		t.Fatalf("expected ShowInMenuBar=false after clicking checkbox")
	}
	reloaded, err := config.Load(a.settings.ConfigDir)
	if err != nil {
		t.Fatalf("reload config after unchecking ShowInMenuBar: %v", err)
	}
	if reloaded.ShowInMenuBar {
		t.Fatalf("expected persisted ShowInMenuBar=false after unchecking, got true")
	}

	// Re-check tray/menu-bar checkbox and verify persistence back to true.
	if err := tt.Click(trayCheckLabel); err != nil {
		t.Fatalf("click %s to re-enable: %v", trayCheckLabel, err)
	}
	if !a.settings.ShowInMenuBar {
		t.Fatalf("expected ShowInMenuBar=true after clicking checkbox again")
	}
	reloaded, err = config.Load(a.settings.ConfigDir)
	if err != nil {
		t.Fatalf("reload config after re-checking ShowInMenuBar: %v", err)
	}
	if !reloaded.ShowInMenuBar {
		t.Fatalf("expected persisted ShowInMenuBar=true after re-checking, got false")
	}
}
