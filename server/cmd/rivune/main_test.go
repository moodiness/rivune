package main

import (
	"errors"
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
