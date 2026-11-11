package index

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
)

const (
	indexSchemaVersion = 2
	maxWalkDepth       = 256
	maxExpandedEntries = 5_000_000
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
	History          []VersionRecord // newest first
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
	stats RepoIndexStats
	items []SearchItem
}

// Engine manages per-repository incremental DAG caches and cross-repository search catalogs.
type Engine struct {
	cacheDir string
	onChange func()

	diskMu sync.Mutex
	mu     sync.RWMutex
	repos  map[string]*repoCatalog
}

// NewEngine creates a new search & indexing engine persisting DAG caches in cacheDir.
func NewEngine(cacheDir string, onChange func()) *Engine {
	return &Engine{
		cacheDir: cacheDir,
		onChange: onChange,
		repos:    make(map[string]*repoCatalog),
	}
}

// Stats returns the current index status for repoID.
func (e *Engine) Stats(repoID string) (RepoIndexStats, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rc, ok := e.repos[repoID]
	if !ok {
		return RepoIndexStats{}, false
	}
	return rc.stats, true
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
	if e.cacheDir != "" {
		e.diskMu.Lock()
		_ = os.Remove(e.cacheFilePath(repoID))
		e.diskMu.Unlock()
	}
	e.notify()
}

// SeedRepo populates a repository catalog directly in memory (used by unit and UI tests).
func (e *Engine) SeedRepo(repoID, repoName string, items []SearchItem) {
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
			UpdatedAt:     time.Now(),
			SnapshotCount: snaps,
			SourceCount:   len(sources),
			FileCount:     files,
			FolderCount:   folders,
			DeletedCount:  deleted,
		},
		items: slices.Clone(items),
	}
	e.mu.Unlock()
	e.notify()
}

// IndexRepo incrementally indexes all sources and snapshots in repoID using DAG directory deduplication.
func (e *Engine) IndexRepo(ctx context.Context, repoID, repoName string, cli *kopia.Client) error {
	if cli == nil {
		return fmt.Errorf("kopia client unavailable")
	}

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
	rc.stats.RepoName = repoName
	rc.stats.Indexing = true
	rc.stats.ProgressDirs = 0
	rc.stats.LastError = ""
	e.mu.Unlock()
	e.notify()

	finishWithErr := func(err error) error {
		e.mu.Lock()
		if cur, exists := e.repos[repoID]; exists {
			cur.stats.Indexing = false
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
		snapshots []*kopia.Snapshot
	}

	var allSources []sourceSnapshots
	totalSnapshots := 0
	neededRoots := make([]string, 0, 64)

	for _, srcStatus := range sourcesResp.Sources {
		snapsResp, err := cli.ListSnapshots(ctx, srcStatus.Source, true)
		if err != nil {
			return finishWithErr(err)
		}
		var valid []*kopia.Snapshot
		for _, sn := range snapsResp.Snapshots {
			if sn.RootID != "" {
				valid = append(valid, sn)
				neededRoots = append(neededRoots, sn.RootID)
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

	dirCache := e.loadDiskCache(repoID)
	reachableDirs := make(map[string]bool, len(dirCache)+256)

	var (
		cacheMu      sync.RWMutex
		fetchedCount atomic.Int64
	)

	// BFS level-by-level concurrent directory DAG fetch for any uncached k... directory object IDs.
	frontier := uniqueStrings(neededRoots)
	for _, oid := range frontier {
		reachableDirs[oid] = true
	}

	for len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return finishWithErr(err)
		}

		var missing []string
		cacheMu.RLock()
		for _, oid := range frontier {
			if _, cached := dirCache[oid]; !cached {
				missing = append(missing, oid)
			}
		}
		cacheMu.RUnlock()

		if len(missing) > 0 {
			if err := fetchDirectoriesConcurrent(ctx, cli, missing, 8, func(oid string, entries []CachedEntry) {
				cacheMu.Lock()
				dirCache[oid] = entries
				cacheMu.Unlock()
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
		}

		var nextFrontier []string
		cacheMu.RLock()
		for _, oid := range frontier {
			for _, ent := range dirCache[oid] {
				if ent.IsDir && ent.ObjectID != "" && !reachableDirs[ent.ObjectID] {
					reachableDirs[ent.ObjectID] = true
					nextFrontier = append(nextFrontier, ent.ObjectID)
				}
			}
		}
		cacheMu.RUnlock()
		frontier = nextFrontier
	}

	// Prune stale unreachable directories and persist updated DAG cache.
	for oid := range dirCache {
		if !reachableDirs[oid] {
			delete(dirCache, oid)
		}
	}
	_ = e.saveDiskCache(repoID, dirCache)

	// Aggregate SearchItems across all sources and snapshots.
	type itemBuilder struct {
		item     SearchItem
		seenOIDs map[string]struct{}
	}

	var catalogItems []SearchItem
	fileCount := 0
	folderCount := 0
	deletedCount := 0

	for _, sg := range allSources {
		if len(sg.snapshots) == 0 {
			continue
		}
		builders := make(map[string]*itemBuilder, 512)
		latestIdx := len(sg.snapshots) - 1

		var expandedCount int
		for snapIdx, snap := range sg.snapshots {
			if err := ctx.Err(); err != nil {
				return err
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
						return err
					}
				}
				frame := stack[len(stack)-1]
				stack = stack[:len(stack)-1]

				entries := dirCache[frame.dirOID]
				for _, ent := range entries {
					if ent.Name == "" {
						continue
					}
					expandedCount++
					if expandedCount > maxExpandedEntries {
						break
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
					var modT time.Time
					if ent.ModUnix > 0 {
						modT = time.Unix(0, ent.ModUnix)
					}
					// Record history entry if first occurrence or content changed or latest snapshot.
					if len(b.item.History) == 0 || b.item.History[len(b.item.History)-1].ObjectID != ent.ObjectID || isLatestSnap {
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
						stack = append(stack, walkFrame{dirOID: ent.ObjectID, prefix: relPath, depth: frame.depth + 1})
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
	}

	// Default order: largest logical size first for instant zero-query browsing.
	slices.SortFunc(catalogItems, func(a, b SearchItem) int {
		if a.LatestSize != b.LatestSize {
			if a.LatestSize > b.LatestSize {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})

	e.mu.Lock()
	e.repos[repoID] = &repoCatalog{
		stats: RepoIndexStats{
			RepoID:        repoID,
			RepoName:      repoName,
			Indexing:      false,
			ProgressDirs:  fetchedCount.Load(),
			UpdatedAt:     time.Now(),
			SnapshotCount: totalSnapshots,
			SourceCount:   len(allSources),
			FileCount:     fileCount,
			FolderCount:   folderCount,
			DeletedCount:  deletedCount,
			CachedDirs:    len(dirCache),
		},
		items: catalogItems,
	}
	e.mu.Unlock()
	e.notify()
	return nil
}

func fetchDirectoriesConcurrent(
	ctx context.Context,
	cli *kopia.Client,
	oids []string,
	workers int,
	onFetched func(oid string, entries []CachedEntry),
) error {
	if workers <= 0 {
		workers = 4
	}
	if workers > len(oids) {
		workers = len(oids)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string, len(oids))
	for _, oid := range oids {
		jobs <- oid
	}
	close(jobs)

	var (
		wg       sync.WaitGroup
		firstErr error
		errOnce  sync.Once
	)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for oid := range jobs {
				if ctx.Err() != nil {
					return
				}
				dm, err := cli.GetDirectory(ctx, oid)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				compact := make([]CachedEntry, 0, len(dm.Entries))
				for _, de := range dm.Entries {
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
				onFetched(oid, compact)
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func (e *Engine) cacheFilePath(repoID string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, repoID)
	if len(safe) > 48 {
		safe = safe[:48]
	}
	sum := sha256.Sum256([]byte(repoID))
	return filepath.Join(e.cacheDir, fmt.Sprintf("dag-index-%s-%s.gob", safe, hex.EncodeToString(sum[:6])))
}

func (e *Engine) loadDiskCache(repoID string) map[string][]CachedEntry {
	e.diskMu.Lock()
	defer e.diskMu.Unlock()
	return e.loadDiskCacheLocked(repoID)
}

func (e *Engine) loadDiskCacheLocked(repoID string) map[string][]CachedEntry {
	out := make(map[string][]CachedEntry)
	if e.cacheDir == "" {
		return out
	}
	f, err := os.Open(e.cacheFilePath(repoID))
	if err != nil {
		return out
	}
	defer f.Close()

	var disk diskRepoCache
	if err := gob.NewDecoder(f).Decode(&disk); err != nil || disk.Version != indexSchemaVersion || disk.RepoID != repoID {
		return out
	}
	for oid, entries := range disk.DirCache {
		out[oid] = slices.Clone(entries)
	}
	return out
}

func (e *Engine) saveDiskCache(repoID string, dirCache map[string][]CachedEntry) error {
	e.diskMu.Lock()
	defer e.diskMu.Unlock()
	return e.saveDiskCacheLocked(repoID, dirCache)
}

func (e *Engine) mergeDiskCacheEntries(repoID string, added map[string][]CachedEntry) error {
	if e.cacheDir == "" || len(added) == 0 {
		return nil
	}
	e.diskMu.Lock()
	defer e.diskMu.Unlock()
	cur := e.loadDiskCacheLocked(repoID)
	for oid, entries := range added {
		cur[oid] = slices.Clone(entries)
	}
	return e.saveDiskCacheLocked(repoID, cur)
}

func (e *Engine) saveDiskCacheLocked(repoID string, dirCache map[string][]CachedEntry) error {
	if e.cacheDir == "" {
		return nil
	}
	if err := config.EnsurePrivateDir(e.cacheDir); err != nil {
		return err
	}
	target := e.cacheFilePath(repoID)
	f, err := os.CreateTemp(e.cacheDir, "dag-index-*.gob.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	encErr := gob.NewEncoder(f).Encode(diskRepoCache{
		Version:  indexSchemaVersion,
		RepoID:   repoID,
		Updated:  time.Now(),
		DirCache: dirCache,
	})
	closeErr := f.Close()
	if encErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		if encErr != nil {
			return encErr
		}
		return closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
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
