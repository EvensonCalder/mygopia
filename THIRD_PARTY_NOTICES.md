# Third-Party Notices

Mygopia is released under the [MIT License](./LICENSE).

It compiles with zero CGO and links only the following permissive open-source Go modules:

| Module | Version | License |
| --- | --- | --- |
| [`github.com/egoist/mygo`](https://github.com/egoist/mygo) | `v0.8.3` | MIT |
| [`github.com/ebitengine/purego`](https://github.com/ebitengine/purego) | `v0.11.1` | Apache-2.0 |
| [`github.com/go-text/typesetting`](https://github.com/go-text/typesetting) | `v0.3.5` | Unlicense / BSD-3-Clause |
| [`golang.org/x/image`](https://cs.opensource.google/go/x/image) | `v0.46.0` | BSD-3-Clause |
| [`golang.org/x/text`](https://cs.opensource.google/go/x/text) | `v0.43.0` | BSD-3-Clause |
| [`golang.org/x/net`](https://cs.opensource.google/go/x/net) | `v0.59.0` | BSD-3-Clause (build tool only) |

## External Runtime Executables (Not Bundled)

Mygopia invokes external CLI tools installed separately by the user as local subprocesses (neither bundled nor linked into Mygopia):

- **[Kopia](https://github.com/kopia/kopia)** (Apache-2.0) — required backup engine (`kopia server start`).
- **[Rclone](https://github.com/rclone/rclone)** (MIT) — optional, only required when connecting Rclone storage remotes.
