package main

import (
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestRenderPages draws every page and every phase without a window, so a
// change that breaks the layout is caught without a desktop session. When
// UPDATE_SHOTS is set, it also writes the frames to /tmp for a look.
func TestRenderPages(t *testing.T) {
	app, now := newTestApp(t)
	// Some history, so the heatmap has colors.
	for i := 0; i < 6; i++ {
		app.startFocus()
		*now = now.Add(25 * time.Minute)
		app.advance()
		*now = now.Add(5 * time.Minute)
		app.advance()
		*now = now.Add(20 * time.Minute)
	}

	shots := []struct {
		name string
		page int
		prep func()
	}{
		{"idle", pageTimer, nil},
		{"focus", pageTimer, func() { app.startFocus(); *now = now.Add(7 * time.Minute) }},
		{"paused", pageTimer, func() { app.pause() }},
		{"break", pageTimer, func() {
			app.endFocus()
			app.startFocus()
			*now = now.Add(26 * time.Minute)
			app.advance()
		}},
		{"stats", pageStats, nil},
		{"settings", pageSettings, nil},
	}
	for _, s := range shots {
		app.page = s.page
		if s.prep != nil {
			s.prep()
		}
		img := ui.Render(app.view, 420, 640, 2)

		// The frame is the right size and not blank.
		if got := img.Bounds().Size(); got.X != 840 || got.Y != 1280 {
			t.Errorf("%s: the frame is %v, want 840x1280", s.name, got)
		}
		if countColors(img) < 8 {
			t.Errorf("%s: the frame has fewer than 8 colors, so it is blank", s.name)
		}
		if os.Getenv("UPDATE_SHOTS") != "" {
			writePNG(t, filepath.Join("/tmp", "shot-"+s.name+".png"), img)
		}
	}
}

// TestRenderDark draws the stats page in the dark theme.
func TestRenderDark(t *testing.T) {
	app, now := newTestApp(t)
	app.startFocus()
	*now = now.Add(25 * time.Minute)
	app.advance()
	app.page = pageStats

	tt := ui.NewTester(app.view, 420, 640)
	tt.SetDark(true)
	img := tt.Image()
	if countColors(img) < 8 {
		t.Error("the dark frame is blank")
	}
	if os.Getenv("UPDATE_SHOTS") != "" {
		writePNG(t, "/tmp/shot-dark.png", img)
	}
}

// TestRenderDayDetail draws the day dialog over the stats page.
func TestRenderDayDetail(t *testing.T) {
	app, now := newTestApp(t)
	app.startFocus()
	*now = now.Add(25 * time.Minute)
	app.advance()
	app.page = pageStats
	app.dayOpen, app.dayDate = true, "2026-10-06"

	img := ui.Render(app.view, 420, 640, 2)
	if countColors(img) < 8 {
		t.Error("the frame is blank")
	}
	if os.Getenv("UPDATE_SHOTS") != "" {
		writePNG(t, "/tmp/shot-day.png", img)
	}
}

// countColors is how many distinct colors the frame has, to tell a drawn page
// from a blank one.
func countColors(img *image.RGBA) int {
	seen := map[uint32]struct{}{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, a := img.At(x, y).RGBA()
			seen[r<<16|g<<8|bl<<0|a>>8] = struct{}{}
		}
	}
	return len(seen)
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pngEncode(f, img); err != nil {
		t.Fatal(err)
	}
}

func pngEncode(w io.Writer, img image.Image) error { return png.Encode(w, img) }
