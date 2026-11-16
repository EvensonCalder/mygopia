// @scope.file app.view_snapshots
// @hash sha256:d032290fc4d5b704
// @implements spec.desktop_ui sha256:67347d72ce79be0a
// @ref default github.com/egoist/mygo@0.9.1
// @ref default github.com/egoist/mygo/ui@0.9.1
// @ref default config.store sha256:b49642aa028d360a
// @ref default index.diff sha256:bffd1e6ac38d6e3a
// @ref default index.engine sha256:1aea8ec7725d31ae
// @ref default kopia.client sha256:c1253141ff6ec455
// @ref default kopia.manager sha256:332d271a00525eae
// @ref default kopia.types sha256:06dd81559d688041
// @ref default app.cache sha256:6e5e1b91e6872ef4
// @ref default app.core sha256:4089493662be47e2
// @ref default app.icons sha256:a3df148b095e3dde
// @ref default app.theme sha256:5167155d01845017
// @ref default app.view_connect sha256:90b187964bdf27c9
// @ref default app.view_diff sha256:7a1c425c5f83abf0
// @ref default app.view_policies sha256:144a290ee4d6d2f2
// Snapshot timeline, directory tree browser, FUSE/WebDAV mount integration, and file restore modal.

package app

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func (a *App) openSnapshotsForSource(src kopia.SourceInfo) {
	srcStr := src.String()
	if a.snapSourceStr != srcStr {
		a.snapSourceStr = srcStr
		a.selectedSnapIdx = -1
		a.browserBreadcrumbs = nil
		a.setBrowserEntriesSlice(nil)
	}
	a.activeNav = navSnapshots
	a.reloadSnapshotsAsync()
}

func (a *App) openSnapshotByID(repoID string, src kopia.SourceInfo, snapID string) {
	if repoID != "" && repoID != a.settings.ActiveRepoID {
		a.activateRepo(repoID)
	}
	a.snapSourceStr = src.String()
	a.activeNav = navSnapshots

	if a.manager == nil {
		for i, sn := range a.snapshots {
			if sn.ID == snapID {
				a.selectedSnapIdx = i
				a.openSnapshotRoot(sn)
				break
			}
		}
		return
	}

	targetRepo := a.settings.ActiveRepoID
	targetSourceStr := a.snapSourceStr
	includeIdentical := a.snapShowIdentical

	// Instant first-frame render if source snapshots are already cached
	seeded := false
	if cached, ok := a.lookupCachedSnapshots(targetRepo, targetSourceStr, includeIdentical); ok && len(cached) > 0 {
		seeded = true
		a.setSnapshotsSlice(cached)
		for i, sn := range cached {
			if sn.ID == snapID {
				a.selectedSnapIdx = i
				a.openSnapshotRoot(sn)
				return
			}
		}
	}

	prof, st := a.activeRepoState()
	a.snapLoadSeq++
	seq := a.snapLoadSeq
	a.snapLoading = true
	a.snapLoadSlow = a.estimateSnapListSlow(prof, st, seeded)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		snaps, err := a.fetchSnapshotsCoalesced(ctx, targetRepo, src, includeIdentical)
		if err != nil {
			a.uiUpdate(func() {
				if a.snapLoadSeq == seq {
					a.snapLoading = false
					a.snapLoadSlow = false
				}
			})
			return
		}
		a.uiUpdate(func() {
			if a.snapLoadSeq != seq || a.settings.ActiveRepoID != targetRepo || a.snapSourceStr != targetSourceStr {
				return
			}
			a.snapLoading = false
			a.snapLoadSlow = false
			a.setSnapshotsSlice(snaps)
			idx := 0
			for i, sn := range snaps {
				if sn.ID == snapID {
					idx = i
					break
				}
			}
			if len(snaps) > 0 {
				a.selectedSnapIdx = idx
				a.openSnapshotRoot(snaps[idx])
			} else {
				a.selectedSnapIdx = -1
				a.browserBreadcrumbs = nil
				a.setBrowserEntriesSlice(nil)
			}
		})
	}()
}

func (a *App) reloadSnapshotsAsync() {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	prof, st := a.activeRepoState()
	sources := st.SourceList()
	if len(sources) == 0 {
		return
	}
	var chosen *kopia.SourceInfo
	for _, s := range sources {
		if s.Source.String() == a.snapSourceStr {
			cp := s.Source
			chosen = &cp
			break
		}
	}
	if chosen == nil {
		cp := sources[0].Source
		chosen = &cp
		a.snapSourceStr = cp.String()
	}

	includeIdentical := a.snapShowIdentical
	targetSource := *chosen
	targetSourceStr := targetSource.String()

	// Stale-While-Revalidate: if target source is already cached, render immediately in 0ms!
	seeded := false
	if cached, ok := a.lookupCachedSnapshots(repoID, targetSourceStr, includeIdentical); ok && len(cached) > 0 {
		seeded = true
		if !snapshotsEqual(a.snapshots, cached) {
			a.setSnapshotsSlice(cached)
			if a.selectedSnapIdx < 0 || a.selectedSnapIdx >= len(cached) {
				a.selectedSnapIdx = 0
				a.openSnapshotRoot(cached[0])
			}
		} else if a.selectedSnapIdx < 0 {
			a.selectedSnapIdx = 0
			a.openSnapshotRoot(cached[0])
		} else if len(a.browserBreadcrumbs) == 0 {
			a.openSnapshotRoot(cached[a.selectedSnapIdx])
		}
	}

	var initialSnapID string
	if a.selectedSnapIdx >= 0 && a.selectedSnapIdx < len(a.snapshots) {
		initialSnapID = a.snapshots[a.selectedSnapIdx].ID
	}
	initialLeafOID := a.currentBrowserOID()

	a.snapLoadSeq++
	seq := a.snapLoadSeq
	a.snapLoading = true
	a.snapLoadSlow = a.estimateSnapListSlow(prof, st, seeded)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		snaps, err := a.fetchSnapshotsCoalesced(ctx, repoID, targetSource, includeIdentical)
		if err != nil {
			a.uiUpdate(func() {
				if a.snapLoadSeq == seq {
					a.snapLoading = false
					a.snapLoadSlow = false
				}
			})
			a.toast(err.Error(), true)
			return
		}
		a.uiUpdate(func() {
			if a.snapLoadSeq != seq || a.settings.ActiveRepoID != repoID || a.snapSourceStr != targetSourceStr {
				return
			}
			a.snapLoading = false
			a.snapLoadSlow = false

			// Fast no-op exit when background revalidation confirms cached snapshot list is already identical!
			if snapshotsEqual(a.snapshots, snaps) && a.selectedSnapIdx >= 0 && a.selectedSnapIdx < len(snaps) && len(a.browserBreadcrumbs) > 0 && len(a.browserEntries) > 0 {
				return
			}

			var currentSnapID string
			if a.selectedSnapIdx >= 0 && a.selectedSnapIdx < len(a.snapshots) {
				currentSnapID = a.snapshots[a.selectedSnapIdx].ID
			}
			userChangedSelection := (currentSnapID != "" && currentSnapID != initialSnapID) ||
				(a.currentBrowserOID() != "" && a.currentBrowserOID() != initialLeafOID)

			wantSnapID := currentSnapID
			if wantSnapID == "" {
				wantSnapID = initialSnapID
			}

			a.setSnapshotsSlice(snaps)
			if len(snaps) == 0 {
				a.selectedSnapIdx = -1
				a.browserBreadcrumbs = nil
				a.setBrowserEntriesSlice(nil)
				return
			}
			preserved := -1
			if wantSnapID != "" {
				for i, sn := range snaps {
					if sn.ID == wantSnapID {
						preserved = i
						break
					}
				}
			}
			if preserved >= 0 {
				a.selectedSnapIdx = preserved
				if userChangedSelection && len(a.browserBreadcrumbs) > 0 {
					return
				}
				if len(a.browserBreadcrumbs) == 0 {
					a.openSnapshotRoot(snaps[preserved])
				} else {
					curOID := a.browserBreadcrumbs[len(a.browserBreadcrumbs)-1].OID
					if len(a.browserEntries) == 0 {
						a.loadDirectoryEntriesAsync(curOID)
					}
				}
			} else {
				a.selectedSnapIdx = 0
				a.openSnapshotRoot(snaps[0])
			}
		})
	}()
}

func (a *App) currentBrowserOID() string {
	if len(a.browserBreadcrumbs) > 0 {
		return a.browserBreadcrumbs[len(a.browserBreadcrumbs)-1].OID
	}
	if a.selectedSnapIdx >= 0 && a.selectedSnapIdx < len(a.snapshots) && a.snapshots[a.selectedSnapIdx] != nil {
		return a.snapshots[a.selectedSnapIdx].RootID
	}
	return ""
}

func (a *App) openSnapshotRoot(sn *kopia.Snapshot) {
	if sn == nil || sn.RootID == "" {
		return
	}
	a.browserBreadcrumbs = []dirCrumb{{Name: "", OID: sn.RootID}}
	a.browserFilter = ""
	a.browserPageOffset = 0
	a.loadDirectoryEntriesWithHint(sn.RootID, sn.Summary)
}

func (a *App) loadDirectoryEntriesAsync(dirOID string) {
	a.loadDirectoryEntriesWithHint(dirOID, nil)
}

func (a *App) loadDirectoryEntriesWithHint(dirOID string, summary *kopia.DirectorySummary) {
	repoID := a.settings.ActiveRepoID
	if repoID == "" || dirOID == "" {
		return
	}
	a.browserLoadSeq++
	seq := a.browserLoadSeq
	a.browserSelectedRow = -1

	// Fast synchronous path (< 20 µs): hit L1 LRU or bbolt DAG cache with zero network RPC or loading flicker!
	if a.indexer != nil {
		if cached, ok := a.indexer.LookupDirectory(repoID, dirOID); ok {
			a.browserLoading = false
			a.browserLoadSlow = false
			a.setBrowserEntriesSlice(index.ToKopiaDirEntries(cached))
			return
		}
	}

	if a.manager == nil {
		return
	}
	prof, st := a.activeRepoState()
	a.browserLoading = true
	a.browserLoadSlow = a.estimateDirLoadSlow(prof, st, summary)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		entries, err := a.fetchDirectoryCoalesced(ctx, repoID, dirOID)
		if err != nil {
			a.uiUpdate(func() {
				if a.browserLoadSeq == seq {
					a.browserLoading = false
					a.browserLoadSlow = false
					a.setBrowserEntriesSlice(nil)
				}
			})
			a.toast(err.Error(), true)
			return
		}
		a.uiUpdate(func() {
			if a.browserLoadSeq != seq || a.settings.ActiveRepoID != repoID || a.currentBrowserOID() != dirOID {
				return
			}
			a.browserLoading = false
			a.browserLoadSlow = false
			a.setBrowserEntriesSlice(entries)
		})
	}()
}

func (a *App) renderSnapshotsView(c *ui.Context, _ config.RepoProfile, st kopia.RepoSnapshotState) {
	sources := st.SourceList()
	if len(sources) == 0 {
		emptyState(c, iconSnapshots, locales.NoBackupSourcesYet(), locales.AddALocalFolderToStartEncrypted(), locales.AddFolder(), func() {
			a.activeNav = navBackups
			a.addSourceModalOpen = true
		})
		return
	}

	if a.snapSourceStr == "" {
		a.snapSourceStr = sources[0].Source.String()
		a.reloadSnapshotsAsync()
	}

	ui.Split(c, &a.snapSplitWidth, func() {
		a.renderSnapshotTimelinePane(c, st)
	}, func() {
		a.renderSnapshotBrowserPane(c, st)
	}).SplitReset(285).Fill()
}

func (a *App) hasActiveSnapTimelineFilters() bool {
	return a.snapTimePreset != timePresetAll ||
		strings.TrimSpace(a.snapYearFilter) != "" ||
		strings.TrimSpace(a.snapFilterText) != "" ||
		a.snapPinnedOnly
}

func (a *App) resetSnapTimelineFilters() {
	a.snapTimePreset = timePresetAll
	a.snapYearFilter = ""
	a.snapDateFrom = time.Time{}
	a.snapDateFromText = ""
	a.snapDateTo = time.Time{}
	a.snapDateToText = ""
	a.snapFilterText = ""
	a.snapPinnedOnly = false
	a.snapDisplayLimit = defaultSnapshotPageSize
}

func (a *App) renderSnapshotTimelinePane(c *ui.Context, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	sources := st.SourceList()
	srcKeys, srcLabels := sourceSelectOptions(sources, st)
	curSrcLabel := ""
	if len(srcLabels) > 0 {
		curSrcLabel = srcLabels[0]
		for i, k := range srcKeys {
			if k == a.snapSourceStr {
				curSrcLabel = srcLabels[i]
				break
			}
		}
	}

	ui.Column(c).Fill().Padding(12).Gap(8).Background(p.cardBg).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			if ui.Select(c.Key("snap-source-sel"), &curSrcLabel, srcLabels).Label(locales.SelectSource()).MinWidth(0).Grow(1).Changed() {
				for i, lb := range srcLabels {
					if lb == curSrcLabel && i < len(srcKeys) {
						if a.snapSourceStr != srcKeys[i] {
							a.snapSourceStr = srcKeys[i]
							a.selectedSnapIdx = -1
							a.browserBreadcrumbs = nil
							a.setBrowserEntriesSlice(nil)
						}
						break
					}
				}
				a.snapDisplayLimit = defaultSnapshotPageSize
				a.reloadSnapshotsAsync()
			}
			if len(a.snapshots) >= 2 {
				if ui.Button(c, "").Label(locales.CompareProgress()).Tooltip(locales.CompareSnapshots()).Padding(5, 7).Children(func() {
					ui.Icon(c, iconDiff).FontSize(13.5)
				}).Clicked() {
					src := parseTargetKey(a.snapSourceStr, st)
					baseID := a.snapshots[1].ID
					if a.selectedSnapIdx > 0 && a.selectedSnapIdx < len(a.snapshots) {
						baseID = a.snapshots[a.selectedSnapIdx].ID
					}
					a.openDiffForSource(src, baseID, a.snapshots[0].ID)
				}
			}
		})

		// Time-range & multi-year quick jumper (prevents endless scrolling across 10-year histories)
		timeLabels := timeRangePresetLabels()
		if a.snapTimePreset < 0 || a.snapTimePreset >= len(timeLabels) {
			a.snapTimePreset = 0
		}
		curTimeLabel := timeLabels[a.snapTimePreset]
		years := snapshotYears(a.snapshots)
		curYear := a.snapYearFilter
		if curYear == "" || !slices.Contains(years, curYear) {
			curYear = years[0]
		}

		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			if ui.Select(c.Key("snap-time-sel"), &curTimeLabel, timeLabels).Label(locales.TimeRange()).MinWidth(0).Grow(1).Changed() {
				for i, lb := range timeLabels {
					if lb == curTimeLabel {
						a.snapTimePreset = i
						a.snapDisplayLimit = defaultSnapshotPageSize
						break
					}
				}
			}
			if len(years) > 2 {
				if ui.Select(c.Key("snap-year-sel"), &curYear, years).MinWidth(0).Width(104).Shrink(0).Changed() {
					if curYear == years[0] {
						a.snapYearFilter = ""
					} else {
						a.snapYearFilter = curYear
					}
					a.snapDisplayLimit = defaultSnapshotPageSize
				}
			}
		})

		if a.snapTimePreset == timePresetCustom {
			if renderDateRangeInputs(c, "snap", &a.snapDateFrom, &a.snapDateFromText, &a.snapDateTo, &a.snapDateToText) {
				a.snapDisplayLimit = defaultSnapshotPageSize
			}
		}

		if ui.SearchField(c.Key("snap-note-filter"), &a.snapFilterText).
			Placeholder(locales.FilterNotesOrTime()).
			Changed() {
			a.snapDisplayLimit = defaultSnapshotPageSize
		}

		ui.Row(c).AlignItems(ui.Center).Wrap().Gap(10).Children(func() {
			if ui.Checkbox(c, &a.snapShowIdentical, locales.ShowIdenticalSnapshots()).Changed() {
				a.reloadSnapshotsAsync()
			}
			if ui.Checkbox(c, &a.snapPinnedOnly, locales.PinnedOnly()).Changed() {
				a.snapDisplayLimit = defaultSnapshotPageSize
			}
			if a.hasActiveSnapTimelineFilters() {
				ui.Spacer(c)
				if compactButton(c, locales.ResetFilters()).Clicked() {
					a.resetSnapTimelineFilters()
				}
			}
		})

		ui.Box(c).Height(1).Background(p.border).Shrink(0)

		if a.snapLoading && len(a.snapshots) == 0 {
			ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, locales.Loading()).TextColor(p.muted)
			})
			return
		}
		if len(a.snapshots) == 0 {
			emptyState(c, iconSnapshots, locales.NoSnapshotsForThisSource(), "", "", nil)
			return
		}

		filteredIdx := a.filteredSnapshotIndices()
		if len(filteredIdx) == 0 {
			emptyState(c, iconSearch, locales.NoMatchingItems(), "", locales.ResetFilters(), func() {
				a.resetSnapTimelineFilters()
			})
			return
		}

		// When active filters exclude the previously selected snapshot, automatically select the first matching snapshot
		if !slices.Contains(filteredIdx, a.selectedSnapIdx) {
			a.selectedSnapIdx = filteredIdx[0]
			a.openSnapshotRoot(a.snapshots[a.selectedSnapIdx])
		}

		if a.snapDisplayLimit <= 0 {
			a.snapDisplayLimit = defaultSnapshotPageSize
		}
		shownCount := min(len(filteredIdx), a.snapDisplayLimit)

		// Map selectedSnapIdx (index in a.snapshots) <-> visible list selection
		visibleSelected := -1
		for vi := range shownCount {
			if filteredIdx[vi] == a.selectedSnapIdx {
				visibleSelected = vi
				break
			}
		}
		a.snapList.Selected = &visibleSelected

		listEl := ui.List(c, &a.snapList, shownCount, func(vi int) {
			origIdx := filteredIdx[vi]
			sn := a.snapshots[origIdx]
			selected := origIdx == a.selectedSnapIdx
			fg, subFg, _ := tableRowColors(c, selected)

			row := ui.Column(c.Key("snap-"+sn.ID)).Padding(8, 10).Gap(3).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, formatTimestamp(sn.StartTime.Time)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(fg).Grow(1).SingleLine()
					if len(sn.Pins) > 0 {
						tableStatusPill(c, selected, locales.Pinned(), p.accent, p.accentSoft)
					}
				})
				sizeStr := "—"
				filesStr := ""
				if sn.Summary != nil {
					sizeStr = formatBytes(sn.Summary.TotalFileSize)
					filesStr = intToStr(int(sn.Summary.TotalFileCount)) + " " + locales.Files()
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, sizeStr).FontSize(11.5).FontFeatures("tnum").TextColor(subFg)
					if filesStr != "" {
						ui.Text(c, "· "+filesStr).FontSize(11.5).TextColor(subFg).SingleLine()
					}
				})
				if sn.Description != "" {
					ui.Text(c, sn.Description).FontSize(11).TextColor(subFg).SingleLine()
				}
			})

			row.ContextMenu(func(m *ui.Menu) {
				isPinned := len(sn.Pins) > 0
				pinLabel := locales.Pin()
				if isPinned {
					pinLabel = locales.Unpin()
				}
				if m.Item(pinLabel).Chosen() {
					a.toggleSnapshotPinAction(sn)
				}
				if m.Item(locales.EditNote()).Chosen() {
					a.editSnapID = sn.ID
					a.editSnapDesc = sn.Description
					a.editSnapPinned = isPinned
					a.editSnapModalOpen = true
				}
				if len(a.snapshots) >= 2 && sn.ID != a.snapshots[0].ID {
					if m.Item(locales.CompareWithLatest()).Chosen() {
						src := parseTargetKey(a.snapSourceStr, st)
						a.openDiffForSource(src, sn.ID, a.snapshots[0].ID)
					}
				}
				if m.Item(locales.Restore()).Chosen() {
					a.openRestoreModal(a.settings.ActiveRepoID, sn.RootID, "snapshot-"+sn.StartTime.Local().Format("20060102-1504"))
				}
				m.Separator()
				if m.Item(locales.DeleteSnapshot()).Chosen() {
					a.deleteSnapshotAction(sn)
				}
			})
		}).Grow(1)

		if listEl.Changed() && visibleSelected >= 0 && visibleSelected < shownCount {
			a.selectedSnapIdx = filteredIdx[visibleSelected]
			a.openSnapshotRoot(a.snapshots[a.selectedSnapIdx])
		}

		// Lazy load footer when snapshots exceed display limit
		if remaining := len(filteredIdx) - shownCount; remaining > 0 {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				if compactButton(c, locales.LoadMoreRemaining(remaining)).Grow(1).Clicked() {
					a.snapDisplayLimit += defaultSnapshotPageSize
				}
				if remaining > defaultSnapshotPageSize {
					if compactButton(c, locales.ShowAllCount(len(filteredIdx))).Clicked() {
						a.snapDisplayLimit = len(filteredIdx)
					}
				}
			})
		} else if len(filteredIdx) != len(a.snapshots) {
			ui.Text(c, locales.FilteredOfTotalSnapshots(len(filteredIdx), len(a.snapshots))).FontSize(11).TextColor(p.muted)
		}
	})
}

func (a *App) filteredSnapshotIndices() []int {
	q := strings.ToLower(strings.TrimSpace(a.snapFilterText))
	yearFilter := strings.TrimSpace(a.snapYearFilter)
	mkey := snapTimelineMemoKey{
		rev:        a.snapshotsRev,
		timePreset: a.snapTimePreset,
		yearFilter: yearFilter,
		dateFrom:   a.snapDateFrom.UnixNano(),
		dateTo:     a.snapDateTo.UnixNano(),
		query:      q,
		pinnedOnly: a.snapPinnedOnly,
	}
	if a.snapMemoIdx != nil && a.snapMemoKey == mkey && a.snapTimePreset == timePresetAll {
		return a.snapMemoIdx
	}

	after, before := resolveTimeRangeBounds(a.snapTimePreset, a.snapDateFrom, a.snapDateTo)
	out := make([]int, 0, len(a.snapshots))
	for i, sn := range a.snapshots {
		if sn == nil {
			continue
		}
		if a.snapPinnedOnly && len(sn.Pins) == 0 {
			continue
		}
		t := sn.StartTime.Time
		if !after.IsZero() && t.Before(after) {
			continue
		}
		if !before.IsZero() && t.After(before) {
			continue
		}
		if yearFilter != "" && strconv.Itoa(sn.StartTime.Local().Year()) != yearFilter {
			continue
		}
		if q != "" && !snapshotMatchesTextQuery(sn, q) {
			continue
		}
		out = append(out, i)
	}
	a.snapMemoKey = mkey
	a.snapMemoIdx = out
	return out
}

func (a *App) renderSnapshotBrowserPane(c *ui.Context, st kopia.RepoSnapshotState) {
	if a.selectedSnapIdx < 0 || a.selectedSnapIdx >= len(a.snapshots) {
		emptyState(c, iconFolder, locales.SelectASnapshotToBrowseFiles(), "", "", nil)
		return
	}
	if len(a.browserBreadcrumbs) > 1 && c.Shortcut(ui.Cmd, ui.KeyUp) {
		prevIdx := len(a.browserBreadcrumbs) - 2
		a.browserBreadcrumbs = a.browserBreadcrumbs[:prevIdx+1]
		a.browserPageOffset = 0
		a.loadDirectoryEntriesAsync(a.browserBreadcrumbs[prevIdx].OID)
	}
	sn := a.snapshots[a.selectedSnapIdx]
	currentOID := sn.RootID
	if len(a.browserBreadcrumbs) > 0 {
		currentOID = a.browserBreadcrumbs[len(a.browserBreadcrumbs)-1].OID
	}

	// Check if currentOID is mounted
	var mountedPath string
	for _, m := range st.Mounts {
		if m.Root == currentOID || m.Root == sn.RootID {
			mountedPath = m.Path
			break
		}
	}

	ui.Column(c).Fill().Padding(14, 16).Gap(10).Children(func() {
		// Snapshot Action Header
		ui.Row(c).AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			if len(a.browserBreadcrumbs) > 1 {
				if ui.Button(c, locales.Up()).Clicked() {
					prevIdx := len(a.browserBreadcrumbs) - 2
					a.browserBreadcrumbs = a.browserBreadcrumbs[:prevIdx+1]
					a.browserPageOffset = 0
					a.loadDirectoryEntriesAsync(a.browserBreadcrumbs[prevIdx].OID)
				}
			}

			crumbNames := make([]string, len(a.browserBreadcrumbs))
			for i, cr := range a.browserBreadcrumbs {
				if i == 0 && cr.Name == "" {
					crumbNames[i] = locales.Root()
				} else {
					crumbNames[i] = cr.Name
				}
			}
			chosenCrumb := len(crumbNames) - 1
			if len(crumbNames) > 0 {
				if ui.Breadcrumbs(c, crumbNames, &chosenCrumb).Label(locales.Path()).Changed() && chosenCrumb >= 0 && chosenCrumb < len(a.browserBreadcrumbs) {
					a.browserBreadcrumbs = a.browserBreadcrumbs[:chosenCrumb+1]
					a.browserPageOffset = 0
					a.loadDirectoryEntriesAsync(a.browserBreadcrumbs[chosenCrumb].OID)
				}
			}

			ui.Spacer(c)

			curSnapSrc := parseTargetKey(a.snapSourceStr, st)
			_, snapHasLocal := a.resolveLocalDiffDir(curSnapSrc, st)
			if len(a.snapshots) >= 2 || (len(a.snapshots) == 1 && snapHasLocal) {
				ui.Button(c, locales.CompareProgress()).OnClick(func() {
					if sn.ID == a.snapshots[0].ID {
						if snapHasLocal {
							a.openDiffForSource(curSnapSrc, sn.ID, diffTargetLocalDir)
						} else if len(a.snapshots) >= 2 {
							a.openDiffForSource(curSnapSrc, a.snapshots[1].ID, a.snapshots[0].ID)
						}
					} else {
						a.openDiffForSource(curSnapSrc, sn.ID, a.snapshots[0].ID)
					}
				})
			}

			if mountedPath != "" {
				if a.desktopRunning {
					ui.Button(c, locales.OpenMount()).OnClick(func() {
						_ = mygo.Shell.OpenPath(mountedPath)
					})
				}
				ui.Button(c, locales.Unmount()).OnClick(func() {
					a.unmountAction(sn.RootID)
				})
			} else {
				ui.Button(c, locales.Mount()).OnClick(func() {
					a.mountAction(sn.RootID)
				})
			}

			isPinned := len(sn.Pins) > 0
			pinBtnText := locales.Pin()
			if isPinned {
				pinBtnText = locales.Unpin()
			}
			ui.Button(c, pinBtnText).OnClick(func() {
				a.toggleSnapshotPinAction(sn)
			})

			ui.Button(c, locales.EditNote()).OnClick(func() {
				a.editSnapID = sn.ID
				a.editSnapDesc = sn.Description
				a.editSnapPinned = isPinned
				a.editSnapModalOpen = true
			})

			ui.PrimaryButton(c, locales.Restore()).OnClick(func() {
				defName := "restore"
				if len(a.browserBreadcrumbs) > 1 {
					defName = a.browserBreadcrumbs[len(a.browserBreadcrumbs)-1].Name
				}
				a.openRestoreModal(a.settings.ActiveRepoID, currentOID, defName)
			})
		})

		// Directory Entry Filter Bar (Name, Kind, Size, Modified Time Range)
		ui.Row(c).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
			if ui.SearchField(c.Key("dir-filter"), &a.browserFilter).
				Placeholder(locales.FilterCurrentFolder()).
				Grow(1).
				MinWidth(150).
				Changed() {
				a.browserPageOffset = 0
			}

			if ui.Segmented(c, &a.browserKindSeg, locales.All(), locales.Files(), locales.Folders()).Changed() {
				a.browserPageOffset = 0
			}

			sizeLabels := []string{locales.AnySize(), locales.SizeOver1MB(), locales.SizeOver10MB(), locales.SizeOver100MB(), locales.SizeOver1GB()}
			if a.browserSizePreset < 0 || a.browserSizePreset >= len(sizeLabels) {
				a.browserSizePreset = 0
			}
			curSize := sizeLabels[a.browserSizePreset]
			if ui.Select(c.Key("dir-size-sel"), &curSize, sizeLabels).Width(104).Changed() {
				for i, lb := range sizeLabels {
					if lb == curSize {
						a.browserSizePreset = i
						a.browserPageOffset = 0
						break
					}
				}
			}

			timeLabels := timeRangePresetLabels()
			if a.browserTimePreset < 0 || a.browserTimePreset >= len(timeLabels) {
				a.browserTimePreset = 0
			}
			curTime := timeLabels[a.browserTimePreset]
			if ui.Select(c.Key("dir-time-sel"), &curTime, timeLabels).Width(118).Changed() {
				for i, lb := range timeLabels {
					if lb == curTime {
						a.browserTimePreset = i
						a.browserPageOffset = 0
						break
					}
				}
			}

			if a.browserFilter != "" || a.browserKindSeg != 0 || a.browserSizePreset != 0 || a.browserTimePreset != 0 {
				if compactButton(c, locales.ResetFilters()).Clicked() {
					a.browserFilter = ""
					a.browserKindSeg = 0
					a.browserSizePreset = 0
					a.browserTimePreset = 0
					a.browserDateFrom = time.Time{}
					a.browserDateFromText = ""
					a.browserDateTo = time.Time{}
					a.browserDateToText = ""
					a.browserPageOffset = 0
				}
			}
		})

		if a.browserTimePreset == timePresetCustom {
			if renderDateRangeInputs(c, "dir", &a.browserDateFrom, &a.browserDateFromText, &a.browserDateTo, &a.browserDateToText) {
				a.browserPageOffset = 0
			}
		}

		// Filter, sort & paginate directory entries
		filtered := a.filteredBrowserEntries()
		totalFiltered := len(filtered)
		if a.browserPageOffset >= totalFiltered {
			a.browserPageOffset = max(0, ((totalFiltered-1)/defaultTablePageSize)*defaultTablePageSize)
		}
		pageEnd := min(totalFiltered, a.browserPageOffset+defaultTablePageSize)
		var pageEntries []*kopia.DirEntry
		if totalFiltered > 0 {
			pageEntries = filtered[a.browserPageOffset:pageEnd]
		}

		cols := []ui.TableColumn{
			{ID: "name", Title: locales.Name(), Width: 270, MinWidth: 160, Sortable: true},
			{ID: "size", Title: locales.Size(), Width: 96, Align: ui.End, Sortable: true},
			{ID: "mtime", Title: locales.Modified(), Width: 172, Sortable: true},
			{ID: "oid", Title: locales.ObjectID(), Width: 150},
		}

		a.browserTable.Selected = &a.browserSelectedRow
		a.browserTable.Sort = &a.browserSort

		p := paletteOf(c)
		browserIncomplete := a.browserLoading || (a.snapLoading && len(a.snapshots) == 0)

		card(c, func() {
			tbl := ui.Table(c, &a.browserTable, cols, len(pageEntries), func(row, col int) {
				ent := pageEntries[row]
				selected := row == a.browserSelectedRow
				primary, secondary, iconAccent := tableRowColors(c, selected)
				switch col {
				case 0:
					nameColor := primary
					dirIconColor := iconAccent
					if browserIncomplete {
						if selected {
							nameColor = secondary
							dirIconColor = secondary
						} else {
							nameColor = p.muted
							dirIconColor = p.muted
						}
					}
					rowBox := ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
						if ent.IsDir() {
							ui.Icon(c, iconFolder).FontSize(14).TextColor(dirIconColor)
						} else {
							ui.Icon(c, iconFile).FontSize(14).TextColor(secondary)
						}
						ui.Text(c, ent.Name).TextColor(nameColor).SingleLine()
					})
					rowBox.ContextMenu(func(m *ui.Menu) {
						if ent.IsDir() {
							if m.Item(locales.Browse()).Chosen() {
								a.enterSubdirectory(ent)
							}
						} else if a.desktopRunning {
							if m.Item(locales.SaveFile()).Chosen() {
								go a.saveSingleObjectInteractive(a.settings.ActiveRepoID, ent.ObjectID, ent.Name)
							}
						}
						if m.Item(locales.Restore()).Chosen() {
							a.openRestoreModal(a.settings.ActiveRepoID, ent.ObjectID, ent.Name)
						}
						m.Separator()
						if m.Item(locales.CopyObjectID()).Chosen() {
							if a.desktopRunning {
								mygo.Clipboard.WriteText(ent.ObjectID)
							}
							a.toast(locales.CopiedToClipboard(), false)
						}
					})
				case 1:
					ui.Text(c, formatBytes(ent.LogicalSize())).FontFeatures("tnum").TextColor(primary).SingleLine()
				case 2:
					ui.Text(c, formatTimestamp(ent.ModTime.Time)).FontFeatures("tnum").TextColor(secondary).SingleLine()
				case 3:
					ui.Text(c, shortenMiddle(ent.ObjectID, 16)).Font("monospace").FontSize(11.5).TextColor(secondary).SingleLine()
				}
			}).Grow(1)

			if tbl.Submitted() && a.browserSelectedRow >= 0 && a.browserSelectedRow < len(pageEntries) {
				chosen := pageEntries[a.browserSelectedRow]
				if chosen.IsDir() {
					a.enterSubdirectory(chosen)
				} else if a.desktopRunning {
					go a.saveSingleObjectInteractive(a.settings.ActiveRepoID, chosen.ObjectID, chosen.Name)
				}
			}
		}).Grow(1).Padding(0)

		renderPaginationBar(c, "dir-browser", &a.browserPageOffset, defaultTablePageSize, totalFiltered)
	})
}

func (a *App) enterSubdirectory(ent *kopia.DirEntry) {
	if ent == nil || !ent.IsDir() || ent.ObjectID == "" {
		return
	}
	a.browserBreadcrumbs = append(a.browserBreadcrumbs, dirCrumb{Name: ent.Name, OID: ent.ObjectID})
	a.browserFilter = ""
	a.browserPageOffset = 0
	a.loadDirectoryEntriesWithHint(ent.ObjectID, ent.DirSummary)
}

func (a *App) filteredBrowserEntries() []*kopia.DirEntry {
	q := strings.ToLower(strings.TrimSpace(a.browserFilter))
	col := a.browserSort.Column
	desc := a.browserSort.Descending
	mkey := browserFilterMemoKey{
		rev:        a.browserEntriesRev,
		query:      q,
		kindSeg:    a.browserKindSeg,
		sizePreset: a.browserSizePreset,
		timePreset: a.browserTimePreset,
		dateFrom:   a.browserDateFrom.UnixNano(),
		dateTo:     a.browserDateTo.UnixNano(),
		sortCol:    col,
		sortDesc:   desc,
	}
	if a.browserMemoEntries != nil && a.browserMemoKey == mkey && a.browserTimePreset == timePresetAll {
		return a.browserMemoEntries
	}

	minBytes := sizePresetBytes(a.browserSizePreset)
	after, before := resolveTimeRangeBounds(a.browserTimePreset, a.browserDateFrom, a.browserDateTo)

	out := make([]*kopia.DirEntry, 0, len(a.browserEntries))
	for _, e := range a.browserEntries {
		if a.browserKindSeg == 1 && e.IsDir() {
			continue
		}
		if a.browserKindSeg == 2 && !e.IsDir() {
			continue
		}
		if minBytes > 0 && e.LogicalSize() < minBytes {
			continue
		}
		if !after.IsZero() && e.ModTime.Time.Before(after) {
			continue
		}
		if !before.IsZero() && e.ModTime.Time.After(before) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Name), q) {
			continue
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(x, y *kopia.DirEntry) int {
		if col == "name" || col == "" {
			if x.IsDir() != y.IsDir() {
				if x.IsDir() {
					return -1
				}
				return 1
			}
		}
		var c int
		switch col {
		case "size":
			c = cmp.Compare(x.LogicalSize(), y.LogicalSize())
		case "mtime":
			c = x.ModTime.Compare(y.ModTime.Time)
		default:
			c = index.CompareFoldPath(x.Name, y.Name)
		}
		if desc {
			c = -c
		}
		return c
	})
	a.browserMemoKey = mkey
	a.browserMemoEntries = out
	return out
}

func (a *App) openRestoreModal(repoID, rootOID, defaultName string) {
	if repoID == "" {
		repoID = a.settings.ActiveRepoID
	}
	a.restoreRepoID = repoID
	a.restoreTargetOID = rootOID
	a.restoreDefaultName = defaultName
	a.restoreMode = 0
	a.restoreDestPath = ""
	a.restoreModalOpen = true
}

func (a *App) renderSnapshotModals(c *ui.Context) {
	p := paletteOf(c)
	if a.editSnapModalOpen {
		ui.Modal(c, &a.editSnapModalOpen, func() {
			ui.Column(c).Width(420).Gap(16).Children(func() {
				ui.Text(c, locales.EditNote()).FontSize(15.5).FontWeight(600)
				ui.Form(c, func() {
					ui.Field(c, locales.SnapshotNote(), func() {
						ui.TextInput(c.Key("snap-note-in"), &a.editSnapDesc).AutoFocus()
					})
					ui.Field(c, "", func() {
						ui.Checkbox(c, &a.editSnapPinned, locales.Pinned())
					})
				})
				ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						a.editSnapModalOpen = false
					}
					ui.PrimaryButton(c, locales.SaveNote()).OnClick(func() {
						a.editSnapModalOpen = false
						a.saveSnapshotEditAction(a.editSnapID, a.editSnapDesc, a.editSnapPinned)
					})
				})
			})
		})
	}

	if a.restoreModalOpen {
		ui.Modal(c, &a.restoreModalOpen, func() {
			ui.Column(c).Width(500).Gap(16).Children(func() {
				ui.Text(c, locales.RestoreFromSnapshot()).FontSize(15.5).FontWeight(600)
				ui.Row(c).Children(func() {
					ui.Segmented(c, &a.restoreMode, locales.RestoreToFolder(), locales.ExportAsZIPArchive(), locales.ExportAsTARArchive())
				})

				ui.Form(c, func() {
					ui.Field(c, locales.TargetDestination(), func() {
						ui.Row(c).Gap(8).Children(func() {
							placeholder := defaultRestorePlaceholder(a.restoreDefaultName)
							if a.restoreMode == 1 {
								placeholder += ".zip"
							} else if a.restoreMode == 2 {
								placeholder += ".tar"
							}
							ui.TextInput(c.Key("restore-dest"), &a.restoreDestPath).Placeholder(placeholder).Grow(1)
							if a.desktopRunning {
								if ui.Button(c, locales.BrowsePicker()).Clicked() {
									if a.restoreMode == 0 {
										go a.pickDirectoryInto(&a.restoreDestPath)
									} else {
										ext := ".zip"
										if a.restoreMode == 2 {
											ext = ".tar"
										}
										go func() {
											p, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
												Parent:      a.window,
												DefaultPath: a.restoreDefaultName + ext,
											})
											if err == nil && p != "" {
												a.uiUpdate(func() { a.restoreDestPath = p })
											}
										}()
									}
								}
							}
						})
					})
				})

				if a.restoreMode == 0 {
					ui.Column(c).Padding(12, 14).Gap(8).Radius(8).Background(p.subtleBg).Border(1, p.borderSoft).Children(func() {
						ui.Checkbox(c, &a.restoreOverwriteF, locales.OverwriteExistingFiles())
						ui.Checkbox(c, &a.restoreOverwriteD, locales.OverwriteExistingDirectories())
						ui.Checkbox(c, &a.restoreIncremental, locales.IncrementalSkipUnchangedFiles())
						ui.Checkbox(c, &a.restoreDeleteExtra, locales.DeleteExtraFilesInTarget())
						ui.Checkbox(c, &a.restoreIgnorePerm, locales.IgnorePermissionErrors())
						ui.Checkbox(c, &a.restoreSkipOwners, locales.SkipRestoringFileOwners())
					})
				}

				ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						a.restoreModalOpen = false
					}
					ui.PrimaryButton(c, locales.StartRestore()).Disabled(strings.TrimSpace(a.restoreDestPath) == "").OnClick(func() {
						a.restoreModalOpen = false
						a.executeRestoreAction()
					})
				})
			})
		})
	}
}

func defaultRestorePlaceholder(defaultName string) string {
	name := strings.TrimSpace(defaultName)
	if name == "" {
		name = "restored-snapshot"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Downloads", name)
	}
	if runtime.GOOS == "windows" {
		return `C:\Users\User\Downloads\` + name
	}
	return "/home/user/Downloads/" + name
}

func (a *App) executeRestoreAction() {
	repoID := a.restoreRepoID
	if repoID == "" {
		repoID = a.settings.ActiveRepoID
	}
	if a.manager == nil || repoID == "" || a.restoreTargetOID == "" {
		return
	}
	dest := config.ExpandPath(a.restoreDestPath)
	req := kopia.RestoreRequest{
		Root: a.restoreTargetOID,
		Options: kopia.RestoreOptions{
			Incremental:  a.restoreIncremental,
			DeleteExtra:  a.restoreDeleteExtra,
			IgnoreErrors: false,
		},
	}
	switch a.restoreMode {
	case 1:
		req.ZipFile = dest
	case 2:
		req.TarFile = dest
	default:
		req.Filesystem = &kopia.FilesystemOutput{
			TargetPath:             dest,
			OverwriteFiles:         a.restoreOverwriteF,
			OverwriteDirectories:   a.restoreOverwriteD,
			OverwriteSymlinks:      true,
			IgnorePermissionErrors: a.restoreIgnorePerm,
			SkipOwners:             a.restoreSkipOwners,
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if _, err := cli.StartRestore(ctx, req); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.RestoreTaskStarted(), false)
	}()
}

func (a *App) saveSingleObjectInteractive(repoID, oid, defaultFilename string) {
	if !a.desktopRunning || oid == "" {
		return
	}
	if repoID == "" {
		repoID = a.settings.ActiveRepoID
	}
	savePath, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      a.window,
		DefaultPath: filepath.Base(defaultFilename),
	})
	if err != nil || savePath == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cli, err := a.manager.ClientFor(ctx, repoID)
	if err != nil {
		a.toast(err.Error(), true)
		return
	}
	if err := cli.DownloadObjectToFile(ctx, oid, filepath.Base(defaultFilename), savePath); err != nil {
		a.toast(err.Error(), true)
		return
	}
	a.toast(locales.FileSavedTo(savePath), false)
}

func (a *App) toggleSnapshotPinAction(sn *kopia.Snapshot) {
	if sn == nil {
		return
	}
	req := kopia.EditSnapshotsRequest{Snapshots: []string{sn.ID}}
	if len(sn.Pins) > 0 {
		req.RemovePins = sn.Pins
	} else {
		req.AddPins = []string{"do-not-delete"}
	}
	a.runSnapshotEdit(req)
}

func (a *App) saveSnapshotEditAction(snapID, desc string, pinned bool) {
	req := kopia.EditSnapshotsRequest{
		Snapshots:      []string{snapID},
		NewDescription: &desc,
	}
	if pinned {
		req.AddPins = []string{"do-not-delete"}
	} else {
		req.RemovePins = []string{"do-not-delete"}
	}
	a.runSnapshotEdit(req)
}

func (a *App) runSnapshotEdit(req kopia.EditSnapshotsRequest) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.EditSnapshots(ctx, req); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.invalidateRepoSnapshotsCache(repoID)
		a.toast(locales.SnapshotUpdated(), false)
		a.uiUpdate(func() {
			a.reloadSnapshotsAsync()
		})
	}()
}

func (a *App) deleteSnapshotAction(sn *kopia.Snapshot) {
	if sn == nil {
		return
	}
	repoID := a.settings.ActiveRepoID
	_, st := a.activeRepoState()
	var src kopia.SourceInfo
	for _, s := range st.SourceList() {
		if s.Source.String() == a.snapSourceStr {
			src = s.Source
			break
		}
	}
	go func() {
		if !a.confirmDestructive(locales.DeleteSnapshot()+" ("+formatTimestamp(sn.StartTime.Time)+")?", locales.Delete()) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		err = cli.DeleteSnapshots(ctx, kopia.DeleteSnapshotsRequest{
			Source:              src,
			SnapshotManifestIDs: []string{sn.ID},
		})
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.invalidateRepoSnapshotsCache(repoID)
		a.toast(locales.SnapshotDeleted(), false)
		a.uiUpdate(func() {
			a.reloadSnapshotsAsync()
		})
	}()
}

func (a *App) mountAction(rootOID string) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" || rootOID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		m, err := cli.MountSnapshot(ctx, rootOID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.SnapshotMountedAt(m.Path), false)
		if a.desktopRunning && m.Path != "" {
			_ = mygo.Shell.OpenPath(m.Path)
		}
	}()
}

func (a *App) unmountAction(rootOID string) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" || rootOID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.UnmountSnapshot(ctx, rootOID); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.SnapshotUnmounted(), false)
	}()
}
