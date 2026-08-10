package addon

import (
	"bytes"
	"errors"
	"strconv"
	"testing"
)

func providerStreamResponsePayload(count int) []byte {
	var payload bytes.Buffer
	payload.WriteString(`{"streams":[`)
	for index := range count {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"name":"Source `)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`","url":"https://media.example/`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`.mp4"}`)
	}
	payload.WriteString(`]}`)
	return payload.Bytes()
}

func providerSubtitleResponsePayload(count int) []byte {
	var payload bytes.Buffer
	payload.WriteString(`{"subtitles":[`)
	for index := range count {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"id":"subtitle-`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`","url":"https://media.example/`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`.vtt","lang":"en"}`)
	}
	payload.WriteString(`]}`)
	return payload.Bytes()
}

func TestProviderStreamResponseCardinalityBoundary(t *testing.T) {
	payload := providerStreamResponsePayload(MaximumProviderStreams)
	response, err := ParseProviderStreamResponse(payload)
	if err != nil {
		t.Fatalf("parse response at stream limit: %v", err)
	}
	if len(response.Streams) != MaximumProviderStreams {
		t.Fatalf("streams = %d, want %d", len(response.Streams), MaximumProviderStreams)
	}
	if err := validateResourceResponse("stream", payload); err != nil {
		t.Fatalf("transport validation at stream limit: %v", err)
	}

	overLimit := providerStreamResponsePayload(MaximumProviderStreams + 1)
	response, err = ParseProviderStreamResponse(overLimit)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("over-limit stream response error = %v", err)
	}
	if len(response.Streams) != 0 {
		t.Fatalf("over-limit response retained %d partial streams", len(response.Streams))
	}
	if err := validateResourceResponse("stream", overLimit); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("transport over-limit stream error = %v", err)
	}
}

func TestProviderSubtitleResponseCardinalityBoundary(t *testing.T) {
	payload := providerSubtitleResponsePayload(MaximumProviderSubtitles)
	response, err := ParseProviderSubtitleResponse(payload)
	if err != nil {
		t.Fatalf("parse response at subtitle limit: %v", err)
	}
	if len(response.Subtitles) != MaximumProviderSubtitles {
		t.Fatalf("subtitles = %d, want %d", len(response.Subtitles), MaximumProviderSubtitles)
	}

	response, err = ParseProviderSubtitleResponse(providerSubtitleResponsePayload(MaximumProviderSubtitles + 1))
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("over-limit subtitle response error = %v", err)
	}
	if len(response.Subtitles) != 0 {
		t.Fatalf("over-limit response retained %d partial subtitles", len(response.Subtitles))
	}
}

func TestProviderStreamResponseRejectsInvalidNestedHeadersAtomically(t *testing.T) {
	response, err := ParseProviderStreamResponse([]byte(`{"streams":[{"url":"https://media.example/valid.mp4"},{"url":"https://media.example/invalid.mp4","behaviorHints":{"proxyHeaders":{"request":{"Authorization":{"nested":true}}}}}]}`))
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("nested header response error = %v", err)
	}
	if len(response.Streams) != 0 {
		t.Fatalf("invalid nested response retained %d partial streams", len(response.Streams))
	}
}

func TestProviderStreamResponseRejectsCaseDuplicateRequestHeaders(t *testing.T) {
	response, err := ParseProviderStreamResponse([]byte(`{"streams":[{"url":"https://media.example/movie.mp4","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer first","authorization":"Bearer second"}}}}]}`))
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("case-duplicate header response error = %v", err)
	}
	if len(response.Streams) != 0 {
		t.Fatalf("case-duplicate response retained %d partial streams", len(response.Streams))
	}
}

func TestProviderStreamResponseAllowsMultilineDisplayText(t *testing.T) {
	payload := []byte(`{"streams":[{"name":"Provider\n1080p","title":"Release\tHDR","description":"Line one\nLine two\r\nLine three","url":"https://media.example/movie.mp4"}]}`)
	response, err := ParseProviderStreamResponse(payload)
	if err != nil {
		t.Fatalf("parse multiline display text: %v", err)
	}
	if len(response.Streams) != 1 || response.Streams[0].Description != "Line one\nLine two\r\nLine three" {
		t.Fatalf("multiline stream description was not preserved: %+v", response.Streams)
	}
}

func TestProviderStreamResponseRejectsUnsafeControlsOutsideDisplayText(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"streams":[{"description":"unsafe\u001btext","url":"https://media.example/movie.mp4"}]}`),
		[]byte(`{"streams":[{"description":"allowed\ntext","url":"https://media.example/movie.mp4\nheader"}]}`),
	} {
		response, err := ParseProviderStreamResponse(payload)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("unsafe provider control error = %v, want ErrInvalidResponse", err)
		}
		if len(response.Streams) != 0 {
			t.Fatalf("unsafe provider response retained %d streams", len(response.Streams))
		}
	}
}
