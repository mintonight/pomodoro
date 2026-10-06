package main

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestControlsReveal(t *testing.T) {
	app, _ := newTestApp(t)
	tt := ui.NewTester(app.view, 420, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Move(10, 300)
	pixels := func(r image.Rectangle) []byte {
		img := tt.Image()
		var out []byte
		for y := r.Min.Y; y < r.Max.Y; y++ {
			i := img.PixOffset(r.Min.X, y)
			out = append(out, img.Pix[i:i+r.Dx()*4]...)
		}
		return out
	}
	nav := image.Rect(60, 12, 360, 65)
	actions := image.Rect(80, 480, 340, 545)
	hiddenNav, hiddenActions := pixels(nav), pixels(actions)
	ring, _ := tt.Find("倒计时圆环")
	capture := func(name string) {
		if dir := os.Getenv("UI_VISIBILITY_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			writePNG(t, filepath.Join(dir, name+".png"), tt.Image())
		}
	}
	capture("clean")
	tt.Move(210, 37)
	if bytes.Equal(hiddenNav, pixels(nav)) {
		t.Fatal("navigation did not reveal")
	}
	if !bytes.Equal(hiddenActions, pixels(actions)) {
		t.Fatal("navigation revealed unrelated actions")
	}
	capture("navigation")
	tt.Move(210, 513)
	if !bytes.Equal(hiddenNav, pixels(nav)) {
		t.Fatal("navigation did not hide")
	}
	if bytes.Equal(hiddenActions, pixels(actions)) {
		t.Fatal("actions did not reveal")
	}
	capture("actions")
	if err := tt.Click("开始"); err != nil {
		t.Fatal(err)
	}
	if app.snapshot().Phase != PhaseFocus {
		t.Fatal("revealed start did not work")
	}
	tt.Move(10, 300)
	if !bytes.Equal(hiddenActions, pixels(actions)) {
		t.Fatal("mouse focus pinned actions visible")
	}
	capture("running-clean")
	after, _ := tt.Find("倒计时圆环")
	if ring != after {
		t.Fatalf("ring moved: %v -> %v", ring, after)
	}
	tt.Key(0, ui.KeyTab)
	if bytes.Equal(hiddenActions, pixels(actions)) && bytes.Equal(hiddenNav, pixels(nav)) {
		t.Fatal("keyboard focus did not reveal controls")
	}
	capture("keyboard-focus")
}
