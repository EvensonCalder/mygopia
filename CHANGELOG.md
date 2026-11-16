# Changelog

All notable changes to **Mygopia** are documented in this file.

## [Unreleased]

### Added
- **Bounded `bbolt` + S2 Cache Engine & Configurable Disk Cap (`internal/index/store.go`, `internal/config/config.go`, `internal/app/view_tasks_repo_settings.go`)**:
  - Replaced monolithic `gob` directory-map deserialization with a pure-Go `bbolt` B+Tree + `klauspost/compress/s2` block-compressed delta-varint store (`~6.7 µs` cold directory decode, `~31 ns` L1 LRU hit) and automatic `80ms` idle unmap.
  - Moved per-file `VersionRecord` histories off the Go heap into on-demand `bbolt` point reads and persisted search catalogs for `< 5ms` cold-start recovery (`EnsureCatalogLoaded`).
  - Added user-configurable maximum disk cache size defaulting to `1 GB` (`128 MB` – `8 GB` in Settings -> Index & Cache) with newest-first rank eviction, `bbolt.Compact` physical space reclamation, live cache usage display, and one-click cache clear.
- **Real-Time Incremental Streaming & Incomplete-Load Visual Feedback (`internal/index/diff.go`, `internal/index/local_diff.go`, `internal/app/view_diff.go`, `internal/app/view_search.go`, `internal/app/view_snapshots.go`)**:
  - Added progressive BFS streaming (`CompareRootsStream` and 8-worker concurrent `CompareRootWithLocalDirStream`) and source-by-source search catalog streaming.
  - Added upfront storage-locality & directory-scale cost estimation (`300ms` threshold) so fast `< 0.3s` operations update silently without toolbar spinner flashes, while any not-yet-complete file/folder rows render filenames in muted gray until 100% loaded.
- **Configured Repository Connecting Transition View (`internal/app/view_connect.go`, `internal/kopia/manager.go`)**:
  - Added a dedicated "Connecting to repository…" transition card (with retry & reconfigure actions and serialized `RefreshRepo` polling) so configured remote/server repositories no longer flash `Disconnected` or raw loopback timeout errors during startup handshakes.

### Fixed
- **Duplicate RPCs & Redundant Diff Executions (`internal/app/cache.go`, `internal/app/view_diff.go`, `internal/app/view_snapshots.go`)**: Coalesced concurrent `ListSnapshots`, `GetDirectory`, and `ResolvePolicy` calls via `singleflight`, eliminated double `CompareRoots` execution on Diff source load, and added 60fps table filter/sort memoization.
- **Kopia Repository Server `apiServer.url` Discovery & macOS Traffic-Light Tooltip Placement (`internal/config/config.go`, `internal/app/theme.go`, `internal/app/app.go`)**:
  - Fixed `InspectConfigFile` to parse Kopia's `apiServer.url` field and self-heal fallback `"repository"` profile names.
  - Directed top-bar and sidebar header tooltips downward (`tooltipBelow`) and collapsed rail tooltips rightward (`tooltipRight`) so hover tooltips never overlap macOS window traffic lights.

## [0.3.2] - 2026-11-16

### Added
- **Meta-lang Native Architecture Governance (`DESIGN.md`, `.mela/`, `.melaignore`)**: Added architectural specifications (`spec.architecture`, `spec.kopia_engine`, `spec.dag_index`, `spec.desktop_ui`) and verified `@scope.file` / `@hash` / `@implements` / `@ref` contracts across all 27 scopes and 126 cross-module dependencies (`0` errors and `0` warnings under `mela check --strict` and `melao check --strict`).
- **Regression Test Coverage (`internal/index/`)**: Added `TestIndexRepoCancelDuringTreeWalkResetsIndexingFlag` (verifying canceled tree walks reset `Indexing = false` cleanly) and `.kopiaignore` exclusion assertions in `TestLocalDiffWithIgnoreRulesAndCacheTag`.

### Fixed
- **Permanent `Indexing = true` Wedge on Context Cancellation (`internal/index/index.go`)**: Fixed `IndexRepo` snapshot tree expansion to return via `finishWithErr(err)` when `ctx.Err() != nil`, preventing canceled index runs from leaving `Indexing = true` stuck until app restart.
- **`.kopiaignore` Rule Filtering in Local Folder Diff (`internal/index/local_diff.go`)**: Removed the `dotIgnoreSet` exemption after `.kopiaignore` rules are loaded so `.kopiaignore` files themselves are excluded from next-backup diffs when matched by policy `IgnoreRules`.
- **`ServerRunner` Pointer Data Race & State Persistence (`internal/kopia/manager.go`)**:
  - Synchronized `managedRepo.runner` reads and `SyncProfiles` reassignments under `mr.mu` (`runnerRef()`).
  - Preserved cached `Tasks`, `Mounts`, and `NotificationProfiles` across transient Kopia REST API errors during `RefreshRepo`.
  - Cleared cached `Sources`, `Policies`, `Mounts`, and `Tasks` on `StopRepo`.
- **Snapshot Navigation Race & Maintenance Timeouts (`internal/app/view_snapshots.go`, `internal/app/view_tasks_repo_settings.go`)**:
  - Added `snapLoadSeq`, active repository, and source guards in `openSnapshotByID` so concurrent repository activations cannot overwrite targeted snapshot navigation.
  - Added bounded `context.WithTimeout` budgets to `saveMaintenanceAction` (`20s`) and `runMaintenanceAction` (`15m`), and cleared cached snapshot/browser/diff UI slices on `disconnectRepoAction`.

### Changed
- **Framework & Toolchain Upgrades (`go.mod`)**: Upgraded Go toolchain to `1.27.4`, `github.com/egoist/mygo` to `v0.9.1`, `purego` to `v0.11.3`, and `golang.org/x/*` (`sys v0.50.0`, `text v0.44.0`, `net v0.62.0`, `crypto v0.58.0`, `image v0.48.0`).

## [0.3.1] - 2026-11-12

### Added
- **Native ARB / JSON Localization Catalogs (`locales/`, `internal/app/locale.go`)**: Migrated all 517 UI strings to structured JSON catalogs (`en`, `de`, `es`, `fr`, `ja`, `zh-Hans`, `zh-Hant`) with compile-time `mygo i18n check` parity verification and generated strongly typed accessors (`locales/catalog_gen.go`).
- **Automated README Showcase Screenshots (`internal/app/docs_screenshots_test.go`, `docs/screenshots/`)**: Added deterministic synthetic screenshot generation in English and Simplified Chinese, and refined macOS Dock and menu bar template icons.

### Changed
- **Compact Backup Source Cards (`internal/app/view_backups.go`)**: Streamlined backup source status layout, action controls, and localized relative/timestamp formatting across all views.

## [0.3.0] - 2026-11-11

### Added
- **Initial Cross-Platform Release**: Native desktop GUI client for Kopia built with `Mygo` across macOS (Universal `.dmg`), Windows (`amd64` & `arm64` NSIS installer + portable `.zip`), and Linux (`amd64` & `arm64` `.deb` + `.tar.gz`).
- **Multi-Repository Lifecycle & 11 Storage Backends (`internal/kopia/`, `internal/app/view_connect.go`)**: Ephemeral loopback TLS `kopia server` orchestration, automatic local profile discovery, and connection/initialization wizard for Filesystem, S3 / MinIO / R2, B2, SFTP, WebDAV, GCS, Azure, Rclone, Kopia Server, and Token repositories.
- **Content-Addressed DAG Search & Diff Engine (`internal/index/`)**: Incremental `k...` directory OID caching, cross-repository file & deleted-item version search, snapshot-to-snapshot diffing, and zero-subprocess local folder diff preview with `.kopiaignore` and `CACHEDIR.TAG` support.
- **Full Backup Operations (`internal/app/`)**: Source management, retention/schedule/compression/ignore/hook policy editor, FUSE/WebDAV snapshot mounting, selective & archive restore, bandwidth rate limiting, maintenance scheduling, notifications, system tray mode, 7 accent colors, and adaptive DPI scaling.
