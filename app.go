package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

// appIconPNG is the application icon, shown in the title bar and on the
// taskbar.
//
//go:embed resources/icon.png
var appIconPNG []byte

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

	// First-entry motion uses the UI frame clock, never the timer's clock.
	timerEntered time.Time

	// The settings page's controls, which hold what the user typed until it
	// is stored. Syncing them out of the settings every frame would fight
	// with the typing.
	focusField     float64
	breakField     float64
	themeField     string
	launchField    bool
	updateField    bool
	wallpaperField bool

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

	// The updater and its state, for the settings page. updateMu guards the
	// state the clock and the update goroutine write; updateCheck holds the
	// check in progress. updateNow is what a running check reads, and is
	// what the tests replace: the real one wraps mygo.Updater, which the
	// tests cannot touch, as it does nothing without an update feed.
	updateMu      sync.Mutex
	updateState   UpdateState
	updateCheck   *updateCheck
	updateNow     func(ctx context.Context) (*mygo.Update, error)
	updateInstall func(ctx context.Context, progress func(done, total int64)) error
	updateLaunch  func()

	// quit tells the clock goroutine to stop, and clockDone is closed when
	// it has: the database is only closed after that.
	quit      chan struct{}
	clockDone chan struct{}
}

// UpdateState is what the settings page shows of the updater.
type UpdateState struct {
	// Status, one of "idle", "checking", "downloading", "ready", "error".
	Status string
	// New is whether a newer version was found.
	New bool
	// Version found, and the error message of a failed check or install.
	Version, Message string
	// Progress of a download, from 0 to 1.
	Progress float64
}

// updateCheck is the check or download in progress.
type updateCheck struct {
	// cancel stops a download; a bare check lets it finish.
	cancel context.CancelFunc
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
	app.updateNow = mygo.Updater.Check
	app.updateInstall = app.installPending
	app.updateLaunch = func() { mygo.App.Relaunch() }
	app.updateState = UpdateState{Status: "idle"}
	app.wallpaperField = settings.CustomWallpaper
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
	a.updateField = v.AutoUpdate
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

// version is the version this build is, as mygo.json's or the package's.
func (a *App) version() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "开发版"
}

// setUpdate stores whether the app checks for updates at startup. Turning
// it off also cancels nothing: a check in progress simply finishes.
func (a *App) setUpdate(v bool) {
	a.updateSettings(func(s *storage.Settings) { s.AutoUpdate = v })
}

// wallpaperSetting reports whether the custom wallpaper is on.
func (a *App) wallpaperSetting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.CustomWallpaper
}

// wallpaperPath returns the image the custom wallpaper shows.
func (a *App) wallpaperPathSetting() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.WallpaperPath
}

// setCustomWallpaper stores whether the custom wallpaper shows.
func (a *App) setCustomWallpaper(v bool) {
	a.wallpaperField = v
	a.updateSettings(func(s *storage.Settings) { s.CustomWallpaper = v })
	a.refresh()
}

// pickWallpaper asks for an image and makes it the custom wallpaper.
func (a *App) pickWallpaper() {
	if !a.desktop {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: a.win,
			Title:  "选择壁纸图片",
			Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{
				"png", "jpg", "jpeg", "gif", "webp", "bmp",
			}}},
		})
		if err != nil {
			a.setMessage("选择图片失败：" + err.Error())
			return
		}
		if len(paths) == 0 {
			return
		}
		// Reading it now tells the user of an unreadable image at once.
		if _, err := os.ReadFile(paths[0]); err != nil {
			a.setMessage("图片无法读取：" + err.Error())
			return
		}
		a.mu.Lock()
		a.settings.WallpaperPath = paths[0]
		a.settings.CustomWallpaper = true
		a.wallpaperField = true
		saved := a.settings
		a.mu.Unlock()
		if err := a.store.SaveSettings(saved); err != nil {
			log.Println("pomodoro: saving the settings:", err)
		}
		a.refresh()
	}()
}

// clearWallpaper forgets the custom image; the switch stays as it is, so
// what is left is the default backdrop.
func (a *App) clearWallpaper() {
	a.updateSettings(func(s *storage.Settings) { s.WallpaperPath = "" })
	a.refresh()
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

	// The title bar and the taskbar carry the app icon even when the
	// executable embeds no icon resource, as a `go run` build does.
	if err := a.win.SetIcon(appIconPNG); err != nil {
		log.Println("pomodoro: setting the window icon:", err)
	}

	a.setupTray()

	// The app lives in the tray: it never quits by itself when its window
	// goes away. Registering a listener is what stops MyGo from quitting
	// then; the tray's 退出 is the way out.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting.Store(true) })
	mygo.Power.OnResume(func() { a.advance() })

	a.applyTheme()
	go a.clockLoop()
	if a.settings.AutoUpdate {
		go a.checkForUpdates(false)
	}
}

// #region the updater

// updateStatus returns what the settings page shows of the updater.
func (a *App) updateStatus() UpdateState {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return a.updateState
}

// setUpdateState stores one change of the updater's state and redraws.
func (a *App) setUpdateState(f func(*UpdateState)) {
	a.updateMu.Lock()
	f(&a.updateState)
	a.updateMu.Unlock()
	a.invalidate()
}

// invalidate redraws the window. The tests run without one.
func (a *App) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// checkForUpdates looks for a newer version and records what it found,
// in the calling goroutine; go checkForUpdates to run it in one. When
// user is false it is the quiet check at startup: it never reports being
// up to date, and says nothing when it cannot reach the feed.
func (a *App) checkForUpdates(user bool) {
	a.updateMu.Lock()
	if a.updateCheck != nil {
		// A check is under way: the user asking again only brings the
		// window up, which the settings page already shows.
		a.updateMu.Unlock()
		return
	}
	if !user && (a.updateState.New || !a.settings.AutoUpdate) {
		// The quiet check neither repeats itself nor runs after the user
		// turned the automatic check off.
		a.updateMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	previous := a.updateState
	a.updateCheck = &updateCheck{cancel: cancel}
	a.updateState = UpdateState{Status: "checking"}
	a.updateMu.Unlock()
	a.invalidate()

	up, err := a.updateNow(ctx)

	a.updateMu.Lock()
	a.updateCheck = nil
	switch {
	case err != nil && ctx.Err() != nil:
		// The user canceled: back to where the page was.
		a.updateState = previous
	case err != nil && user:
		a.updateState = UpdateState{Status: "error", Message: updateMessage(err)}
	case err != nil, up == nil:
		// A quiet check that failed or found nothing stays silent; a
		// manual one says the app is current.
		if user {
			a.updateState = UpdateState{Status: "idle", Message: "已是最新版本"}
		} else {
			a.updateState = UpdateState{Status: "idle"}
		}
	default:
		a.updateState = UpdateState{Status: "idle", New: true, Version: up.Version}
	}
	a.updateMu.Unlock()
	cancel()
	a.invalidate()
}

// installUpdate downloads the update that was found and installs it in
// place of the running app, in a goroutine.
func (a *App) installUpdate() {
	a.updateMu.Lock()
	state := a.updateState
	chk := a.updateCheck
	a.updateMu.Unlock()
	if !state.New || chk != nil {
		return
	}

	// The check is registered before the goroutine starts, so a cancel
	// clicked at once reaches the download.
	ctx, cancel := context.WithCancel(context.Background())
	a.updateMu.Lock()
	a.updateCheck = &updateCheck{cancel: cancel}
	a.updateState.Status = "downloading"
	a.updateState.Message = ""
	a.updateState.Progress = 0
	a.updateMu.Unlock()
	a.invalidate()

	go func() {
		defer cancel()
		err := a.updateInstall(ctx, func(done, total int64) {
			if total > 0 {
				a.setUpdateState(func(s *UpdateState) { s.Progress = float64(done) / float64(total) })
			}
		})

		a.updateMu.Lock()
		a.updateCheck = nil
		a.updateMu.Unlock()

		if err != nil {
			if ctx.Err() != nil {
				// The user canceled: back to the found state.
				a.setUpdateState(func(s *UpdateState) { s.Status = "idle" })
			} else {
				a.setUpdateState(func(s *UpdateState) { s.Status = "error"; s.Message = updateMessage(err) })
			}
			return
		}
		// The new version runs at the next launch; the app offers to
		// restart into it now.
		a.setUpdateState(func(s *UpdateState) { s.Status = "ready" })
	}()
}

// installPending installs the newest update the feed offers, through
// App.updateInstall. Asking the feed again keeps a version pulled from it
// in the meantime from installing anything but the newest.
func (a *App) installPending(ctx context.Context, progress func(done, total int64)) error {
	up, err := a.updateNow(ctx)
	if err != nil {
		return err
	}
	if up == nil {
		return errors.New("the update is no longer offered")
	}
	return up.Install(ctx, progress)
}

// cancelUpdate stops a download in progress.
func (a *App) cancelUpdate() {
	a.updateMu.Lock()
	chk := a.updateCheck
	a.updateMu.Unlock()
	if chk != nil {
		chk.cancel()
	}
}

// relaunchUpdate restarts the app into the installed update.
func (a *App) relaunchUpdate() { a.updateLaunch() }

// updateMessage makes an updater error readable.
func updateMessage(err error) string {
	if errors.Is(err, mygo.ErrUpdatesDisabled) {
		return "此版本不支持自动更新"
	}
	msg := err.Error()
	// The wrapped errors of the updater name the feed and the request; keep
	// the last line, which is the cause.
	if i := strings.LastIndex(msg, ": "); i > 0 {
		msg = msg[i+2:]
	}
	return msg
}

// #endregion

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
	// A left click opens the window: Windows shows the menu on a right
	// click only, so without this a left click does nothing.
	tray.OnClick(func() { a.showWindow() })
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
