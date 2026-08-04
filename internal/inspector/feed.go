package inspector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const requestEventsPath = "/api/events"

// RequestEvent is the deliberately small metadata record streamed to an
// attached foreground cfdev process. Headers and bodies never enter this
// stream; the browser inspector remains the place for request details.
type RequestEvent struct {
	ID          uint64    `json:"id"`
	CompletedAt time.Time `json:"completed_at"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Hostname    string    `json:"hostname"`
	Target      string    `json:"target"`
	Status      int       `json:"status"`
	DurationMS  float64   `json:"duration_ms"`
	ReplayOf    uint64    `json:"replay_of,omitempty"`
	LocalDown   bool      `json:"local_down"`
}

// requestEventPath removes query parameters before request metadata crosses
// the inspector event-stream boundary. The full request URI remains available
// only in the in-memory inspector history.
func requestEventPath(path string) string {
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	if path == "" {
		return "/"
	}
	return path
}

// RequestStream reads future completed request metadata from the loopback
// inspector. Closing it unblocks a pending Next call.
type RequestStream struct {
	response  *http.Response
	scanner   *bufio.Scanner
	transport *http.Transport
}

func SubscribeRequests(ctx context.Context) (*RequestStream, error) {
	return subscribeRequests(ctx, UIURL+requestEventsPath)
}

func subscribeRequests(ctx context.Context, endpoint string) (*RequestStream, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ResponseHeaderTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("inspector request stream returned %s", response.Status)
	}
	return &RequestStream{response: response, scanner: bufio.NewScanner(response.Body), transport: transport}, nil
}

func (stream *RequestStream) Next() (RequestEvent, error) {
	for stream != nil && stream.scanner.Scan() {
		line := stream.scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event RequestEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
			return RequestEvent{}, fmt.Errorf("decode inspector request event: %w", err)
		}
		return event, nil
	}
	if stream != nil && stream.scanner.Err() != nil {
		return RequestEvent{}, stream.scanner.Err()
	}
	return RequestEvent{}, io.EOF
}

func (stream *RequestStream) Close() error {
	if stream == nil || stream.response == nil {
		return nil
	}
	err := stream.response.Body.Close()
	stream.transport.CloseIdleConnections()
	return err
}
