package index

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"mygopia/internal/kopia"
)

// DiffKind classifies how an entry changed between Snapshot A (base) and Snapshot B (target).
type DiffKind uint8

const (
	DiffAdded DiffKind = iota
	DiffModified
	DiffDeleted
)

// DiffEntry describes a single added, modified, or deleted file or directory between two snapshots.
type DiffEntry struct {
	Path        string
	Name        string
	Ext         string
	IsDir       bool
	Kind        DiffKind
	OldSize     int64
	NewSize     int64
	SizeDelta   int64
	OldModTime  time.Time
	NewModTime  time.Time
	OldObjectID string
	NewObjectID string
}

// EffectiveModTime returns NewModTime for added/modified entries and OldModTime for deleted entries.
func (d DiffEntry) EffectiveModTime() time.Time {
	if d.Kind == DiffDeleted || d.NewModTime.IsZero() {
		return d.OldModTime
	}
	return d.NewModTime
}

// DiffSummary holds the full list of differences and aggregate statistics between two snapshots.
type DiffSummary struct {
	Entries       []DiffEntry
	AddedCount    int
	ModifiedCount int
	DeletedCount  int
	AddedBytes    int64
	RemovedBytes  int64
	NetDeltaBytes int64
}

// CompareRoots computes the recursive tree diff between rootA (older/base) and rootB (newer/target).
// Unchanged subtrees sharing the same Merkle ObjectID are skipped in O(1) without fetching child directories.
func (e *Engine) CompareRoots(
	ctx context.Context,
	repoID string,
	cli *kopia.Client,
	rootA, rootB string,
) (DiffSummary, error) {
	if rootA == "" || rootB == "" {
		return DiffSummary{}, fmt.Errorf("snapshot root object ID is empty")
	}
	if rootA == rootB {
		return DiffSummary{}, nil
	}
	if cli == nil {
		return DiffSummary{}, fmt.Errorf("kopia client unavailable")
	}

	dirCache := e.loadDiskCache(repoID)
	newlyFetched := make(map[string][]CachedEntry)
	var cacheMu sync.RWMutex

	ensureDirsLoaded := func(oids []string) error {
		uniq := uniqueStrings(oids)
		var missing []string
		cacheMu.RLock()
		for _, oid := range uniq {
			if _, ok := dirCache[oid]; !ok {
				missing = append(missing, oid)
			}
		}
		cacheMu.RUnlock()
		if len(missing) == 0 {
			return nil
		}
		err := fetchDirectoriesConcurrent(ctx, cli, missing, 8, func(oid string, entries []CachedEntry) {
			cacheMu.Lock()
			dirCache[oid] = entries
			newlyFetched[oid] = entries
			cacheMu.Unlock()
		})
		return err
	}

	type pairTask struct {
		prefix string
		oidA   string // empty if subtree only exists in B (Added)
		oidB   string // empty if subtree only exists in A (Deleted)
		depth  int
	}

	frontier := []pairTask{{prefix: "", oidA: rootA, oidB: rootB, depth: 0}}
	var entries []DiffEntry

	for len(frontier) > 0 && len(entries) < maxExpandedEntries {
		if err := ctx.Err(); err != nil {
			return DiffSummary{}, err
		}

		var needOIDs []string
		for _, task := range frontier {
			if task.oidA != "" && task.oidA == task.oidB {
				continue
			}
			if task.oidA != "" {
				needOIDs = append(needOIDs, task.oidA)
			}
			if task.oidB != "" {
				needOIDs = append(needOIDs, task.oidB)
			}
		}
		if err := ensureDirsLoaded(needOIDs); err != nil {
			return DiffSummary{}, err
		}

		var nextFrontier []pairTask
		for _, task := range frontier {
			if err := ctx.Err(); err != nil {
				return DiffSummary{}, err
			}
			if task.oidA != "" && task.oidA == task.oidB {
				continue
			}

			listA := dirCache[task.oidA]
			listB := dirCache[task.oidB]

			mapA := make(map[string]CachedEntry, len(listA))
			for _, a := range listA {
				if a.Name != "" {
					mapA[a.Name] = a
				}
			}
			mapB := make(map[string]CachedEntry, len(listB))
			for _, b := range listB {
				if b.Name != "" {
					mapB[b.Name] = b
				}
			}

			names := make([]string, 0, len(mapA)+len(mapB))
			for name := range mapA {
				names = append(names, name)
			}
			for name := range mapB {
				if _, exists := mapA[name]; !exists {
					names = append(names, name)
				}
			}
			slices.Sort(names)

			canDescend := task.depth < maxWalkDepth
			for _, name := range names {
				if len(entries) >= maxExpandedEntries {
					break
				}
				relPath := name
				if task.prefix != "" {
					relPath = task.prefix + "/" + name
				}
				entA, inA := mapA[name]
				entB, inB := mapB[name]

				switch {
				case !inA && inB:
					entries = append(entries, makeDiffEntry(relPath, name, entB.IsDir, DiffAdded, CachedEntry{}, entB))
					if canDescend && entB.IsDir && entB.ObjectID != "" && entB.ObjectID != task.oidB {
						nextFrontier = append(nextFrontier, pairTask{prefix: relPath, oidA: "", oidB: entB.ObjectID, depth: task.depth + 1})
					}

				case inA && !inB:
					entries = append(entries, makeDiffEntry(relPath, name, entA.IsDir, DiffDeleted, entA, CachedEntry{}))
					if canDescend && entA.IsDir && entA.ObjectID != "" && entA.ObjectID != task.oidA {
						nextFrontier = append(nextFrontier, pairTask{prefix: relPath, oidA: entA.ObjectID, oidB: "", depth: task.depth + 1})
					}

				case inA && inB:
					if entA.IsDir && entB.IsDir {
						if canDescend && entA.ObjectID != entB.ObjectID {
							nextFrontier = append(nextFrontier, pairTask{prefix: relPath, oidA: entA.ObjectID, oidB: entB.ObjectID, depth: task.depth + 1})
						}
					} else if entA.IsDir != entB.IsDir {
						entries = append(entries, makeDiffEntry(relPath, name, entA.IsDir, DiffDeleted, entA, CachedEntry{}))
						entries = append(entries, makeDiffEntry(relPath, name, entB.IsDir, DiffAdded, CachedEntry{}, entB))
						if canDescend && entA.IsDir && entA.ObjectID != "" && entA.ObjectID != task.oidA {
							nextFrontier = append(nextFrontier, pairTask{prefix: relPath, oidA: entA.ObjectID, oidB: "", depth: task.depth + 1})
						}
						if canDescend && entB.IsDir && entB.ObjectID != "" && entB.ObjectID != task.oidB {
							nextFrontier = append(nextFrontier, pairTask{prefix: relPath, oidA: "", oidB: entB.ObjectID, depth: task.depth + 1})
						}
					} else {
						if entA.ObjectID != entB.ObjectID || entA.Size != entB.Size {
							entries = append(entries, makeDiffEntry(relPath, name, false, DiffModified, entA, entB))
						}
					}
				}
			}
		}
		frontier = nextFrontier
	}

	if len(newlyFetched) > 0 {
		_ = e.mergeDiskCacheEntries(repoID, newlyFetched)
	}

	return summarizeDiffEntries(entries), nil
}

// CompareSeededSnapshots computes a DiffSummary from in-memory seeded SearchItems (for headless tests & offline preview).
func (e *Engine) CompareSeededSnapshots(
	repoID string,
	source kopia.SourceInfo,
	snapA, snapB *kopia.Snapshot,
) DiffSummary {
	if snapA == nil || snapB == nil || snapA.ID == snapB.ID {
		return DiffSummary{}
	}

	e.mu.RLock()
	rc, ok := e.repos[repoID]
	var items []SearchItem
	if ok {
		items = slices.Clone(rc.items)
	}
	e.mu.RUnlock()

	findAtSnap := func(it SearchItem, sn *kopia.Snapshot) (VersionRecord, bool) {
		for _, v := range it.History {
			if (sn.ID != "" && v.SnapshotID == sn.ID) || (sn.RootID != "" && v.RootID == sn.RootID) {
				return v, true
			}
		}
		return VersionRecord{}, false
	}

	var entries []DiffEntry
	srcKey := source.String()
	for _, it := range items {
		if srcKey != "" && it.Source.String() != srcKey {
			continue
		}
		verA, inA := findAtSnap(it, snapA)
		verB, inB := findAtSnap(it, snapB)
		switch {
		case !inA && inB:
			entries = append(entries, DiffEntry{
				Path:        it.Path,
				Name:        it.Name,
				Ext:         it.Ext,
				IsDir:       it.IsDir,
				Kind:        DiffAdded,
				NewSize:     verB.Size,
				SizeDelta:   verB.Size,
				NewModTime:  verB.ModTime,
				NewObjectID: verB.ObjectID,
			})
		case inA && !inB:
			entries = append(entries, DiffEntry{
				Path:        it.Path,
				Name:        it.Name,
				Ext:         it.Ext,
				IsDir:       it.IsDir,
				Kind:        DiffDeleted,
				OldSize:     verA.Size,
				SizeDelta:   -verA.Size,
				OldModTime:  verA.ModTime,
				OldObjectID: verA.ObjectID,
			})
		case inA && inB:
			if verA.ObjectID != verB.ObjectID || verA.Size != verB.Size {
				entries = append(entries, DiffEntry{
					Path:        it.Path,
					Name:        it.Name,
					Ext:         it.Ext,
					IsDir:       it.IsDir,
					Kind:        DiffModified,
					OldSize:     verA.Size,
					NewSize:     verB.Size,
					SizeDelta:   verB.Size - verA.Size,
					OldModTime:  verA.ModTime,
					NewModTime:  verB.ModTime,
					OldObjectID: verA.ObjectID,
					NewObjectID: verB.ObjectID,
				})
			}
		}
	}

	return summarizeDiffEntries(entries)
}

func makeDiffEntry(relPath, name string, isDir bool, kind DiffKind, entA, entB CachedEntry) DiffEntry {
	ext := ""
	if !isDir {
		if raw := path.Ext(name); len(raw) > 1 {
			ext = strings.ToLower(raw[1:])
		}
	}
	var modA, modB time.Time
	if entA.ModUnix > 0 {
		modA = time.Unix(0, entA.ModUnix)
	}
	if entB.ModUnix > 0 {
		modB = time.Unix(0, entB.ModUnix)
	}
	var delta int64
	switch kind {
	case DiffAdded:
		delta = entB.Size
	case DiffDeleted:
		delta = -entA.Size
	case DiffModified:
		delta = entB.Size - entA.Size
	}
	return DiffEntry{
		Path:        relPath,
		Name:        name,
		Ext:         ext,
		IsDir:       isDir,
		Kind:        kind,
		OldSize:     entA.Size,
		NewSize:     entB.Size,
		SizeDelta:   delta,
		OldModTime:  modA,
		NewModTime:  modB,
		OldObjectID: entA.ObjectID,
		NewObjectID: entB.ObjectID,
	}
}

func summarizeDiffEntries(entries []DiffEntry) DiffSummary {
	slices.SortFunc(entries, func(a, b DiffEntry) int {
		return strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path))
	})
	var sum DiffSummary
	sum.Entries = entries
	for _, e := range entries {
		switch e.Kind {
		case DiffAdded:
			sum.AddedCount++
			if !e.IsDir {
				sum.AddedBytes += e.NewSize
				sum.NetDeltaBytes += e.NewSize
			}
		case DiffDeleted:
			sum.DeletedCount++
			if !e.IsDir {
				sum.RemovedBytes += e.OldSize
				sum.NetDeltaBytes -= e.OldSize
			}
		case DiffModified:
			sum.ModifiedCount++
			if !e.IsDir {
				if e.SizeDelta > 0 {
					sum.AddedBytes += e.SizeDelta
				} else if e.SizeDelta < 0 {
					sum.RemovedBytes += -e.SizeDelta
				}
				sum.NetDeltaBytes += e.SizeDelta
			}
		}
	}
	return sum
}
