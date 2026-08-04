package inspector

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/deifos/cfdev/internal/config"
)

//go:embed web/index.html
var indexHTML []byte

type Server struct {
	paths       config.Paths
	token       string
	version     string
	store       *Store
	shutdown    chan struct{}
	once        sync.Once
	eventsMu    sync.Mutex
	subscribers map[chan RequestEvent]struct{}
}

func Run(paths config.Paths, token, version string) error {
	uiListener, err := net.Listen("tcp", UIAddress)
	if err != nil {
		return fmt.Errorf("bind inspector UI %s: %w", UIAddress, err)
	}
	proxyListener, err := net.Listen("tcp", ProxyAddress)
	if err != nil {
		_ = uiListener.Close()
		return fmt.Errorf("bind inspector gateway %s: %w", ProxyAddress, err)
	}
	server := NewServer(paths, token, version)
	uiServer := &http.Server{Handler: server.uiHandler(), ReadHeaderTimeout: 5 * time.Second}
	proxyServer := &http.Server{Handler: http.HandlerFunc(server.serveProxy), ReadHeaderTimeout: 10 * time.Second}
	errors := make(chan error, 2)
	go func() { errors <- uiServer.Serve(uiListener) }()
	go func() { errors <- proxyServer.Serve(proxyListener) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-server.shutdown:
	case <-signals:
	case err = <-errors:
	}
	signal.Stop(signals)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = uiServer.Shutdown(ctx)
	_ = proxyServer.Shutdown(ctx)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func NewServer(paths config.Paths, token, version string) *Server {
	return &Server{
		paths: paths, token: token, version: version, store: NewStore(), shutdown: make(chan struct{}),
		subscribers: make(map[chan RequestEvent]struct{}),
	}
}

func (server *Server) uiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", server.index)
	mux.HandleFunc("GET /api/health", server.apiHealth)
	mux.HandleFunc("GET /api/state", server.apiState)
	mux.HandleFunc("GET /api/requests", server.apiRequests)
	mux.HandleFunc("GET "+requestEventsPath, server.apiEvents)
	mux.HandleFunc("GET /api/requests/{id}", server.apiRequest)
	mux.HandleFunc("POST /api/requests/{id}/replay", server.apiReplay)
	mux.HandleFunc("POST /api/settings", server.apiSettings)
	mux.HandleFunc("POST /api/clear", server.apiClear)
	mux.HandleFunc("POST /api/shutdown", server.apiShutdown)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) index(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write(indexHTML)
}

func (server *Server) apiHealth(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"service": "cfdev-inspector", "home": server.paths.Home, "version": server.version, "capture_bodies": server.store.CaptureBodies()})
}

func (server *Server) apiState(writer http.ResponseWriter, _ *http.Request) {
	cfg, err := config.Load(server.paths)
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]any{"configured": false, "capture_bodies": server.store.CaptureBodies(), "limits": limitsView(), "requests": len(server.store.List())})
		return
	}
	type mappingState struct {
		Hostname  string `json:"hostname"`
		Target    string `json:"target"`
		Listening bool   `json:"listening"`
	}
	mappings := make([]mappingState, 0, len(cfg.Mappings))
	for _, mapping := range cfg.Mappings {
		target := fmt.Sprintf("%s://127.0.0.1:%d", mapping.Protocol, mapping.Port)
		mappings = append(mappings, mappingState{Hostname: mapping.Subdomain + "." + cfg.Domain, Target: target})
	}
	var healthChecks sync.WaitGroup
	for index := range mappings {
		healthChecks.Add(1)
		go func(index int) {
			defer healthChecks.Done()
			parsed, _ := url.Parse(mappings[index].Target)
			port, _ := strconv.Atoi(parsed.Port())
			mappings[index].Listening = portListening(port)
		}(index)
	}
	healthChecks.Wait()
	_, tunnelErr := os.Stat(server.paths.Process)
	writeJSON(writer, http.StatusOK, map[string]any{
		"configured": true, "tunnel_running": tunnelErr == nil, "capture_bodies": server.store.CaptureBodies(),
		"mappings": mappings, "limits": limitsView(), "requests": len(server.store.List()), "memory_bytes": server.store.MemoryBytes(),
	})
}

func limitsView() map[string]any {
	return map[string]any{"requests": MaxRequests, "body_bytes": MaxBodyBytes, "memory_bytes": MaxStoreBytes}
}

func (server *Server) apiRequests(writer http.ResponseWriter, _ *http.Request) {
	items := server.store.List()
	views := make([]exchangeView, 0, len(items))
	for _, item := range items {
		views = append(views, makeExchangeView(item, false))
	}
	writeJSON(writer, http.StatusOK, views)
}

func (server *Server) apiEvents(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	events, unsubscribe := server.subscribeEvents()
	defer unsubscribe()
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, ": cfdev request stream\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event := <-events:
			encoded, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = io.WriteString(writer, ": keepalive\n\n")
			flusher.Flush()
		case <-request.Context().Done():
			return
		}
	}
}

func (server *Server) subscribeEvents() (<-chan RequestEvent, func()) {
	events := make(chan RequestEvent, MaxRequests)
	server.eventsMu.Lock()
	server.subscribers[events] = struct{}{}
	server.eventsMu.Unlock()
	return events, func() {
		server.eventsMu.Lock()
		delete(server.subscribers, events)
		server.eventsMu.Unlock()
	}
}

func (server *Server) record(exchange Exchange) Exchange {
	added := server.store.Add(exchange)
	event := RequestEvent{
		ID: added.ID, CompletedAt: added.CompletedAt, Method: added.Method, Path: requestEventPath(added.Path), Hostname: added.Hostname,
		Target: added.Target, Status: added.Status, DurationMS: added.DurationMS, ReplayOf: added.ReplayOf, LocalDown: added.LocalDown,
	}
	server.eventsMu.Lock()
	for subscriber := range server.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
	server.eventsMu.Unlock()
	return added
}

func (server *Server) apiRequest(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	exchange, ok := server.store.Get(id)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	view := makeExchangeView(exchange, true)
	view.Curl, view.CurlAvailable = curlFor(exchange)
	writeJSON(writer, http.StatusOK, view)
}

func (server *Server) apiSettings(writer http.ResponseWriter, request *http.Request) {
	if !server.authorizedMutation(request) {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	var settings struct {
		CaptureBodies bool `json:"capture_bodies"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024)).Decode(&settings); err != nil {
		http.Error(writer, "invalid settings", http.StatusBadRequest)
		return
	}
	server.store.SetCaptureBodies(settings.CaptureBodies)
	writeJSON(writer, http.StatusOK, map[string]any{"capture_bodies": settings.CaptureBodies, "applies_to": "future_requests"})
}

func (server *Server) apiClear(writer http.ResponseWriter, request *http.Request) {
	if !server.authorizedMutation(request) {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	server.store.Clear()
	writeJSON(writer, http.StatusOK, map[string]bool{"cleared": true})
}

func (server *Server) apiShutdown(writer http.ResponseWriter, request *http.Request) {
	if server.token == "" || request.Header.Get("X-Cfdev-Token") != server.token {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"stopping": true})
	server.once.Do(func() { close(server.shutdown) })
}

func (server *Server) authorizedMutation(request *http.Request) bool {
	if server.token != "" && request.Header.Get("X-Cfdev-Token") == server.token {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		return false
	}
	host := strings.ToLower(request.Host)
	origin := strings.ToLower(request.Header.Get("Origin"))
	return (host == UIAddress || host == "localhost:4040") && (origin == "http://"+UIAddress || origin == "http://localhost:4040")
}

func (server *Server) serveProxy(writer http.ResponseWriter, request *http.Request) {
	server.proxy(writer, request, "", 0)
}

func (server *Server) proxy(writer http.ResponseWriter, request *http.Request, forcedTarget string, replayOf uint64) {
	started := time.Now()
	hostname := normalizedHost(request.Host)
	target, mappingFound := forcedTarget, forcedTarget != ""
	if !mappingFound {
		target, mappingFound = server.targetFor(hostname)
	}
	exchange := Exchange{
		StartedAt: started.UTC(), Method: request.Method, Path: request.URL.RequestURI(), Hostname: hostname,
		Target: target, RequestHeaders: displayHeaders(request.Header), replayHeaders: replayHeaders(request.Header), ReplayOf: replayOf,
		RequestHadBody: request.Body != nil && request.ContentLength != 0,
	}
	streaming := isUpgrade(request.Header) || strings.EqualFold(request.Header.Get("Accept"), "text/event-stream")
	exchange.Streaming = streaming
	captureEnabled := server.store.CaptureBodies()
	capture := captureEnabled && !streaming
	requestCapture := wrapBody(request.Body, capture, skippedReason(captureEnabled, streaming))
	if requestCapture != nil {
		request.Body = requestCapture
	}
	if !mappingFound {
		exchange.Status = http.StatusNotFound
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(writer, unavailablePage("No cfdev mapping matches this hostname."))
		server.finish(exchange, requestCapture, nil, started)
		return
	}
	targetURL, err := url.Parse(target)
	if err != nil {
		exchange.Status = http.StatusBadGateway
		exchange.LocalDown = true
		http.Error(writer, "invalid local target", http.StatusBadGateway)
		server.finish(exchange, requestCapture, nil, started)
		return
	}
	var responseCapture *capturedBody
	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(targetURL)
			proxyRequest.SetXForwarded()
			proxyRequest.Out.Host = proxyRequest.In.Host
			proxyRequest.Out.Header.Del("X-Cfdev-Replay-Of")
		},
		ModifyResponse: func(response *http.Response) error {
			exchange.Status = response.StatusCode
			exchange.ResponseHeaders = displayHeaders(response.Header)
			responseStreaming := response.StatusCode == http.StatusSwitchingProtocols || strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
			if responseStreaming {
				exchange.Streaming = true
				responseCapture = newCapture(false, "streaming")
				return nil
			}
			responseCapture = wrapBody(response.Body, capture, skippedReason(captureEnabled, false))
			if responseCapture != nil {
				response.Body = responseCapture
			}
			return nil
		},
		ErrorHandler: func(responseWriter http.ResponseWriter, _ *http.Request, proxyErr error) {
			exchange.Status = http.StatusBadGateway
			exchange.LocalDown = true
			responseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
			responseWriter.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(responseWriter, unavailablePage("The local app at "+target+" is not accepting connections."))
		},
	}
	proxy.ServeHTTP(writer, request)
	server.finish(exchange, requestCapture, responseCapture, started)
}

func (server *Server) finish(exchange Exchange, requestCapture, responseCapture *capturedBody, started time.Time) {
	exchange.CompletedAt = time.Now().UTC()
	exchange.DurationMS = float64(time.Since(started).Microseconds()) / 1000
	if exchange.Status == 0 {
		exchange.Status = http.StatusOK
	}
	if requestCapture != nil {
		exchange.RequestBody = requestCapture.result()
	}
	if responseCapture != nil {
		exchange.ResponseBody = responseCapture.result()
	}
	server.record(exchange)
}

func (server *Server) targetFor(hostname string) (string, bool) {
	cfg, err := config.Load(server.paths)
	if err != nil {
		return "", false
	}
	for _, mapping := range cfg.Mappings {
		if strings.EqualFold(hostname, mapping.Subdomain+"."+cfg.Domain) {
			return fmt.Sprintf("%s://127.0.0.1:%d", mapping.Protocol, mapping.Port), true
		}
	}
	return "", false
}

func (server *Server) apiReplay(writer http.ResponseWriter, request *http.Request) {
	if !server.authorizedMutation(request) {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	original, ok := server.store.Get(id)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	if !original.Replayable() {
		writeJSON(writer, http.StatusConflict, map[string]string{"error": "This request cannot be replayed because its body was not captured exactly or it used streaming."})
		return
	}
	target, err := localReplayURL(original.Target, original.Path)
	if err != nil {
		http.Error(writer, "invalid replay target", http.StatusInternalServerError)
		return
	}
	var body io.Reader
	if original.RequestHadBody {
		body = bytes.NewReader(original.RequestBody.Bytes)
	}
	replayRequest, err := http.NewRequestWithContext(request.Context(), original.Method, target.String(), body)
	if err != nil {
		http.Error(writer, "could not build replay", http.StatusInternalServerError)
		return
	}
	replayRequest.Header = original.replayHeaders.Clone()
	removeHopHeaders(replayRequest.Header)
	replayRequest.Header.Del("Content-Length")
	replayRequest.Header.Set("X-Cfdev-Replay-Of", strconv.FormatUint(original.ID, 10))
	replayRequest.Host = target.Host
	started := time.Now()
	replayClient := &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, replayErr := replayClient.Do(replayRequest)
	replayed := Exchange{
		StartedAt: started.UTC(), CompletedAt: time.Now().UTC(), Method: original.Method, Path: original.Path,
		Hostname: original.Hostname, Target: original.Target, RequestHeaders: displayHeaders(replayRequest.Header), replayHeaders: replayHeaders(replayRequest.Header),
		RequestHadBody: original.RequestHadBody, ReplayOf: original.ID, RequestBody: original.RequestBody,
	}
	if replayErr != nil {
		replayed.Status = http.StatusBadGateway
		replayed.LocalDown = true
	} else {
		replayed.Status = response.StatusCode
		replayed.ResponseHeaders = displayHeaders(response.Header)
		streaming := response.StatusCode == http.StatusSwitchingProtocols || strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
		replayed.Streaming = streaming
		captureEnabled := server.store.CaptureBodies()
		captureResponse := captureEnabled && !streaming
		captured := newCapture(captureResponse, skippedReason(captureEnabled, streaming))
		_, _ = io.Copy(io.Discard, io.TeeReader(response.Body, captured))
		_ = response.Body.Close()
		replayed.ResponseBody = captured.result()
	}
	replayed.DurationMS = float64(time.Since(started).Microseconds()) / 1000
	added := server.record(replayed)
	writeJSON(writer, http.StatusOK, map[string]any{"replayed": true, "request": makeExchangeView(added, false)})
}

type capturedBody struct {
	closer     io.ReadCloser
	buffer     bytes.Buffer
	size       int64
	enabled    bool
	truncated  bool
	incomplete bool
	skipped    string
}

func wrapBody(body io.ReadCloser, enabled bool, skipped string) *capturedBody {
	if body == nil {
		return nil
	}
	result := newCapture(enabled, skipped)
	result.closer = body
	return result
}

func newCapture(enabled bool, skipped string) *capturedBody {
	return &capturedBody{enabled: enabled, skipped: skipped}
}

func (capture *capturedBody) Read(buffer []byte) (int, error) {
	if capture.closer == nil {
		return 0, io.EOF
	}
	read, err := capture.closer.Read(buffer)
	if read > 0 {
		_, _ = capture.Write(buffer[:read])
	}
	if err != nil && err != io.EOF {
		capture.incomplete = true
	}
	return read, err
}

func (capture *capturedBody) Write(buffer []byte) (int, error) {
	capture.size += int64(len(buffer))
	if capture.enabled && capture.buffer.Len() < MaxBodyBytes {
		remaining := MaxBodyBytes - capture.buffer.Len()
		if remaining > len(buffer) {
			remaining = len(buffer)
		}
		_, _ = capture.buffer.Write(buffer[:remaining])
	}
	if capture.size > MaxBodyBytes {
		capture.truncated = true
	}
	return len(buffer), nil
}

func (capture *capturedBody) Close() error {
	if capture.closer != nil {
		return capture.closer.Close()
	}
	return nil
}

func (capture *capturedBody) result() Body {
	if capture == nil {
		return Body{}
	}
	return Body{Bytes: append([]byte(nil), capture.buffer.Bytes()...), Size: capture.size, Captured: capture.enabled, Truncated: capture.truncated, Incomplete: capture.incomplete, Skipped: capture.skipped}
}

func skippedReason(enabled, streaming bool) string {
	if streaming {
		return "streaming"
	}
	if !enabled {
		return "capture_disabled"
	}
	return ""
}

type bodyView struct {
	Size       int64  `json:"size"`
	Captured   bool   `json:"captured"`
	Truncated  bool   `json:"truncated"`
	Incomplete bool   `json:"incomplete"`
	Skipped    string `json:"skipped,omitempty"`
	Text       string `json:"text,omitempty"`
	Base64     string `json:"base64,omitempty"`
	Binary     bool   `json:"binary"`
}

type exchangeView struct {
	ID              uint64      `json:"id"`
	StartedAt       time.Time   `json:"started_at"`
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	Hostname        string      `json:"hostname"`
	Target          string      `json:"target"`
	Status          int         `json:"status"`
	DurationMS      float64     `json:"duration_ms"`
	RequestHeaders  http.Header `json:"request_headers,omitempty"`
	ResponseHeaders http.Header `json:"response_headers,omitempty"`
	RequestBody     bodyView    `json:"request_body"`
	ResponseBody    bodyView    `json:"response_body"`
	ReplayOf        uint64      `json:"replay_of,omitempty"`
	Replayable      bool        `json:"replayable"`
	LocalDown       bool        `json:"local_down"`
	Streaming       bool        `json:"streaming"`
	Curl            string      `json:"curl,omitempty"`
	CurlAvailable   bool        `json:"curl_available"`
}

func makeExchangeView(exchange Exchange, details bool) exchangeView {
	view := exchangeView{
		ID: exchange.ID, StartedAt: exchange.StartedAt, Method: exchange.Method, Path: exchange.Path, Hostname: exchange.Hostname,
		Target: exchange.Target, Status: exchange.Status, DurationMS: exchange.DurationMS, ReplayOf: exchange.ReplayOf,
		Replayable: exchange.Replayable(), LocalDown: exchange.LocalDown, Streaming: exchange.Streaming,
		RequestBody: makeBodyView(exchange.RequestBody, details), ResponseBody: makeBodyView(exchange.ResponseBody, details),
	}
	if details {
		view.RequestHeaders = exchange.RequestHeaders
		view.ResponseHeaders = exchange.ResponseHeaders
	}
	return view
}

func makeBodyView(body Body, details bool) bodyView {
	view := bodyView{Size: body.Size, Captured: body.Captured, Truncated: body.Truncated, Incomplete: body.Incomplete, Skipped: body.Skipped}
	if !details || !body.Captured {
		return view
	}
	if utf8.Valid(body.Bytes) {
		view.Text = string(body.Bytes)
	} else {
		view.Base64 = base64.StdEncoding.EncodeToString(body.Bytes)
		view.Binary = true
	}
	return view
}

func displayHeaders(headers http.Header) http.Header {
	result := make(http.Header, len(headers))
	for name, values := range headers {
		if sensitiveHeader(name) {
			result[name] = []string{"[REDACTED]"}
		} else {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

func replayHeaders(headers http.Header) http.Header {
	result := make(http.Header, len(headers))
	for name, values := range headers {
		if !sensitiveHeader(name) {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

func sensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	default:
		return false
	}
}

func removeHopHeaders(headers http.Header) {
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}

func isUpgrade(headers http.Header) bool {
	return headers.Get("Upgrade") != "" || strings.Contains(strings.ToLower(headers.Get("Connection")), "upgrade")
}

func normalizedHost(host string) string {
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func localReplayURL(target, requestURI string) (*url.URL, error) {
	base, err := url.Parse(target)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() != "127.0.0.1" || base.Port() == "" {
		return nil, fmt.Errorf("replay target is not a configured loopback HTTP service")
	}
	relative, err := url.ParseRequestURI(requestURI)
	if err != nil || relative.IsAbs() || relative.Host != "" || !strings.HasPrefix(relative.Path, "/") {
		return nil, fmt.Errorf("captured request path is not a local absolute path")
	}
	base.Path = relative.Path
	base.RawPath = relative.RawPath
	base.RawQuery = relative.RawQuery
	base.Fragment = ""
	return base, nil
}

func portListening(port int) bool {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func curlFor(exchange Exchange) (string, bool) {
	if !exchange.Replayable() {
		return "", false
	}
	command := "curl --include --request " + shellQuote(exchange.Method) + " " + shellQuote(exchange.Target+exchange.Path)
	names := make([]string, 0, len(exchange.replayHeaders))
	for name := range exchange.replayHeaders {
		if sensitiveHeader(name) || strings.EqualFold(name, "Host") || strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "X-Cfdev-Replay-Of") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range exchange.replayHeaders.Values(name) {
			command += " \\\n  --header " + shellQuote(name+": "+value)
		}
	}
	if exchange.RequestHadBody {
		encoded := base64.StdEncoding.EncodeToString(exchange.RequestBody.Bytes)
		command = "printf %s " + shellQuote(encoded) + " | base64 --decode | " + command + " \\\n  --data-binary @-"
	}
	return command, true
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func unavailablePage(message string) string {
	return "<!doctype html><html><head><meta charset=utf-8><meta name=viewport content='width=device-width'><title>cfdev · app unavailable</title><style>body{font:16px system-ui;background:#0b1020;color:#e8edf7;display:grid;place-items:center;min-height:100vh;margin:0}.card{max-width:560px;padding:36px;border:1px solid #29334d;border-radius:18px;background:#121a2c}b{color:#75e6b5}p{line-height:1.55;color:#aeb9ce}</style></head><body><main class=card><b>cfdev</b><h1>Local app unavailable</h1><p>" + htmlEscape(message) + "</p><p>Start the app, then retry the request from the inspector at <b>127.0.0.1:4040</b>.</p></main></body></html>"
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return replacer.Replace(value)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
