package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/egoist/mygo/ui"
)

// writeTestPNG writes a solid-color PNG and returns its path.
func writeTestPNG(t *testing.T, c color.RGBA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wall.png")
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pngEncode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCustomWallpaperPaints checks that a readable custom image paints,
// and that switching the setting off returns to the default backdrop.
func TestCustomWallpaperPaints(t *testing.T) {
	app, _ := newTestApp(t)
	blue := writeTestPNG(t, color.RGBA{0, 0, 255, 255})

	app.mu.Lock()
	app.settings.CustomWallpaper = true
	app.settings.WallpaperPath = blue
	app.mu.Unlock()

	first := ui.Render(app.view, 420, 640, 1)
	if !hasBluishPixel(first) {
		dumpColors(t, first)
		t.Error("the custom wallpaper is not on screen")
	}

	app.mu.Lock()
	app.settings.CustomWallpaper = false
	app.mu.Unlock()

	second := ui.Render(app.view, 420, 640, 1)
	if framesEqual(second, first) {
		t.Error("turning the custom wallpaper off changed nothing")
	}
}

// TestMissingCustomWallpaperFallsBack checks that an unreadable image
// leaves the default backdrop in place rather than a blank window.
func TestMissingCustomWallpaperFallsBack(t *testing.T) {
	app, _ := newTestApp(t)
	app.mu.Lock()
	app.settings.CustomWallpaper = true
	app.settings.WallpaperPath = filepath.Join(t.TempDir(), "gone.png")
	app.mu.Unlock()

	img := ui.Render(app.view, 420, 640, 1)
	if countColors(img) < 8 {
		t.Error("the frame is blank with a missing wallpaper")
	}
}

// dumpColors prints the frame's most common colors, for a failing paint
// test.
func dumpColors(t *testing.T, img *image.RGBA) {
	t.Helper()
	counts := map[color.RGBA]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			counts[img.RGBAAt(x, y)]++
		}
	}
	best := make([]color.RGBA, 0, 8)
	for c := range counts {
		best = append(best, c)
	}
	sort.Slice(best, func(i, j int) bool { return counts[best[i]] > counts[best[j]] })
	for i, c := range best {
		if i == 8 {
			break
		}
		t.Logf("color %v x%d", c, counts[c])
	}
}

// framesEqual reports whether two frames painted the same colors, at a
// stride, which is close enough to tell a repaint from its absence.
func framesEqual(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y += 4 {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x += 4 {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				return false
			}
		}
	}
	return true
}

// hasBluishPixel reports whether the frame holds a pixel whose blue
// clearly leads: the glass frosting over a blue image keeps that order,
// and neither the gradient backdrop nor the dark theme does.
func hasBluishPixel(img *image.RGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			got := img.RGBAAt(x, y)
			if int(got.B) > int(got.R)+30 && int(got.B) > int(got.G)+30 {
				return true
			}
		}
	}
	return false
}
