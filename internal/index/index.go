// @scope.file index.engine
// @hash sha256:1aea8ec7725d31ae
// @implements spec.dag_index sha256:c155f3ed2269ad94
// @ref default github.com/hashicorp/golang-lru/v2@2.0.7
// @ref default config.store sha256:b49642aa028d360a
// @ref default index.store sha256:a2c92662d97f482b
// @ref default kopia.client sha256:c1253141ff6ec455
// @ref default kopia.types sha256:06dd81559d688041
// Incremental content-addressed Kopia snapshot DAG indexer with bounded L1 LRU, bbolt+S2 persistence, and live streaming.

package index

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
)

const (
	indexSchemaVersion   = 2
	maxWalkDepth         = 256
	maxExpandedEntries   = 5_000_000
	l1DirCacheMaxEntries = 768
	l1DiffCacheMaxPairs  = 20
	l1DiffMaxItemCount   = 25_000
)

// CachedEntry is the compact immutable representation of a DirEntry stored in the local DAG cache.
type CachedEntry struct {
	Name      string
	IsDir     bool
	IsSymlink bool
	Size      int64
	ModUnix   int64
	ObjectID  string
}

// ToKopiaDirEntries converts compact CachedEntry values back into Kopia DirEntry pointers for zero-RPC Snapshot browsing.
func ToKopiaDirEntries(cached []CachedEntry) []*kopia.DirEntry {
	out := make([]*kopia.DirEntry, len(cached))
	for i, ce := range cached {
		typ := "f"
		var summ *kopia.DirectorySummary
		var fsize int64
		if ce.IsDir {
			typ = "d"
			summ = &kopia.DirectorySummary{TotalFileSize: ce.Size}
		} else {
			if ce.IsSymlink {
				typ = "s"
			}
			fsize = ce.Size
		}
		var modT kopia.FlexTime
		if ce.ModUnix != 0 {
			modT = kopia.FlexTime{Time: time.Unix(0, ce.ModUnix)}
		}
		out[i] = &kopia.DirEntry{
			Name:       ce.Name,
			Type:       typ,
			FileSize:   fsize,
			ModTime:    modT,
			ObjectID:   ce.ObjectID,
			DirSummary: summ,
		}
	}
	return out
}

// FromKopiaDirEntries converts Kopia REST API DirEntry pointers into compact immutable CachedEntry values.
func FromKopiaDirEntries(entries []*kopia.DirEntry) []CachedEntry {
	compact := make([]CachedEntry, 0, len(entries))
	for _, de := range entries {
		if de == nil || de.Name == "" {
			continue
		}
		var modNano int64
		if !de.ModTime.IsZero() {
			modNano = de.ModTime.UnixNano()
		}
		compact = append(compact, CachedEntry{
			Name:      de.Name,
			IsDir:     de.IsDir(),
			IsSymlink: de.Type == "s",
			Size:      de.LogicalSize(),
			ModUnix:   modNano,
			ObjectID:  de.ObjectID,
		})
	}
	return compact
}

type diskRepoCache struct {
	Version  int
	RepoID   string
	Updated  time.Time
	DirCache map[string][]CachedEntry
}

// VersionRecord represents one point-in-time snapshot occurrence of a file or directory.
type VersionRecord struct {
	SnapshotID   string
	SnapshotTime time.Time
	RootID       string
	ObjectID     string
	Size         int64
	ModTime      time.Time
}

// SearchItem is an aggregated file or folder across all snapshots of a backup source.
type SearchItem struct {
	RepoID           string
	RepoName         string
	Source           kopia.SourceInfo
	Path             string // slash-separated path relative to source root
	Name             string
	Ext              string // lowercase extension without dot
	IsDir            bool
	OnDisk           bool // true if present in the source's latest snapshot
	LatestSize       int64
	MaxSize          int64
	Versions         int // count of distinct content ObjectIDs
	SnapshotCount    int
	FirstSeen        time.Time
	LastSeen         time.Time
	LatestObjectID   string
	LatestSnapshotID string
	LatestRootID     string
	History          []VersionRecord // populated inline for seeded tests; loaded on demand from bbolt in production
}

// FullHostPath returns the reconstructed path under Source.Path.
func (it SearchItem) FullHostPath() string {
	if it.Source.Path == "" {
		return it.Path
	}
	return filepath.Join(it.Source.Path, filepath.FromSlash(it.Path))
}

// RepoIndexStats reports indexing metrics and freshness for a repository.
type RepoIndexStats struct {
	RepoID        string
	RepoName      string
	Indexing      bool
	SlowOp        bool
	ProgressDirs  int64
	UpdatedAt     time.Time
	SnapshotCount int
	SourceCount   int
	FileCount     int
	FolderCount   int
	DeletedCount  int
	CachedDirs    int
	LastError     string
}

type repoCatalog struct {
	stats  RepoIndexStats
	items  []SearchItem
	seeded bool
	memDir map[string][]CachedEntry // used only when cacheDir == "" (in-memory unit tests)
}

// Engine manages per-repository incremental DAG caches and cross-repository search catalogs.
type Engine struct {
	cacheDir      string
	onChange      func()
	onSourceSnaps func(repoID string, source kopia.SourceInfo, snaps []*kopia.Snapshot)

	maxCacheBytes atomic.Int64
	rankSeq       atomic.Uint64

	dirLRU  *lru.Cache[string, []CachedEntry]
	diffLRU *lru.Cache[string, DiffSummary]

	storesMu sync.RWMutex
	stores   map[string]*repoStore

	remoteMu    sync.RWMutex
	remoteRepos map[string]bool

	mu    sync.RWMutex
	repos map[string]*repoCatalog
}

// NewEngine creates a new search & indexing engine persisting bounded bbolt+S2 caches in cacheDir.
func NewEngine(cacheDir string, onChange func()) *Engine {
	dirLRU, _ := lru.New[string, []CachedEntry](l1DirCacheMaxEntries)
	diffLRU, _ := lru.New[string, DiffSummary](l1DiffCacheMaxPairs)
	e := &Engine{
		cacheDir:    cacheDir,
		onChange:    onChange,
		dirLRU:      dirLRU,
		diffLRU:     diffLRU,
		stores:      make(map[string]*repoStore),
		remoteRepos: make(map[string]bool),
		repos:       make(map[string]*repoCatalog),
	}
	e.maxCacheBytes.Store(int64(config.DefaultCacheMaxMB) << 20)
	e.rankSeq.Store(uint64(time.Now().UnixNano()))
	return e
}

func (e *Engine) nextRank() uint64 {
	return e.rankSeq.Add(1)
}

// SetRepoRemote records whether repoID is backed by networked/cloud storage so IndexRepo estimates >=0.3s operations upfront.
func (e *Engine) SetRepoRemote(repoID string, remote bool) {
	if repoID == "" {
		return
	}
	e.remoteMu.RLock()
	cur, exists := e.remoteRepos[repoID]
	e.remoteMu.RUnlock()
	if exists && cur == remote {
		return
	}
	e.remoteMu.Lock()
	e.remoteRepos[repoID] = remote
	e.remoteMu.Unlock()
}

func (e *Engine) isRepoRemote(repoID string) bool {
	e.remoteMu.RLock()
	remote := e.remoteRepos[repoID]
	e.remoteMu.RUnlock()
	return remote
}

// SetOnSourceSnapshots registers an optional callback invoked whenever IndexRepo fetches fresh snapshot lists for a source.
func (e *Engine) SetOnSourceSnapshots(fn func(repoID string, source kopia.SourceInfo, snaps []*kopia.Snapshot)) {
	e.mu.Lock()
	e.onSourceSnaps = fn
	e.mu.Unlock()
}

// SetMaxCacheMB updates the maximum disk cache budget (in MiB) and immediately enforces it across stored caches.
func (e *Engine) SetMaxCacheMB(mb int) {
	if mb <= 0 {
		mb = config.DefaultCacheMaxMB
	}
	mb = min(max(mb, config.MinCacheMaxMB), config.MaxCacheMaxMB)
	e.SetMaxCacheBytes(int64(mb) << 20)
}

// SetMaxCacheBytes updates the maximum disk cache budget in bytes and enforces eviction/compaction if needed.
func (e *Engine) SetMaxCacheBytes(maxBytes int64) {
	if maxBytes < 64*1024 {
		maxBytes = 64 * 1024
	}
	e.maxCacheBytes.Store(maxBytes)
	e.EnforceCacheBudget()
}

// MaxCacheBytes returns the active disk cache budget in bytes.
func (e *Engine) MaxCacheBytes() int64 {
	return e.maxCacheBytes.Load()
}

// DiskUsageBytes sums the physical sizes of all cache files in cacheDir.
func (e *Engine) DiskUsageBytes() int64 {
	if e.cacheDir == "" {
		return 0
	}
	entries, err := os.ReadDir(e.cacheDir)
	if err != nil {
		return 0
	}
	var total int64
	for _, de := range entries {
		name := de.Name()
		if !strings.HasPrefix(name, "dag-cache-") && !strings.HasPrefix(name, "dag-index-") {
			continue
		}
		if info, err := de.Info(); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

// TrimMemory purges transient L1 hot caches when the application hides to the background tray.
func (e *Engine) TrimMemory() {
	if e.dirLRU != nil {
		e.dirLRU.Purge()
	}
	if e.diffLRU != nil {
		e.diffLRU.Purge()
	}
}

// ClearDiskCache deletes all persisted DAG/catalog caches on disk and resets in-memory caches.
func (e *Engine) ClearDiskCache() error {
	e.TrimMemory()

	e.storesMu.Lock()
	for id, rs := range e.stores {
		_ = rs.close()
		delete(e.stores, id)
	}
	e.storesMu.Unlock()

	if e.cacheDir != "" {
		if entries, err := os.ReadDir(e.cacheDir); err == nil {
			for _, de := range entries {
				name := de.Name()
				if strings.HasPrefix(name, "dag-cache-") || strings.HasPrefix(name, "dag-index-") {
					_ = os.Remove(filepath.Join(e.cacheDir, name))
				}
			}
		}
	}

	e.mu.Lock()
	for id, rc := range e.repos {
		if !rc.seeded {
			delete(e.repos, id)
		}
	}
	e.mu.Unlock()
	e.notify()
	return nil
}

// Close releases all open bbolt handles cleanly.
func (e *Engine) Close() error {
	e.storesMu.Lock()
	defer e.storesMu.Unlock()
	var firstErr error
	for id, rs := range e.stores {
		if err := rs.close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(e.stores, id)
	}
	return firstErr
}

// EnforceCacheBudget trims oldest/coldest directory entries and compacts bbolt files whenever disk usage exceeds budget.
func (e *Engine) EnforceCacheBudget() {
	if e.cacheDir == "" {
		return
	}
	maxBytes := e.maxCacheBytes.Load()
	if maxBytes <= 0 {
		return
	}

	// Discover any existing .bolt files in cacheDir not yet registered in e.stores
	e.storesMu.RLock()
	knownPaths := make(map[string]bool, len(e.stores))
	storesList := make([]*repoStore, 0, len(e.stores))
	for _, rs := range e.stores {
		knownPaths[rs.path] = true
		storesList = append(storesList, rs)
	}
	e.storesMu.RUnlock()

	if entries, err := os.ReadDir(e.cacheDir); err == nil {
		for _, de := range entries {
			name := de.Name()
			if !de.IsDir() && strings.HasPrefix(name, "dag-cache-") && strings.HasSuffix(name, ".bolt") {
				full := filepath.Join(e.cacheDir, name)
				if !knownPaths[full] {
					storesList = append(storesList, &repoStore{path: full})
				}
			}
		}
	}

	var totalPayload, totalFile int64
	type storeMetric struct {
		rs      *repoStore
		payload int64
		file    int64
	}
	metrics := make([]storeMetric, 0, len(storesList))
	for _, rs := range storesList {
		p, f := rs.payloadAndFileBytes()
		totalPayload += p
		totalFile += f
		metrics = append(metrics, storeMetric{rs: rs, payload: p, file: f})
	}

	maxPayload := max(int64(32*1024), maxBytes*72/100)
	if totalPayload <= maxPayload && totalFile <= maxBytes {
		return
	}

	nStores := max(1, len(metrics))
	for _, m := range metrics {
		sharePayload := maxPayload / int64(nStores)
		if totalPayload > 0 {
			sharePayload = max(int64(16*1024), (maxPayload*85/100)*m.payload/totalPayload)
		}
		shareFile := max(int64(64*1024), maxBytes/int64(nStores))
		if totalPayload > 0 {
			shareFile = max(int64(64*1024), maxBytes*m.payload/totalPayload)
		}
		_ = m.rs.trimToPayloadTarget(sharePayload, shareFile)
	}
}

func (e *Engine) dirLRUKey(repoID, dirOID string) string {
	return repoID + "\x00" + dirOID
}

func (e *Engine) diffLRUKey(repoID, rootA, rootB string) string {
	return repoID + "\x00" + rootA + "\x00" + rootB
}

// LookupDirectory performs a fast O(1) lookup of dirOID from L1 LRU (< 80 ns) or bbolt mmap (~2 µs).
func (e *Engine) LookupDirectory(repoID, dirOID string) ([]CachedEntry, bool) {
	if repoID == "" || dirOID == "" {
		return nil, false
	}
	lkey := e.dirLRUKey(repoID, dirOID)
	if entries, ok := e.dirLRU.Get(lkey); ok {
		return entries, true
	}
	if e.cacheDir == "" {
		e.mu.RLock()
		rc := e.repos[repoID]
		var entries []CachedEntry
		var ok bool
		if rc != nil && rc.memDir != nil {
			entries, ok = rc.memDir[dirOID]
		}
		e.mu.RUnlock()
		if ok {
			e.dirLRU.Add(lkey, entries)
		}
		return entries, ok
	}
	rs, err := e.getOrOpenStore(repoID)
	if err != nil || rs == nil {
		return nil, false
	}
	entries, ok := rs.getDir(dirOID)
	if ok {
		e.dirLRU.Add(lkey, entries)
	}
	return entries, ok
}

// PutDirectory stores a single directory manifest into L1 LRU and persists it to the bounded bbolt store.
func (e *Engine) PutDirectory(repoID, dirOID string, entries []CachedEntry) {
	if repoID == "" || dirOID == "" {
		return
	}
	e.dirLRU.Add(e.dirLRUKey(repoID, dirOID), entries)
	if e.cacheDir == "" {
		e.mu.Lock()
		rc, ok := e.repos[repoID]
		if !ok {
			rc = &repoCatalog{stats: RepoIndexStats{RepoID: repoID}}
			e.repos[repoID] = rc
		}
		if rc.memDir == nil {
			rc.memDir = make(map[string][]CachedEntry)
		}
		rc.memDir[dirOID] = entries
		e.mu.Unlock()
		return
	}
	if rs, err := e.getOrOpenStore(repoID); err == nil && rs != nil {
		_ = rs.putDirs(map[string][]CachedEntry{dirOID: entries}, e.nextRank())
		e.EnforceCacheBudget()
	}
}

func (e *Engine) getDirsBatch(repoID string, oids []string) (map[string][]CachedEntry, []string) {
	uniq := uniqueStrings(oids)
	found := make(map[string][]CachedEntry, len(uniq))
	var needStore []string
	for _, oid := range uniq {
		if entries, ok := e.dirLRU.Get(e.dirLRUKey(repoID, oid)); ok {
			found[oid] = entries
		} else {
			needStore = append(needStore, oid)
		}
	}
	if len(needStore) == 0 {
		return found, nil
	}
	if e.cacheDir == "" {
		var missing []string
		e.mu.RLock()
		rc := e.repos[repoID]
		for _, oid := range needStore {
			if rc != nil && rc.memDir != nil {
				if entries, ok := rc.memDir[oid]; ok {
					found[oid] = entries
					e.dirLRU.Add(e.dirLRUKey(repoID, oid), entries)
					continue
				}
			}
			missing = append(missing, oid)
		}
		e.mu.RUnlock()
		return found, missing
	}
	rs, err := e.getOrOpenStore(repoID)
	if err != nil || rs == nil {
		return found, needStore
	}
	fromDisk, missing := rs.getDirsBatch(needStore)
	for oid, entries := range fromDisk {
		found[oid] = entries
		e.dirLRU.Add(e.dirLRUKey(repoID, oid), entries)
	}
	return found, missing
}

func (e *Engine) putDirsBatch(repoID string, batch map[string][]CachedEntry, rank uint64) {
	if len(batch) == 0 {
		return
	}
	for oid, entries := range batch {
		e.dirLRU.Add(e.dirLRUKey(repoID, oid), entries)
	}
	if e.cacheDir == "" {
		e.mu.Lock()
		rc, ok := e.repos[repoID]
		if !ok {
			rc = &repoCatalog{stats: RepoIndexStats{RepoID: repoID}}
			e.repos[repoID] = rc
		}
		if rc.memDir == nil {
			rc.memDir = make(map[string][]CachedEntry, len(batch))
		}
		for oid, entries := range batch {
			rc.memDir[oid] = entries
		}
		e.mu.Unlock()
		return
	}
	if rs, err := e.getOrOpenStore(repoID); err == nil && rs != nil {
		_ = rs.putDirs(batch, rank)
	}
}

// LookupDiff returns a cached DiffSummary for immutable root OIDs (rootA, rootB) if available.
func (e *Engine) LookupDiff(repoID, rootA, rootB string) (DiffSummary, bool) {
	if repoID == "" || rootA == "" || rootB == "" {
		return DiffSummary{}, false
	}
	return e.diffLRU.Get(e.diffLRUKey(repoID, rootA, rootB))
}

// StoreDiff caches an immutable (rootA, rootB) DiffSummary in the bounded L1 diff cache.
func (e *Engine) StoreDiff(repoID, rootA, rootB string, sum DiffSummary) {
	if repoID == "" || rootA == "" || rootB == "" || len(sum.Entries) > l1DiffMaxItemCount {
		return
	}
	e.diffLRU.Add(e.diffLRUKey(repoID, rootA, rootB), sum)
}

// ItemHistory returns the version history for a SearchItem, loading on-demand from bbolt when not inline.
func (e *Engine) ItemHistory(it SearchItem) []VersionRecord {
	if len(it.History) > 0 {
		return it.History
	}
	if e.cacheDir == "" || it.RepoID == "" {
		return nil
	}
	rs, err := e.getOrOpenStore(it.RepoID)
	if err != nil || rs == nil {
		return nil
	}
	hist, _ := rs.getItemHistory(it.Source.String() + "\x00" + it.Path)
	return hist
}

// EnsureCatalogLoaded restores a persisted search catalog from bbolt on cold app startup (< 5 ms) if present.
func (e *Engine) EnsureCatalogLoaded(repoID string) bool {
	if repoID == "" || e.cacheDir == "" {
		return false
	}
	e.mu.RLock()
	_, exists := e.repos[repoID]
	e.mu.RUnlock()
	if exists {
		return true
	}
	if _, err := os.Stat(e.boltFilePath(repoID)); err != nil {
		return false
	}
	rs, err := e.getOrOpenStore(repoID)
	if err != nil || rs == nil {
		return false
	}
	stats, items, ok := rs.loadPersistedCatalog()
	if !ok {
		return false
	}
	e.mu.Lock()
	if _, already := e.repos[repoID]; !already {
		e.repos[repoID] = &repoCatalog{
			stats: stats,
			items: items,
		}
	}
	e.mu.Unlock()
	return true
}

// Stats returns the current index status for repoID, lazily loading persisted metadata if needed.
func (e *Engine) Stats(repoID string) (RepoIndexStats, bool) {
	e.mu.RLock()
	rc, ok := e.repos[repoID]
	if ok {
		st := rc.stats
		e.mu.RUnlock()
		return st, true
	}
	e.mu.RUnlock()

	if e.EnsureCatalogLoaded(repoID) {
		e.mu.RLock()
		defer e.mu.RUnlock()
		if rc, ok := e.repos[repoID]; ok {
			return rc.stats, true
		}
	}
	return RepoIndexStats{}, false
}

// AllStats returns index statistics across all indexed repositories.
func (e *Engine) AllStats() []RepoIndexStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]RepoIndexStats, 0, len(e.repos))
	for _, rc := range e.repos {
		out = append(out, rc.stats)
	}
	slices.SortFunc(out, func(a, b RepoIndexStats) int {
		return strings.Compare(a.RepoID, b.RepoID)
	})
	return out
}

// RemoveRepo drops in-memory and cached index data for repoID.
func (e *Engine) RemoveRepo(repoID string) {
	e.mu.Lock()
	delete(e.repos, repoID)
	e.mu.Unlock()

	e.storesMu.Lock()
	if rs, ok := e.stores[repoID]; ok {
		_ = rs.close()
		delete(e.stores, repoID)
	}
	e.storesMu.Unlock()

	if e.cacheDir != "" {
		_ = os.Remove(e.boltFilePath(repoID))
		_ = os.Remove(e.legacyGobFilePath(repoID))
	}
	e.notify()
}

// SeedRepo populates a repository catalog directly in memory (used by unit and UI tests).
func (e *Engine) SeedRepo(repoID, repoName string, items []SearchItem) {
	e.SeedRepoWithStats(repoID, repoName, items, 0, time.Now())
}

// SeedRepoWithStats populates a repository catalog with explicit cache count and sync timestamp.
func (e *Engine) SeedRepoWithStats(repoID, repoName string, items []SearchItem, cachedDirs int, updatedAt time.Time) {
	var files, folders, deleted, snaps int
	sources := make(map[string]bool)
	for _, it := range items {
		sources[it.Source.String()] = true
		if it.SnapshotCount > snaps {
			snaps = it.SnapshotCount
		}
		if it.IsDir {
			folders++
		} else {
			files++
		}
		if !it.OnDisk {
			deleted++
		}
	}
	e.mu.Lock()
	e.repos[repoID] = &repoCatalog{
		stats: RepoIndexStats{
			RepoID:        repoID,
			RepoName:      repoName,
			UpdatedAt:     updatedAt,
			SnapshotCount: snaps,
			SourceCount:   len(sources),
			FileCount:     files,
			FolderCount:   folders,
			DeletedCount:  deleted,
			CachedDirs:    cachedDirs,
		},
		items:  slices.Clone(items),
		seeded: true,
	}
	e.mu.Unlock()
	e.notify()
}

// IndexRepo incrementally indexes all sources and snapshots in repoID using bounded bbolt+S2 DAG caching
// and streams progressive catalog updates to the UI while indexing is in flight.
func (e *Engine) IndexRepo(ctx context.Context, repoID, repoName string, cli *kopia.Client) error {
	if cli == nil {
		return fmt.Errorf("kopia client unavailable")
	}

	e.EnsureCatalogLoaded(repoID)

	e.mu.Lock()
	rc, ok := e.repos[repoID]
	if !ok {
		rc = &repoCatalog{stats: RepoIndexStats{RepoID: repoID, RepoName: repoName}}
		e.repos[repoID] = rc
	}
	if rc.stats.Indexing {
		e.mu.Unlock()
		return nil
	}
	remote := e.isRepoRemote(repoID)
	rc.stats.RepoName = repoName
	rc.stats.Indexing = true
	rc.stats.SlowOp = (remote && rc.stats.SnapshotCount == 0) ||
		rc.stats.CachedDirs >= 2500 ||
		rc.stats.FileCount >= 35000 ||
		rc.stats.SnapshotCount >= 80
	rc.stats.ProgressDirs = 0
	rc.stats.LastError = ""
	onSnaps := e.onSourceSnaps
	e.mu.Unlock()
	e.notify()

	finishWithErr := func(err error) error {
		e.mu.Lock()
		if cur, exists := e.repos[repoID]; exists {
			cur.stats.Indexing = false
			cur.stats.SlowOp = false
			if err != nil {
				cur.stats.LastError = err.Error()
			}
		}
		e.mu.Unlock()
		e.notify()
		return err
	}

	sourcesResp, err := cli.ListSources(ctx)
	if err != nil {
		return finishWithErr(err)
	}

	type sourceSnapshots struct {
		source    kopia.SourceInfo
		snapshots []*kopia.Snapshot // chronological oldest -> newest
	}

	var allSources []sourceSnapshots
	totalSnapshots := 0

	type rankedRoot struct {
		oid      string
		snapTime time.Time
		summary  *kopia.DirectorySummary
	}
	var newestFirstRoots []rankedRoot

	for _, srcStatus := range sourcesResp.Sources {
		snapsResp, err := cli.ListSnapshots(ctx, srcStatus.Source, true)
		if err != nil {
			return finishWithErr(err)
		}
		if onSnaps != nil {
			// Publish newest-first clone directly to UI snapshot cache so switching sources is 0ms!
			uiSnaps := slices.Clone(snapsResp.Snapshots)
			slices.SortFunc(uiSnaps, func(x, y *kopia.Snapshot) int {
				return y.StartTime.Compare(x.StartTime.Time)
			})
			onSnaps(repoID, srcStatus.Source, uiSnaps)
		}
		var valid []*kopia.Snapshot
		for _, sn := range snapsResp.Snapshots {
			if sn != nil && sn.RootID != "" {
				valid = append(valid, sn)
				newestFirstRoots = append(newestFirstRoots, rankedRoot{
					oid:      sn.RootID,
					snapTime: sn.StartTime.Time,
					summary:  sn.Summary,
				})
			}
		}
		slices.SortFunc(valid, func(a, b *kopia.Snapshot) int {
			return a.StartTime.Compare(b.StartTime.Time)
		})
		allSources = append(allSources, sourceSnapshots{
			source:    srcStatus.Source,
			snapshots: valid,
		})
		totalSnapshots += len(valid)
	}

	// Prioritize newest snapshots first during DAG fetching so recent snapshots always hold the highest eviction rank.
	slices.SortFunc(newestFirstRoots, func(a, b rankedRoot) int {
		return b.snapTime.Compare(a.snapTime)
	})

	reachableRanks := make(map[string]uint64, len(newestFirstRoots)*4+64)
	rootSummaries := make(map[string]*kopia.DirectorySummary, len(newestFirstRoots))
	ephemeralDirs := make(map[string][]CachedEntry, 256)
	var fetchedCount atomic.Int64

	baseRank := e.nextRank() + uint64(len(newestFirstRoots)+1)*1024
	frontier := make([]string, 0, len(newestFirstRoots))
	for idx, rr := range newestFirstRoots {
		if _, seen := reachableRanks[rr.oid]; !seen {
			rnk := baseRank - uint64(idx)
			reachableRanks[rr.oid] = rnk
			rootSummaries[rr.oid] = rr.summary
			frontier = append(frontier, rr.oid)
		}
	}

	// Upfront scale estimation (>= 300ms threshold) before walking DAG subtrees so <0.3s operations never flash UI spinners.
	if len(frontier) > 0 {
		_, missingRoots := e.getDirsBatch(repoID, frontier)
		missingSet := make(map[string]bool, len(missingRoots))
		for _, m := range missingRoots {
			missingSet[m] = true
		}
		estSlow := remote && len(missingRoots) > 0
		var estMicros int64
		if !estSlow {
			for _, oid := range frontier {
				sum := rootSummaries[oid]
				if sum == nil {
					if missingSet[oid] {
						estSlow = true
						break
					}
					estMicros += 25_000
					continue
				}
				dirs := max(int64(1), sum.TotalDirCount)
				files := max(int64(0), sum.TotalFileCount)
				if missingSet[oid] {
					estMicros += dirs*750 + files*4
				} else {
					estMicros += dirs*25 + files*3
				}
			}
			if estMicros >= 300_000 {
				estSlow = true
			}
		}
		if estSlow {
			e.mu.Lock()
			if cur, exists := e.repos[repoID]; exists && !cur.stats.SlowOp {
				cur.stats.SlowOp = true
				e.mu.Unlock()
				e.notify()
			} else {
				e.mu.Unlock()
			}
		}
	}

	levelStep := uint64(0)
	for len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return finishWithErr(err)
		}
		levelStep++
		cachedBatch, missing := e.getDirsBatch(repoID, frontier)

		var newlyFetchedMu sync.Mutex
		newlyFetched := make(map[string][]CachedEntry, len(missing))
		if len(missing) > 0 {
			if err := fetchDirectoriesConcurrent(ctx, cli, missing, 8, func(oid string, entries []CachedEntry) {
				newlyFetchedMu.Lock()
				newlyFetched[oid] = entries
				newlyFetchedMu.Unlock()
				n := fetchedCount.Add(1)
				if n%20 == 0 {
					e.mu.Lock()
					if cur, exists := e.repos[repoID]; exists {
						cur.stats.ProgressDirs = n
					}
					e.mu.Unlock()
					e.notify()
				}
			}); err != nil {
				return finishWithErr(err)
			}
			writeRank := baseRank
			if levelStep < baseRank {
				writeRank = baseRank - levelStep
			}
			e.putDirsBatch(repoID, newlyFetched, writeRank)
			for oid, entries := range newlyFetched {
				cachedBatch[oid] = entries
			}
		}

		var nextFrontier []string
		for _, oid := range frontier {
			entries := cachedBatch[oid]
			// Keep a bounded working copy for the immediately following aggregation pass
			if len(ephemeralDirs) < 16_384 {
				ephemeralDirs[oid] = entries
			}
			parentRank := reachableRanks[oid]
			for _, ent := range entries {
				if ent.IsDir && ent.ObjectID != "" {
					if prevRnk, seen := reachableRanks[ent.ObjectID]; !seen {
						reachableRanks[ent.ObjectID] = parentRank
						nextFrontier = append(nextFrontier, ent.ObjectID)
					} else if parentRank > prevRnk {
						reachableRanks[ent.ObjectID] = parentRank
					}
				}
			}
		}
		frontier = nextFrontier
	}

	lookupDirForWalk := func(oid string) []CachedEntry {
		if entries, ok := ephemeralDirs[oid]; ok {
			return entries
		}
		entries, _ := e.LookupDirectory(repoID, oid)
		if len(ephemeralDirs) < 16_384 && entries != nil {
			ephemeralDirs[oid] = entries
		}
		return entries
	}

	// Aggregate SearchItems across all sources and snapshots with subtree binding memoization + progressive UI streaming.
	type itemBuilder struct {
		item     SearchItem
		seenOIDs map[string]struct{}
	}
	type boundEntry struct {
		b       *itemBuilder
		ent     CachedEntry
		relPath string
	}
	type subtreeKey struct {
		dirOID string
		prefix string
	}

	var catalogItems []SearchItem
	histories := make(map[string][]byte, 512)
	persistHistOnDisk := e.cacheDir != ""
	fileCount := 0
	folderCount := 0
	deletedCount := 0

	for srcIdx, sg := range allSources {
		if len(sg.snapshots) == 0 {
			continue
		}
		builders := make(map[string]*itemBuilder, 512)
		boundCache := make(map[subtreeKey][]boundEntry, 256)
		latestIdx := len(sg.snapshots) - 1
		srcKey := sg.source.String()

		var expandedCount int
		for snapIdx, snap := range sg.snapshots {
			if err := ctx.Err(); err != nil {
				return finishWithErr(err)
			}
			isLatestSnap := snapIdx == latestIdx
			snapTime := snap.StartTime.Time

			type walkFrame struct {
				dirOID string
				prefix string
				depth  int
			}
			stack := []walkFrame{{dirOID: snap.RootID, prefix: "", depth: 0}}

			for len(stack) > 0 {
				if expandedCount&0xfff == 0 {
					if err := ctx.Err(); err != nil {
						return finishWithErr(err)
					}
				}
				frame := stack[len(stack)-1]
				stack = stack[:len(stack)-1]

				sk := subtreeKey{dirOID: frame.dirOID, prefix: frame.prefix}
				bound, hasBound := boundCache[sk]
				if !hasBound {
					entries := lookupDirForWalk(frame.dirOID)
					bound = make([]boundEntry, 0, len(entries))
					for _, ent := range entries {
						if ent.Name == "" {
							continue
						}
						relPath := ent.Name
						if frame.prefix != "" {
							relPath = frame.prefix + "/" + ent.Name
						}
						b, exists := builders[relPath]
						if !exists {
							ext := ""
							if !ent.IsDir {
								if rawExt := path.Ext(ent.Name); len(rawExt) > 1 {
									ext = strings.ToLower(rawExt[1:])
								}
							}
							b = &itemBuilder{
								item: SearchItem{
									RepoID:    repoID,
									RepoName:  repoName,
									Source:    sg.source,
									Path:      relPath,
									Name:      ent.Name,
									Ext:       ext,
									IsDir:     ent.IsDir,
									FirstSeen: snapTime,
									LastSeen:  snapTime,
								},
								seenOIDs: make(map[string]struct{}, 2),
							}
							builders[relPath] = b
						}
						bound = append(bound, boundEntry{b: b, ent: ent, relPath: relPath})
					}
					if len(boundCache) < 32_768 {
						boundCache[sk] = bound
					}
				}

				for i := range bound {
					be := &bound[i]
					expandedCount++
					if expandedCount > maxExpandedEntries {
						break
					}
					b := be.b
					ent := be.ent

					if snapTime.Before(b.item.FirstSeen) || b.item.FirstSeen.IsZero() {
						b.item.FirstSeen = snapTime
					}
					if !snapTime.Before(b.item.LastSeen) {
						b.item.LastSeen = snapTime
						b.item.LatestSize = ent.Size
						b.item.LatestObjectID = ent.ObjectID
						b.item.LatestSnapshotID = snap.ID
						b.item.LatestRootID = snap.RootID
					}
					if ent.Size > b.item.MaxSize {
						b.item.MaxSize = ent.Size
					}
					if isLatestSnap {
						b.item.OnDisk = true
					}
					b.item.SnapshotCount++
					if ent.ObjectID != "" {
						b.seenOIDs[ent.ObjectID] = struct{}{}
					}
					if len(b.item.History) == 0 || b.item.History[len(b.item.History)-1].ObjectID != ent.ObjectID || isLatestSnap {
						var modT time.Time
						if ent.ModUnix > 0 {
							modT = time.Unix(0, ent.ModUnix)
						}
						b.item.History = append(b.item.History, VersionRecord{
							SnapshotID:   snap.ID,
							SnapshotTime: snapTime,
							RootID:       snap.RootID,
							ObjectID:     ent.ObjectID,
							Size:         ent.Size,
							ModTime:      modT,
						})
					}

					if ent.IsDir && ent.ObjectID != "" && ent.ObjectID != frame.dirOID && frame.depth < maxWalkDepth {
						stack = append(stack, walkFrame{dirOID: ent.ObjectID, prefix: be.relPath, depth: frame.depth + 1})
					}
				}
				if expandedCount > maxExpandedEntries {
					break
				}
			}
		}

		for _, b := range builders {
			b.item.Versions = max(1, len(b.seenOIDs))
			slices.Reverse(b.item.History)
			if persistHistOnDisk {
				histories[srcKey+"\x00"+b.item.Path] = encodeVersionHistory(b.item.History)
				// Drop per-item History slice from Go heap once encoded for bbolt!
				b.item.History = nil
			}
			if b.item.IsDir {
				folderCount++
			} else {
				fileCount++
			}
			if !b.item.OnDisk {
				deletedCount++
			}
			catalogItems = append(catalogItems, b.item)
		}

		// Stream intermediate multi-source catalog to UI while remaining sources continue indexing!
		if srcIdx < len(allSources)-1 && len(catalogItems) > 0 {
			partial := slices.Clone(catalogItems)
			sortCatalogItemsDefault(partial)
			e.mu.Lock()
			if cur, exists := e.repos[repoID]; exists {
				cur.items = partial
				cur.stats.FileCount = fileCount
				cur.stats.FolderCount = folderCount
				cur.stats.DeletedCount = deletedCount
				cur.stats.SourceCount = srcIdx + 1
			}
			e.mu.Unlock()
			e.notify()
		}
	}

	sortCatalogItemsDefault(catalogItems)

	cachedDirCount := len(reachableRanks)
	finalStats := RepoIndexStats{
		RepoID:        repoID,
		RepoName:      repoName,
		Indexing:      false,
		ProgressDirs:  fetchedCount.Load(),
		UpdatedAt:     time.Now(),
		SnapshotCount: totalSnapshots,
		SourceCount:   len(sourcesResp.Sources),
		FileCount:     fileCount,
		FolderCount:   folderCount,
		DeletedCount:  deletedCount,
		CachedDirs:    cachedDirCount,
	}

	if e.cacheDir != "" {
		if rs, err := e.getOrOpenStore(repoID); err == nil && rs != nil {
			maxPayload := max(int64(32*1024), e.maxCacheBytes.Load()*72/100)
			if kept, serr := rs.saveIndexSnapshot(finalStats, catalogItems, histories, reachableRanks, maxPayload); serr == nil {
				finalStats.CachedDirs = kept
			}
		}
		e.EnforceCacheBudget()
	}

	e.mu.Lock()
	var prevMemDir map[string][]CachedEntry
	if e.cacheDir == "" {
		if prev := e.repos[repoID]; prev != nil {
			prevMemDir = prev.memDir
		}
	}
	e.repos[repoID] = &repoCatalog{
		stats:  finalStats,
		items:  catalogItems,
		memDir: prevMemDir,
	}
	e.mu.Unlock()
	e.notify()
	return nil
}

func sortCatalogItemsDefault(items []SearchItem) {
	slices.SortFunc(items, func(a, b SearchItem) int {
		if a.LatestSize != b.LatestSize {
			if a.LatestSize > b.LatestSize {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
}

func fetchDirectoriesConcurrent(
	ctx context.Context,
	cli *kopia.Client,
	oids []string,
	concurrency int,
	onFetched func(oid string, entries []CachedEntry),
) error {
	if len(oids) == 0 {
		return nil
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(oids) {
		concurrency = len(oids)
	}

	workCh := make(chan string, len(oids))
	for _, oid := range oids {
		workCh <- oid
	}
	close(workCh)

	ctxFetch, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)

	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for oid := range workCh {
				if ctxFetch.Err() != nil {
					return
				}
				dm, err := cli.GetDirectory(ctxFetch, oid)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				onFetched(oid, FromKopiaDirEntries(dm.Entries))
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func (e *Engine) loadDiskCache(repoID string) map[string][]CachedEntry {
	if e.cacheDir == "" {
		e.mu.RLock()
		defer e.mu.RUnlock()
		out := make(map[string][]CachedEntry)
		if rc := e.repos[repoID]; rc != nil {
			for k, v := range rc.memDir {
				out[k] = slices.Clone(v)
			}
		}
		return out
	}
	rs, err := e.getOrOpenStore(repoID)
	if err != nil || rs == nil {
		return make(map[string][]CachedEntry)
	}
	return rs.dumpAllDirs()
}

func (e *Engine) saveDiskCache(repoID string, dirCache map[string][]CachedEntry) error {
	e.putDirsBatch(repoID, dirCache, e.nextRank())
	e.EnforceCacheBudget()
	return nil
}

func (e *Engine) mergeDiskCacheEntries(repoID string, added map[string][]CachedEntry) error {
	if len(added) == 0 {
		return nil
	}
	e.putDirsBatch(repoID, added, e.nextRank())
	e.EnforceCacheBudget()
	return nil
}

func (e *Engine) notify() {
	if e.onChange != nil {
		e.onChange()
	}
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
