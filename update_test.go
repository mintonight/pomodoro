package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// newUpdateTestApp builds an app whose updater is a fake: no feed is
// reached, the test decides what a check returns and how an install ends.
func newUpdateTestApp(t *testing.T) (*App, *updateFake) {
	t.Helper()
	app, _ := newTestApp(t)
	app.desktop = false
	fake := &updateFake{}
	app.updateNow = fake.check
	app.updateInstall = fake.install
	app.updateLaunch = func() { fake.launched = true }
	return app, fake
}

// updateFake stands in for mygo.Updater, which does nothing without an
// update feed baked in.
type updateFake struct {
	mu        sync.Mutex
	up        *mygo.Update
	err       error
	installFn func(ctx context.Context, progress func(downloaded, total int64)) error
	calls     int
	// slow blocks check until the test releases it.
	slow chan struct{}

	launched bool
}

func (f *updateFake) check(ctx context.Context) (*mygo.Update, error) {
	f.mu.Lock()
	f.calls++
	up, err, slow := f.up, f.err, f.slow
	f.mu.Unlock()
	if slow != nil {
		select {
		case <-slow:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return up, err
}

// install stands in for Update.Install; a test sets installFn, else it
// fails loudly.
func (f *updateFake) install(ctx context.Context, progress func(downloaded, total int64)) error {
	f.mu.Lock()
	fn := f.installFn
	f.mu.Unlock()
	if fn == nil {
		return errors.New("no install was staged")
	}
	return fn(ctx, progress)
}

// waitFor polls until cond passes, so a goroutine of the app can settle.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the condition never came true")
}

// TestQuietCheckFindsUpdate checks that the startup check finds a version
// and offers it, without the user asking.
func TestQuietCheckFindsUpdate(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	fake.mu.Lock()
	fake.up = &mygo.Update{Version: "0.3.0"}
	fake.mu.Unlock()

	app.checkForUpdates(false)

	state := app.updateStatus()
	if !state.New || state.Version != "0.3.0" {
		t.Errorf("state %+v, want an update 0.3.0", state)
	}
	if state.Status != "idle" {
		t.Errorf("status %q, want idle", state.Status)
	}
}

// TestQuietCheckIsSilentWhenUpToDate checks that the startup check does
// not tell the user the app is up to date; only a manual one does.
func TestQuietCheckIsSilentWhenUpToDate(t *testing.T) {
	app, _ := newUpdateTestApp(t)

	app.checkForUpdates(false)

	state := app.updateStatus()
	if state.New || state.Message != "" {
		t.Errorf("state %+v, want no news and no message", state)
	}
}

// TestManualCheckSaysUpToDate checks that a user-requested check reports
// being current.
func TestManualCheckSaysUpToDate(t *testing.T) {
	app, _ := newUpdateTestApp(t)

	app.checkForUpdates(true)

	state := app.updateStatus()
	if state.New || state.Message != "已是最新版本" {
		t.Errorf("state %+v, want up to date", state)
	}
}

// TestManualCheckError checks that a failed manual check shows why, while
// a quiet one stays quiet.
func TestManualCheckError(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	fake.mu.Lock()
	fake.err = errors.New("no network")
	fake.mu.Unlock()

	app.checkForUpdates(false)
	if s := app.updateStatus(); s.Status != "idle" && s.Message != "" {
		t.Errorf("quiet check made noise: %+v", s)
	}

	app.checkForUpdates(true)
	s := app.updateStatus()
	if s.Status != "error" || s.Message == "" {
		t.Errorf("state %+v, want an error with a message", s)
	}
}

// TestCheckRunsOnce checks that a check under way is not started again.
func TestCheckRunsOnce(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	fake.mu.Lock()
	fake.slow = make(chan struct{})
	fake.mu.Unlock()

	done := make(chan struct{})
	go func() { app.checkForUpdates(true); close(done) }()
	waitFor(t, func() bool { return app.updateStatus().Status == "checking" })

	app.checkForUpdates(true) // must not start a second one

	fake.mu.Lock()
	fake.slow <- struct{}{}
	fake.mu.Unlock()
	<-done

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("the feed was asked %d times, want 1", calls)
	}
}

// TestInstallShowsProgressAndReady checks the download state machine: the
// progress lands in the state, and a good install offers the relaunch.
func TestInstallShowsProgressAndReady(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	fake.mu.Lock()
	fake.up = &mygo.Update{Version: "0.3.0"}
	fake.installFn = func(ctx context.Context, progress func(downloaded, total int64)) error {
		progress(50, 100)
		return nil
	}
	fake.mu.Unlock()

	app.checkForUpdates(false)
	app.installUpdate()

	waitFor(t, func() bool { return app.updateStatus().Status == "ready" })
	if s := app.updateStatus(); s.Version != "0.3.0" {
		t.Errorf("state %+v, want version 0.3.0 kept", s)
	}
}

// TestInstallError checks a failed install lands in the error state.
func TestInstallError(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	fake.mu.Lock()
	fake.up = &mygo.Update{Version: "0.3.0"}
	fake.installFn = func(ctx context.Context, progress func(downloaded, total int64)) error {
		return errors.New("disk full")
	}
	fake.mu.Unlock()

	app.checkForUpdates(false)
	app.installUpdate()

	waitFor(t, func() bool { return app.updateStatus().Status == "error" })
	if s := app.updateStatus(); s.Message == "" {
		t.Error("the error carries no message")
	}
}

// TestCancelDownload checks that canceling a download leaves the update
// offered, so the user can try again.
func TestCancelDownload(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	release := make(chan struct{})
	fake.mu.Lock()
	fake.up = &mygo.Update{Version: "0.3.0"}
	fake.installFn = func(ctx context.Context, progress func(downloaded, total int64)) error {
		<-ctx.Done()
		return ctx.Err()
	}
	fake.mu.Unlock()

	app.checkForUpdates(false)
	app.installUpdate()
	waitFor(t, func() bool { return app.updateStatus().Status == "downloading" })

	app.cancelUpdate()
	close(release)
	waitFor(t, func() bool { return app.updateStatus().Status == "idle" })
}

// TestRelaunchUsesTheSeam checks the restart button goes through
// updateLaunch, which the app points at mygo.App.Relaunch.
func TestRelaunchUsesTheSeam(t *testing.T) {
	app, fake := newUpdateTestApp(t)
	fake.mu.Lock()
	fake.up = &mygo.Update{Version: "0.3.0"}
	fake.mu.Unlock()

	app.checkForUpdates(false)
	app.relaunchUpdate()

	if !fake.launched {
		t.Error("the relaunch never happened")
	}
}

// TestSetUpdatePersists checks the auto-update switch reaches the store.
func TestSetUpdatePersists(t *testing.T) {
	app, _ := newUpdateTestApp(t)

	app.setUpdate(false)
	if got := app.launchSetting(); got {
		t.Log("launch setting follows the same path; fine")
	}
	settings, err := app.store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AutoUpdate {
		t.Error("auto update is still on after turning it off")
	}
	if app.updateStatus().Status == "" {
		t.Error("the state was never initialized")
	}
}

// TestUpdateMessageKeepsTheCause checks the error text is shortened to
// its cause, and the disabled case is translated.
func TestUpdateMessageKeepsTheCause(t *testing.T) {
	if got := updateMessage(errors.New("mygo: checking for updates: GET https://x: 404 Not Found")); got != "404 Not Found" {
		t.Errorf("updateMessage = %q", got)
	}
	if got := updateMessage(mygo.ErrUpdatesDisabled); got != "此版本不支持自动更新" {
		t.Errorf("updateMessage = %q", got)
	}
}
