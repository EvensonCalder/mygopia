package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
	"mygopia/locales"
)

func (a *App) renderBackupsView(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	sources := st.SourceList()
	idxStats, hasIdx := a.indexer.Stats(prof.ID)

	ui.Scroll(c.Key("backups-scroll")).Fill().Padding(16, 20).Children(func() {
		ui.Column(c).Gap(14).Children(func() {
			// Summary metrics strip
			ui.Row(c).Wrap().Gap(12).Children(func() {
				storageTitle := "—"
				storageSub := prof.StorageHint
				if st.Status != nil && st.Status.Storage != "" {
					storageTitle = strings.ToUpper(st.Status.Storage)
					if st.ContentStats != nil && st.ContentStats.PackedBytes > 0 {
						storageSub = formatBytes(st.ContentStats.PackedBytes)
					}
				}
				metricCard(c, locales.Storage(), storageTitle, shortenMiddle(storageSub, 30))

				srcSub := ""
				if st.Sources != nil && st.Sources.LocalHost != "" {
					srcSub = st.Sources.LocalUsername + "@" + st.Sources.LocalHost
				}
				metricCard(c, locales.ActiveSources(), intToStr(len(sources)), srcSub)

				idxVal := locales.NotIndexedYet()
				idxSub := ""
				if idxStats.Indexing {
					idxVal = locales.Indexing()
				} else if hasIdx {
					if idxStats.SnapshotCount == 1 {
						idxVal = locales.N1Snapshot()
					} else {
						idxVal = locales.SnapshotCount(idxStats.SnapshotCount)
					}
					idxSub = locales.IndexFilesDeletedSummary(idxStats.FileCount, idxStats.DeletedCount)
				}
				metricCard(c, locales.SearchIndex(), idxVal, idxSub)

				activeTasks := st.ActiveTaskCount()
				taskSub := locales.AllTasksIdle()
				if activeTasks > 0 {
					taskSub = locales.ActiveTasksCount(activeTasks)
				}
				metricCard(c, locales.RunningTasks(), intToStr(activeTasks), taskSub)
			})

			// Action bar above Sources
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, locales.NavBackups()).FontSize(14).FontWeight(600)
				ui.Spacer(c)
				if len(sources) > 0 {
					ui.Button(c, locales.BackupAll()).OnClick(func() {
						a.triggerSnapshotAction(nil)
					})
				}
				ui.PrimaryButton(c, "").Gap(6).Children(func() {
					ui.Icon(c, iconPlus).FontSize(13.5)
					ui.Text(c, locales.AddFolder()).SingleLine()
				}).OnClick(func() {
					a.newSourcePath = ""
					a.newSourceSnapNow = true
					a.addSourceModalOpen = true
				})
			})

			if len(sources) == 0 {
				card(c, func() {
					emptyState(
						c,
						iconFolder,
						locales.NoBackupSourcesYet(),
						locales.AddALocalFolderToStartEncrypted(),
						locales.AddFolder(),
						func() {
							a.newSourcePath = ""
							a.newSourceSnapNow = true
							a.addSourceModalOpen = true
						},
					)
				})
				return
			}

			// Source cards
			ui.Column(c).Gap(8).Children(func() {
				for _, item := range sources {
					src := item
					srcKey := src.Source.String()
					ui.Column(c.Key("src-" + srcKey)).
						Padding(11, 14).
						Gap(6).
						Radius(8).
						Background(p.cardBg).
						Border(1, p.border).
						Children(func() {
							ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
								ui.Icon(c, iconFolder).FontSize(16.5).TextColor(p.accent)
								ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
									ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
										ui.Text(c, src.Source.Path).FontSize(13.5).FontWeight(600).SingleLine()
										a.renderSourceStatusBadge(c, src.Status)
									})
									hostLine := src.Source.UserName + "@" + src.Source.Host
									ui.Text(c, hostLine).FontSize(11.5).TextColor(p.muted).SingleLine()
								})

								// Quick ergonomic actions
								ui.Row(c).Gap(6).Shrink(0).Children(func() {
									isRunning := src.Status == "UPLOADING" || src.Status == "PENDING"
									if isRunning {
										ui.Button(c, locales.CancelBackup()).OnClick(func() {
											sCopy := src.Source
											a.cancelSnapshotAction(&sCopy)
										})
									} else {
										ui.PrimaryButton(c, locales.BackupNow()).OnClick(func() {
											sCopy := src.Source
											a.triggerSnapshotAction(&sCopy)
										})
									}

									if src.Status == "PAUSED" {
										ui.Button(c, locales.Resume()).OnClick(func() {
											sCopy := src.Source
											a.pauseResumeSourceAction(sCopy, false)
										})
									} else {
										ui.Button(c, locales.Pause()).OnClick(func() {
											sCopy := src.Source
											a.pauseResumeSourceAction(sCopy, true)
										})
									}

									ui.Button(c, locales.Browse()).OnClick(func() {
										a.openSnapshotsForSource(src.Source)
									})
									ui.Button(c, locales.Policy()).OnClick(func() {
										a.openPolicyForTarget(src.Source)
									})
								})
							})

							// Live Upload Progress Bar when UPLOADING
							if src.Upload != nil && (src.Status == "UPLOADING" || src.Status == "PENDING") {
								done := src.Upload.CachedBytes + src.Upload.HashedBytes
								var frac float64 = -1
								if src.Upload.EstimatedBytes > 0 {
									frac = min(1.0, float64(done)/float64(src.Upload.EstimatedBytes))
								}
								ui.Column(c).Gap(4).Padding(2, 0, 0, 0).Children(func() {
									ui.Progress(c, frac).Label(locales.Uploading())
									ui.Row(c).Gap(12).Children(func() {
										ui.Textf(c, "%s / %s", formatBytes(done), formatBytes(src.Upload.EstimatedBytes)).
											FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
										if src.Upload.CurrentDirectory != "" {
											ui.Text(c, shortenMiddle(src.Upload.CurrentDirectory, 55)).
												FontSize(11.5).TextColor(p.muted).SingleLine()
										}
									})
								})
							}

							// Metadata footer row
							ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
								if src.LastSnapshot != nil && !src.LastSnapshot.StartTime.IsZero() {
									lastStr := formatShortTime(src.LastSnapshot.StartTime.Time)
									if src.LastSnapshot.RootEntry != nil && src.LastSnapshot.RootEntry.DirSummary != nil {
										lastStr += " · " + formatBytes(src.LastSnapshot.RootEntry.DirSummary.TotalFileSize)
									}
									ui.Text(c, locales.LastBackupTime(lastStr)).FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
								} else {
									ui.Text(c, locales.NeverBackedUp()).FontSize(11.5).TextColor(p.muted)
								}

								if src.NextSnapshotTime != nil && !src.NextSnapshotTime.IsZero() {
									ui.Text(c, locales.NextRunTime(formatShortTime(src.NextSnapshotTime.Time))).
										FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
								} else if src.Schedule.Manual {
									ui.Text(c, locales.ManualSchedule()).FontSize(11.5).TextColor(p.muted)
								}

								ui.Spacer(c)
								ui.Row(c).AlignItems(ui.Center).Gap(6).Shrink(0).Children(func() {
									localDir, hasLocal := a.resolveLocalDiffDir(src.Source, st)
									if src.LastSnapshot != nil && hasLocal {
										compactButton(c, locales.DiffLocal()).OnClick(func() {
											a.openDiffForSource(src.Source, "", diffTargetLocalDir)
										})
									}
									if a.desktopRunning && hasLocal {
										compactButton(c, locales.RevealInFileManager()).OnClick(func() {
											_ = mygo.Shell.OpenPath(localDir)
										})
									}
									compactButton(c, locales.RemoveSource()).TextColor(c.Theme().Danger).OnClick(func() {
										a.removeSourceAction(src.Source)
									})
								})
							})
						})
				}
			})
		})
	})
}

func (a *App) renderSourceStatusBadge(c *ui.Context, status string) {
	p := paletteOf(c)
	t := c.Theme()
	switch status {
	case "UPLOADING":
		statusPill(c, locales.Uploading(), t.Accent, p.accentSoft)
	case "PENDING":
		statusPill(c, locales.Pending(), t.Warning, p.warningSoft)
	case "PAUSED":
		statusPill(c, locales.Paused(), t.Warning, p.warningSoft)
	case "FAILED":
		statusPill(c, locales.Failed(), t.Danger, p.dangerSoft)
	default:
		statusPill(c, locales.Idle(), t.Success, p.successSoft)
	}
}

func (a *App) renderAddSourceModal(c *ui.Context) {
	if !a.addSourceModalOpen {
		return
	}
	ui.Modal(c, &a.addSourceModalOpen, func() {
		ui.Column(c).Width(460).Gap(16).Children(func() {
			ui.Text(c, locales.AddBackupSource()).FontSize(15.5).FontWeight(600)
			ui.Form(c, func() {
				ui.Field(c, locales.FolderPath(), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("new-src-path"), &a.newSourcePath).
							Placeholder("/Users/you/Documents").
							AutoFocus().
							Grow(1)
						if a.desktopRunning {
							if ui.Button(c, locales.BrowsePicker()).Clicked() {
								go a.pickDirectoryInto(&a.newSourcePath)
							}
						}
					})
				})
				ui.Field(c, "", func() {
					ui.Checkbox(c, &a.newSourceSnapNow, locales.StartFirstBackupImmediately())
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					a.addSourceModalOpen = false
				}
				ui.PrimaryButton(c, locales.AddBackup()).Disabled(strings.TrimSpace(a.newSourcePath) == "").OnClick(func() {
					folder := filepath.Clean(config.ExpandPath(a.newSourcePath))
					snapNow := a.newSourceSnapNow
					a.addSourceModalOpen = false
					a.createSourceAction(folder, snapNow)
				})
			})
		})
	})
}

func (a *App) createSourceAction(folder string, snapNow bool) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
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
		if _, err := cli.CreateSource(ctx, folder, snapNow, &kopia.Policy{}); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.SourceAdded(), false)
	}()
}

func (a *App) triggerSnapshotAction(src *kopia.SourceInfo) {
	repoID := a.settings.ActiveRepoID
	autoIndex := a.settings.AutoIndex
	if a.manager == nil || repoID == "" {
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
		if err := cli.TriggerSnapshot(ctx, src); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.BackupTriggered(), false)

		if autoIndex {
			go func() {
				time.Sleep(3 * time.Second)
				st, ok := a.manager.State(repoID)
				if ok && st.Connected() {
					idxCtx, idxCancel := context.WithTimeout(context.Background(), 2*time.Minute)
					_ = a.indexer.IndexRepo(idxCtx, repoID, st.Profile.Name, cli)
					idxCancel()
					a.uiUpdate(func() {
						a.searchDirty = true
					})
				}
			}()
		}
	}()
}

func (a *App) cancelSnapshotAction(src *kopia.SourceInfo) {
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
		if err := cli.CancelSnapshot(ctx, src); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
	}()
}

func (a *App) pauseResumeSourceAction(src kopia.SourceInfo, pause bool) {
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
		if pause {
			err = cli.PauseSource(ctx, &src)
		} else {
			err = cli.ResumeSource(ctx, &src)
		}
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
	}()
}

func (a *App) removeSourceAction(src kopia.SourceInfo) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	go func() {
		if !a.confirmDestructive(locales.RemoveSource()+": "+src.Path+"?", locales.Remove()) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		snaps, err := cli.ListSnapshots(ctx, src, true)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		ids := make([]string, 0, len(snaps.Snapshots))
		for _, sn := range snaps.Snapshots {
			ids = append(ids, sn.ID)
		}
		err = cli.DeleteSnapshots(ctx, kopia.DeleteSnapshotsRequest{
			Source:                src,
			SnapshotManifestIDs:   ids,
			DeleteSourceAndPolicy: true,
		})
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.SourceRemoved(), false)
	}()
}
