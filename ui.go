package main

import (
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"pomodoro/storage"
)

// view builds the window's interface from the app's state. It runs on the
// main thread whenever the window needs a frame.
func (a *App) view(c *ui.Context) {
	a.advance()

	// Show a message left by a dialog or an import.
	if msg := a.takeMessage(); msg != "" {
		c.Toast(msg)
	}

	snap := a.snapshot()
	// While something is running, ask for another frame soon so the
	// countdown moves. The clock goroutine covers a hidden window.
	if snap.Phase != PhaseIdle {
		c.After(200 * time.Millisecond)
	}

	t := c.Theme()
	// The theme's own Surface is a gray tuned for solid windows; over the
	// frosted backdrop the panels read as dirty. In the light appearance
	// the controls show pure white instead.
	if !t.Dark {
		t.Surface = ui.Hex("#ffffff")
	}
	ui.Column(c).Fill().Background(t.Background).Draw(func(p *ui.Painter, r ui.Rect) {
		a.mu.Lock()
		custom, path := a.settings.CustomWallpaper, a.settings.WallpaperPath
		a.mu.Unlock()
		paintWindowBackground(p, r, t.Dark, custom, path)
	}).Children(func() {
		a.nav(c)
		switch a.page {
		case pageStats:
			a.statsPage(c)
		case pageSettings:
			a.settingsPage(c)
		default:
			a.timerPage(c, snap)
		}
	})
}

// nav is the row of tabs at the top.
func (a *App) nav(c *ui.Context) {
	t := c.Theme()
	zone := ui.Row(c).Padding(16, 16, 8, 16).Justify(ui.Center)
	keyboardFocus := false
	zone.Children(func() {
		parts := ui.SegmentedBase(c, &a.page, 3)
		track := parts.Track.Label("页面导航").Width(288).Height(42).
			Padding(4).Gap(0).Radius(12).Material(glass.Glass{})
		position := track.Animate("selection", float32(a.page), 240*time.Millisecond)
		track.Draw(func(p *ui.Painter, r ui.Rect) {
			width := (r.W - 8) / 3
			selected := ui.Rect{X: r.X + 4 + position*width, Y: r.Y + 4, W: width, H: r.H - 8}
			glass.Paint(p, selected, 8, glass.Glass{})
		})
		track.Children(func() {
			for i, label := range []string{"计时", "统计", "设置"} {
				segment := parts.Segment(i).Grow(1).Width(0).Height(34).Radius(8)
				keyboardFocus = keyboardFocus || segment.FocusVisible()
				color := t.TextMuted
				if i == a.page {
					color = t.Text
				}
				segment.Children(func() { ui.Text(c, label).FontSize(14).TextColor(color).SingleLine() })
			}
		})
	})
	revealControls(zone, keyboardFocus)
}

// revealControls keeps the hit area and layout stable even while transparent.
// Only keyboard-origin focus reveals it: mouse clicks must not pin it open.
func revealControls(zone *ui.Element, keyboardFocus bool) {
	_, _, over := zone.PointerPosition()
	target := float32(0)
	if over || keyboardFocus {
		target = 1
	}
	zone.Opacity(zone.Animate("reveal", target, 180*time.Millisecond))
}

// #region the timer page

func (a *App) timerPage(c *ui.Context, snap Snapshot) {
	t := c.Theme()
	page := ui.Column(c).Key("timer-page").Fill().Center().Gap(24).Padding(24)
	// A finite first-entry reveal; subsequent frames and timer actions retain it.
	if a.timerEntered.IsZero() {
		a.timerEntered = c.Now()
	}
	entry := float32(c.Now().Sub(a.timerEntered)) / float32(320*time.Millisecond)
	if entry < 1 && !c.Preferences().ReduceMotion {
		page.Opacity(ui.EaseOut(max(0, entry))).Top(8 * (1 - ui.EaseOut(max(0, entry))))
		c.AnimationFrame()
	}
	page.Children(func() {
		ui.Box(c).Height(24).Width(248).Center().Children(func() {
			ui.Text(c, phaseLabel(snap.Phase)).Key(snap.Phase).FontSize(14).FontWeight(600).
				TextColor(t.TextMuted).Transition(ui.ElementTransition{
				Duration: 180 * time.Millisecond, Enter: &ui.Motion{Y: 4},
			})
		})
		a.ring(c, snap)
		a.controls(c, snap)
	})
}

// ring draws the countdown inside a progress ring.
func (a *App) ring(c *ui.Context, snap Snapshot) {
	t := c.Theme()
	color, track := ringColors(snap.Phase, t)

	ring := ui.Box(c).Key("countdown").Label("倒计时圆环").Size(248, 248)
	progress := ring.Animate("progress", snap.Progress(), 280*time.Millisecond)
	red := ring.Animate("red", float32(color.R), 240*time.Millisecond)
	green := ring.Animate("green", float32(color.G), 240*time.Millisecond)
	blue := ring.Animate("blue", float32(color.B), 240*time.Millisecond)
	color = ui.RGB(uint8(red), uint8(green), uint8(blue))
	ring.Children(func() {
		// The ring itself.
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			const thickness float32 = 8
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			radius := min(r.W, r.H)/2 - thickness/2 - 2

			p.StrokePath(arc(cx, cy, radius, 0, 1), thickness, track)
			if progress > 0 {
				p.StrokePath(arc(cx, cy, radius, 0, progress), thickness, color)
			}
		})

		// The time in the middle. It is a real text element, so screen
		// readers can read it and tests can find it, laid over the ring.
		ui.Column(c).Attach(ui.AnchorCenter, ui.AnchorCenter).Width(224).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, clockLabel(snap)).Font("monospace").FontSize(44).
				FontWeight(600).SingleLine().TextAlign(ui.Center)
			// Reserve the same line in every phase so the digits never jump.
			ui.Box(c).Width(224).Height(22).Center().Children(func() {
				label := "分钟 · 专注时间"
				switch snap.Phase {
				case PhasePaused:
					label = "已暂停 " + shortLabel(snap.PausedFor)
				case PhaseFocus, PhaseBreak:
					label = fmt.Sprintf("已完成 %d%%", int(snap.Progress()*100+0.5))
				}
				ui.Text(c, label).Key(snap.Phase).FontSize(13).TextColor(t.TextMuted).
					TextAlign(ui.Center).Transition(ui.ElementTransition{
					Duration: 200 * time.Millisecond, Enter: &ui.Motion{Y: 5},
				})
			})
		})
	})
}

// controls builds the buttons of the current phase.
func (a *App) controls(c *ui.Context, snap Snapshot) {
	zone := ui.Row(c).Key("timer-actions").Width(248).Height(44).Gap(12).Justify(ui.Center)
	keyboardFocus := false
	zone.Children(func() {
		label, width := "开始", float32(160)
		if snap.Phase == PhaseFocus {
			label, width = "暂停", 118
		} else if snap.Phase == PhasePaused {
			label, width = "继续", 118
		} else if snap.Phase == PhaseBreak {
			label = "结束"
		}
		primary := ui.PrimaryButton(c, label).Width(width).Height(44).Radius(12).
			Transition(ui.ElementTransition{Duration: 240 * time.Millisecond})
		keyboardFocus = primary.FocusVisible()
		buttonFeedback(primary)
		if primary.Clicked() {
			switch snap.Phase {
			case PhaseFocus:
				a.pause()
			case PhasePaused:
				a.resume()
			case PhaseBreak:
				a.endBreak()
			default:
				a.startFocus()
			}
		}
		if snap.Phase == PhaseFocus || snap.Phase == PhasePaused {
			end := ui.Button(c, "结束").Width(118).Height(44).Radius(12).
				Transition(ui.ElementTransition{Duration: 180 * time.Millisecond,
					Enter: &ui.Motion{X: -6}, Exit: &ui.Motion{Y: 6}})
			keyboardFocus = keyboardFocus || end.FocusVisible()
			buttonFeedback(end)
			if end.Clicked() {
				a.endFocus()
			}
		}
	})
	revealControls(zone, keyboardFocus)
}

// Move the painted button without changing its slot or postponing the action.
func buttonFeedback(button *ui.Element) {
	target := float32(0)
	if button.Pressed() {
		target = 2
	}
	button.Top(button.Animate("press", target, 90*time.Millisecond))
}

// phaseLabel names the phase above the ring.
func phaseLabel(p Phase) string {
	switch p {
	case PhaseFocus:
		return "专注"
	case PhasePaused:
		return "已暂停"
	case PhaseBreak:
		return "休息"
	default:
		return "准备开始"
	}
}

// ringColors picks the ring's color and its track for a phase.
func ringColors(p Phase, t *ui.Theme) (color, track ui.Color) {
	switch p {
	case PhaseFocus:
		return t.Accent, t.Border.Alpha(0.6)
	case PhasePaused:
		return t.Warning, t.Border.Alpha(0.6)
	case PhaseBreak:
		return t.Success, t.Border.Alpha(0.6)
	default:
		return t.Accent, t.Border.Alpha(0.6)
	}
}

// arc returns a circular arc from start to end, as a share of a whole turn,
// beginning at the top and going clockwise.
func arc(cx, cy, radius, from, to float32) *ui.Path {
	path := &ui.Path{}
	const steps = 128
	for i := 0; i <= steps; i++ {
		f := from + (to-from)*float32(i)/float32(steps)
		angle := -math.Pi/2 + 2*math.Pi*float64(f)
		x := cx + radius*float32(math.Cos(angle))
		y := cy + radius*float32(math.Sin(angle))
		if i == 0 {
			path.MoveTo(x, y)
		} else {
			path.LineTo(x, y)
		}
	}
	return path
}

// #endregion

// #region the stats page

func (a *App) statsPage(c *ui.Context) {
	t := c.Theme()
	year, month := a.shownMonth()
	stats := a.dayStats()

	ui.Scroll(c).Grow(1).Padding(20).Gap(20).Children(func() {
		// Which month the heatmap shows.
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			prevBtn := ui.ButtonBase(c).Label("上个月").Tooltip("上个月").Size(32, 32).Radius(16).
				Border(1, t.Border.Alpha(0.6)).
				Material(glass.Glass{Interactive: true}).Children(func() {
				ui.Icon(c, chevronLeft).Size(16, 16).TextColor(t.Text)
			})
			if prevBtn.Clicked() {
				a.shiftMonth(-1)
			}
			ui.Textf(c, "%d 年 %d 月", year, month).FontSize(16).Bold().Grow(1).TextAlign(ui.Center)
			nextBtn := ui.ButtonBase(c).Label("下个月").Tooltip("下个月").Size(32, 32).Radius(16).
				Border(1, t.Border.Alpha(0.6)).
				Material(glass.Glass{Interactive: true}).Children(func() {
				ui.Icon(c, chevronRight).Size(16, 16).TextColor(t.Text)
			})
			if nextBtn.Clicked() {
				a.shiftMonth(1)
			}
		})

		a.monthTotals(c, stats, year, month)
		ui.Divider(c)
		a.heatmap(c, stats, year, month)
		ui.Divider(c)
		a.allTime(c, stats)
	})

	a.dayDetail(c, stats)
}

// monthTotals shows the month's focus time and pomodoro count.
func (a *App) monthTotals(c *ui.Context, stats map[string]storage.DayStat, year int, month time.Month) {
	t := c.Theme()
	var totalMinutes float64
	count := 0
	for date, stat := range stats {
		at, err := time.Parse("2006-01-02", date)
		if err != nil || at.Year() != year || at.Month() != month {
			continue
		}
		totalMinutes += stat.Minutes
		count += stat.Count
	}

	card := func(label, value string) {
		ui.Column(c).Grow(1).Padding(14).Gap(4).Radius(t.Radius+2).
			Background(t.Surface).Border(1, t.Border.Alpha(0.7)).Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, value).FontSize(22).Bold()
		})
	}
	ui.Row(c).Gap(12).Children(func() {
		card("本月总专注", formatMinutes(totalMinutes))
		card("本月番茄", fmt.Sprintf("%d 个", count))
	})
}

// heatmap draws the month as a calendar colored by the focus time of each
// day, in fixed buckets that do not depend on the month's own figures.
func (a *App) heatmap(c *ui.Context, stats map[string]storage.DayStat, year int, month time.Month) {
	t := c.Theme()

	first := time.Date(year, month, 1, 0, 0, 0, 0, time.Local)
	days := time.Date(year, month+1, 0, 0, 0, 0, 0, time.Local).Day()
	// Weeks start on Monday.
	offset := (int(first.Weekday()) + 6) % 7
	rows := (offset + days + 6) / 7
	today := a.now().Format("2006-01-02")

	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(6).Justify(ui.SpaceBetween).Children(func() {
			for _, name := range []string{"一", "二", "三", "四", "五", "六", "日"} {
				ui.Text(c, name).FontSize(11).TextColor(t.TextMuted).WidthPercent(100.0 / 7).TextAlign(ui.Center)
			}
		})
		cell := 0
		ui.Grid(c).Columns(7).GapX(6).GapY(6).Children(func() {
			for i := 0; i < rows*7; i++ {
				dayOfMonth := i - offset + 1
				if dayOfMonth < 1 || dayOfMonth > days {
					ui.Box(c).Height(30)
					continue
				}
				cell++
				date := time.Date(year, month, dayOfMonth, 0, 0, 0, 0, time.Local)
				key := date.Format("2006-01-02")
				stat := stats[key]

				box := ui.Box(c).Key(cell).Height(30).Radius(6).Center()
				box.Background(bucketColor(stat.Minutes, t))
				if stat.Minutes <= 0 && !t.Dark {
					box.Border(1, t.Border)
				}
				if stat.Minutes > 0 {
					box.TextColor(ui.RGB(255, 255, 255))
				} else {
					box.TextColor(t.TextMuted)
				}
				if key == today {
					box.Border(1.5, t.Accent)
				}
				if stat.Minutes > 0 {
					box.Tooltip(fmt.Sprintf("%s\n%d 分钟 · %d 个番茄", date.Format("1 月 2 日"), int(stat.Minutes+0.5), stat.Count))
				}
				box.Children(func() {
					ui.Textf(c, "%d", dayOfMonth).FontSize(12)
				})
				if box.Clicked() {
					a.dayOpen, a.dayDate = true, key
				}
			}
		})

		// The legend of the buckets.
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "少").FontSize(11).TextColor(t.TextMuted)
			for _, m := range []float64{0, 15, 45, 90, 150} {
				swatch := ui.Box(c).Size(14, 14).Radius(4).Background(bucketColor(m, t))
				if m <= 0 && !t.Dark {
					swatch.Border(1, t.Border)
				}
			}
			ui.Text(c, "多").FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			ui.Text(c, "0 · 1-30 · 31-60 · 61-120 · 120+ 分钟").FontSize(11).TextColor(t.TextMuted)
		})
	})
}

// allTime shows how many pomodoros have ever been recorded.
func (a *App) allTime(c *ui.Context, stats map[string]storage.DayStat) {
	t := c.Theme()
	total := 0
	var totalMinutes float64
	for _, stat := range stats {
		total += stat.Count
		totalMinutes += stat.Minutes
	}
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "全部").FontSize(12).TextColor(t.TextMuted)
		ui.RichText(c,
			ui.Span{Text: fmt.Sprintf("%d", total), Size: 20, Weight: 600},
			ui.Span{Text: " 个番茄 · ", Size: 13, Color: t.TextMuted},
			ui.Span{Text: formatMinutes(totalMinutes), Size: 20, Weight: 600},
		)
	})
}

// dayDetail shows one day's totals when its cell was clicked.
func (a *App) dayDetail(c *ui.Context, stats map[string]storage.DayStat) {
	t := c.Theme()
	ui.Modal(c, &a.dayOpen, func() {
		at, _ := time.Parse(storage.DateLayout, a.dayDate)
		stat := stats[a.dayDate]
		ui.Column(c).Width(300).Gap(14).Children(func() {
			ui.Text(c, at.Format("2006-01-02")).FontSize(16).Bold()
			ui.Column(c).Gap(6).Children(func() {
				row := func(label, value string) {
					ui.Row(c).Children(func() {
						ui.Text(c, label).TextColor(t.TextMuted).Grow(1)
						ui.Text(c, value).Bold()
					})
				}
				row("总专注", formatMinutes(stat.Minutes))
				row("完成番茄", fmt.Sprintf("%d 个", stat.Count))
			})
			ui.Row(c).Justify(ui.End).Margin(0, 20, 20, 0).Children(func() {
				if ui.Button(c, "关闭").Clicked() {
					a.dayOpen = false
				}
			})
		})
	})
}

// bucketColor colors a heatmap cell by the day's focus time, in the fixed
// buckets of the PRD: 0, 1-30, 31-60, 61-120 and 120+ minutes.
func bucketColor(minutes float64, t *ui.Theme) ui.Color {
	var level0, level1, level2, level3, level4 ui.Color
	if t.Dark {
		level0, level1 = ui.Hex("#3a3a40"), ui.Hex("#2d5a3d")
		level2, level3, level4 = ui.Hex("#2f8a4e"), ui.Hex("#35a85a"), ui.Hex("#3fca6e")
	} else {
		level0, level1 = ui.Hex("#ffffff"), ui.Hex("#9be9a8")
		level2, level3, level4 = ui.Hex("#40c463"), ui.Hex("#30a14e"), ui.Hex("#216e39")
	}
	switch {
	case minutes <= 0:
		return level0
	case minutes <= 30:
		return level1
	case minutes <= 60:
		return level2
	case minutes <= 120:
		return level3
	default:
		return level4
	}
}

// #endregion

// #region the settings page

func (a *App) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Padding(20).Gap(20).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "时长").FontSize(13).Bold().TextColor(t.TextMuted)
			ui.Form(c, func() {
				ui.Field(c, "专注时长", func() {
					ui.NumberInput(c, &a.focusField, 1, 180, 5).Width(110)
				}).Description("分钟。默认 25。")
				ui.Field(c, "休息时长", func() {
					ui.NumberInput(c, &a.breakField, 1, 60, 1).Width(110)
				}).Description("分钟。默认 5。")
			})
			if a.focusField != a.focusMinutes() {
				a.setFocusMinutes(a.focusField)
			}
			if a.breakField != a.breakMinutes() {
				a.setBreakMinutes(a.breakField)
			}
		})

		ui.Divider(c)

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "外观").FontSize(13).Bold().TextColor(t.TextMuted)
			ui.Form(c, func() {
				ui.Field(c, "主题", func() {
					ui.Select(c, &a.themeField, []string{"跟随系统", "浅色", "深色"}).Width(140)
				})
			})
			if a.themeField != themeLabel(a.themeSetting()) {
				a.setTheme(themeValue(a.themeField))
			}
		})

		ui.Divider(c)

		a.backgroundSection(c)

		ui.Divider(c)

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "系统").FontSize(13).Bold().TextColor(t.TextMuted)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "开机自动启动")
					ui.Text(c, "登录后自动打开 Pomodoro").FontSize(12).TextColor(t.TextMuted)
				})
				if ui.Switch(c, &a.launchField).Label("开机自动启动").Changed() {
					a.setLaunch(a.launchField)
				}
			})
		})

		ui.Divider(c)

		a.updatesSection(c)

		ui.Divider(c)

		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, "数据").FontSize(13).Bold().TextColor(t.TextMuted)
			ui.Text(c, "导出为 JSON 备份，或从备份恢复。导入会覆盖本机上的全部统计。").FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(10).Children(func() {
				if ui.Button(c, "导出数据").Clicked() {
					a.exportData()
				}
				if ui.Button(c, "导入数据").Clicked() {
					a.importData()
				}
			})
			ui.Textf(c, "共 %d 个番茄。数据保存在本机。", a.totalSessions()).FontSize(12).TextColor(t.TextMuted)
		})
	})
}

// backgroundSection is the 背景 block of the settings page: the switch
// of the custom wallpaper, the image it shows with a way to pick another,
// and the way back to the default backdrop.
func (a *App) backgroundSection(c *ui.Context) {
	t := c.Theme()
	custom := a.wallpaperSetting()

	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, "背景").FontSize(13).Bold().TextColor(t.TextMuted)

		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "自定义壁纸")
				ui.Text(c, "用一张图片代替默认的桌面壁纸背景").FontSize(12).TextColor(t.TextMuted)
			})
			if ui.Switch(c, &a.wallpaperField).Label("自定义壁纸").Changed() {
				a.setCustomWallpaper(a.wallpaperField)
			}
		})

		if custom {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					name := a.wallpaperPathSetting()
					if name == "" {
						name = "未选择图片"
					} else {
						name = filepath.Base(name)
					}
					ui.Text(c, name).FontSize(12).TextColor(t.TextMuted)
				})
				if ui.Button(c, "选择图片").Clicked() {
					a.pickWallpaper()
				}
				if a.wallpaperPathSetting() != "" && ui.Button(c, "清除").Clicked() {
					a.clearWallpaper()
				}
			})
		}
	})
}

// updatesSection is the 更新 block of the settings page: the current
// version, the state of the updater with its buttons, and the switch of
// the automatic check.
func (a *App) updatesSection(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, "更新").FontSize(13).Bold().TextColor(t.TextMuted)

		state := a.updateStatus()

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				line := "当前版本 " + a.version()
				switch state.Status {
				case "checking":
					line = "正在检查更新…"
				case "downloading":
					line = fmt.Sprintf("正在下载更新 %s（%.0f%%）", state.Version, state.Progress*100)
				case "ready":
					line = fmt.Sprintf("已安装 %s，重启后生效", state.Version)
				case "error":
					line = state.Message
				case "idle":
					if state.New {
						line = fmt.Sprintf("发现新版本 %s", state.Version)
					}
				}
				ui.Text(c, line)
				if hint := a.updateHint(state); hint != "" {
					ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
				}
				if state.Status == "downloading" {
					ui.Meter(c, state.Progress, 0, 1, nil).Label("下载进度").Grow(1)
				}
			})
			switch state.Status {
			case "checking":
				if ui.Button(c, "取消").Clicked() {
					a.cancelUpdate()
				}
			case "downloading":
				if ui.Button(c, "取消").Clicked() {
					a.cancelUpdate()
				}
			case "ready":
				if ui.PrimaryButton(c, "重启更新").Clicked() {
					a.relaunchUpdate()
				}
			default:
				if ui.PrimaryButton(c, "检查更新").Clicked() {
					go a.checkForUpdates(true)
				}
				if state.New && ui.Button(c, "立即安装").Clicked() {
					a.installUpdate()
				}
			}
		})

		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "自动检查更新")
				ui.Text(c, "启动时联网检查新版本").FontSize(12).TextColor(t.TextMuted)
			})
			if ui.Switch(c, &a.updateField).Label("自动检查更新").Changed() {
				a.setUpdate(a.updateField)
			}
		})
	})
}

// updateHint is the second line under the state, when it has one.
func (a *App) updateHint(s UpdateState) string {
	switch s.Status {
	case "idle":
		if s.New {
			return "安装会先下载更新，完成后提示重启。"
		}
	}
	return ""
}

// themeLabel and themeValue translate between the stored value and the
// label the select shows.
func themeLabel(v string) string {
	switch v {
	case "light":
		return "浅色"
	case "dark":
		return "深色"
	default:
		return "跟随系统"
	}
}

func themeValue(label string) string {
	switch label {
	case "浅色":
		return "light"
	case "深色":
		return "dark"
	default:
		return "system"
	}
}

// #endregion

// #region formatting

// clockLabel is the countdown as mm:ss, or h:mm:ss when it is an hour or
// longer. A running period never shows a negative time.
func clockLabel(s Snapshot) string {
	d := s.Remaining
	if d < 0 {
		d = 0
	}
	// Round up, so the last second is shown as 00:01 rather than 00:00.
	d = d.Round(time.Second)
	if s.Phase == PhaseIdle {
		d = s.Total
	}
	total := int(d / time.Second)
	h, m, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

// clockShort is a duration as mm:ss, or h:mm:ss when it is an hour or
// longer, for the tray's tooltip.
func clockShort(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second) / time.Second)
	h, m, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

// shortLabel is a duration as "30 分钟" or "1 小时 5 分钟", for hints.
func shortLabel(d time.Duration) string {
	if d < time.Minute {
		return "不到 1 分钟"
	}
	mins := int(d.Round(time.Minute) / time.Minute)
	if mins < 60 {
		return fmt.Sprintf("%d 分钟", mins)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", mins/60, mins%60)
}

// formatMinutes is a number of minutes as "125 分钟" or "2 小时 5 分".
func formatMinutes(m float64) string {
	mins := int(m + 0.5)
	if mins < 60 {
		return fmt.Sprintf("%d 分钟", mins)
	}
	if mins%60 == 0 {
		return fmt.Sprintf("%d 小时", mins/60)
	}
	return fmt.Sprintf("%d 小时 %d 分", mins/60, mins%60)
}

// #endregion

// The two chevrons of the month navigation.
var (
	chevronLeft  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>`))
	chevronRight = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`))
)
