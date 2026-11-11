package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
)

func (a *App) triggerIndexActiveRepo() {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	prof, _ := a.activeRepoState()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := a.indexer.IndexRepo(ctx, repoID, prof.Name, cli); err != nil {
			a.toast(err.Error(), true)
			return
		}
		if stats, ok := a.indexer.Stats(repoID); ok {
			a.toast(trf("Index updated (%d files, %d folders)", stats.FileCount, stats.FolderCount), false)
		}
	}()
}

func (a *App) recomputeSearchIfNeeded() {
	if !a.searchDirty {
		return
	}
	a.searchDirty = false

	repoScope := a.settings.ActiveRepoID
	if a.searchAllRepos {
		repoScope = ""
	}
	srcFilter := a.searchSourceFilter
	if srcFilter == tr("All Sources") {
		srcFilter = ""
	}

	var kind index.ItemKind
	switch a.searchKindSeg {
	case 1:
		kind = index.KindFiles
	case 2:
		kind = index.KindFolders
	default:
		kind = index.KindBoth
	}

	minSize := sizePresetBytes(a.searchSizePreset)
	var maxSize int64
	switch a.searchMaxSizePreset {
	case 1:
		maxSize = 1 * 1024 * 1024
	case 2:
		maxSize = 10 * 1024 * 1024
	case 3:
		maxSize = 100 * 1024 * 1024
	case 4:
		maxSize = 1024 * 1024 * 1024
	}

	var status index.StatusFilter
	switch a.searchStatusSeg {
	case 1:
		status = index.StatusOnDiskOnly
	case 2:
		status = index.StatusDeletedOnly
	default:
		if a.searchDeletedOnly {
			status = index.StatusDeletedOnly
		} else {
			status = index.StatusAll
		}
	}

	minVer := 0
	if a.searchMultiVersion {
		minVer = 2
	}

	after, before := resolveTimeRangeBounds(a.searchTimePreset, a.searchDateFrom, a.searchDateTo)

	var sortBy index.SortField
	switch a.searchSort.Column {
	case "path":
		sortBy = index.SortPath
	case "versions":
		sortBy = index.SortVersions
	case "last":
		sortBy = index.SortLastSeen
	case "first":
		sortBy = index.SortFirstSeen
	case "source":
		sortBy = index.SortSource
	default:
		sortBy = index.SortSize
	}

	a.searchResult = a.indexer.Search(index.Query{
		Text:         a.searchQuery,
		RepoID:       repoScope,
		SourceFilter: srcFilter,
		PathPrefix:   a.searchPathPrefix,
		Kind:         kind,
		Status:       status,
		DeletedOnly:  a.searchDeletedOnly,
		MinSize:      minSize,
		MaxSize:      maxSize,
		MinVersions:  minVer,
		IncludeExts:  a.searchIncludeExts,
		ExcludeExts:  a.searchExcludeExts,
		SeenAfter:    after,
		SeenBefore:   before,
		SortBy:       sortBy,
		SortDesc:     a.searchSort.Descending,
		Offset:       a.searchPageOffset,
		Limit:        defaultTablePageSize,
	})
	if a.searchPageOffset > 0 && len(a.searchResult.Items) == 0 && a.searchResult.TotalMatched > 0 {
		a.searchPageOffset = 0
		a.searchDirty = true
		a.recomputeSearchIfNeeded()
		return
	}
	if a.searchSelectedRow >= len(a.searchResult.Items) {
		a.searchSelectedRow = -1
	}
}

func (a *App) hasActiveSearchFilters() bool {
	return a.searchKindSeg != 0 ||
		a.searchSourceFilter != "" ||
		a.searchTimePreset != timePresetAll ||
		a.searchSizePreset != 0 ||
		a.searchMaxSizePreset != 0 ||
		a.searchStatusSeg != 0 ||
		a.searchDeletedOnly ||
		a.searchMultiVersion ||
		strings.TrimSpace(a.searchIncludeExts) != "" ||
		strings.TrimSpace(a.searchExcludeExts) != "" ||
		strings.TrimSpace(a.searchPathPrefix) != ""
}

func (a *App) resetSearchFilters() {
	a.searchKindSeg = 0
	a.searchSourceFilter = ""
	a.searchTimePreset = timePresetAll
	a.searchDateFrom = time.Time{}
	a.searchDateFromText = ""
	a.searchDateTo = time.Time{}
	a.searchDateToText = ""
	a.searchSizePreset = 0
	a.searchMaxSizePreset = 0
	a.searchStatusSeg = 0
	a.searchDeletedOnly = false
	a.searchMultiVersion = false
	a.searchIncludeExts = ""
	a.searchExcludeExts = ""
	a.searchPathPrefix = ""
	a.searchPageOffset = 0
	a.searchDirty = true
}

func (a *App) renderSearchView(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	idxStats, hasIdx := a.indexer.Stats(prof.ID)
	anyStats := a.indexer.AllStats()

	ui.Column(c).Fill().Padding(16, 18).Gap(10).Children(func() {
		// Top Search Bar + Index Controls
		ui.Row(c).AlignItems(ui.Center).Gap(10).Role(ui.RoleSearch).Children(func() {
			sf := ui.SearchField(c.Key("global-search"), &a.searchQuery).
				Placeholder(tr("Search files & folders across all snapshots…")).
				Label(tr("Search")).
				AutoFocus().
				Grow(1)
			if a.focusSearchNextFrame {
				sf.Focus()
				a.focusSearchNextFrame = false
			}
			if sf.Changed() {
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			scopeSeg := 0
			if a.searchAllRepos {
				scopeSeg = 1
			}
			if ui.Segmented(c, &scopeSeg, tr("Current Repository"), tr("All Repositories")).Changed() {
				a.searchAllRepos = scopeSeg == 1
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			btnText := tr("Update Index")
			if idxStats.Indexing {
				btnText = tr("Indexing…")
			}
			ui.PrimaryButton(c, btnText).
				Tooltip(tr("Refresh (⌘R) syncs repository & incremental index; click here to manually scan snapshot trees (cached folders are skipped).")).
				Disabled(idxStats.Indexing || a.manager == nil || !st.Connected()).
				OnClick(func() {
					a.triggerIndexActiveRepo()
				})
		})

		// Primary Filter Row (including Time Range Picker & Advanced Toggle)
		ui.Row(c).AlignItems(ui.Center).Wrap().Gap(8).Children(func() {
			if ui.Segmented(c, &a.searchKindSeg, tr("All"), tr("Files"), tr("Folders")).Changed() {
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			// Source dropdown (path-first labels without redundant local user@host prefix)
			srcKeys, srcLabels := sourceSelectOptions(st.SourceList(), st)
			allSrcLabel := tr("All Sources")
			srcOpts := append([]string{allSrcLabel}, srcLabels...)
			curSrc := allSrcLabel
			if a.searchSourceFilter != "" {
				for i, k := range srcKeys {
					if k == a.searchSourceFilter {
						curSrc = srcLabels[i]
						break
					}
				}
			}
			if ui.Select(c.Key("search-src-sel"), &curSrc, srcOpts).Width(176).Changed() {
				if curSrc == allSrcLabel {
					a.searchSourceFilter = ""
				} else {
					for i, lb := range srcLabels {
						if lb == curSrc && i < len(srcKeys) {
							a.searchSourceFilter = srcKeys[i]
							break
						}
					}
				}
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			// Time Range Preset Dropdown
			timeLabels := timeRangePresetLabels()
			if a.searchTimePreset < 0 || a.searchTimePreset >= len(timeLabels) {
				a.searchTimePreset = 0
			}
			curTimeLabel := timeLabels[a.searchTimePreset]
			if ui.Select(c.Key("search-time-sel"), &curTimeLabel, timeLabels).Width(124).Changed() {
				for i, lb := range timeLabels {
					if lb == curTimeLabel {
						a.searchTimePreset = i
						break
					}
				}
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			// Min Size dropdown
			sizeLabels := []string{tr("Any Size"), tr("> 1 MB"), tr("> 10 MB"), tr("> 100 MB"), tr("> 1 GB")}
			if a.searchSizePreset < 0 || a.searchSizePreset >= len(sizeLabels) {
				a.searchSizePreset = 0
			}
			curSize := sizeLabels[a.searchSizePreset]
			if ui.Select(c.Key("search-size-sel"), &curSize, sizeLabels).Width(98).Changed() {
				for i, lb := range sizeLabels {
					if lb == curSize {
						a.searchSizePreset = i
						break
					}
				}
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			if ui.Checkbox(c, &a.searchDeletedOnly, tr("Deleted only")).Changed() {
				if a.searchDeletedOnly {
					a.searchStatusSeg = 2
				} else if a.searchStatusSeg == 2 {
					a.searchStatusSeg = 0
				}
				a.searchPageOffset = 0
				a.searchDirty = true
			}
			if ui.Checkbox(c, &a.searchMultiVersion, tr("Versions ≥ 2")).Changed() {
				a.searchPageOffset = 0
				a.searchDirty = true
			}

			ui.Spacer(c)

			advLabel := tr("Advanced Filters")
			if a.searchShowAdvanced {
				advLabel = tr("Hide Filters")
			}
			if compactButton(c, advLabel).Clicked() {
				a.searchShowAdvanced = !a.searchShowAdvanced
			}

			if a.hasActiveSearchFilters() {
				if compactButton(c, tr("Reset Filters")).Clicked() {
					a.resetSearchFilters()
				}
			}
		})

		// Custom Date Range Row (when Custom Date Range is chosen)
		if a.searchTimePreset == timePresetCustom {
			if renderDateRangeInputs(c, "search", &a.searchDateFrom, &a.searchDateFromText, &a.searchDateTo, &a.searchDateToText) {
				a.searchPageOffset = 0
				a.searchDirty = true
			}
		}

		// Secondary Advanced Filter Row (Status, Max Size, Path Prefix, Extensions)
		if a.searchShowAdvanced {
			ui.Row(c).
				AlignItems(ui.Center).
				Wrap().
				Gap(8).
				Padding(7, 10).
				Radius(7).
				Background(p.subtleBg).
				Border(1, p.borderSoft).
				Children(func() {
					if ui.Segmented(c, &a.searchStatusSeg, tr("All Status"), tr("On Disk Only"), tr("Deleted Only")).Changed() {
						a.searchDeletedOnly = a.searchStatusSeg == 2
						a.searchPageOffset = 0
						a.searchDirty = true
					}

					ui.Text(c, tr("Max Size")).FontSize(11.5).TextColor(p.muted).SingleLine()
					maxSizeLabels := []string{tr("Any Size"), tr("< 1 MB"), tr("< 10 MB"), tr("< 100 MB"), tr("< 1 GB")}
					if a.searchMaxSizePreset < 0 || a.searchMaxSizePreset >= len(maxSizeLabels) {
						a.searchMaxSizePreset = 0
					}
					curMaxSize := maxSizeLabels[a.searchMaxSizePreset]
					if ui.Select(c.Key("search-max-size-sel"), &curMaxSize, maxSizeLabels).MinWidth(0).Width(98).Changed() {
						for i, lb := range maxSizeLabels {
							if lb == curMaxSize {
								a.searchMaxSizePreset = i
								break
							}
						}
						a.searchPageOffset = 0
						a.searchDirty = true
					}

					if ui.TextInput(c.Key("search-path-prefix"), &a.searchPathPrefix).
						Placeholder(tr("Path prefix (e.g. Designs/)")).
						Grow(1).
						MinWidth(140).
						MaxWidth(210).
						Changed() {
						a.searchPageOffset = 0
						a.searchDirty = true
					}

					if ui.TextInput(c.Key("inc-ext"), &a.searchIncludeExts).
						Placeholder(tr("Include ext (pdf, zip)")).
						Grow(1).
						MinWidth(128).
						MaxWidth(175).
						Changed() {
						a.searchPageOffset = 0
						a.searchDirty = true
					}
					if ui.TextInput(c.Key("exc-ext"), &a.searchExcludeExts).
						Placeholder(tr("Exclude ext (tmp, mp4)")).
						Grow(1).
						MinWidth(128).
						MaxWidth(175).
						Changed() {
						a.searchPageOffset = 0
						a.searchDirty = true
					}
				})
		}

		if !hasIdx && (!a.searchAllRepos || len(anyStats) == 0) {
			card(c, func() {
				emptyState(
					c,
					iconSearch,
					tr("No indexed snapshots yet"),
					tr("Click \"Update Index\" to scan snapshot trees. Unchanged folders are cached locally and never fetched twice."),
					tr("Update Index"),
					func() {
						if a.manager != nil && st.Connected() {
							a.triggerIndexActiveRepo()
						} else {
							a.activeNav = navBackups
						}
					},
				)
			}).Grow(1)
			return
		}

		prevSort := a.searchSort
		a.recomputeSearchIfNeeded()

		// Summary bar
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, trf("Matched %d items (%s)", a.searchResult.TotalMatched, formatBytes(a.searchResult.TotalBytes))).
				FontSize(12).FontFeatures("tnum").TextColor(p.muted)
			ui.Spacer(c)
			if hasIdx && idxStats.SnapshotCount > 0 {
				ui.Text(c, trf("Index Synced (%d snapshots)", idxStats.SnapshotCount)).
					FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
			}
			if hasIdx && !idxStats.UpdatedAt.IsZero() {
				ui.Text(c, trf("DAG Cache: %d dirs · %s", idxStats.CachedDirs, formatShortTime(idxStats.UpdatedAt))).
					FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
			}
		})

		if len(a.searchResult.Items) == 0 {
			card(c, func() {
				emptyState(c, iconSearch, tr("No matching items"), "", "", nil)
			}).Grow(1)
			return
		}

		inspectorOpen := a.searchSelectedRow >= 0 && a.searchSelectedRow < len(a.searchResult.Items)

		// Results Table + Optional Version Inspector Split
		buildTable := func() {
			cols := []ui.TableColumn{
				{ID: "path", Title: tr("Path"), Width: 315, MinWidth: 160, Sortable: true},
				{ID: "status", Title: tr("Status"), Width: 86},
				{ID: "size", Title: tr("Size"), Width: 96, Align: ui.End, Sortable: true},
				{ID: "versions", Title: tr("Versions"), Width: 78, Align: ui.End, Sortable: true},
				{ID: "last", Title: tr("Last Seen"), Width: 144, Sortable: true},
				{ID: "source", Title: tr("Source"), Width: 205, Sortable: true},
			}
			if inspectorOpen {
				cols = []ui.TableColumn{
					{ID: "path", Title: tr("Path"), Width: 180, MinWidth: 130, Sortable: true},
					{ID: "status", Title: tr("Status"), Width: 70},
					{ID: "size", Title: tr("Size"), Width: 80, Align: ui.End, Sortable: true},
					{ID: "versions", Title: tr("Versions"), Width: 70, Align: ui.End, Sortable: true},
					{ID: "last", Title: tr("Last Seen"), Width: 146, Sortable: true},
					{ID: "source", Title: tr("Source"), Width: 96, Sortable: true},
				}
			}
			a.searchTable.Selected = &a.searchSelectedRow
			a.searchTable.Sort = &a.searchSort

			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(8).Children(func() {
				tblCard := card(c, func() {
					ui.Table(c, &a.searchTable, cols, len(a.searchResult.Items), func(row, col int) {
						it := a.searchResult.Items[row]
						selected := row == a.searchSelectedRow
						primary, secondary, iconAccent := tableRowColors(c, selected)
						switch col {
						case 0:
							r := ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
								if it.IsDir {
									ui.Icon(c, iconFolder).FontSize(14).TextColor(iconAccent)
								} else {
									ui.Icon(c, iconFile).FontSize(14).TextColor(secondary)
								}
								ui.Text(c, it.Path).TextColor(primary).SingleLine()
							})
							r.ContextMenu(func(m *ui.Menu) {
								if !it.IsDir && a.desktopRunning {
									if m.Item(tr("Save File…")).Chosen() {
										go a.saveSingleObjectInteractive(it.RepoID, it.LatestObjectID, it.Name)
									}
								}
								if m.Item(tr("Restore…")).Chosen() {
									a.openRestoreModal(it.RepoID, it.LatestObjectID, it.Name)
								}
								if it.LatestSnapshotID != "" {
									if m.Item(tr("Open Snapshot")).Chosen() {
										a.openSnapshotByID(it.RepoID, it.Source, it.LatestSnapshotID)
									}
									if m.Item(tr("Compare…")).Chosen() {
										a.openDiffForSource(it.Source, "", it.LatestSnapshotID)
									}
								}
								m.Separator()
								if m.Item(tr("Copy")).Chosen() {
									if a.desktopRunning {
										mygo.Clipboard.WriteText(it.FullHostPath())
									}
									a.toast(tr("Copied to clipboard"), false)
								}
							})
						case 1:
							if it.OnDisk {
								tableStatusPill(c, selected, tr("On Disk"), c.Theme().Success, p.successSoft)
							} else {
								tableStatusPill(c, selected, tr("Deleted"), c.Theme().Danger, p.dangerSoft)
							}
						case 2:
							ui.Text(c, formatBytes(it.LatestSize)).FontFeatures("tnum").TextColor(primary).SingleLine()
						case 3:
							ui.Text(c, intToStr(it.Versions)).FontFeatures("tnum").TextColor(primary).SingleLine()
						case 4:
							ui.Text(c, formatShortTime(it.LastSeen)).FontFeatures("tnum").TextColor(secondary).SingleLine()
						case 5:
							srcText := it.Source.Path
							if inspectorOpen && !a.searchAllRepos && srcText != "" && len([]rune(srcText)) > 16 {
								srcText = filepath.Base(srcText)
							}
							if a.searchAllRepos && it.RepoName != "" {
								srcText = "[" + it.RepoName + "] " + srcText
							}
							maxChars := 32
							if inspectorOpen {
								maxChars = 18
							}
							ui.Text(c, shortenMiddle(srcText, maxChars)).TextColor(secondary).SingleLine()
						}
					}).Grow(1)

					if a.searchSort != prevSort {
						a.searchPageOffset = 0
						a.searchDirty = true
						c.Invalidate()
					}
				})
				tblCard.Grow(1).Padding(0)

				prevOffset := a.searchPageOffset
				renderPaginationBar(c, "search", &a.searchPageOffset, defaultTablePageSize, a.searchResult.TotalMatched)
				if a.searchPageOffset != prevOffset {
					a.searchSelectedRow = -1
					a.searchDirty = true
					c.Invalidate()
				}
			})
		}

		if inspectorOpen {
			selectedItem := a.searchResult.Items[a.searchSelectedRow]
			ui.SplitPercent(c, &a.searchDetailSplit, func() {
				ui.Column(c).Fill().Margin(0, 8, 0, 0).Children(buildTable)
			}, func() {
				a.renderSearchInspector(c, selectedItem)
			}).SplitReset(64).Grow(1)
		} else {
			buildTable()
		}
	})
}

func (a *App) renderSearchInspector(c *ui.Context, item index.SearchItem) {
	p := paletteOf(c)
	if a.searchVerItemPath != item.Path {
		a.searchVerItemPath = item.Path
		a.searchVerLimit = defaultVersionPageSize
		a.searchVerFilter = ""
	}
	if a.searchVerLimit <= 0 {
		a.searchVerLimit = defaultVersionPageSize
	}

	card(c, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if item.IsDir {
				ui.Icon(c, iconFolder).FontSize(16).TextColor(p.accent)
			} else {
				ui.Icon(c, iconFile).FontSize(16).TextColor(p.accent)
			}
			ui.Text(c, item.Name).FontSize(14).FontWeight(600).Grow(1).SingleLine()
			if compactButton(c, tr("Close")).Clicked() {
				a.searchSelectedRow = -1
			}
		})

		ui.Text(c, item.FullHostPath()).Selectable().FontSize(11.5).TextColor(p.muted)

		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			if item.OnDisk {
				statusPill(c, tr("On Disk"), c.Theme().Success, p.successSoft)
			} else {
				statusPill(c, tr("Deleted"), c.Theme().Danger, p.dangerSoft)
			}
			ui.Text(c, trf("First seen %s · Last seen %s", formatShortTime(item.FirstSeen), formatShortTime(item.LastSeen))).
				FontSize(11.5).TextColor(p.muted)
		})

		ui.Box(c).Height(1).Background(p.border).Shrink(0)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, tr("Version History")).FontSize(12.5).FontWeight(600).Grow(1)
			ui.Badge(c, intToStr(len(item.History)))
		})

		if len(item.History) > 4 {
			if ui.SearchField(c.Key("ver-filter-"+item.Path), &a.searchVerFilter).
				Placeholder(tr("Filter notes or time…")).
				Changed() {
				a.searchVerLimit = defaultVersionPageSize
			}
		}

		// Filter version history by timestamp or OID when query is non-empty
		verFilter := strings.ToLower(strings.TrimSpace(a.searchVerFilter))
		var matchedHistory []index.VersionRecord
		for _, ver := range item.History {
			if verFilter != "" {
				tsStr := strings.ToLower(formatTimestamp(ver.SnapshotTime))
				oidStr := strings.ToLower(ver.ObjectID)
				if !strings.Contains(tsStr, verFilter) && !strings.Contains(oidStr, verFilter) {
					continue
				}
			}
			matchedHistory = append(matchedHistory, ver)
		}

		totalVers := len(matchedHistory)
		visVers := matchedHistory
		if len(visVers) > a.searchVerLimit {
			visVers = visVers[:a.searchVerLimit]
		}

		ui.Scroll(c.Key("ver-hist-" + item.Path)).Grow(1).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				for idx, ver := range visVers {
					v := ver
					ui.Column(c.Key(idx)).
						Padding(10, 12).
						Gap(6).
						Radius(7).
						Background(p.subtleBg).
						Children(func() {
							ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
								ui.Text(c, formatTimestamp(v.SnapshotTime)).FontSize(12).FontWeight(600).FontFeatures("tnum").Grow(1).SingleLine()
								ui.Text(c, formatBytes(v.Size)).FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
							})
							ui.Text(c, shortenMiddle(v.ObjectID, 28)).Font("monospace").FontSize(11).TextColor(p.muted).SingleLine()
							ui.Row(c).Gap(6).Justify(ui.End).Wrap().Padding(2, 0, 0, 0).Children(func() {
								if v.SnapshotID != "" && item.LatestSnapshotID != "" && v.SnapshotID != item.LatestSnapshotID {
									compactButton(c, tr("Diff Latest")).Tooltip(tr("Compare with Latest")).Padding(4, 10).OnClick(func() {
										a.openDiffForSource(item.Source, v.SnapshotID, item.LatestSnapshotID)
									})
								}
								if v.SnapshotID != "" {
									compactButton(c, tr("Locate")).Tooltip(tr("Open Snapshot")).Padding(4, 10).OnClick(func() {
										a.openSnapshotByID(item.RepoID, item.Source, v.SnapshotID)
									})
								}
								if !item.IsDir && a.desktopRunning {
									compactButton(c, tr("Save…")).Tooltip(tr("Save Version…")).Padding(4, 10).OnClick(func() {
										go a.saveSingleObjectInteractive(item.RepoID, v.ObjectID, item.Name)
									})
								}
								compactPrimaryButton(c, tr("Restore…")).Padding(4, 10).OnClick(func() {
									a.openRestoreModal(item.RepoID, v.ObjectID, item.Name)
								})
							})
						})
				}

				if rem := totalVers - len(visVers); rem > 0 {
					ui.Row(c).Gap(6).Justify(ui.Center).Padding(4, 0).Children(func() {
						if compactButton(c, trf("Load More (%d left)", rem)).Clicked() {
							a.searchVerLimit += defaultVersionPageSize
						}
						if rem > defaultVersionPageSize {
							if compactButton(c, trf("Show All (%d)", totalVers)).Clicked() {
								a.searchVerLimit = totalVers
							}
						}
					})
				}
			})
		})
	}).Fill().Margin(0, 0, 0, 8)
}
