package app

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"mygopia/internal/config"
)

// activeLangAtom stores the resolved display language thread-safely across UI & background tasks.
var activeLangAtom atomic.Value

func init() {
	activeLangAtom.Store(config.LangEN)
}

func currentLanguage() string {
	if v, ok := activeLangAtom.Load().(string); ok && v != "" {
		return v
	}
	return config.LangEN
}

func matchLocaleTag(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	lower = strings.ReplaceAll(lower, "_", "-")
	if i := strings.IndexByte(lower, '.'); i >= 0 {
		lower = lower[:i]
	}
	if i := strings.IndexByte(lower, '@'); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case lower == "":
		return ""
	case strings.HasPrefix(lower, "zh-hant"), strings.HasPrefix(lower, "zh-tw"), strings.HasPrefix(lower, "zh-hk"), strings.HasPrefix(lower, "zh-mo"):
		return config.LangZhHant
	case strings.HasPrefix(lower, "zh"):
		return config.LangZhHans
	case strings.HasPrefix(lower, "ja"):
		return config.LangJA
	case strings.HasPrefix(lower, "fr"):
		return config.LangFR
	case strings.HasPrefix(lower, "de"):
		return config.LangDE
	case strings.HasPrefix(lower, "es"):
		return config.LangES
	case strings.HasPrefix(lower, "en"):
		return config.LangEN
	default:
		return ""
	}
}

// ResolveLanguage resolves config.LangSystem or an explicit language code against preferred OS locales.
func ResolveLanguage(mode string, preferred []string) string {
	switch mode {
	case config.LangEN, config.LangZhHans, config.LangZhHant, config.LangJA, config.LangFR, config.LangDE, config.LangES:
		return mode
	default:
		for _, tag := range preferred {
			if m := matchLocaleTag(tag); m != "" {
				return m
			}
		}
		for _, envKey := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if m := matchLocaleTag(os.Getenv(envKey)); m != "" {
				return m
			}
		}
		return config.LangEN
	}
}

func setCurrentLanguage(lang string) {
	switch lang {
	case config.LangZhHans, config.LangZhHant, config.LangJA, config.LangFR, config.LangDE, config.LangES:
		activeLangAtom.Store(lang)
	default:
		activeLangAtom.Store(config.LangEN)
	}
}

func allTranslationCatalogs() map[string]map[string]string {
	return map[string]map[string]string{
		config.LangZhHans: zhHansCatalog,
		config.LangZhHant: zhHantCatalog,
		config.LangJA:     jaCatalog,
		config.LangFR:     frCatalog,
		config.LangDE:     deCatalog,
		config.LangES:     esCatalog,
	}
}

// tr returns the localized string for the active language, falling back to English.
func tr(en string) string {
	switch currentLanguage() {
	case config.LangZhHans:
		if s, ok := zhHansCatalog[en]; ok {
			return s
		}
	case config.LangZhHant:
		if s, ok := zhHantCatalog[en]; ok {
			return s
		}
	case config.LangJA:
		if s, ok := jaCatalog[en]; ok {
			return s
		}
	case config.LangFR:
		if s, ok := frCatalog[en]; ok {
			return s
		}
	case config.LangDE:
		if s, ok := deCatalog[en]; ok {
			return s
		}
	case config.LangES:
		if s, ok := esCatalog[en]; ok {
			return s
		}
	}
	return en
}

// trf formats a localized string.
func trf(format string, args ...any) string {
	return fmt.Sprintf(tr(format), args...)
}

var zhHansCatalog = map[string]string{
	// App & Navigation
	"Mygopia":                   "Mygopia",
	"Workspace":                 "工作区",
	"Backups":                   "备份源",
	"Snapshots":                 "快照浏览",
	"Search":                    "全局搜索",
	"Policies":                  "备份策略",
	"Tasks & Logs":              "任务与日志",
	"Repository":                "仓库与维护",
	"Settings":                  "应用设置",
	"Repositories":              "备份仓库",
	"Add Repository":            "添加仓库",
	"Connect or Create":         "连接或创建仓库",
	"View":                      "视图",
	"Toggle Sidebar":            "收起/展开侧边栏",
	"Expand Sidebar":            "展开侧边栏",
	"Collapse Sidebar":          "收起侧边栏",
	"Refresh":                   "刷新",
	"Connected":                 "已连接",
	"Disconnected":              "未连接",
	"Starting…":                 "正在启动…",
	"Server Offline":            "服务未启动",
	"Start Server":              "启动服务",
	"Open Mygopia":              "打开 Mygopia",
	"Backup All Sources":        "立即备份全部源",
	"Quit":                      "退出",
	"Cancel":                    "取消",
	"Close":                     "关闭",
	"Save":                      "保存",
	"Apply":                     "应用",
	"Delete":                    "删除",
	"Remove":                    "移除",
	"Browse…":                   "浏览…",
	"Edit":                      "编辑",
	"Copy":                      "复制",
	"Copied to clipboard":       "已复制到剪贴板",
	"Yes":                       "是",
	"No":                        "否",
	"Inherited":                 "继承默认",
	"Enabled":                   "已启用",
	"Disabled":                  "已禁用",
	"Loading…":                  "加载中…",
	"None":                      "无",
	"All":                       "全部",
	"Files":                     "文件",
	"Folders":                   "文件夹",
	"Name":                      "名称",
	"Path":                      "路径",
	"Size":                      "大小",
	"Modified":                  "修改时间",
	"Status":                    "状态",
	"Source":                    "备份源",
	"Actions":                   "操作",
	"Description":               "描述备注",
	"Password":                  "密码",
	"Optional":                  "可选",

	// Overview & Backups View
	"Storage":                            "存储后端",
	"Active Sources":                     "备份源数量",
	"Search Index":                       "搜索索引",
	"Running Tasks":                      "运行中任务",
	"Not indexed yet":                    "尚未建立索引",
	"1 snapshot":                         "1 个快照",
	"%d snapshots":                       "%d 个快照",
	"%d files · %d deleted":              "%d 个文件 · %d 个已删除",
	"All tasks idle":                     "当前无后台任务",
	"%d active":                          "%d 个正在执行",
	"Add Folder":                         "添加备份目录",
	"Backup All":                         "全部立即备份",
	"No backup sources yet":              "暂无备份目录",
	"Add a local folder to start encrypted, deduplicated backups.": "添加一个本地文件夹，即可开始加密去重备份。",
	"Backup Now":                         "立即备份",
	"Cancel Backup":                      "取消备份",
	"Pause":                              "暂停",
	"Resume":                             "恢复",
	"Browse":                             "浏览快照",
	"Policy":                             "策略",
	"Remove Source":                      "移除备份源",
	"Add Backup Source":                  "添加备份源",
	"Folder Path":                        "文件夹路径",
	"Start first backup immediately":     "添加后立即开始首次备份",
	"Add & Backup":                       "添加并备份",
	"Last backup: %s":                    "上次备份：%s",
	"Next run: %s":                       "下次计划：%s",
	"Manual schedule":                    "仅手动备份",
	"Never backed up":                    "尚未生成快照",
	"Uploading…":                         "正在上传…",
	"Idle":                               "空闲",
	"Paused":                             "已暂停",
	"Pending":                            "等待中",
	"Failed":                             "失败",
	"Backup triggered":                   "已触发备份任务",
	"Source added":                       "已添加备份目录",
	"Source removed":                     "已移除备份源",
	"Open in Finder":                     "在访达中显示",
	"Reveal in File Manager":             "在文件管理器中显示",

	// Connect / Create Repository Wizard
	"Connect to Repository":              "连接已有仓库",
	"Create New Repository":              "创建新仓库",
	"Connect Existing":                   "连接已有仓库",
	"Create New":                         "创建新仓库",
	"Repository Name":                    "仓库显示名称",
	"Storage Provider":                   "存储类型",
	"Local Folder / NAS":                 "本地目录 / NAS",
	"S3 / S3-Compatible":                 "S3 / 兼容对象存储 (MinIO / R2)",
	"Backblaze B2":                       "Backblaze B2",
	"SFTP / SSH":                         "SFTP / SSH 服务器",
	"WebDAV":                             "WebDAV 存储",
	"Rclone Remote":                      "Rclone 远程存储",
	"Google Cloud Storage":               "Google Cloud Storage",
	"Azure Blob Storage":                 "Azure Blob 存储",
	"Kopia Server":                       "Kopia 仓库服务端",
	"Repository Token":                   "连接令牌 (Token)",
	"Repository Directory":               "仓库存储目录",
	"Bucket Name":                        "存储桶 (Bucket)",
	"Endpoint":                           "端点地址 (Endpoint)",
	"Region":                             "区域 (Region)",
	"Access Key ID":                      "Access Key ID",
	"Secret Access Key":                  "Secret Access Key",
	"Object Prefix":                      "路径前缀 (Prefix)",
	"Use HTTP (Disable TLS)":             "使用 HTTP（禁用 TLS）",
	"Key ID":                             "Key ID",
	"Application Key":                    "Application Key",
	"Host":                               "主机地址 (Host)",
	"Port":                               "端口 (Port)",
	"Username":                           "用户名",
	"Remote Path":                        "远程路径",
	"SSH Key File":                       "SSH 私钥路径",
	"Known Hosts File":                   "known_hosts 路径",
	"Server URL":                         "服务器 URL",
	"TLS Cert Fingerprint (SHA256)":      "TLS 证书指纹 (SHA256)",
	"Rclone Remote Path (remote:path)":   "Rclone 远程路径 (例如 remote:bucket)",
	"Container Name":                     "容器名称 (Container)",
	"Storage Account":                    "存储账户 (Account)",
	"Storage Access Key":                 "访问密钥 (Access Key)",
	"Service Account JSON File":          "服务账号 JSON 文件路径",
	"Paste Kopia Token":                  "粘贴 Kopia 连接令牌",
	"Repository Password":                "仓库主密码",
	"Confirm Password":                   "确认主密码",
	"Passwords do not match":             "两次输入的密码不一致",
	"Password is required":               "请输入仓库密码",
	"Advanced Format Options":            "高级加密与分块参数",
	"Encryption Algorithm":               "加密算法",
	"Hash Algorithm":                     "哈希算法",
	"Content Splitter":                   "内容分块器 (Splitter)",
	"Reed-Solomon ECC":                   "纠删码校验 (ECC)",
	"Disabled (Default)":                 "不启用（默认）",
	"ECC Overhead (%)":                   "纠删码冗余比例 (%)",
	"Client Description":                 "本机客户端标识描述",
	"Read-only Connection":               "以只读模式连接",
	"Connect Repository":                 "连接仓库",
	"Initialize & Connect":               "初始化并连接",
	"Repository connected":               "仓库连接成功",
	"Repository created and connected":   "新仓库已创建并连接",
	"Remove from List":                   "从列表中移除",
	"Repository directory path is required": "请填写仓库存储目录路径",
	"S3 bucket name is required":         "请填写 S3 存储桶名称",
	"B2 bucket name is required":         "请填写 B2 存储桶名称",
	"Server URL is required":             "请填写服务器 URL",
	"Token is required":                  "请填写连接令牌 (Token)",

	// Snapshots & File Browser
	"Select Source":                      "选择备份源",
	"Show identical snapshots":           "显示内容无变化的快照",
	"No snapshots for this source":       "该备份源尚无快照",
	"Select a snapshot to browse files":  "请在左侧选择一个时间点快照以浏览目录与文件",
	"Filter current folder…":             "筛选当前目录名称…",
	"Root":                               "根目录",
	"Up":                                 "上级目录",
	"Pin":                                "置顶锁定",
	"Unpin":                              "取消锁定",
	"Pinned":                             "已锁定",
	"Edit Note":                          "编辑备注",
	"Mount":                              "挂载快照",
	"Unmount":                            "卸载挂载",
	"Open Mount":                         "打开挂载目录",
	"Restore…":                           "恢复数据…",
	"Save File…":                         "导出文件…",
	"Delete Snapshot":                    "删除快照",
	"Object ID":                          "对象 ID",
	"Copy Object ID":                     "复制对象 ID",
	"Snapshot Note":                      "快照描述备注",
	"Pin Name":                           "锁定标签",
	"Save Note":                          "保存备注",
	"Snapshot updated":                   "快照信息已更新",
	"Snapshot deleted":                   "快照已删除",
	"Snapshot mounted at %s":             "快照已挂载至 %s",
	"Snapshot unmounted":                 "已卸载快照挂载点",
	"File saved to %s":                   "文件已保存至 %s",

	// Restore Dialog
	"Restore from Snapshot":              "从快照恢复数据",
	"Restore Mode":                       "恢复方式",
	"Restore to Folder":                  "恢复到本地目录",
	"Export as ZIP Archive":              "导出为 ZIP 压缩包",
	"Export as TAR Archive":              "导出为 TAR 归档包",
	"Target Destination":                 "目标保存路径",
	"Overwrite existing files":           "覆盖已存在的文件",
	"Overwrite existing directories":     "覆盖已存在的文件夹",
	"Incremental (skip unchanged files)": "增量恢复（跳过未变化文件）",
	"Delete extra files in target":       "删除目标目录中多余的文件",
	"Ignore permission errors":           "忽略权限设置错误",
	"Skip restoring file owners":         "跳过恢复文件所有者 (UID/GID)",
	"Start Restore":                      "开始恢复",
	"Restore task started":               "恢复任务已启动，可在「任务与日志」查看进度",

	// KopiaLens-Style Search View
	"Search files & folders across all snapshots…": "搜索所有历史快照中的文件与文件夹（留空按体积排序）…",
	"Update Index":                       "更新索引",
	"Indexing…":                          "正在索引…",
	"Current Repository":                 "当前仓库",
	"All Repositories":                   "所有已索引仓库",
	"All Sources":                        "所有备份源",
	"Deleted only":                       "仅已从磁盘删除",
	"Versions ≥ 2":                       "多版本 (≥2)",
	"Any Size":                           "不限大小",
	"> 10 MB":                            "> 10 MB",
	"> 100 MB":                           "> 100 MB",
	"> 1 GB":                             "> 1 GB",
	"Include ext (pdf, zip)":             "包含后缀: pdf, zip",
	"Exclude ext (tmp, mp4)":             "排除后缀: tmp, mp4",
	"On Disk":                            "在盘",
	"Deleted":                            "已删除",
	"Versions":                           "版本数",
	"Last Seen":                          "最后出现",
	"First Seen":                         "首次出现",
	"Matched %d items (%s)":              "匹配 %d 项（合计 %s）",
	"DAG Cache: %d dirs · %s":            "目录树缓存：%d 个目录 · %s",
	"No indexed snapshots yet":           "尚未建立快照搜索索引",
	"Click \"Update Index\" to scan snapshot trees. Unchanged folders are cached locally and never fetched twice.": "点击「更新索引」扫描快照目录树。未变动的目录对象会持久缓存到本地，后续增量更新仅需毫秒级时间。",
	"No matching items":                  "没有匹配的文件或文件夹",
	"Version History":                    "历史版本记录",
	"Full Path":                          "完整路径",
	"First seen %s · Last seen %s":       "首次出现 %s · 最后出现 %s",
	"Save Version…":                      "另存此版本…",
	"Open Snapshot":                      "在快照中定位",
	"Index updated (%d files, %d folders)": "索引已更新（%d 个文件，%d 个文件夹）",

	// Policies Studio
	"Policy Targets":                     "策略作用范围",
	"Global Policy":                      "全局默认策略",
	"Add Path Policy":                    "新增目录策略",
	"Target Folder Path":                 "目标目录路径",
	"Save Policy":                        "保存策略",
	"Reset Changes":                      "重置未保存修改",
	"Delete Override":                    "删除此级覆盖策略",
	"Retention":                          "保留规则",
	"Schedule":                           "自动计划",
	"Files & Ignores":                    "文件与忽略",
	"Compression":                        "压缩设置",
	"Upload & Errors":                    "上传与容错",
	"Actions & Logging":                  "钩子与日志",
	"Keep Latest Snapshots":              "保留最近快照数 (Latest)",
	"Keep Hourly":                        "保留每小时快照数 (Hourly)",
	"Keep Daily":                         "保留每日快照数 (Daily)",
	"Keep Weekly":                        "保留每周快照数 (Weekly)",
	"Keep Monthly":                       "保留每月快照数 (Monthly)",
	"Keep Annual":                        "保留每年快照数 (Annual)",
	"Ignore Identical Snapshots":         "内容未变化时不生成重复快照",
	"Leave 0 to inherit from parent policy. Effective value: %s": "设为 0 表示继承上级策略。当前生效值：%s",
	"Manual Backups Only":                "仅手动触发备份（关闭自动计划）",
	"Backup Interval (Minutes)":          "定时间隔（分钟）",
	"Fixed Times of Day (HH:MM, one per line)": "每日固定备份时间（HH:MM，每行一个）",
	"Cron Expressions (one per line)":    "Cron 计划表达式（每行一个）",
	"Run Missed Backups on Startup":      "启动时补跑错过的备份计划",
	"Upcoming Scheduled Runs":            "未来计划执行时间预览",
	"No automatic runs scheduled":        "未启用自动定时计划",
	"Every 15m":                          "每 15 分钟",
	"Hourly":                             "每 1 小时",
	"Every 6h":                           "每 6 小时",
	"Daily":                              "每 24 小时",
	"Ignore Rules (one pattern per line)": "忽略规则（每行一条匹配规则）",
	"Do not inherit parent ignore rules": "不继承上级策略的忽略规则",
	"Dot-Ignore Files (e.g. .kopiaignore, .gitignore)": "读取目录内忽略文件（如 .kopiaignore）",
	"Skip Cache Directories (CACHEDIR.TAG)": "自动跳过缓存目录 (CACHEDIR.TAG)",
	"Stay on One Filesystem":             "不跨越文件系统挂载边界",
	"Max File Size (MB, 0 = unlimited)":  "单文件大小上限（MB，0 为不限）",
	"Add Common Ignores":                 "添加常见缓存忽略项",
	"Compression Algorithm":              "内容压缩算法",
	"Metadata Compression":               "元数据压缩算法",
	"Inherit Default":                    "继承默认策略",
	"Min Compress Size (Bytes)":          "最小压缩体积（字节）",
	"Max Compress Size (Bytes, 0 = no max)": "最大压缩体积（字节，0 为不限）",
	"Only Compress Extensions (one per line, e.g. .txt)": "仅压缩以下后缀（每行一个，如 .txt）",
	"Never Compress Extensions (one per line, e.g. .mp4)": "永不压缩以下后缀（每行一个，如 .mp4）",
	"Max Parallel File Reads":            "最大并发文件读取数",
	"Max Parallel Snapshots (Global/Host)": "最大并发快照源数（仅全局/主机级生效）",
	"Parallel Upload Above Size (MB)":    "大文件分片并发上传阈值 (MB)",
	"Ignore File Read Errors":            "忽略文件读取错误（继续备份其余文件）",
	"Ignore Directory Read Errors":       "忽略目录读取错误",
	"Ignore Unknown Entry Types":         "忽略未知文件系统条目类型",
	"Enable Hook Actions on Client":      "提示：执行钩子脚本需在客户端连接配置中启用 Actions",
	"Hook Actions":                       "前后置钩子脚本",
	"Logging Levels":                     "快照日志详细度",
	"Before Snapshot Root Script":        "快照开始前执行脚本 (Before Root)",
	"After Snapshot Root Script":         "快照完成后执行脚本 (After Root)",
	"Hook Execution Mode":                "钩子执行模式",
	"Hook Timeout (Seconds)":             "脚本超时时间（秒）",
	"Directory Snapshotted Log Level (0-10)": "已快照目录日志级别 (0-10)",
	"Directory Ignored Log Level (0-10)": "已忽略目录日志级别 (0-10)",
	"Entry Snapshotted Log Level (0-10)": "已快照文件日志级别 (0-10)",
	"Entry Ignored Log Level (0-10)":     "已忽略文件日志级别 (0-10)",
	"Policy saved":                       "备份策略已保存",
	"Policy override deleted":            "已删除此覆盖策略，恢复继承默认",

	// Tasks & Logs
	"Background Tasks":                   "后台任务",
	"Server Output":                      "Kopia 服务日志",
	"Kind":                               "类型",
	"Started":                            "开始时间",
	"Duration":                           "耗时",
	"Progress":                           "进度详情",
	"Running":                            "运行中",
	"Canceling":                          "正在取消",
	"Succeeded":                          "已完成",
	"Canceled":                           "已取消",
	"Cancel Task":                        "取消任务",
	"Task Logs":                          "任务日志",
	"No tasks recorded yet":              "暂无任务记录",
	"Select a task to inspect logs":      "点击上方任意任务查看详细执行日志",

	// Repository & Maintenance
	"Connection & Format":                "连接与加密格式",
	"Storage & Deduplication":            "存储占用与去重统计",
	"Bandwidth & Rate Limits":            "带宽与速率限制",
	"Maintenance":                        "仓库维护与垃圾回收",
	"Notifications":                      "外部通知配置",
	"Config File":                        "本地配置文件",
	"Format Version":                     "仓库格式版本",
	"Client Identity":                    "客户端身份",
	"Content Compression":                "内容压缩支持",
	"Sync & Refresh":                     "同步刷新仓库",
	"Disconnect Repository":              "断开仓库连接",
	"Original Logical Data":              "原始数据体积",
	"Deduplicated Packed Size":           "去重压缩后占用",
	"Content Chunks":                     "内容数据块数",
	"Uncompacted Pack Data":              "待整理数据量",
	"Space Saved":                        "压缩节省空间",
	"Not detected":                       "未检测到",
	"Refresh Stats":                      "刷新统计",
	"Max Upload Speed (KB/s, 0 = unlimited)":   "最大上传速度（KB/s，0 为不限速）",
	"Max Download Speed (KB/s, 0 = unlimited)": "最大下载速度（KB/s，0 为不限速）",
	"Max Reads / sec":                    "每秒最大读取请求数",
	"Max Writes / sec":                   "每秒最大写入请求数",
	"Max Lists / sec":                    "每秒最大列举请求数",
	"Save Bandwidth Limits":              "保存速率限制",
	"Throttle limits updated":            "带宽与速率限制已更新",
	"Maintenance Owner":                  "维护负责人 (Owner)",
	"Enable Quick Maintenance":           "启用快速维护 (Quick Maintenance)",
	"Quick Interval (Hours)":             "快速维护间隔（小时）",
	"Enable Full Maintenance":            "启用完整维护 (Full Maintenance)",
	"Full Interval (Hours)":              "完整维护间隔（小时）",
	"Next Quick Run":                     "下次快速维护",
	"Next Full Run":                      "下次完整维护",
	"Run Quick Maintenance":              "立即运行快速维护",
	"Run Full Maintenance":               "立即运行完整维护",
	"Save Maintenance Config":            "保存维护参数",
	"Maintenance completed":              "仓库维护已完成",
	"Notification Profiles":              "通知渠道列表",
	"New Notification Profile":           "新增通知配置",
	"Profile Name":                       "配置名称",
	"Notification Method":                "通知方式",
	"Minimum Severity":                   "最低通知级别",
	"Webhook URL":                        "Webhook 地址",
	"HTTP Method":                        "HTTP 方法",
	"SMTP Server":                        "SMTP 服务器",
	"SMTP Port":                          "SMTP 端口",
	"SMTP Username":                      "SMTP 用户名",
	"SMTP Password":                      "SMTP 密码",
	"Mail From":                          "发件人邮箱 (From)",
	"Mail To":                            "收件人邮箱 (To)",
	"Pushover App Token":                 "Pushover 应用 Token",
	"Pushover User Key":                  "Pushover 用户 Key",
	"Send Test":                          "发送测试通知",
	"Save Profile":                       "保存通知配置",
	"Test notification sent":             "测试通知发送成功",
	"Notification profile saved":         "通知配置已保存",

	// App Settings
	"Appearance & Language":              "外观与语言",
	"Appearance":                         "外观模式",
	"Follow System":                      "跟随系统",
	"Light":                              "浅色",
	"Dark":                               "深色",
	"Accent Color":                       "主题色",
	"Blue":                               "经典蓝",
	"Indigo":                             "靛蓝",
	"Violet":                             "紫罗兰",
	"Teal":                               "青碧",
	"Green":                              "翠绿",
	"Orange":                             "琥珀橙",
	"Rose":                               "玫瑰红",
	"Display Language":                   "界面语言",
	"Interface & Font Size":              "界面与字体大小",
	"Small":                              "小",
	"Medium":                             "中",
	"Large":                              "大",
	"Zoom In":                            "放大界面",
	"Zoom Out":                           "缩小界面",
	"Default Size":                       "恢复默认大小",
	"Desktop Behavior":                   "桌面交互行为",
	"Launch at system login":             "登录系统时开机自启动",
	"Show icon in menu bar":              "在菜单栏显示图标",
	"Show icon in system tray":           "在系统托盘显示图标",
	"Keep running in background when window closes": "关闭主窗口时保持在后台运行",
	"Show desktop notifications for Kopia events":    "显示 Kopia 桌面通知提醒",
	"Automatically update search index after backups": "备份与连接完成后自动增量更新搜索索引",
	"Kopia Engine":                       "Kopia 引擎路径",
	"Kopia Binary Path":                  "Kopia 可执行文件路径",
	"Leave blank to auto-detect (currently: %s)": "留空自动检测（当前检测路径：%s）",
	"Kopia Config Directory":             "Kopia 配置目录",
	"Save Settings":                      "保存设置",
	"Settings saved":                     "应用设置已保存",
	"About Mygopia":                      "关于 Mygopia",
	"Data Directory":                     "本地数据目录",

	// Refresh, Snapshot Diff, Time Range, Advanced Filters & Pagination
	"%s: %s":                                           "%s：%s",
	"Refreshed":                                        "已刷新最新状态",
	"Refreshing…":                                      "正在刷新…",
	"Snapshot Diff":                                    "快照对比",
	"Compare":                                          "对比",
	"Compare…":                                         "对比快照…",
	"Compare Snapshots":                                "对比快照差异",
	"Compare with Latest":                              "与最新快照对比",
	"Base Snapshot (Older)":                            "基准快照（旧）",
	"Target Snapshot (Newer)":                          "对比快照（新）",
	"Swap":                                             "交换",
	"Swap Snapshots":                                   "交换新旧快照",
	"Latest vs Previous":                               "最新 vs 上一次",
	"Latest vs 7d Ago":                                 "最新 vs 7天前",
	"Latest vs 30d Ago":                                "最新 vs 30天前",
	"Latest vs 1y Ago":                                 "最新 vs 1年前",
	"Latest vs Oldest":                                 "最新 vs 最早",
	"All Changes":                                      "全部变更",
	"Added":                                            "新增",
	"Changed":                                          "修改",
	"Change":                                           "变更",
	"Old Size":                                         "旧版本大小",
	"New Size":                                         "新版本大小",
	"Size Delta":                                       "体积变化",
	"Changed Items":                                    "变更总数",
	"Net Size Delta":                                   "净体积变化",
	"Added Files":                                      "新增项",
	"Modified Files":                                   "修改项",
	"Deleted Files":                                    "删除项",
	"No differences between the selected snapshots":    "所选两个快照之间没有任何差异",
	"Need at least 2 snapshots to compare":             "当前备份源至少需要 2 个快照才能进行对比",
	"Select two different snapshots to compare":        "请选择两个不同的快照进行对比",
	"Restore Old Version…":                             "恢复旧版本…",
	"Restore New Version…":                             "恢复新版本…",
	"Save Old Version…":                                "另存旧版本…",
	"Save New Version…":                                "另存新版本…",
	"Filter changed paths…":                            "筛选变更路径或文件名…",
	"Time Range":                                       "时间范围",
	"All Time":                                         "全部时间",
	"Today":                                            "今天",
	"Last 24 Hours":                                    "最近 24 小时",
	"Last 7 Days":                                      "最近 7 天",
	"Last 30 Days":                                     "最近 30 天",
	"Last 90 Days":                                     "最近 90 天",
	"Last 1 Year":                                      "最近 1 年",
	"Custom Date Range":                                "自定义日期",
	"All Years":                                        "全部年份",
	"From":                                             "从",
	"To":                                               "至",
	"Filter notes or time…":                            "筛选备注或时间…",
	"Pinned only":                                      "仅已锁定",
	"All Status":                                       "全部状态",
	"On Disk Only":                                     "仅在盘",
	"Deleted Only":                                     "仅已删除",
	"Min Size":                                         "最小体积",
	"Max Size":                                         "最大体积",
	"> 1 MB":                                           "> 1 MB",
	"< 1 MB":                                           "< 1 MB",
	"< 10 MB":                                          "< 10 MB",
	"< 100 MB":                                         "< 100 MB",
	"< 1 GB":                                           "< 1 GB",
	"Path prefix (e.g. Designs/)":                      "目录前缀（如 Designs/）",
	"Reset Filters":                                    "重置筛选",
	"Advanced Filters":                                 "高级筛选",
	"Hide Filters":                                     "收起筛选",
	"Showing %d–%d of %d":                              "显示 %d–%d / 共 %d 项",
	"Page %d / %d":                                     "第 %d / %d 页",
	"Prev":                                             "上一页",
	"Next":                                             "下一页",
	"Load More (%d left)":                              "加载更多（剩余 %d 项）",
	"Show All (%d)":                                    "展开全部（%d 项）",
	"%d / %d snapshots":                                "%d / %d 个快照",
	"Syncing repository…":                              "正在同步仓库…",
	"Loading snapshots…":                               "正在加载快照…",
	"Comparing snapshots…":                             "正在对比快照…",
	"Indexing (%d dirs)…":                              "正在索引 (%d 个目录)…",
	"Cancel Refresh":                                   "取消刷新",
	"Refresh canceled":                                 "已取消刷新",
	"Filter snapshot notes or time…":                   "筛选快照备注或时间…",
	"Filter changed paths, names, or time…":            "筛选变更路径、文件名或时间…",
	"No matching snapshots":                            "无匹配快照",
	"Sync Index":                                       "增量更新索引",
	"Index Synced (%d snapshots)":                      "索引已同步（%d 个快照）",
	"Diff Latest":                                      "对比最新",
	"Locate":                                           "定位快照",
	"Save…":                                            "另存…",
	"Unsaved changes":                                  "未保存修改",
	"Notification profile deleted":                     "通知配置已删除",
	"Filter Snapshots":                                 "快照筛选",
	"Filter path, name, or time…":                      "筛选路径、文件名或时间…",
	"Ext: pdf, zip":                                    "后缀: pdf, zip",
	"Current Local Folder (Next Backup)":               "当前本地目录（待备份状态）",
	"Local vs Latest":                                  "本地当前 vs 最新",
	"Diff Local":                                       "对比本地变更",
	"Comparing with local folder…":                     "正在对比本地目录…",
	"Task: Snapshot":                                   "快照备份",
	"Task: Restore":                                    "数据恢复",
	"Task: Maintenance":                                "仓库维护",
	"Task: Estimate":                                   "体积估算",
	"Task: Sync":                                       "仓库同步",
	"SFTP host is required":                            "请输入 SFTP 主机地址",
	"SFTP username and remote path are required":       "请输入 SFTP 用户名与远程路径",
	"SFTP password or SSH key file is required":        "请提供 SFTP 密码或 SSH 密钥文件",
	"SFTP known_hosts file is required":                "请指定 known_hosts 文件（或开启外部 SSH 命令模式）",
	"WebDAV server URL is required":                    "请输入 WebDAV 服务器地址",
	"Rclone remote path is required":                   "请输入 Rclone 远程路径",
	"Rclone Binary Path":                               "Rclone 可执行文件路径",
	"Rclone Arguments":                                 "Rclone 附加参数",
	"Session Token":                                    "临时会话令牌 (Session Token)",
	"Skip TLS Certificate Verification":                "跳过 TLS 证书校验（自签名 HTTPS）",
	"Use External SSH Command (Agent / ~/.ssh/config)": "使用系统外部 SSH 命令（支持 SSH Agent / ~/.ssh/config）",
	"SSH Command":                                      "SSH 命令",
	"SSH Arguments":                                    "SSH 附加参数",
	"SAS Token":                                        "SAS 令牌 (SAS Token)",
	"Storage Domain":                                   "存储域名 (Storage Domain)",
	"Azure storage access key or SAS token is required": "请提供 Azure 存储访问密钥或 SAS 令牌",
	"GCS bucket name is required":                      "请输入 GCS 存储桶名称",
	"Azure container and storage account are required": "请输入 Azure 容器名与存储账户名",
	"Refresh (⌘R) syncs repository & incremental index; click here to manually scan snapshot trees (cached folders are skipped).": "刷新 (⌘R) 会同步最新快照并增量更新索引；也可点击此处手动触发目录树扫描（已缓存目录自动秒跳过）。",
}
