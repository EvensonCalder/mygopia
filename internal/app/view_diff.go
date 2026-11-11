package app

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
	"mygopia/locales"
)

const (
	defaultTablePageSize    = 150
	defaultSnapshotPageSize = 40
	defaultVersionPageSize  = 25
)

// Shared time-range preset indices used by Snapshot Browser and Global Search.
const (
	timePresetAll = iota
	timePreset24h
	timePreset7d
	timePreset30d
	timePreset90d
	timePreset1y
	timePresetCustom
)

func timeRangePresetLabels() []string {
	return []string{
		locales.AllTime(),
		locales.Last24Hours(),
		locales.Last7Days(),
		locales.Last30Days(),
		locales.Last90Days(),
		locales.Last1Year(),
		locales.CustomDateRange(),
	}
}

func resolveTimeRangeBounds(preset int, customFrom, customTo time.Time) (after, before time.Time) {
	now := time.Now()
	switch preset {
	case timePreset24h:
		return now.Add(-24 * time.Hour), time.Time{}
	case timePreset7d:
		return now.AddDate(0, 0, -7), time.Time{}
	case timePreset30d:
		return now.AddDate(0, 0, -30), time.Time{}
	case timePreset90d:
		return now.AddDate(0, 0, -90), time.Time{}
	case timePreset1y:
		return now.AddDate(-1, 0, 0), time.Time{}
	case timePresetCustom:
		var a, b time.Time
		if !customFrom.IsZero() {
			y, m, d := customFrom.Date()
			a = time.Date(y, m, d, 0, 0, 0, 0, customFrom.Location())
		}
		if !customTo.IsZero() {
			y, m, d := customTo.Date()
			b = time.Date(y, m, d, 23, 59, 59, int(time.Second-time.Nanosecond), customTo.Location())
		}
		return a, b
	default:
		return time.Time{}, time.Time{}
	}
}

// parseFlexibleDate parses "YYYY-MM-DD", "YYYY/MM/DD", "YYYY-MM", or "YYYY" for fast 10-year date jumping.
func parseFlexibleDate(raw string, endOfPeriod bool) (time.Time, bool) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "/", "-"))
	if s == "" {
		return time.Time{}, false
	}
	loc := time.Local
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01", s, loc); err == nil {
		if endOfPeriod {
			return t.AddDate(0, 1, -1), true
		}
		return t, true
	}
	if len(s) == 4 {
		if y, err := strconv.Atoi(s); err == nil && y >= 1970 && y <= 2100 {
			if endOfPeriod {
				return time.Date(y, 12, 31, 0, 0, 0, 0, loc), true
			}
			return time.Date(y, 1, 1, 0, 0, 0, 0, loc), true
		}
	}
	return time.Time{}, false
}

// renderDateRangeInputs renders a unified fast-type ("YYYY-MM-DD" / "YYYY") input + compact calendar popover button pair.
func renderDateRangeInputs(
	c *ui.Context,
	keyPrefix string,
	fromDate *time.Time,
	fromText *string,
	toDate *time.Time,
	toText *string,
) bool {
	p := paletteOf(c)
	now := time.Now()
	if *fromText != "" {
		if parsed, ok := parseFlexibleDate(*fromText, false); ok && fromDate.IsZero() {
			*fromDate = parsed
		}
	}
	if *toText != "" {
		if parsed, ok := parseFlexibleDate(*toText, true); ok && toDate.IsZero() {
			*toDate = parsed
		}
	}
	if fromDate.IsZero() {
		*fromDate = now.AddDate(0, -1, 0)
	}
	if toDate.IsZero() {
		*toDate = now
	}
	if *fromText == "" {
		*fromText = fromDate.Format("2006-01-02")
	}
	if *toText == "" {
		*toText = toDate.Format("2006-01-02")
	}

	changed := false
	renderOneDateField := func(subKey, label string, dateVal *time.Time, textVal *string, endOfPeriod bool) {
		ui.Row(c.Key(keyPrefix + "-grp-" + subKey)).AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, label).FontSize(11.5).TextColor(p.muted).SingleLine()
			if ui.TextInput(c.Key(keyPrefix+"-"+subKey+"-txt"), textVal).
				Placeholder("YYYY-MM-DD").
				Width(100).
				MinWidth(0).
				Changed() {
				if parsed, ok := parseFlexibleDate(*textVal, endOfPeriod); ok {
					*dateVal = parsed
					changed = true
				}
			}
			calBtn := ui.Button(c.Key(keyPrefix+"-"+subKey+"-calbtn"), "").
				Label(label).
				Tooltip(label).
				Padding(5, 7)
			calBtn.Children(func() {
				ui.Icon(c, iconCalendar).FontSize(13.5).TextColor(p.muted)
			})
			open := ui.Local(calBtn, "open", func() bool { return false })
			if calBtn.Clicked() {
				*open = !*open
			}
			ui.Popover(c, calBtn, open, func() {
				if ui.Calendar(c, dateVal).Changed() {
					*textVal = dateVal.Format("2006-01-02")
					*open = false
					changed = true
				}
			})
		})
	}

	ui.Row(c).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
		renderOneDateField("from", locales.From(), fromDate, fromText, false)
		renderOneDateField("to", locales.To(), toDate, toText, true)
	})
	return changed
}

// renderPaginationBar renders a clean page navigation footer whenever total > pageSize.
func renderPaginationBar(c *ui.Context, keyPrefix string, offset *int, pageSize, total int) {
	if pageSize <= 0 {
		pageSize = defaultTablePageSize
	}
	if total <= pageSize && *offset <= 0 {
		return
	}
	maxOffset := max(0, ((total-1)/pageSize)*pageSize)
	if *offset > maxOffset {
		*offset = maxOffset
	}
	if *offset < 0 {
		*offset = 0
	}

	p := paletteOf(c)
	curPage := (*offset / pageSize) + 1
	totalPages := max(1, (total+pageSize-1)/pageSize)
	startIdx := *offset + 1
	endIdx := min(total, *offset+pageSize)

	ui.Row(c.Key(keyPrefix+"-pager")).AlignItems(ui.Center).Gap(8).Padding(2, 2, 0, 2).Children(func() {
		ui.Text(c, locales.ShowingRangeOfTotal(startIdx, endIdx, total)).
			FontSize(11.5).
			FontFeatures("tnum").
			TextColor(p.muted)
		ui.Spacer(c)
		if compactButton(c, locales.Prev()).Disabled(*offset <= 0).Clicked() {
			*offset = max(0, *offset-pageSize)
		}
		ui.Text(c, locales.PageOfTotal(curPage, totalPages)).
			FontSize(11.5).
			FontFeatures("tnum").
			TextColor(p.muted)
		if compactButton(c, locales.Next()).Disabled(*offset+pageSize >= total).Clicked() {
			*offset = min(maxOffset, *offset+pageSize)
		}
	})
}

// snapshotYears extracts distinct years (descending) from a snapshot list for instant multi-year jumping.
func snapshotYears(snaps []*kopia.Snapshot) []string {
	seen := make(map[int]bool, 8)
	var years []int
	for _, s := range snaps {
		if s == nil || s.StartTime.IsZero() {
			continue
		}
		y := s.StartTime.Local().Year()
		if !seen[y] {
			seen[y] = true
			years = append(years, y)
		}
	}
	slices.SortFunc(years, func(a, b int) int { return cmp.Compare(b, a) })
	out := make([]string, 0, len(years)+1)
	out = append(out, locales.AllYears())
	for _, y := range years {
		out = append(out, strconv.Itoa(y))
	}
	return out
}

// snapshotMatchesTextQuery checks if a snapshot matches a lowercase search query by timestamp, date, note, pin, or ID.
func snapshotMatchesTextQuery(sn *kopia.Snapshot, q string) bool {
	if sn == nil || q == "" {
		return true
	}
	t := sn.StartTime.Local()
	hay := strings.ToLower(
		formatTimestamp(sn.StartTime.Time) + " " +
			t.Format("2006/01/02 15:04:05") + " " +
			t.Format("2006-1-2") + " " +
			t.Format("2006/1/2") + " " +
			sn.Description + " " +
			strings.Join(sn.Pins, " ") + " " +
			sn.ID,
	)
	return strings.Contains(hay, q)
}

// ============================================================================
// Snapshot Diff View (Compare Two Snapshots of the Same Backup Source)
// ============================================================================

// diffTargetLocalDir is the virtual target snapshot ID representing the live local directory on disk
// (i.e. pending working-tree changes that the next backup will capture).
const diffTargetLocalDir = "__LOCAL_DIR__"

const (
	diffPresetNone = iota
	diffPresetLocal
	diffPresetPrev
	diffPreset7d
	diffPreset30d
	diffPresetOldest
)

// resolveLocalDiffDir returns the accessible local directory path for src when src belongs to this host
// (or has an explicit headless-test override in a.localDirOverrides).
func (a *App) resolveLocalDiffDir(src kopia.SourceInfo, st kopia.RepoSnapshotState) (string, bool) {
	if a.localDirOverrides != nil {
		if dir, ok := a.localDirOverrides[src.String()]; ok && dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir, true
			}
		}
		if dir, ok := a.localDirOverrides[src.Path]; ok && dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir, true
			}
		}
		return "", false
	}
	if a.manager == nil || strings.TrimSpace(src.Path) == "" {
		return "", false
	}
	if st.Sources != nil && (st.Sources.LocalHost != "" || st.Sources.LocalUsername != "") {
		if src.Host != "" && src.Host != st.Sources.LocalHost {
			return "", false
		}
		if src.UserName != "" && st.Sources.LocalUsername != "" && src.UserName != st.Sources.LocalUsername {
			return "", false
		}
	}
	clean := filepath.Clean(src.Path)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return clean, true
}

func localDiffOptionsFromPolicy(localDir string, pol *kopia.Policy) index.LocalDiffOptions {
	opts := index.LocalDiffOptions{
		LocalPath:              localDir,
		DotIgnoreFiles:         []string{".kopiaignore"},
		IgnoreCacheDirectories: true,
	}
	if pol == nil {
		return opts
	}
	fp := pol.FilesPolicy
	if len(fp.IgnoreRules) > 0 {
		opts.IgnoreRules = slices.Clone(fp.IgnoreRules)
	}
	if len(fp.DotIgnoreFiles) > 0 {
		opts.DotIgnoreFiles = slices.Clone(fp.DotIgnoreFiles)
	}
	if fp.IgnoreCacheDirectories != nil {
		opts.IgnoreCacheDirectories = *fp.IgnoreCacheDirectories
	}
	if fp.MaxFileSize > 0 {
		opts.MaxFileSize = fp.MaxFileSize
	}
	return opts
}

func (a *App) openDiffForSource(src kopia.SourceInfo, baseSnapID, targetSnapID string) {
	a.diffSourceStr = src.String()
	a.diffBaseSnapID = baseSnapID
	a.diffTargetSnapID = targetSnapID
	a.diffActivePreset = diffPresetNone
	a.activeNav = navDiff
	a.reloadDiffSnapshotsAsync()
}

func (a *App) reloadDiffSnapshotsAsync() {
	repoID := a.settings.ActiveRepoID
	_, st := a.activeRepoState()
	sources := st.SourceList()

	if a.manager == nil {
		// Headless / seeded mode: use seeded a.snapshots
		a.diffSnapshots = slices.Clone(a.snapshots)
		if a.diffSourceStr == "" && len(sources) > 0 {
			a.diffSourceStr = sources[0].Source.String()
		}
		a.ensureDefaultDiffPair()
		a.runSnapshotDiffAsync()
		return
	}
	if repoID == "" || len(sources) == 0 {
		return
	}

	var chosen *kopia.SourceInfo
	for _, s := range sources {
		if s.Source.String() == a.diffSourceStr {
			cp := s.Source
			chosen = &cp
			break
		}
	}
	if chosen == nil {
		cp := sources[0].Source
		chosen = &cp
		a.diffSourceStr = cp.String()
	}

	targetSource := *chosen
	targetSourceStr := targetSource.String()
	// If snapshots for this source are already loaded in the Snapshot Browser, seed immediately so default Latest vs Previous is instant.
	if len(a.diffSnapshots) == 0 && a.snapSourceStr == targetSourceStr && len(a.snapshots) > 0 {
		a.diffSnapshots = slices.Clone(a.snapshots)
		a.ensureDefaultDiffPair()
		a.runSnapshotDiffAsync()
	}
	initialBase := a.diffBaseSnapID
	initialTarget := a.diffTargetSnapID
	a.diffLoadSeq++
	seq := a.diffLoadSeq
	a.diffLoading = true

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.uiUpdate(func() {
				if a.diffLoadSeq == seq {
					a.diffLoading = false
				}
			})
			return
		}
		resp, err := cli.ListSnapshots(ctx, targetSource, true)
		if err != nil {
			a.uiUpdate(func() {
				if a.diffLoadSeq == seq {
					a.diffLoading = false
				}
			})
			a.toast(err.Error(), true)
			return
		}
		snaps := resp.Snapshots
		slices.SortFunc(snaps, func(x, y *kopia.Snapshot) int {
			return y.StartTime.Compare(x.StartTime.Time)
		})
		a.uiUpdate(func() {
			if a.diffLoadSeq != seq || a.settings.ActiveRepoID != repoID || a.diffSourceStr != targetSourceStr {
				return
			}
			a.diffLoading = false
			a.diffSnapshots = snaps
			userChangedPair := (a.diffBaseSnapID != "" && a.diffBaseSnapID != initialBase) ||
				(a.diffTargetSnapID != "" && a.diffTargetSnapID != initialTarget)
			a.ensureDefaultDiffPair()
			if !userChangedPair {
				a.runSnapshotDiffAsync()
			}
		})
	}()
}

func (a *App) ensureDefaultDiffPair() {
	a.normalizeDiffPair("")
}

// normalizeDiffPair enforces chronological forward order (Base = Older, Target = Newer)
// so +Added and -Deleted semantics are never inverted.
// preferChangedSide may be "base", "target", or "".
func (a *App) normalizeDiffPair(preferChangedSide string) {
	snaps := a.diffSnapshots
	n := len(snaps)
	if n == 0 {
		a.diffBaseSnapID = ""
		a.diffTargetSnapID = ""
		a.diffActivePreset = diffPresetNone
		a.diffSummary = index.DiffSummary{}
		return
	}

	_, st := a.activeRepoState()
	src := parseTargetKey(a.diffSourceStr, st)
	_, hasLocal := a.resolveLocalDiffDir(src, st)

	indexOf := func(id string) int {
		if id == "" {
			return -1
		}
		for i, s := range snaps {
			if s.ID == id {
				return i
			}
		}
		return -1
	}

	// Virtual live local directory target is strictly newer than every snapshot (0 .. n-1).
	if a.diffTargetSnapID == diffTargetLocalDir {
		if hasLocal {
			idxBase := indexOf(a.diffBaseSnapID)
			if idxBase < 0 {
				idxBase = 0
			}
			a.diffBaseSnapID = snaps[idxBase].ID
			if idxBase == 0 {
				a.diffActivePreset = diffPresetLocal
			} else if a.diffActivePreset == diffPresetLocal {
				a.diffActivePreset = diffPresetNone
			}
			return
		}
		a.diffTargetSnapID = ""
	}

	if n == 1 {
		if hasLocal {
			a.diffBaseSnapID = snaps[0].ID
			a.diffTargetSnapID = diffTargetLocalDir
			a.diffActivePreset = diffPresetLocal
			return
		}
		a.diffTargetSnapID = snaps[0].ID
		a.diffBaseSnapID = snaps[0].ID
		a.diffActivePreset = diffPresetNone
		a.diffSummary = index.DiffSummary{}
		return
	}

	// snaps is sorted newest first: index 0 is newest, index n-1 is oldest.
	// Valid forward chronological order requires idxTarget < idxBase.
	idxTarget := indexOf(a.diffTargetSnapID)
	idxBase := indexOf(a.diffBaseSnapID)

	if idxTarget < 0 && idxBase < 0 {
		if hasLocal {
			a.diffBaseSnapID = snaps[0].ID
			a.diffTargetSnapID = diffTargetLocalDir
			a.diffActivePreset = diffPresetLocal
			return
		}
		idxTarget = 0
		idxBase = 1
	} else if idxTarget < 0 {
		if idxBase == 0 {
			idxTarget = 0
			idxBase = 1
		} else {
			idxTarget = 0
		}
	} else if idxBase < 0 {
		if idxTarget >= n-1 {
			idxTarget = n - 2
			idxBase = n - 1
		} else {
			idxBase = idxTarget + 1
		}
	}

	if idxTarget > idxBase {
		switch preferChangedSide {
		case "base":
			if idxBase == 0 {
				idxTarget = 0
				idxBase = 1
			} else {
				idxTarget = 0
			}
		case "target":
			if idxTarget >= n-1 {
				idxTarget = n - 2
				idxBase = n - 1
			} else {
				idxBase = idxTarget + 1
			}
		default:
			idxTarget, idxBase = idxBase, idxTarget
		}
	}

	if idxTarget == idxBase {
		if preferChangedSide == "target" && idxTarget < n-1 {
			idxBase = idxTarget + 1
		} else if idxBase > 0 {
			idxTarget = 0
		} else {
			idxTarget = 0
			idxBase = 1
		}
	}

	a.diffTargetSnapID = snaps[idxTarget].ID
	a.diffBaseSnapID = snaps[idxBase].ID

	if a.diffActivePreset != diffPresetNone {
		if a.diffTargetSnapID != snaps[0].ID || a.diffBaseSnapID != a.presetDiffBaseID(a.diffActivePreset) {
			a.diffActivePreset = diffPresetNone
		}
	}
	if a.diffActivePreset == diffPresetNone && idxTarget == 0 && idxBase == 1 {
		a.diffActivePreset = diffPresetPrev
	}
}

func (a *App) presetDiffBaseID(preset int) string {
	snaps := a.diffSnapshots
	if preset == diffPresetLocal {
		if len(snaps) >= 1 {
			return snaps[0].ID
		}
		return ""
	}
	if len(snaps) < 2 {
		return ""
	}
	switch preset {
	case diffPresetPrev:
		return snaps[1].ID
	case diffPresetOldest:
		return snaps[len(snaps)-1].ID
	case diffPreset7d, diffPreset30d:
		days := 7
		if preset == diffPreset30d {
			days = 30
		}
		cutoff := snaps[0].StartTime.Time.AddDate(0, 0, -days)
		chosen := snaps[len(snaps)-1]
		for _, s := range snaps[1:] {
			if !s.StartTime.Time.After(cutoff) {
				chosen = s
				break
			}
		}
		return chosen.ID
	default:
		return ""
	}
}

func (a *App) applyDiffQuickPreset(preset int) {
	snaps := a.diffSnapshots
	if preset == diffPresetLocal {
		if len(snaps) < 1 {
			return
		}
		_, st := a.activeRepoState()
		src := parseTargetKey(a.diffSourceStr, st)
		if _, ok := a.resolveLocalDiffDir(src, st); !ok {
			return
		}
		a.diffBaseSnapID = snaps[0].ID
		a.diffTargetSnapID = diffTargetLocalDir
		a.diffActivePreset = diffPresetLocal
		a.runSnapshotDiffAsync()
		return
	}
	if len(snaps) < 2 {
		return
	}
	baseID := a.presetDiffBaseID(preset)
	if baseID == "" {
		return
	}
	a.diffTargetSnapID = snaps[0].ID
	a.diffBaseSnapID = baseID
	a.diffActivePreset = preset
	a.runSnapshotDiffAsync()
}

func (a *App) findDiffSnapshot(id string) *kopia.Snapshot {
	for _, s := range a.diffSnapshots {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (a *App) runSnapshotDiffAsync() {
	snapA := a.findDiffSnapshot(a.diffBaseSnapID)
	a.diffPageOffset = 0
	a.diffSelectedRow = -1

	repoID := a.settings.ActiveRepoID
	_, st := a.activeRepoState()
	src := parseTargetKey(a.diffSourceStr, st)

	if a.diffTargetSnapID == diffTargetLocalDir {
		localDir, hasLocal := a.resolveLocalDiffDir(src, st)
		if snapA == nil || !hasLocal {
			a.diffCalcSeq++
			a.diffLoading = false
			a.diffSummary = index.DiffSummary{}
			return
		}
		if a.manager == nil {
			var effPol *kopia.Policy
			for _, p := range st.Policies {
				if p.Target.String() == src.String() && p.Policy != nil {
					effPol = p.Policy
					break
				}
			}
			sum, err := a.indexer.CompareSeededSnapshotWithLocalDir(repoID, src, snapA, localDiffOptionsFromPolicy(localDir, effPol))
			if err != nil {
				a.toast(err.Error(), true)
			}
			a.diffSummary = sum
			return
		}

		wantBaseID := snapA.ID
		rootA := snapA.RootID
		a.diffCalcSeq++
		seq := a.diffCalcSeq
		a.diffLoading = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cli, err := a.manager.ClientFor(ctx, repoID)
			if err != nil {
				a.uiUpdate(func() {
					if a.diffCalcSeq == seq {
						a.diffLoading = false
					}
				})
				a.toast(err.Error(), true)
				return
			}
			var effPol *kopia.Policy
			if resPol, rerr := cli.ResolvePolicy(ctx, src, nil, 1); rerr == nil && resPol != nil {
				effPol = resPol.Effective
			}
			summary, err := a.indexer.CompareRootWithLocalDir(ctx, repoID, cli, rootA, localDiffOptionsFromPolicy(localDir, effPol))
			if err != nil {
				a.uiUpdate(func() {
					if a.diffCalcSeq == seq {
						a.diffLoading = false
					}
				})
				a.toast(err.Error(), true)
				return
			}
			a.uiUpdate(func() {
				if a.diffCalcSeq != seq || a.settings.ActiveRepoID != repoID || a.diffBaseSnapID != wantBaseID || a.diffTargetSnapID != diffTargetLocalDir {
					return
				}
				a.diffLoading = false
				a.diffSummary = summary
			})
		}()
		return
	}

	snapB := a.findDiffSnapshot(a.diffTargetSnapID)
	if snapA == nil || snapB == nil || snapA.ID == snapB.ID {
		a.diffCalcSeq++
		a.diffLoading = false
		a.diffSummary = index.DiffSummary{}
		return
	}

	if a.manager == nil {
		a.diffSummary = a.indexer.CompareSeededSnapshots(repoID, src, snapA, snapB)
		return
	}

	wantBaseID, wantTargetID := snapA.ID, snapB.ID
	rootA, rootB := snapA.RootID, snapB.RootID
	a.diffCalcSeq++
	seq := a.diffCalcSeq
	a.diffLoading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.uiUpdate(func() {
				if a.diffCalcSeq == seq {
					a.diffLoading = false
				}
			})
			a.toast(err.Error(), true)
			return
		}
		summary, err := a.indexer.CompareRoots(ctx, repoID, cli, rootA, rootB)
		if err != nil {
			a.uiUpdate(func() {
				if a.diffCalcSeq == seq {
					a.diffLoading = false
				}
			})
			a.toast(err.Error(), true)
			return
		}
		a.uiUpdate(func() {
			if a.diffCalcSeq != seq || a.settings.ActiveRepoID != repoID || a.diffBaseSnapID != wantBaseID || a.diffTargetSnapID != wantTargetID {
				return
			}
			a.diffLoading = false
			a.diffSummary = summary
		})
	}()
}

// filteredDiffSnapshots returns all snapshots in diffSnapshots matching the active diffYearFilter and diffSnapSearch.
func (a *App) filteredDiffSnapshots() []*kopia.Snapshot {
	years := snapshotYears(a.diffSnapshots)
	curYear := a.diffYearFilter
	if curYear == "" || !slices.Contains(years, curYear) {
		curYear = years[0]
	}
	snapQuery := strings.ToLower(strings.TrimSpace(a.diffSnapSearch))
	var matched []*kopia.Snapshot
	for _, sn := range a.diffSnapshots {
		if curYear != years[0] && strconv.Itoa(sn.StartTime.Local().Year()) != curYear {
			continue
		}
		if snapQuery != "" && !snapshotMatchesTextQuery(sn, snapQuery) {
			continue
		}
		matched = append(matched, sn)
	}
	return matched
}

// applyDiffSnapshotFilterSelection automatically selects the best matching snapshot(s) when the user filters by year or note/time.
func (a *App) applyDiffSnapshotFilterSelection(matched []*kopia.Snapshot) {
	if len(matched) == 0 || len(a.diffSnapshots) == 0 {
		return
	}
	// If Target is the live local directory, filtering snapshots simply jumps Base Snapshot (Older) to the matched snapshot.
	if a.diffTargetSnapID == diffTargetLocalDir {
		for _, s := range matched {
			if s.ID == a.diffBaseSnapID {
				return
			}
		}
		chosenBase := matched[0]
		if a.diffBaseSnapID != chosenBase.ID {
			a.diffBaseSnapID = chosenBase.ID
			a.diffActivePreset = diffPresetNone
			a.normalizeDiffPair("base")
			a.runSnapshotDiffAsync()
		}
		return
	}
	if len(a.diffSnapshots) < 2 {
		return
	}
	// Keep Target on the latest snapshot unless the filter matched >= 2 snapshots and the user is browsing within a filtered year/group
	targetInMatched := false
	baseInMatched := false
	for _, s := range matched {
		if s.ID == a.diffTargetSnapID {
			targetInMatched = true
		}
		if s.ID == a.diffBaseSnapID {
			baseInMatched = true
		}
	}
	if baseInMatched && targetInMatched && a.diffBaseSnapID != a.diffTargetSnapID {
		return
	}
	if a.diffTargetSnapID == "" {
		a.diffTargetSnapID = a.diffSnapshots[0].ID
	}
	// Pick the first matched snapshot that differs from diffTargetSnapID as Base Snapshot (Older)
	chosenBase := matched[0]
	if chosenBase.ID == a.diffTargetSnapID {
		if len(matched) > 1 {
			chosenBase = matched[1]
		} else if a.diffSnapshots[0].ID != chosenBase.ID {
			a.diffTargetSnapID = a.diffSnapshots[0].ID
		} else if len(a.diffSnapshots) > 1 {
			a.diffTargetSnapID = chosenBase.ID
			chosenBase = a.diffSnapshots[1]
		}
	}
	if a.diffBaseSnapID != chosenBase.ID {
		a.diffBaseSnapID = chosenBase.ID
		a.diffActivePreset = diffPresetNone
		a.normalizeDiffPair("base")
		a.runSnapshotDiffAsync()
	}
}

func formatSnapshotOptionLabel(sn *kopia.Snapshot) string {
	if sn == nil {
		return "—"
	}
	parts := []string{formatTimestamp(sn.StartTime.Time)}
	if sn.Summary != nil {
		parts = append(parts, formatBytes(sn.Summary.TotalFileSize))
	}
	if sn.Description != "" {
		parts = append(parts, shortenMiddle(sn.Description, 26))
	}
	return strings.Join(parts, " · ")
}

func formatSnapshotOptionLabels(snaps []*kopia.Snapshot) []string {
	raw := make([]string, len(snaps))
	counts := make(map[string]int, len(snaps))
	for i, sn := range snaps {
		raw[i] = formatSnapshotOptionLabel(sn)
		counts[raw[i]]++
	}
	out := make([]string, len(snaps))
	for i, sn := range snaps {
		if sn != nil && counts[raw[i]] > 1 {
			out[i] = fmt.Sprintf("%s [%s]", raw[i], shortenMiddle(sn.ID, 8))
		} else {
			out[i] = raw[i]
		}
	}
	return out
}

func (a *App) renderDiffView(c *ui.Context, _ config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	sources := st.SourceList()
	if len(sources) == 0 {
		emptyState(c, iconDiff, locales.NoBackupSourcesYet(), locales.AddALocalFolderToStartEncrypted(), locales.AddFolder(), func() {
			a.activeNav = navBackups
			a.addSourceModalOpen = true
		})
		return
	}

	srcKeys, srcLabels := sourceSelectOptions(sources, st)

	if a.diffSourceStr == "" || !slices.Contains(srcKeys, a.diffSourceStr) {
		a.diffSourceStr = srcKeys[0]
		a.diffBaseSnapID = ""
		a.diffTargetSnapID = ""
		a.diffActivePreset = diffPresetNone
		a.reloadDiffSnapshotsAsync()
	} else if len(a.diffSnapshots) == 0 && !a.diffLoading {
		a.reloadDiffSnapshotsAsync()
	}

	if len(a.diffSnapshots) > 0 {
		prevBase, prevTarget := a.diffBaseSnapID, a.diffTargetSnapID
		a.normalizeDiffPair("")
		if a.diffBaseSnapID != prevBase || a.diffTargetSnapID != prevTarget {
			a.runSnapshotDiffAsync()
		}
	}

	curSrcLabel := srcLabels[0]
	for i, k := range srcKeys {
		if k == a.diffSourceStr {
			curSrcLabel = srcLabels[i]
			break
		}
	}

	curSrc := parseTargetKey(a.diffSourceStr, st)
	localDir, hasLocal := a.resolveLocalDiffDir(curSrc, st)
	isTargetLocal := a.diffTargetSnapID == diffTargetLocalDir && hasLocal

	// Filter candidate snapshots by year & search text so 10-year histories never require endless scrolling
	years := snapshotYears(a.diffSnapshots)
	curYear := a.diffYearFilter
	if curYear == "" || !slices.Contains(years, curYear) {
		curYear = years[0]
	}

	snapQuery := strings.ToLower(strings.TrimSpace(a.diffSnapSearch))
	hasSnapFilter := curYear != years[0] || snapQuery != ""
	var matchedSnaps []*kopia.Snapshot
	var baseCandidates []*kopia.Snapshot
	var targetSnapCandidates []*kopia.Snapshot
	nSnaps := len(a.diffSnapshots)
	for idx, sn := range a.diffSnapshots {
		matches := true
		if curYear != years[0] && strconv.Itoa(sn.StartTime.Local().Year()) != curYear {
			matches = false
		}
		if matches && snapQuery != "" && !snapshotMatchesTextQuery(sn, snapQuery) {
			matches = false
		}
		if matches {
			matchedSnaps = append(matchedSnaps, sn)
		}
		// Base (Older) includes snaps[0] (newest) when comparing against Current Local Folder; otherwise excludes snaps[0] when >=2 snapshots exist.
		if isTargetLocal || nSnaps < 2 || idx > 0 {
			if matches || sn.ID == a.diffBaseSnapID {
				baseCandidates = append(baseCandidates, sn)
			}
		}
		if nSnaps < 2 || idx < nSnaps-1 {
			if matches || sn.ID == a.diffTargetSnapID {
				targetSnapCandidates = append(targetSnapCandidates, sn)
			}
		}
	}

	baseLabels := formatSnapshotOptionLabels(baseCandidates)
	targetSnapLabels := formatSnapshotOptionLabels(targetSnapCandidates)
	var targetOptionIDs []string
	var targetLabels []string
	if hasLocal && nSnaps >= 1 {
		targetOptionIDs = append(targetOptionIDs, diffTargetLocalDir)
		targetLabels = append(targetLabels, locales.CurrentLocalFolderNextBackup())
	}
	for i, sn := range targetSnapCandidates {
		// When only 1 snapshot exists and local folder is available, Target only needs the live local folder option (avoiding snap-0 == snap-0).
		if nSnaps == 1 && hasLocal {
			continue
		}
		targetOptionIDs = append(targetOptionIDs, sn.ID)
		targetLabels = append(targetLabels, targetSnapLabels[i])
	}

	baseLabel := ""
	targetLabel := ""
	for i, sn := range baseCandidates {
		if sn.ID == a.diffBaseSnapID {
			baseLabel = baseLabels[i]
			break
		}
	}
	for i, id := range targetOptionIDs {
		if id == a.diffTargetSnapID {
			targetLabel = targetLabels[i]
			break
		}
	}

	ui.Column(c).Fill().Padding(16, 18).Gap(12).Children(func() {
		// Top Snapshot Selector & Quick Presets Card
		card(c, func() {
			// Row 1: Backup Source (left) + Quick Comparison Presets (right, single non-wrapping group)
			ui.Row(c).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, locales.SelectSource()).FontSize(12).FontWeight(600).TextColor(p.muted)
					if ui.Select(c.Key("diff-src-sel"), &curSrcLabel, srcLabels).MinWidth(0).Width(212).Changed() {
						for i, lb := range srcLabels {
							if lb == curSrcLabel && i < len(srcKeys) {
								a.diffSourceStr = srcKeys[i]
								break
							}
						}
						a.diffBaseSnapID = ""
						a.diffTargetSnapID = ""
						a.diffActivePreset = diffPresetNone
						a.reloadDiffSnapshotsAsync()
					}
				})

				ui.Spacer(c)

				if (hasLocal && len(a.diffSnapshots) >= 1) || len(a.diffSnapshots) >= 2 {
					ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
						renderPresetBtn := func(label string, preset int) {
							if a.diffActivePreset == preset {
								if compactPrimaryButton(c, label).Clicked() {
									a.applyDiffQuickPreset(preset)
								}
							} else {
								if compactButton(c, label).Clicked() {
									a.applyDiffQuickPreset(preset)
								}
							}
						}
						if hasLocal && len(a.diffSnapshots) >= 1 {
							renderPresetBtn(locales.LocalVsLatest(), diffPresetLocal)
						}
						if len(a.diffSnapshots) >= 2 {
							renderPresetBtn(locales.LatestVsPrevious(), diffPresetPrev)
							renderPresetBtn(locales.LatestVs7dAgo(), diffPreset7d)
							renderPresetBtn(locales.LatestVs30dAgo(), diffPreset30d)
							renderPresetBtn(locales.LatestVsOldest(), diffPresetOldest)
						}
					})
				}
			})

			// Row 2: Chronologically Fixed Base Snapshot (Older) -> Target Snapshot (Newer)
			if len(baseCandidates) >= 1 && len(targetOptionIDs) >= 1 {
				ui.Row(c).AlignItems(ui.Center).Wrap().Gap(10).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Grow(1).MinWidth(240).Children(func() {
						ui.Text(c, locales.BaseSnapshotOlder()).FontSize(12).TextColor(p.muted).Shrink(0)
						if ui.Select(c.Key("diff-base-sel"), &baseLabel, baseLabels).Grow(1).Changed() {
							for i, lb := range baseLabels {
								if lb == baseLabel && i < len(baseCandidates) {
									a.diffBaseSnapID = baseCandidates[i].ID
									a.diffActivePreset = diffPresetNone
									a.normalizeDiffPair("base")
									a.runSnapshotDiffAsync()
									break
								}
							}
						}
					})

					ui.Text(c, "→").FontSize(14).FontWeight(600).TextColor(p.muted).Padding(0, 2).Shrink(0)

					ui.Row(c).AlignItems(ui.Center).Gap(6).Grow(1).MinWidth(240).Children(func() {
						ui.Text(c, locales.TargetSnapshotNewer()).FontSize(12).TextColor(p.muted).Shrink(0)
						if ui.Select(c.Key("diff-target-sel"), &targetLabel, targetLabels).Grow(1).Changed() {
							for i, lb := range targetLabels {
								if lb == targetLabel && i < len(targetOptionIDs) {
									a.diffTargetSnapID = targetOptionIDs[i]
									a.diffActivePreset = diffPresetNone
									a.normalizeDiffPair("target")
									a.runSnapshotDiffAsync()
									break
								}
							}
						}
					})
				})
			}

			// Row 3: Multi-Year Jumper & Snapshot Note/Time Narrowing (keeps Row 1 uncluttered)
			if len(a.diffSnapshots) >= 2 {
				ui.Row(c).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
					ui.Text(c, locales.FilterSnapshots()).FontSize(12).TextColor(p.muted)
					if len(years) > 2 {
						if ui.Select(c.Key("diff-year-sel"), &curYear, years).MinWidth(0).Width(108).Changed() {
							if curYear == years[0] {
								a.diffYearFilter = ""
							} else {
								a.diffYearFilter = curYear
							}
							a.applyDiffSnapshotFilterSelection(a.filteredDiffSnapshots())
						}
					}
					if ui.SearchField(c.Key("diff-snap-filter"), &a.diffSnapSearch).
						Placeholder(locales.FilterSnapshotNotesOrTime()).
						MinWidth(160).
						Width(236).
						Changed() {
						a.applyDiffSnapshotFilterSelection(a.filteredDiffSnapshots())
					}
					if hasSnapFilter {
						if len(matchedSnaps) == 0 {
							statusPill(c, locales.NoMatchingSnapshots(), c.Theme().Warning, p.warningSoft)
						} else {
							ui.Text(c, locales.FilteredOfTotalSnapshots(len(matchedSnaps), len(a.diffSnapshots))).
								FontSize(11.5).FontFeatures("tnum").TextColor(p.muted).SingleLine()
						}
						if compactButton(c, locales.ResetFilters()).Clicked() {
							a.diffYearFilter = ""
							a.diffSnapSearch = ""
							a.applyDiffSnapshotFilterSelection(a.filteredDiffSnapshots())
						}
					}
				})
			}
		}).Padding(12, 14).Gap(8)

		if a.diffLoading && len(a.diffSnapshots) == 0 {
			card(c, func() {
				ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Text(c, locales.LoadingSnapshots()).TextColor(p.muted)
				})
			}).Grow(1)
			return
		}
		if len(a.diffSnapshots) == 0 || (len(a.diffSnapshots) == 1 && !hasLocal) {
			card(c, func() {
				emptyState(c, iconDiff, locales.NeedAtLeast2SnapshotsToCompare(), "", "", nil)
			}).Grow(1)
			return
		}
		if a.diffBaseSnapID == a.diffTargetSnapID {
			card(c, func() {
				emptyState(c, iconDiff, locales.SelectTwoDifferentSnapshotsToCompare(), "", "", nil)
			}).Grow(1)
			return
		}
		if a.diffLoading && len(a.diffSummary.Entries) == 0 {
			loadingLabel := locales.ComparingSnapshots()
			if isTargetLocal {
				loadingLabel = locales.ComparingWithLocalFolder()
			}
			card(c, func() {
				ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Text(c, loadingLabel).TextColor(p.muted)
				})
			}).Grow(1)
			return
		}

		// Summary Metric Cards
		sum := a.diffSummary
		totalChanged := len(sum.Entries)
		netSign := ""
		if sum.NetDeltaBytes > 0 {
			netSign = "+"
		} else if sum.NetDeltaBytes < 0 {
			netSign = "-"
		}
		netStr := netSign + formatBytes(absInt64(sum.NetDeltaBytes))
		if sum.NetDeltaBytes == 0 {
			netStr = "0 B"
		}

		ui.Row(c).Wrap().Gap(12).Children(func() {
			metricCard(c, locales.ChangedItems(), intToStr(totalChanged), locales.LabeledValue(locales.NetSizeDelta(), netStr))
			metricCard(c, locales.AddedFiles(), "+"+intToStr(sum.AddedCount), "+"+formatBytes(sum.AddedBytes))
			metricCard(c, locales.ModifiedFiles(), "~"+intToStr(sum.ModifiedCount), "")
			metricCard(c, locales.DeletedFiles(), "-"+intToStr(sum.DeletedCount), "-"+formatBytes(sum.RemovedBytes))
		})

		// Diff Filter Row
		ui.Row(c).AlignItems(ui.Center).Wrap().Gap(6).Children(func() {
			if ui.Segmented(c, &a.diffChangeSeg, locales.AllChanges(), locales.Added(), locales.Changed(), locales.Deleted()).Changed() {
				a.diffPageOffset = 0
			}
			if ui.Segmented(c, &a.diffKindSeg, locales.All(), locales.Files(), locales.Folders()).Changed() {
				a.diffPageOffset = 0
			}

			sizeLabels := []string{locales.AnySize(), locales.SizeOver1MB(), locales.SizeOver10MB(), locales.SizeOver100MB(), locales.SizeOver1GB()}
			if a.diffSizePreset < 0 || a.diffSizePreset >= len(sizeLabels) {
				a.diffSizePreset = 0
			}
			curSize := sizeLabels[a.diffSizePreset]
			if ui.Select(c.Key("diff-size-sel"), &curSize, sizeLabels).MinWidth(0).Width(104).Changed() {
				for i, lb := range sizeLabels {
					if lb == curSize {
						a.diffSizePreset = i
						a.diffPageOffset = 0
						break
					}
				}
			}

			timeLabels := timeRangePresetLabels()
			if a.diffTimePreset < 0 || a.diffTimePreset >= len(timeLabels) {
				a.diffTimePreset = 0
			}
			curTimeLabel := timeLabels[a.diffTimePreset]
			if ui.Select(c.Key("diff-time-sel"), &curTimeLabel, timeLabels).MinWidth(0).Width(116).Changed() {
				for i, lb := range timeLabels {
					if lb == curTimeLabel {
						a.diffTimePreset = i
						a.diffPageOffset = 0
						break
					}
				}
			}

			if ui.SearchField(c.Key("diff-path-q"), &a.diffFilterQuery).
				Placeholder(locales.FilterPathNameOrTime()).
				Grow(1).
				MinWidth(120).
				Changed() {
				a.diffPageOffset = 0
			}

			if ui.TextInput(c.Key("diff-ext-q"), &a.diffIncludeExts).
				Placeholder(locales.ExtPdfZIP()).
				MinWidth(0).
				Width(112).
				Changed() {
				a.diffPageOffset = 0
			}

			if a.diffChangeSeg != 0 || a.diffKindSeg != 0 || a.diffSizePreset != 0 || a.diffTimePreset != 0 || a.diffFilterQuery != "" || a.diffIncludeExts != "" {
				if compactButton(c, locales.ResetFilters()).Clicked() {
					a.diffChangeSeg = 0
					a.diffKindSeg = 0
					a.diffSizePreset = 0
					a.diffTimePreset = 0
					a.diffDateFrom = time.Time{}
					a.diffDateFromText = ""
					a.diffDateTo = time.Time{}
					a.diffDateToText = ""
					a.diffFilterQuery = ""
					a.diffIncludeExts = ""
					a.diffPageOffset = 0
				}
			}
		})

		if a.diffTimePreset == timePresetCustom {
			if renderDateRangeInputs(c, "diff", &a.diffDateFrom, &a.diffDateFromText, &a.diffDateTo, &a.diffDateToText) {
				a.diffPageOffset = 0
			}
		}

		filtered := a.filteredDiffEntries()
		if len(filtered) == 0 {
			card(c, func() {
				if len(sum.Entries) == 0 {
					emptyState(c, iconDiff, locales.NoDifferencesBetweenTheSelectedSnapshots(), "", "", nil)
				} else {
					emptyState(c, iconSearch, locales.NoMatchingItems(), "", "", nil)
				}
			}).Grow(1)
			return
		}

		// Paginate filtered diff entries
		totalFiltered := len(filtered)
		if a.diffPageOffset >= totalFiltered {
			a.diffPageOffset = max(0, ((totalFiltered-1)/defaultTablePageSize)*defaultTablePageSize)
		}
		pageEnd := min(totalFiltered, a.diffPageOffset+defaultTablePageSize)
		pageItems := filtered[a.diffPageOffset:pageEnd]

		cols := []ui.TableColumn{
			{ID: "kind", Title: locales.Change(), Width: 92, Sortable: true},
			{ID: "path", Title: locales.Path(), Width: 310, MinWidth: 160, Sortable: true},
			{ID: "old", Title: locales.OldSize(), Width: 102, Align: ui.End, Sortable: true},
			{ID: "new", Title: locales.NewSize(), Width: 102, Align: ui.End, Sortable: true},
			{ID: "delta", Title: locales.SizeDelta(), Width: 110, Align: ui.End, Sortable: true},
			{ID: "mtime", Title: locales.Modified(), Width: 172, Sortable: true},
		}

		a.diffTable.Selected = &a.diffSelectedRow
		a.diffTable.Sort = &a.diffSort

		card(c, func() {
			tbl := ui.Table(c, &a.diffTable, cols, len(pageItems), func(row, col int) {
				ent := pageItems[row]
				selected := row == a.diffSelectedRow
				primary, secondary, iconAccent := tableRowColors(c, selected)

				switch col {
				case 0:
					switch ent.Kind {
					case index.DiffAdded:
						tableStatusPill(c, selected, "+ "+locales.Added(), c.Theme().Success, p.successSoft)
					case index.DiffModified:
						tableStatusPill(c, selected, "~ "+locales.Changed(), c.Theme().Warning, p.warningSoft)
					case index.DiffDeleted:
						tableStatusPill(c, selected, "- "+locales.Deleted(), c.Theme().Danger, p.dangerSoft)
					}
				case 1:
					r := ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
						if ent.IsDir {
							ui.Icon(c, iconFolder).FontSize(14).TextColor(iconAccent)
						} else {
							ui.Icon(c, iconFile).FontSize(14).TextColor(secondary)
						}
						ui.Text(c, ent.Path).TextColor(primary).SingleLine()
					})
					r.ContextMenu(func(m *ui.Menu) {
						hasAction := false
						if isTargetLocal && hasLocal && ent.Kind != index.DiffDeleted && a.desktopRunning {
							hasAction = true
							if m.Item(locales.RevealInFileManager()).Chosen() {
								fullPath := filepath.Join(localDir, filepath.FromSlash(ent.Path))
								if ent.IsDir {
									_ = mygo.Shell.OpenPath(fullPath)
								} else {
									mygo.Shell.ShowItemInFolder(fullPath)
								}
							}
						}
						if ent.NewObjectID != "" {
							hasAction = true
							if !ent.IsDir && a.desktopRunning {
								if m.Item(locales.SaveNewVersion()).Chosen() {
									go a.saveSingleObjectInteractive(a.settings.ActiveRepoID, ent.NewObjectID, ent.Name)
								}
							}
							if m.Item(locales.RestoreNewVersion()).Chosen() {
								a.openRestoreModal(a.settings.ActiveRepoID, ent.NewObjectID, ent.Name)
							}
						}
						if ent.OldObjectID != "" {
							hasAction = true
							if !ent.IsDir && a.desktopRunning {
								if m.Item(locales.SaveOldVersion()).Chosen() {
									go a.saveSingleObjectInteractive(a.settings.ActiveRepoID, ent.OldObjectID, ent.Name)
								}
							}
							if m.Item(locales.RestoreOldVersion()).Chosen() {
								a.openRestoreModal(a.settings.ActiveRepoID, ent.OldObjectID, ent.Name)
							}
						}
						if hasAction {
							m.Separator()
						}
						if m.Item(locales.Copy()).Chosen() {
							if a.desktopRunning {
								mygo.Clipboard.WriteText(ent.Path)
							}
							a.toast(locales.CopiedToClipboard(), false)
						}
					})
				case 2:
					if ent.Kind == index.DiffAdded {
						ui.Text(c, "—").TextColor(secondary).SingleLine()
					} else {
						ui.Text(c, formatBytes(ent.OldSize)).FontFeatures("tnum").TextColor(secondary).SingleLine()
					}
				case 3:
					if ent.Kind == index.DiffDeleted {
						ui.Text(c, "—").TextColor(secondary).SingleLine()
					} else {
						ui.Text(c, formatBytes(ent.NewSize)).FontFeatures("tnum").TextColor(primary).SingleLine()
					}
				case 4:
					deltaTxt := "0 B"
					deltaColor := secondary
					if ent.SizeDelta > 0 {
						deltaTxt = "+" + formatBytes(ent.SizeDelta)
						if !selected {
							deltaColor = c.Theme().Success
						} else {
							deltaColor = primary
						}
					} else if ent.SizeDelta < 0 {
						deltaTxt = "-" + formatBytes(absInt64(ent.SizeDelta))
						if !selected {
							deltaColor = c.Theme().Danger
						} else {
							deltaColor = primary
						}
					}
					ui.Text(c, deltaTxt).FontFeatures("tnum").TextColor(deltaColor).SingleLine()
				case 5:
					ui.Text(c, formatTimestamp(ent.EffectiveModTime())).FontFeatures("tnum").TextColor(secondary).SingleLine()
				}
			}).Grow(1)

			if tbl.Submitted() && a.diffSelectedRow >= 0 && a.diffSelectedRow < len(pageItems) {
				chosen := pageItems[a.diffSelectedRow]
				oid := chosen.NewObjectID
				if oid == "" {
					oid = chosen.OldObjectID
				}
				if oid != "" {
					if chosen.IsDir {
						a.openRestoreModal(a.settings.ActiveRepoID, oid, chosen.Name)
					} else if a.desktopRunning {
						go a.saveSingleObjectInteractive(a.settings.ActiveRepoID, oid, chosen.Name)
					}
				} else if isTargetLocal && hasLocal && chosen.Kind != index.DiffDeleted && a.desktopRunning {
					fullPath := filepath.Join(localDir, filepath.FromSlash(chosen.Path))
					if chosen.IsDir {
						_ = mygo.Shell.OpenPath(fullPath)
					} else {
						mygo.Shell.ShowItemInFolder(fullPath)
					}
				}
			}
		}).Grow(1).Padding(0)

		renderPaginationBar(c, "diff", &a.diffPageOffset, defaultTablePageSize, totalFiltered)
	})
}

func (a *App) filteredDiffEntries() []index.DiffEntry {
	q := strings.ToLower(strings.TrimSpace(a.diffFilterQuery))
	exts := parseSimpleExtFilter(a.diffIncludeExts)
	minBytes := sizePresetBytes(a.diffSizePreset)
	after, before := resolveTimeRangeBounds(a.diffTimePreset, a.diffDateFrom, a.diffDateTo)

	out := make([]index.DiffEntry, 0, len(a.diffSummary.Entries))
	for _, e := range a.diffSummary.Entries {
		switch a.diffChangeSeg {
		case 1:
			if e.Kind != index.DiffAdded {
				continue
			}
		case 2:
			if e.Kind != index.DiffModified {
				continue
			}
		case 3:
			if e.Kind != index.DiffDeleted {
				continue
			}
		}
		switch a.diffKindSeg {
		case 1:
			if e.IsDir {
				continue
			}
		case 2:
			if !e.IsDir {
				continue
			}
		}
		if minBytes > 0 && max(e.NewSize, e.OldSize) < minBytes {
			continue
		}
		mtime := e.EffectiveModTime()
		if !after.IsZero() && !mtime.IsZero() && mtime.Before(after) {
			continue
		}
		if !before.IsZero() && !mtime.IsZero() && mtime.After(before) {
			continue
		}
		if len(exts) > 0 && (e.IsDir || !exts[e.Ext]) {
			continue
		}
		if q != "" {
			mtLocal := mtime.Local()
			hay := strings.ToLower(
				e.Path + " " +
					formatTimestamp(mtime) + " " +
					mtLocal.Format("2006/01/02 15:04:05") + " " +
					mtLocal.Format("2006-1-2"),
			)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, e)
	}

	col := a.diffSort.Column
	desc := a.diffSort.Descending
	slices.SortFunc(out, func(x, y index.DiffEntry) int {
		var c int
		switch col {
		case "kind":
			c = cmp.Compare(x.Kind, y.Kind)
		case "old":
			c = cmp.Compare(x.OldSize, y.OldSize)
		case "new":
			c = cmp.Compare(x.NewSize, y.NewSize)
		case "delta":
			c = cmp.Compare(absInt64(x.SizeDelta), absInt64(y.SizeDelta))
		case "mtime":
			c = x.EffectiveModTime().Compare(y.EffectiveModTime())
		default:
			c = strings.Compare(strings.ToLower(x.Path), strings.ToLower(y.Path))
		}
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return strings.Compare(x.Path, y.Path)
	})
	return out
}

func sizePresetBytes(preset int) int64 {
	switch preset {
	case 1:
		return 1024 * 1024
	case 2:
		return 10 * 1024 * 1024
	case 3:
		return 100 * 1024 * 1024
	case 4:
		return 1024 * 1024 * 1024
	default:
		return 0
	}
}

func parseSimpleExtFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	out := make(map[string]bool, len(parts))
	for _, p := range parts {
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(p), "."))
		if clean != "" {
			out[clean] = true
		}
	}
	return out
}

func absInt64(v int64) int64 {
	if v == math.MinInt64 {
		return math.MaxInt64
	}
	if v < 0 {
		return -v
	}
	return v
}
