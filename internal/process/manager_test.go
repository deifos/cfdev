package process

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/cloudflared"
	"github.com/deifos/cfdev/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("CFDEV_TEST_PROCESS_CHILD") == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("CFDEV_TEST_PROCESS_PARENT") == "1" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		command := exec.Command(executable)
		command.Env = append(os.Environ(), "CFDEV_TEST_PROCESS_CHILD=1")
		configureBackground(command)
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Println(command.Process.Pid)
		if err := command.Process.Release(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(binary)
	parent.Env = append(os.Environ(), "CFDEV_TEST_PROCESS_PARENT=1")
	output, err := parent.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("invalid background PID %q: %v", output, err)
	}
	defer func() {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
			_ = process.Release()
		}
	}()
	t.Setenv("CFDEV_HOME", t.TempDir())
	paths, _ := config.ResolvePaths()
	manager := Manager{Paths: paths, Client: &cloudflared.Client{Binary: binary}}
	state := State{PID: pid, Binary: binary, StartedAt: time.Now().UTC(), Mode: "background", TunnelID: "test"}
	if err := manager.writeState(state); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if !status.Running || status.PID != pid {
		t.Fatalf("unexpected status: %#v", status)
	}
	stopped, err := manager.Stop()
	if err != nil || !stopped {
		t.Fatalf("Stop = %v, %v", stopped, err)
	}
	if alive(pid) {
		t.Fatal("managed process is still alive")
	}
	if _, err := os.Stat(paths.Process); !os.IsNotExist(err) {
		t.Fatalf("process state was not removed: %v", err)
	}
}
