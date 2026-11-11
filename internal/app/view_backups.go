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
)

func (a *App) renderBackupsView(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	sources := st.SourceList()
	idxStats, hasIdx := a.indexer.Stats(prof.ID)

	ui.Scroll(c.Key("backups-scroll")).Fill().Padding(20).Children(func() {
		ui.Column(c).Gap(18).Children(func() {
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
				metricCard(c, tr("Storage"), storageTitle, shortenMiddle(storageSub, 30))

				srcSub := ""
				if st.Sources != nil && st.Sources.LocalHost != "" {
					srcSub = st.Sources.LocalUsername + "@" + st.Sources.LocalHost
				}
				metricCard(c, tr("Active Sources"), intToStr(len(sources)), srcSub)

				idxVal := tr("Not indexed yet")
				idxSub := ""
				if idxStats.Indexing {
					idxVal = tr("Indexing…")
				} else if hasIdx {
					if idxStats.SnapshotCount == 1 {
						idxVal = tr("1 snapshot")
					} else {
						idxVal = trf("%d snapshots", idxStats.SnapshotCount)
					}
					idxSub = trf("%d files · %d deleted", idxStats.FileCount, idxStats.DeletedCount)
				}
				metricCard(c, tr("Search Index"), idxVal, idxSub)

				activeTasks := st.ActiveTaskCount()
				taskSub := tr("All tasks idle")
				if activeTasks > 0 {
					taskSub = trf("%d active", activeTasks)
				}
				metricCard(c, tr("Running Tasks"), intToStr(activeTasks), taskSub)
			})

			// Action bar above Sources
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, tr("Backups")).FontSize(14).FontWeight(600)
				ui.Spacer(c)
				if len(sources) > 0 {
					ui.Button(c, tr("Backup All")).OnClick(func() {
						a.triggerSnapshotAction(nil)
					})
				}
				ui.PrimaryButton(c, "").Gap(6).Children(func() {
					ui.Icon(c, iconPlus).FontSize(13.5)
					ui.Text(c, tr("Add Folder")).SingleLine()
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
						tr("No backup sources yet"),
						tr("Add a local folder to start encrypted, deduplicated backups."),
						tr("Add Folder"),
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
			ui.Column(c).Gap(10).Children(func() {
				for _, item := range sources {
					src := item
					srcKey := src.Source.String()
					card(c.Key("src-"+srcKey), func() {
						ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
							ui.Icon(c, iconFolder).FontSize(17).TextColor(p.accent)
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
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
									ui.Button(c, tr("Cancel Backup")).OnClick(func() {
										sCopy := src.Source
										a.cancelSnapshotAction(&sCopy)
									})
								} else {
									ui.PrimaryButton(c, tr("Backup Now")).OnClick(func() {
										sCopy := src.Source
										a.triggerSnapshotAction(&sCopy)
									})
								}

								if src.Status == "PAUSED" {
									ui.Button(c, tr("Resume")).OnClick(func() {
										sCopy := src.Source
										a.pauseResumeSourceAction(sCopy, false)
									})
								} else {
									ui.Button(c, tr("Pause")).OnClick(func() {
										sCopy := src.Source
										a.pauseResumeSourceAction(sCopy, true)
									})
								}

								ui.Button(c, tr("Browse")).OnClick(func() {
									a.openSnapshotsForSource(src.Source)
								})
								ui.Button(c, tr("Policy")).OnClick(func() {
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
							ui.Column(c).Gap(4).Padding(6, 0, 0, 0).Children(func() {
								ui.Progress(c, frac).Label(tr("Uploading…"))
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
						ui.Row(c).AlignItems(ui.Center).Gap(16).Padding(4, 0, 0, 0).Children(func() {
							if src.LastSnapshot != nil && !src.LastSnapshot.StartTime.IsZero() {
								lastStr := formatShortTime(src.LastSnapshot.StartTime.Time)
								if src.LastSnapshot.RootEntry != nil && src.LastSnapshot.RootEntry.DirSummary != nil {
									lastStr += " · " + formatBytes(src.LastSnapshot.RootEntry.DirSummary.TotalFileSize)
								}
								ui.Text(c, trf("Last backup: %s", lastStr)).FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
							} else {
								ui.Text(c, tr("Never backed up")).FontSize(11.5).TextColor(p.muted)
							}

							if src.NextSnapshotTime != nil && !src.NextSnapshotTime.IsZero() {
								ui.Text(c, trf("Next run: %s", formatShortTime(src.NextSnapshotTime.Time))).
									FontSize(11.5).FontFeatures("tnum").TextColor(p.muted)
							} else if src.Schedule.Manual {
								ui.Text(c, tr("Manual schedule")).FontSize(11.5).TextColor(p.muted)
							}

							ui.Spacer(c)
							localDir, hasLocal := a.resolveLocalDiffDir(src.Source, st)
							if src.LastSnapshot != nil && hasLocal {
								compactButton(c, tr("Diff Local")).OnClick(func() {
									a.openDiffForSource(src.Source, "", diffTargetLocalDir)
								})
							}
							if a.desktopRunning && hasLocal {
								compactButton(c, tr("Reveal in File Manager")).OnClick(func() {
									_ = mygo.Shell.OpenPath(localDir)
								})
							}
							compactButton(c, tr("Remove Source")).TextColor(c.Theme().Danger).OnClick(func() {
								a.removeSourceAction(src.Source)
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
		statusPill(c, tr("Uploading…"), t.Accent, p.accentSoft)
	case "PENDING":
		statusPill(c, tr("Pending"), t.Warning, p.warningSoft)
	case "PAUSED":
		statusPill(c, tr("Paused"), t.Warning, p.warningSoft)
	case "FAILED":
		statusPill(c, tr("Failed"), t.Danger, p.dangerSoft)
	default:
		statusPill(c, tr("Idle"), t.Success, p.successSoft)
	}
}

func (a *App) renderAddSourceModal(c *ui.Context) {
	if !a.addSourceModalOpen {
		return
	}
	ui.Modal(c, &a.addSourceModalOpen, func() {
		ui.Column(c).Width(460).Gap(16).Children(func() {
			ui.Text(c, tr("Add Backup Source")).FontSize(15.5).FontWeight(600)
			ui.Form(c, func() {
				ui.Field(c, tr("Folder Path"), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("new-src-path"), &a.newSourcePath).
							Placeholder("/Users/you/Documents").
							AutoFocus().
							Grow(1)
						if a.desktopRunning {
							if ui.Button(c, tr("Browse…")).Clicked() {
								go a.pickDirectoryInto(&a.newSourcePath)
							}
						}
					})
				})
				ui.Field(c, "", func() {
					ui.Checkbox(c, &a.newSourceSnapNow, tr("Start first backup immediately"))
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
				if ui.Button(c, tr("Cancel")).Clicked() {
					a.addSourceModalOpen = false
				}
				ui.PrimaryButton(c, tr("Add & Backup")).Disabled(strings.TrimSpace(a.newSourcePath) == "").OnClick(func() {
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
		a.toast(tr("Source added"), false)
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
		a.toast(tr("Backup triggered"), false)

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
		if !a.confirmDestructive(tr("Remove Source")+": "+src.Path+"?", tr("Remove")) {
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
		a.toast(tr("Source removed"), false)
	}()
}
