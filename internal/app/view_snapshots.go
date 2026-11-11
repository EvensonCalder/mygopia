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
	"mygopia/internal/kopia"
)

func (a *App) openSnapshotsForSource(src kopia.SourceInfo) {
	a.snapSourceStr = src.String()
	a.selectedSnapIdx = -1
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
	includeIdentical := a.snapShowIdentical
	a.snapLoading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, targetRepo)
		if err != nil {
			a.uiUpdate(func() { a.snapLoading = false })
			return
		}
		resp, err := cli.ListSnapshots(ctx, src, includeIdentical)
		if err != nil {
			a.uiUpdate(func() { a.snapLoading = false })
			return
		}
		snaps := resp.Snapshots
		slices.SortFunc(snaps, func(x, y *kopia.Snapshot) int {
			return y.StartTime.Compare(x.StartTime.Time)
		})
		a.uiUpdate(func() {
			a.snapLoading = false
			a.snapshots = snaps
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
			}
		})
	}()
}

func (a *App) reloadSnapshotsAsync() {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	_, st := a.activeRepoState()
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

	var initialSnapID string
	if a.selectedSnapIdx >= 0 && a.selectedSnapIdx < len(a.snapshots) {
		initialSnapID = a.snapshots[a.selectedSnapIdx].ID
	}
	initialLeafOID := a.currentBrowserOID()

	includeIdentical := a.snapShowIdentical
	targetSource := *chosen
	targetSourceStr := targetSource.String()
	a.snapLoadSeq++
	seq := a.snapLoadSeq
	a.snapLoading = true

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.uiUpdate(func() {
				if a.snapLoadSeq == seq {
					a.snapLoading = false
				}
			})
			return
		}
		resp, err := cli.ListSnapshots(ctx, targetSource, includeIdentical)
		if err != nil {
			a.uiUpdate(func() {
				if a.snapLoadSeq == seq {
					a.snapLoading = false
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
			if a.snapLoadSeq != seq || a.settings.ActiveRepoID != repoID || a.snapSourceStr != targetSourceStr {
				return
			}
			a.snapLoading = false

			// Check if the user selected a different snapshot or navigated folders while loading
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

			a.snapshots = snaps
			if len(snaps) == 0 {
				a.selectedSnapIdx = -1
				a.browserBreadcrumbs = nil
				a.browserEntries = nil
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
					// Preserve user's active navigation made during refresh
					return
				}
				if len(a.browserBreadcrumbs) == 0 {
					a.openSnapshotRoot(snaps[preserved])
				} else {
					curOID := a.browserBreadcrumbs[len(a.browserBreadcrumbs)-1].OID
					a.loadDirectoryEntriesAsync(curOID)
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
	a.loadDirectoryEntriesAsync(sn.RootID)
}

func (a *App) loadDirectoryEntriesAsync(dirOID string) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" || dirOID == "" {
		return
	}
	a.browserLoadSeq++
	seq := a.browserLoadSeq
	a.browserLoading = true
	a.browserSelectedRow = -1

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.uiUpdate(func() {
				if a.browserLoadSeq == seq {
					a.browserLoading = false
				}
			})
			return
		}
		dm, err := cli.GetDirectory(ctx, dirOID)
		if err != nil {
			a.uiUpdate(func() {
				if a.browserLoadSeq == seq {
					a.browserLoading = false
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
			a.browserEntries = dm.Entries
		})
	}()
}

func (a *App) renderSnapshotsView(c *ui.Context, _ config.RepoProfile, st kopia.RepoSnapshotState) {
	sources := st.SourceList()
	if len(sources) == 0 {
		emptyState(c, iconSnapshots, tr("No backup sources yet"), tr("Add a local folder to start encrypted, deduplicated backups."), tr("Add Folder"), func() {
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
			if ui.Select(c.Key("snap-source-sel"), &curSrcLabel, srcLabels).Label(tr("Select Source")).MinWidth(0).Grow(1).Changed() {
				for i, lb := range srcLabels {
					if lb == curSrcLabel && i < len(srcKeys) {
						a.snapSourceStr = srcKeys[i]
						break
					}
				}
				a.snapDisplayLimit = defaultSnapshotPageSize
				a.reloadSnapshotsAsync()
			}
			if len(a.snapshots) >= 2 {
				if ui.Button(c, "").Label(tr("Compare…")).Tooltip(tr("Compare Snapshots")).Padding(5, 7).Children(func() {
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
			if ui.Select(c.Key("snap-time-sel"), &curTimeLabel, timeLabels).Label(tr("Time Range")).MinWidth(0).Grow(1).Changed() {
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
			Placeholder(tr("Filter notes or time…")).
			Changed() {
			a.snapDisplayLimit = defaultSnapshotPageSize
		}

		ui.Row(c).AlignItems(ui.Center).Wrap().Gap(10).Children(func() {
			if ui.Checkbox(c, &a.snapShowIdentical, tr("Show identical snapshots")).Changed() {
				a.reloadSnapshotsAsync()
			}
			if ui.Checkbox(c, &a.snapPinnedOnly, tr("Pinned only")).Changed() {
				a.snapDisplayLimit = defaultSnapshotPageSize
			}
			if a.hasActiveSnapTimelineFilters() {
				ui.Spacer(c)
				if compactButton(c, tr("Reset Filters")).Clicked() {
					a.resetSnapTimelineFilters()
				}
			}
		})

		ui.Box(c).Height(1).Background(p.border).Shrink(0)

		if a.snapLoading && len(a.snapshots) == 0 {
			ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, tr("Loading…")).TextColor(p.muted)
			})
			return
		}
		if len(a.snapshots) == 0 {
			emptyState(c, iconSnapshots, tr("No snapshots for this source"), "", "", nil)
			return
		}

		filteredIdx := a.filteredSnapshotIndices()
		if len(filteredIdx) == 0 {
			emptyState(c, iconSearch, tr("No matching items"), "", tr("Reset Filters"), func() {
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

			row := ui.Column(c.Key("snap-" + sn.ID)).Padding(8, 10).Gap(3).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, formatTimestamp(sn.StartTime.Time)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(fg).Grow(1).SingleLine()
					if len(sn.Pins) > 0 {
						tableStatusPill(c, selected, tr("Pinned"), p.accent, p.accentSoft)
					}
				})
				sizeStr := "—"
				filesStr := ""
				if sn.Summary != nil {
					sizeStr = formatBytes(sn.Summary.TotalFileSize)
					filesStr = intToStr(int(sn.Summary.TotalFileCount)) + " " + tr("Files")
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
				pinLabel := tr("Pin")
				if isPinned {
					pinLabel = tr("Unpin")
				}
				if m.Item(pinLabel).Chosen() {
					a.toggleSnapshotPinAction(sn)
				}
				if m.Item(tr("Edit Note")).Chosen() {
					a.editSnapID = sn.ID
					a.editSnapDesc = sn.Description
					a.editSnapPinned = isPinned
					a.editSnapModalOpen = true
				}
				if len(a.snapshots) >= 2 && sn.ID != a.snapshots[0].ID {
					if m.Item(tr("Compare with Latest")).Chosen() {
						src := parseTargetKey(a.snapSourceStr, st)
						a.openDiffForSource(src, sn.ID, a.snapshots[0].ID)
					}
				}
				if m.Item(tr("Restore…")).Chosen() {
					a.openRestoreModal(a.settings.ActiveRepoID, sn.RootID, "snapshot-"+sn.StartTime.Local().Format("20060102-1504"))
				}
				m.Separator()
				if m.Item(tr("Delete Snapshot")).Chosen() {
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
				if compactButton(c, trf("Load More (%d left)", remaining)).Grow(1).Clicked() {
					a.snapDisplayLimit += defaultSnapshotPageSize
				}
				if remaining > defaultSnapshotPageSize {
					if compactButton(c, trf("Show All (%d)", len(filteredIdx))).Clicked() {
						a.snapDisplayLimit = len(filteredIdx)
					}
				}
			})
		} else if len(filteredIdx) != len(a.snapshots) {
			ui.Text(c, trf("%d / %d snapshots", len(filteredIdx), len(a.snapshots))).FontSize(11).TextColor(p.muted)
		}
	})
}

func (a *App) filteredSnapshotIndices() []int {
	after, before := resolveTimeRangeBounds(a.snapTimePreset, a.snapDateFrom, a.snapDateTo)
	q := strings.ToLower(strings.TrimSpace(a.snapFilterText))
	yearFilter := strings.TrimSpace(a.snapYearFilter)

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
	return out
}

func (a *App) renderSnapshotBrowserPane(c *ui.Context, st kopia.RepoSnapshotState) {
	if a.selectedSnapIdx < 0 || a.selectedSnapIdx >= len(a.snapshots) {
		emptyState(c, iconFolder, tr("Select a snapshot to browse files"), "", "", nil)
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
				if ui.Button(c, tr("Up")).Clicked() {
					prevIdx := len(a.browserBreadcrumbs) - 2
					a.browserBreadcrumbs = a.browserBreadcrumbs[:prevIdx+1]
					a.browserPageOffset = 0
					a.loadDirectoryEntriesAsync(a.browserBreadcrumbs[prevIdx].OID)
				}
			}

			crumbNames := make([]string, len(a.browserBreadcrumbs))
			for i, cr := range a.browserBreadcrumbs {
				if i == 0 && cr.Name == "" {
					crumbNames[i] = tr("Root")
				} else {
					crumbNames[i] = cr.Name
				}
			}
			chosenCrumb := len(crumbNames) - 1
			if len(crumbNames) > 0 {
				if ui.Breadcrumbs(c, crumbNames, &chosenCrumb).Label(tr("Path")).Changed() && chosenCrumb >= 0 && chosenCrumb < len(a.browserBreadcrumbs) {
					a.browserBreadcrumbs = a.browserBreadcrumbs[:chosenCrumb+1]
					a.browserPageOffset = 0
					a.loadDirectoryEntriesAsync(a.browserBreadcrumbs[chosenCrumb].OID)
				}
			}

			ui.Spacer(c)

			curSnapSrc := parseTargetKey(a.snapSourceStr, st)
			_, snapHasLocal := a.resolveLocalDiffDir(curSnapSrc, st)
			if len(a.snapshots) >= 2 || (len(a.snapshots) == 1 && snapHasLocal) {
				ui.Button(c, tr("Compare…")).OnClick(func() {
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
					ui.Button(c, tr("Open Mount")).OnClick(func() {
						_ = mygo.Shell.OpenPath(mountedPath)
					})
				}
				ui.Button(c, tr("Unmount")).OnClick(func() {
					a.unmountAction(sn.RootID)
				})
			} else {
				ui.Button(c, tr("Mount")).OnClick(func() {
					a.mountAction(sn.RootID)
				})
			}

			isPinned := len(sn.Pins) > 0
			pinBtnText := tr("Pin")
			if isPinned {
				pinBtnText = tr("Unpin")
			}
			ui.Button(c, pinBtnText).OnClick(func() {
				a.toggleSnapshotPinAction(sn)
			})

			ui.Button(c, tr("Edit Note")).OnClick(func() {
				a.editSnapID = sn.ID
				a.editSnapDesc = sn.Description
				a.editSnapPinned = isPinned
				a.editSnapModalOpen = true
			})

			ui.PrimaryButton(c, tr("Restore…")).OnClick(func() {
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
				Placeholder(tr("Filter current folder…")).
				Grow(1).
				MinWidth(150).
				Changed() {
				a.browserPageOffset = 0
			}

			if ui.Segmented(c, &a.browserKindSeg, tr("All"), tr("Files"), tr("Folders")).Changed() {
				a.browserPageOffset = 0
			}

			sizeLabels := []string{tr("Any Size"), tr("> 1 MB"), tr("> 10 MB"), tr("> 100 MB"), tr("> 1 GB")}
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
				if compactButton(c, tr("Reset Filters")).Clicked() {
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
			{ID: "name", Title: tr("Name"), Width: 270, MinWidth: 160, Sortable: true},
			{ID: "size", Title: tr("Size"), Width: 96, Align: ui.End, Sortable: true},
			{ID: "mtime", Title: tr("Modified"), Width: 172, Sortable: true},
			{ID: "oid", Title: tr("Object ID"), Width: 150},
		}

		a.browserTable.Selected = &a.browserSelectedRow
		a.browserTable.Sort = &a.browserSort

		card(c, func() {
			tbl := ui.Table(c, &a.browserTable, cols, len(pageEntries), func(row, col int) {
				ent := pageEntries[row]
				selected := row == a.browserSelectedRow
				primary, secondary, iconAccent := tableRowColors(c, selected)
				switch col {
				case 0:
					rowBox := ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
						if ent.IsDir() {
							ui.Icon(c, iconFolder).FontSize(14).TextColor(iconAccent)
						} else {
							ui.Icon(c, iconFile).FontSize(14).TextColor(secondary)
						}
						ui.Text(c, ent.Name).TextColor(primary).SingleLine()
					})
					rowBox.ContextMenu(func(m *ui.Menu) {
						if ent.IsDir() {
							if m.Item(tr("Browse")).Chosen() {
								a.enterSubdirectory(ent)
							}
						} else if a.desktopRunning {
							if m.Item(tr("Save File…")).Chosen() {
								go a.saveSingleObjectInteractive(a.settings.ActiveRepoID, ent.ObjectID, ent.Name)
							}
						}
						if m.Item(tr("Restore…")).Chosen() {
							a.openRestoreModal(a.settings.ActiveRepoID, ent.ObjectID, ent.Name)
						}
						m.Separator()
						if m.Item(tr("Copy Object ID")).Chosen() {
							if a.desktopRunning {
								mygo.Clipboard.WriteText(ent.ObjectID)
							}
							a.toast(tr("Copied to clipboard"), false)
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
	a.loadDirectoryEntriesAsync(ent.ObjectID)
}

func (a *App) filteredBrowserEntries() []*kopia.DirEntry {
	q := strings.ToLower(strings.TrimSpace(a.browserFilter))
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
	col := a.browserSort.Column
	desc := a.browserSort.Descending
	slices.SortFunc(out, func(x, y *kopia.DirEntry) int {
		// Keep folders grouped above files when sorting by name
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
			c = strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		}
		if desc {
			c = -c
		}
		return c
	})
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
				ui.Text(c, tr("Edit Note")).FontSize(15.5).FontWeight(600)
				ui.Form(c, func() {
					ui.Field(c, tr("Snapshot Note"), func() {
						ui.TextInput(c.Key("snap-note-in"), &a.editSnapDesc).AutoFocus()
					})
					ui.Field(c, "", func() {
						ui.Checkbox(c, &a.editSnapPinned, tr("Pinned"))
					})
				})
				ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
					if ui.Button(c, tr("Cancel")).Clicked() {
						a.editSnapModalOpen = false
					}
					ui.PrimaryButton(c, tr("Save Note")).OnClick(func() {
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
				ui.Text(c, tr("Restore from Snapshot")).FontSize(15.5).FontWeight(600)
				ui.Row(c).Children(func() {
					ui.Segmented(c, &a.restoreMode, tr("Restore to Folder"), tr("Export as ZIP Archive"), tr("Export as TAR Archive"))
				})

				ui.Form(c, func() {
					ui.Field(c, tr("Target Destination"), func() {
						ui.Row(c).Gap(8).Children(func() {
							placeholder := defaultRestorePlaceholder(a.restoreDefaultName)
							if a.restoreMode == 1 {
								placeholder += ".zip"
							} else if a.restoreMode == 2 {
								placeholder += ".tar"
							}
							ui.TextInput(c.Key("restore-dest"), &a.restoreDestPath).Placeholder(placeholder).Grow(1)
							if a.desktopRunning {
								if ui.Button(c, tr("Browse…")).Clicked() {
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
						ui.Checkbox(c, &a.restoreOverwriteF, tr("Overwrite existing files"))
						ui.Checkbox(c, &a.restoreOverwriteD, tr("Overwrite existing directories"))
						ui.Checkbox(c, &a.restoreIncremental, tr("Incremental (skip unchanged files)"))
						ui.Checkbox(c, &a.restoreDeleteExtra, tr("Delete extra files in target"))
						ui.Checkbox(c, &a.restoreIgnorePerm, tr("Ignore permission errors"))
						ui.Checkbox(c, &a.restoreSkipOwners, tr("Skip restoring file owners"))
					})
				}

				ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
					if ui.Button(c, tr("Cancel")).Clicked() {
						a.restoreModalOpen = false
					}
					ui.PrimaryButton(c, tr("Start Restore")).Disabled(strings.TrimSpace(a.restoreDestPath) == "").OnClick(func() {
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
		a.toast(tr("Restore task started"), false)
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
	a.toast(trf("File saved to %s", savePath), false)
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
		a.toast(tr("Snapshot updated"), false)
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
		if !a.confirmDestructive(tr("Delete Snapshot")+" ("+formatTimestamp(sn.StartTime.Time)+")?", tr("Delete")) {
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
		a.toast(tr("Snapshot deleted"), false)
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
		a.toast(trf("Snapshot mounted at %s", m.Path), false)
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
		a.toast(tr("Snapshot unmounted"), false)
	}()
}
