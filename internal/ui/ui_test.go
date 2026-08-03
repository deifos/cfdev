package ui

import (
	"bytes"
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
