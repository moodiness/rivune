package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShutdownDrainsHTTPRequestBeforeCancelingJellyfinGeneration(t *testing.T) {
	compatibilityContext, cancelCompatibility := newJellyfinCompatibilityContext()
	compatibilityDone := make(chan struct{})
	go func() {
		<-compatibilityContext.Done()
		close(compatibilityDone)
	}()

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	requestDone := make(chan struct{})
	go func() {
		close(requestStarted)
		<-releaseRequest
		close(requestDone)
	}()
	<-requestStarted

	shutdownStarted := make(chan struct{})
	shutdownErr := errors.New("graceful shutdown deadline")
	result := make(chan error, 1)
	go func() {
		result <- shutdownHTTPBeforeJellyfinCompatibility(func() error {
			close(shutdownStarted)
			<-requestDone
			return shutdownErr
		}, cancelCompatibility, compatibilityDone)
	}()
	<-shutdownStarted
	select {
	case <-compatibilityContext.Done():
		t.Fatal("Jellyfin generation was canceled while the HTTP request was still draining")
	default:
	}

	close(releaseRequest)
	select {
	case err := <-result:
		if !errors.Is(err, shutdownErr) {
			t.Fatalf("shutdown error=%v want=%v", err, shutdownErr)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("shutdown did not cancel and await Jellyfin after the request drained")
	}
	select {
	case <-compatibilityDone:
	default:
		t.Fatal("Jellyfin generation cleanup was not awaited")
	}
}

func TestDefaultHealthCheckTargetsReadiness(t *testing.T) {
	if defaultHealthCheckURL != "http://127.0.0.1:8080/ready" {
		t.Fatalf("default healthcheck URL = %q, want readiness endpoint", defaultHealthCheckURL)
	}
}

func TestCheckHealthRequiresSuccessfulEndpoint(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{name: "healthy", statusCode: http.StatusOK},
		{name: "unhealthy", statusCode: http.StatusServiceUnavailable, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.statusCode)
			}))
			defer server.Close()
			err := checkHealth(context.Background(), server.URL)
			if (err != nil) != test.wantError {
				t.Fatalf("check health error = %v, wantError=%t", err, test.wantError)
			}
		})
	}
}

func TestHTTPServerAcceptsReadinessWhileSemanticWarmupRuns(t *testing.T) {
	started := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ready" {
			http.NotFound(response, request)
			return
		}
		response.WriteHeader(http.StatusOK)
	})}
	runtime, err := startHTTPServer(context.Background(), server, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		runtime.stopWarmup()
	})
	<-started

	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + runtime.listener.Addr().String() + "/ready")
	if err != nil {
		t.Fatalf("request readiness during warmup: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("readiness during warmup = %s", response.Status)
	}
}

func TestHTTPServerBindFailureDoesNotStartSemanticWarmup(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	warmupStarted := make(chan struct{}, 1)
	server := &http.Server{Addr: occupied.Addr().String(), Handler: http.NotFoundHandler()}

	runtime, err := startHTTPServer(context.Background(), server, func(context.Context) error {
		warmupStarted <- struct{}{}
		return nil
	}, discardLogger())
	if err == nil || runtime != nil {
		if runtime != nil {
			_ = server.Close()
			runtime.stopWarmup()
		}
		t.Fatalf("bind result runtime=%#v error=%v", runtime, err)
	}
	select {
	case <-warmupStarted:
		t.Fatal("semantic warmup started after bind failure")
	default:
	}
}

func TestHTTPServerWarmupIsCanceledAndAwaited(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	runtime, err := startHTTPServer(context.Background(), server, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	<-started
	stopped := make(chan struct{})
	go func() {
		runtime.stopWarmup()
		close(stopped)
	}()
	<-canceled
	select {
	case <-stopped:
		t.Fatal("warmup stop returned before worker completed")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("warmup worker was not joined")
	}
}

func TestSemanticWarmupFailureDoesNotStopHTTPServer(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	})}
	runtime, err := startHTTPServer(context.Background(), server, func(context.Context) error {
		return errors.New("warmup unavailable")
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		runtime.stopWarmup()
	})
	<-runtime.warmupDone
	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + runtime.listener.Addr().String() + "/ready")
	if err != nil {
		t.Fatalf("request after failed warmup: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("server after failed warmup = %s", response.Status)
	}
	if !strings.Contains(logs.String(), "warm semantic extension") || !strings.Contains(logs.String(), "warmup unavailable") {
		t.Fatalf("warmup failure was not logged: %s", logs.String())
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}
