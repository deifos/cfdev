package process

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/deifos/cfdev/internal/cloudflared"
	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/failure"
)

type State struct {
	PID       int       `json:"pid"`
	Binary    string    `json:"binary"`
	StartedAt time.Time `json:"started_at"`
	Mode      string    `json:"mode"`
	TunnelID  string    `json:"tunnel_id"`
}

type Status struct {
	Running   bool      `json:"running"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Mode      string    `json:"mode,omitempty"`
}

type Manager struct {
	Paths  config.Paths
	Client *cloudflared.Client
}

func (manager Manager) Status() Status {
	state, err := manager.readState()
	if err != nil || state.PID <= 0 {
		return Status{}
	}
	if !alive(state.PID) || !processMatches(state.PID, firstNonEmpty(state.Binary, manager.Client.Binary)) {
		_ = os.Remove(manager.Paths.Process)
		return Status{}
	}
	return Status{Running: true, PID: state.PID, StartedAt: state.StartedAt, Mode: state.Mode}
}

func (manager Manager) StartBackground(cfg *config.Config) (Status, bool, error) {
	if current := manager.Status(); current.Running {
		return current, true, nil
	}
	logFile, err := manager.openLog()
	if err != nil {
		return Status{}, false, err
	}
	command := exec.Command(manager.Client.Binary, manager.runArgs(cfg)...)
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = os.Environ()
	configureBackground(command)
	if err := command.Start(); err != nil {
		logFile.Close()
		return Status{}, false, failure.Wrap("TUNNEL_START_FAILED", "could not start cloudflared", failure.ExitGeneral, err)
	}
	state := State{
		PID: command.Process.Pid, Binary: manager.Client.Binary, StartedAt: time.Now().UTC(), Mode: "background", TunnelID: cfg.TunnelID,
	}
	if err := manager.writeState(state); err != nil {
		_ = command.Process.Kill()
		logFile.Close()
		return Status{}, false, err
	}
	_ = command.Process.Release()
	_ = logFile.Close()
	time.Sleep(500 * time.Millisecond)
	if !alive(state.PID) {
		_ = os.Remove(manager.Paths.Process)
		detail := tail(manager.Paths.Log, 4)
		message := "the tunnel stopped while starting"
		if detail != "" {
			message += ": " + detail
		}
		err := failure.New("TUNNEL_START_FAILED", message, failure.ExitGeneral)
		err.Hint = "Run `cfdev doctor`, then inspect the background log."
		return Status{}, false, err
	}
	return Status{Running: true, PID: state.PID, StartedAt: state.StartedAt, Mode: state.Mode}, false, nil
}

func (manager Manager) StartForeground(cfg *config.Config, stdin io.Reader, stdout, stderr io.Writer, verbose bool, onStarted func(Status)) (Status, bool, error) {
	if current := manager.Status(); current.Running {
		return current, true, nil
	}
	logFile, err := manager.openLog()
	if err != nil {
		return Status{}, false, err
	}
	defer logFile.Close()
	processStdout, processStderr := foregroundWriters(logFile, stdout, stderr, verbose)
	command := exec.Command(manager.Client.Binary, manager.runArgs(cfg)...)
	command.Stdin = stdin
	command.Stdout = processStdout
	command.Stderr = processStderr
	command.Env = os.Environ()
	if err := command.Start(); err != nil {
		return Status{}, false, failure.Wrap("TUNNEL_START_FAILED", "could not start cloudflared", failure.ExitGeneral, err)
	}
	state := State{
		PID: command.Process.Pid, Binary: manager.Client.Binary, StartedAt: time.Now().UTC(), Mode: "foreground", TunnelID: cfg.TunnelID,
	}
	if err := manager.writeState(state); err != nil {
		_ = command.Process.Kill()
		return Status{}, false, err
	}
	defer manager.removeStateIfPID(state.PID)
	startedStatus := Status{Running: true, PID: state.PID, StartedAt: state.StartedAt, Mode: state.Mode}
	if onStarted != nil {
		onStarted(startedStatus)
	}

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChannel)
	var interrupted atomic.Bool
	done := make(chan struct{})
	go func() {
		select {
		case received := <-signalChannel:
			interrupted.Store(true)
			_ = command.Process.Signal(received)
		case <-done:
		}
	}()
	err = command.Wait()
	close(done)
	_, stateErr := os.Stat(manager.Paths.Process)
	stoppedExternally := os.IsNotExist(stateErr)
	if err != nil && !interrupted.Load() && !stoppedExternally {
		return Status{}, false, failure.Wrap("TUNNEL_EXITED", "cloudflared exited unexpectedly", failure.ExitGeneral, err)
	}
	return Status{Running: false, PID: state.PID, StartedAt: state.StartedAt, Mode: state.Mode}, false, nil
}

func (manager Manager) removeStateIfPID(pid int) {
	state, err := manager.readState()
	if err == nil && state.PID == pid {
		_ = os.Remove(manager.Paths.Process)
	}
}

func (manager Manager) openLog() (*os.File, error) {
	if err := rotateLog(manager.Paths.Log); err != nil {
		return nil, failure.Wrap("TUNNEL_START_FAILED", "could not rotate the tunnel log", failure.ExitGeneral, err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.Paths.Log), 0o700); err != nil {
		return nil, failure.Wrap("TUNNEL_START_FAILED", "could not create the tunnel log directory", failure.ExitGeneral, err)
	}
	logFile, err := os.OpenFile(manager.Paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, failure.Wrap("TUNNEL_START_FAILED", "could not open the tunnel log", failure.ExitGeneral, err)
	}
	return logFile, nil
}

func foregroundWriters(logWriter, stdout, stderr io.Writer, verbose bool) (io.Writer, io.Writer) {
	if !verbose {
		return logWriter, logWriter
	}
	return io.MultiWriter(stdout, logWriter), io.MultiWriter(stderr, logWriter)
}

func (manager Manager) Stop() (bool, error) {
	state, err := manager.readState()
	if err != nil || state.PID <= 0 || !alive(state.PID) {
		_ = os.Remove(manager.Paths.Process)
		_ = os.Remove(manager.Paths.ConnectorPID)
		return false, nil
	}
	if !processMatches(state.PID, firstNonEmpty(state.Binary, manager.Client.Binary)) {
		_ = os.Remove(manager.Paths.Process)
		err := failure.New("STALE_PROCESS_STATE", "the saved process ID belongs to another program, so cfdev left it alone", failure.ExitConflict)
		err.Hint = "The stale state was cleared; run `cfdev up` again."
		return false, err
	}
	if err := terminate(state.PID, 3*time.Second, state.Mode == "background"); err != nil {
		return false, failure.Wrap("TUNNEL_STOP_FAILED", "could not stop cloudflared", failure.ExitGeneral, err)
	}
	_ = os.Remove(manager.Paths.Process)
	_ = os.Remove(manager.Paths.ConnectorPID)
	return true, nil
}

func (manager Manager) RestartBackground(cfg *config.Config) (bool, error) {
	current := manager.Status()
	if !current.Running || current.Mode != "background" {
		return false, nil
	}
	if _, err := manager.Stop(); err != nil {
		return false, err
	}
	_, _, err := manager.StartBackground(cfg)
	return err == nil, err
}

func (manager Manager) runArgs(cfg *config.Config) []string {
	return []string{
		"tunnel", "--no-autoupdate",
		"--config", manager.Paths.Ingress,
		"--pidfile", manager.Paths.ConnectorPID,
		"run", cfg.TunnelID,
	}
}

func (manager Manager) readState() (State, error) {
	contents, err := os.ReadFile(manager.Paths.Process)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(contents, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (manager Manager) writeState(state State) error {
	if err := os.MkdirAll(filepath.Dir(manager.Paths.Process), 0o700); err != nil {
		return err
	}
	contents, _ := json.MarshalIndent(state, "", "  ")
	contents = append(contents, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(manager.Paths.Process), ".process-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	_ = os.Remove(manager.Paths.Process)
	return os.Rename(name, manager.Paths.Process)
}

func waitUntilStopped(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !alive(pid)
}

func rotateLog(logPath string) error {
	info, err := os.Stat(logPath)
	if os.IsNotExist(err) || (err == nil && info.Size() < 2<<20) {
		return nil
	}
	if err != nil {
		return err
	}
	rotated := logPath + ".1"
	_ = os.Remove(rotated)
	return os.Rename(logPath, rotated)
}

func tail(logPath string, count int) string {
	contents, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	lines := strings.FieldsFunc(string(contents), func(char rune) bool { return char == '\n' || char == '\r' })
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "cloudflared"
}
