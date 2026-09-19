package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// fluidFrame is one frame of the status spinner: the mark it shows and how
// long it holds on screen.
type fluidFrame struct {
	// mark is the glyph sequence the frame shows.
	mark string

	// hold is how long the frame stays before the spinner advances to the
	// next one.
	hold time.Duration
}

// fluidFrames describe the Varavel fluid mark: three cells that roll between
// the ▀▄▀ and ▄▀▄ states through a flat ■■■ state. The rolled states hold
// longer than the flat transitions, so the mark reads as mass in motion while
// keeping a constant size through the whole cycle.
var fluidFrames = []fluidFrame{
	{mark: "▀▄▀", hold: 380 * time.Millisecond},
	{mark: "■■■", hold: 110 * time.Millisecond},
	{mark: "▄▀▄", hold: 380 * time.Millisecond},
	{mark: "■■■", hold: 110 * time.Millisecond},
}

// varavelLogo is the Varavel mark that opens the identity line of every phase.
// It is the first frame of the fluid cycle, kept static so the status line
// carries the only animation of the interface.
const varavelLogo = "▀▄▀"

// spinnerStartMsg arms the status spinner when a busy phase begins. It is
// delivered at once, unlike the tick that follows it, which waits for the hold
// of the frame on screen.
type spinnerStartMsg struct{}

// spinnerTickMsg advances the status spinner to its next frame.
type spinnerTickMsg struct{}

// spinner animates the Varavel fluid mark while a run is busy. Each frame holds
// for its own time, so the animation keeps the cadence of the mark instead of a
// single fixed rate.
type spinner struct {
	// index is the frame on screen.
	index int
}

// View returns the mark of the frame on screen.
func (s *spinner) View() string {
	return fluidFrames[s.index].mark
}

// reset returns the spinner to the first frame, so every animation starts on
// the same mark.
func (s *spinner) reset() {
	s.index = 0
}

// advance moves to the next frame, wrapping around the cycle.
func (s *spinner) advance() {
	s.index = (s.index + 1) % len(fluidFrames)
}

// command returns the command that advances the spinner once the frame on
// screen has held for its time.
func (s *spinner) command() tea.Cmd {
	return tea.Tick(fluidFrames[s.index].hold, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}
