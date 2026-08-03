package process

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/cloudflared"
	"github.com/deifos/cfdev/internal/config"
)

func TestForegroundWritersHideDetailsUnlessVerbose(t *testing.T) {
	var logOutput bytes.Buffer
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	quietStdout, quietStderr := foregroundWriters(&logOutput, &stdout, &stderr, false)
	_, _ = quietStdout.Write([]byte("normal detail\n"))
	_, _ = quietStderr.Write([]byte("error detail\n"))
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatal("default mode streamed cloudflared details")
	}
	if got := logOutput.String(); got != "normal detail\nerror detail\n" {
		t.Fatalf("default log output = %q", got)
	}

	logOutput.Reset()
	verboseStdout, verboseStderr := foregroundWriters(&logOutput, &stdout, &stderr, true)
	_, _ = verboseStdout.Write([]byte("visible normal\n"))
	_, _ = verboseStderr.Write([]byte("visible error\n"))
	if stdout.String() != "visible normal\n" || stderr.String() != "visible error\n" {
		t.Fatalf("verbose output was not streamed: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if got := logOutput.String(); got != "visible normal\nvisible error\n" {
		t.Fatalf("verbose log output = %q", got)
	}
}

func TestRemoveStateOnlyRemovesMatchingProcess(t *testing.T) {
	t.Setenv("CFDEV_HOME", t.TempDir())
	paths, _ := config.ResolvePaths()
	manager := Manager{Paths: paths, Client: &cloudflared.Client{Binary: "cloudflared"}}
	if err := manager.writeState(State{PID: 100, Binary: "cloudflared"}); err != nil {
		t.Fatal(err)
	}
	manager.removeStateIfPID(200)
	if _, err := os.Stat(paths.Process); err != nil {
		t.Fatalf("mismatched process state was removed: %v", err)
	}
	manager.removeStateIfPID(100)
	if _, err := os.Stat(paths.Process); !os.IsNotExist(err) {
		t.Fatalf("matching process state remains: %v", err)
	}
}

func TestManagerRecognizesAndStopsItsExactProcess(t *testing.T) {
	var binary string
	var args []string
	if runtime.GOOS == "windows" {
		binary, _ = exec.LookPath("ping.exe")
		args = []string{"127.0.0.1", "-n", "30"}
	} else {
		binary, _ = exec.LookPath("sleep")
		args = []string{"30"}
	}
	if binary == "" {
		t.Skip("no suitable long-running test process")
	}
	command := exec.Command(binary, args...)
	configureBackground(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	t.Setenv("CFDEV_HOME", t.TempDir())
	paths, _ := config.ResolvePaths()
	manager := Manager{Paths: paths, Client: &cloudflared.Client{Binary: binary}}
	state := State{PID: command.Process.Pid, Binary: binary, StartedAt: time.Now().UTC(), Mode: "background", TunnelID: "test"}
	if err := manager.writeState(state); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if !status.Running || status.PID != command.Process.Pid {
		t.Fatalf("unexpected status: %#v", status)
	}
	stopped, err := manager.Stop()
	if err != nil || !stopped {
		t.Fatalf("Stop = %v, %v", stopped, err)
	}
	if alive(command.Process.Pid) {
		t.Fatal("managed process is still alive")
	}
	if _, err := os.Stat(paths.Process); !os.IsNotExist(err) {
		t.Fatalf("process state was not removed: %v", err)
	}
}
