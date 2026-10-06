package main

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// The window's backdrop, drawn by paintWindowBackground: the desktop
// wallpaper, frosted through the glass plugin — the Mica look the
// native-UI surface cannot show. Falls back to a static gradient when the
// wallpaper cannot be read. Read once per launch; a wallpaper change shows
// the next time the app starts.
type wallpaper struct {
	once   sync.Once
	bitmap *ui.Bitmap
}

var desktopWallpaper wallpaper

// load decodes the current wallpaper once. The TranscodedWallpaper file is
// what Windows itself shows, whatever image format the user picked.
func (w *wallpaper) load() {
	w.once.Do(func() {
		path := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Themes", "TranscodedWallpaper")
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if bm, err := ui.DecodeBitmap(data); err == nil {
			w.bitmap = bm
		}
	})
}

func paintWindowBackground(p *ui.Painter, r ui.Rect, dark bool) {
	desktopWallpaper.load()
	if desktopWallpaper.bitmap == nil {
		paintGradientBackground(p, r, dark)
		return
	}
	// The wallpaper fills the window, then one pane of glass over it all
	// frosts it: the Mica-like backdrop.
	p.Image(desktopWallpaper.bitmap, r, ui.Cover)
	glass.Paint(p, r, 0, glass.Glass{Style: glass.Regular})
}

// paintGradientBackground is the fallback backdrop: GoRex's four-stop
// gradient, for machines where the wallpaper cannot be read.
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
