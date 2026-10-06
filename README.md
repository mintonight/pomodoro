<p align="center">
  <img src="./assets/readme/hero.svg" width="100%" alt="Pomodoro：一个用 MyGo 写的原生桌面番茄钟，几 MB 的 Go 可执行文件，界面由 GPU 直接绘制；右侧是计时界面，圆环显示 18:00 已完成 28%">
</p>

<p align="center">
  <a href="#下载安装">下载安装</a> ·
  <a href="#功能">功能</a> ·
  <a href="#计时怎么算的">计时原理</a> ·
  <a href="#开发">开发</a>
</p>

## 为什么是原生 UI

Pomodoro 用 [MyGo](https://mygo.egoist.dev/) 的 native UI 直接在 GPU 上绘制界面——
没有网页、没有 webview，整个应用是一个几 MB 的 Go 可执行文件，启动即用。

<table>
<tr>
<td width="50%" align="center"><img src="./assets/readme/screens.png" width="100%" alt="真实界面截图：左侧是空闲状态 25:00，右侧是专注中 18:00 已完成 28%，壁纸透过玻璃材质成为背景"></td>
</tr>
</table>

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

需要 Go 1.27 或更新版本。
Linux 上的托盘图标需要 `libayatana-appindicator3`。

```sh
go test ./...          # 不需要桌面会话
go tool mygo dev       # 开发模式，改动自动重载
go tool mygo build     # 打包成可安装的应用
```

