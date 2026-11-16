// @scope.file index.store
// @hash sha256:a2c92662d97f482b
// @implements spec.dag_index sha256:c155f3ed2269ad94
// @ref default go.etcd.io/bbolt@1.5.0
// @ref default github.com/klauspost/compress@1.20.4
// @ref default config.store sha256:b49642aa028d360a
// @ref default index.engine sha256:1aea8ec7725d31ae
// @ref default kopia.types sha256:06dd81559d688041
// Bounded mmap B+Tree (`bbolt`) + S2-compressed binary store with idle auto-unmap and disk budget eviction.

package index

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/s2"
	bolt "go.etcd.io/bbolt"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
)

const (
	boltSchemaVersion uint64 = 1

	codecRaw byte = 0x01
	codecS2  byte = 0x02

	s2MinCompressBytes = 96
	maxDecodedCount    = 10_000_000
	storeIdleLease     = 80 * time.Millisecond
)

var (
	bucketDirs    = []byte("dirs")
	bucketDirRank = []byte("dir_rank")
	bucketHist    = []byte("hist")
	bucketMeta    = []byte("meta")

	metaKeySchema       = []byte("schema")
	metaKeyPayloadBytes = []byte("payload_bytes")
	metaKeyCatalog      = []byte("catalog")

	errCorruptCache = errors.New("corrupt cache payload")
)

type repoStore struct {
	repoID string
	path   string

	compactMu sync.RWMutex
	mu        sync.Mutex
	db        *bolt.DB
	activeOps int
	idleSeq   uint64
	idleTimer *time.Timer
}

func sha256Sum(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}

func wrapCompressed(raw []byte) []byte {
	if len(raw) < s2MinCompressBytes {
		out := make([]byte, 1+len(raw))
		out[0] = codecRaw
		copy(out[1:], raw)
		return out
	}
	comp := s2.Encode(nil, raw)
	if len(comp)+1 >= len(raw) {
		out := make([]byte, 1+len(raw))
		out[0] = codecRaw
		copy(out[1:], raw)
		return out
	}
	out := make([]byte, 1+len(comp))
	out[0] = codecS2
	copy(out[1:], comp)
	return out
}

func unwrapCompressed(buf []byte) ([]byte, error) {
	if len(buf) == 0 {
		return nil, errCorruptCache
	}
	switch buf[0] {
	case codecRaw:
		return buf[1:], nil
	case codecS2:
		return s2.Decode(nil, buf[1:])
	default:
		return nil, errCorruptCache
	}
}

func appendLenStr(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func readUvarintAt(src []byte, pos int) (uint64, int, error) {
	if pos >= len(src) {
		return 0, pos, errCorruptCache
	}
	v, n := binary.Uvarint(src[pos:])
	if n <= 0 {
		return 0, pos, errCorruptCache
	}
	return v, pos + n, nil
}

func readVarintAt(src []byte, pos int) (int64, int, error) {
	if pos >= len(src) {
		return 0, pos, errCorruptCache
	}
	v, n := binary.Varint(src[pos:])
	if n <= 0 {
		return 0, pos, errCorruptCache
	}
	return v, pos + n, nil
}

func readLenStrAt(src []byte, pos int) (string, int, error) {
	n, next, err := readUvarintAt(src, pos)
	if err != nil {
		return "", pos, err
	}
	if n > uint64(len(src)-next) {
		return "", pos, errCorruptCache
	}
	end := next + int(n)
	return string(src[next:end]), end, nil
}

// encodeCachedEntries packs a slice of CachedEntry using delta-encoded timestamps and S2 compression.
func encodeCachedEntries(entries []CachedEntry) []byte {
	raw := make([]byte, 0, 16+len(entries)*36)
	raw = binary.AppendUvarint(raw, uint64(len(entries)))
	var prevMod int64
	for _, e := range entries {
		var flags byte
		if e.IsDir {
			flags |= 1
		}
		if e.IsSymlink {
			flags |= 2
		}
		raw = append(raw, flags)
		raw = binary.AppendVarint(raw, e.Size)
		raw = binary.AppendVarint(raw, e.ModUnix-prevMod)
		prevMod = e.ModUnix
		raw = appendLenStr(raw, e.Name)
		raw = appendLenStr(raw, e.ObjectID)
	}
	return wrapCompressed(raw)
}

func decodeCachedEntries(buf []byte) ([]CachedEntry, error) {
	raw, err := unwrapCompressed(buf)
	if err != nil {
		return nil, err
	}
	// Copy raw slice when uncompressed so strings never retain references into an unmapped bbolt page.
	if len(buf) > 0 && buf[0] == codecRaw {
		raw = slices.Clone(raw)
	}
	count, pos, err := readUvarintAt(raw, 0)
	if err != nil || count > maxDecodedCount {
		return nil, errCorruptCache
	}
	out := make([]CachedEntry, int(count))
	var prevMod int64
	for i := range int(count) {
		if pos >= len(raw) {
			return nil, errCorruptCache
		}
		flags := raw[pos]
		pos++
		size, p1, err := readVarintAt(raw, pos)
		if err != nil {
			return nil, err
		}
		modDelta, p2, err := readVarintAt(raw, p1)
		if err != nil {
			return nil, err
		}
		prevMod += modDelta
		name, p3, err := readLenStrAt(raw, p2)
		if err != nil {
			return nil, err
		}
		oid, p4, err := readLenStrAt(raw, p3)
		if err != nil {
			return nil, err
		}
		pos = p4
		out[i] = CachedEntry{
			Name:      name,
			IsDir:     flags&1 != 0,
			IsSymlink: flags&2 != 0,
			Size:      size,
			ModUnix:   prevMod,
			ObjectID:  oid,
		}
	}
	return out, nil
}

func encodeVersionHistory(hist []VersionRecord) []byte {
	raw := make([]byte, 0, 16+len(hist)*48)
	raw = binary.AppendUvarint(raw, uint64(len(hist)))
	var prevSnapNano, prevModNano int64
	for _, v := range hist {
		snapNano := v.SnapshotTime.UnixNano()
		if v.SnapshotTime.IsZero() {
			snapNano = 0
		}
		modNano := v.ModTime.UnixNano()
		if v.ModTime.IsZero() {
			modNano = 0
		}
		raw = binary.AppendVarint(raw, snapNano-prevSnapNano)
		prevSnapNano = snapNano
		raw = binary.AppendVarint(raw, modNano-prevModNano)
		prevModNano = modNano
		raw = binary.AppendVarint(raw, v.Size)
		raw = appendLenStr(raw, v.SnapshotID)
		raw = appendLenStr(raw, v.RootID)
		raw = appendLenStr(raw, v.ObjectID)
	}
	return wrapCompressed(raw)
}

func decodeVersionHistory(buf []byte) ([]VersionRecord, error) {
	raw, err := unwrapCompressed(buf)
	if err != nil {
		return nil, err
	}
	if len(buf) > 0 && buf[0] == codecRaw {
		raw = slices.Clone(raw)
	}
	count, pos, err := readUvarintAt(raw, 0)
	if err != nil || count > maxDecodedCount {
		return nil, errCorruptCache
	}
	out := make([]VersionRecord, int(count))
	var prevSnapNano, prevModNano int64
	for i := range int(count) {
		snapDelta, p1, err := readVarintAt(raw, pos)
		if err != nil {
			return nil, err
		}
		prevSnapNano += snapDelta
		modDelta, p2, err := readVarintAt(raw, p1)
		if err != nil {
			return nil, err
		}
		prevModNano += modDelta
		size, p3, err := readVarintAt(raw, p2)
		if err != nil {
			return nil, err
		}
		snapID, p4, err := readLenStrAt(raw, p3)
		if err != nil {
			return nil, err
		}
		rootID, p5, err := readLenStrAt(raw, p4)
		if err != nil {
			return nil, err
		}
		oid, p6, err := readLenStrAt(raw, p5)
		if err != nil {
			return nil, err
		}
		pos = p6
		var snapTime, modTime time.Time
		if prevSnapNano != 0 {
			snapTime = time.Unix(0, prevSnapNano).UTC()
		}
		if prevModNano != 0 {
			modTime = time.Unix(0, prevModNano).UTC()
		}
		out[i] = VersionRecord{
			SnapshotID:   snapID,
			SnapshotTime: snapTime,
			RootID:       rootID,
			ObjectID:     oid,
			Size:         size,
			ModTime:      modTime,
		}
	}
	return out, nil
}

func encodeSearchCatalog(stats RepoIndexStats, items []SearchItem) []byte {
	srcMap := make(map[kopia.SourceInfo]uint64, 8)
	var sources []kopia.SourceInfo
	for i := range items {
		s := items[i].Source
		if _, ok := srcMap[s]; !ok {
			srcMap[s] = uint64(len(sources))
			sources = append(sources, s)
		}
	}

	raw := make([]byte, 0, 128+len(items)*64)
	raw = appendLenStr(raw, stats.RepoID)
	raw = appendLenStr(raw, stats.RepoName)
	var updatedNano int64
	if !stats.UpdatedAt.IsZero() {
		updatedNano = stats.UpdatedAt.UnixNano()
	}
	raw = binary.AppendVarint(raw, updatedNano)
	raw = binary.AppendVarint(raw, int64(stats.SnapshotCount))
	raw = binary.AppendVarint(raw, int64(stats.SourceCount))
	raw = binary.AppendVarint(raw, int64(stats.FileCount))
	raw = binary.AppendVarint(raw, int64(stats.FolderCount))
	raw = binary.AppendVarint(raw, int64(stats.DeletedCount))
	raw = binary.AppendVarint(raw, int64(stats.CachedDirs))

	raw = binary.AppendUvarint(raw, uint64(len(sources)))
	for _, s := range sources {
		raw = appendLenStr(raw, s.Host)
		raw = appendLenStr(raw, s.UserName)
		raw = appendLenStr(raw, s.Path)
	}

	raw = binary.AppendUvarint(raw, uint64(len(items)))
	for i := range items {
		it := &items[i]
		var flags byte
		if it.IsDir {
			flags |= 1
		}
		if it.OnDisk {
			flags |= 2
		}
		raw = append(raw, flags)
		raw = binary.AppendUvarint(raw, srcMap[it.Source])
		raw = appendLenStr(raw, it.Path)
		raw = appendLenStr(raw, it.Name)
		raw = appendLenStr(raw, it.Ext)
		raw = binary.AppendVarint(raw, it.LatestSize)
		raw = binary.AppendVarint(raw, it.MaxSize)
		raw = binary.AppendVarint(raw, int64(it.Versions))
		raw = binary.AppendVarint(raw, int64(it.SnapshotCount))
		var firstNano, lastNano int64
		if !it.FirstSeen.IsZero() {
			firstNano = it.FirstSeen.UnixNano()
		}
		if !it.LastSeen.IsZero() {
			lastNano = it.LastSeen.UnixNano()
		}
		raw = binary.AppendVarint(raw, firstNano)
		raw = binary.AppendVarint(raw, lastNano)
		raw = appendLenStr(raw, it.LatestObjectID)
		raw = appendLenStr(raw, it.LatestSnapshotID)
		raw = appendLenStr(raw, it.LatestRootID)
	}
	return wrapCompressed(raw)
}

func decodeSearchCatalog(buf []byte) (RepoIndexStats, []SearchItem, error) {
	raw, err := unwrapCompressed(buf)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	if len(buf) > 0 && buf[0] == codecRaw {
		raw = slices.Clone(raw)
	}
	pos := 0
	repoID, pos, err := readLenStrAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	repoName, pos, err := readLenStrAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	updatedNano, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	snapCnt, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	srcCnt, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	fileCnt, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	folderCnt, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	delCnt, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}
	cachedDirs, pos, err := readVarintAt(raw, pos)
	if err != nil {
		return RepoIndexStats{}, nil, err
	}

	numSources, pos, err := readUvarintAt(raw, pos)
	if err != nil || numSources > 100_000 {
		return RepoIndexStats{}, nil, errCorruptCache
	}
	sources := make([]kopia.SourceInfo, int(numSources))
	for i := range int(numSources) {
		h, p1, err := readLenStrAt(raw, pos)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		u, p2, err := readLenStrAt(raw, p1)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		pa, p3, err := readLenStrAt(raw, p2)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		pos = p3
		sources[i] = kopia.SourceInfo{Host: h, UserName: u, Path: pa}
	}

	numItems, pos, err := readUvarintAt(raw, pos)
	if err != nil || numItems > maxDecodedCount {
		return RepoIndexStats{}, nil, errCorruptCache
	}
	items := make([]SearchItem, int(numItems))
	for i := range int(numItems) {
		if pos >= len(raw) {
			return RepoIndexStats{}, nil, errCorruptCache
		}
		flags := raw[pos]
		pos++
		srcIdx, p1, err := readUvarintAt(raw, pos)
		if err != nil || srcIdx >= uint64(len(sources)) {
			return RepoIndexStats{}, nil, errCorruptCache
		}
		relPath, p2, err := readLenStrAt(raw, p1)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		name, p3, err := readLenStrAt(raw, p2)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		ext, p4, err := readLenStrAt(raw, p3)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		latestSize, p5, err := readVarintAt(raw, p4)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		maxSize, p6, err := readVarintAt(raw, p5)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		vers, p7, err := readVarintAt(raw, p6)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		snaps, p8, err := readVarintAt(raw, p7)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		firstNano, p9, err := readVarintAt(raw, p8)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		lastNano, p10, err := readVarintAt(raw, p9)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		latestOID, p11, err := readLenStrAt(raw, p10)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		latestSnapID, p12, err := readLenStrAt(raw, p11)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		latestRootID, p13, err := readLenStrAt(raw, p12)
		if err != nil {
			return RepoIndexStats{}, nil, err
		}
		pos = p13

		var firstSeen, lastSeen time.Time
		if firstNano != 0 {
			firstSeen = time.Unix(0, firstNano)
		}
		if lastNano != 0 {
			lastSeen = time.Unix(0, lastNano)
		}
		items[i] = SearchItem{
			RepoID:           repoID,
			RepoName:         repoName,
			Source:           sources[srcIdx],
			Path:             relPath,
			Name:             name,
			Ext:              ext,
			IsDir:            flags&1 != 0,
			OnDisk:           flags&2 != 0,
			LatestSize:       latestSize,
			MaxSize:          maxSize,
			Versions:         int(vers),
			SnapshotCount:    int(snaps),
			FirstSeen:        firstSeen,
			LastSeen:         lastSeen,
			LatestObjectID:   latestOID,
			LatestSnapshotID: latestSnapID,
			LatestRootID:     latestRootID,
		}
	}

	var updatedAt time.Time
	if updatedNano != 0 {
		updatedAt = time.Unix(0, updatedNano)
	}
	stats := RepoIndexStats{
		RepoID:        repoID,
		RepoName:      repoName,
		UpdatedAt:     updatedAt,
		SnapshotCount: int(snapCnt),
		SourceCount:   int(srcCnt),
		FileCount:     int(fileCnt),
		FolderCount:   int(folderCnt),
		DeletedCount:  int(delCnt),
		CachedDirs:    int(cachedDirs),
	}
	return stats, items, nil
}

func putUint64Meta(b *bolt.Bucket, key []byte, v uint64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return b.Put(key, buf)
}

func getUint64Meta(b *bolt.Bucket, key []byte) uint64 {
	raw := b.Get(key)
	if len(raw) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}

func openBoltFile(path string) (*bolt.DB, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{
		Timeout:      2 * time.Second,
		FreelistType: bolt.FreelistMapType,
	})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists(bucketMeta)
		if err != nil {
			return err
		}
		ver := getUint64Meta(meta, metaKeySchema)
		if ver != boltSchemaVersion {
			for _, name := range [][]byte{bucketDirs, bucketDirRank, bucketHist} {
				if tx.Bucket(name) != nil {
					_ = tx.DeleteBucket(name)
				}
				if _, err := tx.CreateBucket(name); err != nil {
					return err
				}
			}
			if err := meta.Delete(metaKeyCatalog); err != nil {
				return err
			}
			if err := putUint64Meta(meta, metaKeySchema, boltSchemaVersion); err != nil {
				return err
			}
			if err := putUint64Meta(meta, metaKeyPayloadBytes, 0); err != nil {
				return err
			}
			return nil
		}
		for _, name := range [][]byte{bucketDirs, bucketDirRank, bucketHist} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (rs *repoStore) acquireDB() (*bolt.DB, func(), error) {
	rs.compactMu.RLock()
	rs.mu.Lock()
	if rs.db == nil {
		db, err := openBoltFile(rs.path)
		if err != nil {
			// Recover from corrupted file if not simply a lock timeout
			if !errors.Is(err, bolt.ErrTimeout) {
				_ = os.Remove(rs.path)
				db, err = openBoltFile(rs.path)
			}
			if err != nil {
				rs.mu.Unlock()
				rs.compactMu.RUnlock()
				return nil, nil, err
			}
		}
		rs.db = db
	}
	rs.activeOps++
	rs.idleSeq++
	if rs.idleTimer != nil {
		rs.idleTimer.Stop()
	}
	db := rs.db
	rs.mu.Unlock()

	release := func() {
		rs.mu.Lock()
		rs.activeOps--
		if rs.activeOps == 0 && rs.db != nil {
			seq := rs.idleSeq
			rs.idleTimer = time.AfterFunc(storeIdleLease, func() {
				rs.closeOnIdle(seq)
			})
		}
		rs.mu.Unlock()
		rs.compactMu.RUnlock()
	}
	return db, release, nil
}

func (rs *repoStore) closeOnIdle(seq uint64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.activeOps == 0 && rs.idleSeq == seq && rs.db != nil {
		_ = rs.db.Close()
		rs.db = nil
	}
}

func (rs *repoStore) close() error {
	rs.compactMu.Lock()
	defer rs.compactMu.Unlock()
	return rs.closeAlreadyLocked()
}

func (rs *repoStore) closeAlreadyLocked() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.idleSeq++
	if rs.idleTimer != nil {
		rs.idleTimer.Stop()
		rs.idleTimer = nil
	}
	if rs.db == nil {
		return nil
	}
	err := rs.db.Close()
	rs.db = nil
	return err
}

func safeCacheSlug(repoID string) (string, string) {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, repoID)
	if len(safe) > 48 {
		safe = safe[:48]
	}
	sum := sha256Sum(repoID)
	return safe, hex.EncodeToString(sum[:6])
}

func (e *Engine) boltFilePath(repoID string) string {
	safe, h := safeCacheSlug(repoID)
	return filepath.Join(e.cacheDir, fmt.Sprintf("dag-cache-%s-%s.bolt", safe, h))
}

func (e *Engine) legacyGobFilePath(repoID string) string {
	safe, h := safeCacheSlug(repoID)
	return filepath.Join(e.cacheDir, fmt.Sprintf("dag-index-%s-%s.gob", safe, h))
}

func (e *Engine) getOrOpenStore(repoID string) (*repoStore, error) {
	if e.cacheDir == "" || repoID == "" {
		return nil, nil
	}
	e.storesMu.RLock()
	rs, ok := e.stores[repoID]
	e.storesMu.RUnlock()
	if ok {
		return rs, nil
	}

	e.storesMu.Lock()
	if rs, ok = e.stores[repoID]; ok {
		e.storesMu.Unlock()
		return rs, nil
	}
	if err := config.EnsurePrivateDir(e.cacheDir); err != nil {
		e.storesMu.Unlock()
		return nil, err
	}
	boltPath := e.boltFilePath(repoID)
	_, boltStatErr := os.Stat(boltPath)
	isNewBolt := os.IsNotExist(boltStatErr)

	rs = &repoStore{
		repoID: repoID,
		path:   boltPath,
	}
	e.stores[repoID] = rs
	e.storesMu.Unlock()

	// Smooth one-time migration from legacy `.gob` file if present
	gobPath := e.legacyGobFilePath(repoID)
	if isNewBolt {
		if f, gerr := os.Open(gobPath); gerr == nil {
			var legacy diskRepoCache
			decErr := gob.NewDecoder(f).Decode(&legacy)
			_ = f.Close()
			if decErr == nil && legacy.Version == indexSchemaVersion && len(legacy.DirCache) > 0 {
				_ = rs.putDirs(legacy.DirCache, e.nextRank())
			}
			_ = os.Remove(gobPath)
		}
	} else {
		_ = os.Remove(gobPath)
	}
	return rs, nil
}

func (rs *repoStore) getDir(oid string) ([]CachedEntry, bool) {
	db, release, err := rs.acquireDB()
	if err != nil {
		return nil, false
	}
	defer release()

	var out []CachedEntry
	var found bool
	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDirs)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(oid))
		if raw == nil {
			return nil
		}
		entries, err := decodeCachedEntries(raw)
		if err == nil {
			out = entries
			found = true
		}
		return nil
	})
	return out, found
}

func (rs *repoStore) getDirsBatch(oids []string) (map[string][]CachedEntry, []string) {
	found := make(map[string][]CachedEntry, len(oids))
	if len(oids) == 0 {
		return found, nil
	}
	db, release, err := rs.acquireDB()
	if err != nil {
		return found, slices.Clone(oids)
	}
	defer release()

	var missing []string
	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDirs)
		if b == nil {
			missing = append(missing, oids...)
			return nil
		}
		for _, oid := range oids {
			raw := b.Get([]byte(oid))
			if raw == nil {
				missing = append(missing, oid)
				continue
			}
			entries, err := decodeCachedEntries(raw)
			if err != nil {
				missing = append(missing, oid)
				continue
			}
			found[oid] = entries
		}
		return nil
	})
	return found, missing
}

func (rs *repoStore) putDirs(batch map[string][]CachedEntry, rank uint64) error {
	if len(batch) == 0 {
		return nil
	}
	type encodedPair struct {
		oid     []byte
		payload []byte
	}
	encoded := make([]encodedPair, 0, len(batch))
	for oid, entries := range batch {
		encoded = append(encoded, encodedPair{
			oid:     []byte(oid),
			payload: encodeCachedEntries(entries),
		})
	}

	db, release, err := rs.acquireDB()
	if err != nil {
		return err
	}
	defer release()

	rankBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(rankBuf, rank)

	return db.Update(func(tx *bolt.Tx) error {
		bDirs := tx.Bucket(bucketDirs)
		bRank := tx.Bucket(bucketDirRank)
		meta := tx.Bucket(bucketMeta)
		total := int64(getUint64Meta(meta, metaKeyPayloadBytes))

		for _, ep := range encoded {
			if prev := bDirs.Get(ep.oid); prev != nil {
				total -= int64(len(ep.oid) + len(prev))
			} else {
				total += int64(len(ep.oid) + 8)
			}
			if err := bDirs.Put(ep.oid, ep.payload); err != nil {
				return err
			}
			if err := bRank.Put(ep.oid, rankBuf); err != nil {
				return err
			}
			total += int64(len(ep.oid) + len(ep.payload))
		}
		if total < 0 {
			total = 0
		}
		return putUint64Meta(meta, metaKeyPayloadBytes, uint64(total))
	})
}

func (rs *repoStore) dumpAllDirs() map[string][]CachedEntry {
	out := make(map[string][]CachedEntry)
	if _, err := os.Stat(rs.path); err != nil {
		return out
	}
	db, release, err := rs.acquireDB()
	if err != nil {
		return out
	}
	defer release()

	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDirs)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if dec, err := decodeCachedEntries(v); err == nil {
				out[string(k)] = dec
			}
		}
		return nil
	})
	return out
}

func (rs *repoStore) getItemHistory(histKey string) ([]VersionRecord, bool) {
	if _, err := os.Stat(rs.path); err != nil {
		return nil, false
	}
	db, release, err := rs.acquireDB()
	if err != nil {
		return nil, false
	}
	defer release()

	var out []VersionRecord
	var ok bool
	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHist)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(histKey))
		if raw == nil {
			return nil
		}
		if dec, err := decodeVersionHistory(raw); err == nil {
			out = dec
			ok = true
		}
		return nil
	})
	return out, ok
}

func (rs *repoStore) loadPersistedCatalog() (RepoIndexStats, []SearchItem, bool) {
	if _, err := os.Stat(rs.path); err != nil {
		return RepoIndexStats{}, nil, false
	}
	db, release, err := rs.acquireDB()
	if err != nil {
		return RepoIndexStats{}, nil, false
	}
	defer release()

	var (
		stats RepoIndexStats
		items []SearchItem
		ok    bool
	)
	_ = db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		if meta == nil {
			return nil
		}
		raw := meta.Get(metaKeyCatalog)
		if len(raw) == 0 {
			return nil
		}
		if st, it, err := decodeSearchCatalog(raw); err == nil {
			if bDirs := tx.Bucket(bucketDirs); bDirs != nil {
				st.CachedDirs = bDirs.Stats().KeyN
			}
			stats = st
			items = it
			ok = true
		}
		return nil
	})
	return stats, items, ok
}

func (rs *repoStore) saveIndexSnapshot(
	stats RepoIndexStats,
	items []SearchItem,
	histories map[string][]byte,
	reachable map[string]uint64,
	maxPayloadBytes int64,
) (int, error) {
	catBytes := encodeSearchCatalog(stats, items)
	db, release, err := rs.acquireDB()
	if err != nil {
		return 0, err
	}
	defer release()

	var keptDirs int
	err = db.Update(func(tx *bolt.Tx) error {
		bDirs := tx.Bucket(bucketDirs)
		bRank := tx.Bucket(bucketDirRank)
		meta := tx.Bucket(bucketMeta)

		if err := tx.DeleteBucket(bucketHist); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
			return err
		}
		bHist, err := tx.CreateBucket(bucketHist)
		if err != nil {
			return err
		}

		// 1. Prune unreachable directory OIDs and update ranks of reachable ones
		var dirBytes int64
		var deadOIDs [][]byte
		type rankUpdate struct {
			key []byte
			val []byte
		}
		var updates []rankUpdate
		c := bDirs.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			oid := string(k)
			if rnk, ok := reachable[oid]; ok {
				rb := make([]byte, 8)
				binary.BigEndian.PutUint64(rb, rnk)
				updates = append(updates, rankUpdate{key: slices.Clone(k), val: rb})
				dirBytes += int64(len(k)*2 + len(v) + 8)
				keptDirs++
			} else {
				deadOIDs = append(deadOIDs, slices.Clone(k))
			}
		}
		for _, u := range updates {
			_ = bRank.Put(u.key, u.val)
		}
		for _, k := range deadOIDs {
			_ = bDirs.Delete(k)
			_ = bRank.Delete(k)
		}

		// 2. Write histories while respecting maxPayloadBytes budget
		var histBytes int64
		for hk, hv := range histories {
			entryCost := int64(len(hk) + len(hv))
			if maxPayloadBytes > 0 && int64(len(catBytes))+histBytes+entryCost > maxPayloadBytes*3/4 && histBytes > 0 {
				continue
			}
			if err := bHist.Put([]byte(hk), hv); err != nil {
				return err
			}
			histBytes += entryCost
		}

		if err := meta.Put(metaKeyCatalog, catBytes); err != nil {
			return err
		}
		total := dirBytes + histBytes + int64(len(catBytes))
		return putUint64Meta(meta, metaKeyPayloadBytes, uint64(max(int64(0), total)))
	})
	return keptDirs, err
}

func (rs *repoStore) payloadAndFileBytes() (int64, int64) {
	fi, err := os.Stat(rs.path)
	if err != nil {
		return 0, 0
	}
	fileBytes := fi.Size()
	db, release, err := rs.acquireDB()
	if err != nil {
		return 0, fileBytes
	}
	defer release()

	var payload int64
	_ = db.View(func(tx *bolt.Tx) error {
		if meta := tx.Bucket(bucketMeta); meta != nil {
			payload = int64(getUint64Meta(meta, metaKeyPayloadBytes))
		}
		return nil
	})
	return payload, fileBytes
}

// trimToPayloadTarget evicts lowest-rank (oldest/coldest) directory entries until payloadBytes <= targetBytes,
// then compacts the bbolt file on disk if physical file size exceeds maxFileBytes.
func (rs *repoStore) trimToPayloadTarget(targetBytes, maxFileBytes int64) error {
	if _, err := os.Stat(rs.path); err != nil {
		return nil
	}

	db, release, err := rs.acquireDB()
	if err != nil {
		return err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		bDirs := tx.Bucket(bucketDirs)
		bRank := tx.Bucket(bucketDirRank)
		bHist := tx.Bucket(bucketHist)
		meta := tx.Bucket(bucketMeta)

		cur := int64(getUint64Meta(meta, metaKeyPayloadBytes))
		if cur <= targetBytes {
			return nil
		}

		type rankedDir struct {
			key  []byte
			rank uint64
			cost int64
		}
		var cand []rankedDir
		c := bDirs.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rnk uint64
			if rb := bRank.Get(k); len(rb) == 8 {
				rnk = binary.BigEndian.Uint64(rb)
			}
			cand = append(cand, rankedDir{
				key:  slices.Clone(k),
				rank: rnk,
				cost: int64(len(k)*2 + len(v) + 8),
			})
		}
		slices.SortFunc(cand, func(a, b rankedDir) int {
			if a.rank < b.rank {
				return -1
			}
			if a.rank > b.rank {
				return 1
			}
			return 0
		})
		for _, item := range cand {
			if cur <= targetBytes {
				break
			}
			_ = bDirs.Delete(item.key)
			_ = bRank.Delete(item.key)
			cur -= item.cost
		}

		if cur > targetBytes && bHist != nil {
			hc := bHist.Cursor()
			var delHist [][]byte
			for k, v := hc.First(); k != nil && cur > targetBytes; k, v = hc.Next() {
				delHist = append(delHist, slices.Clone(k))
				cur -= int64(len(k) + len(v))
			}
			for _, k := range delHist {
				_ = bHist.Delete(k)
			}
		}

		if cur < 0 {
			cur = 0
		}
		return putUint64Meta(meta, metaKeyPayloadBytes, uint64(cur))
	})
	release()
	if err != nil {
		return err
	}

	if maxFileBytes > 0 {
		if fi, serr := os.Stat(rs.path); serr == nil && fi.Size() > maxFileBytes {
			return rs.compact()
		}
	}
	return nil
}

func (rs *repoStore) compact() error {
	rs.compactMu.Lock()
	defer rs.compactMu.Unlock()

	_ = rs.closeAlreadyLocked()
	if _, err := os.Stat(rs.path); err != nil {
		return nil
	}

	src, err := openBoltFile(rs.path)
	if err != nil {
		return err
	}
	tmpPath := rs.path + ".compact.tmp"
	_ = os.Remove(tmpPath)
	dst, err := bolt.Open(tmpPath, 0o600, &bolt.Options{
		Timeout:      2 * time.Second,
		FreelistType: bolt.FreelistMapType,
	})
	if err != nil {
		_ = src.Close()
		return err
	}
	compactErr := bolt.Compact(dst, src, 65536)
	closeDstErr := dst.Close()
	_ = src.Close()
	if compactErr != nil || closeDstErr != nil {
		_ = os.Remove(tmpPath)
		if compactErr != nil {
			return compactErr
		}
		return closeDstErr
	}

	_ = os.Chmod(tmpPath, 0o600)
	if err := os.Rename(tmpPath, rs.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
