# Pomodoro

一个用 [MyGo](https://mygo.egoist.dev/) 写的最小桌面番茄钟。界面由 MyGo 自己用
GPU 绘制（native UI），没有网页、没有 webview，整个应用是一个几 MB 的 Go 可执行文件。

## 下载安装

从 [Releases](https://github.com/mintonight/pomodoro/releases/latest) 下载对应平台的文件：

| 平台 | 文件 |
|---|---|
| **Linux** | `install.sh` 一行装好：`curl -fsSL https://github.com/mintonight/pomodoro/releases/latest/download/install.sh \| sh` |
| | 或下 `.deb`：`sudo apt install ./pomodoro_*_amd64.deb` |
| | 或下 `.tar.gz` 解压后直接运行里面的 `pomodoro` |
| **Windows** | `pomodoro.Setup.<版本>-windows-amd64.exe`（或 `arm64`），双击安装 |
| **macOS** | `pomodoro.<版本>-darwin-universal.dmg`，打开后把应用拖进 Applications |

Linux 上的托盘图标需要 `libayatana-appindicator3`；`.deb` 已经把它写进依赖，
`install.sh` 没有 root 权限所以不会自动装。

> 这些包都没有代码签名：macOS 的 Gatekeeper 和 Windows 的 SmartScreen 会警告，
> 首次打开需要在系统设置里放行。自己编译则不会有这个问题。

## 功能

- **番茄计时**：默认专注 25 分钟、休息 5 分钟，都可以自定义。
- **暂停 / 继续**：暂停时间不计入专注时间；暂停超过 30 分钟自动丢弃当前番茄，
  不记录、不通知。
- **提前结束**：专注或休息都可以随时结束，提前结束的专注不进统计。
- **完成专注后自动进入休息**，休息结束后回到空闲，等用户手动开始下一轮。
- **系统托盘**：关闭窗口只是隐藏，计时继续在托盘里跑；托盘提示显示
  `专注中 · 18:42` 这样的状态。只有托盘的「退出」才真正退出程序。
- **统计热力图**：按天显示专注时长，颜色档位固定
  （0 / 1–30 / 31–60 / 61–120 / 120+ 分钟），不受当月最高值影响。
- **本地存储**：用 SQLite 保存每个完整完成的番茄。
- **JSON 导出 / 导入**：导入会直接覆盖本机数据，不做合并。
- **主题**：跟随系统 / 浅色 / 深色。
- **开机自启动**：默认关闭。

## 计时怎么算的

计时器只相信真实时间，不靠 `remainingSeconds--`：

- 记录 `startTime` / `endTime`，界面显示 `remaining = endTime - now`；
- 界面每秒重画一次，只负责显示，不负责推进时间；
- 电脑睡眠期间时间照常流逝：醒来时如果专注（甚至接着的休息）早已结束，
  一次性推进到正确状态，不会补发多条通知；
- 暂停超过 30 分钟会自动取消当前番茄（按现实时间判断）。

这些规则都在 `timer.go` 里，并且有单元测试覆盖（包括跨零点归属、休眠追赶、
暂停超时取消等）。

## 目录结构

```
pomodoro/
├── main.go              启动、打开数据库、挂上托盘
├── app.go               应用状态：计时、设置、窗口、托盘、通知、导入导出
├── ui.go                三个页面的界面：计时 / 统计 / 设置
├── timer.go             计时状态机（纯逻辑，不依赖 UI）
├── timer_test.go        计时逻辑的测试
├── app_test.go          应用逻辑与页面的测试
├── render_test.go       把每一页画到内存里，检查不空白（无桌面会话也能跑）
├── storage/              SQLite：记录、设置、导出、导入
└── resources/
    ├── icon.png          应用图标
    └── tray.png          托盘图标
```

## 开发

需要 Go 1.27 或更新版本。原生 UI 不需要 Bun，也不需要 WebKitGTK；
Linux 上的托盘图标需要 `libayatana-appindicator3`。

```sh
go test ./...          # 不需要桌面会话
go tool mygo dev       # 开发模式，改动自动重载
go tool mygo build     # 打包成可安装的应用
```

界面测试不需要窗口：`ui.NewTester` 与 `ui.Render` 会在内存里渲染每一帧，
所以 `go test ./...` 在没有图形环境的机器上也能通过。
`UPDATE_SHOTS=1 go test -run TestRenderPages .` 会把每一页的截图写到 `/tmp/shot-*.png`。

## 发布

两个 GitHub Actions 工作流：

- **CI**（`.github/workflows/ci.yml`）：每次 push 和 PR 跑测试（`-race`）、`go vet`、
  `gofmt` 检查，并交叉编译六个目标（linux / windows / darwin × amd64 / arm64）验证能编过。
- **Release**（`.github/workflows/release.yml`）：推一个 `v*` 的 tag 触发。先校验 tag 和
  `mygo.json` 里的 `version` 一致，建一个 draft release，然后三个 runner 并行构建：
  macOS 出 `.dmg`，Linux 出 `.deb` / `.tar.gz` / `install.sh`，Windows 出安装器
  （在 Linux runner 上用 NSIS 打）。最后把 draft 转正发布。

因为 `mygo build` 给两个架构的 Windows 安装器取的是同一个名字，磁盘镜像也只按应用名命名，
工作流会把产物收进一个扁平目录，给需要的文件加上平台后缀，避免同名资产互相覆盖。

发新版本：

```sh
# 改 mygo.json 里的 version，提交，然后
# 注意：tag 必须和 version 完全一致，否则 Release 会在构建前就失败
git tag -a v0.2.0 -m "pomodoro v0.2.0"
git push origin v0.2.0
```
