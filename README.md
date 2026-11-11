# Mygopia

English | [简体中文](./README.zh-CN.md)

A native desktop GUI client for [Kopia](https://kopia.io), built with the [Mygo](https://github.com/egoist/mygo) framework. Supports multi-repository management, cross-snapshot file search, and local-vs-snapshot diffing across macOS, Windows, and Linux.

## Features

- **Native Cross-Platform UI**: Built with [Mygo](https://github.com/egoist/mygo) for macOS, Windows, and Linux.
- **Multi-Repository Management**: Connect or create Filesystem, S3 / MinIO / R2, Backblaze B2, SFTP, WebDAV, Google Cloud Storage, Azure Blob, Rclone, Kopia Server, or Token repositories.
- **Cross-Snapshot Global Search**: Incrementally indexes Kopia's content-addressed directory DAG to search files, deleted items, and version history across snapshots.
- **Snapshot & Local Folder Diff**: Compare any two historical snapshots or preview uncommitted changes in a local folder (respecting `.kopiaignore` and `CACHEDIR.TAG`).
- **Full Backup Lifecycle**: Manage sources, retention & schedule policies, snapshot mounts, selective/archive restores, bandwidth throttling, repository maintenance, and notifications.
- **Appearance & Languages**: Light / Dark / System theme, 7 accent colors, UI scaling, optional menu bar / system tray icon, and 7 languages (`English`, `简体中文`, `繁體中文`, `日本語`, `Français`, `Deutsch`, `Español`).

## Prerequisites

Mygopia uses your locally installed `kopia` CLI as its backup engine (and `rclone` only if you use Rclone remotes):

1. **Kopia CLI (Required)**
   - **macOS**: `brew install kopia`
   - **Windows**: `scoop install kopia` or `winget install Kopia.Kopia`
   - **Linux**: follow [Kopia installation instructions](https://kopia.io/docs/installation/)
2. **Rclone (Optional — only for Rclone storage remotes)**
   - **macOS**: `brew install rclone`
   - **Windows**: `scoop install rclone` or `winget install Rclone.Rclone`
   - **Linux**: `sudo apt install rclone` or [rclone.org/downloads](https://rclone.org/downloads/)

Mygopia automatically detects `kopia` and `rclone` from standard system paths, or you can specify custom binary paths in **Settings → Kopia Engine**.

## Installation

### macOS (Homebrew Tap)

```bash
brew tap EvensonCalder/tap
brew install --cask EvensonCalder/tap/mygopia
```

> **macOS Gatekeeper Note**: Installing via the cask automatically installs `kopia` if not already present. Because release builds are ad-hoc signed rather than notarized with a paid Apple Developer ID, macOS Gatekeeper may block the first launch. Allow it in **System Settings → Privacy & Security → Open Anyway**, or clear the quarantine attribute manually:
> ```bash
> sudo xattr -dr com.apple.quarantine /Applications/Mygopia.app
> ```

### Direct Downloads (macOS / Windows / Linux)

Download the latest package from [Releases](https://github.com/EvensonCalder/mygopia/releases):

- **macOS (Universal — Apple Silicon & Intel)**: `mygopia-<version>.dmg`
- **Windows (`x64` / `arm64`)**: `mygopia-setup-<version>-<arch>.exe` (installer) or `mygopia-<version>-windows-<arch>-portable.zip` (portable)
- **Linux (`amd64` / `arm64`)**: `mygopia_<version>_<arch>.deb` or `mygopia-<version>-linux-<arch>.tar.gz`

## Quick Start

1. Install `kopia` (and `rclone` if needed).
2. Launch **Mygopia** — existing Kopia `repository*.config` profiles in your default Kopia config directory are discovered automatically, or click **`+`** in the sidebar to connect/create a repository.
3. Add a local folder in **Backups**, configure retention/schedules in **Policies**, and use **Search** (`⌘F` / `Ctrl+F`) or **Snapshot Diff** to inspect file history.

## Build from Source

Requires Go `1.27+`:

```bash
git clone https://github.com/EvensonCalder/mygopia.git
cd mygopia

# Run tests
go test -race ./...

# Run in development
go tool mygo dev

# Build native package for current OS
go tool mygo build
```

## License

[MIT](./LICENSE) — see [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md) for dependency attributions.
