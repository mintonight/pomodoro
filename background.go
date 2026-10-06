package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// The window's backdrop, drawn by paintWindowBackground. With the custom
// wallpaper off, it is the desktop wallpaper frosted through the glass
// plugin — the Mica look — or the gradient below when the wallpaper cannot
// be read. With it on, it is the image the user picked, frosted the same
// way. Images are cached by path: a change of path shows at once, a
// change of the desktop wallpaper at the next launch.
type wallpaper struct {
	mu      sync.Mutex
	path    string // the image loaded, "" for the desktop's
	bitmap  *ui.Bitmap
	modTime time.Time
}

var desktopWallpaper wallpaper

// load returns the bitmap of path, or of the desktop wallpaper when path
// is "". The result is cached until the file changes.
func (w *wallpaper) load(path string) *ui.Bitmap {
	if path == "" {
		path = filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Themes", "TranscodedWallpaper")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.bitmap != nil && w.path == path && w.modTime.Equal(info.ModTime()) {
		return w.bitmap
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	bm, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil
	}
	w.path, w.bitmap, w.modTime = path, bm, info.ModTime()
	return bm
}

// paintWindowBackground draws the backdrop the settings describe: the
// custom image when custom is set and it can be read, the desktop
// wallpaper frosted otherwise, and the gradient when neither works.
func paintWindowBackground(p *ui.Painter, r ui.Rect, dark, custom bool, path string) {
	if custom {
		if bm := desktopWallpaper.load(path); bm != nil {
			p.Image(bm, r, ui.Cover)
			glass.Paint(p, r, 0, glass.Glass{Style: glass.Regular})
			return
		}
	}
	if bm := desktopWallpaper.load(""); bm != nil {
		p.Image(bm, r, ui.Cover)
		glass.Paint(p, r, 0, glass.Glass{Style: glass.Regular})
		return
	}
	paintGradientBackground(p, r, dark)
}

// paintGradientBackground is the fallback backdrop: GoRex's four-stop
// gradient, for machines where no wallpaper can be read.
func paintGradientBackground(p *ui.Painter, r ui.Rect, dark bool) {
	stops := []ui.Color{ui.Hex("#f6e7f6"), ui.Hex("#efe1e6"), ui.Hex("#eee0d3"), ui.Hex("#ece1bd")}
	tint := ui.RGBA(242, 214, 222, 0.35)
	if dark {
		stops = []ui.Color{ui.Hex("#2c2131"), ui.Hex("#231e27"), ui.Hex("#221f22"), ui.Hex("#2a2619")}
		tint = ui.RGBA(80, 40, 60, 0.25)
	}
	boundaries := []float32{0, 0.38, 0.70, 1}
	for i := 0; i < 3; i++ {
		top, bottom := boundaries[i]*r.H, boundaries[i+1]*r.H
		// Cover rounding seams between adjacent gradient bands.
		height := bottom - top
		if i < 2 {
			height += 1
		}
		p.FillGradient(ui.Rect{X: r.X, Y: r.Y + top, W: r.W, H: height}, ui.LinearGradient{
			From: stops[i], To: stops[i+1], Angle: 180,
		}, 0)
	}
	clear := tint
	clear.A = 0
	p.FillGradient(r, ui.LinearGradient{From: clear, To: tint, Angle: 90, Start: 0.35, End: 1}, 0)
}
