package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/deifos/cfdev/internal/cli"
	"github.com/deifos/cfdev/internal/failure"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[31m"
	cyan   = "\x1b[36m"
)

type UI struct {
	Out     io.Writer
	Err     io.Writer
	Options cli.Options
	Color   bool
	Animate bool
	mu      sync.Mutex
}

var (
	spinnerDelay    = 120 * time.Millisecond
	spinnerInterval = 80 * time.Millisecond
)

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Progress provides delayed terminal feedback for operations that may take a
// moment. It falls back to one static line outside an interactive terminal.
type Progress struct {
	ui        *UI
	message   string
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	frame     int
	lastWidth int
	maxWidth  int
	active    bool
	shown     bool
}

type envelope struct {
	OK      bool       `json:"ok"`
	Data    any        `json:"data"`
	Summary string     `json:"summary"`
	Error   *errorBody `json:"error,omitempty"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func New(out, errOut io.Writer, options cli.Options) *UI {
	terminal := false
	if file, ok := out.(*os.File); ok {
		info, err := file.Stat()
		terminal = err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	color := terminal && os.Getenv("NO_COLOR") == "" && !options.JSON
	animate := animationEnabled(terminal, options)
	return &UI{Out: out, Err: errOut, Options: options, Color: color, Animate: animate}
}

func animationEnabled(terminal bool, options cli.Options) bool {
	return terminal && !options.JSON && !options.Quiet && !options.Verbose &&
		os.Getenv("CFDEV_NO_SPINNER") == "" && os.Getenv("TERM") != "dumb" && os.Getenv("CI") == ""
}

func (ui *UI) Success(message string) { ui.Line(ui.Paint(green, "✓") + "  " + message) }
func (ui *UI) Info(message string)    { ui.Line(ui.Paint(cyan, "→") + "  " + message) }
func (ui *UI) Warning(message string) { ui.Line(ui.Paint(yellow, "!") + "  " + message) }
func (ui *UI) Heading(message string) { ui.Line(ui.Paint(bold, message)) }
func (ui *UI) Muted(message string)   { ui.Line(ui.Paint(dim, message)) }

// Request prints one compact, terminal-safe line for a completed proxied
// request. Query parameters, headers, and bodies remain in the inspector UI.
func (ui *UI) Request(completedAt time.Time, method, path string, status int, duration time.Duration, target string, replay bool) {
	method = terminalText(method, 12)
	path = terminalRequestPath(path, 72)
	target = terminalText(strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://"), 48)
	target = strings.Replace(target, "127.0.0.1", "localhost", 1)
	statusText := fmt.Sprint(status)
	switch {
	case status >= 400:
		statusText = ui.Paint(red, statusText)
	case status >= 300:
		statusText = ui.Paint(dim, statusText)
	case status >= 200:
		statusText = ui.Paint(green, statusText)
	default:
		statusText = ui.Paint(dim, statusText)
	}
	suffix := ""
	if replay {
		suffix = ui.Paint(dim, "  replay")
	}
	ui.Line(fmt.Sprintf("%s  %s  %s  %s  %s  → %s%s",
		ui.Paint(dim, completedAt.Local().Format("15:04:05")),
		ui.Paint(cyan, fmt.Sprintf("%-7s", method)), path, statusText, requestDuration(duration), target, suffix))
}

func terminalRequestPath(value string, limit int) string {
	if index := strings.IndexByte(value, '?'); index >= 0 {
		value = value[:index]
	}
	if value == "" {
		value = "/"
	}
	return terminalText(value, limit)
}

func terminalText(value string, limit int) string {
	value = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f || (character >= 0x80 && character <= 0x9f) {
			return '�'
		}
		return character
	}, value)
	runes := []rune(value)
	if limit > 3 && len(runes) > limit {
		return string(runes[:limit-3]) + "..."
	}
	return value
}

func requestDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Round(time.Millisecond)/time.Millisecond)
	}
	return fmt.Sprintf("%.1fs", duration.Seconds())
}

func (ui *UI) Line(message string) {
	if !ui.Options.Quiet {
		ui.mu.Lock()
		defer ui.mu.Unlock()
		fmt.Fprintln(ui.Out, message)
	}
}

func (ui *UI) Error(err *failure.Error) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if ui.Options.JSON {
		_ = json.NewEncoder(ui.Out).Encode(envelope{
			OK: false, Data: err.Data, Summary: err.Message,
			Error: &errorBody{Code: err.Code, Message: err.Message, Hint: err.Hint},
		})
		return
	}
	fmt.Fprintln(ui.Err, ui.Paint(red, "✗")+"  "+err.Message)
	if err.Hint != "" {
		fmt.Fprintln(ui.Err, ui.Paint(dim, "   "+err.Hint))
	}
}

func (ui *UI) Result(ok bool, data any, summary string) {
	if ui.Options.JSON {
		_ = json.NewEncoder(ui.Out).Encode(envelope{OK: ok, Data: data, Summary: summary})
		return
	}
	if summary != "" {
		ui.Success(summary)
	}
}

func (ui *UI) Paint(code, value string) string {
	if !ui.Color {
		return value
	}
	return code + value + reset
}

func (ui *UI) Green(value string) string  { return ui.Paint(green, value) }
func (ui *UI) Yellow(value string) string { return ui.Paint(yellow, value) }
func (ui *UI) Dim(value string) string    { return ui.Paint(dim, value) }
func (ui *UI) Bold(value string) string   { return ui.Paint(bold, value) }

// Progress starts concise feedback for a potentially slow operation. Animated
// output is reserved for interactive terminals so pipes, logs, agents, and JSON
// retain stable line-oriented output.
func (ui *UI) Progress(message string) *Progress {
	progress := &Progress{ui: ui, message: message}
	if ui.Options.JSON || ui.Options.Quiet {
		return progress
	}
	if !ui.Animate {
		ui.Info(message)
		return progress
	}
	progress.active = true
	progress.stop = make(chan struct{})
	progress.done = make(chan struct{})
	go progress.run()
	return progress
}

// Update changes the progress message without adding another terminal line.
func (progress *Progress) Update(message string) {
	if progress == nil || !progress.active {
		return
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()
	progress.message = message
	if progress.shown {
		progress.renderLocked()
	}
}

// Stop clears animated progress. It is safe to call more than once.
func (progress *Progress) Stop() {
	if progress == nil || !progress.active {
		return
	}
	progress.once.Do(func() {
		close(progress.stop)
		<-progress.done
		progress.clear()
	})
}

func (progress *Progress) run() {
	defer close(progress.done)
	delay := time.NewTimer(spinnerDelay)
	defer delay.Stop()
	select {
	case <-progress.stop:
		return
	case <-delay.C:
		progress.mu.Lock()
		progress.shown = true
		progress.renderLocked()
		progress.mu.Unlock()
	}

	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-progress.stop:
			return
		case <-ticker.C:
			progress.mu.Lock()
			progress.frame = (progress.frame + 1) % len(spinnerFrames)
			progress.renderLocked()
			progress.mu.Unlock()
		}
	}
}

func (progress *Progress) renderLocked() {
	line := spinnerFrames[progress.frame] + "  " + progress.message
	width := utf8.RuneCountInString(line)
	padding := progress.lastWidth - width
	if padding < 0 {
		padding = 0
	}
	progress.ui.mu.Lock()
	_, _ = fmt.Fprint(progress.ui.Out, "\r", progress.ui.Paint(cyan, spinnerFrames[progress.frame]), "  ", progress.message, strings.Repeat(" ", padding))
	progress.ui.mu.Unlock()
	progress.lastWidth = width
	if width > progress.maxWidth {
		progress.maxWidth = width
	}
}

func (progress *Progress) clear() {
	progress.mu.Lock()
	if !progress.shown {
		progress.mu.Unlock()
		return
	}
	width := progress.maxWidth
	progress.shown = false
	progress.mu.Unlock()

	progress.ui.mu.Lock()
	_, _ = fmt.Fprint(progress.ui.Out, "\r", strings.Repeat(" ", width), "\r")
	progress.ui.mu.Unlock()
}
