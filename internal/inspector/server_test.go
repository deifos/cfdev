package inspector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/failure"
)

const inspectorTestTunnelID = "123e4567-e89b-42d3-a456-426614174000"

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "__cfdev_inspector" {
		paths, err := config.ResolvePaths()
		if err == nil {
			err = Run(paths, os.Getenv("CFDEV_INSPECTOR_TOKEN"), "test")
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDaemonKeepsHistoryAcrossMappingChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("first")) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("second")) }))
	defer second.Close()
	saveConfigForTarget(t, paths, first.URL)
	status, _, err := Ensure(paths, false, "test")
	if err != nil {
		if strings.Contains(err.Error(), "cannot bind") {
			t.Skip("loopback inspector ports are already in use")
		}
		t.Fatal(err)
	}
	if !status.Running {
		t.Fatal("inspector did not start")
	}
	t.Cleanup(func() { _, _ = Stop(paths) })

	proxyRequest := func() string {
		request, _ := http.NewRequest(http.MethodGet, "http://"+ProxyAddress+"/hook", nil)
		request.Host = "hooks.example.com"
		response, requestErr := (&http.Client{Timeout: 2 * time.Second}).Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		contents, _ := io.ReadAll(response.Body)
		return string(contents)
	}
	if body := proxyRequest(); body != "first" {
		t.Fatalf("first mapping response = %q", body)
	}
	saveConfigForTarget(t, paths, second.URL)
	if body := proxyRequest(); body != "second" {
		t.Fatalf("changed mapping response = %q", body)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Get(UIURL + "/api/requests")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var history []exchangeView
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Target == history[1].Target {
		t.Fatalf("history after mapping change: %#v", history)
	}
}

func TestEnsureReportsLoopbackPortConflict(t *testing.T) {
	listener, err := net.Listen("tcp", UIAddress)
	if err != nil {
		t.Skip("loopback inspector port is already in use")
	}
	defer listener.Close()
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Ensure(paths, false, "test")
	if err == nil || failure.As(err).Code != "INSPECTOR_PORT_UNAVAILABLE" || !strings.Contains(err.Error(), UIAddress) {
		t.Fatalf("unexpected conflict error: %#v", err)
	}
}

func TestProxyAlwaysCapturesMetadataAndRedactsSecrets(t *testing.T) {
	var received []byte
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received, _ = io.ReadAll(request.Body)
		writer.Header().Set("Set-Cookie", "session=response-secret")
		writer.Header().Set("X-Webhook-Signature", "response-signature")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("accepted"))
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)

	request := httptest.NewRequest(http.MethodPost, "http://gateway/hooks/stripe?attempt=1", strings.NewReader("{ \"exact\": true }\n"))
	request.Host = "hooks.example.com"
	request.Header.Set("Authorization", "Bearer request-secret")
	request.Header.Set("Cookie", "session=request-secret")
	request.Header.Set("Stripe-Signature", "t=1,v1=kept")
	response := httptest.NewRecorder()
	server.serveProxy(response, request)

	if response.Code != http.StatusCreated || string(received) != "{ \"exact\": true }\n" {
		t.Fatalf("transparent proxy = status %d, body %q", response.Code, received)
	}
	items := server.store.List()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	exchange := items[0]
	if exchange.Method != http.MethodPost || exchange.Path != "/hooks/stripe?attempt=1" || exchange.Status != http.StatusCreated {
		t.Fatalf("unexpected metadata: %#v", exchange)
	}
	if exchange.RequestBody.Captured || exchange.ResponseBody.Captured {
		t.Fatal("bodies should be disabled by default")
	}
	if exchange.RequestHeaders.Get("Authorization") != "[REDACTED]" || exchange.RequestHeaders.Get("Cookie") != "[REDACTED]" {
		t.Fatalf("request secrets were not redacted: %#v", exchange.RequestHeaders)
	}
	if exchange.ResponseHeaders.Get("Set-Cookie") != "[REDACTED]" || exchange.RequestHeaders.Get("Stripe-Signature") != "t=1,v1=kept" {
		t.Fatalf("redaction removed the wrong headers: request=%#v response=%#v", exchange.RequestHeaders, exchange.ResponseHeaders)
	}
	if exchange.replayHeaders.Get("Authorization") != "" || exchange.replayHeaders.Get("Cookie") != "" {
		t.Fatal("replay headers retained sensitive values")
	}
}

func TestRequestEventStreamPublishesOnlyFutureMetadata(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)

	oldRequest := httptest.NewRequest(http.MethodGet, "http://gateway/old?token=secret", nil)
	oldRequest.Host = "hooks.example.com"
	server.serveProxy(httptest.NewRecorder(), oldRequest)

	uiServer := httptest.NewServer(server.uiHandler())
	defer uiServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := subscribeRequests(ctx, uiServer.URL+requestEventsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	request := httptest.NewRequest(http.MethodPost, "http://gateway/hooks/stripe?token=do-not-print", strings.NewReader("private body"))
	request.Host = "hooks.example.com"
	request.Header.Set("Authorization", "Bearer private-header")
	server.serveProxy(httptest.NewRecorder(), request)

	events := make(chan RequestEvent, 1)
	errors := make(chan error, 1)
	go func() {
		event, nextErr := stream.Next()
		if nextErr != nil {
			errors <- nextErr
			return
		}
		events <- event
	}()
	select {
	case err := <-errors:
		t.Fatal(err)
	case event := <-events:
		if event.ID != 2 || event.Method != http.MethodPost || event.Path != "/hooks/stripe" || event.Status != http.StatusNoContent {
			t.Fatalf("unexpected request event: %#v", event)
		}
		encoded, _ := json.Marshal(event)
		if bytes.Contains(encoded, []byte("do-not-print")) || bytes.Contains(encoded, []byte("private-header")) || bytes.Contains(encoded, []byte("private body")) {
			t.Fatalf("request event exposed captured details: %s", encoded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request event")
	}
}

func TestInspectorDashboardIncludesPolishedDebuggingControls(t *testing.T) {
	page := string(indexHTML)
	for _, expected := range []string{
		`id="noise"`, "Hide framework noise", "frameworkNoisePrefixes", `id="serviceNotice"`,
		"Response status", "Response headers", "Response body", "Replay to localhost", `class="replay-tag"`,
		"No traffic yet", "Tunnel is down", "Local app is not listening", "not accepting connections",
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("inspector dashboard is missing %q", expected)
		}
	}
	if !strings.Contains(page, "status>=400?'error':status>=300?'redirect':status>=200?'success'") {
		t.Fatal("inspector dashboard status color language is not explicit")
	}
}

func TestCapturePreservesExactBytesAndTruncatesWithoutBreakingProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		contents, _ := io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write(contents)
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)
	server.store.SetCaptureBodies(true)
	exact := []byte{'{', '\n', ' ', '"', 'x', '"', ':', '1', '\n', '}', 0, 255}
	request := httptest.NewRequest(http.MethodPost, "http://gateway/raw", bytes.NewReader(exact))
	request.Host = "hooks.example.com"
	response := httptest.NewRecorder()
	server.serveProxy(response, request)
	if !bytes.Equal(response.Body.Bytes(), exact) {
		t.Fatal("proxy changed response bytes")
	}
	exchange := server.store.List()[0]
	if !bytes.Equal(exchange.RequestBody.Bytes, exact) || !bytes.Equal(exchange.ResponseBody.Bytes, exact) {
		t.Fatal("captured bytes differ from traffic")
	}
	if !exchange.Replayable() {
		t.Fatal("exact captured request should be replayable")
	}

	large := bytes.Repeat([]byte("a"), MaxBodyBytes+37)
	request = httptest.NewRequest(http.MethodPost, "http://gateway/large", bytes.NewReader(large))
	request.Host = "hooks.example.com"
	response = httptest.NewRecorder()
	server.serveProxy(response, request)
	exchange = server.store.List()[0]
	if response.Body.Len() != len(large) || len(exchange.RequestBody.Bytes) != MaxBodyBytes || !exchange.RequestBody.Truncated {
		t.Fatalf("truncation broke traffic or limits: forwarded=%d captured=%d truncated=%v", response.Body.Len(), len(exchange.RequestBody.Bytes), exchange.RequestBody.Truncated)
	}
	if exchange.Replayable() {
		t.Fatal("truncated request must not be replayable")
	}
}

func TestReplayUsesOriginalLocalTargetAndCreatesMarkedEntry(t *testing.T) {
	var calls atomic.Int32
	var replayAuthorization atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if calls.Add(1) > 1 && request.Header.Get("Authorization") != "" {
			replayAuthorization.Store(true)
		}
		contents, _ := io.ReadAll(request.Body)
		_, _ = writer.Write(contents)
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)
	server.store.SetCaptureBodies(true)
	request := httptest.NewRequest(http.MethodPost, "http://gateway/webhook", strings.NewReader("signed bytes"))
	request.Host = "hooks.example.com"
	request.Header.Set("Authorization", "Bearer do-not-replay")
	request.Header.Set("X-Hub-Signature-256", "sha256=keep")
	server.serveProxy(httptest.NewRecorder(), request)
	original := server.store.List()[0]

	unreachable := "http://127.0.0.1:1"
	saveConfigForTarget(t, server.paths, unreachable)
	replayRequest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:4040/api/requests/%d/replay", original.ID), strings.NewReader("{}"))
	replayRequest.Host = UIAddress
	replayRequest.Header.Set("Origin", UIURL)
	replayRequest.Header.Set("Content-Type", "application/json")
	replayResponse := httptest.NewRecorder()
	server.uiHandler().ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("replay status=%d calls=%d body=%s", replayResponse.Code, calls.Load(), replayResponse.Body.String())
	}
	if replayAuthorization.Load() {
		t.Fatal("replay forwarded a redacted authorization header")
	}
	replayed := server.store.List()[0]
	if replayed.ReplayOf != original.ID || replayed.Target != original.Target || string(replayed.RequestBody.Bytes) != "signed bytes" {
		t.Fatalf("unexpected replay entry: %#v", replayed)
	}
	if replayed.RequestHeaders.Get("X-Hub-Signature-256") != "sha256=keep" {
		t.Fatal("webhook signature header was not preserved")
	}
}

func TestReplayNeverFollowsRedirectsOrAcceptsANonLoopbackTarget(t *testing.T) {
	var redirected atomic.Bool
	external := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer external.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", external.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)
	request := httptest.NewRequest(http.MethodGet, "http://gateway/redirect", nil)
	request.Host = "hooks.example.com"
	server.serveProxy(httptest.NewRecorder(), request)
	original := server.store.List()[0]
	replayRequest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:4040/api/requests/%d/replay", original.ID), strings.NewReader("{}"))
	replayRequest.Host = UIAddress
	replayRequest.Header.Set("Origin", UIURL)
	replayRequest.Header.Set("Content-Type", "application/json")
	replayResponse := httptest.NewRecorder()
	server.uiHandler().ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK || redirected.Load() {
		t.Fatalf("replay followed a redirect: status=%d redirected=%v", replayResponse.Code, redirected.Load())
	}
	if replayed := server.store.List()[0]; replayed.Status != http.StatusFound {
		t.Fatalf("redirect response was not recorded: %#v", replayed)
	}
	if _, err := localReplayURL("https://example.com:443", "/hook"); err == nil {
		t.Fatal("non-loopback replay target was accepted")
	}
	if _, err := localReplayURL(original.Target, "https://example.com/hook"); err == nil {
		t.Fatal("absolute captured URI was accepted")
	}
}

func TestStreamingResponsePassesThroughWithoutBodyCapture(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: exact\n\n"))
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)
	server.store.SetCaptureBodies(true)
	request := httptest.NewRequest(http.MethodGet, "http://gateway/events", nil)
	request.Host = "hooks.example.com"
	response := httptest.NewRecorder()
	server.serveProxy(response, request)
	if response.Body.String() != "data: exact\n\n" {
		t.Fatalf("stream changed: %q", response.Body.String())
	}
	exchange := server.store.List()[0]
	if !exchange.Streaming || exchange.ResponseBody.Captured || exchange.ResponseBody.Skipped != "streaming" {
		t.Fatalf("stream capture state: %#v", exchange.ResponseBody)
	}
}

func TestUnavailableLocalAppReturnsBrandedPageAndRecordsDownState(t *testing.T) {
	server := configuredServer(t, "http://127.0.0.1:1")
	request := httptest.NewRequest(http.MethodGet, "http://gateway/hook", nil)
	request.Host = "hooks.example.com"
	response := httptest.NewRecorder()
	server.serveProxy(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "Local app unavailable") {
		t.Fatalf("down response = %d %q", response.Code, response.Body.String())
	}
	exchange := server.store.List()[0]
	if !exchange.LocalDown || exchange.Status != http.StatusBadGateway {
		t.Fatalf("down exchange = %#v", exchange)
	}
}

func TestWebSocketUpgradePassesThrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Fatal("backend writer cannot hijack")
		}
		connection, buffer, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\nhello")
		_ = buffer.Flush()
	}))
	defer backend.Close()
	server := configuredServer(t, backend.URL)
	proxy := httptest.NewServer(http.HandlerFunc(server.serveProxy))
	defer proxy.Close()
	parsed, _ := url.Parse(proxy.URL)
	connection, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET /socket HTTP/1.1\r\nHost: hooks.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("upgrade failed: status=%q err=%v", status, err)
	}
	all, _ := io.ReadAll(reader)
	if !strings.Contains(string(all), "hello") {
		t.Fatalf("upgraded stream missing payload: %q", all)
	}
}

func TestStoreEvictsOldestByCountAndMemory(t *testing.T) {
	store := NewStore()
	for index := 0; index < MaxRequests+5; index++ {
		store.Add(Exchange{Path: fmt.Sprintf("/%d", index)})
	}
	items := store.List()
	if len(items) != MaxRequests || items[len(items)-1].Path != "/5" {
		t.Fatalf("count eviction kept %d items; oldest=%q", len(items), items[len(items)-1].Path)
	}
	store.Clear()
	chunk := bytes.Repeat([]byte("x"), MaxBodyBytes)
	for index := 0; index < 40; index++ {
		store.Add(Exchange{RequestBody: Body{Bytes: chunk, Captured: true}})
	}
	if store.MemoryBytes() > MaxStoreBytes || len(store.List()) != 32 {
		t.Fatalf("memory eviction = %d bytes, %d items", store.MemoryBytes(), len(store.List()))
	}
}

func TestCurlUsesLocalTargetAndOmitsSensitiveHeaders(t *testing.T) {
	exchange := Exchange{
		Method: http.MethodPost, Path: "/hooks?x=1", Target: "http://127.0.0.1:3000", RequestHadBody: true,
		RequestBody:   Body{Bytes: []byte("exact\nbytes"), Captured: true},
		replayHeaders: http.Header{"Authorization": {"secret"}, "Content-Type": {"application/json"}, "Stripe-Signature": {"keep"}},
	}
	command, ok := curlFor(exchange)
	if !ok || !strings.Contains(command, "http://127.0.0.1:3000/hooks?x=1") || strings.Contains(command, "secret") || !strings.Contains(command, "Stripe-Signature: keep") || !strings.Contains(command, "--data-binary @-") {
		t.Fatalf("unexpected curl command: %s", command)
	}
}

func configuredServer(t *testing.T, target string) *Server {
	t.Helper()
	home := t.TempDir()
	paths := config.Paths{Home: home, Config: filepath.Join(home, "config.json"), Ingress: filepath.Join(home, "cloudflared.yml"), Process: filepath.Join(home, "process.json")}
	saveConfigForTarget(t, paths, target)
	return NewServer(paths, "test-token", "test")
}

func saveConfigForTarget(t *testing.T, paths config.Paths, target string) {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test", TunnelID: inspectorTestTunnelID,
		CredentialsFile: filepath.Join(paths.Home, "credential.json"), MachineID: "test",
		Mappings: []config.Mapping{{Subdomain: "hooks", Port: port, Protocol: parsed.Scheme, CreatedAt: time.Now().UTC()}},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Config, 0o600); err != nil {
		t.Fatal(err)
	}
}
