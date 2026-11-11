package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
	"mygopia/locales"
)

type policyFormDraft struct {
	// Retention (0 = inherit)
	keepLatest  float64
	keepHourly  float64
	keepDaily   float64
	keepWeekly  float64
	keepMonthly float64
	keepAnnual  float64
	ignoreIdent bool

	// Schedule
	manual          bool
	intervalMinutes float64
	timesOfDayText  string
	cronText        string
	runMissed       bool

	// Files & Ignores
	ignoreRulesText    string
	noParentIgnore     bool
	dotIgnoreText      string
	ignoreCacheDirs    bool
	oneFileSystem      bool
	maxFileSizeMB      float64

	// Compression
	compressorName string
	metaCompressor string
	minCompBytes   float64
	maxCompBytes   float64
	onlyCompText   string
	neverCompText  string

	// Upload & Errors
	maxParallelReads float64
	maxParallelSnaps float64
	parallelAboveMB  float64
	ignoreFileErr    bool
	ignoreDirErr     bool
	ignoreUnknown    bool

	// Actions & Logging
	beforeRootScript string
	afterRootScript  string
	hookMode         string
	hookTimeoutSec   float64
	logDirSnap       float64
	logDirIgnored    float64
	logEntrySnap     float64
	logEntryIgnored  float64
}

func (a *App) openPolicyForTarget(target kopia.SourceInfo) {
	a.policyTargetStr = target.String()
	a.policyLoadedTarget = ""
	a.activeNav = navPolicies
}

func parseTargetKey(key string, st kopia.RepoSnapshotState) kopia.SourceInfo {
	if key == "" || key == "(global)" {
		return kopia.SourceInfo{}
	}
	for _, s := range st.SourceList() {
		if s.Source.String() == key {
			return s.Source
		}
	}
	for _, p := range st.Policies {
		if p.Target.String() == key {
			return p.Target
		}
	}
	if atIdx := strings.IndexByte(key, '@'); atIdx > 0 {
		rest := key[atIdx+1:]
		if colonIdx := strings.IndexByte(rest, ':'); colonIdx > 0 {
			return kopia.SourceInfo{
				UserName: key[:atIdx],
				Host:     rest[:colonIdx],
				Path:     filepath.Clean(rest[colonIdx+1:]),
			}
		}
	}
	user := "local"
	host := "localhost"
	if st.Sources != nil {
		if st.Sources.LocalUsername != "" {
			user = st.Sources.LocalUsername
		}
		if st.Sources.LocalHost != "" {
			host = st.Sources.LocalHost
		}
	} else if st.Status != nil {
		if st.Status.Username != "" {
			user = st.Status.Username
		}
		if st.Status.Hostname != "" {
			host = st.Status.Hostname
		}
	}
	return kopia.SourceInfo{UserName: user, Host: host, Path: filepath.Clean(key)}
}

func (a *App) loadPolicyTargetAsync(target kopia.SourceInfo) {
	repoID := a.settings.ActiveRepoID
	targetStr := target.String()
	a.policyLoadedTarget = targetStr

	if a.manager == nil || repoID == "" {
		a.policyCleanDraft = a.policyDraft
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			return
		}
		resolved, err := cli.ResolvePolicy(ctx, target, nil, 5)
		if err != nil {
			return
		}
		draft := draftFromPolicy(resolved.Defined, resolved.Effective)
		a.uiUpdate(func() {
			if a.policyTargetStr == targetStr {
				a.policyResolved = resolved
				a.policyDraft = draft
				a.policyCleanDraft = draft
			}
		})
	}()
}

func draftFromPolicy(def, eff *kopia.Policy) policyFormDraft {
	if def == nil {
		def = &kopia.Policy{}
	}
	if eff == nil {
		eff = def
	}
	d := policyFormDraft{
		keepLatest:       optIntFloat(def.RetentionPolicy.KeepLatest),
		keepHourly:       optIntFloat(def.RetentionPolicy.KeepHourly),
		keepDaily:        optIntFloat(def.RetentionPolicy.KeepDaily),
		keepWeekly:       optIntFloat(def.RetentionPolicy.KeepWeekly),
		keepMonthly:      optIntFloat(def.RetentionPolicy.KeepMonthly),
		keepAnnual:       optIntFloat(def.RetentionPolicy.KeepAnnual),
		ignoreIdent:      optBoolVal(def.RetentionPolicy.IgnoreIdenticalSnapshots, optBoolVal(eff.RetentionPolicy.IgnoreIdenticalSnapshots, false)),
		manual:           def.SchedulingPolicy.Manual,
		intervalMinutes:  float64(def.SchedulingPolicy.IntervalSeconds) / 60.0,
		runMissed:        optBoolVal(def.SchedulingPolicy.RunMissed, optBoolVal(eff.SchedulingPolicy.RunMissed, true)),
		ignoreRulesText:  strings.Join(def.FilesPolicy.IgnoreRules, "\n"),
		noParentIgnore:   def.FilesPolicy.NoParentIgnoreRules,
		dotIgnoreText:    strings.Join(def.FilesPolicy.DotIgnoreFiles, "\n"),
		ignoreCacheDirs:  optBoolVal(def.FilesPolicy.IgnoreCacheDirectories, optBoolVal(eff.FilesPolicy.IgnoreCacheDirectories, true)),
		oneFileSystem:    optBoolVal(def.FilesPolicy.OneFileSystem, false),
		maxFileSizeMB:    float64(def.FilesPolicy.MaxFileSize) / (1024 * 1024),
		compressorName:   def.CompressionPolicy.CompressorName,
		metaCompressor:   def.MetadataCompressionPolicy.CompressorName,
		minCompBytes:     float64(def.CompressionPolicy.MinSize),
		maxCompBytes:     float64(def.CompressionPolicy.MaxSize),
		onlyCompText:     strings.Join(def.CompressionPolicy.OnlyCompress, "\n"),
		neverCompText:    strings.Join(def.CompressionPolicy.NeverCompress, "\n"),
		maxParallelReads: optIntFloat(def.UploadPolicy.MaxParallelFileReads),
		maxParallelSnaps: optIntFloat(def.UploadPolicy.MaxParallelSnapshots),
		ignoreFileErr:    optBoolVal(def.ErrorHandlingPolicy.IgnoreFileErrors, false),
		ignoreDirErr:     optBoolVal(def.ErrorHandlingPolicy.IgnoreDirectoryErrors, false),
		ignoreUnknown:    optBoolVal(def.ErrorHandlingPolicy.IgnoreUnknownTypes, true),
		hookMode:         "optional",
		hookTimeoutSec:   30,
		logDirSnap:       optIntFloat(def.LoggingPolicy.Directories.Snapshotted),
		logDirIgnored:    optIntFloat(def.LoggingPolicy.Directories.Ignored),
		logEntrySnap:     optIntFloat(def.LoggingPolicy.Entries.Snapshotted),
		logEntryIgnored:  optIntFloat(def.LoggingPolicy.Entries.Ignored),
	}
	if def.UploadPolicy.ParallelUploadAboveSize != nil {
		d.parallelAboveMB = float64(*def.UploadPolicy.ParallelUploadAboveSize) / (1024 * 1024)
	}
	var todLines []string
	for _, t := range def.SchedulingPolicy.TimesOfDay {
		todLines = append(todLines, t.String())
	}
	d.timesOfDayText = strings.Join(todLines, "\n")
	d.cronText = strings.Join(def.SchedulingPolicy.Cron, "\n")

	if def.Actions.BeforeSnapshotRoot != nil {
		d.beforeRootScript = def.Actions.BeforeSnapshotRoot.Script
		if def.Actions.BeforeSnapshotRoot.Mode != "" {
			d.hookMode = def.Actions.BeforeSnapshotRoot.Mode
		}
		if def.Actions.BeforeSnapshotRoot.TimeoutSeconds > 0 {
			d.hookTimeoutSec = float64(def.Actions.BeforeSnapshotRoot.TimeoutSeconds)
		}
	}
	if def.Actions.AfterSnapshotRoot != nil {
		d.afterRootScript = def.Actions.AfterSnapshotRoot.Script
		if def.Actions.BeforeSnapshotRoot == nil {
			if def.Actions.AfterSnapshotRoot.Mode != "" {
				d.hookMode = def.Actions.AfterSnapshotRoot.Mode
			}
			if def.Actions.AfterSnapshotRoot.TimeoutSeconds > 0 {
				d.hookTimeoutSec = float64(def.Actions.AfterSnapshotRoot.TimeoutSeconds)
			}
		}
	}
	return d
}

func buildPolicyFromDraft(d policyFormDraft, isGlobalOrHost bool) *kopia.Policy {
	pol := &kopia.Policy{
		RetentionPolicy: kopia.RetentionPolicy{
			KeepLatest:               floatOptInt(d.keepLatest),
			KeepHourly:               floatOptInt(d.keepHourly),
			KeepDaily:                floatOptInt(d.keepDaily),
			KeepWeekly:               floatOptInt(d.keepWeekly),
			KeepMonthly:              floatOptInt(d.keepMonthly),
			KeepAnnual:               floatOptInt(d.keepAnnual),
			IgnoreIdenticalSnapshots: boolPtr(d.ignoreIdent),
		},
		SchedulingPolicy: kopia.SchedulingPolicy{
			Manual:          d.manual,
			IntervalSeconds: int64(d.intervalMinutes * 60),
			Cron:            trimLines(d.cronText),
			RunMissed:       boolPtr(d.runMissed),
		},
		FilesPolicy: kopia.FilesPolicy{
			IgnoreRules:            trimLines(d.ignoreRulesText),
			NoParentIgnoreRules:    d.noParentIgnore,
			DotIgnoreFiles:         trimLines(d.dotIgnoreText),
			IgnoreCacheDirectories: boolPtr(d.ignoreCacheDirs),
			OneFileSystem:          boolPtr(d.oneFileSystem),
			MaxFileSize:            int64(d.maxFileSizeMB * 1024 * 1024),
		},
		CompressionPolicy: kopia.CompressionPolicy{
			CompressorName: strings.TrimSpace(d.compressorName),
			MinSize:        int64(d.minCompBytes),
			MaxSize:        int64(d.maxCompBytes),
			OnlyCompress:   trimLines(d.onlyCompText),
			NeverCompress:  trimLines(d.neverCompText),
		},
		MetadataCompressionPolicy: kopia.MetadataCompressionPolicy{
			CompressorName: strings.TrimSpace(d.metaCompressor),
		},
		ErrorHandlingPolicy: kopia.ErrorHandlingPolicy{
			IgnoreFileErrors:      boolPtr(d.ignoreFileErr),
			IgnoreDirectoryErrors: boolPtr(d.ignoreDirErr),
			IgnoreUnknownTypes:    boolPtr(d.ignoreUnknown),
		},
		UploadPolicy: kopia.UploadPolicy{
			MaxParallelFileReads: floatOptInt(d.maxParallelReads),
		},
		LoggingPolicy: kopia.LoggingPolicy{
			Directories: kopia.DirLoggingPolicy{
				Snapshotted: floatOptInt(d.logDirSnap),
				Ignored:     floatOptInt(d.logDirIgnored),
			},
			Entries: kopia.EntryLoggingPolicy{
				Snapshotted: floatOptInt(d.logEntrySnap),
				Ignored:     floatOptInt(d.logEntryIgnored),
			},
		},
	}

	if isGlobalOrHost {
		pol.UploadPolicy.MaxParallelSnapshots = floatOptInt(d.maxParallelSnaps)
	}
	if d.parallelAboveMB > 0 {
		v := int64(d.parallelAboveMB * 1024 * 1024)
		pol.UploadPolicy.ParallelUploadAboveSize = &v
	}

	for _, line := range trimLines(d.timesOfDayText) {
		if tod, err := kopia.ParseTimeOfDay(line); err == nil {
			pol.SchedulingPolicy.TimesOfDay = append(pol.SchedulingPolicy.TimesOfDay, tod)
		}
	}

	mode := d.hookMode
	if mode == "" {
		mode = "optional"
	}
	timeout := int(d.hookTimeoutSec)
	if timeout <= 0 {
		timeout = 30
	}
	if s := strings.TrimSpace(d.beforeRootScript); s != "" {
		pol.Actions.BeforeSnapshotRoot = &kopia.ActionCommand{
			Script:         s,
			Mode:           mode,
			TimeoutSeconds: timeout,
		}
	}
	if s := strings.TrimSpace(d.afterRootScript); s != "" {
		pol.Actions.AfterSnapshotRoot = &kopia.ActionCommand{
			Script:         s,
			Mode:           mode,
			TimeoutSeconds: timeout,
		}
	}
	return pol
}

func (a *App) renderPoliciesView(c *ui.Context, _ config.RepoProfile, st kopia.RepoSnapshotState) {
	if a.policyTargetStr == "" {
		a.policyTargetStr = "(global)"
	}
	target := parseTargetKey(a.policyTargetStr, st)
	if a.policyLoadedTarget != target.String() {
		a.loadPolicyTargetAsync(target)
	}

	ui.Split(c, &a.policySplitWidth, func() {
		a.renderPolicyTargetsSidebar(c, st)
	}, func() {
		a.renderPolicyEditorPane(c, st, target)
	}).SplitReset(208).Fill()
}

func (a *App) renderPolicyTargetsSidebar(c *ui.Context, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	seen := map[string]bool{"(global)": true}
	targets := []kopia.SourceInfo{{}}

	for _, s := range st.SourceList() {
		k := s.Source.String()
		if !seen[k] {
			seen[k] = true
			targets = append(targets, s.Source)
		}
	}
	for _, pol := range st.Policies {
		k := pol.Target.String()
		if !seen[k] {
			seen[k] = true
			targets = append(targets, pol.Target)
		}
	}

	ui.Column(c).Fill().Padding(12).Gap(8).Background(p.cardBg).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.PolicyTargets()).FontSize(12).FontWeight(600).TextColor(p.muted).Grow(1)
			if ui.Button(c, "").Label(locales.AddPathPolicy()).Tooltip(locales.AddPathPolicy()).Padding(4, 6).Radius(6).Children(func() {
				ui.Icon(c, iconPlus).FontSize(13)
			}).Clicked() {
				a.newPolicyFolderPath = ""
				a.addPolicyModalOpen = true
			}
		})

		ui.Scroll(c.Key("pol-targets")).Grow(1).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				for _, tgt := range targets {
					tCopy := tgt
					key := tCopy.String()
					selected := key == a.policyTargetStr
					label := locales.GlobalPolicy()
					sub := "(global)"
					if !tCopy.IsGlobal() {
						if tCopy.Path != "" {
							label = tCopy.Path
							sub = tCopy.UserName + "@" + tCopy.Host
						} else {
							label = key
							sub = ""
						}
					}

					bg := ui.Transparent
					if selected {
						bg = p.accentSoft
					}
					btn := ui.ButtonBase(c.Key("pol-tgt-" + key)).
						Padding(7, 9).
						Radius(7).
						Background(bg).
						Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
								ui.Text(c, label).FontSize(12.5).FontWeight(500).SingleLine()
								if sub != "" {
									ui.Text(c, sub).FontSize(10.5).TextColor(p.muted).SingleLine()
								}
							})
						})
					if btn.Clicked() && !selected {
						a.policyTargetStr = key
						a.policyLoadedTarget = ""
					}
				}
			})
		})
	})
}

func (a *App) renderPolicyEditorPane(c *ui.Context, st kopia.RepoSnapshotState, target kopia.SourceInfo) {
	p := paletteOf(c)
	d := &a.policyDraft
	var eff *kopia.Policy
	if a.policyResolved != nil {
		eff = a.policyResolved.Effective
	}

	isDirty := a.policyDraft != a.policyCleanDraft

	ui.Column(c).Fill().Padding(16, 20).Gap(12).Children(func() {
		// Header bar
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			title := locales.GlobalPolicy()
			if !target.IsGlobal() {
				if target.Path != "" {
					title = target.Path
				} else {
					title = target.String()
				}
			}
			ui.Text(c, title).FontSize(15).FontWeight(600).SingleLine()
			if isDirty {
				statusPill(c, locales.UnsavedChanges(), c.Theme().Warning, p.warningSoft)
			}
			ui.Spacer(c)

			if !target.IsGlobal() {
				ui.Button(c, locales.DeleteOverride()).TextColor(c.Theme().Danger).OnClick(func() {
					a.deletePolicyAction(target)
				})
			}
			ui.Button(c, locales.ResetChanges()).Disabled(!isDirty).OnClick(func() {
				a.policyDraft = a.policyCleanDraft
				a.loadPolicyTargetAsync(target)
			})
			ui.PrimaryButton(c, locales.SavePolicy()).OnClick(func() {
				a.savePolicyAction(target)
			})
		})

		ui.Tabs(c, &a.policyTab,
			locales.Retention(),
			locales.Schedule(),
			locales.FilesIgnores(),
			locales.Compression(),
			locales.UploadErrors(),
			locales.ActionsLogging(),
		)

		ui.Scroll(c.Key("pol-editor-scroll")).Grow(1).Padding(0, 4, 8, 0).Children(func() {
			card(c, func() {
				switch a.policyTab {
				case 0:
					ui.Form(c, func() {
						ui.Field(c, locales.KeepLatestSnapshots(), func() {
							ui.NumberInput(c.Key("ret-latest"), &d.keepLatest, 0, 9999, 1).Width(150)
						}).Description(locales.PolicyInheritZeroEffective(effIntStr(eff, func(p *kopia.Policy) *int { return p.RetentionPolicy.KeepLatest })))
						ui.Field(c, locales.KeepHourly(), func() {
							ui.NumberInput(c.Key("ret-hourly"), &d.keepHourly, 0, 9999, 1).Width(150)
						}).Description(locales.PolicyInheritZeroEffective(effIntStr(eff, func(p *kopia.Policy) *int { return p.RetentionPolicy.KeepHourly })))
						ui.Field(c, locales.KeepDaily(), func() {
							ui.NumberInput(c.Key("ret-daily"), &d.keepDaily, 0, 9999, 1).Width(150)
						}).Description(locales.PolicyInheritZeroEffective(effIntStr(eff, func(p *kopia.Policy) *int { return p.RetentionPolicy.KeepDaily })))
						ui.Field(c, locales.KeepWeekly(), func() {
							ui.NumberInput(c.Key("ret-weekly"), &d.keepWeekly, 0, 9999, 1).Width(150)
						}).Description(locales.PolicyInheritZeroEffective(effIntStr(eff, func(p *kopia.Policy) *int { return p.RetentionPolicy.KeepWeekly })))
						ui.Field(c, locales.KeepMonthly(), func() {
							ui.NumberInput(c.Key("ret-monthly"), &d.keepMonthly, 0, 9999, 1).Width(150)
						}).Description(locales.PolicyInheritZeroEffective(effIntStr(eff, func(p *kopia.Policy) *int { return p.RetentionPolicy.KeepMonthly })))
						ui.Field(c, locales.KeepAnnual(), func() {
							ui.NumberInput(c.Key("ret-annual"), &d.keepAnnual, 0, 9999, 1).Width(150)
						}).Description(locales.PolicyInheritZeroEffective(effIntStr(eff, func(p *kopia.Policy) *int { return p.RetentionPolicy.KeepAnnual })))
						ui.Field(c, "", func() {
							ui.Checkbox(c, &d.ignoreIdent, locales.IgnoreIdenticalSnapshots())
						})
					})

				case 1:
					ui.Form(c, func() {
						ui.Field(c, "", func() {
							ui.Checkbox(c, &d.manual, locales.ManualBackupsOnly())
						})
						ui.Field(c, locales.BackupIntervalMinutes(), func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
								ui.NumberInput(c.Key("sch-int"), &d.intervalMinutes, 0, 10080, 15).Width(96).Disabled(d.manual)
								renderIntervalBtn := func(label string, mins float64) {
									if !d.manual && d.intervalMinutes == mins {
										if compactPrimaryButton(c, label).Clicked() {
											d.intervalMinutes = mins
										}
									} else {
										if compactButton(c, label).Disabled(d.manual).Clicked() {
											d.intervalMinutes = mins
										}
									}
								}
								renderIntervalBtn(locales.Every15m(), 15)
								renderIntervalBtn(locales.Hourly(), 60)
								renderIntervalBtn(locales.Every6h(), 360)
								renderIntervalBtn(locales.Daily(), 1440)
							})
						})
						ui.Field(c, locales.FixedTimesOfDayHHMMOne(), func() {
							ui.TextArea(c.Key("sch-tod"), &d.timesOfDayText).Height(72).Disabled(d.manual).Placeholder("09:00\n21:00")
						})
						ui.Field(c, locales.CronExpressionsOnePerLine(), func() {
							ui.TextArea(c.Key("sch-cron"), &d.cronText).Height(64).Disabled(d.manual).Placeholder("0 */4 * * *")
						})
						ui.Field(c, "", func() {
							ui.Checkbox(c, &d.runMissed, locales.RunMissedBackupsOnStartup())
						})
					})

					// Upcoming schedule preview
					ui.Box(c).Height(1).Background(p.border)
					ui.Text(c, locales.UpcomingScheduledRuns()).FontSize(12.5).FontWeight(600)
					if a.policyResolved != nil && len(a.policyResolved.UpcomingSnapshotTimes) > 0 {
						ui.Row(c).Wrap().Gap(8).Children(func() {
							for _, ft := range a.policyResolved.UpcomingSnapshotTimes {
								statusPill(c, formatTimestamp(ft.Time), p.text, p.subtleBg)
							}
						})
					} else {
						ui.Text(c, locales.NoAutomaticRunsScheduled()).FontSize(12).TextColor(p.muted)
					}

				case 2:
					ui.Form(c, func() {
						ui.Field(c, locales.IgnoreRulesOnePatternPerLine(), func() {
							ui.Column(c).Gap(6).Children(func() {
								ui.TextArea(c.Key("fil-ignore"), &d.ignoreRulesText).Height(120).Font("monospace").Placeholder("node_modules/\n.DS_Store\n*.tmp")
								if ui.Button(c, locales.AddCommonIgnores()).Clicked() {
									preset := "node_modules/\n.DS_Store\nThumbs.db\n*.tmp\n.Trash/\n__pycache__/"
									if strings.TrimSpace(d.ignoreRulesText) == "" {
										d.ignoreRulesText = preset
									} else {
										d.ignoreRulesText += "\n" + preset
									}
								}
							})
						})
						ui.Field(c, "", func() {
							ui.Checkbox(c, &d.noParentIgnore, locales.DoNotInheritParentIgnoreRules())
						})
						ui.Field(c, locales.DotIgnoreFilesEGKopiaignoreGitignore(), func() {
							ui.TextArea(c.Key("fil-dot"), &d.dotIgnoreText).Height(68).Font("monospace").Placeholder(".kopiaignore\n.gitignore")
						})
						ui.Field(c, locales.MaxFileSizeMB0Unlimited(), func() {
							ui.NumberInput(c.Key("fil-maxmb"), &d.maxFileSizeMB, 0, 1048576, 10).Width(160)
						})
						ui.Field(c, "", func() {
							ui.Column(c).Gap(6).Children(func() {
								ui.Checkbox(c, &d.ignoreCacheDirs, locales.SkipCacheDirectoriesCachedirTAG())
								ui.Checkbox(c, &d.oneFileSystem, locales.StayOnOneFilesystem())
							})
						})
					})

				case 3:
					inheritLabel := locales.InheritDefault()
					compAlgos := append([]string{inheritLabel, "none"}, algorithmNames(algosField(st.Algorithms, "comp"), "zstd", "zstd-fastest", "zstd-better-compression", "s2-default", "pgzip")...)
					curComp := d.compressorName
					if curComp == "" {
						curComp = inheritLabel
					}
					curMeta := d.metaCompressor
					if curMeta == "" {
						curMeta = inheritLabel
					}
					ui.Form(c, func() {
						ui.Field(c, locales.CompressionAlgorithm(), func() {
							if ui.Select(c.Key("cmp-algo"), &curComp, compAlgos).Changed() {
								if curComp == inheritLabel {
									d.compressorName = ""
								} else {
									d.compressorName = curComp
								}
							}
						})
						ui.Field(c, locales.MetadataCompression(), func() {
							if ui.Select(c.Key("cmp-meta"), &curMeta, compAlgos).Changed() {
								if curMeta == inheritLabel {
									d.metaCompressor = ""
								} else {
									d.metaCompressor = curMeta
								}
							}
						})
						ui.Field(c, locales.MinCompressSizeBytes(), func() {
							ui.NumberInput(c.Key("cmp-min"), &d.minCompBytes, 0, 1e9, 1024).Width(180)
						})
						ui.Field(c, locales.MaxCompressSizeBytes0NoMax(), func() {
							ui.NumberInput(c.Key("cmp-max"), &d.maxCompBytes, 0, 1e12, 1048576).Width(180)
						})
						ui.Field(c, locales.OnlyCompressExtensionsOnePerLineE(), func() {
							ui.TextArea(c.Key("cmp-only"), &d.onlyCompText).Height(70).Font("monospace")
						})
						ui.Field(c, locales.NeverCompressExtensionsOnePerLineE(), func() {
							ui.TextArea(c.Key("cmp-never"), &d.neverCompText).Height(70).Font("monospace").Placeholder(".mp4\n.zip\n.jpg")
						})
					})

				case 4:
					isGlobalOrHost := target.IsGlobal() || target.IsHostLevel()
					ui.Form(c, func() {
						ui.Field(c, locales.MaxParallelFileReads(), func() {
							ui.NumberInput(c.Key("up-reads"), &d.maxParallelReads, 0, 128, 1).Width(150)
						})
						if isGlobalOrHost {
							ui.Field(c, locales.MaxParallelSnapshotsGlobalHost(), func() {
								ui.NumberInput(c.Key("up-snaps"), &d.maxParallelSnaps, 0, 64, 1).Width(150)
							})
						}
						ui.Field(c, locales.ParallelUploadAboveSizeMB(), func() {
							ui.NumberInput(c.Key("up-above"), &d.parallelAboveMB, 0, 65536, 16).Width(150)
						})
						ui.Field(c, "", func() {
							ui.Column(c).Gap(6).Children(func() {
								ui.Checkbox(c, &d.ignoreFileErr, locales.IgnoreFileReadErrors())
								ui.Checkbox(c, &d.ignoreDirErr, locales.IgnoreDirectoryReadErrors())
								ui.Checkbox(c, &d.ignoreUnknown, locales.IgnoreUnknownEntryTypes())
							})
						})
					})

				case 5:
					ui.Column(c).Gap(4).Children(func() {
						ui.Text(c, locales.HookActions()).FontSize(13).FontWeight(600)
						ui.Text(c, locales.EnableHookActionsOnClient()).FontSize(11.5).TextColor(p.muted)
					})
					ui.Form(c, func() {
						ui.Field(c, locales.BeforeSnapshotRootScript(), func() {
							ui.TextArea(c.Key("act-before"), &d.beforeRootScript).Height(68).Font("monospace")
						})
						ui.Field(c, locales.AfterSnapshotRootScript(), func() {
							ui.TextArea(c.Key("act-after"), &d.afterRootScript).Height(68).Font("monospace")
						})
						ui.Field(c, locales.HookExecutionMode(), func() {
							ui.Select(c.Key("act-mode"), &d.hookMode, []string{"optional", "essential", "async"})
						})
						ui.Field(c, locales.HookTimeoutSeconds(), func() {
							ui.NumberInput(c.Key("act-timeout"), &d.hookTimeoutSec, 1, 3600, 5).Width(140)
						})
					})
					ui.Box(c).Height(1).Background(p.border)
					ui.Text(c, locales.LoggingLevels()).FontSize(13).FontWeight(600)
					ui.Form(c, func() {
						ui.Field(c, locales.DirSnapshottedLogLevel(), func() {
							ui.NumberInput(c.Key("log-ds"), &d.logDirSnap, 0, 10, 1).Width(140)
						})
						ui.Field(c, locales.DirIgnoredLogLevel(), func() {
							ui.NumberInput(c.Key("log-di"), &d.logDirIgnored, 0, 10, 1).Width(140)
						})
						ui.Field(c, locales.EntrySnapshottedLogLevel(), func() {
							ui.NumberInput(c.Key("log-es"), &d.logEntrySnap, 0, 10, 1).Width(140)
						})
						ui.Field(c, locales.EntryIgnoredLogLevel(), func() {
							ui.NumberInput(c.Key("log-ei"), &d.logEntryIgnored, 0, 10, 1).Width(140)
						})
					})
				}
			}).MaxWidth(780)
		})
	})
}

func (a *App) renderPolicyModals(c *ui.Context) {
	if !a.addPolicyModalOpen {
		return
	}
	_, st := a.activeRepoState()
	ui.Modal(c, &a.addPolicyModalOpen, func() {
		ui.Column(c).Width(440).Gap(16).Children(func() {
			ui.Text(c, locales.AddPathPolicy()).FontSize(15.5).FontWeight(600)
			ui.Form(c, func() {
				ui.Field(c, locales.TargetFolderPath(), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("new-pol-path"), &a.newPolicyFolderPath).AutoFocus().Grow(1)
						if a.desktopRunning {
							if ui.Button(c, locales.BrowsePicker()).Clicked() {
								go a.pickDirectoryInto(&a.newPolicyFolderPath)
							}
						}
					})
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					a.addPolicyModalOpen = false
				}
				ui.PrimaryButton(c, locales.Apply()).Disabled(strings.TrimSpace(a.newPolicyFolderPath) == "").OnClick(func() {
					tgt := parseTargetKey(filepath.Clean(config.ExpandPath(a.newPolicyFolderPath)), st)
					a.addPolicyModalOpen = false
					a.policyTargetStr = tgt.String()
					a.policyLoadedTarget = ""
				})
			})
		})
	})
}

func (a *App) savePolicyAction(target kopia.SourceInfo) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" {
		return
	}
	pol := buildPolicyFromDraft(a.policyDraft, target.IsGlobal() || target.IsHostLevel())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.SetPolicy(ctx, target, pol); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.uiUpdate(func() {
			a.loadPolicyTargetAsync(target)
		})
		a.toast(locales.PolicySaved(), false)
	}()
}

func (a *App) deletePolicyAction(target kopia.SourceInfo) {
	repoID := a.settings.ActiveRepoID
	if a.manager == nil || repoID == "" || target.IsGlobal() {
		return
	}
	label := target.String()
	if target.Path != "" {
		label = target.Path
	}
	go func() {
		if !a.confirmDestructive(locales.DeleteOverride()+": "+label+"?", locales.Delete()) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			a.toast(err.Error(), true)
			return
		}
		if err := cli.DeletePolicy(ctx, target); err != nil {
			a.toast(err.Error(), true)
			return
		}
		a.manager.RefreshRepo(ctx, repoID, false)
		a.uiUpdate(func() {
			a.policyTargetStr = "(global)"
			a.policyLoadedTarget = ""
		})
		a.toast(locales.PolicyOverrideDeleted(), false)
	}()
}

func optIntFloat(v *int) float64 {
	if v == nil || *v < 0 {
		return 0
	}
	return float64(*v)
}

func floatOptInt(f float64) *int {
	n := int(f)
	if n <= 0 {
		return nil
	}
	return &n
}

func optBoolVal(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func boolPtr(b bool) *bool {
	return &b
}

func effIntStr(eff *kopia.Policy, fn func(*kopia.Policy) *int) string {
	if eff == nil {
		return "—"
	}
	v := fn(eff)
	if v == nil {
		return "—"
	}
	return intToStr(*v)
}
