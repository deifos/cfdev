package ui

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/cli"
)

func TestProgressUsesOneStaticLineOutsideTerminal(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, &output, cli.Options{})
	progress := view.Progress("Removing demo.example.com…")
	progress.Update("Reloading the tunnel…")
	progress.Stop()

	if got, want := output.String(), "→  Removing demo.example.com…\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRequestFeedIsCompactAndOmitsQueryParameters(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, &output, cli.Options{})
	completedAt := time.Date(2026, time.August, 3, 19, 55, 7, 0, time.Local)
	view.Request(completedAt, "GET", "/hooks/stripe?token=do-not-print", http.StatusNoContent, 18*time.Millisecond, "http://127.0.0.1:3000", false)

	if got, want := output.String(), "19:55:07  GET      /hooks/stripe  204  18ms  → localhost:3000\n"; got != want {
		t.Fatalf("request output = %q, want %q", got, want)
	}
}

func TestRequestFeedSanitizesTerminalControlCharacters(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, &output, cli.Options{})
	view.Request(time.Date(2026, time.August, 3, 19, 55, 7, 0, time.Local), "POST\x1b[31m", "/hook\nforged", http.StatusBadGateway, 500*time.Microsecond, "http://127.0.0.1:3000", true)

	got := output.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\nforged") || !strings.Contains(got, "POST�[31m") || !strings.Contains(got, "/hook�forged") || !strings.Contains(got, "<1ms") || !strings.Contains(got, "replay") {
		t.Fatalf("request output was not safely formatted: %q", got)
	}
}

func TestRequestFeedRespectsQuietMode(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, &output, cli.Options{Quiet: true})
	view.Request(time.Now(), "GET", "/", http.StatusOK, time.Millisecond, "http://127.0.0.1:3000", false)
	if output.Len() != 0 {
		t.Fatalf("quiet request feed output = %q", output.String())
	}
}

func TestRequestFeedUsesConsistentStatusColors(t *testing.T) {
	var output bytes.Buffer
	view := &UI{Out: &output, Err: &output, Color: true}
	completedAt := time.Date(2026, time.August, 3, 19, 55, 7, 0, time.Local)
	view.Request(completedAt, "GET", "/ok", http.StatusOK, time.Millisecond, "http://127.0.0.1:3000", false)
	view.Request(completedAt, "GET", "/moved", http.StatusFound, time.Millisecond, "http://127.0.0.1:3000", false)
	view.Request(completedAt, "GET", "/missing", http.StatusNotFound, time.Millisecond, "http://127.0.0.1:3000", false)

	got := output.String()
	if !strings.Contains(got, green+"200"+reset) || !strings.Contains(got, dim+"302"+reset) || !strings.Contains(got, red+"404"+reset) {
		t.Fatalf("status colors do not match the UI language: %q", got)
	}
}

func TestProgressStaysSilentForStructuredAndQuietOutput(t *testing.T) {
	for _, options := range []cli.Options{{JSON: true}, {Quiet: true}} {
		var output bytes.Buffer
		view := &UI{Out: &output, Err: &output, Options: options, Animate: true}
		progress := view.Progress("Removing demo.example.com…")
		progress.Update("Reloading the tunnel…")
		progress.Stop()
		if output.Len() != 0 {
			t.Fatalf("options %#v produced progress output %q", options, output.String())
		}
	}
}

func TestAnimationRespectsTerminalAndOutputGuards(t *testing.T) {
	t.Setenv("CFDEV_NO_SPINNER", "")
	t.Setenv("TERM", "")
	t.Setenv("CI", "")
	if !animationEnabled(true, cli.Options{}) {
		t.Fatal("interactive terminal should animate by default")
	}
	for name, options := range map[string]cli.Options{
		"json": {JSON: true}, "quiet": {Quiet: true}, "verbose": {Verbose: true},
	} {
		if animationEnabled(true, options) {
			t.Fatalf("%s output should not animate", name)
		}
	}
	if animationEnabled(false, cli.Options{}) {
		t.Fatal("redirected output should not animate")
	}

	t.Setenv("CFDEV_NO_SPINNER", "1")
	if animationEnabled(true, cli.Options{}) {
		t.Fatal("CFDEV_NO_SPINNER should disable animation")
	}
	t.Setenv("CFDEV_NO_SPINNER", "")
	t.Setenv("TERM", "dumb")
	if animationEnabled(true, cli.Options{}) {
		t.Fatal("TERM=dumb should disable animation")
	}
	t.Setenv("TERM", "")
	t.Setenv("CI", "true")
	if animationEnabled(true, cli.Options{}) {
		t.Fatal("CI should disable animation")
	}
}

func TestProgressAnimatesUpdatesAndClearsOneTerminalLine(t *testing.T) {
	originalDelay, originalInterval := spinnerDelay, spinnerInterval
	spinnerDelay, spinnerInterval = 0, time.Hour
	defer func() { spinnerDelay, spinnerInterval = originalDelay, originalInterval }()

	var output bytes.Buffer
	view := &UI{Out: &output, Err: &output, Animate: true}
	progress := view.Progress("Removing demo.example.com…")
	time.Sleep(20 * time.Millisecond)
	progress.Update("Reloading the tunnel…")
	progress.Stop()
	progress.Stop()

	got := output.String()
	if !strings.Contains(got, "\r⠋  Removing demo.example.com…") {
		t.Fatalf("initial spinner frame missing from %q", got)
	}
	if !strings.Contains(got, "\r⠋  Reloading the tunnel…") {
		t.Fatalf("updated spinner message missing from %q", got)
	}
	if strings.Contains(got, "\n") || !strings.HasSuffix(got, "\r") {
		t.Fatalf("spinner did not remain on and clear one line: %q", got)
	}
}

func TestFastProgressDoesNotFlicker(t *testing.T) {
	originalDelay := spinnerDelay
	spinnerDelay = time.Hour
	defer func() { spinnerDelay = originalDelay }()

	var output bytes.Buffer
	view := &UI{Out: &output, Err: &output, Animate: true}
	progress := view.Progress("A fast operation…")
	progress.Stop()
	if output.Len() != 0 {
		t.Fatalf("fast progress should remain invisible, got %q", output.String())
	}
}
