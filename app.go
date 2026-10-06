package main

import (
	_ "embed"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"pomodoro/storage"
)

// trayIconPNG is the tray icon: a 64x64 PNG, which the desktop scales.
//
//go:embed resources/tray.png
var trayIconPNG []byte

// App is the whole application: the timer, the settings, the window and the
// tray. The timer and the settings are guarded by mu, as the clock runs on a
// goroutine of its own; the window and the page shown are touched on the main
// thread only, where the view runs.
type App struct {
	mu       sync.Mutex
	engine   Engine
	settings storage.Settings
	store    *storage.Storage
	now      func() time.Time

	// the stats cache, which the view reads
	stats      map[string]storage.DayStat
	statsDirty bool

	// The page shown, the day the detail dialog shows, and the month the
	// heatmap shows. Main thread only.
	page      int
	dayOpen   bool
	dayDate   string
	statYear  int
	statMonth time.Month

	// The settings page's controls, which hold what the user typed until it
	// is stored. Syncing them out of the settings every frame would fight
	// with the typing.
	focusField  float64
	breakField  float64
	themeField  string
	launchField bool

	// A message the view shows as a toast once, set from a goroutine.
	pending string

	// The window, the tray and whether the desktop APIs are available. In
	// tests they are not: the app never touches mygo.App, so the timer can
	// be exercised without a desktop session.
	win     *mygo.Window
	tray    *mygo.Tray
	desktop bool

	quitting    atomic.Bool
	lastTrayTip string

	// quit tells the clock goroutine to stop, and clockDone is closed when
	// it has: the database is only closed after that.
	quit      chan struct{}
	clockDone chan struct{}
}

// The pages of the app.
const (
	pageTimer = iota
	pageStats
	pageSettings
)

func newApp(store *storage.Storage, settings storage.Settings) *App {
	app := &App{
		store:      store,
		settings:   settings,
		now:        time.Now,
		statsDirty: true,
		page:       pageTimer,
		quit:       make(chan struct{}),
		clockDone:  make(chan struct{}),
	}
	app.syncSettingFields(settings)
	return app
}

// syncSettingFields copies the settings into the controls, which is what an
// import does: the stored values replace what was typed.
func (a *App) syncSettingFields(v storage.Settings) {
	a.focusField = v.FocusMinutes
	a.breakField = v.BreakMinutes
	a.themeField = themeLabel(v.Theme)
	a.launchField = v.LaunchAtStartup
}

// #region the timer

// snapshot reports where the timer is now. While idle it shows the focus
// length the next round would use.
func (a *App) snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked()
}

func (a *App) snapshotLocked() Snapshot {
	snap := a.engine.Snapshot(a.now())
	if snap.Phase == PhaseIdle {
		snap.Total = minutes(a.settings.FocusMinutes)
		snap.Remaining = snap.Total
	}
	return snap
}

// advance moves the timer to now and carries out what happened on the way.
// The clock goroutine and every frame call it, so the app notices a finished
// focus even while its window is hidden or the computer was asleep.
func (a *App) advance() {
	now := a.now()

	a.mu.Lock()
	events := a.engine.Advance(now)
	snap := a.snapshotLocked()
	a.mu.Unlock()

	for _, ev := range events {
		a.handle(ev)
	}
	a.updateTray(snap)
}

// handle carries out one transition the clock brought about.
func (a *App) handle(ev Event) {
	switch ev.Kind {
	case EventFocusCompleted:
		if err := a.store.AddSession(ev.StartedAt, ev.CompletedAt, ev.Focused.Minutes()); err != nil {
			log.Println("pomodoro: recording the session:", err)
		}
		a.mu.Lock()
		a.statsDirty = true
		a.mu.Unlock()
		a.notify("专注完成", "该休息一下了。")

	case EventBreakCompleted:
		a.notify("休息结束", "可以开始下一轮了。")

	case EventFocusCancelled:
		// A focus paused for longer than the limit is dropped without a
		// word: it is not recorded and no notification is shown.
	}
}

// StartFocus begins a focus. The periods come from the settings, so a change
// made earlier takes effect now and never disturbs a running round.
func (a *App) startFocus() {
	a.mu.Lock()
	a.engine.StartFocus(a.now(), minutes(a.settings.FocusMinutes), minutes(a.settings.BreakMinutes))
	a.mu.Unlock()
	a.refresh()
}

// Pause pauses a running focus.
func (a *App) pause() {
	a.mu.Lock()
	a.engine.Pause(a.now())
	a.mu.Unlock()
	a.refresh()
}

// resume continues a paused focus.
func (a *App) resume() {
	a.mu.Lock()
	a.engine.Resume(a.now())
	a.mu.Unlock()
	a.refresh()
}

// endFocus gives up the current focus without recording it.
func (a *App) endFocus() {
	a.mu.Lock()
	a.engine.EndFocus()
	a.mu.Unlock()
	a.refresh()
}

// endBreak gives up the current break.
func (a *App) endBreak() {
	a.mu.Lock()
	a.engine.EndBreak()
	a.mu.Unlock()
	a.refresh()
}

// refresh redraws the window and updates the tray, after the state changed.
func (a *App) refresh() {
	a.updateTray(a.snapshot())
	if a.win != nil {
		a.win.Invalidate()
	}
}

// clockLoop keeps the timer moving while the window is hidden. The window's
// own frames do the same while it is visible.
func (a *App) clockLoop() {
	defer close(a.clockDone)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			a.advance()
		}
	}
}

// #endregion

// #region the settings

func (a *App) focusMinutes() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.FocusMinutes
}

func (a *App) breakMinutes() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.BreakMinutes
}

func (a *App) themeSetting() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.Theme
}

func (a *App) launchSetting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.LaunchAtStartup
}

// setFocusMinutes stores the focus length. A running round keeps its own
// length; the next one uses this.
func (a *App) setFocusMinutes(v float64) {
	a.updateSettings(func(s *storage.Settings) { s.FocusMinutes = v })
}

func (a *App) setBreakMinutes(v float64) {
	a.updateSettings(func(s *storage.Settings) { s.BreakMinutes = v })
}

func (a *App) setTheme(v string) {
	a.updateSettings(func(s *storage.Settings) { s.Theme = v })
	a.applyTheme()
}

func (a *App) setLaunch(v bool) {
	if a.desktop {
		if err := mygo.App.SetOpenAtLogin(v); err != nil {
			a.setMessage("无法修改开机启动：" + err.Error())
			return
		}
	}
	a.updateSettings(func(s *storage.Settings) { s.LaunchAtStartup = v })
}

// updateSettings changes the settings and saves them.
func (a *App) updateSettings(change func(*storage.Settings)) {
	a.mu.Lock()
	change(&a.settings)
	saved := a.settings
	a.mu.Unlock()
	if err := a.store.SaveSettings(saved); err != nil {
		log.Println("pomodoro: saving the settings:", err)
	}
}

// applyTheme tells the system which appearance the app uses.
func (a *App) applyTheme() {
	if !a.desktop {
		return
	}
	switch a.themeSetting() {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// #endregion

// #region the stats

// dayStats returns the total focus of every day, loading it from the database
// when it changed.
func (a *App) dayStats() map[string]storage.DayStat {
	a.mu.Lock()
	if !a.statsDirty && a.stats != nil {
		defer a.mu.Unlock()
		return a.stats
	}
	a.mu.Unlock()

	stats, err := a.store.DayStats()
	if err != nil {
		log.Println("pomodoro: reading the stats:", err)
		stats = map[string]storage.DayStat{}
	}
	a.mu.Lock()
	a.stats, a.statsDirty = stats, false
	a.mu.Unlock()
	return stats
}

// totalSessions is how many pomodoros have been recorded.
func (a *App) totalSessions() int {
	total := 0
	for _, stat := range a.dayStats() {
		total += stat.Count
	}
	return total
}

// shownMonth is the month the heatmap shows, which starts as this one.
func (a *App) shownMonth() (int, time.Month) {
	if a.statMonth == 0 {
		now := a.now()
		a.statYear, a.statMonth = now.Year(), now.Month()
	}
	return a.statYear, a.statMonth
}

// shiftMonth moves the heatmap to another month.
func (a *App) shiftMonth(delta int) {
	year, month := a.shownMonth()
	at := time.Date(year, month, 1, 0, 0, 0, 0, time.Local).AddDate(0, delta, 0)
	a.statYear, a.statMonth = at.Year(), at.Month()
}

// #endregion

// stop ends the clock goroutine and waits for the last tick, so the database
// is closed after it.
func (a *App) stop() {
	close(a.quit)
	<-a.clockDone
}

// #region the desktop

// start makes the window and the tray, once the application is ready.
func (a *App) start() {
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "Pomodoro",
		Width:     420,
		Height:    600,
		MinWidth:  380,
		MinHeight: 520,
		StateKey:  "main",
		Content:   ui.View(a.view),
	})
	// Closing the window hides it: the timer keeps running in the tray,
	// and the app only quits from the tray's 退出.
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting.Load() {
			return
		}
		e.PreventDefault()
		a.win.Hide()
	})

	a.setupTray()

	// The app lives in the tray: it never quits by itself when its window
	// goes away. Registering a listener is what stops MyGo from quitting
	// then; the tray's 退出 is the way out.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting.Store(true) })
	mygo.Power.OnResume(func() { a.advance() })

	a.applyTheme()
	go a.clockLoop()
}

// setupTray puts the icon in the menu bar or the notification area. Without
// the library a Linux desktop needs, it reports the problem and goes on: the
// app still works, without a tray.
func (a *App) setupTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:    trayIconPNG,
		ToolTip: "Pomodoro",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "打开", Click: func(*mygo.MenuItem, *mygo.Window) { a.showWindow() }},
			mygo.Separator(),
			{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}),
	})
	if err != nil {
		log.Println("pomodoro: no tray icon:", err)
		return
	}
	a.tray = tray
	a.updateTray(a.snapshot())
}

// showWindow brings the window back from the tray.
func (a *App) showWindow() {
	if a.win == nil {
		return
	}
	a.win.Show()
	a.win.Focus()
}

// updateTray writes the state and the time left into the tray's tooltip, as
// often as it changes.
func (a *App) updateTray(snap Snapshot) {
	if a.tray == nil {
		return
	}
	tip := trayTip(snap)
	if tip == a.lastTrayTip {
		return
	}
	a.lastTrayTip = tip
	a.tray.SetToolTip(tip)
	// On macOS this shows the text next to the icon; elsewhere it does
	// nothing.
	a.tray.SetTitle(trayTitle(snap))
}

// notify shows a desktop notification with the system's alert sound.
func (a *App) notify(title, body string) {
	if !a.desktop {
		return
	}
	mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show()
	mygo.Shell.Beep()
}

// setMessage asks the view to show a toast on its next frame.
func (a *App) setMessage(msg string) {
	a.mu.Lock()
	a.pending = msg
	a.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// takeMessage returns the toast to show, if any.
func (a *App) takeMessage() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	msg := a.pending
	a.pending = ""
	return msg
}

// exportData asks where to save a JSON copy of everything and writes it.
func (a *App) exportData() {
	if !a.desktop {
		return
	}
	a.mu.Lock()
	settings := a.settings
	a.mu.Unlock()
	name := "pomodoro-" + a.now().Format("20060102") + ".json"

	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       "导出数据",
			DefaultPath: name,
			Filters:     []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil {
			a.setMessage("导出失败：" + err.Error())
			return
		}
		if path == "" {
			return
		}
		if err := a.store.Export(path, settings); err != nil {
			a.setMessage("导出失败：" + err.Error())
			return
		}
		a.setMessage("已导出到 " + filepath.Base(path))
	}()
}

// importData asks for a JSON export and replaces the local data with it.
func (a *App) importData() {
	if !a.desktop {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   "导入数据",
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil {
			a.setMessage("导入失败：" + err.Error())
			return
		}
		if len(paths) == 0 {
			return
		}
		imported, sessions, err := a.store.Import(paths[0])
		if err != nil {
			a.setMessage("导入失败：" + err.Error())
			return
		}
		a.mu.Lock()
		a.settings.FocusMinutes = imported.FocusMinutes
		a.settings.BreakMinutes = imported.BreakMinutes
		saved := a.settings
		a.statsDirty = true
		a.mu.Unlock()
		a.syncSettingFields(saved)

		if err := a.store.SaveSettings(saved); err != nil {
			log.Println("pomodoro: saving the settings:", err)
		}
		a.setMessage(fmt.Sprintf("导入完成，已覆盖本地数据（%d 个番茄）", len(sessions)))
		a.refresh()
	}()
}

// #endregion

// #region helpers

// minutes turns a number of minutes into a duration.
func minutes(m float64) time.Duration {
	return time.Duration(m * float64(time.Minute))
}

// trayTip is the tray's hover text, as the PRD asks: the state and the time
// left, as "专注中 · 18:42".
func trayTip(s Snapshot) string {
	switch s.Phase {
	case PhaseFocus:
		return "专注中 · " + clockShort(s.Remaining)
	case PhasePaused:
		return "已暂停 · " + clockShort(s.Remaining)
	case PhaseBreak:
		return "休息中 · " + clockShort(s.Remaining)
	default:
		return "Pomodoro · 空闲"
	}
}

// trayTitle is the short text beside the icon on macOS.
func trayTitle(s Snapshot) string {
	switch s.Phase {
	case PhaseFocus, PhasePaused, PhaseBreak:
		return " " + clockShort(s.Remaining)
	default:
		return ""
	}
}

// #endregion
