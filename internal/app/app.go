// @scope.file app.core
// @hash sha256:4089493662be47e2
// @implements spec.desktop_ui sha256:67347d72ce79be0a
// @ref default github.com/egoist/mygo@0.9.1
// @ref default github.com/egoist/mygo/ui@0.9.1
// @ref default golang.org/x/sync@0.25.0
// @ref default config.store sha256:b49642aa028d360a
// @ref default index.engine sha256:1aea8ec7725d31ae
// @ref default kopia.client sha256:c1253141ff6ec455
// @ref default kopia.manager sha256:332d271a00525eae
// @ref default kopia.types sha256:06dd81559d688041
// @ref default app.cache sha256:6e5e1b91e6872ef4
// @ref default app.icons sha256:a3df148b095e3dde
// @ref default app.locale sha256:0ce2d55e5d85d9b3
// @ref default app.theme sha256:5167155d01845017
// @ref default app.view_backups sha256:86061e8786a53e61
// @ref default app.view_connect sha256:90b187964bdf27c9
// @ref default app.view_diff sha256:7a1c425c5f83abf0
// @ref default app.view_policies sha256:144a290ee4d6d2f2
// @ref default app.view_search sha256:e9ad75c5374d5129
// @ref default app.view_snapshots sha256:d032290fc4d5b704
// @ref default app.view_tasks_repo_settings sha256:4c6b60617687d978
// Root immediate-mode desktop application controller, window/tray lifecycle, and two-phase repository refresh.

package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sync/singleflight"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
	"mygopia/locales"
)

const sidebarRailWidth float32 = 52

// Navigation section IDs.
const (
	navBackups   = "backups"
	navSnapshots = "snapshots"
	navSearch    = "search"
	navDiff      = "diff"
	navPolicies  = "policies"
	navTasks     = "tasks"
	navRepo      = "repo"
	navSettings  = "settings"
)

// Options configures an App instance (both desktop runtime and headless tests).
type Options struct {
	UserDataDir    string
	CacheDir       string
	LogDir         string
	Settings       *config.Settings
	PreferredLangs []string
	DisableServers bool
}

// App holds the main-thread UI state and coordinates async backend operations.
type App struct {
	userDataDir    string
	cacheDir       string
	logDir         string
	detectedKopia  string
	detectedRclone string
	preferredLangs []string
	desktopRunning bool

	settings config.Settings
	manager  *kopia.Manager
	indexer  *index.Engine

	window      *mygo.Window
	winAtom     atomic.Pointer[mygo.Window]
	desktopMode atomic.Bool
	notifyAtom  atomic.Bool
	uiMu        sync.Mutex
	uiQueueMu   sync.Mutex
	uiBusy      bool
	uiQueue     []func()
	tray        *mygo.Tray
	trayReady   bool
	quitting    bool

	// Pending toast emitted from async goroutines.
	pendingToast    string
	pendingToastErr bool

	// Shell & sidebar state
	activeNav        string
	sidebarLiveWidth float32
	lastSidebarWidth float32
	refreshing       bool
	refreshSlow      bool
	refreshPhase     string
	refreshSeq       uint64
	refreshCancel    context.CancelFunc
	snapLoadSeq      uint64
	snapLoadSlow     bool
	browserLoadSeq   uint64
	browserLoadSlow  bool
	diffLoadSeq      uint64
	diffSnapLoading  bool
	diffSnapSlow     bool
	diffCalcSeq      uint64
	diffCalcSlow     bool

	// Add Repository / Connect wizard state
	connectModalOpen      bool
	connectAsNewRepo      bool
	forceOnboardingRepoID string
	repoActivating        bool
	connectForm           connectFormState

	// Backups view state
	addSourceModalOpen bool
	newSourcePath      string
	newSourceSnapNow   bool

	// Snapshots & File Browser state
	snapSourceStr       string
	snapShowIdentical   bool
	snapLoading         bool
	snapshots           []*kopia.Snapshot
	snapList            ui.ListState
	selectedSnapIdx     int
	snapSplitWidth      float32
	snapTimePreset      int
	snapYearFilter      string
	snapDateFrom        time.Time
	snapDateFromText    string
	snapDateTo          time.Time
	snapDateToText      string
	snapFilterText      string
	snapPinnedOnly      bool
	snapDisplayLimit    int
	browserBreadcrumbs  []dirCrumb
	browserEntries      []*kopia.DirEntry
	browserFilter       string
	browserKindSeg      int
	browserSizePreset   int
	browserTimePreset   int
	browserDateFrom     time.Time
	browserDateFromText string
	browserDateTo       time.Time
	browserDateToText   string
	browserPageOffset   int
	browserTable        ui.ListState
	browserSelectedRow  int
	browserSort         ui.SortOrder
	browserLoading      bool
	// Restore & Edit Snapshot state
	editSnapModalOpen  bool
	editSnapID         string
	editSnapDesc       string
	editSnapPinned     bool
	restoreModalOpen   bool
	restoreRepoID      string
	restoreTargetOID   string
	restoreDefaultName string
	restoreMode        int // 0 folder, 1 zip, 2 tar
	restoreDestPath    string
	restoreOverwriteF  bool
	restoreOverwriteD  bool
	restoreIncremental bool
	restoreDeleteExtra bool
	restoreIgnorePerm  bool
	restoreSkipOwners  bool

	// KopiaLens-style Search state
	focusSearchNextFrame bool
	searchQuery          string
	searchAllRepos       bool
	searchSourceFilter   string
	searchKindSeg        int // 0 All, 1 Files, 2 Folders
	searchStatusSeg      int // 0 All, 1 On Disk Only, 2 Deleted Only
	searchDeletedOnly    bool
	searchMultiVersion   bool
	searchSizePreset     int // 0 Any, 1 >1MB, 2 >10MB, 3 >100MB, 4 >1GB
	searchMaxSizePreset  int // 0 Any, 1 <1MB, 2 <10MB, 3 <100MB, 4 <1GB
	searchTimePreset     int
	searchDateFrom       time.Time
	searchDateFromText   string
	searchDateTo         time.Time
	searchDateToText     string
	searchPathPrefix     string
	searchIncludeExts    string
	searchExcludeExts    string
	searchShowAdvanced   bool
	searchPageOffset     int
	searchTable          ui.ListState
	searchSelectedRow    int
	searchSort           ui.SortOrder
	searchDetailSplit    float32
	searchDirty          bool
	searchResult         index.Result
	searchVerFilter      string
	searchVerLimit       int
	searchVerItemPath    string
	searchVerHistKey     string
	searchVerHistSlice   []index.VersionRecord

	// Snapshot Diff state
	diffSourceStr    string
	diffSnapshots    []*kopia.Snapshot
	diffBaseSnapID   string
	diffTargetSnapID string
	diffActivePreset int // 0 custom, 1 prev, 2 7d, 3 30d, 4 oldest
	diffYearFilter   string
	diffSnapSearch   string
	diffLoading      bool
	diffSummary      index.DiffSummary
	diffChangeSeg    int // 0 All, 1 Added, 2 Modified, 3 Deleted
	diffKindSeg      int // 0 All, 1 Files, 2 Folders
	diffSizePreset   int
	diffTimePreset   int
	diffDateFrom     time.Time
	diffDateFromText string
	diffDateTo       time.Time
	diffDateToText   string
	diffFilterQuery  string
	diffIncludeExts  string
	diffTable        ui.ListState
	diffSelectedRow  int
	diffSort         ui.SortOrder
	diffPageOffset   int

	// Policies Studio state
	policySplitWidth    float32
	policyTargetStr     string
	policyTab           int
	policyDraft         policyFormDraft
	policyCleanDraft    policyFormDraft
	policyResolved      *kopia.ResolvePolicyResponse
	policyLoadedTarget  string
	addPolicyModalOpen  bool
	newPolicyFolderPath string

	// Tasks & Logs state
	tasksTab         int // 0 Tasks, 1 Server Logs
	tasksTable       ui.ListState
	selectedTaskRow  int
	selectedTaskID   string
	selectedTaskLogs []kopia.TaskLogEntry

	// Repository & Maintenance state
	repoTab          int // 0 Overview & Stats, 1 Bandwidth, 2 Maintenance, 3 Notifications
	repoDescDraft    string
	repoDescLoadedID string
	throttleDraft    throttleFormDraft
	throttleLoadedID string
	maintDraft       maintFormDraft
	maintLoadedID    string
	notifyForm       notifyProfileDraft

	// Settings draft state
	settingsThemeSeg       int
	settingsLangSeg        int
	settingsScaleSeg       int
	settingsCacheMaxIdx    int
	settingsCacheUsedBytes int64
	settingsCacheUsedAt    time.Time
	settingsKopiaBin       string
	settingsRcloneBin      string
	settingsCfgDir         string
	settingsLoaded         bool

	// Coalesced request singleflight, bounded snapshot/policy hot caches & frame memoization
	sfGroup            singleflight.Group
	cacheMu            sync.RWMutex
	snapListCache      map[snapCacheKey][]*kopia.Snapshot
	policyCache        map[string]*kopia.Policy
	snapshotsRev       uint64
	browserEntriesRev  uint64
	diffSummaryRev     uint64
	diffActiveCalcKey  string
	snapMemoKey        snapTimelineMemoKey
	snapMemoIdx        []int
	browserMemoKey     browserFilterMemoKey
	browserMemoEntries []*kopia.DirEntry
	diffMemoKey        diffFilterMemoKey
	diffMemoEntries    []index.DiffEntry

	// Display geometry for resolution-adaptive UI/font scaling
	displayBounds mygo.Rectangle

	// Optional seeded states & local folder overrides for headless UI testing & visual verification
	seededStates      map[string]kopia.RepoSnapshotState
	localDirOverrides map[string]string
}

type dirCrumb struct {
	Name string
	OID  string
}

// New constructs an App ready to render via ui.View(app.View) orui.NewTester.
func New(opts Options) *App {
	var cfg config.Settings
	if opts.Settings != nil {
		cfg = *opts.Settings
		cfg.Normalize()
	} else {
		loaded, err := config.Load(opts.UserDataDir)
		if err == nil {
			cfg = loaded
		} else {
			cfg = config.DefaultSettings()
		}
	}

	var resDirs []string
	if opts.UserDataDir != "" {
		if rd, err := mygo.App.Path(mygo.PathResources); err == nil && rd != "" {
			resDirs = append(resDirs, rd)
		}
	}
	kopiaBin, _ := config.ResolveKopiaBinary(cfg.KopiaBinary, resDirs...)
	rcloneBin, _ := config.ResolveRcloneBinary(cfg.RcloneBinary, resDirs...)
	cfg.Repositories = config.DiscoverProfiles(cfg.Repositories, cfg.EffectiveConfigDir())
	if cfg.ActiveRepoID == "" && len(cfg.Repositories) > 0 {
		cfg.ActiveRepoID = cfg.Repositories[0].ID
	}

	a := &App{
		userDataDir:        opts.UserDataDir,
		cacheDir:           opts.CacheDir,
		logDir:             opts.LogDir,
		detectedKopia:      kopiaBin,
		detectedRclone:     rcloneBin,
		preferredLangs:     opts.PreferredLangs,
		settings:           cfg,
		activeNav:          navBackups,
		newSourceSnapNow:   true,
		selectedSnapIdx:    -1,
		snapSplitWidth:     285,
		snapDisplayLimit:   defaultSnapshotPageSize,
		browserSelectedRow: -1,
		browserSort:        ui.SortOrder{Column: "name", Descending: false},
		searchSelectedRow:  -1,
		searchSort:         ui.SortOrder{Column: "size", Descending: true},
		searchDetailSplit:  65,
		searchDirty:        true,
		searchVerLimit:     defaultVersionPageSize,
		diffSelectedRow:    -1,
		diffSort:           ui.SortOrder{Column: "path", Descending: false},
		policySplitWidth:   208,
		selectedTaskRow:    -1,
		restoreOverwriteF:  true,
		restoreOverwriteD:  true,
		restoreIncremental: true,
		restoreIgnorePerm:  true,
		restoreSkipOwners:  true,
	}

	a.notifyAtom.Store(a.settings.Notify)
	a.initConnectForm()
	a.syncSettingsDraft()

	a.indexer = index.NewEngine(opts.CacheDir, func() {
		a.uiUpdate(func() {
			a.searchDirty = true
		})
	})
	a.indexer.SetMaxCacheMB(a.settings.CacheMaxMB)
	a.indexer.SetOnSourceSnapshots(func(repoID string, src kopia.SourceInfo, snaps []*kopia.Snapshot) {
		a.putCachedSnapshots(repoID, src.String(), true, snaps)
	})

	if !opts.DisableServers {
		a.manager = kopia.NewManager(kopiaBin, opts.LogDir, func() {
			a.uiInvalidate()
		}, func(repoName string, payload kopia.DesktopNotificationPayload) {
			a.showDesktopNotification(repoName, payload)
		})
		a.manager.SetRcloneBinary(a.detectedRclone)
		a.manager.SyncProfiles(a.settings.Repositories)
	}

	setCurrentLanguage(ResolveLanguage(a.settings.Language, a.preferredLangs))
	return a
}

// Engine exposes the underlying snapshot search engine (useful for tests & preload).
func (a *App) Engine() *index.Engine {
	return a.indexer
}

// SetDisplayBounds sets the active monitor DIP bounds (used by desktop runtime and resolution tests).
func (a *App) SetDisplayBounds(width, height int) {
	a.displayBounds = mygo.Rectangle{Width: width, Height: height}
}

func initialWindowSize(workArea mygo.Rectangle) (int, int) {
	w, h := 1180, 760
	if workArea.Width >= 3000 {
		w, h = 1480, 940
	} else if workArea.Width >= 2200 {
		w, h = 1320, 840
	}
	if workArea.Width > 400 && w > workArea.Width-48 {
		w = max(860, workArea.Width-48)
	}
	if workArea.Height > 360 && h > workArea.Height-48 {
		h = max(560, workArea.Height-48)
	}
	return w, h
}

// SeedRepoState populates a repository state snapshot for headless UI tests.
func (a *App) SeedRepoState(repoID string, st kopia.RepoSnapshotState) {
	if a.seededStates == nil {
		a.seededStates = make(map[string]kopia.RepoSnapshotState)
	}
	a.seededStates[repoID] = st
}

// SeedSnapshotsAndEntries populates snapshots and directory entries for headless UI tests.
func (a *App) SeedSnapshotsAndEntries(sourceStr string, snaps []*kopia.Snapshot, entries []*kopia.DirEntry) {
	a.snapSourceStr = sourceStr
	a.setSnapshotsSlice(snaps)
	if len(snaps) > 0 {
		a.selectedSnapIdx = 0
		a.browserBreadcrumbs = []dirCrumb{{Name: "", OID: snaps[0].RootID}}
	}
	a.setBrowserEntriesSlice(entries)
	a.diffSourceStr = sourceStr
	a.diffSnapshots = slices.Clone(snaps)
	a.diffActiveCalcKey = ""
	a.ensureDefaultDiffPair()
}

// AppVersion is the current application version of Mygopia.
const AppVersion = "0.3.3"

// RunDesktop launches the native desktop application window, menus, tray, and lifecycle hooks.
func RunDesktop() error {
	mygo.App.SetName("Mygopia")
	mygo.App.SetVersion(AppVersion)
	mygo.App.SetLanguages("en", "zh-Hans", "zh-Hant", "ja", "fr", "de", "es")

	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}

	userDataDir, _ := mygo.App.Path(mygo.PathUserData)
	cacheDir, _ := mygo.App.Path(mygo.PathCache)
	logDir, _ := mygo.App.Path(mygo.PathLogs)

	cfg, _ := config.Load(userDataDir)
	applyNativeThemeSource(cfg.Theme)
	applyNativeLocale(cfg.Language)

	app := New(Options{
		UserDataDir:    userDataDir,
		CacheDir:       cacheDir,
		LogDir:         logDir,
		Settings:       &cfg,
		PreferredLangs: mygo.App.PreferredLanguages(),
	})
	app.desktopRunning = true
	app.desktopMode.Store(true)

	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		app.showMainWindow()
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		app.quitting = true
	})
	mygo.App.OnWindowAllClosed(func() {
		if !app.canRunInBackground() {
			mygo.App.Quit()
			return
		}
		app.trimCachesForBackground()
	})
	mygo.App.OnActivate(func(hasVisible bool) {
		if !hasVisible {
			app.showMainWindow()
		}
	})
	mygo.App.OnDidBecomeActive(func() {
		mygo.ClearNotifications()
	})
	mygo.App.OnQuit(func() {
		if app.manager != nil {
			app.manager.Shutdown()
		}
		if app.indexer != nil {
			_ = app.indexer.Close()
		}
	})

	mygo.App.WhenReady(func() {
		app.preferredLangs = mygo.App.PreferredLanguages()
		setCurrentLanguage(ResolveLanguage(app.settings.Language, app.preferredLangs))
		mygo.App.OnLocaleChanged(func(_ string) {
			app.preferredLangs = mygo.App.PreferredLanguages()
			setCurrentLanguage(ResolveLanguage(app.settings.Language, app.preferredLangs))
			app.setupMenus()
			app.refreshTrayMenu()
			if w := app.winAtom.Load(); w != nil && !w.IsDestroyed() {
				w.Invalidate()
			}
		})

		// Sync OS login-item registration with persisted preference.
		if mygo.App.OpenAtLogin() {
			app.settings.OpenAtLogin = true
		} else if app.settings.OpenAtLogin {
			if err := mygo.App.SetOpenAtLogin(true); err != nil && mygo.App.IsPackaged() {
				app.settings.OpenAtLogin = false
			}
		}

		app.setupMenus()
		app.syncTrayVisibility()

		primary := mygo.Screen.PrimaryDisplay()
		app.displayBounds = primary.Bounds
		winW, winH := initialWindowSize(primary.WorkArea)

		winOpts := mygo.WindowOptions{
			Title:          "Mygopia",
			Width:          winW,
			Height:         winH,
			MinWidth:       860,
			MinHeight:      560,
			StateKey:       "main",
			Hidden:         mygo.App.WasOpenedAtLogin() && app.canRunInBackground(),
			TitleBarStyle:  mygo.TitleBarHidden,
			TitleBarHeight: windowHeaderDIP,
			Content:        ui.View(app.View),
		}
		if runtime.GOOS == "darwin" {
			winOpts.TitleBarStyle = mygo.TitleBarHiddenInset
			winOpts.TrafficLightPosition = &mygo.Point{X: 16, Y: windowBarMid - 7}
		}

		win := mygo.NewWindow(winOpts)
		app.window = win
		app.winAtom.Store(win)

		refreshDisplay := func() {
			if win.IsDestroyed() {
				return
			}
			d := mygo.Screen.DisplayMatching(win.Bounds())
			if d.Bounds.Width > 0 && d.Bounds != app.displayBounds {
				app.displayBounds = d.Bounds
				win.Invalidate()
			}
		}
		refreshDisplay()
		win.OnMove(refreshDisplay)
		mygo.Screen.OnDisplaysChanged(refreshDisplay)

		win.OnClose(func(e *mygo.CloseEvent) {
			if !app.quitting && app.canRunInBackground() {
				e.PreventDefault()
				win.Hide()
			}
		})

		if app.settings.ActiveRepoID != "" {
			app.activateRepo(app.settings.ActiveRepoID)
		}
	})

	return mygo.App.Run()
}

func applyNativeThemeSource(mode string) {
	switch mode {
	case config.ThemeLight:
		mygo.Theme.SetSource(mygo.ThemeLight)
	case config.ThemeDark:
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

func applyNativeLocale(mode string) {
	switch mode {
	case config.LangEN, config.LangZhHans, config.LangZhHant, config.LangJA, config.LangFR, config.LangDE, config.LangES:
		mygo.App.SetLocale(mode)
	default:
		mygo.App.SetLocale("")
	}
}

func (a *App) enterHeadlessUI() {
	a.uiMu.Lock()
	a.uiQueueMu.Lock()
	a.uiBusy = true
	q := a.uiQueue
	a.uiQueue = nil
	a.uiQueueMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func (a *App) exitHeadlessUI() {
	for {
		a.uiQueueMu.Lock()
		rem := a.uiQueue
		a.uiQueue = nil
		if len(rem) == 0 {
			a.uiBusy = false
			a.uiQueueMu.Unlock()
			break
		}
		a.uiQueueMu.Unlock()
		for _, fn := range rem {
			fn()
		}
	}
	a.uiMu.Unlock()
}

func (a *App) uiUpdate(fn func()) {
	win := a.winAtom.Load()
	if win != nil && !win.IsDestroyed() {
		win.Update(fn)
		return
	}
	if a.desktopMode.Load() {
		return
	}
	a.uiQueueMu.Lock()
	if a.uiBusy {
		a.uiQueue = append(a.uiQueue, fn)
		a.uiQueueMu.Unlock()
		return
	}
	a.uiQueueMu.Unlock()
	a.enterHeadlessUI()
	defer a.exitHeadlessUI()
	fn()
}

func (a *App) uiInvalidate() {
	win := a.winAtom.Load()
	if win != nil && !win.IsDestroyed() {
		win.Invalidate()
	}
}

func (a *App) toast(msg string, isErr bool) {
	a.uiUpdate(func() {
		a.pendingToast = msg
		a.pendingToastErr = isErr
	})
}

// confirmDestructive prompts the user with a native warning dialog on desktop before executing
// a destructive operation. In headless unit tests it returns true immediately.
func (a *App) confirmDestructive(message, confirmLabel string) bool {
	win := a.winAtom.Load()
	if !a.desktopRunning || win == nil || win.IsDestroyed() {
		return true
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:             win,
		Type:               mygo.MessageWarning,
		Message:            message,
		Buttons:            []string{locales.Cancel(), confirmLabel},
		DestructiveButtons: []int{1},
	})
	return err == nil && res.Button == 1
}

func (a *App) showMainWindow() {
	win := a.winAtom.Load()
	if win == nil || win.IsDestroyed() {
		return
	}
	if win.IsMinimized() {
		win.Restore()
	}
	win.Show()
	win.Focus()
}

func (a *App) showDesktopNotification(repoName string, payload kopia.DesktopNotificationPayload) {
	if !a.desktopMode.Load() || !a.notifyAtom.Load() || !mygo.NotificationsSupported() {
		return
	}
	title := payload.Subject
	if title == "" {
		title = "Mygopia — " + repoName
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		Title: title,
		Body:  payload.Body,
	})
	n.OnClick(func() {
		a.showMainWindow()
		n.Close()
	})
	_ = n.Show()
}

func (a *App) setupMenus() {
	if !a.desktopRunning {
		return
	}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{
			Label: "Mygopia",
			Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				mygo.Separator(),
				{
					Label:       locales.NavSettings(),
					Accelerator: "CmdOrCtrl+,",
					Click: func(*mygo.MenuItem, *mygo.Window) {
						a.uiUpdate(func() { a.activeNav = navSettings })
					},
				},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			},
		},
		{Role: mygo.RoleEditMenu},
		{
			Label: locales.View(),
			Submenu: []*mygo.MenuItem{
				{
					Label:       locales.ToggleSidebar(),
					Accelerator: "CmdOrCtrl+B",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.toggleSidebarCollapsed() }) },
				},
				mygo.Separator(),
				{
					Label:       locales.NavBackups(),
					Accelerator: "CmdOrCtrl+1",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navBackups) }) },
				},
				{
					Label:       locales.NavSnapshots(),
					Accelerator: "CmdOrCtrl+2",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navSnapshots) }) },
				},
				{
					Label:       locales.NavSearch(),
					Accelerator: "CmdOrCtrl+3",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navSearch) }) },
				},
				{
					Label:       locales.NavSnapshotDiff(),
					Accelerator: "CmdOrCtrl+4",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navDiff) }) },
				},
				{
					Label:       locales.NavPolicies(),
					Accelerator: "CmdOrCtrl+5",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navPolicies) }) },
				},
				{
					Label:       locales.NavTasksLogs(),
					Accelerator: "CmdOrCtrl+6",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navTasks) }) },
				},
				{
					Label:       locales.NavRepository(),
					Accelerator: "CmdOrCtrl+7",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.switchNavTo(navRepo) }) },
				},
				mygo.Separator(),
				{
					Label:       locales.ZoomIn(),
					Accelerator: "CmdOrCtrl+=",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.stepUIScale(1) }) },
				},
				{
					Label:       locales.ZoomOut(),
					Accelerator: "CmdOrCtrl+-",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.stepUIScale(-1) }) },
				},
				{
					Label:       locales.DefaultSize(),
					Accelerator: "CmdOrCtrl+0",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.stepUIScale(0) }) },
				},
				mygo.Separator(),
				{
					Label:       locales.Refresh(),
					Accelerator: "CmdOrCtrl+R",
					Click:       func(*mygo.MenuItem, *mygo.Window) { a.uiUpdate(func() { a.triggerManualRefresh() }) },
				},
			},
		},
		{Role: mygo.RoleWindowMenu},
	})
	mygo.App.SetMenu(menu)
}

func (a *App) canRunInBackground() bool {
	return a.settings.CloseToTray && (a.trayReady || runtime.GOOS == "darwin")
}

func (a *App) syncTrayVisibility() {
	if !a.desktopRunning {
		return
	}
	if a.settings.ShowInMenuBar {
		if a.tray != nil && a.trayReady {
			return
		}
		a.setupTray()
		return
	}
	if a.tray != nil {
		a.tray.Destroy()
		a.tray = nil
	}
	a.trayReady = false
}

func (a *App) setupTray() {
	if !a.desktopRunning || !a.settings.ShowInMenuBar {
		return
	}
	iconBytes, isTemplate := loadOrGenerateTrayIcon()
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "mygopia",
		Icon:           iconBytes,
		IconIsTemplate: isTemplate,
		ToolTip:        "Mygopia",
		Menu:           a.buildTrayMenu(),
	})
	if err != nil {
		return
	}
	a.tray = tray
	a.trayReady = true
}

func (a *App) refreshTrayMenu() {
	if a.tray != nil {
		a.tray.SetMenu(a.buildTrayMenu())
	}
}

func (a *App) buildTrayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{
			Label: locales.OpenMygopia(),
			Click: func(*mygo.MenuItem, *mygo.Window) { a.showMainWindow() },
		},
		{
			Label: locales.BackupAllSources(),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.uiUpdate(func() { a.triggerSnapshotAction(nil) })
			},
		},
		mygo.Separator(),
		{
			Label: locales.Quit(),
			Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() },
		},
	})
}

func loadOrGenerateTrayIcon() ([]byte, bool) {
	if resDir, err := mygo.App.Path(mygo.PathResources); err == nil && resDir != "" {
		if runtime.GOOS == "darwin" {
			if b, err := os.ReadFile(filepath.Join(resDir, "tray-template.png")); err == nil {
				return b, true
			}
		}
		if b, err := os.ReadFile(filepath.Join(resDir, "tray.png")); err == nil {
			return b, false
		}
	}
	// Fallback crisp 36x36 monochrome template glyph in memory.
	img := image.NewNRGBA(image.Rect(0, 0, 36, 36))
	ink := color.NRGBA{R: 0, G: 0, B: 0, A: 235}
	for y := 6; y <= 30; y++ {
		for x := 6; x <= 30; x++ {
			border := x == 6 || x == 30 || y == 6 || y == 30 || y == 14
			center := x >= 14 && x <= 22 && y >= 20 && y <= 24
			if border || center {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes(), runtime.GOOS == "darwin"
}

func (a *App) repoStateFor(repoID string) (kopia.RepoSnapshotState, bool) {
	if a.manager != nil && repoID != "" {
		if st, ok := a.manager.State(repoID); ok {
			return st, true
		}
	}
	if a.seededStates != nil {
		if st, ok := a.seededStates[repoID]; ok {
			return st, true
		}
	}
	return kopia.RepoSnapshotState{}, false
}

// activeRepoState returns the current repository profile and cached server state.
func (a *App) activeRepoState() (config.RepoProfile, kopia.RepoSnapshotState) {
	var prof config.RepoProfile
	for _, p := range a.settings.Repositories {
		if p.ID == a.settings.ActiveRepoID {
			prof = p
			break
		}
	}
	if prof.ID == "" && len(a.settings.Repositories) > 0 {
		prof = a.settings.Repositories[0]
		a.settings.ActiveRepoID = prof.ID
	}
	if st, ok := a.repoStateFor(prof.ID); ok {
		if st.Profile.ID == "" {
			st.Profile = prof
		}
		if a.indexer != nil && prof.ID != "" {
			a.indexer.SetRepoRemote(prof.ID, isRemoteRepository(prof, st))
		}
		return prof, st
	}
	empty := kopia.RepoSnapshotState{Profile: prof}
	if a.indexer != nil && prof.ID != "" {
		a.indexer.SetRepoRemote(prof.ID, isRemoteRepository(prof, empty))
	}
	return prof, empty
}

func (a *App) removeRepoProfileFromList(repoID string) {
	if len(a.settings.Repositories) <= 1 {
		return
	}
	var kept []config.RepoProfile
	for _, r := range a.settings.Repositories {
		if r.ID != repoID {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		return
	}
	a.settings.Repositories = kept
	if a.manager != nil {
		a.manager.SetRcloneBinary(a.detectedRclone)
		a.manager.SyncProfiles(a.settings.Repositories)
	}
	a.indexer.RemoveRepo(repoID)
	if a.settings.ActiveRepoID == repoID {
		a.activateRepo(kept[0].ID)
	} else {
		_ = config.Save(a.userDataDir, a.settings)
	}
}

func (a *App) confirmRemoveRepoProfileAction(repoID, repoName string) {
	if len(a.settings.Repositories) <= 1 {
		return
	}
	go func() {
		if !a.confirmDestructive(locales.RemoveFromList()+": "+repoName+"?", locales.Remove()) {
			return
		}
		a.uiUpdate(func() {
			a.removeRepoProfileFromList(repoID)
		})
	}()
}

func (a *App) activateRepo(repoID string) {
	if a.refreshCancel != nil {
		a.refreshCancel()
		a.refreshCancel = nil
	}
	a.refreshing = false
	a.refreshSlow = false
	a.refreshPhase = ""
	a.refreshSeq++
	a.snapLoadSeq++
	a.browserLoadSeq++
	a.diffLoadSeq++
	a.diffCalcSeq++
	a.snapLoading = false
	a.snapLoadSlow = false
	a.browserLoading = false
	a.browserLoadSlow = false
	a.diffSnapLoading = false
	a.diffSnapSlow = false
	a.diffLoading = false
	a.diffCalcSlow = false

	a.settings.ActiveRepoID = repoID
	_ = config.Save(a.userDataDir, a.settings)
	a.forceOnboardingRepoID = ""
	a.repoActivating = false
	a.setSnapshotsSlice(nil)
	a.selectedSnapIdx = -1
	a.browserBreadcrumbs = nil
	a.setBrowserEntriesSlice(nil)
	a.diffSourceStr = ""
	a.diffSnapshots = nil
	a.diffBaseSnapID = ""
	a.diffTargetSnapID = ""
	a.diffActivePreset = 0
	a.diffActiveCalcKey = ""
	a.setDiffSummary(index.DiffSummary{})
	a.policyLoadedTarget = ""
	a.repoDescLoadedID = ""
	a.throttleLoadedID = ""
	a.maintLoadedID = ""
	a.searchDirty = true

	if a.manager == nil || repoID == "" {
		return
	}
	// Restore persisted catalog stats & search items in < 5ms from bbolt if already indexed on disk
	a.indexer.EnsureCatalogLoaded(repoID)
	seq := a.refreshSeq
	autoIndex := a.settings.AutoIndex
	a.repoActivating = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		cli, err := a.manager.EnsureStarted(ctx, repoID)
		a.uiUpdate(func() {
			if a.refreshSeq == seq {
				a.repoActivating = false
			}
		})
		if err != nil {
			return
		}
		a.preloadFirstSourceDefaults(ctx, repoID, cli)
		st, ok := a.manager.State(repoID)
		if ok && st.Connected() && autoIndex {
			if stats, indexed := a.indexer.Stats(repoID); !indexed || time.Since(stats.UpdatedAt) > 2*time.Minute {
				idxCtx, idxCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				_ = a.indexer.IndexRepo(idxCtx, repoID, st.Profile.Name, cli)
				idxCancel()
				a.uiUpdate(func() {
					if a.refreshSeq == seq {
						a.searchDirty = true
					}
				})
			}
		}
	}()
}

// preloadFirstSourceDefaults fetches the snapshot list for the first backup source right after connecting or fast-refreshing,
// so that both Snapshot Timeline and Snapshot Diff immediately have default selections (First Source + Latest vs Previous) ready.
func (a *App) preloadFirstSourceDefaults(ctx context.Context, repoID string, cli *kopia.Client) {
	if cli == nil || repoID == "" {
		return
	}
	st, ok := a.manager.State(repoID)
	if !ok || !st.Connected() {
		return
	}
	sources := st.SourceList()
	if len(sources) == 0 {
		return
	}
	firstSrc := sources[0].Source
	firstSrcStr := firstSrc.String()
	snaps, err := a.fetchSnapshotsCoalesced(ctx, repoID, firstSrc, true)
	if err != nil {
		return
	}
	a.uiUpdate(func() {
		if a.settings.ActiveRepoID != repoID {
			return
		}
		if a.snapSourceStr == "" {
			a.snapSourceStr = firstSrcStr
		}
		if len(a.snapshots) == 0 && a.snapSourceStr == firstSrcStr {
			if a.snapShowIdentical {
				a.setSnapshotsSlice(slices.Clone(snaps))
			} else {
				a.setSnapshotsSlice(filterUniqueRootSnapshots(snaps))
			}
			if len(a.snapshots) > 0 {
				a.selectedSnapIdx = 0
				a.openSnapshotRoot(a.snapshots[0])
			}
		}
		if a.diffSourceStr == "" {
			a.diffSourceStr = firstSrcStr
		}
		if len(a.diffSnapshots) == 0 && a.diffSourceStr == firstSrcStr {
			a.diffSnapshots = slices.Clone(snaps)
			if a.activeNav == navDiff {
				a.ensureDefaultDiffPair()
				a.runSnapshotDiffAsync()
			}
		}
	})
}

// activeRefreshStatus reports whether any >=0.3s estimated slow operation is running in the current repo,
// along with a concise localized status label for the top-right toolbar (preventing <0.3s spinner flashes).
func (a *App) activeRefreshStatus(prof config.RepoProfile, st kopia.RepoSnapshotState) (bool, string) {
	if a.refreshing && (a.refreshSlow || isRemoteRepository(prof, st)) {
		if a.refreshPhase != "" {
			return true, a.refreshPhase
		}
		return true, locales.Refreshing()
	}
	if a.diffLoading && a.diffCalcSlow {
		if n := len(a.diffSummary.Entries); n > 0 {
			return true, locales.ComparingItemsFound(n)
		}
		if a.diffTargetSnapID == diffTargetLocalDir {
			return true, locales.ComparingWithLocalFolder()
		}
		return true, locales.ComparingSnapshots()
	}
	if (a.diffSnapLoading && a.diffSnapSlow) || (a.snapLoading && a.snapLoadSlow) || (a.browserLoading && a.browserLoadSlow) {
		return true, locales.LoadingSnapshots()
	}
	if prof.ID != "" {
		if idxStats, ok := a.indexer.Stats(prof.ID); ok && idxStats.Indexing && idxStats.SlowOp {
			if idxStats.ProgressDirs > 0 {
				return true, locales.IndexingDirsProgress(int(idxStats.ProgressDirs))
			}
			return true, locales.Indexing()
		}
	}
	if st.Starting {
		return true, locales.Starting()
	}
	if a.isRepoActivelyConnecting(prof, st) && isRemoteRepository(prof, st) {
		return true, locales.ConnectingToRepository()
	}
	return false, ""
}

func (a *App) isRepoActivelyConnecting(prof config.RepoProfile, st kopia.RepoSnapshotState) bool {
	if a.manager == nil || st.Connected() || a.forceOnboardingRepoID == prof.ID || strings.TrimSpace(prof.StorageHint) == "" {
		return false
	}
	return a.repoActivating ||
		st.Starting ||
		(st.ServerRunning && st.Status == nil && st.LastError == "") ||
		(st.Status != nil && st.Status.InitRepoTaskID != "")
}

func (a *App) triggerManualRefresh() {
	// If a refresh is already running, cancel its stale context and restart cleanly.
	if a.refreshCancel != nil {
		a.refreshCancel()
		a.refreshCancel = nil
	}

	// Reset form-loaded markers and invalidate mutable snapshot/policy caches synchronously at refresh start.
	a.searchDirty = true
	a.policyLoadedTarget = ""
	a.repoDescLoadedID = ""
	a.throttleLoadedID = ""
	a.maintLoadedID = ""
	a.settings.Repositories = config.DiscoverProfiles(a.settings.Repositories, a.settings.EffectiveConfigDir())
	if a.manager != nil {
		a.manager.SetRcloneBinary(a.detectedRclone)
		a.manager.SyncProfiles(a.settings.Repositories)
	}

	repoID := a.settings.ActiveRepoID
	a.invalidateRepoSnapshotsCache(repoID)
	a.invalidateRepoPolicyCache(repoID)
	if a.manager == nil || repoID == "" {
		if a.activeNav == navDiff {
			a.runSnapshotDiffAsync()
		}
		a.toast(locales.Refreshed(), false)
		return
	}

	a.refreshSeq++
	seq := a.refreshSeq
	activeNav := a.activeNav
	selectedTaskID := a.selectedTaskID
	autoIndex := a.settings.AutoIndex
	prof, st := a.activeRepoState()
	a.refreshing = true
	a.refreshSlow = a.estimateManualRefreshSlow(prof, st, autoIndex || activeNav == navSearch)
	a.refreshPhase = locales.Refreshing()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	a.refreshCancel = cancel

	go func() {
		defer cancel()
		defer a.uiUpdate(func() {
			if a.refreshSeq == seq {
				a.refreshing = false
				a.refreshSlow = false
				a.refreshPhase = ""
				a.refreshCancel = nil
			}
		})

		cli, err := a.manager.EnsureStarted(ctx, repoID)
		if err != nil {
			if ctx.Err() == nil {
				a.toast(err.Error(), true)
			}
			return
		}

		// Phase 1 (Fast <100ms): Refresh local Kopia server state and reload active view once before remote blob sync.
		a.manager.RefreshRepo(ctx, repoID, false)
		if ctx.Err() != nil {
			return
		}
		a.preloadFirstSourceDefaults(ctx, repoID, cli)
		a.uiUpdate(func() {
			if a.refreshSeq != seq || a.settings.ActiveRepoID != repoID {
				return
			}
			if a.activeNav == navSnapshots {
				a.reloadSnapshotsAsync()
			} else if a.activeNav == navDiff {
				a.reloadDiffSnapshotsAsync()
			}
		})

		if activeNav == navTasks && selectedTaskID != "" {
			if logs, err := cli.GetTaskLogs(ctx, selectedTaskID); err == nil {
				a.uiUpdate(func() {
					if a.refreshSeq == seq && a.selectedTaskID == selectedTaskID {
						a.selectedTaskLogs = logs
					}
				})
			}
		}

		// Phase 2: Sync repository index blobs from storage & refresh maintenance/stats if needed
		prevSnapMarker := ""
		if stBefore, ok := a.manager.State(repoID); ok {
			for _, s := range stBefore.SourceList() {
				if s.LastSnapshot != nil {
					prevSnapMarker += s.Source.String() + ":" + s.LastSnapshot.ID + ";"
				}
			}
		}
		a.uiUpdate(func() {
			if a.refreshSeq == seq {
				a.refreshPhase = locales.SyncingRepository()
			}
		})
		_ = cli.SyncRepo(ctx)
		if ctx.Err() != nil {
			return
		}
		a.manager.RefreshRepo(ctx, repoID, activeNav == navRepo)
		if ctx.Err() != nil {
			return
		}
		nextSnapMarker := ""
		if stAfter, ok := a.manager.State(repoID); ok {
			for _, s := range stAfter.SourceList() {
				if s.LastSnapshot != nil {
					nextSnapMarker += s.Source.String() + ":" + s.LastSnapshot.ID + ";"
				}
			}
		}

		// Only trigger a second view reload if remote blob sync actually discovered new snapshots!
		if nextSnapMarker != prevSnapMarker {
			a.invalidateRepoSnapshotsCache(repoID)
			a.uiUpdate(func() {
				if a.refreshSeq != seq || a.settings.ActiveRepoID != repoID {
					return
				}
				if a.activeNav == navSnapshots {
					a.reloadSnapshotsAsync()
				} else if a.activeNav == navDiff {
					a.reloadDiffSnapshotsAsync()
				}
			})
		}

		// Phase 3: Incrementally sync Global Search DAG index (cached directory OIDs are skipped in O(1))
		if autoIndex || activeNav == navSearch {
			a.uiUpdate(func() {
				if a.refreshSeq == seq {
					a.refreshPhase = locales.Indexing()
				}
			})
			_ = a.indexer.IndexRepo(ctx, repoID, prof.Name, cli)
			a.uiUpdate(func() {
				if a.refreshSeq == seq {
					a.searchDirty = true
				}
			})
		}

		if ctx.Err() == nil {
			a.toast(locales.Refreshed(), false)
		}
	}()
}

// View builds the entire Mygopia immediate-mode interface.
func (a *App) View(c *ui.Context) {
	if !a.desktopMode.Load() {
		a.enterHeadlessUI()
		defer a.exitHeadlessUI()
	}
	// Settle active language, resolution-adaptive UI zoom, and theme before building widgets.
	setCurrentLanguage(ResolveLanguage(a.settings.Language, a.preferredLangs))
	c.SetZoom(ComputeEffectiveZoom(
		a.settings.UIScale,
		a.displayBounds.Width,
		a.displayBounds.Height,
		c.Preferences().TextScale,
	))

	var useDark bool
	switch a.settings.Theme {
	case config.ThemeDark:
		useDark = true
	case config.ThemeLight:
		useDark = false
	default:
		useDark = c.Theme().Dark
	}
	c.SetTheme(buildAppTheme(useDark, a.settings.AccentColor))

	if a.pendingToast != "" {
		if a.pendingToastErr {
			c.AddToast(ui.Toast{
				Title:   a.pendingToast,
				Type:    "error",
				Timeout: 5 * time.Second,
			})
		} else {
			c.Toast(a.pendingToast)
		}
		a.pendingToast = ""
		a.pendingToastErr = false
	}

	// Global keyboard shortcuts
	if c.Shortcut(ui.Cmd, ui.KeyB) {
		a.toggleSidebarCollapsed()
	}
	if c.Shortcut(ui.Cmd, ui.Key1) {
		a.switchNavTo(navBackups)
	}
	if c.Shortcut(ui.Cmd, ui.Key2) {
		a.switchNavTo(navSnapshots)
	}
	if c.Shortcut(ui.Cmd, ui.Key3) || c.Shortcut(ui.Cmd, ui.KeyF) || c.Shortcut(ui.Cmd, ui.KeyK) {
		a.switchNavTo(navSearch)
		a.focusSearchNextFrame = true
	}
	if c.Shortcut(ui.Cmd, ui.Key4) {
		a.switchNavTo(navDiff)
	}
	if c.Shortcut(ui.Cmd, ui.Key5) {
		a.switchNavTo(navPolicies)
	}
	if c.Shortcut(ui.Cmd, ui.Key6) {
		a.switchNavTo(navTasks)
	}
	if c.Shortcut(ui.Cmd, ui.Key7) {
		a.switchNavTo(navRepo)
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.switchNavTo(navSettings)
	}
	if c.Shortcut(ui.Cmd, ui.KeyR) {
		a.triggerManualRefresh()
	}
	if c.Shortcut(ui.Cmd, ui.KeyEqual) || c.Shortcut(ui.Cmd|ui.Shift, ui.KeyEqual) {
		a.stepUIScale(1)
	}
	if c.Shortcut(ui.Cmd, ui.KeyMinus) {
		a.stepUIScale(-1)
	}
	if c.Shortcut(ui.Cmd, ui.Key0) {
		a.stepUIScale(0)
	}

	// Keep secondary splitters within ergonomic bounds without snapping back.
	a.snapSplitWidth = min(max(a.snapSplitWidth, 220), 460)
	a.policySplitWidth = min(max(a.policySplitWidth, 172), 360)
	a.searchDetailSplit = min(max(a.searchDetailSplit, 45), 78)

	p := paletteOf(c)
	c.Root().Background(p.canvas)

	if a.settings.SidebarCollapsed {
		a.renderCollapsedShell(c)
	} else {
		adaptiveW := a.adaptiveSidebarWidth(c)
		if a.lastSidebarWidth > 0 && a.sidebarLiveWidth != a.lastSidebarWidth {
			var newCustom float32
			if math.Abs(float64(a.sidebarLiveWidth-adaptiveW)) < 1.5 {
				a.sidebarLiveWidth = adaptiveW
				newCustom = 0
			} else {
				clamped := min(max(a.sidebarLiveWidth, config.SidebarMinWidth), config.SidebarMaxWidth)
				a.sidebarLiveWidth = clamped
				newCustom = clamped
			}
			if a.settings.SidebarCustomWidth != newCustom {
				a.settings.SidebarCustomWidth = newCustom
				_ = config.Save(a.userDataDir, a.settings)
			}
		} else if a.settings.SidebarCustomWidth > 0 {
			a.sidebarLiveWidth = min(max(a.settings.SidebarCustomWidth, config.SidebarMinWidth), config.SidebarMaxWidth)
		} else {
			a.sidebarLiveWidth = adaptiveW
		}
		a.lastSidebarWidth = a.sidebarLiveWidth

		ui.Split(c, &a.sidebarLiveWidth, func() {
			a.renderSidebar(c)
		}, func() {
			a.renderMainPane(c)
		}).SplitReset(adaptiveW).Fill()
	}

	// Shared Modals
	a.renderConnectModal(c)
	a.renderAddSourceModal(c)
	a.renderSnapshotModals(c)
	a.renderPolicyModals(c)
}

func (a *App) toggleSidebarCollapsed() {
	a.settings.SidebarCollapsed = !a.settings.SidebarCollapsed
	_ = config.Save(a.userDataDir, a.settings)
}

func (a *App) switchNavTo(nav string) {
	if a.activeNav == nav {
		return
	}
	a.activeNav = nav
	if nav == navSnapshots && len(a.snapshots) == 0 {
		a.reloadSnapshotsAsync()
	}
	if nav == navDiff && len(a.diffSnapshots) == 0 {
		a.reloadDiffSnapshotsAsync()
	}
	if nav == navRepo && a.manager != nil && a.settings.ActiveRepoID != "" {
		go a.manager.RefreshMaintenanceAndStats(context.Background(), a.settings.ActiveRepoID)
	}
}

func allSectionTitles() []string {
	return []string{
		locales.NavBackups(),
		locales.NavSnapshots(),
		locales.NavSearch(),
		locales.NavSnapshotDiff(),
		locales.NavPolicies(),
		locales.NavTasksLogs(),
		locales.NavRepository(),
		locales.NavSettings(),
	}
}

// sectionTitleSlotWidth computes a constant pixel-exact slot width for the top toolbar's
// section title across all 8 views in the active language, preventing horizontal jitter
// of the adjacent repository name & status badge when switching tabs.
func sectionTitleSlotWidth(c *ui.Context) float32 {
	var maxW float32 = 64
	for _, title := range allSectionTitles() {
		w, _ := c.MeasureText(0, ui.Span{Text: title, Size: 15, Weight: 600})
		if w > maxW {
			maxW = w
		}
	}
	return float32(math.Ceil(float64(maxW))) + 4
}

func modShortcutHint(key string) string {
	if runtime.GOOS == "darwin" {
		return " (⌘" + key + ")"
	}
	return " (Ctrl+" + key + ")"
}

func (a *App) adaptiveSidebarWidth(c *ui.Context) float32 {
	w := float32(162)
	for _, title := range allSectionTitles() {
		tw, _ := c.MeasureText(0, ui.Span{Text: title, Size: 13, Weight: 500})
		need := tw + 74 // 8 left pad + 36 icon slot + label + optional badge + 10 right pad
		if need > w {
			w = need
		}
	}
	prof, _ := a.activeRepoState()
	if prof.Name != "" {
		tw, _ := c.MeasureText(0, ui.Span{Text: prof.Name, Size: 12.5, Weight: 600})
		need := tw + 108 // 8 pad + 36 icon + name + gap + chevron + pad + gap + 30 [+] btn + 8 pad
		if need > w {
			w = min(need, 216)
		}
	}
	return min(max(float32(math.Ceil(float64(w))), config.SidebarMinWidth), 240)
}

func (a *App) renderSidebar(c *ui.Context) {
	p := paletteOf(c)
	bar := c.TitleBar()

	ui.Column(c).
		Fill().
		Background(p.sidebarBg).
		Border(0, p.border).
		Children(func() {
			// Window traffic-light aligned header row (matches main toolbar midline & divider)
			topH := topBarHeight(c)
			leftPad := float32(12)
			if bar.Left > 0 {
				leftPad = bar.Left + 8
			}
			ui.Row(c).
				Height(topH).
				Shrink(0).
				Padding(0, 10, 0, leftPad).
				AlignItems(ui.Center).
				DragWindow().
				Children(func() {
					ui.Text(c, "Mygopia").FontSize(13.5).FontWeight(600).TextColor(p.text).SingleLine()
					ui.Spacer(c)
				})

			ui.Box(c).Height(1).Background(p.border).Shrink(0)

			a.renderSidebarNavBody(c, false)
		})
}

func (a *App) renderCollapsedShell(c *ui.Context) {
	p := paletteOf(c)
	prof, st := a.activeRepoState()

	ui.Column(c).Fill().Background(p.canvas).Children(func() {
		a.renderTopToolbar(c, prof, st, true)
		ui.Box(c).Height(1).Background(p.border).Shrink(0)
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.renderSidebarNavBody(c, true)
			ui.Box(c).Width(1).Background(p.border).Shrink(0)
			a.renderRouteBody(c, prof, st)
		})
	})
}

// renderSidebarNavBody renders both the expanded sidebar body and the collapsed 52px icon rail
// using an identical vertical & horizontal grid (Padding(10, 8), Gap(6), 36x36 left icon slots),
// guaranteeing 0.0px icon position shift when toggling collapsed/expanded and pinning Settings to the bottom.
func (a *App) renderSidebarNavBody(c *ui.Context, collapsed bool) {
	p := paletteOf(c)
	prof, activeState := a.activeRepoState()

	col := ui.Column(c).
		Background(p.sidebarBg).
		Padding(10, 8).
		Gap(6).
		AlignItems(ui.Stretch)
	if collapsed {
		col.Width(sidebarRailWidth).Shrink(0)
	} else {
		col.Grow(1).MinHeight(0)
	}

	col.Children(func() {
		// 1. Active Repository Switcher Row (Height 36, 36x36 icon slot at X=8)
		dotColor := p.muted
		if activeState.Connected() {
			dotColor = c.Theme().Success
		} else if activeState.Starting || a.isRepoActivelyConnecting(prof, activeState) {
			dotColor = c.Theme().Warning
		}
		repoTip := locales.Repositories()
		if prof.Name != "" && prof.StorageHint != "" {
			repoTip = prof.Name + " · " + prof.StorageHint
		} else if prof.Name != "" {
			repoTip = prof.Name
		}

		attachRepoMenus := func(btn ui.Element) {
			btn.Menu(func(m *ui.Menu) {
				for _, rp := range a.settings.Repositories {
					repoItem := rp
					isCurrent := repoItem.ID == a.settings.ActiveRepoID
					if m.Item(repoItem.Name).ID(repoItem.ID).Checked(isCurrent).Chosen() && !isCurrent {
						a.activateRepo(repoItem.ID)
					}
				}
				if len(a.settings.Repositories) > 0 {
					m.Separator()
				}
				if m.Item(locales.AddRepository()).Chosen() {
					a.openConnectWizard(true)
				}
				if len(a.settings.Repositories) > 1 && prof.ID != "" {
					m.Separator()
					if m.Item(locales.RemoveFromList()).Chosen() {
						a.confirmRemoveRepoProfileAction(prof.ID, prof.Name)
					}
				}
			})
			if len(a.settings.Repositories) > 1 && prof.ID != "" {
				btn.ContextMenu(func(m *ui.Menu) {
					if m.Item(locales.RemoveFromList()).Chosen() {
						a.confirmRemoveRepoProfileAction(prof.ID, prof.Name)
					}
				})
			}
		}

		renderRepoIconSlot := func() {
			ui.Row(c).Size(36, 36).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Icon(c.Key("sidebar-icon-repo-top"), iconRepo).FontSize(16.5).TextColor(p.muted)
				ui.Box(c).Absolute().Right(6).Bottom(6).Size(6, 6).Radius(99).Background(dotColor)
			})
		}

		if collapsed {
			repoBtn := ui.ButtonBase(c.Key("sidebar-repo-switcher")).
				Height(36).
				Shrink(0).
				Padding(0).
				Radius(8).
				AlignItems(ui.Center).
				Background(ui.Transparent).
				Label(locales.Repositories())
			tooltipRight(c, repoBtn, repoTip)
			if repoBtn.Hovered() {
				repoBtn.Background(c.Theme().SurfaceHover)
			}
			repoBtn.Children(renderRepoIconSlot)
			attachRepoMenus(repoBtn)
		} else {
			ui.Row(c).Height(36).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
				repoBtn := ui.ButtonBase(c.Key("sidebar-repo-switcher")).
					Grow(1).
					MinWidth(0).
					Height(36).
					Padding(0).
					Radius(8).
					AlignItems(ui.Center).
					Background(p.cardBg).
					Label(locales.Repositories())
				tooltipBelow(c, repoBtn, repoTip)
				if repoBtn.Hovered() {
					repoBtn.Background(c.Theme().SurfaceHover)
				}
				repoBtn.Children(func() {
					renderRepoIconSlot()
					dispName := prof.Name
					if dispName == "" {
						dispName = locales.Repositories()
					}
					ui.Text(c, dispName).FontSize(12.5).FontWeight(600).Grow(1).SingleLine()
					ui.Box(c).Width(4).Shrink(0)
					ui.Text(c, "▾").FontSize(10).TextColor(p.muted).Shrink(0)
					ui.Box(c).Width(8).Shrink(0)
				})
				attachRepoMenus(repoBtn)

				addRepoBtn := ui.Button(c, "").
					Label(locales.AddRepository()).
					Size(30, 30).
					Shrink(0).
					Padding(0).
					Radius(7).
					Children(func() {
						ui.Icon(c, iconPlus).FontSize(13)
					})
				tooltipBelow(c, addRepoBtn, locales.AddRepository())
				if addRepoBtn.Clicked() {
					a.openConnectWizard(true)
				}
			})
		}

		// 2. Top Divider (identical vertical footprint in both collapsed & expanded states)
		ui.Box(c).Height(1).Margin(2, 4).Background(p.borderSoft).Shrink(0)

		// 3. Shared Navigation Item Row Builder (Height 36, 36x36 left icon slot at X=8)
		navItem := func(navID string, icon *ui.SVG, label string, badgeText string, badgeAccent bool) {
			chosen := a.activeNav == navID
			bg := ui.Transparent
			if chosen {
				bg = c.Theme().SurfacePressed
			}
			btn := ui.ButtonBase(c.Key("nav-" + navID)).
				Height(36).
				Shrink(0).
				Padding(0).
				Radius(8).
				AlignItems(ui.Center).
				Background(bg).
				Label(label)
			if !chosen && btn.Hovered() {
				btn.Background(c.Theme().SurfaceHover)
			}
			if collapsed {
				tooltipRight(c, btn, label)
			}
			btn.Children(func() {
				ui.Row(c).Size(36, 36).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c.Key("nav-icon-"+navID), icon).FontSize(16.5).TextColor(p.accent)
					if collapsed && badgeAccent && badgeText != "" {
						ui.Box(c).Absolute().Right(6).Top(6).Size(6, 6).Radius(99).Background(c.Theme().Accent)
					}
				})
				if !collapsed {
					lblEl := ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(p.text).Grow(1).SingleLine()
					if chosen {
						lblEl.FontWeight(600)
					}
					if badgeText != "" {
						b := ui.Badge(c, badgeText)
						if badgeAccent {
							b.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
						}
					}
					ui.Box(c).Width(10).Shrink(0)
				}
			})
			if btn.Clicked() {
				a.switchNavTo(navID)
			}
		}

		var srcBadge string
		if n := len(activeState.SourceList()); n > 0 {
			srcBadge = intToStr(n)
		}
		var taskBadge string
		if active := activeState.ActiveTaskCount(); active > 0 {
			taskBadge = intToStr(active)
		}

		navItem(navBackups, iconBackups, locales.NavBackups(), srcBadge, false)
		navItem(navSnapshots, iconSnapshots, locales.NavSnapshots(), "", false)
		navItem(navSearch, iconSearch, locales.NavSearch(), "", false)
		navItem(navDiff, iconDiff, locales.NavSnapshotDiff(), "", false)
		navItem(navPolicies, iconPolicies, locales.NavPolicies(), "", false)
		navItem(navTasks, iconTasks, locales.NavTasksLogs(), taskBadge, true)
		navItem(navRepo, iconRepo, locales.NavRepository(), "", false)

		// 4. Flexible Spacer + Bottom Divider + Bottom-Pinned Settings (identical in both states)
		ui.Spacer(c)
		ui.Box(c).Height(1).Margin(2, 4).Background(p.borderSoft).Shrink(0)
		navItem(navSettings, iconSettings, locales.NavSettings(), "", false)
	})
}

func (a *App) renderMainPane(c *ui.Context) {
	p := paletteOf(c)
	prof, st := a.activeRepoState()

	ui.Column(c).Fill().Background(p.canvas).Children(func() {
		a.renderTopToolbar(c, prof, st, false)
		ui.Box(c).Height(1).Background(p.border).Shrink(0)
		a.renderRouteBody(c, prof, st)
	})
}

func (a *App) renderTopToolbar(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState, fullWindowSpan bool) {
	p := paletteOf(c)
	bar := c.TitleBar()
	topH := topBarHeight(c)
	leftPad := float32(14)
	if fullWindowSpan && bar.Left > 0 {
		leftPad = bar.Left + 10
	}
	rightPad := float32(14)
	if bar.Right > 0 {
		rightPad = bar.Right + 10
	}
	toggleTip := locales.CollapseSidebar()
	if a.settings.SidebarCollapsed {
		toggleTip = locales.ExpandSidebar()
	}

	ui.Row(c).
		Height(topH).
		Shrink(0).
		Padding(0, rightPad, 0, leftPad).
		Gap(10).
		AlignItems(ui.Center).
		Background(p.canvas).
		DragWindow().
		Children(func() {
			toggleBtn := ui.Button(c, "").Label(locales.ToggleSidebar()).Padding(5, 7).Children(func() {
				ui.Icon(c, iconSidebar).FontSize(15)
			})
			tooltipBelow(c, toggleBtn, toggleTip+modShortcutHint("B"))
			if toggleBtn.Clicked() {
				a.toggleSidebarCollapsed()
			}

			ui.Row(c).Width(sectionTitleSlotWidth(c)).Shrink(0).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.currentSectionTitle()).FontSize(15).FontWeight(600).SingleLine()
			})

			if prof.Name != "" {
				ui.Text(c, "·").TextColor(p.muted)
				ui.Text(c, prof.Name).FontSize(12.5).TextColor(p.muted).SingleLine()
			}

			if st.Starting {
				statusPill(c, locales.Starting(), c.Theme().Warning, p.warningSoft)
			} else if a.isRepoActivelyConnecting(prof, st) {
				statusPill(c, locales.ConnectingToRepository(), c.Theme().Warning, p.warningSoft)
			} else if st.Connected() {
				statusPill(c, locales.Connected(), c.Theme().Success, p.successSoft)
			} else if a.manager != nil {
				statusPill(c, locales.Disconnected(), p.muted, p.subtleBg)
			}

			ui.Spacer(c)

			spinning, spinStatus := a.activeRefreshStatus(prof, st)
			if spinning && spinStatus != "" && spinStatus != locales.ConnectingToRepository() {
				statusPill(c, spinStatus, c.Theme().Accent, p.accentSoft)
			}

			refreshTip := locales.Refresh() + modShortcutHint("R")
			if spinning && spinStatus != "" {
				refreshTip = spinStatus + modShortcutHint("R")
			}
			refreshBtn := ui.Button(c.Key("toolbar-refresh-btn"), "").
				Label(locales.Refresh()).
				Padding(5, 7)
			tooltipBelow(c, refreshBtn, refreshTip)
			refreshBtn.Children(func() {
				ic := ui.Icon(c.Key("toolbar-refresh-icon"), iconRefresh).FontSize(14)
				if spinning {
					deg := ic.Loop("spin", 850*time.Millisecond, ui.Linear) * 360
					ic.Transform(ui.Rotation(deg)).TextColor(c.Theme().Accent)
				}
			})
			if refreshBtn.Clicked() {
				a.triggerManualRefresh()
			}
		})
}

func (a *App) renderRouteBody(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		switch a.activeNav {
		case navSettings:
			a.renderSettingsView(c)
		case navSearch:
			a.renderSearchView(c, prof, st)
		case navTasks:
			a.renderTasksView(c, prof, st)
		default:
			if st.Connected() && a.forceOnboardingRepoID == prof.ID {
				a.forceOnboardingRepoID = ""
			}
			if !st.Connected() && a.manager != nil {
				if strings.TrimSpace(prof.StorageHint) != "" && a.forceOnboardingRepoID != prof.ID {
					a.renderRepoConnecting(c, prof, st)
				} else {
					a.renderRepoOnboarding(c, prof, st)
				}
				return
			}
			switch a.activeNav {
			case navBackups:
				a.renderBackupsView(c, prof, st)
			case navSnapshots:
				a.renderSnapshotsView(c, prof, st)
			case navDiff:
				a.renderDiffView(c, prof, st)
			case navPolicies:
				a.renderPoliciesView(c, prof, st)
			case navRepo:
				a.renderRepoView(c, prof, st)
			default:
				a.renderBackupsView(c, prof, st)
			}
		}
	})
}

func (a *App) currentSectionTitle() string {
	switch a.activeNav {
	case navBackups:
		return locales.NavBackups()
	case navSnapshots:
		return locales.NavSnapshots()
	case navSearch:
		return locales.NavSearch()
	case navDiff:
		return locales.NavSnapshotDiff()
	case navPolicies:
		return locales.NavPolicies()
	case navTasks:
		return locales.NavTasksLogs()
	case navRepo:
		return locales.NavRepository()
	case navSettings:
		return locales.NavSettings()
	default:
		return "Mygopia"
	}
}

func shortenMiddle(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes || maxRunes < 8 {
		return s
	}
	half := (maxRunes - 1) / 2
	return string(r[:half]) + "…" + string(r[len(r)-half:])
}

func intToStr(n int) string {
	return strconv.Itoa(n)
}

func trimLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
