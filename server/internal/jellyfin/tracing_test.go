package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRouteTracingEmitsOneBoundedRedactedEventForRootAndEmby(t *testing.T) {
	const (
		pathSecret   = "12345678-1234-4234-8234-123456789abc"
		querySecret  = "query-api-key-SENTINEL"
		headerSecret = "header-SENTINEL"
		bodySecret   = "body-SENTINEL"
		tokenSecret  = "compat-token-SENTINEL"
		cookieSecret = "cookie-SENTINEL"
		ipSecret     = "198.51.100.77"
		agentSecret  = "user-agent-SENTINEL"
	)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	calls := 0
	implementation := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/Items/"+pathSecret+"/PlaybackInfo" {
			t.Errorf("normalized path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("api_key") != querySecret {
			t.Errorf("query was changed")
		}
		if request.Header.Get("X-Sentinel") != headerSecret || request.Header.Get("X-Emby-Token") != tokenSecret {
			t.Errorf("headers were changed")
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil || string(payload) != bodySecret {
			t.Errorf("body = %q, error = %v", payload, err)
		}
		response.WriteHeader(http.StatusPartialContent)
	})
	handler, err := New(Dependencies{
		Logger: logger,
		Handlers: map[Route]http.Handler{
			RoutePlaybackInfoPost: implementation,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)

	for _, prefix := range []string{"", "/emby"} {
		request := httptest.NewRequest(
			http.MethodPost,
			prefix+"/Items/"+pathSecret+"/PlaybackInfo?api_key="+querySecret,
			strings.NewReader(bodySecret),
		)
		request.Header.Set("X-Sentinel", headerSecret)
		request.Header.Set("X-Emby-Token", tokenSecret)
		request.Header.Set("Cookie", "session="+cookieSecret)
		request.Header.Set("User-Agent", agentSecret)
		request.RemoteAddr = ipSecret + ":54321"
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusPartialContent {
			t.Fatalf("POST %s status = %d", prefix, response.Code)
		}
	}
	if calls != 2 {
		t.Fatalf("handler calls = %d, want 2", calls)
	}

	rawLogs := logs.String()
	for _, forbidden := range []string{
		pathSecret,
		querySecret,
		headerSecret,
		bodySecret,
		tokenSecret,
		cookieSecret,
		ipSecret,
		agentSecret,
		"/Items/",
		"/emby/",
		"api_key",
		"X-Emby-Token",
	} {
		if strings.Contains(rawLogs, forbidden) {
			t.Errorf("logs contain forbidden value %q: %s", forbidden, rawLogs)
		}
	}

	lines := strings.Split(strings.TrimSpace(rawLogs), "\n")
	if len(lines) != 2 {
		t.Fatalf("log events = %d, want exactly 2: %s", len(lines), rawLogs)
	}
	for index, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode event %d: %v", index, err)
		}
		if event["msg"] != compatRequestCompletedMessage || event["route"] != string(RoutePlaybackInfoPost) || event["method"] != http.MethodPost {
			t.Errorf("event %d identity = %#v", index, event)
		}
		if event["status"] != float64(http.StatusPartialContent) {
			t.Errorf("event %d status = %#v", index, event["status"])
		}
		duration, ok := event["duration"].(float64)
		if !ok || duration < 0 {
			t.Errorf("event %d duration = %#v", index, event["duration"])
		}
		if event["bytes"] != float64(0) || event["range_request"] != false || event["content_type"] != "" {
			t.Errorf("event %d transport metadata = %#v", index, event)
		}
		for key := range event {
			switch key {
			case "time", "level", "msg", "route", "method", "status", "duration", "bytes", "range_request", "content_type":
			default:
				t.Errorf("event %d exposes unexpected field %q", index, key)
			}
		}
	}
}

func TestRouteTracingPreservesHEADRangeStreamingAndCancellation(t *testing.T) {
	t.Run("range streaming capabilities", func(t *testing.T) {
		var logs bytes.Buffer
		writer := newStreamingResponseWriter()
		implementation := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet || request.Header.Get("Range") != "bytes=4-7" {
				t.Errorf("stream request method/range = %s %q", request.Method, request.Header.Get("Range"))
			}
			controller := http.NewResponseController(response)
			if err := controller.EnableFullDuplex(); err != nil {
				t.Errorf("EnableFullDuplex: %v", err)
			}
			if err := controller.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
				t.Errorf("SetWriteDeadline: %v", err)
			}
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Fatal("traced response no longer implements http.Flusher")
			}
			response.Header().Set("Content-Range", "bytes 4-7/12")
			response.WriteHeader(http.StatusPartialContent)
			_, _ = response.Write([]byte("5678"))
			flusher.Flush()
			if err := controller.Flush(); err != nil {
				t.Errorf("ResponseController.Flush: %v", err)
			}
		})
		handler := tracedHandler(t, slog.New(slog.NewJSONHandler(&logs, nil)), RouteStream, implementation)
		request := httptest.NewRequest(http.MethodGet, "/Videos/12345678-1234-4234-8234-123456789abc/stream", nil)
		request.Header.Set("Range", "bytes=4-7")
		handler.ServeHTTP(writer, request)

		if writer.status != http.StatusPartialContent || writer.body.String() != "5678" || writer.header.Get("Content-Range") != "bytes 4-7/12" {
			t.Fatalf("stream response status/body/range = %d %q %q", writer.status, writer.body.String(), writer.header.Get("Content-Range"))
		}
		if writer.flushes != 2 || !writer.fullDuplex || !writer.writeDeadline {
			t.Fatalf("stream capabilities: flushes=%d fullDuplex=%t deadline=%t", writer.flushes, writer.fullDuplex, writer.writeDeadline)
		}
		if strings.Contains(logs.String(), "12345678-1234-4234-8234-123456789abc") || strings.Count(logs.String(), compatRequestCompletedMessage) != 1 {
			t.Fatalf("unexpected stream logs: %s", logs.String())
		}
	})

	t.Run("head", func(t *testing.T) {
		var logs bytes.Buffer
		implementation := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodHead || request.Header.Get("Range") != "bytes=0-0" {
				t.Errorf("HEAD method/range = %s %q", request.Method, request.Header.Get("Range"))
			}
			response.Header().Set("Content-Length", "12")
			response.WriteHeader(http.StatusPartialContent)
		})
		handler := tracedHandler(t, slog.New(slog.NewJSONHandler(&logs, nil)), RouteStreamHead, implementation)
		request := httptest.NewRequest(http.MethodHead, "/emby/Videos/12345678-1234-4234-8234-123456789abc/stream", nil)
		request.Header.Set("Range", "bytes=0-0")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusPartialContent || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "12" {
			t.Fatalf("HEAD response = %d body=%q length=%q", response.Code, response.Body.String(), response.Header().Get("Content-Length"))
		}
		if strings.Count(logs.String(), compatRequestCompletedMessage) != 1 {
			t.Fatalf("HEAD log events: %s", logs.String())
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		var logs bytes.Buffer
		started := make(chan struct{})
		finished := make(chan struct{})
		implementation := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.WriteHeader(http.StatusOK)
			response.(http.Flusher).Flush()
			close(started)
			<-request.Context().Done()
			close(finished)
		})
		handler := tracedHandler(t, slog.New(slog.NewJSONHandler(&logs, nil)), RouteStream, implementation)
		ctx, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(http.MethodGet, "/Videos/12345678-1234-4234-8234-123456789abc/stream", nil).WithContext(ctx)
		writer := newStreamingResponseWriter()
		served := make(chan struct{})
		go func() {
			handler.ServeHTTP(writer, request)
			close(served)
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("stream did not start")
		}
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("request cancellation did not reach stream handler")
		}
		select {
		case <-served:
		case <-time.After(time.Second):
			t.Fatal("traced stream did not return after cancellation")
		}
		if writer.flushes != 1 || strings.Count(logs.String(), compatRequestCompletedMessage) != 1 || strings.Contains(logs.String(), "12345678-1234-4234-8234-123456789abc") {
			t.Fatalf("cancelled stream result: flushes=%d logs=%s", writer.flushes, logs.String())
		}
	})
}

func TestRouteTracingOnlyWrapsDispatchedRealHandlers(t *testing.T) {
	var logs bytes.Buffer
	calls := 0
	implementation := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls++
		response.WriteHeader(http.StatusNoContent)
	})
	handler, err := New(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		Handlers: map[Route]http.Handler{
			RouteSystemPing: implementation,
			RouteItem:       implementation,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.Handler(handler)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodHead, "/System/Ping", nil),
		httptest.NewRequest(http.MethodGet, "/Items/not-a-uuid", nil),
		httptest.NewRequest(http.MethodGet, "/System//Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System/./Ping", nil),
		httptest.NewRequest(http.MethodGet, "/System%2FPing", nil),
		httptest.NewRequest(http.MethodGet, `/System\Ping`, nil),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("rejected %s %s status = %d, want 404", request.Method, request.URL.Path, response.Code)
		}
	}
	if calls != 0 || logs.Len() != 0 {
		t.Fatalf("rejected dispatch calls/logs = %d/%q, want none", calls, logs.String())
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/System/Ping", nil))
	if response.Code != http.StatusNoContent || calls != 1 || strings.Count(logs.String(), compatRequestCompletedMessage) != 1 {
		t.Fatalf("real dispatch status/calls/logs = %d/%d/%q", response.Code, calls, logs.String())
	}
}

func TestNilLoggerAndUnavailableRoutesDoNotLog(t *testing.T) {
	nilLoggerHandler, err := New(Dependencies{Handlers: map[Route]http.Handler{
		RouteSystemPing: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}),
	}})
	if err != nil {
		t.Fatalf("New with nil logger: %v", err)
	}
	nilLoggerHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/System/Ping", nil))

	var logs bytes.Buffer
	unavailable, err := New(Dependencies{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if err != nil {
		t.Fatalf("New unavailable handler: %v", err)
	}
	response := httptest.NewRecorder()
	unavailable.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/System/Ping", nil))
	if response.Code != http.StatusNotFound || logs.Len() != 0 {
		t.Fatalf("unavailable route status/log = %d %q", response.Code, logs.String())
	}
}

func tracedHandler(t *testing.T, logger *slog.Logger, route Route, implementation http.Handler) http.Handler {
	t.Helper()
	handler, err := New(Dependencies{Logger: logger, Handlers: map[Route]http.Handler{route: implementation}})
	if err != nil {
		t.Fatalf("New traced handler: %v", err)
	}
	return handler
}

type streamingResponseWriter struct {
	header        http.Header
	body          bytes.Buffer
	status        int
	flushes       int
	fullDuplex    bool
	writeDeadline bool
}

func newStreamingResponseWriter() *streamingResponseWriter {
	return &streamingResponseWriter{header: make(http.Header)}
}

func (writer *streamingResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *streamingResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *streamingResponseWriter) Write(payload []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.body.Write(payload)
}

func (writer *streamingResponseWriter) Flush() {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	writer.flushes++
}

func (writer *streamingResponseWriter) EnableFullDuplex() error {
	writer.fullDuplex = true
	return nil
}

func (writer *streamingResponseWriter) SetWriteDeadline(time.Time) error {
	writer.writeDeadline = true
	return nil
}
