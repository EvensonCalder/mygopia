package app

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
	"mygopia/locales"
)

const (
	// windowBarMid is the vertical centerline (in window DIPs) shared by macOS
	// traffic lights, the sidebar header title, and the main toolbar.
	windowBarMid    = 22
	windowHeaderDIP = 2 * windowBarMid // 44 window DIPs
)

// topBarHeight returns the unified header row height in content DIPs so that
// both sidebar title ("Mygopia") and main toolbar stay pixel-centered on the
// native window controls (macOS traffic lights / Windows & Linux caption buttons)
// across all UI scale & zoom levels.
func topBarHeight(c *ui.Context) float32 {
	bar := c.TitleBar()
	z := c.Zoom()
	if z <= 0 {
		z = 1
	}
	if bar.Height > 0 || bar.Left > 0 || bar.Right > 0 {
		return max(float32(34), float32(windowHeaderDIP)/z)
	}
	return float32(windowHeaderDIP)
}

// ComputeEffectiveZoom combines display-resolution adaptation, OS accessibility
// text scale, and the user's Small / Medium / Large preference (default Medium).
func ComputeEffectiveZoom(uiScale string, displayDIPWidth, displayDIPHeight int, osTextScale float32) float32 {
	var base float64 = 1.0
	if displayDIPWidth > 0 && displayDIPHeight > 0 {
		switch {
		case displayDIPWidth <= 1366 || displayDIPHeight <= 800:
			base = 0.94
		case displayDIPWidth >= 3000:
			base = 1.15
		case displayDIPWidth >= 2200:
			base = 1.06
		}
	}

	var preset float64 = 1.0
	switch uiScale {
	case config.ScaleSmall:
		preset = 0.90
	case config.ScaleLarge:
		preset = 1.12
	default:
		preset = 1.00
	}

	textMult := 1.0
	if osTextScale > 0 && !math.IsNaN(float64(osTextScale)) {
		textMult = float64(osTextScale)
	}

	z := base * preset * textMult
	z = min(max(z, 0.75), 2.0)
	return float32(math.Round(z*100) / 100)
}

// palette provides semantic surface tokens on top of ui.Theme for calm visual hierarchy.
type palette struct {
	canvas      ui.Color
	sidebarBg   ui.Color
	cardBg      ui.Color
	subtleBg    ui.Color
	border      ui.Color
	borderSoft  ui.Color
	text        ui.Color
	muted       ui.Color
	accent      ui.Color
	accentSoft  ui.Color
	successSoft ui.Color
	warningSoft ui.Color
	dangerSoft  ui.Color
}

// AccentOption defines a selectable theme accent preset across Light & Dark modes.
type AccentOption struct {
	Key          string
	Label        func() string
	LightAccent  ui.Color
	LightHover   ui.Color
	LightPressed ui.Color
	DarkAccent   ui.Color
	DarkHover    ui.Color
	DarkPressed  ui.Color
}

var accentOptions = []AccentOption{
	{
		Key:          config.AccentBlue,
		Label:        locales.ColorBlue,
		LightAccent:  ui.Hex("#2563eb"),
		LightHover:   ui.Hex("#1d4ed8"),
		LightPressed: ui.Hex("#1e40af"),
		DarkAccent:   ui.Hex("#3b82f6"),
		DarkHover:    ui.Hex("#60a5fa"),
		DarkPressed:  ui.Hex("#2563eb"),
	},
	{
		Key:          config.AccentIndigo,
		Label:        locales.ColorIndigo,
		LightAccent:  ui.Hex("#4f46e5"),
		LightHover:   ui.Hex("#4338ca"),
		LightPressed: ui.Hex("#3730a3"),
		DarkAccent:   ui.Hex("#6366f1"),
		DarkHover:    ui.Hex("#818cf8"),
		DarkPressed:  ui.Hex("#4f46e5"),
	},
	{
		Key:          config.AccentViolet,
		Label:        locales.ColorViolet,
		LightAccent:  ui.Hex("#7c3aed"),
		LightHover:   ui.Hex("#6d28d9"),
		LightPressed: ui.Hex("#5b21b6"),
		DarkAccent:   ui.Hex("#8b5cf6"),
		DarkHover:    ui.Hex("#a78bfa"),
		DarkPressed:  ui.Hex("#7c3aed"),
	},
	{
		Key:          config.AccentTeal,
		Label:        locales.ColorTeal,
		LightAccent:  ui.Hex("#0d9488"),
		LightHover:   ui.Hex("#0f766e"),
		LightPressed: ui.Hex("#115e59"),
		DarkAccent:   ui.Hex("#14b8a6"),
		DarkHover:    ui.Hex("#2dd4bf"),
		DarkPressed:  ui.Hex("#0d9488"),
	},
	{
		Key:          config.AccentEmerald,
		Label:        locales.ColorGreen,
		LightAccent:  ui.Hex("#16a34a"),
		LightHover:   ui.Hex("#15803d"),
		LightPressed: ui.Hex("#166534"),
		DarkAccent:   ui.Hex("#10b981"),
		DarkHover:    ui.Hex("#34d399"),
		DarkPressed:  ui.Hex("#059669"),
	},
	{
		Key:          config.AccentOrange,
		Label:        locales.ColorOrange,
		LightAccent:  ui.Hex("#ea580c"),
		LightHover:   ui.Hex("#c2410c"),
		LightPressed: ui.Hex("#9a3412"),
		DarkAccent:   ui.Hex("#f97316"),
		DarkHover:    ui.Hex("#fb923c"),
		DarkPressed:  ui.Hex("#ea580c"),
	},
	{
		Key:          config.AccentRose,
		Label:        locales.ColorRose,
		LightAccent:  ui.Hex("#e11d48"),
		LightHover:   ui.Hex("#be123c"),
		LightPressed: ui.Hex("#9f1239"),
		DarkAccent:   ui.Hex("#f43f5e"),
		DarkHover:    ui.Hex("#fb7185"),
		DarkPressed:  ui.Hex("#e11d48"),
	},
}

func resolveAccentOption(key string) AccentOption {
	for _, opt := range accentOptions {
		if opt.Key == key {
			return opt
		}
	}
	return accentOptions[0]
}

// buildAppTheme constructs a calm, restrained Light or Dark ui.Theme with the chosen accent color.
func buildAppTheme(dark bool, accentKey string) *ui.Theme {
	acc := resolveAccentOption(accentKey)
	if dark {
		t := ui.DarkTheme()
		t.Dark = true
		t.Background = ui.Hex("#141416")
		t.Surface = ui.Hex("#1d1d20")
		t.SurfaceHover = ui.Hex("#26262a")
		t.SurfacePressed = ui.Hex("#2e2e33")
		t.Border = ui.RGBA(255, 255, 255, 0.09)
		t.Text = ui.Hex("#ededef")
		t.TextMuted = ui.Hex("#8e8e96")
		t.Accent = acc.DarkAccent
		t.AccentHover = acc.DarkHover
		t.AccentPressed = acc.DarkPressed
		t.AccentText = ui.Hex("#ffffff")
		t.Selection = ui.RGBA(acc.DarkAccent.R, acc.DarkAccent.G, acc.DarkAccent.B, 0.38)
		t.Focus = ui.RGBA(acc.DarkHover.R, acc.DarkHover.G, acc.DarkHover.B, 0.58)
		t.Success = ui.Hex("#22c55e")
		t.Warning = ui.Hex("#f59e0b")
		t.Danger = ui.Hex("#ef4444")
		t.Radius = 7
		t.Spacing = 4
		t.FontSize = 13
		return t
	}
	t := ui.LightTheme()
	t.Dark = false
	t.Background = ui.Hex("#f7f7f5")
	t.Surface = ui.Hex("#eeeeeb")
	t.SurfaceHover = ui.Hex("#e5e5e1")
	t.SurfacePressed = ui.Hex("#dcdcd7")
	t.Border = ui.RGBA(24, 24, 27, 0.10)
	t.Text = ui.Hex("#18181b")
	t.TextMuted = ui.Hex("#6e6e77")
	t.Accent = acc.LightAccent
	t.AccentHover = acc.LightHover
	t.AccentPressed = acc.LightPressed
	t.AccentText = ui.Hex("#ffffff")
	t.Selection = ui.RGBA(acc.LightAccent.R, acc.LightAccent.G, acc.LightAccent.B, 0.24)
	t.Focus = ui.RGBA(acc.LightAccent.R, acc.LightAccent.G, acc.LightAccent.B, 0.52)
	t.Success = ui.Hex("#16a34a")
	t.Warning = ui.Hex("#d97706")
	t.Danger = ui.Hex("#dc2626")
	t.Radius = 7
	t.Spacing = 4
	t.FontSize = 13
	return t
}

func paletteOf(c *ui.Context) palette {
	t := c.Theme()
	if t.Dark {
		return palette{
			canvas:      ui.Hex("#141416"),
			sidebarBg:   ui.Hex("#18181b"),
			cardBg:      ui.Hex("#1b1b1f"),
			subtleBg:    ui.Hex("#222227"),
			border:      ui.RGBA(255, 255, 255, 0.09),
			borderSoft:  ui.RGBA(255, 255, 255, 0.05),
			text:        t.Text,
			muted:       t.TextMuted,
			accent:      t.Accent,
			accentSoft:  ui.RGBA(t.Accent.R, t.Accent.G, t.Accent.B, 0.16),
			successSoft: ui.RGBA(34, 197, 94, 0.15),
			warningSoft: ui.RGBA(245, 158, 11, 0.16),
			dangerSoft:  ui.RGBA(239, 68, 68, 0.16),
		}
	}
	return palette{
		canvas:      ui.Hex("#f7f7f5"),
		sidebarBg:   ui.Hex("#f1f1ee"),
		cardBg:      ui.Hex("#ffffff"),
		subtleBg:    ui.Hex("#f2f2ef"),
		border:      ui.RGBA(24, 24, 27, 0.09),
		borderSoft:  ui.RGBA(24, 24, 27, 0.05),
		text:        t.Text,
		muted:       t.TextMuted,
		accent:      t.Accent,
		accentSoft:  ui.RGBA(t.Accent.R, t.Accent.G, t.Accent.B, 0.11),
		successSoft: ui.RGBA(22, 163, 74, 0.11),
		warningSoft: ui.RGBA(217, 119, 6, 0.12),
		dangerSoft:  ui.RGBA(220, 38, 38, 0.11),
	}
}

// card renders a calm bordered surface container.
func card(c *ui.Context, fn func()) ui.Element {
	p := paletteOf(c)
	return ui.Column(c).
		Padding(16).
		Gap(12).
		Radius(9).
		Background(p.cardBg).
		Border(1, p.border).
		Children(fn)
}

// metricCard renders a compact summary stat tile with uniform vertical rhythm.
func metricCard(c *ui.Context, label, value, sub string) {
	p := paletteOf(c)
	if sub == "" {
		sub = "\u00a0"
	}
	ui.Column(c).
		Grow(1).
		Basis(0).
		MinWidth(150).
		Padding(12, 14).
		Gap(4).
		Radius(8).
		Background(p.cardBg).
		Border(1, p.border).
		Children(func() {
			ui.Text(c, label).FontSize(11.5).TextColor(p.muted).SingleLine()
			ui.Text(c, value).FontSize(16).FontWeight(600).FontFeatures("tnum").SingleLine()
			ui.Text(c, sub).FontSize(11.5).TextColor(p.muted).SingleLine()
		})
}

// compactButton renders a secondary compact button with balanced CJK/Latin breathing room.
func compactButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Padding(5, 12).Radius(6).FontSize(11.5)
}

// compactPrimaryButton renders an accented compact button with balanced CJK/Latin breathing room.
func compactPrimaryButton(c *ui.Context, label string) ui.Element {
	return ui.PrimaryButton(c, label).Padding(5, 12).Radius(6).FontSize(11.5)
}

// sourceSelectOptions builds parallel canonical keys (Source.String()) and clean human-readable
// labels (path-first, omitting redundant local user@host prefix when unambiguous) for ui.Select.
func sourceSelectOptions(sources []*kopia.SourceStatus, st kopia.RepoSnapshotState) (keys, labels []string) {
	keys = make([]string, len(sources))
	labels = make([]string, len(sources))
	pathCount := make(map[string]int, len(sources))
	for _, s := range sources {
		pathCount[s.Source.Path]++
	}
	localUser, localHost := "", ""
	if st.Sources != nil {
		localUser = st.Sources.LocalUsername
		localHost = st.Sources.LocalHost
	}
	if (localUser == "" || localHost == "") && st.Status != nil {
		if localUser == "" {
			localUser = st.Status.Username
		}
		if localHost == "" {
			localHost = st.Status.Hostname
		}
	}
	for i, s := range sources {
		src := s.Source
		keys[i] = src.String()
		isLocal := localUser != "" && localHost != "" && src.UserName == localUser && src.Host == localHost
		if src.Path != "" && isLocal && pathCount[src.Path] <= 1 {
			labels[i] = src.Path
		} else if src.Path != "" && (src.UserName != "" || src.Host != "") {
			labels[i] = fmt.Sprintf("%s (%s@%s)", src.Path, src.UserName, src.Host)
		} else {
			labels[i] = keys[i]
		}
	}
	return keys, labels
}

// statusPill renders a restrained semantic pill badge.
func statusPill(c *ui.Context, text string, fg, bg ui.Color) ui.Element {
	return ui.Row(c).
		AlignItems(ui.Center).
		Padding(3, 9).
		Radius(999).
		Background(bg).
		Shrink(0).
		Children(func() {
			ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(fg).SingleLine()
		})
}

// tableStatusPill adapts badge contrast automatically when rendered inside a selected row.
func tableStatusPill(c *ui.Context, selected bool, text string, fg, bg ui.Color) ui.Element {
	if selected {
		return statusPill(c, text, ui.Hex("#ffffff"), ui.RGBA(255, 255, 255, 0.22))
	}
	return statusPill(c, text, fg, bg)
}

// tableRowColors returns high-contrast foreground tokens for selected vs unselected table/list rows.
func tableRowColors(c *ui.Context, selected bool) (primary, secondary, iconAccent ui.Color) {
	if selected {
		return c.Theme().AccentText, ui.RGBA(255, 255, 255, 0.84), c.Theme().AccentText
	}
	p := paletteOf(c)
	return p.text, p.muted, p.accent
}

// emptyState renders a centered calm empty state message with optional primary action.
func emptyState(c *ui.Context, icon *ui.SVG, title, subtitle, actionLabel string, onAction func()) {
	p := paletteOf(c)
	ui.Column(c).
		Grow(1).
		AlignItems(ui.Center).
		Justify(ui.Center).
		Padding(32).
		Gap(10).
		Children(func() {
			if icon != nil {
				ui.Icon(c, icon).FontSize(28).TextColor(p.muted)
			}
			ui.Text(c, title).FontSize(15).FontWeight(600)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12.5).TextColor(p.muted).MaxWidth(420).TextAlign(ui.Center)
			}
			if actionLabel != "" && onAction != nil {
				ui.PrimaryButton(c, actionLabel).Margin(6, 0, 0, 0).OnClick(onAction)
			}
		})
}

// formatBytes formats byte counts cleanly (e.g. "1.42 GB", "840 KB").
func formatBytes(b int64) string {
	if b < 0 {
		b = 0
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	suffixes := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	val := float64(b) / float64(div)
	if val >= 100 {
		return fmt.Sprintf("%.0f %s", val, suffixes[exp])
	}
	if val >= 10 {
		return fmt.Sprintf("%.1f %s", val, suffixes[exp])
	}
	return fmt.Sprintf("%.2f %s", val, suffixes[exp])
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func formatShortTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	now := time.Now()
	loc := t.Local()
	if loc.Year() == now.Year() && loc.YearDay() == now.YearDay() {
		return loc.Format("15:04:05")
	}
	return loc.Format("2006-01-02 15:04")
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %dm", h, m)
}
