package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// These checks exercise the actual native layout and input pipeline.
func TestTimerLayout(t *testing.T) {
	for _, width := range []int{360, 420, 600} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/dark=%v", width, dark), func(t *testing.T) {
				app, now := newTestApp(t)
				tt := ui.NewTester(app.view, width, 640)
				tt.SetDark(dark)
				tt.SetPreferences(ui.Preferences{ReduceMotion: true})
				center := func(label string, want float32) {
					t.Helper()
					r, ok := tt.Find(label)
					if !ok {
						t.Fatalf("missing %q", label)
					}
					if math.Abs(float64(r.X+r.W/2-want)) > 1 {
						t.Errorf("%s center = %v, want %v", label, r.X+r.W/2, want)
					}
				}
				for i, label := range []string{"计时", "统计", "设置"} {
					center(label, float32(width)/2+float32(i-1)*(280.0/3))
					if err := tt.Click(label); err != nil {
						t.Fatal(err)
					}
					if app.page != i {
						t.Fatalf("navigation %s selected %d", label, app.page)
					}
				}
				if err := tt.Click("计时"); err != nil {
					t.Fatal(err)
				}
				if err := tt.Click("开始"); err != nil {
					t.Fatal(err)
				}
				*now = now.Add(7 * time.Minute)
				tt.Frame()
				center("18:00", float32(width)/2)
				center("已完成 28%", float32(width)/2)
				if err := tt.Click("暂停"); err != nil {
					t.Fatal(err)
				}
				center("已暂停 不到 1 分钟", float32(width)/2)
				for i := 0; i < 4; i++ {
					if err := tt.Click("继续"); err != nil {
						t.Fatal(err)
					}
					if err := tt.Click("暂停"); err != nil {
						t.Fatal(err)
					}
				}
				if err := tt.Click("结束"); err != nil {
					t.Fatal(err)
				}
				if app.snapshot().Phase != PhaseIdle {
					t.Fatal("end did not return to idle")
				}
				center("25:00", float32(width)/2)
			})
		}
	}
}

// Optional evidence export uses the real renderer, not a web mockup.
func TestTimerMotionEvidence(t *testing.T) {
	dir := os.Getenv("UI_EVIDENCE_DIR")
	if dir == "" {
		t.Skip("set UI_EVIDENCE_DIR to export native frames")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	app, now := newTestApp(t)
	tt := ui.NewTester(app.view, 420, 640)
	capture := func(name string) { writePNG(t, filepath.Join(dir, name+".png"), tt.Image()) }
	advanceFrames := func(duration time.Duration) {
		until := time.Now().Add(duration)
		for time.Now().Before(until) {
			time.Sleep(12 * time.Millisecond)
			tt.Frame()
		}
	}
	capture("entry-start")
	advanceFrames(90 * time.Millisecond)
	capture("entry-mid")
	advanceFrames(300 * time.Millisecond)
	capture("idle")
	action := func(name, label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		capture(name + "-start")
		advanceFrames(75 * time.Millisecond)
		capture(name + "-mid")
		advanceFrames(300 * time.Millisecond)
		capture(name + "-end")
	}
	action("start", "开始")
	*now = now.Add(7 * time.Minute)
	tt.Frame()
	advanceFrames(300 * time.Millisecond)
	capture("focus")
	action("pause", "暂停")
	action("resume", "继续")
	action("cancel", "结束")
	action("navigation", "统计")
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	advanceFrames(300 * time.Millisecond)
	capture("settings")
	if err := tt.Click("计时"); err != nil {
		t.Fatal(err)
	}
	tt.SetDark(true)
	advanceFrames(300 * time.Millisecond)
	capture("dark-idle")
	tt.SetScale(2)
	capture("idle-200-percent")
}
