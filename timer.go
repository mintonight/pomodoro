package main

import "time"

// Phase is where the timer is.
type Phase int

// The four phases of the app.
const (
	PhaseIdle Phase = iota
	PhaseFocus
	PhasePaused
	PhaseBreak
)

// String returns the phase's name, for the tray and for tests.
func (p Phase) String() string {
	switch p {
	case PhaseFocus:
		return "focus"
	case PhasePaused:
		return "paused"
	case PhaseBreak:
		return "break"
	default:
		return "idle"
	}
}

// PauseTimeout is how long a focus may stay paused before it is cancelled.
const PauseTimeout = 30 * time.Minute

// EventKind is what happened when the clock moved on.
type EventKind int

// The events Advance reports.
const (
	// EventFocusCompleted is a focus that ran to its end.
	EventFocusCompleted EventKind = iota
	// EventBreakCompleted is a break that ran to its end.
	EventBreakCompleted
	// EventFocusCancelled is a focus given up because it was paused for
	// longer than PauseTimeout. It is not recorded anywhere.
	EventFocusCancelled
)

// Event is one transition the clock brought about.
type Event struct {
	Kind EventKind
	// StartedAt and CompletedAt are the wall-clock times the focus began and
	// the moment it was due to finish, for a completed focus.
	StartedAt   time.Time
	CompletedAt time.Time
	// Focused is the focused time of a completed focus, which the pauses
	// within it do not count toward.
	Focused time.Duration
	// Cancelled is the pause that was too long, for EventFocusCancelled.
	Cancelled time.Duration
}

// Snapshot is what a frame needs to draw the timer.
type Snapshot struct {
	Phase Phase
	// Total is the whole period being counted: the focus or the break.
	Total time.Duration
	// Remaining counts down to the end of the focus or the break. While
	// paused it stays where the pause stopped it, as the period is neither
	// running nor over.
	Remaining time.Duration
	// Focused is the focused time so far, which pauses do not count toward.
	Focused time.Duration
	// PausedFor is how long the focus has been paused.
	PausedFor time.Duration
}

// Progress is the share of the period that is done, between 0 and 1.
func (s Snapshot) Progress() float32 {
	if s.Total <= 0 {
		return 0
	}
	done := s.Total - s.Remaining
	if done < 0 {
		done = 0
	}
	if done > s.Total {
		done = s.Total
	}
	return float32(done) / float32(s.Total)
}

// Engine is the timer. It knows only the real time: every question about the
// clock takes a time.Time, and the state follows from it. Nothing counts
// down second by second, so a slow frame, a sleeping computer or a hidden
// window cannot make the count drift.
//
// Engine is not safe for concurrent use; the app guards it with a mutex.
type Engine struct {
	phase Phase

	// The periods of the current round, taken from the settings when the
	// round begins so that changing them does not disturb it.
	focusPeriod time.Duration
	breakPeriod time.Duration

	// The current pomodoro.
	startedAt time.Time
	focused   time.Duration // focused time before the current run began
	runStart  time.Time     // when the current run (focus or break) began

	// The current pause.
	pauseStart time.Time
}

// StartFocus begins a focus of the given lengths.
func (e *Engine) StartFocus(now time.Time, focus, brk time.Duration) {
	e.phase = PhaseFocus
	e.focusPeriod = focus
	e.breakPeriod = brk
	e.startedAt = now
	e.focused = 0
	e.runStart = now
	e.pauseStart = time.Time{}
}

// Pause pauses a running focus. The paused time does not count toward the
// focus, and the focus is cancelled if the pause lasts longer than
// PauseTimeout.
func (e *Engine) Pause(now time.Time) {
	if e.phase != PhaseFocus {
		return
	}
	e.focused += now.Sub(e.runStart)
	e.phase = PhasePaused
	e.pauseStart = now
}

// Resume continues a paused focus, moving its end later by the pause.
func (e *Engine) Resume(now time.Time) {
	if e.phase != PhasePaused {
		return
	}
	e.runStart = now
	e.pauseStart = time.Time{}
	e.phase = PhaseFocus
}

// EndFocus gives up the current focus, whether it is running or paused. It is
// not recorded.
func (e *Engine) EndFocus() {
	e.phase = PhaseIdle
	e.startedAt = time.Time{}
	e.focused = 0
	e.runStart = time.Time{}
	e.pauseStart = time.Time{}
}

// EndBreak gives up the current break.
func (e *Engine) EndBreak() {
	if e.phase != PhaseBreak {
		return
	}
	e.phase = PhaseIdle
	e.runStart = time.Time{}
}

// Snapshot tells where the timer is at now.
func (e *Engine) Snapshot(now time.Time) Snapshot {
	switch e.phase {
	case PhaseFocus:
		focused := e.focused + now.Sub(e.runStart)
		return Snapshot{
			Phase:     PhaseFocus,
			Total:     e.focusPeriod,
			Remaining: e.focusPeriod - focused,
			Focused:   focused,
		}
	case PhasePaused:
		// Pause moved the time focused so far into e.focused, so it stands
		// still while the pause lasts.
		return Snapshot{
			Phase:     PhasePaused,
			Total:     e.focusPeriod,
			Remaining: e.focusPeriod - e.focused,
			Focused:   e.focused,
			PausedFor: now.Sub(e.pauseStart),
		}
	case PhaseBreak:
		elapsed := now.Sub(e.runStart)
		return Snapshot{
			Phase:     PhaseBreak,
			Total:     e.breakPeriod,
			Remaining: e.breakPeriod - elapsed,
		}
	default:
		return Snapshot{Phase: PhaseIdle}
	}
}

// focusDeadline is when the running focus ends.
func (e *Engine) focusDeadline() time.Time {
	return e.runStart.Add(e.focusPeriod - e.focused)
}

// breakDeadline is when the running break ends.
func (e *Engine) breakDeadline() time.Time {
	return e.runStart.Add(e.breakPeriod)
}

// pauseDeadline is when a pause becomes too long.
func (e *Engine) pauseDeadline() time.Time {
	return e.pauseStart.Add(PauseTimeout)
}

// NextDeadline is when the timer next has something to do on its own: the end
// of the focus or the break, or the pause growing too long. It reports false
// while nothing is running.
func (e *Engine) NextDeadline(now time.Time) (time.Time, bool) {
	switch e.phase {
	case PhaseFocus:
		return e.focusDeadline(), true
	case PhasePaused:
		return e.pauseDeadline(), true
	case PhaseBreak:
		return e.breakDeadline(), true
	default:
		return time.Time{}, false
	}
}

// Advance moves the timer to now and reports what happened on the way, oldest
// first. The period that elapsed while the computer was asleep is real: a
// focus whose end has passed is complete, and if a break that followed it is
// over too, the timer arrives at Idle in one call, with both events.
func (e *Engine) Advance(now time.Time) []Event {
	var events []Event
	for {
		switch e.phase {
		case PhaseFocus:
			deadline := e.focusDeadline()
			if now.Before(deadline) {
				return events
			}
			events = append(events, Event{
				Kind:        EventFocusCompleted,
				StartedAt:   e.startedAt,
				CompletedAt: deadline,
				Focused:     e.focusPeriod,
			})
			// The break begins the moment the focus was due to end, not
			// when we noticed: a sleeping computer must not shift it.
			e.phase = PhaseBreak
			e.runStart = deadline

		case PhaseBreak:
			deadline := e.breakDeadline()
			if now.Before(deadline) {
				return events
			}
			events = append(events, Event{
				Kind:        EventBreakCompleted,
				CompletedAt: deadline,
			})
			e.phase = PhaseIdle
			e.runStart = time.Time{}
			return events

		case PhasePaused:
			deadline := e.pauseDeadline()
			if now.Before(deadline) {
				return events
			}
			events = append(events, Event{
				Kind:      EventFocusCancelled,
				Cancelled: now.Sub(e.pauseStart),
			})
			e.EndFocus()
			return events

		default:
			return events
		}
	}
}
