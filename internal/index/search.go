package index

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// ItemKind filters files vs folders in search queries.
type ItemKind uint8

const (
	KindBoth ItemKind = iota
	KindFiles
	KindFolders
)

// StatusFilter filters items by whether they still exist in the latest snapshot.
type StatusFilter uint8

const (
	StatusAll StatusFilter = iota
	StatusOnDiskOnly
	StatusDeletedOnly
)

// SortField selects which column orders search results.
type SortField uint8

const (
	SortSize SortField = iota
	SortVersions
	SortLastSeen
	SortFirstSeen
	SortPath
	SortSource
)

// Query defines KopiaLens-style cross-snapshot / cross-repository search criteria.
type Query struct {
	Text         string
	PathPrefix   string // optional folder/path prefix or substring filter
	RepoID       string // empty = search across all indexed repositories
	SourceFilter string // empty = all sources, else SourceInfo.String()
	Kind         ItemKind
	Status       StatusFilter
	DeletedOnly  bool // legacy alias for Status == StatusDeletedOnly
	MinSize      int64
	MaxSize      int64
	MinVersions  int
	SeenAfter    time.Time // optional lower bound on snapshot occurrence time
	SeenBefore   time.Time // optional upper bound on snapshot occurrence time
	IncludeExts  string    // comma or space separated, e.g. "pdf, zip"
	ExcludeExts  string    // comma or space separated, e.g. "tmp, mp4"
	SortBy       SortField
	SortDesc     bool
	Offset       int
	Limit        int
}

// Result holds matched items along with aggregate totals prior to Limit truncation.
type Result struct {
	Items        []SearchItem
	TotalMatched int
	TotalBytes   int64
}

// Search executes an in-memory filtered and sorted query across indexed repositories.
func (e *Engine) Search(q Query) Result {
	tokens := splitQueryTokens(q.Text)
	normPrefix := strings.ReplaceAll(strings.TrimSpace(q.PathPrefix), "\\", "/")
	pathPrefix := strings.ToLower(strings.Trim(normPrefix, "/"))
	incExts := parseExtSet(q.IncludeExts)
	excExts := parseExtSet(q.ExcludeExts)

	status := q.Status
	if q.DeletedOnly {
		status = StatusDeletedOnly
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 1500
	}

	e.mu.RLock()
	var matched []SearchItem
	var totalBytes int64

	for id, rc := range e.repos {
		if q.RepoID != "" && id != q.RepoID {
			continue
		}
		for _, it := range rc.items {
			if q.Kind == KindFiles && it.IsDir {
				continue
			}
			if q.Kind == KindFolders && !it.IsDir {
				continue
			}
			if status == StatusDeletedOnly && it.OnDisk {
				continue
			}
			if status == StatusOnDiskOnly && !it.OnDisk {
				continue
			}
			if q.SourceFilter != "" && it.Source.String() != q.SourceFilter {
				continue
			}
			if q.MinSize > 0 && it.LatestSize < q.MinSize {
				continue
			}
			if q.MaxSize > 0 && it.LatestSize > q.MaxSize {
				continue
			}
			if q.MinVersions > 1 && it.Versions < q.MinVersions {
				continue
			}
			if !q.SeenAfter.IsZero() || !q.SeenBefore.IsZero() {
				if !matchesTimeWindow(it, q.SeenAfter, q.SeenBefore) {
					continue
				}
			}
			if pathPrefix != "" {
				lowerPath := strings.ToLower(it.Path)
				if !strings.HasPrefix(lowerPath, pathPrefix) && !strings.Contains(lowerPath, pathPrefix+"/") {
					continue
				}
			}
			if len(incExts) > 0 {
				if it.IsDir || !incExts[it.Ext] {
					continue
				}
			}
			if len(excExts) > 0 && !it.IsDir && excExts[it.Ext] {
				continue
			}
			if len(tokens) > 0 && !matchesAllTokens(it.Path, tokens) {
				continue
			}
			matched = append(matched, it)
			if !it.IsDir {
				totalBytes += it.LatestSize
			}
		}
	}
	e.mu.RUnlock()

	sortResults(matched, q.SortBy, q.SortDesc)

	total := len(matched)
	offset := max(0, q.Offset)
	if offset >= total {
		matched = nil
	} else {
		end := min(total, offset+limit)
		matched = matched[offset:end]
	}
	return Result{
		Items:        matched,
		TotalMatched: total,
		TotalBytes:   totalBytes,
	}
}

func matchesTimeWindow(it SearchItem, after, before time.Time) bool {
	if len(it.History) > 0 {
		for _, v := range it.History {
			if !after.IsZero() && v.SnapshotTime.Before(after) {
				continue
			}
			if !before.IsZero() && v.SnapshotTime.After(before) {
				continue
			}
			return true
		}
		return false
	}
	if !after.IsZero() && it.LastSeen.Before(after) {
		return false
	}
	if !before.IsZero() && it.FirstSeen.After(before) {
		return false
	}
	return true
}

func splitQueryTokens(s string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func matchesAllTokens(p string, tokens []string) bool {
	lower := strings.ToLower(p)
	for _, tok := range tokens {
		if !strings.Contains(lower, tok) {
			return false
		}
	}
	return true
}

func parseExtSet(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	out := make(map[string]bool, len(parts))
	for _, p := range parts {
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(p), "."))
		if clean != "" {
			out[clean] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortResults(items []SearchItem, by SortField, desc bool) {
	slices.SortFunc(items, func(a, b SearchItem) int {
		var c int
		switch by {
		case SortSize:
			c = cmp.Compare(a.LatestSize, b.LatestSize)
		case SortVersions:
			c = cmp.Compare(a.Versions, b.Versions)
			if c == 0 {
				c = cmp.Compare(a.SnapshotCount, b.SnapshotCount)
			}
		case SortLastSeen:
			c = a.LastSeen.Compare(b.LastSeen)
		case SortFirstSeen:
			c = a.FirstSeen.Compare(b.FirstSeen)
		case SortSource:
			c = strings.Compare(a.Source.String(), b.Source.String())
		case SortPath:
			c = strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path))
		default:
			c = cmp.Compare(a.LatestSize, b.LatestSize)
		}
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
}
