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

## 网络与代理

桌面版默认跟随操作系统的代理，不需要先导出环境变量。开关在窗口右上角齿轮 →「网络」→「网络请求走系统代理」，**默认开启**。

- 开启时启动器读取系统代理（macOS 用 `scutil --proxy`，Windows 读 WinINET 注册表，GNOME 桌面用 `gsettings`），把它作为 `http_proxy` / `https_proxy` / `all_proxy` / `no_proxy` 交给内置 Harness 进程；桌面自身的更新检查和 DeepSeek 用量查询走同一条链路。
- 关闭时不读取系统代理；但你自己导出过的变量仍然生效，`export http_proxy=...` 对同名协议始终优先。
- `127.0.0.1`、`localhost`、`::1` 永远直连，避免内置服务被绕进代理导致启动失败。
- macOS 的 PAC（自动代理配置）不会被解析：执行 PAC 脚本等于运行第三方代码，且结果会与本机其他客户端不一致。
- 设置保存在桌面数据目录的 `settings.json`（macOS 为 `~/Library/Application Support/DeepSeekHarnessDesktop/settings.json`），与共享 Harness 数据目录平级，不写进 Harness 自己的配置。
- 子进程在启动时继承环境，所以开关对「Harness 自身的模型请求」在重新打开应用后完全生效；桌面内的更新与用量请求立即生效。

需要时仍可显式导出：

```sh
export https_proxy=http://127.0.0.1:7897
export http_proxy=http://127.0.0.1:7897
export all_proxy=socks5://127.0.0.1:7897
```

## DeepSeek 用量与额度

齿轮 →「DeepSeek 官方用量与额度」显示当前 DeepSeek 官方配置的账户情况，口径对齐官方 platform.deepseek.com/usage：

- **额度**：官方公开接口 `GET /user/balance`，复用 Harness 里配置的 `DEEPSEEK_API_KEY`，展示多币种的总余额 / 充值余额 / 赠送余额与可用状态。
- **用量**：本月 token 合计、请求次数、消费，今日 token 与消费，缓存命中 / 未命中 / 输出 token 构成，以及分模型拆分。

用量接口（`platform.deepseek.com/api/v0/usage/amount`、`usage/cost`）是平台自己在用的登录态接口，不是公开 API，因此需要额外提供 `DEEPSEEK_USER_TOKEN`：登录 platform.deepseek.com 后，在浏览器控制台执行 `localStorage.getItem("userToken")`，把结果写进 `<DSH_HOME>/.credentials.yaml`：

```yaml
version: 1
refs:
  DEEPSEEK_USER_TOKEN: <粘贴的 userToken>
```

没有该 token 时面板只显示余额并说明原因。凭据解析顺序与 Harness 一致：进程环境变量 > `<DSH_HOME>/.credentials.yaml` > `<当前目录>/.env` > `<DSH_HOME>/.env`；API Key 只用于这一次发往 `api.deepseek.com` 的查询，不写日志。

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

桌面 WebView 与普通浏览器的 `localStorage` 相互独立，因此桌面版首次打开时会自动选择共享数据中最近使用的有效会话。实时工作区与会话事件通过仅监听 `127.0.0.1` 的本地 WebSocket 桥转发，并在桥内恢复 Harness 要求的同源 Host、Origin 与 Fetch Metadata 校验。

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

### 自动构建与发布

- 每次推送到 `main`：自动构建 macOS、Windows、Linux，并在三个任务全部成功后更新 `continuous` 预发布；连续推送会取消同分支的旧构建。
- 推送 `v*` 标签：自动构建三平台并创建或更新对应的正式 GitHub Release。
- 手动运行 workflow：只生成 Actions artifacts，适合验证指定版本，不会覆盖公开 Release。
- Windows 构建强制要求 NSIS 安装器存在；若 `makensis.exe` 不可用，任务会失败并阻止发版。

### 自动更新

应用启动后会自动查询本仓库的 GitHub Releases，并每 6 小时复查一次：

- `continuous` 构建跟随 `continuous` 预发布，通过构建提交判断是否有新版本。
- `v*` 正式版本只跟随 GitHub 标记的 latest 正式 Release，并按版本号比较。
- 更新包按当前平台选择 macOS DMG、Windows NSIS 安装器或 Linux tar.gz。
- 下载完成前必须通过 GitHub Release 资产提供的 SHA-256 摘要校验，并限制为本仓库对应 Release 的 HTTPS 下载地址。
- 发现更新后由用户确认下载；Windows 会打开安装器并退出当前应用，macOS 会打开 DMG，Linux 会打开下载的归档包。

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

### 适配验收

`make sync-accept` 只记录指纹，不证明桌面壳仍然可用，所以接受之前先跑一次真实启动验收：

```sh
make smoke
```

它用 `runtime/current` 里的真实上游版本启动 `dsh web`，然后走桌面壳自己的代码路径做断言：

- 解析 `dsh web: <url>?token=…` 交接行（`authenticatedHarnessURL`）；
- 用 token 换 `dsh-auth-*` 会话 Cookie（`exchangeHarnessBrowserSession`）；
- 经同源反向代理取 `/`，确认上游文档仍以 `</body>` 收尾且四段注入（会话恢复、`__DSH_TRANSPORT__`、更新面板、桌面设置面板）都落了进去；
- 经代理调 `/api/session/list`，并额外用**分块传输**再调一次——上游从 0.1.5 起为原子上传新增了流式请求体（`POST /api/session/uploadFileBinary`），反代必须能转发没有 `Content-Length` 的请求体。

没有准备运行时时该测试自动跳过；它带 `smoke` 构建标签，所以 `go test ./...` 根本不会编译它，必须显式 `make smoke` 才会跑。它用 `runtime/current/smoke-home` 作为独立的 Harness 数据目录，首次运行时从共享目录复制一份 profile 做种子（profile 的 `node_modules` 是一堆指向运行时包的软链，复制很便宜）——这样既不改动真实数据，也不用每次从网络装一遍插件。

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

`prepare-runtime` 解包 Node 发行包时会保留符号链接和文件权限位。Node 的 `bin/npm`、`bin/npx` 是指向 `lib/node_modules` 的软链；如果把它们摊平成空文件，运行时里就会留下两个「执行成功但什么都不做」的假 `npm`——这正是打包产物坏掉时最难查的那种症状。

## Python 边界

当前固定的 DeepSeek Harness 上游 commit 中，生产运行目录 `apps/`、`packages/`、`native/` 没有 Python 业务文件。上游 Python 文件只用于发布或校验脚本，因此桌面运行时无需 Python，也不存在功能降级。此项目自己的上游同步、下载和解包逻辑均使用 Go 实现。

## 许可证与声明

本包装项目采用 MIT License。DeepSeek Harness 及其第三方依赖遵循各自许可证。
本项目是社区包装层，不代表 DeepSeek AI 官方桌面客户端。
