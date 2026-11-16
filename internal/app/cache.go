// @scope.file app.cache
// @hash sha256:6e5e1b91e6872ef4
// @implements spec.desktop_ui sha256:67347d72ce79be0a
// @ref default golang.org/x/sync@0.25.0
// @ref default config.store sha256:b49642aa028d360a
// @ref default index.diff sha256:bffd1e6ac38d6e3a
// @ref default index.engine sha256:1aea8ec7725d31ae
// @ref default kopia.client sha256:c1253141ff6ec455
// @ref default kopia.manager sha256:332d271a00525eae
// @ref default kopia.types sha256:06dd81559d688041
// @ref default app.core sha256:4089493662be47e2
// Coalesced singleflight RPC loaders, multi-source snapshot/policy hot caches, upfront >=0.3s cost estimation, and 60fps memoization.

package app

import (
	"context"
	"slices"
	"strings"

	"mygopia/internal/config"
	"mygopia/internal/index"
	"mygopia/internal/kopia"
)

const (
	maxCachedSourceLists  = 128
	slowOpThresholdMicros = 300_000 // 0.3s a-priori threshold separating fast vs slow UI operations
)

type snapCacheKey struct {
	repoID           string
	sourceStr        string
	includeIdentical bool
}

type snapTimelineMemoKey struct {
	rev        uint64
	timePreset int
	yearFilter string
	dateFrom   int64
	dateTo     int64
	query      string
	pinnedOnly bool
}

type browserFilterMemoKey struct {
	rev        uint64
	query      string
	kindSeg    int
	sizePreset int
	timePreset int
	dateFrom   int64
	dateTo     int64
	sortCol    string
	sortDesc   bool
}

type diffFilterMemoKey struct {
	rev         uint64
	changeSeg   int
	kindSeg     int
	sizePreset  int
	timePreset  int
	dateFrom    int64
	dateTo      int64
	query       string
	includeExts string
	sortCol     string
	sortDesc    bool
}

func (a *App) putCachedSnapshots(repoID, sourceStr string, includeIdentical bool, snaps []*kopia.Snapshot) {
	if repoID == "" || sourceStr == "" {
		return
	}
	cloned := slices.Clone(snaps)
	a.cacheMu.Lock()
	if a.snapListCache == nil {
		a.snapListCache = make(map[snapCacheKey][]*kopia.Snapshot, 16)
	}
	if len(a.snapListCache) >= maxCachedSourceLists {
		clear(a.snapListCache)
	}
	a.snapListCache[snapCacheKey{
		repoID:           repoID,
		sourceStr:        sourceStr,
		includeIdentical: includeIdentical,
	}] = cloned
	a.cacheMu.Unlock()
}

func (a *App) lookupCachedSnapshots(repoID, sourceStr string, includeIdentical bool) ([]*kopia.Snapshot, bool) {
	if repoID == "" || sourceStr == "" {
		return nil, false
	}
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	if a.snapListCache == nil {
		return nil, false
	}
	if exact, ok := a.snapListCache[snapCacheKey{
		repoID:           repoID,
		sourceStr:        sourceStr,
		includeIdentical: includeIdentical,
	}]; ok {
		return slices.Clone(exact), true
	}
	// If non-identical view was requested and we already cached the superset (includeIdentical=true),
	// derive unique-root snapshots immediately for zero-latency first paint while revalidating in background.
	if !includeIdentical {
		if all, ok := a.snapListCache[snapCacheKey{
			repoID:           repoID,
			sourceStr:        sourceStr,
			includeIdentical: true,
		}]; ok {
			return filterUniqueRootSnapshots(all), true
		}
	}
	return nil, false
}

func filterUniqueRootSnapshots(newestFirst []*kopia.Snapshot) []*kopia.Snapshot {
	if len(newestFirst) <= 1 {
		return slices.Clone(newestFirst)
	}
	// Keep chronological transitions where RootID differs from the immediately older snapshot (or has pins/notes).
	out := make([]*kopia.Snapshot, 0, len(newestFirst))
	for i, sn := range newestFirst {
		if sn == nil {
			continue
		}
		if i == len(newestFirst)-1 || len(sn.Pins) > 0 || sn.Description != "" {
			out = append(out, sn)
			continue
		}
		older := newestFirst[i+1]
		if older == nil || sn.RootID != older.RootID {
			out = append(out, sn)
		}
	}
	return out
}

func (a *App) invalidateRepoSnapshotsCache(repoID string) {
	a.cacheMu.Lock()
	for k := range a.snapListCache {
		if repoID == "" || k.repoID == repoID {
			delete(a.snapListCache, k)
		}
	}
	a.cacheMu.Unlock()
	a.diffActiveCalcKey = ""
}

func (a *App) invalidateRepoPolicyCache(repoID string) {
	a.cacheMu.Lock()
	for k := range a.policyCache {
		if repoID == "" || len(k) > len(repoID) && k[:len(repoID)+1] == repoID+"\x00" {
			delete(a.policyCache, k)
		}
	}
	a.cacheMu.Unlock()
	a.diffActiveCalcKey = ""
}

func (a *App) trimCachesForBackground() {
	if a.indexer != nil {
		a.indexer.TrimMemory()
	}
	a.cacheMu.Lock()
	clear(a.snapListCache)
	clear(a.policyCache)
	a.cacheMu.Unlock()

	a.snapMemoIdx = nil
	a.snapMemoKey = snapTimelineMemoKey{}
	a.browserMemoEntries = nil
	a.browserMemoKey = browserFilterMemoKey{}
	a.diffMemoEntries = nil
	a.diffMemoKey = diffFilterMemoKey{}
	a.searchVerHistKey = ""
	a.searchVerHistSlice = nil
}

func snapshotsEqual(a, b []*kopia.Snapshot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		sa, sb := a[i], b[i]
		if (sa == nil) != (sb == nil) {
			return false
		}
		if sa == nil {
			continue
		}
		if sa.ID != sb.ID ||
			sa.RootID != sb.RootID ||
			sa.Description != sb.Description ||
			sa.IncompleteReason != sb.IncompleteReason ||
			!sa.StartTime.Equal(sb.StartTime.Time) ||
			!slices.Equal(sa.Pins, sb.Pins) {
			return false
		}
	}
	return true
}

func (a *App) fetchSnapshotsCoalesced(
	ctx context.Context,
	repoID string,
	src kopia.SourceInfo,
	includeIdentical bool,
) ([]*kopia.Snapshot, error) {
	sfKey := "snaps:" + repoID + ":" + src.String() + ":"
	if includeIdentical {
		sfKey += "1"
	} else {
		sfKey += "0"
	}
	val, err, _ := a.sfGroup.Do(sfKey, func() (any, error) {
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			return nil, err
		}
		resp, err := cli.ListSnapshots(ctx, src, includeIdentical)
		if err != nil {
			return nil, err
		}
		snaps := slices.Clone(resp.Snapshots)
		slices.SortFunc(snaps, func(x, y *kopia.Snapshot) int {
			return y.StartTime.Compare(x.StartTime.Time)
		})
		a.putCachedSnapshots(repoID, src.String(), includeIdentical, snaps)
		return snaps, nil
	})
	if err != nil {
		return nil, err
	}
	snaps, _ := val.([]*kopia.Snapshot)
	return slices.Clone(snaps), nil
}

func (a *App) fetchDirectoryCoalesced(ctx context.Context, repoID, dirOID string) ([]*kopia.DirEntry, error) {
	if cached, ok := a.indexer.LookupDirectory(repoID, dirOID); ok {
		return index.ToKopiaDirEntries(cached), nil
	}
	sfKey := "dir:" + repoID + ":" + dirOID
	val, err, _ := a.sfGroup.Do(sfKey, func() (any, error) {
		if cached, ok := a.indexer.LookupDirectory(repoID, dirOID); ok {
			return index.ToKopiaDirEntries(cached), nil
		}
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			return nil, err
		}
		dm, err := cli.GetDirectory(ctx, dirOID)
		if err != nil {
			return nil, err
		}
		a.indexer.PutDirectory(repoID, dirOID, index.FromKopiaDirEntries(dm.Entries))
		return dm.Entries, nil
	})
	if err != nil {
		return nil, err
	}
	entries, _ := val.([]*kopia.DirEntry)
	return entries, nil
}

func (a *App) fetchEffectivePolicyCoalesced(ctx context.Context, repoID string, src kopia.SourceInfo) *kopia.Policy {
	pkey := repoID + "\x00" + src.String()
	a.cacheMu.RLock()
	if a.policyCache != nil {
		if pol, ok := a.policyCache[pkey]; ok {
			a.cacheMu.RUnlock()
			return pol
		}
	}
	a.cacheMu.RUnlock()

	val, err, _ := a.sfGroup.Do("pol:"+pkey, func() (any, error) {
		cli, err := a.manager.ClientFor(ctx, repoID)
		if err != nil {
			return nil, err
		}
		resPol, err := cli.ResolvePolicy(ctx, src, nil, 1)
		if err != nil || resPol == nil || resPol.Effective == nil {
			return nil, err
		}
		a.cacheMu.Lock()
		if a.policyCache == nil {
			a.policyCache = make(map[string]*kopia.Policy, 8)
		}
		a.policyCache[pkey] = resPol.Effective
		a.cacheMu.Unlock()
		return resPol.Effective, nil
	})
	if err != nil {
		return nil
	}
	pol, _ := val.(*kopia.Policy)
	return pol
}

func (a *App) setSnapshotsSlice(snaps []*kopia.Snapshot) {
	a.snapshots = snaps
	a.snapshotsRev++
}

func (a *App) setBrowserEntriesSlice(entries []*kopia.DirEntry) {
	a.browserEntries = entries
	a.browserEntriesRev++
}

func (a *App) setDiffSummary(sum index.DiffSummary) {
	a.diffSummary = sum
	a.diffSummaryRev++
}

func makeDiffCalcKey(repoID, baseID, targetID string) string {
	return repoID + "\x00" + baseID + "\x00" + targetID
}

// isRemoteRepository reports whether the repository requires network I/O (cloud object storage, SFTP/WebDAV/Rclone,
// remote Kopia Repository Server, or SMB/UNC network share) rather than a local disk filesystem.
func isRemoteRepository(prof config.RepoProfile, st kopia.RepoSnapshotState) bool {
	if st.Status != nil {
		if strings.TrimSpace(st.Status.APIServerURL) != "" {
			return true
		}
		if storage := strings.TrimSpace(st.Status.Storage); storage != "" {
			return storage != "filesystem"
		}
	}
	hint := strings.TrimSpace(prof.StorageHint)
	if hint == "" {
		return false
	}
	if hint == "filesystem" || strings.HasPrefix(hint, "filesystem:") {
		rest := strings.TrimSpace(strings.TrimPrefix(hint, "filesystem:"))
		return strings.HasPrefix(rest, `\\`) || strings.HasPrefix(rest, "//")
	}
	return true
}

func snapshotTreeScale(sn *kopia.Snapshot) (files, dirs int64, known bool) {
	if sn == nil || sn.Summary == nil {
		return 0, 0, false
	}
	return max(0, sn.Summary.TotalFileCount), max(1, sn.Summary.TotalDirCount), true
}

func (a *App) estimateIndexSlow(prof config.RepoProfile, st kopia.RepoSnapshotState) bool {
	remote := isRemoteRepository(prof, st)
	var idxStats index.RepoIndexStats
	var indexed bool
	if a.indexer != nil && prof.ID != "" {
		idxStats, indexed = a.indexer.Stats(prof.ID)
	}
	if !indexed && remote {
		return true
	}
	if indexed && (idxStats.CachedDirs >= 2500 || idxStats.FileCount >= 35000 || idxStats.SnapshotCount >= 80) {
		return true
	}
	var estMicros int64
	for _, s := range st.SourceList() {
		var rootOID string
		var sum *kopia.DirectorySummary
		if s.LastSnapshot != nil && s.LastSnapshot.RootEntry != nil {
			rootOID = s.LastSnapshot.RootEntry.ObjectID
			sum = s.LastSnapshot.RootEntry.DirSummary
		}
		var files, dirs int64
		known := sum != nil
		if known {
			files = max(0, sum.TotalFileCount)
			dirs = max(1, sum.TotalDirCount)
		}
		rootCached := false
		if a.indexer != nil && rootOID != "" {
			_, rootCached = a.indexer.LookupDirectory(prof.ID, rootOID)
		}
		if !rootCached {
			if remote || !known {
				return true
			}
			estMicros += dirs*750 + files*4
		} else {
			estMicros += dirs*25 + files*3
		}
	}
	return estMicros >= slowOpThresholdMicros
}

func (a *App) estimateManualRefreshSlow(prof config.RepoProfile, st kopia.RepoSnapshotState, willIndex bool) bool {
	if isRemoteRepository(prof, st) || !st.Connected() {
		return true
	}
	if len(st.SourceList()) >= 8 {
		return true
	}
	if willIndex && a.estimateIndexSlow(prof, st) {
		return true
	}
	return false
}

func (a *App) estimateSnapListSlow(prof config.RepoProfile, st kopia.RepoSnapshotState, seeded bool) bool {
	if seeded {
		return false
	}
	return isRemoteRepository(prof, st) || !st.Connected()
}

func (a *App) estimateDirLoadSlow(prof config.RepoProfile, st kopia.RepoSnapshotState, summary *kopia.DirectorySummary) bool {
	if isRemoteRepository(prof, st) || !st.Connected() {
		return true
	}
	if summary != nil && summary.TotalFileCount+summary.TotalDirCount >= 15000 {
		return true
	}
	return false
}

func (a *App) estimateLocalDiffSlow(prof config.RepoProfile, st kopia.RepoSnapshotState, repoID string, snapA *kopia.Snapshot) bool {
	filesA, dirsA, known := snapshotTreeScale(snapA)
	rootCached := false
	var indexed bool
	if a.indexer != nil {
		if snapA != nil && snapA.RootID != "" {
			_, rootCached = a.indexer.LookupDirectory(repoID, snapA.RootID)
		}
		_, indexed = a.indexer.Stats(repoID)
	}
	treeCached := rootCached && (indexed || dirsA <= 1)
	if !treeCached && isRemoteRepository(prof, st) {
		return true
	}
	if !known {
		return true
	}
	estMicros := dirsA*80 + filesA*4
	if !treeCached {
		estMicros += dirsA * 900
	} else {
		estMicros += dirsA * 8
	}
	return estMicros >= slowOpThresholdMicros
}

func (a *App) estimateSnapDiffSlow(prof config.RepoProfile, st kopia.RepoSnapshotState, repoID string, snapA, snapB *kopia.Snapshot) bool {
	filesA, dirsA, knownA := snapshotTreeScale(snapA)
	filesB, dirsB, knownB := snapshotTreeScale(snapB)
	if !knownA && !knownB {
		return true
	}
	maxFiles := max(filesA, filesB)
	maxDirs := max(dirsA, dirsB)
	var okA, okB, indexed bool
	if a.indexer != nil {
		if snapA != nil && snapA.RootID != "" {
			_, okA = a.indexer.LookupDirectory(repoID, snapA.RootID)
		}
		if snapB != nil && snapB.RootID != "" {
			_, okB = a.indexer.LookupDirectory(repoID, snapB.RootID)
		}
		_, indexed = a.indexer.Stats(repoID)
	}
	bothCached := okA && okB && (indexed || maxDirs <= 1)
	if !bothCached && isRemoteRepository(prof, st) {
		return true
	}
	var estMicros int64
	if bothCached {
		estMicros = maxDirs*15 + maxFiles*2
	} else {
		estMicros = maxDirs*700 + maxFiles*4
	}
	return estMicros >= slowOpThresholdMicros
}
