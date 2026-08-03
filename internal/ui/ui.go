package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

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
	color := os.Getenv("NO_COLOR") == ""
	if file, ok := out.(*os.File); ok {
		if info, err := file.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
			color = false
		}
	} else {
		color = false
	}
	if options.JSON {
		color = false
	}
	return &UI{Out: out, Err: errOut, Options: options, Color: color}
}

func (ui *UI) Success(message string) { ui.Line(ui.Paint(green, "✓") + "  " + message) }
func (ui *UI) Info(message string)    { ui.Line(ui.Paint(cyan, "→") + "  " + message) }
func (ui *UI) Warning(message string) { ui.Line(ui.Paint(yellow, "!") + "  " + message) }
func (ui *UI) Heading(message string) { ui.Line(ui.Paint(bold, message)) }
func (ui *UI) Muted(message string)   { ui.Line(ui.Paint(dim, message)) }

func (ui *UI) Line(message string) {
	if !ui.Options.Quiet {
		fmt.Fprintln(ui.Out, message)
	}
}

func (ui *UI) Error(err *failure.Error) {
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
