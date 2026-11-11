package index

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"mygopia/internal/kopia"
)

const (
	cacheDirTagSignature = "Signature: 8a477f597d28d172789f06886806bc55"
	maxDotIgnoreBytes    = 1 << 20 // 1 MiB safety cap per ignore file
)

// LocalDiffOptions configures comparing a snapshot tree against a live local filesystem directory
// (modeling what the next Kopia snapshot of LocalPath would capture).
type LocalDiffOptions struct {
	LocalPath              string
	IgnoreRules            []string
	DotIgnoreFiles         []string
	IgnoreCacheDirectories bool
	MaxFileSize            int64
}

type compiledIgnoreRule struct {
	baseRel  string // slash-separated directory relative to root ("" for root policy)
	pattern  string // normalized slash-separated pattern without leading '!' or '/' or trailing '/'
	negated  bool
	anchored bool
	dirOnly  bool
	hasSlash bool
}

func parseIgnoreRules(baseRel string, lines []string) []compiledIgnoreRule {
	var out []compiledIgnoreRule
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r\n")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		} else {
			line = trimmed
		}

		var r compiledIgnoreRule
		r.baseRel = baseRel
		if strings.HasPrefix(line, "!") {
			r.negated = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			r.anchored = true
			line = strings.TrimLeft(line, "/")
		}
		line = strings.TrimSpace(strings.ReplaceAll(line, "\\", "/"))
		if line == "" {
			continue
		}
		r.pattern = line
		r.hasSlash = strings.Contains(line, "/")
		out = append(out, r)
	}
	return out
}

func isSafeBaseFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && filepath.Base(name) == name
}

func loadDotIgnoreRules(dirDiskPath, dirRel string, dotFiles []string) []compiledIgnoreRule {
	if len(dotFiles) == 0 {
		return nil
	}
	var out []compiledIgnoreRule
	for _, df := range dotFiles {
		df = strings.TrimSpace(df)
		if !isSafeBaseFilename(df) {
			continue
		}
		fullPath := filepath.Join(dirDiskPath, df)
		fi, err := os.Lstat(fullPath)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() <= 0 || fi.Size() > maxDotIgnoreBytes {
			continue
		}
		f, err := os.Open(fullPath)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(f, maxDotIgnoreBytes))
		_ = f.Close()
		if readErr != nil || len(data) == 0 {
			continue
		}
		var lines []string
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		out = append(out, parseIgnoreRules(dirRel, lines)...)
	}
	return out
}

func hasCacheDirTag(dirDiskPath string) bool {
	tagPath := filepath.Join(dirDiskPath, "CACHEDIR.TAG")
	fi, err := os.Lstat(tagPath)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(tagPath)
	if err != nil {
		return false
	}
	defer f.Close()
	var buf [64]byte
	n, _ := f.Read(buf[:])
	return strings.HasPrefix(string(buf[:n]), cacheDirTagSignature)
}

func isIgnoredByRules(relPath, baseName string, isDir bool, rules []compiledIgnoreRule) bool {
	ignored := false
	for _, r := range rules {
		if r.dirOnly && !isDir {
			continue
		}
		subRel := relPath
		if r.baseRel != "" {
			prefix := r.baseRel + "/"
			if !strings.HasPrefix(relPath, prefix) {
				continue
			}
			subRel = relPath[len(prefix):]
		}
		if matchOneIgnoreRule(r, subRel, baseName) {
			ignored = !r.negated
		}
	}
	return ignored
}

func matchOneIgnoreRule(r compiledIgnoreRule, subRel, baseName string) bool {
	if !r.anchored && !r.hasSlash {
		return globMatchSegments(r.pattern, baseName)
	}
	if r.anchored {
		return globMatchPath(r.pattern, subRel)
	}
	// Non-anchored pattern with slash: matches from rule base or after any '**/'.
	if globMatchPath(r.pattern, subRel) {
		return true
	}
	if strings.HasPrefix(r.pattern, "**/") {
		return globMatchPath(r.pattern[3:], subRel)
	}
	return false
}

func globMatchPath(pattern, target string) bool {
	pSegs := strings.Split(pattern, "/")
	tSegs := strings.Split(target, "/")
	return matchSegSlice(pSegs, tSegs)
}

func matchSegSlice(pSegs, tSegs []string) bool {
	for len(pSegs) > 0 {
		seg := pSegs[0]
		if seg == "**" {
			for len(pSegs) > 1 && pSegs[1] == "**" {
				pSegs = pSegs[1:]
			}
			if len(pSegs) == 1 {
				return true
			}
			for i := 0; i <= len(tSegs); i++ {
				if matchSegSlice(pSegs[1:], tSegs[i:]) {
					return true
				}
			}
			return false
		}
		if len(tSegs) == 0 {
			return false
		}
		if !globMatchSegments(seg, tSegs[0]) {
			return false
		}
		pSegs = pSegs[1:]
		tSegs = tSegs[1:]
	}
	return len(tSegs) == 0
}

func globMatchSegments(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	if err != nil {
		return pattern == name
	}
	return ok
}

// sameSnapshotModTime compares a snapshot's stored ModTime (Unix nanoseconds) against a local file's ModTime.
// A 1ms tolerance absorbs sub-millisecond rounding across filesystems and JSON serialization while catching edits.
func sameSnapshotModTime(snapUnixNano, localUnixNano int64) bool {
	if snapUnixNano <= 0 || localUnixNano <= 0 {
		return snapUnixNano == localUnixNano
	}
	var diff uint64
	if snapUnixNano >= localUnixNano {
		diff = uint64(snapUnixNano) - uint64(localUnixNano)
	} else {
		diff = uint64(localUnixNano) - uint64(snapUnixNano)
	}
	return diff <= uint64(time.Millisecond)
}

func readFilteredLocalDir(
	dirDiskPath string,
	dirRel string,
	inheritedRules []compiledIgnoreRule,
	opts LocalDiffOptions,
) ([]CachedEntry, []compiledIgnoreRule, error) {
	localRules := inheritedRules
	if extra := loadDotIgnoreRules(dirDiskPath, dirRel, opts.DotIgnoreFiles); len(extra) > 0 {
		localRules = make([]compiledIgnoreRule, 0, len(inheritedRules)+len(extra))
		localRules = append(localRules, inheritedRules...)
		localRules = append(localRules, extra...)
	}

	rawEntries, err := os.ReadDir(dirDiskPath)
	if err != nil {
		// Permission-denied or transient unreadable subdirectories are skipped cleanly rather than aborting the whole diff.
		if os.IsPermission(err) || os.IsNotExist(err) {
			return nil, localRules, nil
		}
		return nil, localRules, err
	}

	dotIgnoreSet := make(map[string]bool, len(opts.DotIgnoreFiles))
	for _, df := range opts.DotIgnoreFiles {
		if df != "" {
			dotIgnoreSet[df] = true
		}
	}

	out := make([]CachedEntry, 0, len(rawEntries))
	for _, de := range rawEntries {
		name := de.Name()
		if name == "" {
			continue
		}
		typ := de.Type()
		// Skip sockets, named pipes, and device files (Kopia does not snapshot special IPC/device nodes).
		if typ&(os.ModeSocket|os.ModeNamedPipe|os.ModeDevice|os.ModeCharDevice) != 0 {
			continue
		}

		info, err := de.Info()
		if err != nil {
			continue
		}
		isSymlink := info.Mode()&os.ModeSymlink != 0
		isDir := !isSymlink && info.IsDir()
		relPath := name
		if dirRel != "" {
			relPath = dirRel + "/" + name
		}

		// Never ignore the .kopiaignore file itself unless explicitly matched by rules
		if !dotIgnoreSet[name] && isIgnoredByRules(relPath, name, isDir, localRules) {
			continue
		}
		if isDir && opts.IgnoreCacheDirectories && hasCacheDirTag(filepath.Join(dirDiskPath, name)) {
			continue
		}
		var size int64
		if !isDir && !isSymlink {
			size = info.Size()
			if opts.MaxFileSize > 0 && size > opts.MaxFileSize {
				continue
			}
		}
		var modNano int64
		if !info.ModTime().IsZero() {
			modNano = info.ModTime().UnixNano()
		}
		out = append(out, CachedEntry{
			Name:      name,
			IsDir:     isDir,
			IsSymlink: isSymlink,
			Size:      size,
			ModUnix:   modNano,
		})
	}
	return out, localRules, nil
}

// CompareRootWithLocalDir computes the recursive diff between a historical snapshot rootA (older/base)
// and a live local directory on disk (newer/target, modeling the next backup state).
func (e *Engine) CompareRootWithLocalDir(
	ctx context.Context,
	repoID string,
	cli *kopia.Client,
	rootA string,
	opts LocalDiffOptions,
) (DiffSummary, error) {
	if rootA == "" {
		return DiffSummary{}, fmt.Errorf("snapshot root object ID is empty")
	}
	if cli == nil {
		return DiffSummary{}, fmt.Errorf("kopia client unavailable")
	}
	cleanLocal := filepath.Clean(strings.TrimSpace(opts.LocalPath))
	st, err := os.Stat(cleanLocal)
	if err != nil {
		return DiffSummary{}, fmt.Errorf("local directory unavailable (%s): %w", cleanLocal, err)
	}
	if !st.IsDir() {
		return DiffSummary{}, fmt.Errorf("local path is not a directory: %s", cleanLocal)
	}
	opts.LocalPath = cleanLocal
	if len(opts.DotIgnoreFiles) == 0 {
		opts.DotIgnoreFiles = []string{".kopiaignore"}
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
		return fetchDirectoriesConcurrent(ctx, cli, missing, 8, func(oid string, entries []CachedEntry) {
			cacheMu.Lock()
			dirCache[oid] = entries
			newlyFetched[oid] = entries
			cacheMu.Unlock()
		})
	}

	rootRules := parseIgnoreRules("", opts.IgnoreRules)

	type localPairTask struct {
		prefix    string
		oidA      string // empty if subtree only exists on local disk (Added)
		localPath string // empty if subtree only exists in Snapshot A (Deleted)
		rules     []compiledIgnoreRule
		depth     int
	}

	frontier := []localPairTask{{
		prefix:    "",
		oidA:      rootA,
		localPath: cleanLocal,
		rules:     rootRules,
		depth:     0,
	}}
	var entries []DiffEntry

	for len(frontier) > 0 && len(entries) < maxExpandedEntries {
		if err := ctx.Err(); err != nil {
			return DiffSummary{}, err
		}

		var needOIDs []string
		for _, task := range frontier {
			if task.oidA != "" {
				needOIDs = append(needOIDs, task.oidA)
			}
		}
		if err := ensureDirsLoaded(needOIDs); err != nil {
			return DiffSummary{}, err
		}

		var nextFrontier []localPairTask
		for _, task := range frontier {
			if err := ctx.Err(); err != nil {
				return DiffSummary{}, err
			}

			var listA []CachedEntry
			if task.oidA != "" {
				listA = dirCache[task.oidA]
			}
			var (
				listLocal []CachedEntry
				dirRules  = task.rules
			)
			if task.localPath != "" {
				var lerr error
				listLocal, dirRules, lerr = readFilteredLocalDir(task.localPath, task.prefix, task.rules, opts)
				if lerr != nil {
					return DiffSummary{}, lerr
				}
			}

			mapA := make(map[string]CachedEntry, len(listA))
			for _, a := range listA {
				if a.Name != "" {
					mapA[a.Name] = a
				}
			}
			mapB := make(map[string]CachedEntry, len(listLocal))
			for _, b := range listLocal {
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
					if canDescend && entB.IsDir && task.localPath != "" {
						nextFrontier = append(nextFrontier, localPairTask{
							prefix:    relPath,
							oidA:      "",
							localPath: filepath.Join(task.localPath, name),
							rules:     dirRules,
							depth:     task.depth + 1,
						})
					}

				case inA && !inB:
					entries = append(entries, makeDiffEntry(relPath, name, entA.IsDir, DiffDeleted, entA, CachedEntry{}))
					if canDescend && entA.IsDir && entA.ObjectID != "" && entA.ObjectID != task.oidA {
						nextFrontier = append(nextFrontier, localPairTask{
							prefix:    relPath,
							oidA:      entA.ObjectID,
							localPath: "",
							rules:     dirRules,
							depth:     task.depth + 1,
						})
					}

				case inA && inB:
					if entA.IsDir && entB.IsDir {
						if canDescend {
							nextFrontier = append(nextFrontier, localPairTask{
								prefix:    relPath,
								oidA:      entA.ObjectID,
								localPath: filepath.Join(task.localPath, name),
								rules:     dirRules,
								depth:     task.depth + 1,
							})
						}
					} else if entA.IsDir != entB.IsDir {
						entries = append(entries, makeDiffEntry(relPath, name, entA.IsDir, DiffDeleted, entA, CachedEntry{}))
						entries = append(entries, makeDiffEntry(relPath, name, entB.IsDir, DiffAdded, CachedEntry{}, entB))
						if canDescend && entA.IsDir && entA.ObjectID != "" && entA.ObjectID != task.oidA {
							nextFrontier = append(nextFrontier, localPairTask{
								prefix:    relPath,
								oidA:      entA.ObjectID,
								localPath: "",
								rules:     dirRules,
								depth:     task.depth + 1,
							})
						}
						if canDescend && entB.IsDir && task.localPath != "" {
							nextFrontier = append(nextFrontier, localPairTask{
								prefix:    relPath,
								oidA:      "",
								localPath: filepath.Join(task.localPath, name),
								rules:     dirRules,
								depth:     task.depth + 1,
							})
						}
					} else if entA.IsSymlink || entB.IsSymlink {
						if entA.IsSymlink != entB.IsSymlink {
							entries = append(entries, makeDiffEntry(relPath, name, false, DiffModified, entA, entB))
						}
					} else {
						if entA.Size != entB.Size || !sameSnapshotModTime(entA.ModUnix, entB.ModUnix) {
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

// CompareSeededSnapshotWithLocalDir compares an in-memory seeded snapshot against a live local directory
// (used by headless UI tests and offline previews).
func (e *Engine) CompareSeededSnapshotWithLocalDir(
	repoID string,
	source kopia.SourceInfo,
	snapA *kopia.Snapshot,
	opts LocalDiffOptions,
) (DiffSummary, error) {
	if snapA == nil {
		return DiffSummary{}, nil
	}
	cleanLocal := filepath.Clean(strings.TrimSpace(opts.LocalPath))
	st, err := os.Stat(cleanLocal)
	if err != nil {
		return DiffSummary{}, err
	}
	if !st.IsDir() {
		return DiffSummary{}, fmt.Errorf("local path is not a directory: %s", cleanLocal)
	}
	opts.LocalPath = cleanLocal
	if len(opts.DotIgnoreFiles) == 0 {
		opts.DotIgnoreFiles = []string{".kopiaignore"}
	}

	e.mu.RLock()
	rc, ok := e.repos[repoID]
	var items []SearchItem
	if ok {
		items = slices.Clone(rc.items)
	}
	e.mu.RUnlock()

	type snapRec struct {
		item SearchItem
		ver  VersionRecord
	}
	snapMap := make(map[string]snapRec)
	srcKey := source.String()
	for _, it := range items {
		if srcKey != "" && it.Source.String() != srcKey {
			continue
		}
		for _, v := range it.History {
			if (snapA.ID != "" && v.SnapshotID == snapA.ID) || (snapA.RootID != "" && v.RootID == snapA.RootID) {
				snapMap[it.Path] = snapRec{item: it, ver: v}
				// Synthesize ancestor directory entries in case seeded test items only listed leaf files.
				for dir := path.Dir(it.Path); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
					if _, exists := snapMap[dir]; !exists {
						snapMap[dir] = snapRec{
							item: SearchItem{Path: dir, Name: path.Base(dir), IsDir: true, Source: it.Source},
							ver:  VersionRecord{SnapshotID: v.SnapshotID, SnapshotTime: v.SnapshotTime},
						}
					}
				}
				break
			}
		}
	}

	// Walk local directory honoring ignore rules with depth & entry safety caps
	localMap := make(map[string]CachedEntry)
	type walkDir struct {
		prefix string
		disk   string
		rules  []compiledIgnoreRule
		depth  int
	}
	queue := []walkDir{{prefix: "", disk: cleanLocal, rules: parseIgnoreRules("", opts.IgnoreRules), depth: 0}}
	for len(queue) > 0 && len(localMap) < maxExpandedEntries {
		cur := queue[0]
		queue = queue[1:]
		children, rules, err := readFilteredLocalDir(cur.disk, cur.prefix, cur.rules, opts)
		if err != nil {
			return DiffSummary{}, err
		}
		for _, ch := range children {
			if len(localMap) >= maxExpandedEntries {
				break
			}
			rel := ch.Name
			if cur.prefix != "" {
				rel = cur.prefix + "/" + ch.Name
			}
			localMap[rel] = ch
			if ch.IsDir && cur.depth < maxWalkDepth {
				queue = append(queue, walkDir{
					prefix: rel,
					disk:   filepath.Join(cur.disk, ch.Name),
					rules:  rules,
					depth:  cur.depth + 1,
				})
			}
		}
	}

	allPaths := make([]string, 0, len(snapMap)+len(localMap))
	for p := range snapMap {
		allPaths = append(allPaths, p)
	}
	for p := range localMap {
		if _, exists := snapMap[p]; !exists {
			allPaths = append(allPaths, p)
		}
	}
	slices.Sort(allPaths)

	var entries []DiffEntry
	for _, relPath := range allPaths {
		sr, inA := snapMap[relPath]
		lb, inB := localMap[relPath]
		var entA CachedEntry
		if inA {
			var modNano int64
			if !sr.ver.ModTime.IsZero() {
				modNano = sr.ver.ModTime.UnixNano()
			} else if !sr.ver.SnapshotTime.IsZero() {
				modNano = sr.ver.SnapshotTime.UnixNano()
			}
			entA = CachedEntry{
				Name:     sr.item.Name,
				IsDir:    sr.item.IsDir,
				Size:     sr.ver.Size,
				ModUnix:  modNano,
				ObjectID: sr.ver.ObjectID,
			}
		}
		switch {
		case !inA && inB:
			entries = append(entries, makeDiffEntry(relPath, lb.Name, lb.IsDir, DiffAdded, CachedEntry{}, lb))
		case inA && !inB:
			entries = append(entries, makeDiffEntry(relPath, entA.Name, entA.IsDir, DiffDeleted, entA, CachedEntry{}))
		case inA && inB:
			if entA.IsDir != lb.IsDir {
				entries = append(entries, makeDiffEntry(relPath, entA.Name, entA.IsDir, DiffDeleted, entA, CachedEntry{}))
				entries = append(entries, makeDiffEntry(relPath, lb.Name, lb.IsDir, DiffAdded, CachedEntry{}, lb))
			} else if !entA.IsDir {
				if entA.Size != lb.Size || !sameSnapshotModTime(entA.ModUnix, lb.ModUnix) {
					entries = append(entries, makeDiffEntry(relPath, lb.Name, false, DiffModified, entA, lb))
				}
			}
		}
	}

	return summarizeDiffEntries(entries), nil
}
