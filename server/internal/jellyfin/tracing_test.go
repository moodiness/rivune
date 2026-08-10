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

	"github.com/moodiness/rivune/server/internal/requestwork"
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
		requestwork.BeginDB(request.Context(), 10)
		requestwork.EndDB(request.Context(), 40)
		requestwork.BeginOutbound(request.Context(), 20)
		requestwork.EndOutbound(request.Context(), 70, 321)
		response.Header().Set("Content-Type", `application/json; boundary="`+headerSecret+`"`)
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
		if event["db_call_count"] != float64(1) || event["db_duration"] != float64(30) ||
			event["outbound_call_count"] != float64(1) || event["outbound_duration"] != float64(50) ||
			event["upstream_bytes"] != float64(321) {
			t.Errorf("event %d work metadata = %#v", index, event)
		}
		if event["bytes"] != float64(0) || event["range_request"] != false || event["content_type"] != "application/json" {
			t.Errorf("event %d transport metadata = %#v", index, event)
		}
		for key := range event {
			switch key {
			case "time", "level", "msg", "route", "method", "status", "duration", "db_call_count", "db_duration",
				"outbound_call_count", "outbound_duration", "upstream_bytes", "bytes", "range_request", "content_type":
			default:
				t.Errorf("event %d exposes unexpected field %q", index, key)
			}
		}
	}
}

func TestRouteErrorTracingIncludesOnlySafeRequestShapeWithoutDebug(t *testing.T) {
	const querySecret = "query-value-SENTINEL"
	var logs bytes.Buffer
	handler := tracedHandler(t, slog.New(slog.NewJSONHandler(&logs, nil)), RouteLatestItems,
		http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusBadRequest)
			_, _ = response.Write([]byte(`{"ResponseStatus":{"ErrorCode":"BadRequest"}}`))
		}))
	request := httptest.NewRequest(http.MethodGet, "/Items/Latest?Fields=Chapters&api_key="+querySecret, nil)
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="private-device-SENTINEL", DeviceId="device-id-SENTINEL", Version="8.1", Token="token-SENTINEL"`)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	rawLog := strings.TrimSpace(logs.String())
	for _, forbidden := range []string{querySecret, "Chapters", "private-device-SENTINEL", "device-id-SENTINEL", "token-SENTINEL"} {
		if strings.Contains(rawLog, forbidden) {
			t.Fatalf("error trace contains forbidden value %q: %s", forbidden, rawLog)
		}
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(rawLog), &event); err != nil {
		t.Fatalf("decode error trace: %v", err)
	}
	if event["status"] != float64(http.StatusBadRequest) || event["client_family"] != "infuse" ||
		event["client_version_major"] != "8" || event["client_metadata_present"] != true ||
		!equalJSONStrings(event["query_names"], []string{"Fields", "api_key"}) ||
		!equalJSONNumbers(event["query_cardinalities"], []float64{1, 1}) {
		t.Fatalf("unexpected error trace metadata: %#v", event)
	}
}

func TestRouteTracingEmitsZeroWorkWithoutInstrumentation(t *testing.T) {
	var logs bytes.Buffer
	handler := tracedHandler(t, slog.New(slog.NewJSONHandler(&logs, nil)), RouteSystemPing,
		http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/System/Ping", nil))

	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatalf("decode completed event: %v", err)
	}
	for _, field := range []string{"db_call_count", "db_duration", "outbound_call_count", "outbound_duration", "upstream_bytes"} {
		if event[field] != float64(0) {
			t.Fatalf("%s = %#v, want zero: %#v", field, event[field], event)
		}
	}
}

func TestRouteDebugTracingIsOptInBoundedAndNeverLogsValues(t *testing.T) {
	const (
		querySecret    = "query-value-SENTINEL"
		headerSecret   = "header-value-SENTINEL"
		bodySecret     = "body-value-SENTINEL"
		tokenSecret    = "token-value-SENTINEL"
		itemSecret     = "item-id-SENTINEL"
		sessionSecret  = "session-id-SENTINEL"
		providerSecret = "https://provider.invalid/media?password=provider-SENTINEL"
	)
	var logs bytes.Buffer
	implementation := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil || string(payload) != bodySecret || request.Header.Get("X-Sentinel") != headerSecret {
			t.Fatalf("debug tracing changed request: body=%q err=%v headers=%v", payload, err, request.Header)
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8; boundary=content-type-SENTINEL")
		response.Header().Set("Content-Range", "items 0-0/1")
		_, _ = response.Write([]byte(`{"Items":[{"Id":"` + itemSecret + `","SessionId":"` + sessionSecret + `","Path":"` + providerSecret + `"}],"TotalRecordCount":1}`))
	})
	handler, err := New(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		Debug:  true,
		Handlers: map[Route]http.Handler{
			RoutePlaybackInfoPost: implementation,
		},
	})
	if err != nil {
		t.Fatalf("New debug handler: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/Items/12345678-1234-4234-8234-123456789abc/PlaybackInfo?Fields=Path&Fields=MediaSources&Limit=1&api_key="+querySecret, strings.NewReader(bodySecret))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Range", "bytes=0-1")
	request.Header.Set("X-Sentinel", headerSecret)
	request.Header.Set("Cookie", "sid=cookie-value-SENTINEL")
	request.Header.Set("User-Agent", "user-agent-SENTINEL")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="private-device-SENTINEL", DeviceId="device-id-SENTINEL", Version="8.2", Token="`+tokenSecret+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("debug response status=%d body=%s", response.Code, response.Body.String())
	}
	rawLog := strings.TrimSpace(logs.String())
	for _, forbidden := range []string{
		querySecret, headerSecret, bodySecret, tokenSecret, itemSecret, sessionSecret, providerSecret,
		"content-type-SENTINEL", "cookie-value-SENTINEL", "user-agent-SENTINEL", "private-device-SENTINEL", "device-id-SENTINEL", "12345678-1234-4234-8234-123456789abc",
	} {
		if strings.Contains(rawLog, forbidden) {
			t.Errorf("debug log contains forbidden value %q: %s", forbidden, rawLog)
		}
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(rawLog), &event); err != nil {
		t.Fatalf("decode debug event: %v", err)
	}
	if event["route"] != string(RoutePlaybackInfoPost) || event["method"] != http.MethodPost ||
		event["status"] != float64(http.StatusOK) || event["bytes"] != float64(response.Body.Len()) || event["content_type"] != "application/json" ||
		event["client_family"] != "generic-client" || event["client_version_major"] != "8" || event["client_metadata_present"] != true ||
		event["range_request"] != true || event["range_response"] != true || event["json_top_level"] != "object" {
		t.Fatalf("debug event metadata=%#v", event)
	}
	if duration, ok := event["duration"].(float64); !ok || duration < 0 {
		t.Fatalf("debug duration=%#v", event["duration"])
	}
	if got := event["query_names"]; !equalJSONStrings(got, []string{"Fields", "Limit", "api_key"}) {
		t.Fatalf("debug query names=%#v", got)
	}
	if got := event["query_cardinalities"]; !equalJSONNumbers(got, []float64{2, 1, 1}) {
		t.Fatalf("debug query cardinalities=%#v", got)
	}
	if got := event["headers_present"]; !equalJSONStrings(got, []string{"Content-Type", "Range", "User-Agent", "X-Emby-Authorization"}) {
		t.Fatalf("debug header presence=%#v", got)
	}
	if got := event["json_fields"]; !equalJSONStrings(got, []string{"Items", "TotalRecordCount"}) {
		t.Fatalf("debug JSON fields=%#v", got)
	}
}

func equalJSONStrings(value any, want []string) bool {
	values, ok := value.([]any)
	if !ok || len(values) != len(want) {
		return false
	}
	for index := range want {
		if values[index] != want[index] {
			return false
		}
	}
	return true
}

func equalJSONNumbers(value any, want []float64) bool {
	values, ok := value.([]any)
	if !ok || len(values) != len(want) {
		return false
	}
	for index := range want {
		if values[index] != want[index] {
			return false
		}
	}
	return true
}

func TestDebugTraceQueryAndJSONFieldNamesAreBounded(t *testing.T) {
	var query strings.Builder
	var payload strings.Builder
	payload.WriteByte('{')
	for index := range maximumCompatTraceQueryNames + 8 {
		name := "Field" + string(rune('A'+index/26)) + string(rune('A'+index%26))
		if index != 0 {
			query.WriteByte('&')
			payload.WriteByte(',')
		}
		query.WriteString(name + "=secret-value-SENTINEL")
		payload.WriteString(`"` + name + `":"secret-value-SENTINEL"`)
	}
	payload.WriteByte('}')
	request := httptest.NewRequest(http.MethodGet, "/Items?"+query.String(), nil)
	queryNames, cardinalities := compatTraceQueryShape(request)
	if len(queryNames) != maximumCompatTraceQueryNames || len(cardinalities) != maximumCompatTraceQueryNames {
		t.Fatalf("bounded query shape names=%d cardinalities=%d", len(queryNames), len(cardinalities))
	}
	shape, fields := compatTraceJSONShape([]byte(payload.String()))
	if shape != "object" || len(fields) != maximumCompatTraceJSONFields {
		t.Fatalf("bounded JSON shape=%q fields=%d", shape, len(fields))
	}
	if strings.Contains(strings.Join(queryNames, ","), "secret-value-SENTINEL") || strings.Contains(strings.Join(fields, ","), "secret-value-SENTINEL") {
		t.Fatal("debug shape included a value")
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
		handler := tracedDebugHandler(t, slog.New(slog.NewJSONHandler(&logs, nil)), RouteStream, implementation)
		request := httptest.NewRequest(http.MethodGet, "/Videos/12345678-1234-4234-8234-123456789abc/stream", nil)
		request.Header.Set("Range", "bytes=4-7")
		handler.ServeHTTP(writer, request)

		if writer.status != http.StatusPartialContent || writer.body.String() != "5678" || writer.header.Get("Content-Range") != "bytes 4-7/12" {
			t.Fatalf("stream response status/body/range = %d %q %q", writer.status, writer.body.String(), writer.header.Get("Content-Range"))
		}
		if writer.flushes != 2 || !writer.fullDuplex || !writer.writeDeadline {
			t.Fatalf("stream capabilities: flushes=%d fullDuplex=%t deadline=%t", writer.flushes, writer.fullDuplex, writer.writeDeadline)
		}
		if strings.Contains(logs.String(), "12345678-1234-4234-8234-123456789abc") || strings.Contains(logs.String(), "5678") || strings.Count(logs.String(), compatRequestCompletedMessage) != 1 {
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

func tracedDebugHandler(t *testing.T, logger *slog.Logger, route Route, implementation http.Handler) http.Handler {
	t.Helper()
	handler, err := New(Dependencies{Logger: logger, Debug: true, Handlers: map[Route]http.Handler{route: implementation}})
	if err != nil {
		t.Fatalf("New debug traced handler: %v", err)
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
