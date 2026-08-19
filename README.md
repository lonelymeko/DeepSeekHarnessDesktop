# DeepSeek Harness Desktop

一个基于 **Wails v2 + Go** 的非官方桌面包装层，将 DeepSeek AI 的
[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 作为可直接打开的 macOS、Windows、Linux 软件运行。

本项目不把 Harness 逻辑重写成不完整的 Web 副本，而是随应用携带官方 `@deepseek-ai/dsh` npm 发行包和独立 Node.js 运行时。Wails 启动本机 Harness 服务，并通过同源反向代理显示完整 Web UI，因此插件安装、工作区文件、终端、WebSocket、文档和产物下载仍走官方实现。

反向代理会把 Wails WebView 的内部 `wails.localhost` Host/Origin 规范化为 Harness 实际监听的 loopback 地址，满足上游 DNS-rebinding / cross-site 安全栅栏；macOS 使用与 Suzaku 相同的隐藏内嵌透明标题栏和拖动区，预留 44px 避开原生红绿灯，Windows 无边框模式提供拖动区及窗口控制按钮。

> DeepSeek Harness 当前仍是 developer preview。上游明确会出现破坏性更新，本仓库因此默认拒绝自动接受关键启动契约的变化。

## 环境

- Go：见 `go.mod`
- Node.js / npm：仅用于构建 Wails 启动画面；最终应用使用内置 Node
- Wails CLI v2.10.2
- pnpm 不要求用户安装；Harness 插件管理使用上游发行包自带的运行逻辑

中国大陆网络可按需设置代理：

```sh
export https_proxy=http://127.0.0.1:7897
export http_proxy=http://127.0.0.1:7897
export all_proxy=socks5://127.0.0.1:7897
```

## 开发运行

```sh
make dev
```

需要同时检查 WebView 控制台、网络请求和 DOM 时：

```sh
DSH_DESKTOP_DEVTOOLS=1 make dev
```

首次运行会下载固定版本 Node，并安装 `upstream/manifest.json` 固定的 `@deepseek-ai/dsh`。

## 用户数据共用

桌面版、`dsh web` 命令行实例和浏览器中的 Harness 共用同一份项目、会话记忆、设置、凭据和插件：

- macOS：实体目录为 `~/Library/Application Support/DeepSeekHarnessDesktop/dsh`，官方 `~/.dsh` 会自动迁移并替换为指向该目录的兼容链接。
- Linux：实体目录为 `${XDG_CONFIG_HOME:-~/.config}/DeepSeekHarnessDesktop/dsh`，官方 `~/.dsh` 同样作为兼容链接。
- Windows：直接使用官方 `%USERPROFILE%\\.dsh`，避免创建链接所需的额外权限。

迁移只移动整个目录，不会拆分或转换内部文件。若检测到旧目录和新目录同时存在，软件会停止启动并输出冲突路径，防止静默覆盖数据。迁移完成后，从桌面版或 `dsh web` 安装插件、打开项目和创建会话都会立即作用于同一份数据。

如需显式指定其他共享目录，可设置：

```sh
export DSH_DESKTOP_HOME=/path/to/shared/dsh
```

## 一键打包

当前平台：

```sh
make package
```

显式目标参数：

```sh
TARGET=darwin/arm64 make package
TARGET=linux/amd64 make package
TARGET=windows/amd64 make package
```

或直接：

```sh
./scripts/package.sh darwin/arm64
./scripts/package.sh linux/amd64
./scripts/package.sh windows/amd64
```

不同操作系统的 Wails/WebView 原生依赖不能可靠地在单机交叉打包；`.github/workflows/release.yml` 使用 macOS、Ubuntu、Windows 原生 runner 一键生成三平台产物。
Linux 使用 Ubuntu 24.04 的 WebKitGTK 4.1，并通过 Wails 的 `webkit2_41` 构建标签编译；Windows workflow 会安装 NSIS 后生成完整安装器。

## 一键更新上游

```sh
make sync
```

同步器会读取上游 `master` 最新 commit、CLI 版本、Node engine，并对以下关键契约做 SHA-256 指纹：

- 根 `package.json`
- CLI `package.json` 与参数解析器
- Web 启动参数解析器
- Web profile 的 Cordis 配置

如果关键文件变化，命令会：

1. 在控制台输出醒目告警；
2. 返回非零状态并停止写入新版本；
3. 生成 `upstream/ADAPTATION_REQUIRED.md`，列出旧/新 commit 和待检查文件；
4. 要求维护者修改 Go 启动器、代理或打包适配后再继续。

人工适配并验证后：

```sh
make sync-accept
```

## 运行时布局

```text
runtime/current/
├── node/                       # 固定版本 Node.js
└── app/
    └── node_modules/@deepseek-ai/dsh
```

- macOS：复制到 `.app/Contents/Resources/runtime`
- Windows/Linux：复制到可执行文件同级 `runtime`
- 开发：使用 `DSH_DESKTOP_RUNTIME=runtime/current`

## Python 边界

当前固定的 DeepSeek Harness 上游 commit 中，生产运行目录 `apps/`、`packages/`、`native/` 没有 Python 业务文件。上游 Python 文件只用于发布或校验脚本，因此桌面运行时无需 Python，也不存在功能降级。此项目自己的上游同步、下载和解包逻辑均使用 Go 实现。

## 许可证与声明

本包装项目采用 MIT License。DeepSeek Harness 及其第三方依赖遵循各自许可证。
本项目是社区包装层，不代表 DeepSeek AI 官方桌面客户端。
