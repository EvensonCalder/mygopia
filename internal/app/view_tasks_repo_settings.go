package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
	"mygopia/locales"
)

type throttleFormDraft struct {
	uploadKBps   float64
	downloadKBps float64
	readsPerSec  float64
	writesPerSec float64
	listsPerSec  float64
}

type maintFormDraft struct {
	owner         string
	enableQuick   bool
	quickHours    float64
	enableFull    bool
	fullHours     float64
	runningAction bool
}

type notifyProfileDraft struct {
	name         string
	method       string // webhook, email, pushover
	minSeverity  string
	webhookURL   string
	httpMethod   string
	smtpServer   string
	smtpPort     string
	smtpUser     string
	smtpPassword string
	mailFrom     string
	mailTo       string
	pushToken    string
	pushUserKey  string
}

// ============================================================================
// Tasks & Server Logs View
// ============================================================================

func taskKindLabel(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "snapshot":
		return locales.TaskKindSnapshot()
	case "restore":
		return locales.TaskKindRestore()
	case "maintenance":
		return locales.TaskKindMaintenance()
	case "estimate":
		return locales.TaskKindEstimate()
	case "sync":
		return locales.TaskKindSync()
	default:
		return kind
	}
}

func (a *App) renderTasksView(c *ui.Context, _ config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)

	ui.Column(c).Fill().Padding(16, 20).Gap(12).Children(func() {
		ui.Tabs(c, &a.tasksTab, locales.BackgroundTasks(), locales.ServerOutput())

		if a.tasksTab == 1 {
			card(c, func() {
				ui.Scroll(c.Key("srv-log-scroll")).Grow(1).Children(func() {
					if len(st.ServerLogs) == 0 {
						ui.Text(c, "—").TextColor(p.muted)
						return
					}
					ui.Text(c, strings.Join(st.ServerLogs, "\n")).
						Selectable().
						Font("monospace").
						FontSize(11.5)
				})
			}).Grow(1)
			return
		}

		if len(st.Tasks) == 0 {
			card(c, func() {
				emptyState(c, iconTasks, locales.NoTasksRecordedYet(), "", "", nil)
			}).Grow(1)
			return
		}

		cols := []ui.TableColumn{
			{ID: "kind", Title: locales.Kind(), Width: 105},
			{ID: "desc", Title: locales.Description(), Width: 255, MinWidth: 150},
			{ID: "status", Title: locales.Status(), Width: 98},
			{ID: "started", Title: locales.Started(), Width: 172},
			{ID: "dur", Title: locales.Duration(), Width: 82, Align: ui.End},
			{ID: "prog", Title: locales.Progress(), Width: 220},
		}

		a.tasksTable.Selected = &a.selectedTaskRow

		card(c, func() {
			tbl := ui.Table(c, &a.tasksTable, cols, len(st.Tasks), func(row, col int) {
				tk := st.Tasks[row]
				selected := row == a.selectedTaskRow
				primary, secondary, _ := tableRowColors(c, selected)
				switch col {
				case 0:
					ui.Text(c, taskKindLabel(tk.Kind)).FontWeight(500).TextColor(primary).SingleLine()
				case 1:
					ui.Text(c, tk.Description).TextColor(primary).SingleLine()
				case 2:
					switch tk.Status {
					case "RUNNING":
						tableStatusPill(c, selected, locales.Running(), c.Theme().Accent, p.accentSoft)
					case "CANCELING":
						tableStatusPill(c, selected, locales.Canceling(), c.Theme().Warning, p.warningSoft)
					case "SUCCESS":
						tableStatusPill(c, selected, locales.Succeeded(), c.Theme().Success, p.successSoft)
					case "FAILED":
						tableStatusPill(c, selected, locales.Failed(), c.Theme().Danger, p.dangerSoft)
					case "CANCELED":
						tableStatusPill(c, selected, locales.Canceled(), p.muted, p.subtleBg)
					default:
						tableStatusPill(c, selected, tk.Status, p.muted, p.subtleBg)
					}
				case 3:
					ui.Text(c, formatTimestamp(tk.StartTime.Time)).FontFeatures("tnum").TextColor(secondary).SingleLine()
				case 4:
					if tk.StartTime.IsZero() {
						ui.Text(c, "—").TextColor(secondary).SingleLine()
					} else {
						end := time.Now()
						if tk.EndTime != nil && !tk.EndTime.IsZero() {
							end = tk.EndTime.Time
						}
						ui.Text(c, formatDuration(end.Sub(tk.StartTime.Time))).FontFeatures("tnum").TextColor(primary).SingleLine()
					}
				case 5:
					msg := tk.ProgressInfo
					if tk.ErrorMessage != "" {
						msg = tk.ErrorMessage
					}
					ui.Text(c, msg).TextColor(secondary).SingleLine()
				}
			}).Grow(1)

			if tbl.Changed() && a.selectedTaskRow >= 0 && a.selectedTaskRow < len(st.Tasks) {
				tid := st.Tasks[a.selectedTaskRow].ID
				a.selectedTaskID = tid
				a.loadTaskLogsAsync(tid)
			}
		}).Grow(1).Padding(0)

		// Selected task log panel
		if a.selectedTaskRow >= 0 && a.selectedTaskRow < len(st.Tasks) {
			selTask := st.Tasks[a.selectedTaskRow]
			card(c, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, locales.TaskLogs()+" — "+selTask.Description).FontSize(13).FontWeight(600).Grow(1).SingleLine()
					if selTask.IsRunning() {
						compactButton(c, locales.CancelTask()).TextColor(c.Theme().Danger).OnClick(func() {
							a.cancelTaskAction(selTask.ID)
						})
					}
				})
				ui.Scroll(c.Key("task-log-" + selTask.ID)).Height(160).Children(func() {
					if len(a.selectedTaskLogs) == 0 {
						if selTask.ErrorMessage != "" {
							ui.Text(c, selTask.ErrorMessage).TextColor(c.Theme().Danger).Selectable()
						} else {
							ui.Text(c, "—").TextColor(p.muted)
						}
						return
					}
					var lines []string
					for _, l := range a.selectedTaskLogs {
						lines = append(lines, l.Text)
					}
					ui.Text(c, strings.Join(lines, "\n")).Selectable().Font("monospace").FontSize(11.5)
				})
			})
		}
	})
}

func (a *App) loadTaskLogsAsync(taskID string) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" || taskID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			return
		}
		logs, err := cli.GetTaskLogs(ctx, taskID)
		if err == nil {
			a.uiUpdate(func() {
				if a.selectedTaskID == taskID {
					a.selectedTaskLogs = logs
				}
			})
		}
	}()
}

func (a *App) cancelTaskAction(taskID string) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.CancelTask(ctx, taskID); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
	}()
}

// ============================================================================
// Repository, Throttling, Maintenance & Notifications View
// ============================================================================

func (a *App) renderRepoView(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)

	if st.Status != nil && a.repoDescLoadedID != prof.ID {
		a.repoDescLoadedID = prof.ID
		a.repoDescDraft = st.Status.Description
	}
	if st.Throttle != nil && a.throttleLoadedID != prof.ID {
		a.throttleLoadedID = prof.ID
		a.throttleDraft = throttleFormDraft{
			uploadKBps:   st.Throttle.UploadBytesPerSecond / 1024.0,
			downloadKBps: st.Throttle.DownloadBytesPerSecond / 1024.0,
			readsPerSec:  st.Throttle.ReadsPerSecond,
			writesPerSec: st.Throttle.WritesPerSecond,
			listsPerSec:  st.Throttle.ListsPerSecond,
		}
	}
	if st.Maintenance != nil && a.maintLoadedID != prof.ID {
		a.maintLoadedID = prof.ID
		a.maintDraft = maintFormDraft{
			owner:       st.Maintenance.Owner,
			enableQuick: st.Maintenance.QuickCycle.Enabled,
			quickHours:  float64(time.Duration(st.Maintenance.QuickCycle.Interval).Hours()),
			enableFull:  st.Maintenance.FullCycle.Enabled,
			fullHours:   float64(time.Duration(st.Maintenance.FullCycle.Interval).Hours()),
		}
		if a.maintDraft.quickHours <= 0 {
			a.maintDraft.quickHours = 1
		}
		if a.maintDraft.fullHours <= 0 {
			a.maintDraft.fullHours = 24
		}
	}
	if a.notifyForm.method == "" {
		a.notifyForm.method = "webhook"
		a.notifyForm.httpMethod = "POST"
		a.notifyForm.smtpPort = "587"
		a.notifyForm.minSeverity = "0 (Info)"
	}

	ui.Column(c).Fill().Padding(16, 20).Gap(12).Children(func() {
		ui.Tabs(c, &a.repoTab,
			locales.ConnectionFormat(),
			locales.BandwidthRateLimits(),
			locales.Maintenance(),
			locales.Notifications(),
		)

		ui.Scroll(c.Key("repo-scroll")).Grow(1).Padding(0, 4, 8, 0).Children(func() {
			ui.Column(c).Gap(14).MaxWidth(760).Children(func() {
				switch a.repoTab {
				case 0:
					// Deduplication & Storage Stats
					if st.ContentStats != nil {
						savedBytes := max(int64(0), st.ContentStats.OriginalBytes-st.ContentStats.PackedBytes)
						savedSub := ""
						if st.ContentStats.OriginalBytes > 0 && savedBytes > 0 {
							pct := float64(savedBytes) * 100.0 / float64(st.ContentStats.OriginalBytes)
							savedSub = fmt.Sprintf("-%.1f%%", pct)
						}
						ui.Row(c).Wrap().Gap(12).Children(func() {
							metricCard(c, locales.OriginalLogicalData(), formatBytes(st.ContentStats.OriginalBytes), "")
							metricCard(c, locales.DeduplicatedPackedSize(), formatBytes(st.ContentStats.PackedBytes), "")
							metricCard(c, locales.SpaceSaved(), formatBytes(savedBytes), savedSub)
							metricCard(c, locales.ContentChunks(), fmt.Sprintf("%d", st.ContentStats.Count), "")
						})
					}

					card(c, func() {
						ui.Text(c, locales.ConnectionFormat()).FontSize(14).FontWeight(600)
						if st.Status != nil {
							kvRow(c, locales.Storage(), strings.ToUpper(st.Status.Storage))
							kvRow(c, locales.ConfigFile(), prof.ConfigFile)
							kvRow(c, locales.FormatVersion(), intToStr(st.Status.FormatVersion))
							kvRow(c, locales.EncryptionAlgorithm(), st.Status.Encryption)
							kvRow(c, locales.HashAlgorithm(), st.Status.Hash)
							kvRow(c, locales.ContentSplitter(), st.Status.Splitter)
							if st.Status.ECC != "" {
								kvRow(c, locales.ReedSolomonECC(), fmt.Sprintf("%s (%d%%)", st.Status.ECC, st.Status.ECCOverheadPercent))
							}
							kvRow(c, locales.ClientIdentity(), st.Status.Username+"@"+st.Status.Hostname)
						}

						ui.Box(c).Height(1).Background(p.border)
						ui.Form(c, func() {
							ui.Field(c, locales.ClientDescription(), func() {
								ui.Row(c).Gap(8).Children(func() {
									ui.TextInput(c.Key("repo-edit-desc"), &a.repoDescDraft).Grow(1)
									ui.Button(c, locales.Save()).OnClick(func() {
										a.saveRepoDescriptionAction(a.repoDescDraft)
									})
								})
							})
						})

						ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
							if a.desktopRunning && prof.ConfigFile != "" {
								ui.Button(c, locales.RevealInFileManager()).OnClick(func() {
									mygo.Shell.ShowItemInFolder(prof.ConfigFile)
								})
							}
							ui.Button(c, locales.SyncRefresh()).OnClick(func() {
								a.triggerManualRefresh()
							})
							ui.Button(c, locales.DisconnectRepository()).TextColor(c.Theme().Danger).OnClick(func() {
								a.disconnectRepoAction()
							})
						})
					})

				case 1:
					card(c, func() {
						ui.Text(c, locales.BandwidthRateLimits()).FontSize(14).FontWeight(600)
						td := &a.throttleDraft
						ui.Form(c, func() {
							ui.Field(c, locales.MaxUploadSpeedKBS0Unlimited(), func() {
								ui.NumberInput(c.Key("th-up"), &td.uploadKBps, 0, 10485760, 256).Width(180)
							})
							ui.Field(c, locales.MaxDownloadSpeedKBS0Unlimited(), func() {
								ui.NumberInput(c.Key("th-down"), &td.downloadKBps, 0, 10485760, 256).Width(180)
							})
							ui.Field(c, locales.MaxReadsSec(), func() {
								ui.NumberInput(c.Key("th-r"), &td.readsPerSec, 0, 100000, 10).Width(180)
							})
							ui.Field(c, locales.MaxWritesSec(), func() {
								ui.NumberInput(c.Key("th-w"), &td.writesPerSec, 0, 100000, 10).Width(180)
							})
							ui.Field(c, locales.MaxListsSec(), func() {
								ui.NumberInput(c.Key("th-l"), &td.listsPerSec, 0, 100000, 5).Width(180)
							})
						})
						ui.Row(c).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
							ui.PrimaryButton(c, locales.SaveBandwidthLimits()).OnClick(func() {
								a.saveThrottleAction()
							})
						})
					})

				case 2:
					card(c, func() {
						ui.Text(c, locales.Maintenance()).FontSize(14).FontWeight(600)
						md := &a.maintDraft
						if st.Maintenance != nil {
							kvRow(c, locales.NextQuickRun(), formatTimestamp(st.Maintenance.Schedule.NextQuickMaintenanceTime.Time))
							kvRow(c, locales.NextFullRun(), formatTimestamp(st.Maintenance.Schedule.NextFullMaintenanceTime.Time))
							ui.Box(c).Height(1).Background(p.border)
						}
						ui.Form(c, func() {
							ui.Field(c, locales.MaintenanceOwner(), func() {
								ui.TextInput(c.Key("mnt-owner"), &md.owner).Placeholder("user@host")
							})
							ui.Field(c, "", func() {
								ui.Checkbox(c, &md.enableQuick, locales.EnableQuickMaintenance())
							})
							ui.Field(c, locales.QuickIntervalHours(), func() {
								ui.NumberInput(c.Key("mnt-qh"), &md.quickHours, 0.25, 720, 1).Width(150).Disabled(!md.enableQuick)
							})
							ui.Field(c, "", func() {
								ui.Checkbox(c, &md.enableFull, locales.EnableFullMaintenance())
							})
							ui.Field(c, locales.FullIntervalHours(), func() {
								ui.NumberInput(c.Key("mnt-fh"), &md.fullHours, 1, 2160, 6).Width(150).Disabled(!md.enableFull)
							})
						})
						ui.Row(c).Gap(8).Justify(ui.End).Wrap().Padding(4, 0, 0, 0).Children(func() {
							ui.Button(c, locales.RunQuickMaintenance()).Disabled(md.runningAction).OnClick(func() {
								a.runMaintenanceAction(false)
							})
							ui.Button(c, locales.RunFullMaintenance()).Disabled(md.runningAction).OnClick(func() {
								a.runMaintenanceAction(true)
							})
							ui.PrimaryButton(c, locales.SaveMaintenanceConfig()).OnClick(func() {
								a.saveMaintenanceAction()
							})
						})
					})

				case 3:
					if len(st.NotificationProfiles) > 0 {
						card(c, func() {
							ui.Text(c, locales.NotificationProfiles()).FontSize(14).FontWeight(600)
							for _, np := range st.NotificationProfiles {
								profItem := np
								ui.Row(c.Key("np-" + profItem.ProfileName)).AlignItems(ui.Center).Gap(10).Padding(6, 0).Children(func() {
									ui.Text(c, profItem.ProfileName).FontWeight(600).Grow(1)
									statusPill(c, profItem.MethodConfig.Type, p.text, p.subtleBg)
									compactButton(c, locales.Delete()).TextColor(c.Theme().Danger).OnClick(func() {
										a.deleteNotificationProfileAction(profItem.ProfileName)
									})
								})
							}
						})
					}

					card(c, func() {
						ui.Text(c, locales.NewNotificationProfile()).FontSize(14).FontWeight(600)
						nf := &a.notifyForm
						ui.Form(c, func() {
							ui.Field(c, locales.ProfileName(), func() {
								ui.TextInput(c.Key("nf-name"), &nf.name).Placeholder("ops-alerts")
							})
							ui.Field(c, locales.NotificationMethod(), func() {
								ui.Select(c.Key("nf-method"), &nf.method, []string{"webhook", "email", "pushover"})
							})
							ui.Field(c, locales.MinimumSeverity(), func() {
								ui.Select(c.Key("nf-sev"), &nf.minSeverity, []string{"-100 (Verbose)", "-10 (Report)", "0 (Info)", "10 (Warning)", "20 (Error)"})
							})

							switch nf.method {
							case "webhook":
								ui.Field(c, locales.WebhookURL(), func() {
									ui.TextInput(c.Key("nf-wh-url"), &nf.webhookURL).Placeholder("https://hooks.example.com/kopia")
								})
								ui.Field(c, locales.HttpMethod(), func() {
									ui.Select(c.Key("nf-wh-m"), &nf.httpMethod, []string{"POST", "PUT"})
								})
							case "email":
								ui.Field(c, locales.SmtpServer(), func() {
									ui.Row(c).Gap(8).Children(func() {
										ui.TextInput(c.Key("nf-smtp-s"), &nf.smtpServer).Placeholder("smtp.example.com").Grow(1)
										ui.TextInput(c.Key("nf-smtp-p"), &nf.smtpPort).Placeholder("587").Width(80)
									})
								})
								ui.Field(c, locales.SmtpUsername(), func() {
									ui.TextInput(c.Key("nf-smtp-u"), &nf.smtpUser)
								})
								ui.Field(c, locales.SmtpPassword(), func() {
									ui.TextInput(c.Key("nf-smtp-pw"), &nf.smtpPassword).Password()
								})
								ui.Field(c, locales.MailFrom(), func() {
									ui.TextInput(c.Key("nf-from"), &nf.mailFrom)
								})
								ui.Field(c, locales.MailTo(), func() {
									ui.TextInput(c.Key("nf-to"), &nf.mailTo)
								})
							case "pushover":
								ui.Field(c, locales.PushoverAppToken(), func() {
									ui.TextInput(c.Key("nf-po-tok"), &nf.pushToken).Password()
								})
								ui.Field(c, locales.PushoverUserKey(), func() {
									ui.TextInput(c.Key("nf-po-uk"), &nf.pushUserKey)
								})
							}
						})
						ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
							ui.Button(c, locales.SendTest()).OnClick(func() {
								a.saveOrTestNotificationAction(true)
							})
							ui.PrimaryButton(c, locales.SaveProfile()).Disabled(strings.TrimSpace(nf.name) == "").OnClick(func() {
								a.saveOrTestNotificationAction(false)
							})
						})
					})
				}
			})
		})
	})
}

func kvRow(c *ui.Context, k, v string) {
	p := paletteOf(c)
	ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, k).Width(170).Shrink(0).TextColor(p.muted).FontSize(12.5)
		ui.Text(c, v).Selectable().FontSize(12.5).Grow(1)
	})
}

func (a *App) saveRepoDescriptionAction(desc string) {
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
		if _, err := cli.SetDescription(ctx, strings.TrimSpace(desc)); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.toast(locales.SettingsSaved(), false)
	}()
}

func (a *App) disconnectRepoAction() {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	go func() {
		if !a.confirmDestructive(locales.DisconnectRepository()+"?", locales.DisconnectRepository()) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.DisconnectRepo(ctx); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.indexer.RemoveRepo(repoID)
		a.manager.RefreshRepo(ctx, repoID, false)
	}()
}

func (a *App) saveThrottleAction() {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	td := a.throttleDraft
	limits := kopia.ThrottleLimits{
		UploadBytesPerSecond:   td.uploadKBps * 1024,
		DownloadBytesPerSecond: td.downloadKBps * 1024,
		ReadsPerSecond:         td.readsPerSec,
		WritesPerSecond:        td.writesPerSec,
		ListsPerSecond:         td.listsPerSec,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.SetThrottle(ctx, limits); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, true)
		a.toast(locales.ThrottleLimitsUpdated(), false)
	}()
}

func (a *App) saveMaintenanceAction() {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	helper, ok := a.manager.CLIFor(repoID)
	if !ok {
		return
	}
	md := a.maintDraft
	go func() {
		err := helper.SetMaintenance(
			context.Background(),
			strings.TrimSpace(md.owner),
			md.enableQuick,
			time.Duration(md.quickHours*float64(time.Hour)),
			md.enableFull,
			time.Duration(md.fullHours*float64(time.Hour)),
		)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshMaintenanceAndStats(context.Background(), repoID)
		a.toast(locales.SettingsSaved(), false)
	}()
}

func (a *App) runMaintenanceAction(full bool) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	helper, ok := a.manager.CLIFor(repoID)
	if !ok {
		return
	}
	a.maintDraft.runningAction = true
	go func() {
		_, err := helper.RunMaintenance(context.Background(), full)
		a.uiUpdate(func() {
			a.maintDraft.runningAction = false
		})
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshMaintenanceAndStats(context.Background(), repoID)
		a.toast(locales.MaintenanceCompleted(), false)
	}()
}

func (a *App) saveOrTestNotificationAction(testOnly bool) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	nf := a.notifyForm
	sev := 0
	if fields := strings.Fields(nf.minSeverity); len(fields) > 0 {
		sev, _ = strconv.Atoi(fields[0])
	}
	cfgMap := map[string]any{}
	switch nf.method {
	case "webhook":
		cfgMap["endpoint"] = strings.TrimSpace(nf.webhookURL)
		cfgMap["method"] = nf.httpMethod
	case "email":
		port, _ := strconv.Atoi(strings.TrimSpace(nf.smtpPort))
		if port <= 0 {
			port = 587
		}
		cfgMap["smtpServer"] = strings.TrimSpace(nf.smtpServer)
		cfgMap["smtpPort"] = port
		cfgMap["smtpUsername"] = strings.TrimSpace(nf.smtpUser)
		cfgMap["smtpPassword"] = nf.smtpPassword
		cfgMap["from"] = strings.TrimSpace(nf.mailFrom)
		cfgMap["to"] = strings.TrimSpace(nf.mailTo)
	case "pushover":
		cfgMap["appToken"] = strings.TrimSpace(nf.pushToken)
		cfgMap["userKey"] = strings.TrimSpace(nf.pushUserKey)
	}
	profName := strings.TrimSpace(nf.name)
	if profName == "" {
		profName = "test-profile"
	}
	payload := kopia.NotificationProfile{
		ProfileName: profName,
		MinSeverity: sev,
		MethodConfig: kopia.NotificationMethodConfig{
			Type:   nf.method,
			Config: cfgMap,
		},
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if testOnly {
			if err := cli.TestNotificationProfile(ctx, payload); err != nil {
				a.toast(err.Error(), true)
				return
			}
			a.toast(locales.TestNotificationSent(), false)
			return
		}
		if err := cli.SaveNotificationProfile(ctx, payload); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, true)
		a.uiUpdate(func() {
			a.notifyForm.smtpPassword = ""
			a.notifyForm.pushToken = ""
		})
		a.toast(locales.NotificationProfileSaved(), false)
	}()
}

func (a *App) deleteNotificationProfileAction(name string) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	go func() {
		if !a.confirmDestructive(locales.Delete()+" "+name+"?", locales.Delete()) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.DeleteNotificationProfile(ctx, name); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, true)
		a.toast(locales.NotificationProfileDeleted(), false)
	}()
}

// ============================================================================
// Application Settings View (Theme: System/Light/Dark, Language: System/中文/EN)
// ============================================================================

type languageChoice struct {
	Code  string
	Label string
}

var languageChoices = []languageChoice{
	{Code: config.LangSystem, Label: ""},
	{Code: config.LangEN, Label: "English"},
	{Code: config.LangZhHans, Label: "简体中文"},
	{Code: config.LangZhHant, Label: "繁體中文"},
	{Code: config.LangJA, Label: "日本語"},
	{Code: config.LangFR, Label: "Français"},
	{Code: config.LangDE, Label: "Deutsch"},
	{Code: config.LangES, Label: "Español"},
}

func (a *App) syncSettingsDraft() {
	switch a.settings.Theme {
	case config.ThemeLight:
		a.settingsThemeSeg = 1
	case config.ThemeDark:
		a.settingsThemeSeg = 2
	default:
		a.settingsThemeSeg = 0
	}

	a.settingsLangSeg = 0
	for i, ch := range languageChoices {
		if ch.Code == a.settings.Language {
			a.settingsLangSeg = i
			break
		}
	}

	switch a.settings.UIScale {
	case config.ScaleSmall:
		a.settingsScaleSeg = 0
	case config.ScaleLarge:
		a.settingsScaleSeg = 2
	default:
		a.settingsScaleSeg = 1
	}

	a.settingsKopiaBin = a.settings.KopiaBinary
	a.settingsRcloneBin = a.settings.RcloneBinary
	a.settingsCfgDir = a.settings.ConfigDir
	a.settingsLoaded = true
}

func (a *App) applyAppearanceImmediately() {
	modes := []string{config.ThemeSystem, config.ThemeLight, config.ThemeDark}
	if a.settingsThemeSeg >= 0 && a.settingsThemeSeg < len(modes) {
		a.settings.Theme = modes[a.settingsThemeSeg]
	}
	if a.desktopRunning {
		applyNativeThemeSource(a.settings.Theme)
	}
	_ = config.Save(a.userDataDir, a.settings)
}

func (a *App) applyAccentImmediately(accentKey string) {
	a.settings.AccentColor = accentKey
	a.settings.Normalize()
	_ = config.Save(a.userDataDir, a.settings)
}

func (a *App) applyUIScaleImmediately() {
	scales := []string{config.ScaleSmall, config.ScaleMedium, config.ScaleLarge}
	if a.settingsScaleSeg >= 0 && a.settingsScaleSeg < len(scales) {
		a.settings.UIScale = scales[a.settingsScaleSeg]
	}
	_ = config.Save(a.userDataDir, a.settings)
}

// stepUIScale steps interface scale by delta (-1 smaller, +1 larger, 0 reset to Medium).
func (a *App) stepUIScale(delta int) {
	if !a.settingsLoaded {
		a.syncSettingsDraft()
	}
	if delta == 0 {
		a.settingsScaleSeg = 1
	} else {
		a.settingsScaleSeg = min(max(a.settingsScaleSeg+delta, 0), 2)
	}
	a.applyUIScaleImmediately()
}

func (a *App) applyLanguageImmediately() {
	if a.settingsLangSeg >= 0 && a.settingsLangSeg < len(languageChoices) {
		a.settings.Language = languageChoices[a.settingsLangSeg].Code
	}
	setCurrentLanguage(ResolveLanguage(a.settings.Language, a.preferredLangs))
	if a.desktopRunning {
		applyNativeLocale(a.settings.Language)
		a.setupMenus()
		a.refreshTrayMenu()
	}
	_ = config.Save(a.userDataDir, a.settings)
}

func (a *App) renderSettingsView(c *ui.Context) {
	if !a.settingsLoaded {
		a.syncSettingsDraft()
	}
	p := paletteOf(c)

	ui.Scroll(c.Key("settings-scroll")).Fill().Padding(20, 24).Children(func() {
		ui.Column(c).MaxWidth(680).Gap(16).Children(func() {
			// Appearance, Accent Color, Language & UI Scale (applies immediately!)
			card(c, func() {
				ui.Text(c, locales.AppearanceLanguage()).FontSize(14).FontWeight(600)
				ui.Form(c, func() {
					ui.Field(c, locales.Appearance(), func() {
						ui.Row(c).Children(func() {
							if ui.Segmented(c.Key("set-theme"), &a.settingsThemeSeg, locales.FollowSystem(), locales.Light(), locales.Dark()).
								Label(locales.Appearance()).Changed() {
								a.applyAppearanceImmediately()
							}
						})
					})
					ui.Field(c, locales.AccentColor(), func() {
						activeOpt := resolveAccentOption(a.settings.AccentColor)
						ui.Row(c).AlignItems(ui.Center).Gap(10).Wrap().Children(func() {
							for _, opt := range accentOptions {
								optCopy := opt
								selected := optCopy.Key == activeOpt.Key
								swatchCol := optCopy.LightAccent
								if c.Theme().Dark {
									swatchCol = optCopy.DarkAccent
								}
								ringCol := ui.RGBA(swatchCol.R, swatchCol.G, swatchCol.B, 0)
								if selected {
									ringCol = p.text
								}
								ui.Row(c.Key("accent-" + optCopy.Key)).
									Width(24).
									Height(24).
									Radius(999).
									Background(swatchCol).
									Border(2, ringCol).
									AlignItems(ui.Center).
									Justify(ui.Center).
									Cursor(ui.CursorPointer).
									Role(ui.RoleButton).
									Label(optCopy.Label()).
									Tooltip(optCopy.Label()).
									OnClick(func() {
										a.applyAccentImmediately(optCopy.Key)
									}).
									Children(func() {
										if selected {
											ui.Icon(c, iconCheck).FontSize(13).TextColor(ui.Hex("#ffffff"))
										}
									})
							}
							ui.Text(c, activeOpt.Label()).FontSize(12).TextColor(p.muted).Margin(0, 0, 0, 4)
						})
					})
					ui.Field(c, locales.DisplayLanguage(), func() {
						langLabels := make([]string, len(languageChoices))
						for i, ch := range languageChoices {
							if ch.Label == "" {
								langLabels[i] = locales.FollowSystem()
							} else {
								langLabels[i] = ch.Label
							}
						}
						idx := min(max(a.settingsLangSeg, 0), len(langLabels)-1)
						curLangLabel := langLabels[idx]
						ui.Row(c).Children(func() {
							if ui.Select(c.Key("set-lang"), &curLangLabel, langLabels).Label(locales.DisplayLanguage()).Width(210).Changed() {
								for i, lbl := range langLabels {
									if lbl == curLangLabel {
										a.settingsLangSeg = i
										break
									}
								}
								a.applyLanguageImmediately()
							}
						})
					})
					ui.Field(c, locales.InterfaceFontSize(), func() {
						ui.Row(c).Children(func() {
							if ui.Segmented(c.Key("set-scale"), &a.settingsScaleSeg, locales.Small(), locales.Medium(), locales.Large()).
								Label(locales.InterfaceFontSize()).Changed() {
								a.applyUIScaleImmediately()
							}
						})
					})
				})
			})

			// Desktop Behavior
			card(c, func() {
				ui.Text(c, locales.DesktopBehavior()).FontSize(14).FontWeight(600)
				if ui.Checkbox(c, &a.settings.OpenAtLogin, locales.LaunchAtSystemLogin()).Changed() {
					if a.desktopRunning {
						if err := mygo.App.SetOpenAtLogin(a.settings.OpenAtLogin); err != nil {
							a.settings.OpenAtLogin = mygo.App.OpenAtLogin()
							c.AddToast(ui.Toast{
								Title:   err.Error(),
								Type:    "error",
								Timeout: 5 * time.Second,
							})
						}
					}
					_ = config.Save(a.userDataDir, a.settings)
				}
				trayLabel := locales.ShowIconInSystemTray()
				if runtime.GOOS == "darwin" {
					trayLabel = locales.ShowIconInMenuBar()
				}
				if ui.Checkbox(c, &a.settings.ShowInMenuBar, trayLabel).Changed() {
					a.syncTrayVisibility()
					_ = config.Save(a.userDataDir, a.settings)
				}
				if ui.Checkbox(c, &a.settings.CloseToTray, locales.KeepRunningInBackgroundWhenWindowCloses()).Changed() {
					_ = config.Save(a.userDataDir, a.settings)
				}
				if ui.Checkbox(c, &a.settings.Notify, locales.ShowDesktopNotificationsForKopiaEvents()).Changed() {
					a.notifyAtom.Store(a.settings.Notify)
					_ = config.Save(a.userDataDir, a.settings)
				}
				if ui.Checkbox(c, &a.settings.AutoIndex, locales.AutomaticallyUpdateSearchIndexAfterBackups()).Changed() {
					_ = config.Save(a.userDataDir, a.settings)
				}
			})

			// Kopia Engine Paths
			card(c, func() {
				ui.Text(c, locales.KopiaEngine()).FontSize(14).FontWeight(600)
				det := a.detectedKopia
				if det == "" {
					det = locales.NotDetected()
				}
				detRclone := a.detectedRclone
				if detRclone == "" {
					detRclone = locales.NotDetected()
				}
				ui.Form(c, func() {
					ui.Field(c, locales.KopiaBinaryPath(), func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c.Key("set-kopia-bin"), &a.settingsKopiaBin).Placeholder(config.ExampleKopiaBinaryPath()).Grow(1)
							if a.desktopRunning {
								if ui.Button(c, locales.BrowsePicker()).Clicked() {
									go a.pickFileInto(&a.settingsKopiaBin)
								}
							}
						})
					}).Description(locales.AutoDetectBinaryHint(det))

					ui.Field(c, locales.RcloneBinaryPath(), func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c.Key("set-rclone-bin"), &a.settingsRcloneBin).Placeholder(config.ExampleRcloneBinaryPath()).Grow(1)
							if a.desktopRunning {
								if ui.Button(c, locales.BrowsePicker()).Clicked() {
									go a.pickFileInto(&a.settingsRcloneBin)
								}
							}
						})
					}).Description(locales.AutoDetectBinaryHint(detRclone))

					ui.Field(c, locales.KopiaConfigDirectory(), func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c.Key("set-cfg-dir"), &a.settingsCfgDir).Placeholder(config.DefaultKopiaConfigDir()).Grow(1)
							if a.desktopRunning {
								if ui.Button(c, locales.BrowsePicker()).Clicked() {
									go a.pickDirectoryInto(&a.settingsCfgDir)
								}
							}
						})
					})
				})
				ui.Row(c).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
					ui.PrimaryButton(c, locales.SaveSettings()).OnClick(func() {
						rawKopia := config.ExpandPath(a.settingsKopiaBin)
						rawRclone := config.ExpandPath(a.settingsRcloneBin)
						rawCfgDir := config.ExpandPath(a.settingsCfgDir)

						resolvedKopia, err := config.ResolveKopiaBinary(rawKopia)
						if rawKopia != "" && err != nil {
							c.AddToast(ui.Toast{Title: err.Error(), Type: "error", Timeout: 5 * time.Second})
							return
						}
						resolvedRclone, errRclone := config.ResolveRcloneBinary(rawRclone)
						if rawRclone != "" && errRclone != nil {
							c.AddToast(ui.Toast{Title: errRclone.Error(), Type: "error", Timeout: 5 * time.Second})
							return
						}

						wasMissingKopia := a.detectedKopia == ""
						a.settings.KopiaBinary = rawKopia
						a.settings.RcloneBinary = rawRclone
						a.settings.ConfigDir = rawCfgDir
						a.settingsKopiaBin = rawKopia
						a.settingsRcloneBin = rawRclone
						a.settingsCfgDir = rawCfgDir
						a.detectedKopia = resolvedKopia
						a.detectedRclone = resolvedRclone

						if a.manager != nil {
							if resolvedKopia != "" {
								a.manager.SetKopiaBinary(resolvedKopia)
							}
							a.manager.SetRcloneBinary(resolvedRclone)
						}
						a.settings.Repositories = config.DiscoverProfiles(a.settings.Repositories, a.settings.EffectiveConfigDir())
						if a.manager != nil {
							a.manager.SyncProfiles(a.settings.Repositories)
						}
						_ = config.Save(a.userDataDir, a.settings)
						if wasMissingKopia && resolvedKopia != "" && a.settings.ActiveRepoID != "" {
							a.activateRepo(a.settings.ActiveRepoID)
						}
						c.Toast(locales.SettingsSaved())
					})
				})
			})

			// About & License
			card(c, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, locales.AboutMygopia()).FontSize(14).FontWeight(600)
					ui.Badge(c, "v"+AppVersion+" · MIT")
				})
				if a.userDataDir != "" {
					kvRow(c, locales.DataDirectory(), filepath.Clean(a.userDataDir))
				}
			})
		})
	})
}
