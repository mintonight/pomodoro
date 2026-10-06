package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Storage {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

// TestSessionRoundTrip checks that a session keeps its times, its length and
// the date it is filed under.
func TestSessionRoundTrip(t *testing.T) {
	s := openTest(t)
	start := at("2026-10-06 09:00")
	end := at("2026-10-06 09:25")
	if err := s.AddSession(start, end, 25); err != nil {
		t.Fatal(err)
	}

	sessions, err := s.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("read %d sessions, want 1", len(sessions))
	}
	got := sessions[0]
	if !got.StartedAt.Equal(start) || !got.CompletedAt.Equal(end) {
		t.Errorf("times %v %v, want %v %v", got.StartedAt, got.CompletedAt, start, end)
	}
	if got.DurationMinutes != 25 {
		t.Errorf("duration %v, want 25", got.DurationMinutes)
	}
	if got.Date != "2026-10-06" {
		t.Errorf("date %s, want 2026-10-06", got.Date)
	}
}

// TestSessionFilesUnderItsStartDate checks the rule for a pomodoro that
// crosses midnight: it belongs to the day it began on.
func TestSessionFilesUnderItsStartDate(t *testing.T) {
	s := openTest(t)
	// 23:50 on one day, finishing at 00:15 the next.
	if err := s.AddSession(at("2026-10-06 23:50"), at("2026-10-07 00:15"), 25); err != nil {
		t.Fatal(err)
	}
	stats, err := s.DayStats()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stats["2026-10-06"]; !ok {
		t.Fatalf("the session is not filed under its start date: %v", stats)
	}
	if _, ok := stats["2026-10-07"]; ok {
		t.Errorf("the session also landed on the next day: %v", stats)
	}
}

// TestDayStatsAddsUp checks that the totals group by day.
func TestDayStatsAddsUp(t *testing.T) {
	s := openTest(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AddSession(at("2026-10-06 09:00"), at("2026-10-06 09:25"), 25))
	must(s.AddSession(at("2026-10-06 10:00"), at("2026-10-06 10:50"), 50))
	must(s.AddSession(at("2026-10-07 09:00"), at("2026-10-07 09:25"), 25))

	stats, err := s.DayStats()
	if err != nil {
		t.Fatal(err)
	}
	if got := stats["2026-10-06"]; got.Minutes != 75 || got.Count != 2 {
		t.Errorf("2026-10-06 is %v, want 75 minutes over 2", got)
	}
	if got := stats["2026-10-07"]; got.Minutes != 25 || got.Count != 1 {
		t.Errorf("2026-10-07 is %v, want 25 minutes over 1", got)
	}
}

// TestSettingsRoundTrip checks that the settings survive saving and loading,
// and that the defaults fill in what a stored copy lacks.
func TestSettingsRoundTrip(t *testing.T) {
	s := openTest(t)

	// A fresh database has the defaults.
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultSettings() {
		t.Errorf("a fresh database has %+v, want %+v", got, DefaultSettings())
	}

	want := Settings{FocusMinutes: 50, BreakMinutes: 10, LaunchAtStartup: true, AutoUpdate: true, CustomWallpaper: true, WallpaperPath: "C:/pics/wall.png", Theme: "dark"}
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("loaded %+v, want %+v", got, want)
	}

	// A value added to the settings later takes its default.
	if _, err := s.db.Exec(`UPDATE settings SET value = '{"focus_duration_minutes": 30}' WHERE key = 'settings'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.FocusMinutes != 30 {
		t.Errorf("focus is %v, want 30", got.FocusMinutes)
	}
	if got.BreakMinutes != DefaultSettings().BreakMinutes {
		t.Errorf("break is %v, want the default %v", got.BreakMinutes, DefaultSettings().BreakMinutes)
	}
	if got.Theme != "system" {
		t.Errorf("theme is %q, want system", got.Theme)
	}
}

// TestSettingsAreClamped checks that out of range values are brought back.
func TestSettingsAreClamped(t *testing.T) {
	s := openTest(t)
	if err := s.SaveSettings(Settings{FocusMinutes: 0, BreakMinutes: 9999, Theme: "neon"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.FocusMinutes != 25 {
		t.Errorf("focus is %v, want the default 25", got.FocusMinutes)
	}
	if got.BreakMinutes != 60 {
		t.Errorf("break is %v, want the maximum 60", got.BreakMinutes)
	}
	if got.Theme != "system" {
		t.Errorf("theme is %q, want system", got.Theme)
	}
}

// TestExportImportRoundTrip checks that an export can be read back, and that
// importing replaces the local sessions rather than adding to them.
func TestExportImportRoundTrip(t *testing.T) {
	s := openTest(t)
	if err := s.AddSession(at("2026-10-06 09:00"), at("2026-10-06 09:25"), 25); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSession(at("2026-10-06 10:00"), at("2026-10-06 10:50"), 50); err != nil {
		t.Fatal(err)
	}
	settings := Settings{FocusMinutes: 50, BreakMinutes: 10}
	path := filepath.Join(t.TempDir(), "export.json")
	if err := s.Export(path, settings); err != nil {
		t.Fatal(err)
	}

	// Importing into a database that already has other data overwrites it.
	other := openTest(t)
	if err := other.AddSession(at("2020-01-01 09:00"), at("2020-01-01 09:25"), 25); err != nil {
		t.Fatal(err)
	}
	imported, sessions, err := other.Import(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("imported %d sessions, want 2", len(sessions))
	}
	if imported.FocusMinutes != 50 || imported.BreakMinutes != 10 {
		t.Errorf("imported settings %+v", imported)
	}
	stats, err := other.DayStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats["2026-10-06"].Count != 2 {
		t.Errorf("after importing, the stats are %v", stats)
	}
	if _, ok := stats["2020-01-01"]; ok {
		t.Errorf("the old data was merged rather than replaced: %v", stats)
	}
}

// TestImportRejectsBadFiles checks the checks an import makes before it
// touches the local data.
func TestImportRejectsBadFiles(t *testing.T) {
	s := openTest(t)
	if err := s.AddSession(at("2026-10-06 09:00"), at("2026-10-06 09:25"), 25); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
	}{
		{"not json", `hello`},
		{"empty", ``},
		{"a foreign document", `{"hello": "world"}`},
		{"a future version", `{"version": 99, "sessions": []}`},
		{"a bad started_at", `{"version": 1, "sessions": [{"started_at": "yesterday", "completed_at": "2026-10-06T09:25:00+08:00", "duration_minutes": 25, "date": "2026-10-06"}]}`},
		{"a bad date", `{"version": 1, "sessions": [{"started_at": "2026-10-06T09:00:00+08:00", "completed_at": "2026-10-06T09:25:00+08:00", "duration_minutes": 25, "date": "06/10/2026"}]}`},
		{"a negative duration", `{"version": 1, "sessions": [{"started_at": "2026-10-06T09:00:00+08:00", "completed_at": "2026-10-06T09:25:00+08:00", "duration_minutes": -25, "date": "2026-10-06"}]}`},
	}
	dir := t.TempDir()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "bad.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Import(path); err == nil {
				t.Fatal("the import accepted the file")
			}
		})
	}

	// The local data is untouched by every failed import.
	sessions, err := s.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("a failed import left %d sessions, want the original 1", len(sessions))
	}
}
