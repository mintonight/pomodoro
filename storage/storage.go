// Package storage keeps the app's data: the completed focus sessions and the
// settings, both in a local SQLite database.
//
// Only sessions that were completed in full are stored. A focus that was
// ended early, or cancelled because it was paused for too long, leaves no
// trace.
package storage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DataVersion is the version of the JSON export format.
const DataVersion = 1

// DateLayout is the layout of the local date a session belongs to.
const DateLayout = "2006-01-02"

// Session is one completed focus period.
type Session struct {
	ID              int64
	StartedAt       time.Time
	CompletedAt     time.Time
	DurationMinutes float64
	Date            string // the local date of StartedAt, YYYY-MM-DD
}

// DayStat is the total focus of one local day.
type DayStat struct {
	Minutes float64
	Count   int
}

// Settings are the values the user can change.
type Settings struct {
	FocusMinutes    float64 `json:"focus_duration_minutes"`
	BreakMinutes    float64 `json:"break_duration_minutes"`
	LaunchAtStartup bool    `json:"launch_at_startup"`
	AutoUpdate      bool    `json:"auto_update"`
	// CustomWallpaper turns the custom backdrop on; WallpaperPath is the
	// image it shows. Empty falls back to the gradient.
	CustomWallpaper bool   `json:"custom_wallpaper"`
	WallpaperPath   string `json:"wallpaper_path"`
	Theme           string `json:"theme"` // "system", "light" or "dark"
}

// DefaultSettings are the settings of a fresh install.
func DefaultSettings() Settings {
	return Settings{
		FocusMinutes:    25,
		BreakMinutes:    5,
		LaunchAtStartup: false,
		AutoUpdate:      true,
		Theme:           "system",
	}
}

// Storage is the local database.
type Storage struct {
	db   *sql.DB
	path string
}

// Open opens (and creates, with its schema) the database at path.
func Open(path string) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is safe with one writer; keep it simple.
	db.SetMaxOpenConns(1)
	s := &Storage{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Storage) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS focus_sessions (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at       TEXT NOT NULL,
	completed_at     TEXT NOT NULL,
	duration_minutes REAL NOT NULL,
	date             TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_focus_sessions_date ON focus_sessions (date);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`)
	return err
}

// Close closes the database.
func (s *Storage) Close() error { return s.db.Close() }

// Path is where the database lives.
func (s *Storage) Path() string { return s.path }

// AddSession stores a completed focus whose start and end are the scheduled
// wall-clock times. durationMinutes is the focused time, which the pause time
// does not count toward.
func (s *Storage) AddSession(startedAt, completedAt time.Time, durationMinutes float64) error {
	date := startedAt.Format(DateLayout)
	_, err := s.db.Exec(
		`INSERT INTO focus_sessions (started_at, completed_at, duration_minutes, date) VALUES (?, ?, ?, ?)`,
		startedAt.Format(time.RFC3339), completedAt.Format(time.RFC3339), durationMinutes, date,
	)
	return err
}

// Sessions returns every session, oldest first.
func (s *Storage) Sessions() ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, started_at, completed_at, duration_minutes, date FROM focus_sessions ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var (
			sess                   Session
			startedAt, completedAt string
		)
		if err := rows.Scan(&sess.ID, &startedAt, &completedAt, &sess.DurationMinutes, &sess.Date); err != nil {
			return nil, err
		}
		sess.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		sess.CompletedAt, _ = time.Parse(time.RFC3339, completedAt)
		out = append(out, sess)
	}
	return out, rows.Err()
}

// DayStats returns the total focus time and session count per local date.
func (s *Storage) DayStats() (map[string]DayStat, error) {
	rows, err := s.db.Query(
		`SELECT date, SUM(duration_minutes), COUNT(*) FROM focus_sessions GROUP BY date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]DayStat{}
	for rows.Next() {
		var (
			date string
			stat DayStat
		)
		if err := rows.Scan(&date, &stat.Minutes, &stat.Count); err != nil {
			return nil, err
		}
		out[date] = stat
	}
	return out, rows.Err()
}

// ReplaceAll deletes every session and inserts the given ones, in one
// transaction. It is what importing does: the local data is overwritten, and
// nothing is merged.
func (s *Storage) ReplaceAll(sessions []Session) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM focus_sessions`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO focus_sessions (started_at, completed_at, duration_minutes, date) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, sess := range sessions {
		if _, err := stmt.Exec(
			sess.StartedAt.Format(time.RFC3339), sess.CompletedAt.Format(time.RFC3339), sess.DurationMinutes, sess.Date,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const settingsKey = "settings"

// LoadSettings returns the stored settings, or the defaults.
func (s *Storage) LoadSettings() (Settings, error) {
	def := DefaultSettings()
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	// Start from the defaults so a key added later has a value.
	out := def
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return def, fmt.Errorf("settings are corrupt: %w", err)
	}
	out.normalize()
	return out, nil
}

// SaveSettings stores the settings.
func (s *Storage) SaveSettings(v Settings) error {
	v.normalize()
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKey, string(raw),
	)
	return err
}

// normalize keeps the values within what the app supports.
func (v *Settings) normalize() {
	if v.FocusMinutes < 1 {
		v.FocusMinutes = 25
	}
	if v.FocusMinutes > 180 {
		v.FocusMinutes = 180
	}
	if v.BreakMinutes < 1 {
		v.BreakMinutes = 5
	}
	if v.BreakMinutes > 60 {
		v.BreakMinutes = 60
	}
	switch v.Theme {
	case "system", "light", "dark":
	default:
		v.Theme = "system"
	}
}

// #region export and import

type exportSettings struct {
	FocusDurationMinutes float64 `json:"focus_duration_minutes"`
	BreakDurationMinutes float64 `json:"break_duration_minutes"`
}

type exportSession struct {
	StartedAt       string  `json:"started_at"`
	CompletedAt     string  `json:"completed_at"`
	DurationMinutes float64 `json:"duration_minutes"`
	Date            string  `json:"date"`
}

type exportFile struct {
	Version    int             `json:"version"`
	ExportedAt string          `json:"exported_at"`
	Settings   exportSettings  `json:"settings"`
	Sessions   []exportSession `json:"sessions"`
}

// Export writes every session and the durations as JSON.
func (s *Storage) Export(path string, settings Settings) error {
	sessions, err := s.Sessions()
	if err != nil {
		return err
	}
	doc := exportFile{
		Version:    DataVersion,
		ExportedAt: time.Now().Format(time.RFC3339),
		Settings: exportSettings{
			FocusDurationMinutes: settings.FocusMinutes,
			BreakDurationMinutes: settings.BreakMinutes,
		},
		Sessions: make([]exportSession, 0, len(sessions)),
	}
	for _, sess := range sessions {
		doc.Sessions = append(doc.Sessions, exportSession{
			StartedAt:       sess.StartedAt.Format(time.RFC3339),
			CompletedAt:     sess.CompletedAt.Format(time.RFC3339),
			DurationMinutes: sess.DurationMinutes,
			Date:            sess.Date,
		})
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

// Import reads a file written by Export, checks it, and replaces the local
// sessions. It does not merge anything.
func (s *Storage) Import(path string) (Settings, []Session, error) {
	var out Settings
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, nil, err
	}

	// Decode strictly enough to catch a file that is not ours.
	var doc exportFile
	dec := json.NewDecoder(bytes.NewReader(trimBOM(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return out, nil, fmt.Errorf("this does not look like a Pomodoro export: %w", err)
	}
	if doc.Version != DataVersion {
		return out, nil, fmt.Errorf("unsupported export version %d (this app reads version %d)", doc.Version, DataVersion)
	}

	sessions := make([]Session, 0, len(doc.Sessions))
	for i, in := range doc.Sessions {
		startedAt, err := time.Parse(time.RFC3339, in.StartedAt)
		if err != nil {
			return out, nil, fmt.Errorf("session %d: started_at: %w", i+1, err)
		}
		completedAt, err := time.Parse(time.RFC3339, in.CompletedAt)
		if err != nil {
			return out, nil, fmt.Errorf("session %d: completed_at: %w", i+1, err)
		}
		if in.DurationMinutes < 0 {
			return out, nil, fmt.Errorf("session %d: negative duration", i+1)
		}
		date := in.Date
		if _, err := time.Parse(DateLayout, date); err != nil {
			return out, nil, fmt.Errorf("session %d: date: %w", i+1, err)
		}
		sessions = append(sessions, Session{
			StartedAt:       startedAt,
			CompletedAt:     completedAt,
			DurationMinutes: in.DurationMinutes,
			Date:            date,
		})
	}

	if err := s.ReplaceAll(sessions); err != nil {
		return out, nil, err
	}

	out = DefaultSettings()
	out.FocusMinutes = doc.Settings.FocusDurationMinutes
	out.BreakMinutes = doc.Settings.BreakDurationMinutes
	out.normalize()
	return out, sessions, nil
}

// trimBOM drops a UTF-8 byte order mark, which some editors add.
func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		return b[3:]
	}
	return b
}
