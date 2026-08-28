package requestwork

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRequestIDAcceptsSafeValueAndReplacesInvalidValues(t *testing.T) {
	const supplied = "edge-01:request_42.v1"
	ctx, requestID := WithRequestID(context.Background(), supplied)
	if requestID != supplied || RequestID(ctx) != supplied {
		t.Fatalf("accepted request ID = %q context=%q", requestID, RequestID(ctx))
	}
	boundary := strings.Repeat("z", 128)
	boundaryContext, boundaryID := WithRequestID(context.Background(), boundary)
	if boundaryID != boundary || RequestID(boundaryContext) != boundary {
		t.Fatalf("128-character request ID was replaced: %q", boundaryID)
	}

	invalid := []string{"", "has whitespace", "line\nbreak", "quoted\"value", "slash/value", "comma,value", strings.Repeat("a", 129), "nonascii-é"}
	seen := make(map[string]struct{}, len(invalid))
	for _, value := range invalid {
		ctx, generated := WithRequestID(context.Background(), value)
		if len(generated) != 32 || !ValidRequestID(generated) || generated == value || RequestID(ctx) != generated {
			t.Fatalf("replacement for %q = %q context=%q", value, generated, RequestID(ctx))
		}
		if _, duplicate := seen[generated]; duplicate {
			t.Fatalf("generated duplicate request ID %q", generated)
		}
		seen[generated] = struct{}{}
	}
}

func TestPropagateRequestIDCopiesContextValueOnly(t *testing.T) {
	ctx, requestID := WithRequestID(context.Background(), "caller-request-7")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://provider.example/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	PropagateRequestID(request)
	if got := request.Header.Get(RequestIDHeader); got != requestID {
		t.Fatalf("outbound request ID = %q, want %q", got, requestID)
	}

	withoutContext, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://provider.example/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	withoutContext.Header.Set(RequestIDHeader, "transport-owned")
	PropagateRequestID(withoutContext)
	if got := withoutContext.Header.Get(RequestIDHeader); got != "transport-owned" {
		t.Fatalf("helper changed header without context: %q", got)
	}
}

func TestObservedBodyCountsBytesOnceAtEOForClose(t *testing.T) {
	ctx, counters := WithCounters(context.Background())
	started := Now()
	BeginOutbound(ctx, started)
	body := ObserveBody(ctx, io.NopCloser(strings.NewReader("private-provider-payload")))
	payload, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read observed body: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close observed body: %v", err)
	}

	snapshot := counters.Snapshot()
	if snapshot.OutboundCalls != 1 || snapshot.UpstreamBytes != int64(len(payload)) || snapshot.OutboundDuration < 0 {
		t.Fatalf("observed body snapshot = %+v", snapshot)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestBoundedHTTPClientSupportsCustomTransport(t *testing.T) {
	custom := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ProtoMajor:    1,
			ContentLength: 2,
			Body:          io.NopCloser(strings.NewReader("ok")),
			Header:        make(http.Header),
			Request:       request,
		}, nil
	})
	response, err := BoundedHTTPClient(&http.Client{Transport: custom}).Get("https://custom.example/resource")
	if err != nil {
		t.Fatalf("custom transport request: %v", err)
	}
	payload, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || string(payload) != "ok" {
		t.Fatalf("custom response = (%q, %v, close %v), want ok", payload, readErr, closeErr)
	}
}

func TestBoundedHTTPClientSupportsDefaultTransports(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	t.Cleanup(server.Close)

	clients := map[string]*http.Client{
		"nil client":    BoundedHTTPClient(nil),
		"nil transport": BoundedHTTPClient(&http.Client{}),
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatalf("default transport request: %v", err)
			}
			payload, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil || string(payload) != "ok" {
				t.Fatalf("default response = (%q, %v, close %v), want ok", payload, readErr, closeErr)
			}
		})
	}
}

func TestBoundedHTTPClientPreservesHTTP2Reuse(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client := BoundedHTTPClient(server.Client())
	first, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("first HTTP/2 request: %v", err)
	}
	firstPayload, readErr := io.ReadAll(first.Body)
	closeErr := first.Body.Close()
	if readErr != nil || closeErr != nil || first.ProtoMajor != 2 || string(firstPayload) != "ok" {
		t.Fatalf("first HTTP/2 response = (HTTP/%d, %q, %v, close %v)", first.ProtoMajor, firstPayload, readErr, closeErr)
	}

	gotConnection := make(chan httptrace.GotConnInfo, 1)
	request, err := http.NewRequestWithContext(
		httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			gotConnection <- info
		}}),
		http.MethodGet,
		server.URL,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Do(request)
	if err != nil {
		t.Fatalf("second HTTP/2 request: %v", err)
	}
	secondPayload, readErr := io.ReadAll(second.Body)
	closeErr = second.Body.Close()
	if readErr != nil || closeErr != nil || second.ProtoMajor != 2 || string(secondPayload) != "ok" {
		t.Fatalf("second HTTP/2 response = (HTTP/%d, %q, %v, close %v)", second.ProtoMajor, secondPayload, readErr, closeErr)
	}
	if connection := <-gotConnection; !connection.Reused {
		t.Fatal("HTTP/2 connection was not reused")
	}
}

func TestBoundedHTTPClientDoesNotReuseClosedBufferedConnection(t *testing.T) {
	const bufferedBodySize = 2304
	var secondAttempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/first":
			writer.Header().Set("Content-Length", fmt.Sprint(bufferedBodySize))
			_, _ = io.WriteString(writer, strings.Repeat("x", bufferedBodySize))
		case "/second":
			secondAttempts.Add(1)
			_, _ = io.WriteString(writer, "ok")
		}
	}))
	t.Cleanup(server.Close)

	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = 1
	t.Cleanup(transport.CloseIdleConnections)
	client := BoundedHTTPClient(&http.Client{Transport: transport})
	first, err := client.Get(server.URL + "/first")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	waitingForConnection := make(chan struct{}, 1)
	gotSecondConnection := make(chan httptrace.GotConnInfo, 1)
	secondRequest, err := http.NewRequestWithContext(
		httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
			GetConn: func(string) { waitingForConnection <- struct{}{} },
			GotConn: func(info httptrace.GotConnInfo) { gotSecondConnection <- info },
		}),
		http.MethodPost,
		server.URL+"/second",
		io.LimitReader(strings.NewReader("x"), 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan error, 1)
	go func() {
		response, requestErr := client.Do(secondRequest)
		if requestErr == nil {
			var payload []byte
			payload, requestErr = io.ReadAll(response.Body)
			_ = response.Body.Close()
			if requestErr == nil && string(payload) != "ok" {
				requestErr = fmt.Errorf("second response = %q, want ok", payload)
			}
		}
		secondResult <- requestErr
	}()

	<-waitingForConnection
	if err := first.Body.Close(); err != nil {
		t.Fatalf("close first response: %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("non-replayable request after buffered close: %v", err)
	}
	if connection := <-gotSecondConnection; connection.Reused {
		t.Fatal("second request reused the abandoned response connection")
	}
	if got := secondAttempts.Load(); got != 1 {
		t.Fatalf("second request attempts = %d, want 1", got)
	}
}

func TestBoundedHTTPClientKeepsConcurrentRequestSafeAfterBodylessResponse(t *testing.T) {
	secondStarted := make(chan struct{}, 1)
	releaseSecond := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSecond) }) }
	var secondAttempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/empty":
			writer.WriteHeader(http.StatusNoContent)
		case "/second":
			secondAttempts.Add(1)
			secondStarted <- struct{}{}
			<-releaseSecond
			_, _ = io.WriteString(writer, "ok")
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(release)

	client := BoundedHTTPClient(server.Client())
	first, err := client.Get(server.URL + "/empty")
	if err != nil {
		t.Fatalf("bodyless request: %v", err)
	}

	gotConnection := make(chan httptrace.GotConnInfo, 1)
	secondRequest, err := http.NewRequestWithContext(
		httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			gotConnection <- info
		}}),
		http.MethodGet,
		server.URL+"/second",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan error, 1)
	go func() {
		response, requestErr := client.Do(secondRequest)
		if requestErr == nil {
			var payload []byte
			payload, requestErr = io.ReadAll(response.Body)
			_ = response.Body.Close()
			if requestErr == nil && string(payload) != "ok" {
				requestErr = fmt.Errorf("second response = %q, want ok", payload)
			}
		}
		secondResult <- requestErr
	}()

	connection := <-gotConnection
	<-secondStarted
	if connection.Reused {
		release()
		<-secondResult
		t.Fatal("bodyless response connection was reused")
	}
	if err := first.Body.Close(); err != nil {
		release()
		<-secondResult
		t.Fatalf("close bodyless response: %v", err)
	}
	release()
	if err := <-secondResult; err != nil {
		t.Fatalf("concurrent request after bodyless response: %v", err)
	}
	if got := secondAttempts.Load(); got != 1 {
		t.Fatalf("concurrent request attempts = %d, want 1", got)
	}
}

func TestBoundedHTTPClientPreservesDecodedGzipResponse(t *testing.T) {
	payload := strings.Repeat("x", 32<<10)
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(gzipWriter, payload); err != nil {
		t.Fatalf("compress response: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", fmt.Sprint(compressed.Len()))
		_, _ = writer.Write(compressed.Bytes())
	}))
	t.Cleanup(server.Close)

	response, err := BoundedHTTPClient(server.Client()).Get(server.URL)
	if err != nil {
		t.Fatalf("gzip request: %v", err)
	}
	if !response.Uncompressed {
		_ = response.Body.Close()
		t.Fatal("gzip response was not transparently decoded")
	}
	decoded, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || string(decoded) != payload {
		t.Fatalf("decoded gzip response = (%d bytes, %v, close %v), want %d bytes", len(decoded), readErr, closeErr, len(payload))
	}
}
