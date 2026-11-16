# Third-Party Notices

Mygopia is released under the [MIT License](./LICENSE).

It compiles with zero CGO and links only the following permissive open-source Go modules:

| Module | Version | License |
| --- | --- | --- |
| [`github.com/egoist/mygo`](https://github.com/egoist/mygo) | `v0.9.1` | MIT |
| [`go.etcd.io/bbolt`](https://github.com/etcd-io/bbolt) | `v1.5.0` | MIT |
| [`github.com/klauspost/compress`](https://github.com/klauspost/compress) | `v1.20.4` | BSD-3-Clause |
| [`github.com/hashicorp/golang-lru/v2`](https://github.com/hashicorp/golang-lru) | `v2.0.7` | MPL-2.0 |
| [`golang.org/x/sync`](https://cs.opensource.google/go/x/sync) | `v0.25.0` | BSD-3-Clause |
| [`github.com/ebitengine/purego`](https://github.com/ebitengine/purego) | `v0.11.3` | Apache-2.0 |
| [`github.com/go-text/typesetting`](https://github.com/go-text/typesetting) | `v0.3.5` | Unlicense / BSD-3-Clause |
| [`golang.org/x/image`](https://cs.opensource.google/go/x/image) | `v0.48.0` | BSD-3-Clause |
| [`golang.org/x/sys`](https://cs.opensource.google/go/x/sys) | `v0.50.0` | BSD-3-Clause |
| [`golang.org/x/text`](https://cs.opensource.google/go/x/text) | `v0.44.0` | BSD-3-Clause |
| [`golang.org/x/net`](https://cs.opensource.google/go/x/net) | `v0.62.0` | BSD-3-Clause (build tool only) |

## External Runtime Executables (Not Bundled)

Mygopia invokes external CLI tools installed separately by the user as local subprocesses (neither bundled nor linked into Mygopia):

- **[Kopia](https://github.com/kopia/kopia)** (Apache-2.0) — required backup engine (`kopia server start`).
- **[Rclone](https://github.com/rclone/rclone)** (MIT) — optional, only required when connecting Rclone storage remotes.
