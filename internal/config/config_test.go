package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSaveAndNormalize(t *testing.T) {
	dir := t.TempDir()

	// Missing settings.json returns expected defaults.
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load empty dir: %v", err)
	}
	if cfg.Theme != ThemeSystem || cfg.Language != LangSystem || cfg.UIScale != ScaleMedium {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.OpenAtLogin {
		t.Fatalf("expected OpenAtLogin default false, got true")
	}
	if !cfg.ShowInMenuBar || !cfg.CloseToTray || !cfg.Notify || !cfg.AutoIndex {
		t.Fatalf("expected boolean defaults true: %+v", cfg)
	}

	// Upgrading from an older settings.json without "showInMenuBar" must default ShowInMenuBar to true,
	// while explicitly saving ShowInMenuBar=false must persist false across reloads.
	legacyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacyDir, "settings.json"), []byte(`{"theme":"dark","closeToTray":true}`), 0o600); err != nil {
		t.Fatalf("write legacy settings.json: %v", err)
	}
	legacyCfg, err := Load(legacyDir)
	if err != nil || !legacyCfg.ShowInMenuBar {
		t.Fatalf("expected upgraded legacy settings.json to default ShowInMenuBar=true, got %+v (err=%v)", legacyCfg, err)
	}
	legacyCfg.ShowInMenuBar = false
	if err := Save(legacyDir, legacyCfg); err != nil {
		t.Fatalf("Save ShowInMenuBar=false: %v", err)
	}
	reloadedFalse, err := Load(legacyDir)
	if err != nil || reloadedFalse.ShowInMenuBar {
		t.Fatalf("expected ShowInMenuBar=false to persist across Save/Load, got %+v (err=%v)", reloadedFalse, err)
	}

	// Invalid enum values and out-of-range sidebar custom width are clamped/normalized on Save/Load.
	cfg.Theme = "neon"
	cfg.AccentColor = "magenta-neon"
	cfg.Language = "xx-invalid"
	cfg.UIScale = "huge"
	cfg.OpenAtLogin = true
	cfg.SidebarCustomWidth = 9999
	cfg.SidebarCollapsed = true
	cfg.ActiveRepoID = "repo-main"
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load saved: %v", err)
	}
	if reloaded.Theme != ThemeSystem {
		t.Fatalf("expected ThemeSystem after normalize, got %q", reloaded.Theme)
	}
	if reloaded.AccentColor != AccentBlue {
		t.Fatalf("expected AccentBlue after normalize, got %q", reloaded.AccentColor)
	}
	if reloaded.Language != LangSystem {
		t.Fatalf("expected LangSystem after normalize, got %q", reloaded.Language)
	}
	if reloaded.UIScale != ScaleMedium {
		t.Fatalf("expected ScaleMedium after invalid UIScale normalize, got %q", reloaded.UIScale)
	}
	if !reloaded.OpenAtLogin {
		t.Fatalf("expected OpenAtLogin true preserved after reload")
	}
	if reloaded.SidebarCustomWidth != SidebarMaxWidth {
		t.Fatalf("expected clamped SidebarCustomWidth %v, got %v", SidebarMaxWidth, reloaded.SidebarCustomWidth)
	}
	if !reloaded.SidebarCollapsed {
		t.Fatalf("expected SidebarCollapsed true preserved after reload")
	}
	if reloaded.ActiveRepoID != "repo-main" {
		t.Fatalf("expected ActiveRepoID preserved, got %q", reloaded.ActiveRepoID)
	}

	// Valid explicit Language, AccentColor, and UIScale values round-trip cleanly.
	for _, lang := range []string{LangEN, LangZhHans, LangZhHant, LangJA, LangFR, LangDE, LangES} {
		reloaded.Language = lang
		if err := Save(dir, reloaded); err != nil {
			t.Fatalf("Save lang %s: %v", lang, err)
		}
		got, err := Load(dir)
		if err != nil || got.Language != lang {
			t.Fatalf("expected Language %q, got %q (err=%v)", lang, got.Language, err)
		}
	}
	for _, acc := range []string{AccentBlue, AccentIndigo, AccentViolet, AccentTeal, AccentEmerald, AccentOrange, AccentRose} {
		reloaded.AccentColor = acc
		if err := Save(dir, reloaded); err != nil {
			t.Fatalf("Save accent %s: %v", acc, err)
		}
		got, err := Load(dir)
		if err != nil || got.AccentColor != acc {
			t.Fatalf("expected AccentColor %q, got %q (err=%v)", acc, got.AccentColor, err)
		}
	}

	// Valid explicit UIScale values round-trip cleanly.
	for _, scale := range []string{ScaleSmall, ScaleMedium, ScaleLarge} {
		reloaded.UIScale = scale
		if err := Save(dir, reloaded); err != nil {
			t.Fatalf("Save scale %s: %v", scale, err)
		}
		got, err := Load(dir)
		if err != nil || got.UIScale != scale {
			t.Fatalf("expected UIScale %q, got %q (err=%v)", scale, got.UIScale, err)
		}
	}
}

func TestInspectAndDiscoverProfiles(t *testing.T) {
	cfgDir := t.TempDir()

	localCfg := filepath.Join(cfgDir, "repository.config")
	localJSON := `{
		"storage": {
			"type": "filesystem",
			"config": { "path": "/Volumes/Archive/kopia-repo" }
		},
		"description": "Studio NAS Backup"
	}`
	if err := os.WriteFile(localCfg, []byte(localJSON), 0o600); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	s3Cfg := filepath.Join(cfgDir, "repository-cloud.config")
	s3JSON := `{
		"storage": {
			"type": "s3",
			"config": { "bucket": "my-backups", "endpoint": "s3.amazonaws.com", "prefix": "macbook/" }
		}
	}`
	if err := os.WriteFile(s3Cfg, []byte(s3JSON), 0o600); err != nil {
		t.Fatalf("write s3 config: %v", err)
	}

	name, hint, exists := InspectConfigFile(localCfg)
	if !exists || name != "Studio NAS Backup" || !strings.Contains(hint, "/Volumes/Archive/kopia-repo") {
		t.Fatalf("unexpected InspectConfigFile(localCfg): name=%q hint=%q exists=%v", name, hint, exists)
	}

	profiles := DiscoverProfiles(nil, cfgDir)
	if len(profiles) != 2 {
		t.Fatalf("expected 2 discovered profiles, got %d: %+v", len(profiles), profiles)
	}

	np := NewRepoProfile(cfgDir, "Offsite B2")
	if np.Name != "Offsite B2" || !strings.HasSuffix(np.ConfigFile, ".config") {
		t.Fatalf("unexpected NewRepoProfile: %+v", np)
	}

	// Kopia Repository Server .config (top-level "apiServer" without "storage") must also be recognized.
	srvCfg := filepath.Join(cfgDir, "repository-srv.config")
	srvJSON := `{
		"apiServer": { "baseUrl": "https://kopia.internal:51515" },
		"description": "Corp Kopia Server"
	}`
	if err := os.WriteFile(srvCfg, []byte(srvJSON), 0o600); err != nil {
		t.Fatalf("write srv config: %v", err)
	}
	srvName, srvHint, srvOK := InspectConfigFile(srvCfg)
	if !srvOK || srvName != "Corp Kopia Server" || srvHint != "server: https://kopia.internal:51515" {
		t.Fatalf("unexpected InspectConfigFile(srvCfg): name=%q hint=%q ok=%v", srvName, srvHint, srvOK)
	}

	// URL credentials in Kopia Server baseUrl are stripped from StorageHint.
	srvAuthCfg := filepath.Join(cfgDir, "repository-srv-auth.config")
	if err := os.WriteFile(srvAuthCfg, []byte(`{"apiServer":{"baseUrl":"https://user:supersecret@kopia.internal:51515/?tok=123"}}`), 0o600); err != nil {
		t.Fatalf("write srv auth config: %v", err)
	}
	_, authHint, authOK := InspectConfigFile(srvAuthCfg)
	if !authOK || strings.Contains(authHint, "supersecret") || strings.Contains(authHint, "tok=") {
		t.Fatalf("expected URL credentials/query stripped in InspectConfigFile, got hint=%q (ok=%v)", authHint, authOK)
	}
}

func TestExpandPathAndSubprocessEnv(t *testing.T) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		got := ExpandPath("~/.ssh/id_ed25519")
		want := filepath.Join(home, ".ssh", "id_ed25519")
		if got != want {
			t.Fatalf("expected ExpandPath(~/.ssh/id_ed25519)=%q, got %q", want, got)
		}
		if ExpandPath("~") != home {
			t.Fatalf("expected ExpandPath(~)=%q, got %q", home, ExpandPath("~"))
		}
	}

	tmp := t.TempDir()
	fakeRclone := filepath.Join(tmp, "custom-bin", "rclone")
	if err := os.MkdirAll(filepath.Dir(fakeRclone), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(fakeRclone, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake rclone: %v", err)
	}
	resolved, err := ResolveRcloneBinary(fakeRclone)
	if err != nil || resolved != fakeRclone {
		t.Fatalf("expected ResolveRcloneBinary=%q, got %q (err=%v)", fakeRclone, resolved, err)
	}

	env := SubprocessEnv("/opt/homebrew/bin/kopia", fakeRclone)
	var pathVal string
	hasNoUpdate := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathVal = strings.TrimPrefix(kv, "PATH=")
		}
		if kv == "KOPIA_CHECK_FOR_UPDATES=false" {
			hasNoUpdate = true
		}
	}
	if !hasNoUpdate {
		t.Fatalf("expected KOPIA_CHECK_FOR_UPDATES=false in SubprocessEnv")
	}
	if !strings.Contains(pathVal, filepath.Dir(fakeRclone)) {
		t.Fatalf("expected custom rclone dir %q in SubprocessEnv PATH=%q", filepath.Dir(fakeRclone), pathVal)
	}
}
