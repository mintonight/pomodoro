package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"pomodoro/storage"
)

// newTestApp builds an app on a database in a temporary directory, with a
// clock the test moves by hand.
func newTestApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	app := newApp(store, storage.DefaultSettings())
	app.now = func() time.Time { return now }
	return app, &now
}

// TestSessionIsRecorded checks that a completed focus is stored with its real
// length and its starting date, and that the stats cache follows it.
func TestSessionIsRecorded(t *testing.T) {
	app, now := newTestApp(t)

	app.startFocus()
	*now = now.Add(25 * time.Minute)
	app.advance()

	sessions, err := app.store.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("stored %d sessions, want 1", len(sessions))
	}
	if got := sessions[0].DurationMinutes; got != 25 {
		t.Errorf("stored %v minutes, want 25", got)
	}
	if got := sessions[0].Date; got != "2026-10-06" {
		t.Errorf("stored under %s, want 2026-10-06", got)
	}

	stats := app.dayStats()
	if got := stats["2026-10-06"].Count; got != 1 {
		t.Errorf("the day counts %d pomodoros, want 1", got)
	}
	if got := stats["2026-10-06"].Minutes; got != 25 {
		t.Errorf("the day has %v minutes, want 25", got)
	}
}

// TestEarlyEndIsNotRecorded checks that ending a focus early leaves no trace.
func TestEarlyEndIsNotRecorded(t *testing.T) {
	app, now := newTestApp(t)

	app.startFocus()
	*now = now.Add(18 * time.Minute)
	app.endFocus()

	sessions, err := app.store.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("an early end stored %d sessions, want 0", len(sessions))
	}
	if got := app.snapshot().Phase; got != PhaseIdle {
		t.Errorf("phase %v, want idle", got)
	}
}

// TestPauseCancelsAfterTheLimit checks that a pause past 30 minutes drops the
// focus without recording it.
func TestPauseCancelsAfterTheLimit(t *testing.T) {
	app, now := newTestApp(t)

	app.startFocus()
	*now = now.Add(5 * time.Minute)
	app.pause()
	*now = now.Add(31 * time.Minute)
	app.advance()

	sessions, err := app.store.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("a cancelled focus stored %d sessions, want 0", len(sessions))
	}
	if got := app.snapshot().Phase; got != PhaseIdle {
		t.Errorf("phase %v, want idle", got)
	}
}

// TestBreakThenManualStart checks that a break ends in idle and that the next
// focus needs the user.
func TestBreakThenManualStart(t *testing.T) {
	app, now := newTestApp(t)

	app.startFocus()
	*now = now.Add(25 * time.Minute)
	app.advance()
	if got := app.snapshot().Phase; got != PhaseBreak {
		t.Fatalf("phase %v, want break", got)
	}

	*now = now.Add(5 * time.Minute)
	app.advance()
	if got := app.snapshot().Phase; got != PhaseIdle {
		t.Fatalf("phase %v, want idle", got)
	}
	// Nothing starts on its own.
	*now = now.Add(time.Hour)
	app.advance()
	if got := app.snapshot().Phase; got != PhaseIdle {
		t.Errorf("phase %v after an hour, want idle", got)
	}
}

// TestSettingsApplyToTheNextRound checks that changing the lengths leaves a
// running round alone and is used by the next one.
func TestSettingsApplyToTheNextRound(t *testing.T) {
	app, now := newTestApp(t)

	app.startFocus()
	app.setFocusMinutes(50)

	// The running round keeps its own length.
	if got := app.snapshot().Remaining; got != 25*time.Minute {
		t.Errorf("the running focus has %v left, want 25m", got)
	}
	*now = now.Add(25 * time.Minute)
	app.advance()
	if got := app.snapshot().Phase; got != PhaseBreak {
		t.Fatalf("phase %v, want break", got)
	}

	*now = now.Add(5 * time.Minute)
	app.advance()
	// The next round uses the new length, and so does the idle display.
	if got := app.snapshot().Total; got != 50*time.Minute {
		t.Errorf("the next focus is %v, want 50m", got)
	}
	app.startFocus()
	if got := app.snapshot().Total; got != 50*time.Minute {
		t.Errorf("the started focus is %v, want 50m", got)
	}
}

// TestImportedSettingsReachTheControls checks that an import refreshes what
// the settings page shows.
func TestImportedSettingsReachTheControls(t *testing.T) {
	app, _ := newTestApp(t)

	app.focusField = 25
	app.syncSettingFields(storage.Settings{FocusMinutes: 50, BreakMinutes: 10, Theme: "dark"})
	if app.focusField != 50 || app.breakField != 10 || app.themeField != "深色" {
		t.Errorf("the controls show %v/%v/%q", app.focusField, app.breakField, app.themeField)
	}
}

// TestViewRenders checks that every page builds, and that the timer's buttons
// drive the app. It runs without a window, or a desktop session.
func TestViewRenders(t *testing.T) {
	app, now := newTestApp(t)
	tt := ui.NewTester(app.view, 420, 640)

	if !tt.HasText("开始") {
		t.Fatalf("the timer page shows %q", tt.Texts())
	}
	if !tt.HasText("25:00") {
		t.Fatalf("the timer page does not show the countdown: %q", tt.Texts())
	}

	if err := tt.Click("开始"); err != nil {
		t.Fatal(err)
	}
	if app.snapshot().Phase != PhaseFocus {
		t.Fatalf("clicking 开始 left the app in %v", app.snapshot().Phase)
	}

	// Let the countdown move, then pause.
	*now = now.Add(time.Minute)
	tt.Frame()
	if !tt.HasText("24:00") {
		t.Errorf("after a minute the page shows %q", tt.Texts())
	}
	if err := tt.Click("暂停"); err != nil {
		t.Fatal(err)
	}
	if app.snapshot().Phase != PhasePaused {
		t.Fatalf("clicking 暂停 left the app in %v", app.snapshot().Phase)
	}
	if err := tt.Click("继续"); err != nil {
		t.Fatal(err)
	}
	if app.snapshot().Phase != PhaseFocus {
		t.Fatalf("clicking 继续 left the app in %v", app.snapshot().Phase)
	}
	if err := tt.Click("结束"); err != nil {
		t.Fatal(err)
	}
	if app.snapshot().Phase != PhaseIdle {
		t.Fatalf("clicking 结束 left the app in %v", app.snapshot().Phase)
	}

	// The other two pages.
	app.page = pageStats
	tt.Frame()
	if !tt.HasText("本月总专注") || !tt.HasText("本月番茄") {
		t.Errorf("the stats page shows %q", tt.Texts())
	}
	app.page = pageSettings
	tt.Frame()
	if !tt.HasText("专注时长") || !tt.HasText("导出数据") {
		t.Errorf("the settings page shows %q", tt.Texts())
	}
}

// TestStatsPageCounts checks that the stats page adds up the month.
func TestStatsPageCounts(t *testing.T) {
	app, now := newTestApp(t)

	// Two finished pomodoros today, one yesterday.
	for i := 0; i < 2; i++ {
		app.startFocus()
		*now = now.Add(25 * time.Minute)
		app.advance()
		*now = now.Add(5 * time.Minute)
		app.advance()
	}
	app.startFocus()
	*now = now.Add(25 * time.Minute)
	app.advance()

	stats := app.dayStats()
	if len(stats) != 1 {
		t.Fatalf("the stats cover %d days, want 1: %v", len(stats), stats)
	}
	if got := stats["2026-10-06"].Count; got != 3 {
		t.Errorf("the day counts %d, want 3", got)
	}
	if got := app.totalSessions(); got != 3 {
		t.Errorf("the total is %d, want 3", got)
	}
}
