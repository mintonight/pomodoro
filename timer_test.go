package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestFocusRunsToItsEnd checks the whole round: a focus completes, a break
// follows and ends, and only the focus is recorded.
func TestFocusRunsToItsEnd(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 25*time.Minute, 5*time.Minute)

	if events := e.Advance(base.Add(10 * time.Minute)); len(events) != 0 {
		t.Fatalf("a focus in progress reported %v", events)
	}

	events := e.Advance(base.Add(25 * time.Minute))
	if len(events) != 1 || events[0].Kind != EventFocusCompleted {
		t.Fatalf("at the end of the focus: %v", events)
	}
	if got := events[0].CompletedAt; !got.Equal(base.Add(25 * time.Minute)) {
		t.Errorf("completed at %v, want %v", got, base.Add(25*time.Minute))
	}
	if got := events[0].Focused; got != 25*time.Minute {
		t.Errorf("focused %v, want 25m", got)
	}
	if snap := e.Snapshot(base.Add(25 * time.Minute)); snap.Phase != PhaseBreak {
		t.Errorf("phase %v, want break", snap.Phase)
	}

	events = e.Advance(base.Add(30 * time.Minute))
	if len(events) != 1 || events[0].Kind != EventBreakCompleted {
		t.Fatalf("at the end of the break: %v", events)
	}
	if snap := e.Snapshot(base.Add(30 * time.Minute)); snap.Phase != PhaseIdle {
		t.Errorf("phase %v, want idle", snap.Phase)
	}
	// The next round waits for the user.
	if events := e.Advance(base.Add(3 * time.Hour)); len(events) != 0 {
		t.Errorf("an idle timer reported %v", events)
	}
}

// TestSleepCatchesUp checks that a focus and the break after it both ending
// while the computer slept bring the timer straight to idle, with both events
// in order and no extra ones.
func TestSleepCatchesUp(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 5*time.Minute, 5*time.Minute)

	events := e.Advance(base.Add(30 * time.Minute))
	if len(events) != 2 {
		t.Fatalf("two periods ending while asleep reported %d events: %v", len(events), events)
	}
	if events[0].Kind != EventFocusCompleted || events[1].Kind != EventBreakCompleted {
		t.Fatalf("events %v, want focus then break", events)
	}
	if snap := e.Snapshot(base.Add(30 * time.Minute)); snap.Phase != PhaseIdle {
		t.Errorf("phase %v, want idle", snap.Phase)
	}
}

// TestSleepDuringFocus checks that a focus whose end passed while the
// computer slept is complete when it wakes.
func TestSleepDuringFocus(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 25*time.Minute, 5*time.Minute)

	events := e.Advance(base.Add(28 * time.Minute))
	if len(events) != 1 || events[0].Kind != EventFocusCompleted {
		t.Fatalf("waking after the focus was due: %v", events)
	}
	if snap := e.Snapshot(base.Add(28 * time.Minute)); snap.Phase != PhaseBreak {
		t.Errorf("phase %v, want break", snap.Phase)
	}
	// The break began when the focus was due, so it is 3 minutes in.
	if got, want := e.breakDeadline(), base.Add(30*time.Minute); !got.Equal(want) {
		t.Errorf("the break ends at %v, want %v", got, want)
	}
}

// TestPauseDoesNotCount checks that the paused time is not focused time.
func TestPauseDoesNotCount(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 25*time.Minute, 5*time.Minute)

	e.Pause(base.Add(10 * time.Minute))
	snap := e.Snapshot(base.Add(40 * time.Minute))
	if snap.Phase != PhasePaused {
		t.Fatalf("phase %v, want paused", snap.Phase)
	}
	if got, want := snap.Remaining, 15*time.Minute; got != want {
		t.Errorf("remaining %v, want %v", got, want)
	}
	if got, want := snap.Focused, 10*time.Minute; got != want {
		t.Errorf("focused %v, want %v", got, want)
	}
	if got, want := snap.PausedFor, 30*time.Minute; got != want {
		t.Errorf("paused for %v, want %v", got, want)
	}

	// Continuing pushes the end out by the pause.
	e.Resume(base.Add(40 * time.Minute))
	if got, want := e.focusDeadline(), base.Add(55*time.Minute); !got.Equal(want) {
		t.Errorf("the focus ends at %v, want %v", got, want)
	}

	events := e.Advance(base.Add(55 * time.Minute))
	if len(events) != 1 || events[0].Kind != EventFocusCompleted {
		t.Fatalf("the resumed focus: %v", events)
	}
	if got := events[0].Focused; got != 25*time.Minute {
		t.Errorf("focused %v, want 25m", got)
	}
}

// TestPauseTimeoutCancels checks that a pause longer than the limit drops the
// focus, recording nothing.
func TestPauseTimeoutCancels(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 25*time.Minute, 5*time.Minute)
	e.Pause(base.Add(10 * time.Minute))

	// Just inside the limit: still paused.
	if events := e.Advance(base.Add(10*time.Minute + PauseTimeout - time.Second)); len(events) != 0 {
		t.Fatalf("a pause within the limit reported %v", events)
	}
	// Past it, even though the computer slept through it.
	events := e.Advance(base.Add(10*time.Minute + PauseTimeout + 5*time.Minute))
	if len(events) != 1 || events[0].Kind != EventFocusCancelled {
		t.Fatalf("a pause past the limit: %v", events)
	}
	if snap := e.Snapshot(base.Add(time.Hour)); snap.Phase != PhaseIdle {
		t.Errorf("phase %v, want idle", snap.Phase)
	}
}

// TestCustomPeriods checks that a longer focus is still one pomodoro and that
// its length is what is reported.
func TestCustomPeriods(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 50*time.Minute, 10*time.Minute)

	events := e.Advance(base.Add(50 * time.Minute))
	if len(events) != 1 {
		t.Fatalf("events %v", events)
	}
	if got := events[0].Focused.Minutes(); got != 50 {
		t.Errorf("recorded %v minutes, want 50", got)
	}
}

// TestStartedAtKeepsTheOriginalTime checks that a paused and resumed focus
// still belongs to the day it began on.
func TestStartedAtKeepsTheOriginalTime(t *testing.T) {
	base := time.Date(2026, 10, 6, 23, 50, 0, 0, time.Local)
	var e Engine
	e.StartFocus(base, 25*time.Minute, 5*time.Minute)

	events := e.Advance(base.Add(25 * time.Minute))
	if len(events) != 1 {
		t.Fatalf("events %v", events)
	}
	if got := events[0].StartedAt.Format("2006-01-02"); got != "2026-10-06" {
		t.Errorf("recorded under %s, want 2026-10-06", got)
	}
	if got := events[0].CompletedAt.Format("2006-01-02"); got != "2026-10-07" {
		t.Errorf("completed on %s, want 2026-10-07", got)
	}
}

// TestNextDeadline checks what the clock should wake for.
func TestNextDeadline(t *testing.T) {
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	var e Engine
	if _, ok := e.NextDeadline(base); ok {
		t.Error("an idle timer reported a deadline")
	}
	e.StartFocus(base, 25*time.Minute, 5*time.Minute)
	at, ok := e.NextDeadline(base.Add(time.Minute))
	if !ok || !at.Equal(base.Add(25*time.Minute)) {
		t.Errorf("deadline %v %v, want %v", at, ok, base.Add(25*time.Minute))
	}
	e.Pause(base.Add(time.Minute))
	at, ok = e.NextDeadline(base.Add(time.Minute))
	if !ok || !at.Equal(base.Add(time.Minute+PauseTimeout)) {
		t.Errorf("paused deadline %v %v", at, ok)
	}
}

// TestClockLabel checks the countdown formatting.
func TestClockLabel(t *testing.T) {
	tests := []struct {
		snap Snapshot
		want string
	}{
		{Snapshot{Phase: PhaseIdle, Total: 25 * time.Minute}, "25:00"},
		{Snapshot{Phase: PhaseFocus, Total: 25 * time.Minute, Remaining: 18*time.Minute + 42*time.Second}, "18:42"},
		{Snapshot{Phase: PhaseFocus, Total: 25 * time.Minute, Remaining: 900 * time.Millisecond}, "00:01"},
		{Snapshot{Phase: PhaseFocus, Total: time.Hour, Remaining: time.Hour}, "1:00:00"},
	}
	for _, tc := range tests {
		if got := clockLabel(tc.snap); got != tc.want {
			t.Errorf("clockLabel(%+v) = %q, want %q", tc.snap, got, tc.want)
		}
	}
}

// TestTrayTip checks the hover text of the tray.
func TestTrayTip(t *testing.T) {
	tests := []struct {
		snap Snapshot
		want string
	}{
		{Snapshot{Phase: PhaseFocus, Remaining: 18*time.Minute + 42*time.Second}, "专注中 · 18:42"},
		{Snapshot{Phase: PhaseBreak, Remaining: 4*time.Minute + 32*time.Second}, "休息中 · 04:32"},
		{Snapshot{Phase: PhasePaused, Remaining: time.Minute}, "已暂停 · 01:00"},
		{Snapshot{Phase: PhaseIdle}, "Pomodoro · 空闲"},
	}
	for _, tc := range tests {
		if got := trayTip(tc.snap); got != tc.want {
			t.Errorf("trayTip(%+v) = %q, want %q", tc.snap, got, tc.want)
		}
	}
}

// TestBucketColor checks the fixed buckets of the heatmap: 0, 1-30, 31-60,
// 61-120 and 120+ minutes each get one color, whatever the month's figures.
func TestBucketColor(t *testing.T) {
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		groups := map[ui.Color][]float64{}
		for _, m := range []float64{0, 1, 15, 30, 31, 45, 60, 61, 90, 120, 121, 300} {
			c := bucketColor(m, theme)
			groups[c] = append(groups[c], m)
		}
		if len(groups) != 5 {
			t.Errorf("dark=%v: the buckets gave %d colors, want 5: %v", theme.Dark, len(groups), groups)
		}
		// Neighbouring buckets never share a color.
		if bucketColor(0, theme) == bucketColor(1, theme) ||
			bucketColor(30, theme) == bucketColor(31, theme) ||
			bucketColor(60, theme) == bucketColor(61, theme) ||
			bucketColor(120, theme) == bucketColor(121, theme) {
			t.Errorf("dark=%v: a bucket boundary shares a color", theme.Dark)
		}
	}
}
