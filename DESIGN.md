# Mygopia Architecture & Design Specification

<!-- @scope.begin spec.architecture kind=spec -->
<!-- @hash sha256:de075e4493c51e2f -->
Layered desktop architecture separating persistent profile configuration (`config.store`), local Kopia server/CLI orchestration (`kopia.*`), content-addressed DAG indexing (`index.*`), and immediate-mode MyGO views (`app.*`).
<!-- @scope.end -->

<!-- @scope.begin spec.kopia_engine kind=spec -->
<!-- @hash sha256:44f993b3db7e5386 -->
<!-- @implements spec.architecture sha256:de075e4493c51e2f -->
Per-repository Kopia engine combining ephemeral loopback HTTPS server processes (`kopia server start --random-password --random-server-control-password --tls-generate-cert`), cert-pinned REST API clients, and non-interactive CLI invocations for repository initialization, maintenance, and deduplication stats.
<!-- @scope.end -->

<!-- @scope.begin spec.dag_index kind=spec -->
<!-- @hash sha256:c155f3ed2269ad94 -->
<!-- @implements spec.architecture sha256:de075e4493c51e2f -->
Incremental snapshot DAG indexing and diff engine that keys directory manifests by immutable Kopia `k...` object IDs (`O(1)` skip on unchanged subtrees), combines a tiny bounded in-memory L1 LRU with S2-compressed `bbolt` B+Tree storage under a user-configurable disk cap (default 1 GB) with rank-based eviction/compaction, keeps per-item version histories off-heap, and streams progressive search catalogs and snapshot/local diffs without spawning subprocesses.
<!-- @scope.end -->

<!-- @scope.begin spec.desktop_ui kind=spec -->
<!-- @hash sha256:67347d72ce79be0a -->
<!-- @implements spec.architecture sha256:de075e4493c51e2f -->
Immediate-mode desktop UI controller enforcing non-blocking `View(c *ui.Context)` frames, `singleflight`-coalesced RPCs with Stale-While-Revalidate multi-source snapshot/policy caches, 60fps table filter/sort memoization, live incremental streaming with toolbar spinner status, monotonic sequence-guarded async mutations via `uiUpdate`, two-phase repository refresh, and per-frame locale/DPI/theme settlement.
<!-- @scope.end -->
