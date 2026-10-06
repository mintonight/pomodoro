package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// Draw the app icon: a pomodoro whose stem fans out into five sprouts in
// the five colors of mygo.egoist.dev's bang marks (oklch from the site CSS,
// converted to sRGB). The tray icon is the same drawing at 64x64.

// goColor is the straight-alpha RGBA a Go color paints with.
func goColor(hex string, alpha uint8) color.RGBA {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		panic("bad hex " + hex)
	}
	return color.RGBA{uint8(r), uint8(g), uint8(b), alpha}
}

var bangColors = []string{"#589cbc", "#e67183", "#40a844", "#b2913b", "#7777aa"}

const (
	size   = 1024
	cx     = 512.0
	cy     = 620.0 // the tomato's center
	radius = 330.0
	// The root all five sprouts grow from, sunken into the tomato's top.
	rootX, rootY = 512.0, 336.0
)

func inTomato(x, y float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx/(radius*radius)+dy*dy/(radius*radius*0.94) <= 1
}

func main() {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// The tomato body: the warm red of the tray icon, shaded top-down.
	body := color.RGBA{228, 88, 82, 255}
	bodyTop := color.RGBA{242, 118, 104, 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if inTomato(float64(x), float64(y)) {
				// A soft vertical shade from bodyTop to body.
				t := math.Min(1, math.Max(0, (float64(y)-(cy-radius))/radius*1.4))
				img.Set(x, y, mix(bodyTop, body, t))
			}
		}
	}

	// The five sprouts fan out from one root, back to front so the middle
	// one sits in front. Each is a tapered leaf: a quadratic curve to its
	// tip, fattest a third of the way out, pointed at both ends. The tips
	// carry a small ball, as the bang marks of the MyGo wordmark read as
	// stems with a dot on top.
	type leaf struct {
		angle float64 // degrees from straight up
		len   float64
		wide  float64
	}
	leaves := []leaf{
		{-62, 235, 46},
		{62, 235, 46},
		{-31, 285, 54},
		{31, 285, 54},
		{0, 320, 60},
	}
	for i, l := range leaves {
		drawLeaf(img, l.angle, l.len, l.wide, goColor(bangColors[i], 255))
	}

	writePNG(img, filepath.Join("resources", "icon.png"))
	// The tray icon is the same drawing downscaled, so the tray and the
	// taskbar show one mark.
	writePNG(downscale(img, 64), filepath.Join("resources", "tray.png"))
}

// writePNG encodes img to path.
func writePNG(img image.Image, path string) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

// downscale averages whole blocks of the source into each pixel of a
// size x size image, which needs the source side to divide evenly. The
// pixels are alpha-premultiplied, so edges shrink without dark halos.
func downscale(src *image.RGBA, size int) *image.RGBA {
	block := src.Bounds().Dx() / size
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a, n int
			for dy := 0; dy < block; dy++ {
				for dx := 0; dx < block; dx++ {
					c := src.RGBAAt(x*block+dx, y*block+dy)
					r, g, b, a = r+int(c.R), g+int(c.G), b+int(c.B), a+int(c.A)
					n++
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)})
		}
	}
	return out
}

// drawLeaf paints one sprout: a curved spine from the root toward angle,
// thickened by the width profile and rounded at the tip.
func drawLeaf(img *image.RGBA, angleDeg, length, wide float64, c color.RGBA) {
	a := (angleDeg - 90) * math.Pi / 180 // 0 = straight up
	tipX, tipY := rootX+length*math.Cos(a), rootY+length*math.Sin(a)
	// Bend the spine outward: the control point sits between the root and
	// the tip, pushed away from straight up.
	midX, midY := (rootX+tipX)/2, (rootY+tipY)/2
	bend := length * 0.22
	ctlX := midX + bend*math.Cos(a+math.Pi/2)*sign(angleDeg)
	ctlY := midY + bend*math.Sin(a+math.Pi/2)*sign(angleDeg)

	steps := 500
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		// The quadratic bezier spine.
		u := 1 - t
		x := u*u*rootX + 2*u*t*ctlX + t*t*tipX
		y := u*u*rootY + 2*u*t*ctlY + t*t*tipY
		// Fattest a third of the way out, tapering toward the tip.
		r := wide / 2 * math.Sin(math.Pi*math.Pow(t, 0.72))
		fillCircle(img, x, y, r, c)
	}
	// A dot at the tip, the bang mark's head.
	fillCircle(img, tipX, tipY, wide/2*0.62, c)
}

func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func fillCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	x0, x1 := int(cx-r)-1, int(cx+r)+1
	y0, y1 := int(cy-r)-1, int(cy+r)+1
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: lerp(a.R, b.R, t), G: lerp(a.G, b.G, t),
		B: lerp(a.B, b.B, t), A: 255,
	}
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t + 0.5)
}
