package kopia

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"mygopia/internal/config"
)

// RepoSnapshotState is an immutable, UI-friendly snapshot of a repository's runtime state.
type RepoSnapshotState struct {
	Profile       config.RepoProfile
	ServerRunning bool
	ServerAddress string
	Starting      bool
	LastError     string
	LastUpdated   time.Time

	Status               *StatusResponse
	Algorithms           *SupportedAlgorithmsResponse
	Sources              *SourcesResponse
	Policies             []*PolicyListEntry
	Tasks                []TaskInfo
	Mounts               []*MountedSnapshot
	Throttle             *ThrottleLimits
	NotificationProfiles []NotificationProfile
	Maintenance          *MaintenanceInfo
	ContentStats         *ContentStats
	ServerLogs           []string
}

func (st RepoSnapshotState) Connected() bool {
	return st.Status != nil && st.Status.Connected
}

func (st RepoSnapshotState) ActiveTaskCount() int {
	n := 0
	for _, t := range st.Tasks {
		if t.IsRunning() {
			n++
		}
	}
	for _, src := range st.SourceList() {
		if src.Status == "UPLOADING" || src.Status == "PENDING" {
			n++
		}
	}
	return n
}

func (st RepoSnapshotState) SourceList() []*SourceStatus {
	if st.Sources == nil {
		return nil
	}
	return st.Sources.Sources
}

type managedRepo struct {
	profile config.RepoProfile
	runner  *ServerRunner
	cli     CLIHelper

	mu    sync.RWMutex
	state RepoSnapshotState
}

// Manager orchestrates local kopia servers and background state refreshes across one or more repositories.
type Manager struct {
	mu        sync.RWMutex
	kopiaBin  string
	rcloneBin string
	logDir    string
	repos     map[string]*managedRepo
	order     []string
	onChange  func()
	onNotify  func(repoName string, payload DesktopNotificationPayload)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager creates a multi-repository Kopia manager.
func NewManager(kopiaBin, logDir string, onChange func(), onNotify func(string, DesktopNotificationPayload)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		kopiaBin: kopiaBin,
		logDir:   logDir,
		repos:    make(map[string]*managedRepo),
		onChange: onChange,
		onNotify: onNotify,
		ctx:      ctx,
		cancel:   cancel,
	}
	m.wg.Add(1)
	go m.pollLoop()
	return m
}

// SetKopiaBinary updates the kopia binary path for future server starts.
func (m *Manager) SetKopiaBinary(bin string) {
	m.mu.Lock()
	m.kopiaBin = bin
	for _, mr := range m.repos {
		mr.mu.Lock()
		mr.cli.KopiaBin = bin
		mr.mu.Unlock()
		mr.runner.SetKopiaBinary(bin)
	}
	m.mu.Unlock()
}

// SetRcloneBinary updates the optional rclone binary path for future server/CLI starts.
func (m *Manager) SetRcloneBinary(bin string) {
	m.mu.Lock()
	m.rcloneBin = bin
	for _, mr := range m.repos {
		mr.mu.Lock()
		mr.cli.RcloneBin = bin
		mr.mu.Unlock()
		mr.runner.SetRcloneBinary(bin)
	}
	m.mu.Unlock()
}

// SyncProfiles registers/updates the set of repository profiles managed by the app.
func (m *Manager) SyncProfiles(profiles []config.RepoProfile) {
	m.mu.Lock()
	newOrder := make([]string, 0, len(profiles))
	keep := make(map[string]bool, len(profiles))
	var toStop []*ServerRunner

	newRunnerFor := func(mr *managedRepo, cfgFile string) *ServerRunner {
		r := NewServerRunner(m.kopiaBin, cfgFile, m.logDir, func(n DesktopNotificationPayload) {
			if m.onNotify != nil {
				mr.mu.RLock()
				repoName := mr.profile.Name
				mr.mu.RUnlock()
				m.onNotify(repoName, n)
			}
		})
		r.SetRcloneBinary(m.rcloneBin)
		return r
	}

	for _, p := range profiles {
		keep[p.ID] = true
		newOrder = append(newOrder, p.ID)
		if existing, ok := m.repos[p.ID]; ok {
			existing.mu.Lock()
			cfgChanged := existing.profile.ConfigFile != p.ConfigFile
			existing.profile = p
			existing.state.Profile = p
			existing.cli.ConfigFile = p.ConfigFile
			existing.mu.Unlock()
			if cfgChanged {
				toStop = append(toStop, existing.runner)
				existing.runner = newRunnerFor(existing, p.ConfigFile)
			}
			continue
		}
		mr := &managedRepo{
			profile: p,
			cli:     CLIHelper{KopiaBin: m.kopiaBin, RcloneBin: m.rcloneBin, ConfigFile: p.ConfigFile},
			state: RepoSnapshotState{
				Profile: p,
			},
		}
		mr.runner = newRunnerFor(mr, p.ConfigFile)
		m.repos[p.ID] = mr
	}

	for id, mr := range m.repos {
		if !keep[id] {
			toStop = append(toStop, mr.runner)
			delete(m.repos, id)
		}
	}
	m.order = newOrder
	m.mu.Unlock()

	for _, r := range toStop {
		r.Stop()
	}
}

// EnsureStarted starts the kopia server for repoID if not already running and refreshes its state.
func (m *Manager) EnsureStarted(ctx context.Context, repoID string) (*Client, error) {
	mr := m.getManaged(repoID)
	if mr == nil {
		return nil, fmt.Errorf("unknown repository %q", repoID)
	}
	m.mu.RLock()
	bin := m.kopiaBin
	m.mu.RUnlock()
	if bin == "" {
		err := fmt.Errorf("kopia executable not found")
		mr.mu.Lock()
		mr.state.LastError = err.Error()
		mr.mu.Unlock()
		m.notifyChange()
		return nil, err
	}

	if cli := mr.runner.Client(); cli != nil && mr.runner.Running() {
		return cli, nil
	}

	mr.mu.Lock()
	mr.state.Starting = true
	mr.state.LastError = ""
	mr.mu.Unlock()
	mr.runner.SetKopiaBinary(bin)
	m.notifyChange()

	cli, err := mr.runner.Start(ctx)
	mr.mu.Lock()
	mr.state.Starting = false
	mr.state.ServerRunning = mr.runner.Running()
	mr.state.ServerAddress = mr.runner.Address()
	mr.state.ServerLogs = mr.runner.Logs()
	if err != nil {
		mr.state.LastError = err.Error()
	}
	mr.mu.Unlock()

	if err != nil {
		m.notifyChange()
		return nil, err
	}

	m.RefreshRepo(ctx, repoID, false)
	go m.RefreshMaintenanceAndStats(m.ctx, repoID)
	return cli, nil
}

// ClientFor returns an active Client for repoID, starting its server if necessary.
func (m *Manager) ClientFor(ctx context.Context, repoID string) (*Client, error) {
	return m.EnsureStarted(ctx, repoID)
}

// CLIFor returns a CLIHelper for repoID.
func (m *Manager) CLIFor(repoID string) (CLIHelper, bool) {
	mr := m.getManaged(repoID)
	if mr == nil {
		return CLIHelper{}, false
	}
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	return mr.cli, true
}

// State returns a snapshot of repoID's cached state for immediate-mode rendering.
func (m *Manager) State(repoID string) (RepoSnapshotState, bool) {
	mr := m.getManaged(repoID)
	if mr == nil {
		return RepoSnapshotState{}, false
	}
	mr.mu.RLock()
	st := mr.state
	mr.mu.RUnlock()
	st.ServerRunning = mr.runner.Running()
	st.ServerAddress = mr.runner.Address()
	st.ServerLogs = mr.runner.Logs()
	return st, true
}

// AllStates returns cached states for all configured repositories in display order.
func (m *Manager) AllStates() []RepoSnapshotState {
	m.mu.RLock()
	ids := make([]string, len(m.order))
	copy(ids, m.order)
	m.mu.RUnlock()

	out := make([]RepoSnapshotState, 0, len(ids))
	for _, id := range ids {
		if st, ok := m.State(id); ok {
			out = append(out, st)
		}
	}
	return out
}

// RefreshRepo queries the Kopia server API and updates the cached state for repoID.
func (m *Manager) RefreshRepo(ctx context.Context, repoID string, includeHeavy bool) {
	mr := m.getManaged(repoID)
	if mr == nil {
		return
	}
	cli := mr.runner.Client()
	if cli == nil || !mr.runner.Running() {
		mr.mu.Lock()
		mr.state.ServerRunning = false
		mr.state.ServerLogs = mr.runner.Logs()
		mr.mu.Unlock()
		return
	}

	timeout := 12 * time.Second
	if includeHeavy {
		timeout = 45 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	status, err := cli.Status(rctx)
	if err != nil {
		mr.mu.Lock()
		mr.state.LastError = err.Error()
		mr.state.ServerRunning = mr.runner.Running()
		mr.state.ServerLogs = mr.runner.Logs()
		mr.mu.Unlock()
		m.notifyChange()
		return
	}

	tasks, _ := cli.ListTasks(rctx)

	mr.mu.RLock()
	needAlgos := mr.state.Algorithms == nil
	mr.mu.RUnlock()

	var algos *SupportedAlgorithmsResponse
	if needAlgos {
		algos, _ = cli.SupportedAlgorithms(rctx)
	}

	var (
		sources  *SourcesResponse
		policies []*PolicyListEntry
		mounts   []*MountedSnapshot
		throttle *ThrottleLimits
		profiles []NotificationProfile
		maint    *MaintenanceInfo
		cstats   *ContentStats
	)

	if status.Connected {
		sources, _ = cli.ListSources(rctx)
		if sources != nil && len(sources.Sources) > 1 {
			slices.SortFunc(sources.Sources, func(a, b *SourceStatus) int {
				return strings.Compare(a.Source.String(), b.Source.String())
			})
		}
		policies, _ = cli.ListPolicies(rctx)
		mounts, _ = cli.ListMounts(rctx)
		if includeHeavy {
			mr.mu.RLock()
			cliHelper := mr.cli
			mr.mu.RUnlock()
			var heavyWG sync.WaitGroup
			heavyWG.Add(4)
			go func() {
				defer heavyWG.Done()
				throttle, _ = cli.GetThrottle(rctx)
			}()
			go func() {
				defer heavyWG.Done()
				profiles, _ = cli.ListNotificationProfiles(rctx)
			}()
			go func() {
				defer heavyWG.Done()
				maint, _ = cliHelper.GetMaintenanceInfo(rctx)
			}()
			go func() {
				defer heavyWG.Done()
				cstats, _ = cliHelper.GetContentStats(rctx)
			}()
			heavyWG.Wait()
		}
	}

	mr.mu.Lock()
	mr.state.ServerRunning = mr.runner.Running()
	mr.state.ServerAddress = mr.runner.Address()
	mr.state.ServerLogs = mr.runner.Logs()
	mr.state.Status = status
	mr.state.Tasks = tasks
	mr.state.LastError = ""
	mr.state.LastUpdated = time.Now()
	if algos != nil {
		mr.state.Algorithms = algos
	}
	if status.Connected {
		if sources != nil {
			mr.state.Sources = sources
		}
		if policies != nil {
			mr.state.Policies = policies
		}
		mr.state.Mounts = mounts
		if includeHeavy {
			if throttle != nil {
				mr.state.Throttle = throttle
			}
			mr.state.NotificationProfiles = profiles
			if maint != nil {
				mr.state.Maintenance = maint
			}
			if cstats != nil {
				mr.state.ContentStats = cstats
			}
		}
	} else {
		mr.state.Sources = nil
		mr.state.Policies = nil
		mr.state.Mounts = nil
	}
	mr.mu.Unlock()
	m.notifyChange()
}

// RefreshMaintenanceAndStats refreshes CLI-sourced maintenance info and deduplication stats concurrently.
func (m *Manager) RefreshMaintenanceAndStats(ctx context.Context, repoID string) {
	mr := m.getManaged(repoID)
	if mr == nil {
		return
	}
	mr.mu.RLock()
	cliHelper := mr.cli
	mr.mu.RUnlock()
	var (
		maint  *MaintenanceInfo
		cstats *ContentStats
		wg     sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		maint, _ = cliHelper.GetMaintenanceInfo(ctx)
	}()
	go func() {
		defer wg.Done()
		cstats, _ = cliHelper.GetContentStats(ctx)
	}()
	wg.Wait()
	mr.mu.Lock()
	if maint != nil {
		mr.state.Maintenance = maint
	}
	if cstats != nil {
		mr.state.ContentStats = cstats
	}
	mr.mu.Unlock()
	m.notifyChange()
}

// StopRepo stops the kopia server for a single repository.
func (m *Manager) StopRepo(repoID string) {
	mr := m.getManaged(repoID)
	if mr == nil {
		return
	}
	mr.runner.Stop()
	mr.mu.Lock()
	mr.state.ServerRunning = false
	mr.state.Status = nil
	mr.mu.Unlock()
	m.notifyChange()
}

// Shutdown stops background polling and cleanly terminates every spawned kopia server.
func (m *Manager) Shutdown() {
	m.cancel()
	m.wg.Wait()

	m.mu.RLock()
	runners := make([]*ServerRunner, 0, len(m.repos))
	for _, mr := range m.repos {
		runners = append(runners, mr.runner)
	}
	m.mu.RUnlock()

	var stopWG sync.WaitGroup
	for _, r := range runners {
		stopWG.Add(1)
		go func(sr *ServerRunner) {
			defer stopWG.Done()
			sr.Stop()
		}(r)
	}
	stopWG.Wait()
}

func (m *Manager) pollLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	ticks := 0
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			ticks++
			m.mu.RLock()
			ids := make([]string, len(m.order))
			copy(ids, m.order)
			m.mu.RUnlock()

			for _, id := range ids {
				mr := m.getManaged(id)
				if mr == nil || !mr.runner.Running() {
					continue
				}
				mr.mu.RLock()
				busy := mr.state.ActiveTaskCount() > 0 || (mr.state.Status != nil && mr.state.Status.InitRepoTaskID != "")
				mr.mu.RUnlock()
				if busy || ticks%3 == 0 {
					m.RefreshRepo(m.ctx, id, false)
				}
			}
		}
	}
}

func (m *Manager) getManaged(repoID string) *managedRepo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.repos[repoID]
}

func (m *Manager) notifyChange() {
	if m.onChange != nil {
		m.onChange()
	}
}
