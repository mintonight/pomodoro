// Command pomodoro is a small desktop Pomodoro timer.
//
// The window shows the timer, a heatmap of the focus time of every day and a
// few settings; the timer keeps running in the system tray after the window is
// closed. Everything is stored locally, in a SQLite database.
package main

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"

	"pomodoro/storage"
)

func main() {
	// One instance only: a second launch shows the window of the first.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	store, err := openStorage()
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	settings, err := store.LoadSettings()
	if err != nil {
		log.Println("pomodoro:", err)
	}

	app := newApp(store, settings)
	app.desktop = true
	// The defer above runs after this, so the clock is stopped first.
	defer app.stop()

	// The appearance can be set before Run, from what the user saved.
	mygo.Theme.SetSource(themeSource(settings.Theme))

	mygo.App.WhenReady(app.start)
	mygo.App.OnSecondInstance(func([]string, string) { app.showWindow() })

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// openStorage opens the database in the app's data directory, which MyGo
// creates on first use.
func openStorage() (*storage.Storage, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	return storage.Open(filepath.Join(dir, "pomodoro.db"))
}

// themeSource maps the stored theme to MyGo's appearance.
func themeSource(v string) mygo.ThemeSource {
	switch v {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	default:
		return mygo.ThemeSystem
	}
}
