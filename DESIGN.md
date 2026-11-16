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
<!-- @hash sha256:70fe594b11b72420 -->
<!-- @implements spec.architecture sha256:de075e4493c51e2f -->
Incremental snapshot DAG indexing and diff engine that keys directory manifests by immutable Kopia `k...` object IDs (`O(1)` skip on unchanged subtrees), persists atomic gob caches, and computes snapshot-to-snapshot or snapshot-to-local diffs without spawning subprocesses.
<!-- @scope.end -->

<!-- @scope.begin spec.desktop_ui kind=spec -->
<!-- @hash sha256:df32174eba8bbd2b -->
<!-- @implements spec.architecture sha256:de075e4493c51e2f -->
Immediate-mode desktop UI controller enforcing non-blocking `View(c *ui.Context)` frames, monotonic sequence-guarded async mutations via `uiUpdate`, two-phase repository refresh, and per-frame locale/DPI/theme settlement.
<!-- @scope.end -->
