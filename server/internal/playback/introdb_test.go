package playback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestFetchIntroDBMarkersUsesVerifiedSegmentsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/segments" ||
			request.URL.Query().Get("imdb_id") != "tt0903747" || request.URL.Query().Get("season") != "1" || request.URL.Query().Get("episode") != "1" {
			t.Fatalf("unexpected IntroDB request %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("User-Agent") != "Rivune/1" {
			t.Fatalf("unexpected IntroDB request headers: %+v", request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"imdb_id":"tt0903747","season":1,"episode":1,
			"intro":{"start_ms":2500,"end_ms":58000,"start_sec":2.5,"end_sec":58,"confidence":0.9,"submission_count":3},
			"recap":{"start_ms":58000,"end_ms":90000,"start_sec":58,"end_sec":90,"confidence":1,"submission_count":1},
			"outro":{"start_ms":3431000,"end_ms":3500000,"start_sec":3431,"end_sec":3500,"confidence":1,"submission_count":1}
		}`))
	}))
	defer server.Close()

	service := &Service{introDBClient: server.Client(), introDBBaseURL: server.URL}
	markers, found, err := service.fetchIntroDBMarkers(context.Background(), MarkerInput{IMDBID: "tt0903747", Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("fetch IntroDB markers: %v", err)
	}
	if !found || len(markers) != 3 || markers[0].Type != MarkerTypeIntro || markers[1].Type != MarkerTypeRecap || markers[2].Type != MarkerTypeOutro {
		t.Fatalf("unexpected normalized markers: found=%v markers=%+v", found, markers)
	}
}

func TestNormalizeIntroDBMarkersRejectsInvalidBoundsUnitsAndOrder(t *testing.T) {
	markers := normalizeIntroDBMarkers(introDBResponse{
		Intro: &introDBSegment{StartMS: 10_000, EndMS: 70_000, StartSeconds: 10, EndSeconds: 70, Confidence: 1, SubmissionCount: 2},
		Recap: &introDBSegment{StartMS: 20_000, EndMS: 80_000, StartSeconds: 20, EndSeconds: 80, Confidence: 1, SubmissionCount: 1},
		Outro: &introDBSegment{StartMS: 100_000, EndMS: 90_000, StartSeconds: 100, EndSeconds: 90, Confidence: 1, SubmissionCount: 1},
	})
	if len(markers) != 1 || markers[0].Type != MarkerTypeIntro {
		t.Fatalf("invalid or overlapping markers were not rejected: %+v", markers)
	}

	unitMismatch := normalizeIntroDBMarkers(introDBResponse{
		Intro: &introDBSegment{StartMS: 10, EndMS: 70, StartSeconds: 10, EndSeconds: 70, Confidence: 1, SubmissionCount: 1},
	})
	if len(unitMismatch) != 0 {
		t.Fatalf("millisecond/second mismatch was accepted: %+v", unitMismatch)
	}
}

func TestFetchIntroDBMarkersDoesNotExposeUpstreamErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "private upstream detail", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	service := &Service{introDBClient: server.Client(), introDBBaseURL: server.URL}
	_, _, err := service.fetchIntroDBMarkers(context.Background(), MarkerInput{IMDBID: "tt0903747", Season: 1, Episode: 1})
	if err == nil || err.Error() != "IntroDB returned status 503" {
		t.Fatalf("unexpected sanitized upstream error: %v", err)
	}
}

func TestMarkersFailsOpenWhenIntroDBIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	now := time.Now().UTC()
	profileID := "profile-id"
	expiresAt := now.Add(time.Hour)
	service := &Service{
		introDBClient: server.Client(), introDBBaseURL: server.URL,
		now: func() time.Time { return now },
	}
	result, err := service.Markers(context.Background(), auth.Principal{
		ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expiresAt,
	}, MarkerInput{
		IMDBID: "tt0903747", Season: 1, Episode: 1,
		IncludeIntro: true, IncludeRecap: true, IncludeOutro: true,
	})
	if err != nil || len(result.Markers) != 0 {
		t.Fatalf("provider outage changed playback: result=%+v err=%v", result, err)
	}
}
