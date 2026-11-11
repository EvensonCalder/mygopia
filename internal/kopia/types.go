package kopia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FlexTime unmarshals RFC3339 timestamps or numeric Unix timestamps from Kopia JSON.
type FlexTime struct {
	time.Time
}

func (ft *FlexTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" || s == `""` {
		ft.Time = time.Time{}
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		if str == "" {
			ft.Time = time.Time{}
			return nil
		}
		if t, err := time.Parse(time.RFC3339Nano, str); err == nil {
			ft.Time = t
			return nil
		}
		if t, err := time.Parse(time.RFC3339, str); err == nil {
			ft.Time = t
			return nil
		}
		return nil
	}
	var ns int64
	if err := json.Unmarshal(b, &ns); err == nil {
		if ns == 0 {
			ft.Time = time.Time{}
		} else if ns > 1e14 {
			ft.Time = time.Unix(0, ns)
		} else if ns > 1e11 {
			ft.Time = time.UnixMilli(ns)
		} else {
			ft.Time = time.Unix(ns, 0)
		}
		return nil
	}
	return nil
}

func (ft FlexTime) MarshalJSON() ([]byte, error) {
	if ft.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(ft.UTC().Format(time.RFC3339Nano))
}

// APIError represents a structured error returned by the Kopia HTTP server.
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"error"`
}

func (e *APIError) Error() string {
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("kopia server error (HTTP %d)", e.StatusCode)
}

// ClientOptions describes client identity and options on a repository connection.
type ClientOptions struct {
	Description           string `json:"description,omitempty"`
	Username              string `json:"username,omitempty"`
	Hostname              string `json:"hostname,omitempty"`
	ReadOnly              bool   `json:"readonly,omitempty"`
	PermissiveCacheLoad   bool   `json:"permissiveCacheLoading,omitempty"`
	EnableActions         bool   `json:"enableActions,omitempty"`
	FormatBlobCacheMillis int64  `json:"formatBlobCacheDuration,omitempty"`
}

// StatusResponse is returned by GET /api/v1/repo/status.
type StatusResponse struct {
	Connected                  bool   `json:"connected"`
	ConfigFile                 string `json:"configFile,omitempty"`
	FormatVersion              int    `json:"formatVersion,omitempty"`
	Hash                       string `json:"hash,omitempty"`
	Encryption                 string `json:"encryption,omitempty"`
	ECC                        string `json:"ecc,omitempty"`
	ECCOverheadPercent         int    `json:"eccOverheadPercent,omitempty"`
	Splitter                   string `json:"splitter,omitempty"`
	MaxPackSize                int    `json:"maxPackSize,omitempty"`
	Storage                    string `json:"storage,omitempty"`
	APIServerURL               string `json:"apiServerURL,omitempty"`
	SupportsContentCompression bool   `json:"supportsContentCompression"`
	InitRepoTaskID             string `json:"initTaskID,omitempty"`

	Description   string `json:"description,omitempty"`
	Username      string `json:"username,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	ReadOnly      bool   `json:"readonly,omitempty"`
	EnableActions bool   `json:"enableActions,omitempty"`
}

// StorageConnection matches Kopia's blob.ConnectionInfo JSON wire format.
type StorageConnection struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

// APIServerInfo configures connection to a remote Kopia Repository Server.
type APIServerInfo struct {
	BaseURL                             string `json:"url"`
	TrustedServerCertificateFingerprint string `json:"serverCertFingerprint,omitempty"`
	DisableGRPC                         bool   `json:"disableGRPC,omitempty"`
}

// ConnectRepositoryRequest connects to an existing Kopia repository.
type ConnectRepositoryRequest struct {
	Storage             StorageConnection `json:"storage,omitempty"`
	Password            string            `json:"password,omitempty"`
	Token               string            `json:"token,omitempty"`
	APIServer           *APIServerInfo    `json:"apiServer,omitempty"`
	ClientOptions       ClientOptions     `json:"clientOptions"`
	SyncWaitTimeSeconds int               `json:"syncWaitTime,omitempty"`
}

// BlockFormatOptions sets repository creation encryption/hash/ECC parameters.
type BlockFormatOptions struct {
	Version            int    `json:"version,omitempty"`
	Hash               string `json:"hash,omitempty"`
	Encryption         string `json:"encryption,omitempty"`
	ECC                string `json:"ecc,omitempty"`
	ECCOverheadPercent int    `json:"eccOverheadPercent,omitempty"`
	MaxPackSize        int    `json:"maxPackSize,omitempty"`
}

// ObjectFormatOptions sets repository content-defined chunking splitter.
type ObjectFormatOptions struct {
	Splitter string `json:"splitter,omitempty"`
}

// NewRepositoryOptions configures newly initialized repositories.
type NewRepositoryOptions struct {
	BlockFormat     BlockFormatOptions  `json:"blockFormat,omitempty"`
	ObjectFormat    ObjectFormatOptions `json:"objectFormat,omitempty"`
	RetentionMode   string              `json:"retentionMode,omitempty"`
	RetentionPeriod string              `json:"retentionPeriod,omitempty"`
}

// CreateRepositoryRequest initializes and connects a new Kopia repository.
type CreateRepositoryRequest struct {
	ConnectRepositoryRequest
	NewRepositoryOptions NewRepositoryOptions `json:"options"`
}

// CheckRepositoryExistsRequest checks if storage already contains an initialized repository.
type CheckRepositoryExistsRequest struct {
	Storage StorageConnection `json:"storage"`
}

// AlgorithmInfo describes a supported hash, encryption, splitter, ECC, or compression algorithm.
type AlgorithmInfo struct {
	ID         string `json:"id"`
	Deprecated bool   `json:"deprecated"`
}

// SupportedAlgorithmsResponse is returned by GET /api/v1/repo/algorithms.
type SupportedAlgorithmsResponse struct {
	DefaultHash                    string          `json:"defaultHash"`
	DefaultEncryption              string          `json:"defaultEncryption"`
	DefaultECC                     string          `json:"defaultEcc"`
	DefaultSplitter                string          `json:"defaultSplitter"`
	SupportedHashAlgorithms        []AlgorithmInfo `json:"hash"`
	SupportedEncryptionAlgorithms  []AlgorithmInfo `json:"encryption"`
	SupportedECCAlgorithms         []AlgorithmInfo `json:"ecc"`
	SupportedSplitterAlgorithms    []AlgorithmInfo `json:"splitter"`
	SupportedCompressionAlgorithms []AlgorithmInfo `json:"compression"`
}

// ThrottleLimits matches Kopia's repo/blob/throttling.Limits JSON structure.
type ThrottleLimits struct {
	ReadsPerSecond         float64 `json:"readsPerSecond,omitempty"`
	WritesPerSecond        float64 `json:"writesPerSecond,omitempty"`
	ListsPerSecond         float64 `json:"listsPerSecond,omitempty"`
	UploadBytesPerSecond   float64 `json:"maxUploadSpeedBytesPerSecond,omitempty"`
	DownloadBytesPerSecond float64 `json:"maxDownloadSpeedBytesPerSecond,omitempty"`
	ConcurrentReads        int     `json:"concurrentReads,omitempty"`
	ConcurrentWrites       int     `json:"concurrentWrites,omitempty"`
}

// SourceInfo identifies a snapshot source or policy target.
type SourceInfo struct {
	Host     string `json:"host"`
	UserName string `json:"userName"`
	Path     string `json:"path"`
}

func (s SourceInfo) IsGlobal() bool {
	return s.Host == "" && s.UserName == "" && s.Path == ""
}

func (s SourceInfo) IsHostLevel() bool {
	return s.Path == "" && (s.Host != "" || s.UserName != "")
}

func (s SourceInfo) String() string {
	if s.IsGlobal() {
		return "(global)"
	}
	if s.Path == "" {
		return fmt.Sprintf("%s@%s", s.UserName, s.Host)
	}
	return fmt.Sprintf("%s@%s:%s", s.UserName, s.Host, s.Path)
}

// UploadCounters tracks live upload progress for a running snapshot.
type UploadCounters struct {
	CachedBytes          int64  `json:"cachedBytes"`
	HashedBytes          int64  `json:"hashedBytes"`
	UploadedBytes        int64  `json:"uploadedBytes"`
	EstimatedBytes       int64  `json:"estimatedBytes"`
	CachedFiles          int32  `json:"cachedFiles"`
	HashedFiles          int32  `json:"hashedFiles"`
	ExcludedFiles        int32  `json:"excludedFiles"`
	ExcludedDirs         int32  `json:"excludedDirs"`
	Errors               int32  `json:"errors"`
	IgnoredErrorCount    int32  `json:"ignoredErrorCount"`
	EstimatedFiles       int64  `json:"estimatedFiles"`
	CurrentDirectory     string `json:"directory"`
	LastErrorPath        string `json:"lastErrorPath"`
	LastError            string `json:"lastError"`
}

// DirectorySummary summarizes counts and total size of a directory tree.
type DirectorySummary struct {
	TotalFileSize     int64    `json:"size"`
	TotalFileCount    int64    `json:"files"`
	TotalSymlinkCount int64    `json:"symlinks"`
	TotalDirCount     int64    `json:"dirs"`
	MaxModTime        FlexTime `json:"maxTime"`
	IncompleteReason  string   `json:"incomplete,omitempty"`
	FatalErrorCount   int      `json:"numFailed"`
}

// ManifestPreview is the LastSnapshot object embedded in SourceStatus.
type ManifestPreview struct {
	ID               string    `json:"id"`
	Description      string    `json:"description,omitempty"`
	StartTime        FlexTime  `json:"startTime"`
	EndTime          FlexTime  `json:"endTime"`
	IncompleteReason string    `json:"incomplete,omitempty"`
	RootEntry        *DirEntry `json:"rootEntry,omitempty"`
	Pins             []string  `json:"pins,omitempty"`
}

// SourceStatus describes a backup source returned by GET /api/v1/sources.
type SourceStatus struct {
	Source           SourceInfo       `json:"source"`
	Status           string           `json:"status"` // IDLE, PENDING, UPLOADING, PAUSED, FAILED
	Schedule         SchedulingPolicy `json:"schedule"`
	LastSnapshot     *ManifestPreview `json:"lastSnapshot,omitempty"`
	NextSnapshotTime *FlexTime        `json:"nextSnapshotTime,omitempty"`
	Upload           *UploadCounters  `json:"upload,omitempty"`
	CurrentTask      string           `json:"currentTask,omitempty"`
}

// SourcesResponse is returned by GET /api/v1/sources.
type SourcesResponse struct {
	LocalUsername string          `json:"localUsername"`
	LocalHost     string          `json:"localHost"`
	MultiUser     bool            `json:"multiUser"`
	Sources       []*SourceStatus `json:"sources"`
}

// CreateSnapshotSourceRequest adds a new local directory to Kopia backups.
type CreateSnapshotSourceRequest struct {
	Path           string  `json:"path"`
	CreateSnapshot bool    `json:"createSnapshot"`
	Policy         *Policy `json:"policy"`
}

type CreateSnapshotSourceResponse struct {
	SnapshotStarted bool `json:"snapshotted"`
}

// Snapshot is an entry returned by GET /api/v1/snapshots.
type Snapshot struct {
	ID               string            `json:"id"`
	Description      string            `json:"description"`
	StartTime        FlexTime          `json:"startTime"`
	EndTime          FlexTime          `json:"endTime"`
	IncompleteReason string            `json:"incomplete,omitempty"`
	Summary          *DirectorySummary `json:"summary,omitempty"`
	RootID           string            `json:"rootID"`
	RetentionReasons []string          `json:"retention"`
	Pins             []string          `json:"pins"`
}

// SnapshotsResponse is returned by GET /api/v1/snapshots.
type SnapshotsResponse struct {
	Snapshots       []*Snapshot `json:"snapshots"`
	UnfilteredCount int         `json:"unfilteredCount"`
	UniqueCount     int         `json:"uniqueCount"`
}

// EditSnapshotsRequest updates description and pins on one or more snapshots.
type EditSnapshotsRequest struct {
	Snapshots      []string `json:"snapshots"`
	NewDescription *string  `json:"description,omitempty"`
	AddPins        []string `json:"addPins,omitempty"`
	RemovePins     []string `json:"removePins,omitempty"`
}

// DeleteSnapshotsRequest deletes specific snapshots or an entire source.
type DeleteSnapshotsRequest struct {
	Source                SourceInfo `json:"source"`
	SnapshotManifestIDs   []string   `json:"snapshotManifestIds,omitempty"`
	DeleteSourceAndPolicy bool       `json:"deleteSourceAndPolicy,omitempty"`
}

// FlexMode handles Kopia's octal string permissions (e.g. "0755") or numeric modes.
type FlexMode uint32

func (m *FlexMode) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*m = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*m = 0
			return nil
		}
		v, err := strconv.ParseUint(s, 0, 32)
		if err == nil {
			*m = FlexMode(v)
		}
		return nil
	}
	var v uint32
	if err := json.Unmarshal(b, &v); err == nil {
		*m = FlexMode(v)
	}
	return nil
}

// DirEntry represents a single entry inside a Kopia directory object ("kopia:directory").
type DirEntry struct {
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"` // "d" directory, "s" symlink, "f" or "" file
	Mode       FlexMode          `json:"mode,omitempty"`
	FileSize   int64             `json:"size,omitempty"`
	ModTime    FlexTime          `json:"mtime,omitempty"`
	UserID     uint32            `json:"uid,omitempty"`
	GroupID    uint32            `json:"gid,omitempty"`
	ObjectID   string            `json:"obj"`
	DirSummary *DirectorySummary `json:"summ,omitempty"`
}

func (e *DirEntry) IsDir() bool {
	return e.Type == "d" || strings.HasPrefix(e.ObjectID, "k")
}

// LogicalSize returns FileSize for files or TotalFileSize from DirSummary for directories.
func (e *DirEntry) LogicalSize() int64 {
	if e.IsDir() && e.DirSummary != nil {
		return e.DirSummary.TotalFileSize
	}
	return e.FileSize
}

// DirManifest is the JSON payload stored inside directory objects returned by GET /api/v1/objects/{oid}.
type DirManifest struct {
	StreamType string            `json:"stream"`
	Entries    []*DirEntry       `json:"entries"`
	Summary    *DirectorySummary `json:"summary,omitempty"`
}

// MountedSnapshot represents an active FUSE / WebDAV snapshot mount.
type MountedSnapshot struct {
	Path string `json:"path"`
	Root string `json:"root"`
}

type MountedSnapshots struct {
	Items []*MountedSnapshot `json:"items"`
}

// FilesystemOutput configures restoring to a local target directory/file.
type FilesystemOutput struct {
	TargetPath             string `json:"targetPath"`
	OverwriteDirectories   bool   `json:"overwriteDirectories"`
	OverwriteFiles         bool   `json:"overwriteFiles"`
	OverwriteSymlinks      bool   `json:"overwriteSymlinks"`
	IgnorePermissionErrors bool   `json:"ignorePermissionErrors"`
	WriteFilesAtomically   bool   `json:"writeFilesAtomically"`
	SkipOwners             bool   `json:"skipOwners"`
	SkipPermissions        bool   `json:"skipPermissions"`
	SkipTimes              bool   `json:"skipTimes"`
}

// RestoreOptions configures restore concurrency and behavior.
type RestoreOptions struct {
	Parallel               int   `json:"parallel,omitempty"`
	Incremental            bool  `json:"incremental,omitempty"`
	DeleteExtra            bool  `json:"deleteExtra,omitempty"`
	IgnoreErrors           bool  `json:"ignoreErrors,omitempty"`
	RestoreDirEntryAtDepth int32 `json:"restoreDirEntryAtDepth,omitempty"`
	MinSizeForPlaceholder  int32 `json:"minSizeForPlaceholder,omitempty"`
}

// RestoreRequest starts an asynchronous restore task via POST /api/v1/restore.
type RestoreRequest struct {
	Root            string            `json:"root"`
	Filesystem      *FilesystemOutput `json:"fsOutput,omitempty"`
	ZipFile         string            `json:"zipFile,omitempty"`
	UncompressedZip bool              `json:"uncompressedZip,omitempty"`
	TarFile         string            `json:"tarFile,omitempty"`
	Options         RestoreOptions    `json:"options"`
}

// TimeOfDay represents HH:MM in 24-hour format for SchedulingPolicy.
type TimeOfDay struct {
	Hour   int `json:"hour"`
	Minute int `json:"min"`
}

func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

func ParseTimeOfDay(s string) (TimeOfDay, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return TimeOfDay{}, fmt.Errorf("expected HH:MM, got %q", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return TimeOfDay{}, fmt.Errorf("hour must be 0..23 in %q", s)
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return TimeOfDay{}, fmt.Errorf("minute must be 0..59 in %q", s)
	}
	return TimeOfDay{Hour: h, Minute: m}, nil
}

// RetentionPolicy configures snapshot retention counts.
type RetentionPolicy struct {
	KeepLatest               *int  `json:"keepLatest,omitempty"`
	KeepHourly               *int  `json:"keepHourly,omitempty"`
	KeepDaily                *int  `json:"keepDaily,omitempty"`
	KeepWeekly               *int  `json:"keepWeekly,omitempty"`
	KeepMonthly              *int  `json:"keepMonthly,omitempty"`
	KeepAnnual               *int  `json:"keepAnnual,omitempty"`
	IgnoreIdenticalSnapshots *bool `json:"ignoreIdenticalSnapshots,omitempty"`
}

// SchedulingPolicy configures automatic snapshot intervals, times of day, and cron expressions.
type SchedulingPolicy struct {
	IntervalSeconds    int64       `json:"intervalSeconds,omitempty"`
	TimesOfDay         []TimeOfDay `json:"timeOfDay,omitempty"`
	NoParentTimesOfDay bool        `json:"noParentTimeOfDay,omitempty"`
	Manual             bool        `json:"manual,omitempty"`
	Cron               []string    `json:"cron,omitempty"`
	RunMissed          *bool       `json:"runMissed,omitempty"`
}

// FilesPolicy configures ignore rules, dot-ignore files, max file size, and cache directory skipping.
type FilesPolicy struct {
	IgnoreRules            []string `json:"ignore,omitempty"`
	NoParentIgnoreRules    bool     `json:"noParentIgnore,omitempty"`
	DotIgnoreFiles         []string `json:"ignoreDotFiles,omitempty"`
	NoParentDotIgnoreFiles bool     `json:"noParentDotFiles,omitempty"`
	IgnoreCacheDirectories *bool    `json:"ignoreCacheDirs,omitempty"`
	MaxFileSize            int64    `json:"maxFileSize,omitempty"`
	OneFileSystem          *bool    `json:"oneFileSystem,omitempty"`
}

// CompressionPolicy configures content compression rules.
type CompressionPolicy struct {
	CompressorName        string   `json:"compressorName,omitempty"`
	OnlyCompress          []string `json:"onlyCompress,omitempty"`
	NoParentOnlyCompress  bool     `json:"noParentOnlyCompress,omitempty"`
	NeverCompress         []string `json:"neverCompress,omitempty"`
	NoParentNeverCompress bool     `json:"noParentNeverCompress,omitempty"`
	MinSize               int64    `json:"minSize,omitempty"`
	MaxSize               int64    `json:"maxSize,omitempty"`
}

type MetadataCompressionPolicy struct {
	CompressorName string `json:"compressorName,omitempty"`
}

// ErrorHandlingPolicy configures how read errors are treated during snapshots.
type ErrorHandlingPolicy struct {
	IgnoreFileErrors      *bool `json:"ignoreFileErrors,omitempty"`
	IgnoreDirectoryErrors *bool `json:"ignoreDirectoryErrors,omitempty"`
	IgnoreUnknownTypes    *bool `json:"ignoreUnknownTypes,omitempty"`
}

// UploadPolicy configures upload concurrency.
type UploadPolicy struct {
	MaxParallelSnapshots    *int   `json:"maxParallelSnapshots,omitempty"`
	MaxParallelFileReads    *int   `json:"maxParallelFileReads,omitempty"`
	ParallelUploadAboveSize *int64 `json:"parallelUploadAboveSize,omitempty"`
}

// ActionCommand configures a before/after hook command or script.
type ActionCommand struct {
	Command        string   `json:"path,omitempty"`
	Arguments      []string `json:"args,omitempty"`
	Script         string   `json:"script,omitempty"`
	TimeoutSeconds int      `json:"timeout,omitempty"`
	Mode           string   `json:"mode,omitempty"` // essential, optional, async
}

// ActionsPolicy configures snapshot lifecycle hooks.
type ActionsPolicy struct {
	BeforeFolder       *ActionCommand `json:"beforeFolder,omitempty"`
	AfterFolder        *ActionCommand `json:"afterFolder,omitempty"`
	BeforeSnapshotRoot *ActionCommand `json:"beforeSnapshotRoot,omitempty"`
	AfterSnapshotRoot  *ActionCommand `json:"afterSnapshotRoot,omitempty"`
}

type DirLoggingPolicy struct {
	Snapshotted *int `json:"snapshotted,omitempty"`
	Ignored     *int `json:"ignored,omitempty"`
}

type EntryLoggingPolicy struct {
	Snapshotted *int `json:"snapshotted,omitempty"`
	Ignored     *int `json:"ignored,omitempty"`
	CacheHit    *int `json:"cacheHit,omitempty"`
	CacheMiss   *int `json:"cacheMiss,omitempty"`
}

// LoggingPolicy configures snapshot diagnostic log verbosity (0..10).
type LoggingPolicy struct {
	Directories DirLoggingPolicy   `json:"directories,omitempty"`
	Entries     EntryLoggingPolicy `json:"entries,omitempty"`
}

// Policy represents a complete Kopia backup policy (global, host, user@host, or path).
type Policy struct {
	RetentionPolicy           RetentionPolicy           `json:"retention,omitempty"`
	FilesPolicy               FilesPolicy               `json:"files,omitempty"`
	ErrorHandlingPolicy       ErrorHandlingPolicy       `json:"errorHandling,omitempty"`
	SchedulingPolicy          SchedulingPolicy          `json:"scheduling,omitempty"`
	CompressionPolicy         CompressionPolicy         `json:"compression,omitempty"`
	MetadataCompressionPolicy MetadataCompressionPolicy `json:"metadataCompression,omitempty"`
	Actions                   ActionsPolicy             `json:"actions,omitempty"`
	LoggingPolicy             LoggingPolicy             `json:"logging,omitempty"`
	UploadPolicy              UploadPolicy              `json:"upload,omitempty"`
	NoParent                  bool                      `json:"noParent,omitempty"`
}

func (p *Policy) Clone() *Policy {
	if p == nil {
		return &Policy{}
	}
	b, _ := json.Marshal(p)
	var out Policy
	_ = json.Unmarshal(b, &out)
	return &out
}

// PolicyListEntry wraps a Policy with its ID and Target.
type PolicyListEntry struct {
	ID     string     `json:"id"`
	Target SourceInfo `json:"target"`
	Policy *Policy    `json:"policy"`
}

type PoliciesResponse struct {
	Policies []*PolicyListEntry `json:"policies"`
}

// ResolvePolicyRequest resolves effective inherited values and upcoming schedule times.
type ResolvePolicyRequest struct {
	Updates                  *Policy `json:"updates,omitempty"`
	NumUpcomingSnapshotTimes int     `json:"numUpcomingSnapshotTimes,omitempty"`
}

type ResolvePolicyResponse struct {
	Effective             *Policy         `json:"effective"`
	Defined               *Policy         `json:"defined"`
	Definition            json.RawMessage `json:"definition,omitempty"`
	UpcomingSnapshotTimes []FlexTime      `json:"upcomingSnapshotTimes"`
	SchedulingError       string          `json:"schedulingError,omitempty"`
}

// CounterValue represents a task counter item in uitask.Info.
type CounterValue struct {
	Value int64  `json:"value"`
	Units string `json:"units,omitempty"`
	Level string `json:"level,omitempty"`
}

// TaskInfo matches Kopia's internal/uitask.Info JSON payload.
type TaskInfo struct {
	ID           string                  `json:"id"`
	StartTime    FlexTime                `json:"startTime"`
	EndTime      *FlexTime               `json:"endTime,omitempty"`
	Kind         string                  `json:"kind"`
	Description  string                  `json:"description"`
	Status       string                  `json:"status"` // RUNNING, SUCCESS, FAILED, CANCELED, CANCELING
	ProgressInfo string                  `json:"progressInfo"`
	ErrorMessage string                  `json:"errorMessage,omitempty"`
	Counters     map[string]CounterValue `json:"counters,omitempty"`
}

func (t TaskInfo) IsRunning() bool {
	return t.Status == "RUNNING" || t.Status == "CANCELING"
}

type TaskListResponse struct {
	Tasks []TaskInfo `json:"tasks"`
}

type TaskSummary map[string]int

type TaskLogEntry struct {
	Timestamp float64 `json:"ts"`
	Module    string  `json:"mod"`
	Level     int     `json:"level"`
	Text      string  `json:"msg"`
}

type TaskLogResponse struct {
	Logs []TaskLogEntry `json:"logs"`
}

// NotificationMethodConfig configures Webhook, Email, or Pushover senders.
type NotificationMethodConfig struct {
	Type   string         `json:"type"` // webhook, email, pushover
	Config map[string]any `json:"config"`
}

// NotificationProfile configures a repository notification profile.
type NotificationProfile struct {
	ProfileName  string                   `json:"profile"`
	MethodConfig NotificationMethodConfig `json:"method"`
	MinSeverity  int                      `json:"minSeverity"` // -100 verbose, -10 report, 0 info, 10 warning, 20 error
}

// MaintenanceCycle describes Quick or Full maintenance cycle parameters.
type MaintenanceCycle struct {
	Enabled  bool  `json:"enabled"`
	Interval int64 `json:"interval"` // time.Duration in nanoseconds in Go JSON
}

// MaintenanceRunInfo describes a historical maintenance execution entry.
type MaintenanceRunInfo struct {
	Start   FlexTime `json:"start"`
	End     FlexTime `json:"end"`
	Success bool     `json:"success"`
	Error   string   `json:"error,omitempty"`
}

// MaintenanceParams holds top-level parameters emitted by `kopia maintenance info --json`.
type MaintenanceParams struct {
	Owner             string           `json:"owner"`
	QuickCycle        MaintenanceCycle `json:"quick"`
	FullCycle         MaintenanceCycle `json:"full"`
	ExtendObjectLocks bool             `json:"extendObjectLocks"`
	ListParallelism   int              `json:"listParallelism"`
}

// MaintenanceInfo summarizes output of `kopia maintenance info --json`.
type MaintenanceInfo struct {
	MaintenanceParams
	Schedule struct {
		NextFullMaintenanceTime  FlexTime                        `json:"nextFullMaintenance"`
		NextQuickMaintenanceTime FlexTime                        `json:"nextQuickMaintenance"`
		Runs                     map[string][]MaintenanceRunInfo `json:"runs"`
	} `json:"schedule"`
}
