# Mygopia

[English](./README.md) | 简体中文

基于 [Mygo](https://github.com/egoist/mygo) 框架构建的原生 [Kopia](https://kopia.io) 桌面图形客户端，支持 macOS、Windows 与 Linux，提供多仓库管理、跨快照全局文件搜索与本地目录差异对比。

## 功能特性

- **原生跨平台界面**：基于 [Mygo](https://github.com/egoist/mygo) 构建，原生支持 macOS、Windows 与 Linux。
- **多仓库统一管理**：支持连接或新建本地文件系统、S3 / MinIO / Cloudflare R2、Backblaze B2、SFTP、WebDAV、GCS、Azure Blob、Rclone、Kopia Server 及 Token 仓库。
- **跨快照全局搜索**：增量索引 Kopia 内容寻址目录树，快速检索历史文件、已删除文件及完整版本历史。
- **快照对比与本地待备份对比**：对比任意两次历史快照，或直接将最新快照与当前本地文件夹对比（自动识别 `.kopiaignore` 与 `CACHEDIR.TAG`）。
- **完整备份工作流**：管理备份源、保留与定时策略、挂载浏览、单文件/整目录/压缩包恢复、带宽限速、仓库维护与通知配置。
- **个性化与多语言**：支持浅色 / 深色 / 跟随系统、7 种主题强调色、界面缩放、菜单栏/系统托盘开关，以及 7 种界面语言（`简体中文`、`繁體中文`、`English`、`日本語`、`Français`、`Deutsch`、`Español`）。

## 前置依赖

Mygopia 调用系统已安装的 `kopia` 命令行程序作为底层备份引擎（仅在使用 Rclone 存储后端时才需要安装 `rclone`）：

1. **Kopia CLI（必装）**
   - **macOS**：`brew install kopia`
   - **Windows**：`scoop install kopia` 或 `winget install Kopia.Kopia`
   - **Linux**：参考 [Kopia 官方安装文档](https://kopia.io/docs/installation/)
2. **Rclone（可选，仅连接 Rclone 远程存储时需要）**
   - **macOS**：`brew install rclone`
   - **Windows**：`scoop install rclone` 或 `winget install Rclone.Rclone`
   - **Linux**：`sudo apt install rclone` 或访问 [rclone.org/downloads](https://rclone.org/downloads/)

启动后会自动检测常用安装路径下的 `kopia` 与 `rclone`；也可在 **应用设置 → Kopia 引擎配置** 中手动指定可执行文件路径。

## 安装方式

### macOS（Homebrew Tap）

```bash
brew tap EvensonCalder/tap
brew install --cask EvensonCalder/tap/mygopia
```

> **macOS Gatekeeper 说明**：通过 Cask 安装会自动安装 `kopia` 依赖。由于构建产物未进行付费 Apple Developer ID 公证，首次打开若遇到系统拦截，可在 **系统设置 → 隐私与安全性** 中点击 **仍要打开**，或手动移除隔离属性：
> ```bash
> sudo xattr -dr com.apple.quarantine /Applications/Mygopia.app
> ```

### 直接下载安装包（macOS / Windows / Linux）

前往 [Releases](https://github.com/EvensonCalder/mygopia/releases) 下载对应平台安装包：

- **macOS（Universal 通用架构）**：`mygopia-<version>.dmg`
- **Windows（`x64` / `arm64`）**：`mygopia-setup-<version>-<arch>.exe`（安装版）或 `mygopia-<version>-windows-<arch>-portable.zip`（便携版）
- **Linux（`amd64` / `arm64`）**：`mygopia_<version>_<arch>.deb` 或 `mygopia-<version>-linux-<arch>.tar.gz`

## 快速上手

1. 先安装好 `kopia`（如需使用 Rclone 存储后端则一并安装 `rclone`）。
2. 打开 **Mygopia** —— 默认配置目录下已有的 Kopia 仓库会被自动发现；也可点击左侧边栏顶部的 **`+`** 连接已有仓库或创建新仓库。
3. 在 **备份源** 中添加需要备份的目录，在 **备份策略** 中配置保留份数与自动计划，随时通过 **全局搜索**（`⌘F` / `Ctrl+F`）或 **快照对比** 查找并恢复历史版本。

## 从源码构建

需要 Go `1.27+`：

```bash
git clone https://github.com/EvensonCalder/mygopia.git
cd mygopia

# 运行测试
go test -race ./...

# 开发模式运行
go tool mygo dev

# 构建当前系统安装包
go tool mygo build
```

## 开源许可

[MIT](./LICENSE) —— 第三方开源依赖声明见 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。
