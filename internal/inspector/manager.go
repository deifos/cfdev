package inspector

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/failure"
)

const (
	UIAddress    = "127.0.0.1:4040"
	ProxyAddress = "127.0.0.1:4041"
	UIURL        = "http://127.0.0.1:4040"
)

type State struct {
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"started_at"`
}

type Status struct {
	Running       bool      `json:"running"`
	PID           int       `json:"pid,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CaptureBodies bool      `json:"capture_bodies"`
	URL           string    `json:"url"`
	Version       string    `json:"version,omitempty"`
}

func InspectStatus(paths config.Paths) Status {
	health, ok := health()
	if !ok || health.Home != paths.Home {
		return Status{URL: UIURL}
	}
	state, err := readState(paths)
	if err != nil {
		return Status{URL: UIURL}
	}
	return Status{Running: true, PID: state.PID, StartedAt: state.StartedAt, CaptureBodies: health.CaptureBodies, URL: UIURL, Version: health.Version}
}

func Ensure(paths config.Paths, captureBodies bool, version string) (Status, bool, error) {
	if os.Getenv("CFDEV_TEST_DISABLE_INSPECTOR") == "1" {
		return Status{URL: UIURL}, false, nil
	}
	if current := InspectStatus(paths); current.Running && current.Version == version {
		if captureBodies && !current.CaptureBodies {
			if err := setCapture(paths, true); err != nil {
				return current, true, err
			}
			current.CaptureBodies = true
		}
		return current, true, nil
	} else if current.Running {
		if _, err := Stop(paths); err != nil {
			return current, true, err
		}
	}
	_ = os.Remove(paths.Inspector)
	for _, address := range []string{UIAddress, ProxyAddress} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			failureErr := failure.Wrap("INSPECTOR_PORT_UNAVAILABLE", fmt.Sprintf("the local inspector cannot bind %s", address), failure.ExitConflict, err)
			failureErr.Hint = "Close the program using that loopback port, then run `cfdev up` again. The tunnel can still use direct routing without inspection."
			return Status{URL: UIURL}, false, failureErr
		}
		_ = listener.Close()
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		return Status{}, false, err
	}
	logFile, err := os.OpenFile(paths.InspectorLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Status{}, false, failure.Wrap("INSPECTOR_START_FAILED", "could not open the inspector log", failure.ExitGeneral, err)
	}
	executable, err := os.Executable()
	if err != nil {
		_ = logFile.Close()
		return Status{}, false, err
	}
	token, err := randomToken()
	if err != nil {
		_ = logFile.Close()
		return Status{}, false, err
	}
	command := exec.Command(executable, "__cfdev_inspector")
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = append(os.Environ(), "CFDEV_INSPECTOR_TOKEN="+token)
	configureBackground(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return Status{}, false, failure.Wrap("INSPECTOR_START_FAILED", "could not start the local inspector", failure.ExitGeneral, err)
	}
	state := State{PID: command.Process.Pid, Token: token, StartedAt: time.Now().UTC()}
	if err := writeState(paths, state); err != nil {
		_ = command.Process.Kill()
		_ = logFile.Close()
		return Status{}, false, err
	}
	_ = logFile.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if current := InspectStatus(paths); current.Running {
			_ = command.Process.Release()
			if captureBodies {
				if err := setCapture(paths, true); err != nil {
					return current, false, err
				}
				current.CaptureBodies = true
			}
			return current, false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_, _ = command.Process.Wait()
	_ = os.Remove(paths.Inspector)
	detail := ""
	if contents, readErr := os.ReadFile(paths.InspectorLog); readErr == nil && len(contents) > 0 {
		if len(contents) > 600 {
			contents = contents[len(contents)-600:]
		}
		detail = ": " + string(bytes.TrimSpace(contents))
	}
	failureErr := failure.New("INSPECTOR_START_FAILED", "the local inspector stopped while starting"+detail, failure.ExitGeneral)
	failureErr.Hint = "Run `cfdev doctor`, or inspect the inspector log shown by `cfdev config path`."
	return Status{URL: UIURL}, false, failureErr
}

func Stop(paths config.Paths) (bool, error) {
	state, err := readState(paths)
	if err != nil {
		_ = os.Remove(paths.Inspector)
		return false, nil
	}
	request, _ := http.NewRequest(http.MethodPost, UIURL+"/api/shutdown", nil)
	request.Header.Set("X-Cfdev-Token", state.Token)
	client := &http.Client{Timeout: 2 * time.Second}
	response, requestErr := client.Do(request)
	if requestErr == nil {
		_ = response.Body.Close()
	}
	deadline := time.Now().Add(3 * time.Second)
	for InspectStatus(paths).Running && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	stillRunning := InspectStatus(paths).Running
	if !stillRunning {
		_ = os.Remove(paths.Inspector)
	}
	if stillRunning {
		return false, failure.Wrap("INSPECTOR_STOP_FAILED", "could not stop the local inspector", failure.ExitGeneral, requestErr)
	}
	return true, nil
}

type healthResponse struct {
	Service       string `json:"service"`
	Home          string `json:"home"`
	CaptureBodies bool   `json:"capture_bodies"`
	Version       string `json:"version"`
}

func health() (healthResponse, bool) {
	client := &http.Client{Timeout: 250 * time.Millisecond}
	response, err := client.Get(UIURL + "/api/health")
	if err != nil {
		return healthResponse{}, false
	}
	defer response.Body.Close()
	var result healthResponse
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil || result.Service != "cfdev-inspector" {
		return healthResponse{}, false
	}
	return result, true
}

func setCapture(paths config.Paths, enabled bool) error {
	state, err := readState(paths)
	if err != nil {
		return failure.Wrap("INSPECTOR_STATE_INVALID", "the inspector is running without readable local state", failure.ExitConfig, err)
	}
	body, _ := json.Marshal(map[string]bool{"capture_bodies": enabled})
	request, _ := http.NewRequest(http.MethodPost, UIURL+"/api/settings", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Cfdev-Token", state.Token)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return failure.Wrap("INSPECTOR_SETTINGS_FAILED", "could not update inspector settings", failure.ExitGeneral, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return failure.New("INSPECTOR_SETTINGS_FAILED", "the inspector rejected its settings update", failure.ExitGeneral)
	}
	return nil
}

func readState(paths config.Paths) (State, error) {
	contents, err := os.ReadFile(paths.Inspector)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(contents, &state); err != nil {
		return State{}, err
	}
	if state.Token == "" || state.PID <= 0 {
		return State{}, fmt.Errorf("incomplete inspector state")
	}
	return state, nil
}

func writeState(paths config.Paths, state State) error {
	contents, _ := json.MarshalIndent(state, "", "  ")
	contents = append(contents, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(paths.Inspector), ".inspector-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	_ = os.Remove(paths.Inspector)
	return os.Rename(name, paths.Inspector)
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
