package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// ThemeMode controls application light/dark appearance.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// LanguageMode controls application display language.
const (
	LangSystem = "system"
	LangEN     = "en"
	LangZhHans = "zh-Hans"
	LangZhHant = "zh-Hant"
	LangJA     = "ja"
	LangFR     = "fr"
	LangDE     = "de"
	LangES     = "es"
)

// AccentColorMode controls primary interactive highlight color.
const (
	AccentBlue    = "blue"
	AccentIndigo  = "indigo"
	AccentViolet  = "violet"
	AccentTeal    = "teal"
	AccentEmerald = "emerald"
	AccentOrange  = "orange"
	AccentRose    = "rose"
)

// UIScaleMode controls interface and font sizing ("small", "medium", "large").
const (
	ScaleSmall  = "small"
	ScaleMedium = "medium"
	ScaleLarge  = "large"
)

// RepoProfile describes a managed or discovered Kopia repository config file.
type RepoProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ConfigFile  string `json:"configFile"`
	StorageHint string `json:"storageHint,omitempty"`
}

// Sidebar width clamping bounds (in content DIPs) when expanded.
const (
	SidebarMinWidth float32 = 144
	SidebarMaxWidth float32 = 340
)

// Settings holds persisted application preferences.
type Settings struct {
	Theme              string        `json:"theme"`
	AccentColor        string        `json:"accentColor,omitempty"`
	Language           string        `json:"language"`
	UIScale            string        `json:"uiScale,omitempty"`
	OpenAtLogin        bool          `json:"openAtLogin"`
	KopiaBinary        string        `json:"kopiaBinary,omitempty"`
	RcloneBinary       string        `json:"rcloneBinary,omitempty"`
	ConfigDir          string        `json:"configDir,omitempty"`
	ShowInMenuBar      bool          `json:"showInMenuBar"`
	CloseToTray        bool          `json:"closeToTray"`
	Notify             bool          `json:"notify"`
	AutoIndex          bool          `json:"autoIndex"`
	ActiveRepoID       string        `json:"activeRepoId,omitempty"`
	SidebarCustomWidth float32       `json:"sidebarCustomWidth,omitempty"`
	SidebarCollapsed   bool          `json:"sidebarCollapsed,omitempty"`
	Repositories       []RepoProfile `json:"repositories,omitempty"`
}

// DefaultSettings returns default preferences for a fresh installation.
func DefaultSettings() Settings {
	return Settings{
		Theme:         ThemeSystem,
		AccentColor:   AccentBlue,
		Language:      LangSystem,
		UIScale:       ScaleMedium,
		OpenAtLogin:   false,
		ShowInMenuBar: true,
		CloseToTray:   true,
		Notify:        true,
		AutoIndex:     true,
	}
}

// Normalize validates and repairs loaded settings in place.
func (s *Settings) Normalize() {
	switch s.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		s.Theme = ThemeSystem
	}
	switch s.AccentColor {
	case AccentBlue, AccentIndigo, AccentViolet, AccentTeal, AccentEmerald, AccentOrange, AccentRose:
	default:
		s.AccentColor = AccentBlue
	}
	switch s.Language {
	case LangSystem, LangEN, LangZhHans, LangZhHant, LangJA, LangFR, LangDE, LangES:
	default:
		s.Language = LangSystem
	}
	switch s.UIScale {
	case ScaleSmall, ScaleMedium, ScaleLarge:
	default:
		s.UIScale = ScaleMedium
	}
	if s.SidebarCustomWidth < 0 {
		s.SidebarCustomWidth = 0
	} else if s.SidebarCustomWidth > 0 {
		s.SidebarCustomWidth = min(max(s.SidebarCustomWidth, SidebarMinWidth), SidebarMaxWidth)
	}
}

// Load reads settings.json from userDataDir, returning DefaultSettings if missing.
func Load(userDataDir string) (Settings, error) {
	cfg := DefaultSettings()
	if userDataDir == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(filepath.Join(userDataDir, "settings.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultSettings(), err
	}
	cfg.Normalize()
	return cfg, nil
}

// EnsurePrivateDir creates dir with 0700 permissions, rejects leaf symlinks, and tightens
// existing directory permissions to 0700 on Unix-like systems.
func EnsurePrivateDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("empty directory path")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("refusing non-directory or symlink path: %s", dir)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// IsExecutableFile reports whether p is an absolute path to a regular executable file.
func IsExecutableFile(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return false
	}
	return true
}

// Save writes settings.json atomically into userDataDir.
func Save(userDataDir string, cfg Settings) error {
	if userDataDir == "" {
		return nil
	}
	cfg.Normalize()
	if err := EnsurePrivateDir(userDataDir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(userDataDir, "settings.json")
	f, err := os.CreateTemp(userDataDir, "settings-*.json.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// DefaultKopiaConfigDir returns Kopia's standard OS configuration directory.
func DefaultKopiaConfigDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			return filepath.Join(home, "Library", "Application Support", "kopia")
		}
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "kopia")
		}
		if home != "" {
			return filepath.Join(home, "AppData", "Roaming", "kopia")
		}
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "kopia")
		}
		if home != "" {
			return filepath.Join(home, ".config", "kopia")
		}
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("mygopia-kopia-%d", os.Getuid()))
}

// ExpandPath trims whitespace and expands a leading "~" or "~/..." to the current user's home directory.
func ExpandPath(p string) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return ""
	}
	if s == "~" || strings.HasPrefix(s, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(s, `~\`)) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if s == "~" {
				return home
			}
			return filepath.Join(home, s[2:])
		}
	}
	return s
}

// EffectiveConfigDir returns the user's configured Kopia config dir or the OS default.
func (s Settings) EffectiveConfigDir() string {
	if p := ExpandPath(s.ConfigDir); p != "" {
		return p
	}
	return DefaultKopiaConfigDir()
}

func resolveExplicitBinary(configured, label string) (string, error) {
	p := ExpandPath(configured)
	if !strings.ContainsAny(p, `/\`) && p != "." && p != ".." {
		if found, err := exec.LookPath(p); err == nil && IsExecutableFile(found) {
			return filepath.Clean(found), nil
		}
		return "", fmt.Errorf("configured %s binary not found in PATH: %s", label, p)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("configured %s binary must be an absolute path: %s", label, p)
	}
	clean := filepath.Clean(p)
	if IsExecutableFile(clean) {
		return clean, nil
	}
	return "", fmt.Errorf("configured %s binary not found or not executable: %s", label, clean)
}

// ResolveKopiaBinary locates a working kopia executable.
func ResolveKopiaBinary(configured string, resourceDirs ...string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return resolveExplicitBinary(configured, "kopia")
	}

	exeName := "kopia"
	if runtime.GOOS == "windows" {
		exeName = "kopia.exe"
	}

	for _, dir := range resourceDirs {
		if dir == "" {
			continue
		}
		cand := filepath.Clean(filepath.Join(dir, exeName))
		if IsExecutableFile(cand) {
			return cand, nil
		}
	}

	if found, err := exec.LookPath(exeName); err == nil && IsExecutableFile(found) {
		return filepath.Clean(found), nil
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/opt/homebrew/bin/kopia",
			"/usr/local/bin/kopia",
			"/opt/local/bin/kopia",
			"/Applications/KopiaUI.app/Contents/Resources/server/kopia",
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates,
				filepath.Join(home, "Applications", "KopiaUI.app", "Contents", "Resources", "server", "kopia"),
				filepath.Join(home, ".nix-profile", "bin", "kopia"),
			)
		}
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		progFiles := os.Getenv("ProgramFiles")
		if progFiles != "" {
			candidates = append(candidates,
				filepath.Join(progFiles, "KopiaUI", "resources", "server", "kopia.exe"),
				filepath.Join(progFiles, "Kopia", "kopia.exe"),
			)
		}
		if localApp != "" {
			candidates = append(candidates,
				filepath.Join(localApp, "Programs", "KopiaUI", "resources", "server", "kopia.exe"),
			)
		}
	default:
		candidates = []string{
			"/usr/local/bin/kopia",
			"/usr/bin/kopia",
			"/snap/bin/kopia",
			"/opt/KopiaUI/resources/server/kopia",
		}
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "bin", exeName), filepath.Join(home, ".local", "bin", exeName))
	}

	for _, cand := range candidates {
		clean := filepath.Clean(cand)
		if IsExecutableFile(clean) {
			return clean, nil
		}
	}

	return "", fmt.Errorf("kopia executable not found; install kopia or set its path in Settings")
}

// ResolveRcloneBinary locates a working rclone executable for Kopia's rclone storage backend.
func ResolveRcloneBinary(configured string, resourceDirs ...string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return resolveExplicitBinary(configured, "rclone")
	}

	exeName := "rclone"
	if runtime.GOOS == "windows" {
		exeName = "rclone.exe"
	}

	for _, dir := range resourceDirs {
		if dir == "" {
			continue
		}
		cand := filepath.Clean(filepath.Join(dir, exeName))
		if IsExecutableFile(cand) {
			return cand, nil
		}
	}

	if found, err := exec.LookPath(exeName); err == nil && IsExecutableFile(found) {
		return filepath.Clean(found), nil
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/opt/homebrew/bin/rclone",
			"/usr/local/bin/rclone",
			"/opt/local/bin/rclone",
			"/run/current-system/sw/bin/rclone",
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates, filepath.Join(home, ".nix-profile", "bin", "rclone"))
		}
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		progFiles := os.Getenv("ProgramFiles")
		userProf := os.Getenv("USERPROFILE")
		if progFiles != "" {
			candidates = append(candidates, filepath.Join(progFiles, "rclone", "rclone.exe"))
		}
		if localApp != "" {
			candidates = append(candidates,
				filepath.Join(localApp, "Programs", "rclone", "rclone.exe"),
				filepath.Join(localApp, "Microsoft", "WinGet", "Links", "rclone.exe"),
			)
		}
		if userProf != "" {
			candidates = append(candidates, filepath.Join(userProf, "scoop", "shims", "rclone.exe"))
		}
	default:
		candidates = []string{
			"/usr/local/bin/rclone",
			"/usr/bin/rclone",
			"/snap/bin/rclone",
		}
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "bin", exeName), filepath.Join(home, ".local", "bin", exeName))
	}

	for _, cand := range candidates {
		clean := filepath.Clean(cand)
		if IsExecutableFile(clean) {
			return clean, nil
		}
	}

	return "", fmt.Errorf("rclone executable not found")
}

// ExampleKopiaBinaryPath returns an OS-appropriate placeholder path for Kopia binary inputs.
func ExampleKopiaBinaryPath() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\Program Files\KopiaUI\resources\server\kopia.exe`
	case "darwin":
		return "/opt/homebrew/bin/kopia"
	default:
		return "/usr/bin/kopia"
	}
}

// ExampleRcloneBinaryPath returns an OS-appropriate placeholder path for Rclone binary inputs.
func ExampleRcloneBinaryPath() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\Program Files\rclone\rclone.exe`
	case "darwin":
		return "/opt/homebrew/bin/rclone"
	default:
		return "/usr/bin/rclone"
	}
}

// SubprocessEnv builds an environment slice for kopia child processes with common package-manager
// directories (e.g. /opt/homebrew/bin on macOS .app GUI launches) and any configured tool directories
// appended to PATH, so external helpers (rclone, ssh, FUSE mount tools, hook scripts) are always found.
// Relative or "." entries are rejected to prevent CWD PATH hijacking.
func SubprocessEnv(extraBinaries ...string) []string {
	baseEnv := os.Environ()
	var currentPath string
	kept := make([]string, 0, len(baseEnv)+2)
	for _, kv := range baseEnv {
		if strings.HasPrefix(kv, "PATH=") || (runtime.GOOS == "windows" && strings.HasPrefix(strings.ToUpper(kv), "PATH=")) {
			_, currentPath, _ = strings.Cut(kv, "=")
			continue
		}
		if strings.HasPrefix(kv, "KOPIA_CHECK_FOR_UPDATES=") {
			continue
		}
		kept = append(kept, kv)
	}

	seen := make(map[string]bool)
	var dirs []string
	addDir := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" || d == "." || d == ".." {
			return
		}
		clean := filepath.Clean(d)
		if !filepath.IsAbs(clean) {
			return
		}
		key := clean
		if runtime.GOOS == "windows" {
			key = strings.ToLower(clean)
		}
		if !seen[key] {
			seen[key] = true
			dirs = append(dirs, clean)
		}
	}

	for _, part := range filepath.SplitList(currentPath) {
		addDir(part)
	}

	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		addDir("/usr/bin")
		addDir("/bin")
		addDir("/usr/sbin")
		addDir("/sbin")
		addDir("/opt/homebrew/bin")
		addDir("/opt/homebrew/sbin")
		addDir("/usr/local/bin")
		addDir("/usr/local/sbin")
		addDir("/opt/local/bin")
		addDir("/run/current-system/sw/bin")
		if home != "" {
			addDir(filepath.Join(home, ".nix-profile", "bin"))
			addDir(filepath.Join(home, ".local", "bin"))
			addDir(filepath.Join(home, "bin"))
		}
	case "windows":
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			addDir(filepath.Join(pf, "rclone"))
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			addDir(filepath.Join(la, "Programs", "rclone"))
			addDir(filepath.Join(la, "Microsoft", "WinGet", "Links"))
		}
	default:
		addDir("/usr/local/sbin")
		addDir("/usr/local/bin")
		addDir("/usr/sbin")
		addDir("/usr/bin")
		addDir("/sbin")
		addDir("/bin")
		addDir("/snap/bin")
		if home != "" {
			addDir(filepath.Join(home, ".nix-profile", "bin"))
			addDir(filepath.Join(home, ".local", "bin"))
			addDir(filepath.Join(home, "bin"))
		}
	}

	for _, bin := range extraBinaries {
		expanded := ExpandPath(bin)
		if expanded != "" && filepath.IsAbs(expanded) {
			addDir(filepath.Dir(expanded))
		}
	}

	kept = append(kept, "PATH="+strings.Join(dirs, string(os.PathListSeparator)))
	kept = append(kept, "KOPIA_CHECK_FOR_UPDATES=false")
	return kept
}

func sanitizeHintValue(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" || !strings.Contains(v, "://") {
		return v
	}
	u, err := url.Parse(v)
	if err != nil {
		return v
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

type rawKopiaConfig struct {
	Description string `json:"description"`
	Storage     struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	} `json:"storage"`
	APIServer *struct {
		BaseURL string `json:"baseUrl"`
	} `json:"apiServer"`
	Caching struct {
		CacheDirectory string `json:"cacheDirectory"`
	} `json:"caching"`
}

// InspectConfigFile extracts a human-friendly name and storage summary from a Kopia .config file.
func InspectConfigFile(configPath string) (name string, storageHint string, connected bool) {
	base := strings.TrimSuffix(filepath.Base(configPath), filepath.Ext(configPath))
	data, err := os.ReadFile(configPath)
	if err != nil {
		return base, "", false
	}
	var raw rawKopiaConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return base, "", false
	}
	if raw.Storage.Type == "" {
		if raw.APIServer != nil && strings.TrimSpace(raw.APIServer.BaseURL) != "" {
			u := sanitizeHintValue(raw.APIServer.BaseURL)
			storageHint = "server: " + u
			if strings.TrimSpace(raw.Description) != "" {
				name = strings.TrimSpace(raw.Description)
			} else {
				name = fmt.Sprintf("server (%s)", u)
			}
			return name, storageHint, true
		}
		return base, "", false
	}

	var cfgMap map[string]any
	_ = json.Unmarshal(raw.Storage.Config, &cfgMap)

	var detail string
	for _, key := range []string{"path", "bucket", "endpoint", "url", "host", "remotePath", "container"} {
		if v, ok := cfgMap[key].(string); ok && v != "" {
			detail = sanitizeHintValue(v)
			break
		}
	}
	if detail != "" {
		storageHint = fmt.Sprintf("%s: %s", raw.Storage.Type, detail)
	} else {
		storageHint = raw.Storage.Type
	}

	if strings.TrimSpace(raw.Description) != "" {
		name = strings.TrimSpace(raw.Description)
	} else if raw.Storage.Type == "filesystem" && detail != "" {
		name = filepath.Base(detail)
	} else if detail != "" {
		name = fmt.Sprintf("%s (%s)", raw.Storage.Type, detail)
	} else {
		name = base
	}
	return name, storageHint, true
}

func canonicalProfilePathKey(cleanPath string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleanPath)
	}
	return cleanPath
}

// DiscoverProfiles merges already-configured RepoProfiles with any .config files in configDir.
func DiscoverProfiles(existing []RepoProfile, configDir string) []RepoProfile {
	seenPaths := make(map[string]bool, len(existing))
	var out []RepoProfile

	for _, p := range existing {
		if p.ConfigFile == "" {
			continue
		}
		clean := filepath.Clean(p.ConfigFile)
		key := canonicalProfilePathKey(clean)
		if seenPaths[key] {
			continue
		}
		seenPaths[key] = true
		name, hint, ok := InspectConfigFile(clean)
		if p.Name == "" || p.Name == "Repository" {
			if name != "" {
				p.Name = name
			}
		}
		if ok && hint != "" {
			p.StorageHint = hint
		}
		if p.ID == "" {
			p.ID = idFromPath(clean)
		}
		out = append(out, p)
	}

	entries, err := os.ReadDir(configDir)
	if err == nil {
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if strings.HasSuffix(n, ".config") && !strings.HasPrefix(n, ".") {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		for _, n := range names {
			full := filepath.Clean(filepath.Join(configDir, n))
			key := canonicalProfilePathKey(full)
			if seenPaths[key] {
				continue
			}
			seenPaths[key] = true
			dispName, hint, _ := InspectConfigFile(full)
			if dispName == "" {
				dispName = strings.TrimSuffix(n, ".config")
			}
			out = append(out, RepoProfile{
				ID:          idFromPath(full),
				Name:        dispName,
				ConfigFile:  full,
				StorageHint: hint,
			})
		}
	}

	if len(out) == 0 {
		defPath := filepath.Clean(filepath.Join(configDir, "repository.config"))
		out = append(out, RepoProfile{
			ID:         "repository",
			Name:       "Primary Repository",
			ConfigFile: defPath,
		})
	}
	return out
}

// NewRepoProfile creates a new isolated RepoProfile path in configDir for multi-repository management.
func NewRepoProfile(configDir, displayName string) RepoProfile {
	ts := time.Now().Format("20060102-150405")
	id := "repository-" + ts
	cfgPath := filepath.Join(configDir, id+".config")
	for i := 1; ; i++ {
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			break
		}
		id = fmt.Sprintf("repository-%s-%d", ts, i)
		cfgPath = filepath.Join(configDir, id+".config")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = id
	}
	return RepoProfile{
		ID:         id,
		Name:       strings.TrimSpace(displayName),
		ConfigFile: cfgPath,
	}
}

func idFromPath(p string) string {
	base := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	if base == "" {
		return fmt.Sprintf("repo-%d", time.Now().UnixNano())
	}
	return base
}
