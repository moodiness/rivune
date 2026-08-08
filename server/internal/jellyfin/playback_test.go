package jellyfin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestPlaybackCapabilitiesTranslateDirectHLSRemuxTranscodeSubtitlesAndBitrate(t *testing.T) {
	capabilities, transcode, err := playbackCapabilities(PlaybackInfoRequest{
		MaxStreamingBitrate: 8_000_000,
		DeviceProfile: DeviceProfile{
			MaxStreamingBitrate: 12_000_000,
			DirectPlayProfiles:  []DirectPlayProfile{{Container: "mkv,mp4", VideoCodec: "h264,hevc", AudioCodec: "aac,ac3", Type: "Video"}},
			TranscodingProfiles: []TranscodingProfile{{Container: "ts", VideoCodec: "h264", AudioCodec: "aac", Protocol: "hls", Context: "Streaming", Type: "Video"}},
			SubtitleProfiles:    []SubtitleProfile{{Format: "srt", Method: "External"}, {Format: "pgs", Method: "Encode"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !transcode || capabilities.MaximumVideoBitrateKbps != 8000 || capabilities.TranscodeVideoBitrateKbps != 8000 {
		t.Fatalf("bitrate/transcode mapping = %#v transcode=%v", capabilities, transcode)
	}
	for _, value := range []string{"http", "hls"} {
		if !containsFold(capabilities.StreamingProtocols, value) {
			t.Fatalf("missing protocol %q in %#v", value, capabilities.StreamingProtocols)
		}
	}
	for _, value := range []string{"remux", "transcode_audio", "transcode"} {
		if !containsFold(capabilities.ProcessingModes, value) {
			t.Fatalf("missing processing mode %q in %#v", value, capabilities.ProcessingModes)
		}
	}
	for _, value := range []string{"external", "burn"} {
		if !containsFold(capabilities.SubtitleModes, value) {
			t.Fatalf("missing subtitle mode %q in %#v", value, capabilities.SubtitleModes)
		}
	}
	if len(capabilities.MediaProfiles) != 8 {
		t.Fatalf("media profiles = %d, want 8 deduplicated profiles: %#v", len(capabilities.MediaProfiles), capabilities.MediaProfiles)
	}
}

func TestPlaybackCapabilitiesHonorModernFlagsAndSegmentContainer(t *testing.T) {
	falseValue, trueValue := false, true
	profile := DeviceProfile{
		DirectPlayProfiles:  []DirectPlayProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Type: "Video"}},
		TranscodingProfiles: []TranscodingProfile{{Container: "ts", VideoCodec: "h264", AudioCodec: "aac", Protocol: "hls", Context: "Streaming", Type: "Video"}},
	}
	capabilities, transcode, err := playbackCapabilities(PlaybackInfoRequest{
		DeviceProfile: profile, MaxAudioChannels: 6,
		EnableDirectPlay: &falseValue, EnableDirectStream: &trueValue, EnableTranscoding: &trueValue,
		AllowVideoStreamCopy: &falseValue, AllowAudioStreamCopy: &trueValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !transcode || capabilities.HLSSegmentContainer != "ts" || capabilities.MaximumAudioChannels != 6 ||
		playbackFlag(capabilities.PreferDirectPlay, true) || containsFold(capabilities.ProcessingModes, "remux") ||
		containsFold(capabilities.ProcessingModes, "transcode_audio") || !containsFold(capabilities.ProcessingModes, "transcode") {
		t.Fatalf("modern flags were not preserved: capabilities=%+v transcode=%t", capabilities, transcode)
	}
	profile.TranscodingProfiles[0].Container = "mp4"
	capabilities, _, err = playbackCapabilities(PlaybackInfoRequest{DeviceProfile: profile})
	if err != nil || capabilities.HLSSegmentContainer != "mp4" {
		t.Fatalf("mp4-only HLS profile capabilities=%+v err=%v", capabilities, err)
	}
}

func TestPlaybackCapabilitiesKeepHLSCodecsBoundToSelectedSegmentContainer(t *testing.T) {
	profiles := []TranscodingProfile{
		{Container: "mp4", VideoCodec: "av1", AudioCodec: "opus", Protocol: "hls", Context: "Streaming", Type: "Video"},
		{Container: "ts", VideoCodec: "h264", AudioCodec: "aac", Protocol: "hls", Context: "Streaming", Type: "Video"},
	}
	for _, ordered := range [][]TranscodingProfile{profiles, []TranscodingProfile{profiles[1], profiles[0]}} {
		capabilities, allowTranscode, err := playbackCapabilities(PlaybackInfoRequest{DeviceProfile: DeviceProfile{TranscodingProfiles: ordered}})
		if err != nil {
			t.Fatal(err)
		}
		if !allowTranscode || capabilities.HLSSegmentContainer != "ts" || len(capabilities.MediaProfiles) != 1 {
			t.Fatalf("mixed HLS selection lost its profile association: capabilities=%+v allowTranscode=%t", capabilities, allowTranscode)
		}
		selected := capabilities.MediaProfiles[0]
		if selected.Container != "mp4" || selected.VideoCodec != "h264" || selected.AudioCodec != "aac" {
			t.Fatalf("TS output inherited fMP4-only codecs: %+v", capabilities.MediaProfiles)
		}
	}
}

func TestParsePlaybackInfoRequestAcceptsModernQueryFlagsAndTrackIndices(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/Items/"+routeTestUUID+"/PlaybackInfo?EnableDirectPlay=false&EnableDirectStream=true&EnableTranscoding=false&AllowVideoStreamCopy=false&AllowAudioStreamCopy=true&AudioStreamIndex=2&SubtitleStreamIndex=7&MaxAudioChannels=6", strings.NewReader(`{}`))
	input, err := parsePlaybackInfoRequest(httptest.NewRecorder(), request)
	if err != nil {
		t.Fatal(err)
	}
	if playbackFlag(input.EnableDirectPlay, true) || !playbackFlag(input.EnableDirectStream, false) || playbackFlag(input.EnableTranscoding, true) ||
		playbackFlag(input.AllowVideoStreamCopy, true) || !playbackFlag(input.AllowAudioStreamCopy, false) || input.AudioStreamIndex == nil || *input.AudioStreamIndex != 2 ||
		input.SubtitleStreamIndex == nil || *input.SubtitleStreamIndex != 7 || input.MaxAudioChannels != 6 {
		t.Fatalf("parsed modern playback query=%+v", input)
	}
}

func TestPlaybackInfoAcceptsVidHubBitrateAndClampsNativeCapability(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "vidhub-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	body := `{"DeviceProfile":{"MaxStreamingBitrate":500000000,"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`
	request := httptest.NewRequest(http.MethodPost, "/Items/"+fixture.item.ID+"/PlaybackInfo?StartTimeTicks=0&AutoOpenLiveStream=false&UserId="+fixture.authentication.session.ProfileID+"&MaxStreamingBitrate=500000000&reqformat=json&IsPlayback=true", strings.NewReader(body))
	request.SetPathValue("id", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	response := httptest.NewRecorder()
	fixture.handler.handlePlaybackInfo(response, request)
	var result PlaybackInfoResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || result.PlaySessionId == "" {
		t.Fatalf("VidHub PlaybackInfo status=%d result=%+v err=%v body=%s", response.Code, result, err, response.Body.String())
	}
	entry := fixture.handler.playSessions.entries[result.PlaySessionId]
	if entry == nil || entry.capabilities.MaximumVideoBitrateKbps != int(maximumCompatPlaybackBitrate/1000) {
		t.Fatalf("VidHub bitrate was not safely clamped: %+v", entry)
	}

	overflow := httptest.NewRequest(http.MethodPost, "/Items/"+fixture.item.ID+"/PlaybackInfo?MaxStreamingBitrate=2147483648", strings.NewReader(body))
	overflow.SetPathValue("id", fixture.item.ID)
	overflow.Header.Set("X-Emby-Token", fixture.token)
	overflowResponse := httptest.NewRecorder()
	fixture.handler.handlePlaybackInfo(overflowResponse, overflow)
	if overflowResponse.Code != http.StatusBadRequest {
		t.Fatalf("out-of-int32 bitrate status=%d body=%s", overflowResponse.Code, overflowResponse.Body.String())
	}
}

func TestPlaybackCapabilitiesRejectUnsupportedAndUnboundedProfiles(t *testing.T) {
	tests := []struct {
		name  string
		input PlaybackInfoRequest
	}{
		{name: "unsupported protocol", input: PlaybackInfoRequest{DeviceProfile: DeviceProfile{TranscodingProfiles: []TranscodingProfile{{Protocol: "http", VideoCodec: "h264", AudioCodec: "aac", Type: "Video"}}}}},
		{name: "bitrate below native bound", input: PlaybackInfoRequest{MaxStreamingBitrate: 63_999, DeviceProfile: DeviceProfile{DirectPlayProfiles: []DirectPlayProfile{{Container: "mp4", VideoCodec: "h264"}}}}},
		{name: "profile count", input: PlaybackInfoRequest{DeviceProfile: DeviceProfile{DirectPlayProfiles: make([]DirectPlayProfile, 33)}}},
		{name: "cross product", input: PlaybackInfoRequest{DeviceProfile: DeviceProfile{DirectPlayProfiles: []DirectPlayProfile{{Container: "mp4,mkv,webm,avi", VideoCodec: "h264,hevc,vp9,av1", AudioCodec: "aac,ac3,eac3,opus", Type: "Video"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := playbackCapabilities(test.input); err == nil {
				t.Fatal("unsupported/unbounded profile was accepted")
			}
		})
	}
}

func TestPlaybackUsesStoredDeviceProfileAndConservativePostedFallback(t *testing.T) {
	t.Run("stored session profile", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "stored-profile-source", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour)}}}
		body := `{"PlayableMediaTypes":["Video"],"SupportsMediaControl":true,"DeviceProfile":{"Name":"Generic Client","DirectPlayProfiles":[{"Container":"mkv","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`
		request := httptest.NewRequest(http.MethodPost, "/Sessions/Capabilities/Full", strings.NewReader(body))
		request.Header.Set("X-Emby-Token", fixture.token)
		response := httptest.NewRecorder()
		fixture.handler.handleSessionCapabilitiesFull(response, request)
		stored, ok := fixture.handler.playSessions.deviceProfile(fixture.authentication.session)
		if response.Code != http.StatusNoContent || !ok || stored.Name != "Generic Client" || len(stored.DirectPlayProfiles) != 1 {
			t.Fatalf("stored capabilities status=%d ok=%t profile=%+v", response.Code, ok, stored)
		}
		playbackResponse := fixture.playbackInfo(http.MethodPost, `{}`)
		var result PlaybackInfoResponse
		if err := json.Unmarshal(playbackResponse.Body.Bytes(), &result); err != nil || playbackResponse.Code != http.StatusOK || len(result.MediaSources) != 1 ||
			!result.MediaSources[0].SupportsDirectPlay || result.MediaSources[0].SupportsTranscoding {
			t.Fatalf("stored profile playback status=%d result=%+v err=%v", playbackResponse.Code, result, err)
		}
		logout := httptest.NewRequest(http.MethodPost, "/Sessions/Logout", nil)
		logout.Header.Set("X-Emby-Token", fixture.token)
		logoutResponse := httptest.NewRecorder()
		fixture.handler.handleLogout(logoutResponse, logout)
		if _, stillStored := fixture.handler.playSessions.deviceProfile(fixture.authentication.session); logoutResponse.Code != http.StatusNoContent || stillStored {
			t.Fatalf("logout status=%d retainedDeviceProfile=%t", logoutResponse.Code, stillStored)
		}
	})

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "empty posted profile", body: `{}`},
		{name: "bounded broad posted profile", body: `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4,mkv,webm,avi","VideoCodec":"h264,hevc,vp9,av1","AudioCodec":"aac,ac3,eac3,opus","Type":"Video"}]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlaybackFixture(t)
			fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "fallback-profile-source", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour)}}}
			response := fixture.playbackInfo(http.MethodPost, test.body)
			var result PlaybackInfoResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || len(result.MediaSources) != 1 ||
				result.MediaSources[0].SupportsDirectPlay || !result.MediaSources[0].SupportsTranscoding || !strings.Contains(result.MediaSources[0].TranscodingUrl, "/master.m3u8?") {
				t.Fatalf("fallback profile status=%d result=%+v err=%v", response.Code, result, err)
			}
		})
	}
}

func TestPlaybackInfoListsMultipleSourcesInOrderAndEagerlyInspectsFirstWithoutDisclosure(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "source-ref-provider-secret-1", Name: "https://provider.invalid/watch?token=provider-secret", Protocol: "http", Container: "mp4", ReportedHeight: 1080, ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-ref-provider-secret-2", Name: "Bearer provider-name-secret; Cookie=provider-cookie", Protocol: "http", Container: "mkv", ReportedHeight: 2160, ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-ref-provider-secret-external", Name: "External", Protocol: "external", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	body := fmt.Sprintf(`{"UserId":%q,"MaxStreamingBitrate":5000000,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4,mkv","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`, fixture.authentication.session.ProfileID)
	response := fixture.playbackInfo(http.MethodPost, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result PlaybackInfoResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.openCalls != 1 || len(fixture.delivery.inputs) != 1 || fixture.delivery.inputs[0].SourceRef != "source-ref-provider-secret-1" || len(result.MediaSources) != 2 {
		t.Fatalf("eager first source: opens=%d inputs=%#v sources=%#v", fixture.delivery.openCalls, fixture.delivery.inputs, result.MediaSources)
	}
	if result.MediaSources[0].Id != fixture.item.ID || result.MediaSources[0].Name != "Source 1 · 1080p · H.264 · E-AC-3 · HDR10" || len(result.MediaSources[0].MediaStreams) == 0 || result.MediaSources[1].Name != "Source 2 · 2160p" || result.MediaSources[1].Id == fixture.item.ID {
		t.Fatalf("source ordering/safe quality/opaque IDs = %#v", result.MediaSources)
	}
	encoded := response.Body.String()
	for _, secret := range []string{"source-ref-provider-secret", "provider-token", "provider.invalid", "provider-name-secret", "provider-cookie", "/api/v1/"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("compat playback JSON disclosed %q: %s", secret, encoded)
		}
	}
	for _, source := range result.MediaSources {
		if source.Protocol != "File" || !strings.HasPrefix(source.Path, "/rivune/") || strings.ContainsAny(source.Path, "?#") || strings.Contains(source.Path, result.PlaySessionId) {
			t.Fatalf("descriptive media path contains transport authority: %#v", source)
		}
		streamURL, err := url.Parse(source.DirectStreamUrl)
		if err != nil {
			t.Fatalf("compat DirectStreamUrl is invalid: %q err=%v", source.DirectStreamUrl, err)
		}
		if streamURL.Query().Get("api_key") != result.PlaySessionId || streamURL.Query().Get("PlaySessionId") != result.PlaySessionId || streamURL.Query().Get("MediaSourceId") != source.Id {
			t.Fatalf("compat DirectStreamUrl lacks scoped playback capability: %q", source.DirectStreamUrl)
		}
	}
}

func TestPlaybackInfoGETUsesConservativeProfileAndPreservesSeekingInTransportURLs(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	request := httptest.NewRequest(http.MethodGet, "/Items/"+fixture.item.ID+"/PlaybackInfo?StartTimeTicks=900000000&MaxStreamingBitrate=4000000", nil)
	request.SetPathValue("id", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	response := httptest.NewRecorder()
	fixture.handler.handlePlaybackInfo(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET PlaybackInfo status=%d body=%s", response.Code, response.Body.String())
	}
	var result PlaybackInfoResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.MediaSources) != 1 || result.MediaSources[0].Protocol != "File" || !strings.HasPrefix(result.MediaSources[0].Path, "/rivune/") || strings.ContainsAny(result.MediaSources[0].Path, "?#") ||
		!strings.Contains(result.MediaSources[0].DirectStreamUrl, "StartTimeTicks=900000000") || !strings.Contains(result.MediaSources[0].DirectStreamUrl, "/stream.mp4?") || !strings.Contains(result.MediaSources[0].DirectStreamUrl, "api_key="+result.PlaySessionId) || strings.Contains(result.MediaSources[0].DirectStreamUrl, fixture.token) ||
		!result.MediaSources[0].SupportsTranscoding || result.MediaSources[0].TranscodingSubProtocol != "hls" || result.MediaSources[0].TranscodingContainer != "mp4" || len(result.MediaSources[0].MediaStreams) == 0 ||
		!strings.Contains(result.MediaSources[0].TranscodingUrl, "/master.m3u8?") || result.MediaSources[0].TranscodingUrl == result.MediaSources[0].Path || fixture.delivery.openCalls != 1 {
		t.Fatalf("GET playback result=%#v opens=%d", result, fixture.delivery.openCalls)
	}
}

func TestCompatibilityPlaybackCapsSourcesBelowReferenceQuotas(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources.Sources = make([]playback.SourceOption, maximumCompatibilityMediaSources+5)
	for index := range fixture.delivery.sources.Sources {
		fixture.delivery.sources.Sources[index] = playback.SourceOption{
			SourceRef: fmt.Sprintf("bounded-source-%02d", index), Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
		}
	}
	response := fixture.playbackInfo(http.MethodGet, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result PlaybackInfoResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.MediaSources) != maximumCompatibilityMediaSources || len(fixture.delivery.sourceInputs) != 1 ||
		fixture.delivery.sourceInputs[0].MaximumSources != maximumCompatibilityMediaSources {
		t.Fatalf("sources=%d inputs=%+v", len(result.MediaSources), fixture.delivery.sourceInputs)
	}
}

func TestItemDetailEmbedsSideEffectFreePlayableSource(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.item.Title = "Playable detail"
	fixture.item.Genres = []string{}
	fixture.item.ProviderIDs = map[string]string{"imdb": "tt0000042"}
	fixture.handler.catalog.(*fakeCompatPlaybackCatalog).item = fixture.item
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "detail-source-secret-1", Name: "https://provider.invalid/signed?token=detail-secret", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "detail-source-secret-2", Name: "Bearer detail-provider-secret", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	request := httptest.NewRequest(http.MethodGet, "/Users/"+fixture.authentication.session.ProfileID+"/Items/"+fixture.item.ID+"?Fields=MediaSources", nil)
	request.SetPathValue("userId", fixture.authentication.session.ProfileID)
	request.SetPathValue("itemId", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	response := httptest.NewRecorder()
	fixture.handler.handleUserItem(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var item BaseItemDto
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if len(item.MediaSources) != 1 || item.MediaSources[0].Name != "Source 1" ||
		item.MediaSources[0].Protocol != "File" || item.MediaSources[0].Container != "mp4" ||
		item.MediaSources[0].RunTimeTicks == nil || *item.MediaSources[0].RunTimeTicks != MinutesToTicks(121) || len(item.MediaSources[0].MediaStreams) != 0 ||
		!strings.HasPrefix(item.MediaSources[0].Path, "/rivune/") || strings.ContainsAny(item.MediaSources[0].Path, "?#") ||
		!strings.Contains(item.MediaSources[0].DirectStreamUrl, "MediaSourceId="+url.QueryEscape(item.MediaSources[0].Id)) || !strings.Contains(item.MediaSources[0].DirectStreamUrl, "Static=true") || strings.Contains(item.MediaSources[0].DirectStreamUrl, "PlaySessionId=") || strings.Contains(item.MediaSources[0].DirectStreamUrl, "api_key=") ||
		strings.Contains(response.Body.String(), "detail-source-secret") || strings.Contains(response.Body.String(), "provider.invalid") || strings.Contains(response.Body.String(), "detail-provider-secret") ||
		fixture.delivery.sourceCalls != 1 || len(fixture.delivery.sourceInputs) != 1 || fixture.delivery.sourceInputs[0].MaximumSources != maximumCompatibilityMediaSources || fixture.delivery.openCalls != 0 || len(fixture.handler.playSessions.entries) != 0 {
		t.Fatalf("detail sources=%+v sourceCalls=%d openCalls=%d sessions=%d body=%s", item.MediaSources, fixture.delivery.sourceCalls, fixture.delivery.openCalls, len(fixture.handler.playSessions.entries), response.Body.String())
	}
}

func TestItemDetailKeepsDeferredSourceWhenDiscoveryFails(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.item.Title = "Fallback detail"
	fixture.item.Genres = []string{}
	fixture.handler.catalog.(*fakeCompatPlaybackCatalog).item = fixture.item
	request := httptest.NewRequest(http.MethodGet, "/Users/"+fixture.authentication.session.ProfileID+"/Items/"+fixture.item.ID, nil)
	request.SetPathValue("userId", fixture.authentication.session.ProfileID)
	request.SetPathValue("itemId", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	response := httptest.NewRecorder()
	fixture.handler.handleUserItem(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var item BaseItemDto
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	requireDeferredMediaSource(t, item)
	if fixture.delivery.openCalls != 0 || len(fixture.handler.playSessions.entries) != 0 {
		t.Fatalf("fallback detail created playback state: opens=%d sessions=%d", fixture.delivery.openCalls, len(fixture.handler.playSessions.entries))
	}
}

func TestEpisodeDetailUsesSeriesPlaybackIdentity(t *testing.T) {
	fixture := newPlaybackFixture(t)
	seasonNumber, episodeNumber := 1, 2
	series := watchstate.CatalogTitle{
		ID: "66666666-6666-4666-8666-666666666666", MediaType: "series", ProviderIDs: map[string]string{"imdb": "tt7587890"},
	}
	fixture.item = watchstate.CatalogTitle{
		ID: fixture.item.ID, MediaType: "episode", SeriesID: series.ID, ParentOrdinal: &seasonNumber, Ordinal: &episodeNumber,
		Genres: []string{}, ProviderIDs: map[string]string{"tmdb": "12345"},
	}
	fixture.handler.catalog.(*fakeCompatPlaybackCatalog).item = fixture.item
	fixture.handler.catalog.(*fakeCompatPlaybackCatalog).series = &series
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "episode-source", Name: "Episode", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	sources := fixture.handler.detailMediaSources(context.Background(), fixture.authentication.session, fixture.item)
	if len(sources) != 1 || len(fixture.delivery.sourceInputs) != 1 || fixture.delivery.sourceInputs[0].MediaType != "episode" ||
		fixture.delivery.sourceInputs[0].ResourceID != "tt7587890:1:2" || fixture.delivery.sourceInputs[0].AddonID != "" || fixture.delivery.sourceInputs[0].MaximumSources != maximumCompatibilityMediaSources ||
		fixture.delivery.openCalls != 0 || len(fixture.handler.playSessions.entries) != 0 {
		t.Fatalf("episode sources=%+v inputs=%+v opens=%d sessions=%d", sources, fixture.delivery.sourceInputs, fixture.delivery.openCalls, len(fixture.handler.playSessions.entries))
	}
}

func TestPlaybackInfoSelectedSourceOpensOnlyThatSource(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-000000000002", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	body := fmt.Sprintf(`{"MediaSourceId":%q,"StartTimeTicks":1200000000,"AudioStreamIndex":2,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`, fixture.item.ID)
	response := fixture.playbackInfo(http.MethodPost, body)
	if response.Code != http.StatusOK {
		t.Fatalf("selected PlaybackInfo status=%d body=%s", response.Code, response.Body.String())
	}
	var result PlaybackInfoResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.openCalls != 1 || fixture.delivery.inputs[0].SourceRef != "source-reference-000000000001" || fixture.delivery.inputs[0].StartSeconds != 120 ||
		fixture.delivery.inputs[0].PreferredAudioTrack == nil || *fixture.delivery.inputs[0].PreferredAudioTrack != 2 {
		t.Fatalf("selected open calls=%d inputs=%#v", fixture.delivery.openCalls, fixture.delivery.inputs)
	}
	if result.MediaSources[0].RunTimeTicks == nil || *result.MediaSources[0].RunTimeTicks != SecondsToTicks(3600) ||
		result.MediaSources[0].Name != "Source 1 · 1080p · H.264 · E-AC-3 · HDR10" || len(result.MediaSources[0].MediaStreams) != 5 ||
		result.MediaSources[0].MediaStreams[0].Type != "Video" || result.MediaSources[0].MediaStreams[0].Height != 1080 ||
		result.MediaSources[0].MediaStreams[1].Type != "Audio" || result.MediaSources[0].MediaStreams[1].IsDefault ||
		result.MediaSources[0].MediaStreams[2].Codec != "eac3" || !result.MediaSources[0].MediaStreams[2].IsDefault ||
		result.MediaSources[0].MediaStreams[3].Type != "Subtitle" || result.MediaSources[0].MediaStreams[3].Index != 3 || !result.MediaSources[0].MediaStreams[3].IsExternal ||
		result.MediaSources[0].MediaStreams[3].DeliveryMethod != "External" || result.MediaSources[0].MediaStreams[3].Codec != "webvtt" ||
		!result.MediaSources[0].MediaStreams[3].IsTextSubtitleStream || !result.MediaSources[0].MediaStreams[3].SupportsExternalStream ||
		result.MediaSources[0].MediaStreams[4].Index != 4 || result.MediaSources[0].MediaStreams[4].Language != "en" || !result.MediaSources[0].MediaStreams[4].IsForced ||
		result.MediaSources[0].DefaultAudioStreamIndex == nil || *result.MediaSources[0].DefaultAudioStreamIndex != 2 {
		t.Fatalf("resolved media metadata=%#v", result.MediaSources[0])
	}
	for streamIndex, assetIndex := range map[int]int{3: 3, 4: 4} {
		deliveryURL, err := url.Parse(result.MediaSources[0].MediaStreams[streamIndex].DeliveryUrl)
		if err != nil || deliveryURL.Path != "/Videos/"+fixture.item.ID+"/"+result.MediaSources[0].Id+"/Subtitles/"+strconv.Itoa(assetIndex)+"/Stream.vtt" ||
			len(deliveryURL.Query()) != 3 || deliveryURL.Query().Get("PlaySessionId") != result.PlaySessionId ||
			deliveryURL.Query().Get("MediaSourceId") != result.MediaSources[0].Id || deliveryURL.Query().Get("StartTimeTicks") != "1200000000" {
			t.Fatalf("subtitle DeliveryUrl=%q parsed=%v err=%v", result.MediaSources[0].MediaStreams[streamIndex].DeliveryUrl, deliveryURL, err)
		}
	}
	if strings.Contains(response.Body.String(), "provider-secret") || strings.Contains(response.Body.String(), "source-reference") {
		t.Fatalf("selected playback response disclosed native data: %s", response.Body.String())
	}
	providerIndex := 4
	if err := fixture.handler.playSessions.setPlaybackPreferences(fixture.authentication.session, fixture.item.ID, result.PlaySessionId, result.MediaSources[0].Id, nil, &providerIndex); err != nil {
		t.Fatal(err)
	}
	if selected := fixture.handler.playSessions.entries[result.PlaySessionId].preferredSubtitleID; selected != "subtitle-1" {
		t.Fatalf("provider subtitle index mapped to %q", selected)
	}
}

func TestSafeDeliverySessionPreservesURLFreeSubtitleMetadata(t *testing.T) {
	native := playback.Session{SelectedSubtitleID: "subtitle-1", Subtitles: []playback.Subtitle{{
		ID: "subtitle-1", Language: "fr", Forced: true, Delivery: "external", URL: "https://provider.invalid/subtitle.vtt?token=secret",
	}}}
	safe := safeDeliverySession(native)
	if safe.SelectedSubtitleID != "subtitle-1" || len(safe.Subtitles) != 1 || safe.Subtitles[0].ID != "subtitle-1" ||
		safe.Subtitles[0].Language != "fr" || !safe.Subtitles[0].Forced || safe.Subtitles[0].Delivery != "external" || safe.Subtitles[0].URL != "" {
		t.Fatalf("safe subtitle metadata=%+v", safe)
	}
	if native.Subtitles[0].URL == "" {
		t.Fatal("safe delivery session mutated native subtitle metadata")
	}
}

func TestProviderSubtitleIndicesAreStableAndDoNotCollideWithMediaTracks(t *testing.T) {
	streams := []MediaStreamInfo{{Type: "Video", Index: 0}, {Type: "Audio", Index: 2}, {Type: "Subtitle", Index: 7}}
	subtitles := []playback.Subtitle{
		{ID: "subtitle-1", Delivery: "external"},
		{ID: "embedded-subtitle-7", Delivery: "external"},
		{ID: "subtitle-2", Delivery: "external"},
	}
	first := compatibilitySubtitleBindings(streams, subtitles)
	second := compatibilitySubtitleBindings(streams, subtitles)
	if !reflect.DeepEqual(first, second) || len(first) != 3 || first[0].index != 8 || first[0].assetID != "subtitle-1" ||
		first[1].index != 7 || first[1].assetID != "embedded-subtitle-7" || first[2].index != 9 || first[2].assetID != "subtitle-2" {
		t.Fatalf("unstable or colliding subtitle bindings: first=%+v second=%+v", first, second)
	}
}
func TestResolvedProviderSubtitleIndexIsBoundToSelectedMediaSource(t *testing.T) {
	entry := &playSessionEntry{sources: map[string]*playSessionSource{
		"source-a": {resolvedSession: playback.Session{
			SelectedSourceID: "native-a",
			Sources: []playback.Source{{ID: "native-a", Media: &playback.MediaInspection{
				VideoTracks: []playback.MediaTrack{{Index: 0}}, SubtitleTracks: []playback.MediaTrack{{Index: 3}},
			}}},
			Subtitles: []playback.Subtitle{{ID: "subtitle-1", Delivery: "external"}},
		}},
		"source-b": {resolvedSession: playback.Session{
			SelectedSourceID: "native-b",
			Sources: []playback.Source{{ID: "native-b", Media: &playback.MediaInspection{
				VideoTracks: []playback.MediaTrack{{Index: 0}}, SubtitleTracks: []playback.MediaTrack{{Index: 4}},
			}}},
			Subtitles: []playback.Subtitle{{ID: "embedded-subtitle-4", Delivery: "external"}},
		}},
	}}
	if assetID, found := resolvedSubtitleAssetID(entry, "source-a", 4); !found || assetID != "subtitle-1" {
		t.Fatalf("source-a index resolved to %q found=%t", assetID, found)
	}
	if assetID, found := resolvedSubtitleAssetID(entry, "source-b", 4); !found || assetID != "embedded-subtitle-4" {
		t.Fatalf("source-b index resolved to %q found=%t", assetID, found)
	}
}

func TestSubtitleStreamServesCapabilityBoundAssetForGETHEADAndStartVariant(t *testing.T) {
	for _, test := range []struct {
		name         string
		method       string
		startVariant bool
	}{
		{name: "modern get", method: http.MethodGet},
		{name: "start ticks head", method: http.MethodHead, startVariant: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlaybackFixture(t)
			fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
				SourceRef: "subtitle-source-reference", StableIdentity: "subtitle-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
			}}}
			listed := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,"StartTimeTicks":1200000000,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`, fixture.item.ID))
			var info PlaybackInfoResponse
			if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || listed.Code != http.StatusOK || len(info.MediaSources) != 1 || len(info.MediaSources[0].MediaStreams) != 5 {
				t.Fatalf("PlaybackInfo status=%d info=%+v err=%v", listed.Code, info, err)
			}
			deliveryURL := info.MediaSources[0].MediaStreams[4].DeliveryUrl
			parsed, err := url.Parse(deliveryURL)
			if err != nil || len(parsed.Query()) != 3 || parsed.Query().Get("PlaySessionId") != info.PlaySessionId ||
				parsed.Query().Get("MediaSourceId") != info.MediaSources[0].Id || parsed.Query().Get("StartTimeTicks") != "1200000000" ||
				strings.Contains(deliveryURL, fixture.token) || strings.Contains(deliveryURL, "provider") || strings.Contains(deliveryURL, "token=") {
				t.Fatalf("unsafe subtitle DeliveryUrl=%q err=%v", deliveryURL, err)
			}
			if test.startVariant {
				parsed.Path = strings.TrimSuffix(parsed.Path, "/Stream.vtt") + "/1200000000/Stream.vtt"
				values := parsed.Query()
				values.Del("StartTimeTicks")
				parsed.RawQuery = values.Encode()
			}
			request := httptest.NewRequest(test.method, parsed.String(), nil)
			request.SetPathValue("id", fixture.item.ID)
			request.SetPathValue("mediaSourceId", info.MediaSources[0].Id)
			request.SetPathValue("subtitleIndex", "4")
			request.SetPathValue("format", "vtt")
			if test.startVariant {
				request.SetPathValue("startPositionTicks", "1200000000")
			}
			request.Header.Set("Range", "bytes=10-19")
			response := httptest.NewRecorder()
			fixture.handler.handleSubtitleStream(response, request)
			if response.Code != http.StatusNoContent || fixture.delivery.serveAssetCalls != 1 || fixture.delivery.servedAssetID != "subtitle-1" ||
				fixture.delivery.servedMethod != test.method || fixture.delivery.servedRange != "bytes=10-19" || fixture.delivery.servedAPIKey != "" || fixture.delivery.servedStartTicks != "" ||
				fixture.delivery.openCalls != 1 || fixture.authentication.revalidateCalls != 1 {
				t.Fatalf("subtitle response=%d assetCalls=%d asset=%q method=%q range=%q key=%q ticks=%q opens=%d revalidations=%d", response.Code,
					fixture.delivery.serveAssetCalls, fixture.delivery.servedAssetID, fixture.delivery.servedMethod, fixture.delivery.servedRange,
					fixture.delivery.servedAPIKey, fixture.delivery.servedStartTicks, fixture.delivery.openCalls, fixture.authentication.revalidateCalls)
			}
		})
	}
}

func TestSubtitleStreamRejectsMalformedForeignStaleAndUnboundRequestsBeforeServeAsset(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*playbackFixture, *http.Request, PlaybackInfoResponse)
	}{
		{name: "invalid format", mutate: func(_ *playbackFixture, request *http.Request, _ PlaybackInfoResponse) {
			request.SetPathValue("format", "srt")
		}},
		{name: "non canonical index", mutate: func(_ *playbackFixture, request *http.Request, _ PlaybackInfoResponse) {
			request.SetPathValue("subtitleIndex", "04")
		}},
		{name: "unbound source", mutate: func(_ *playbackFixture, request *http.Request, _ PlaybackInfoResponse) {
			request.SetPathValue("mediaSourceId", "99999999-9999-4999-8999-999999999999")
		}},
		{name: "foreign profile", mutate: func(fixture *playbackFixture, request *http.Request, _ PlaybackInfoResponse) {
			foreign := playbackSessionTestOtherOwner(fixture.authentication.session, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
			fixture.authentication.sessions = map[string]AuthenticatedSession{"foreign-token": foreign}
			request.Header.Set("X-Emby-Token", "foreign-token")
		}},
		{name: "stale capability", mutate: func(fixture *playbackFixture, _ *http.Request, info PlaybackInfoResponse) {
			fixture.handler.playSessions.mu.Lock()
			delete(fixture.handler.playSessions.entries, info.PlaySessionId)
			fixture.handler.playSessions.mu.Unlock()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlaybackFixture(t)
			fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
				SourceRef: "subtitle-reject-source", StableIdentity: "subtitle-reject", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
			}}}
			listed := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`, fixture.item.ID))
			var info PlaybackInfoResponse
			if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || listed.Code != http.StatusOK || len(info.MediaSources) != 1 || len(info.MediaSources[0].MediaStreams) != 5 {
				t.Fatalf("PlaybackInfo status=%d info=%+v err=%v", listed.Code, info, err)
			}
			request := httptest.NewRequest(http.MethodGet, info.MediaSources[0].MediaStreams[4].DeliveryUrl, nil)
			request.SetPathValue("id", fixture.item.ID)
			request.SetPathValue("mediaSourceId", info.MediaSources[0].Id)
			request.SetPathValue("subtitleIndex", "4")
			request.SetPathValue("format", "vtt")
			test.mutate(fixture, request, info)
			response := httptest.NewRecorder()
			fixture.handler.handleSubtitleStream(response, request)
			if response.Code == http.StatusNoContent || fixture.delivery.serveAssetCalls != 0 {
				t.Fatalf("rejected subtitle status=%d ServeAsset calls=%d", response.Code, fixture.delivery.serveAssetCalls)
			}
		})
	}
}

func TestPlaybackInfoReusesSecondAndThirdCandidateAcrossRequests(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "source-reference-000000000001", Name: "First", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-000000000002", Name: "Second", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-000000000003", Name: "Third", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	profile := `"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}`
	listed := fixture.playbackInfo(http.MethodPost, `{`+profile+`}`)
	if listed.Code != http.StatusOK {
		t.Fatalf("initial PlaybackInfo status=%d body=%s", listed.Code, listed.Body.String())
	}
	var initial PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.MediaSources) != 3 {
		t.Fatalf("initial sources=%#v", initial.MediaSources)
	}
	for index := 1; index < 3; index++ {
		selected := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,%s}`, initial.MediaSources[index].Id, profile))
		if selected.Code != http.StatusOK {
			t.Fatalf("select source %d status=%d body=%s", index, selected.Code, selected.Body.String())
		}
		var result PlaybackInfoResponse
		if err := json.Unmarshal(selected.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.PlaySessionId != initial.PlaySessionId || len(result.MediaSources) != 3 {
			t.Fatalf("source %d did not reuse candidate set: initial=%#v selected=%#v", index, initial, result)
		}
		for candidate := range result.MediaSources {
			if result.MediaSources[candidate].Id != initial.MediaSources[candidate].Id {
				t.Fatalf("source %d candidate %d ID changed from %q to %q", index, candidate, initial.MediaSources[candidate].Id, result.MediaSources[candidate].Id)
			}
		}
	}
	if fixture.delivery.sourceCalls != 1 || fixture.delivery.openCalls != 3 ||
		fixture.delivery.inputs[0].SourceRef != "source-reference-000000000001" || fixture.delivery.inputs[1].SourceRef != "source-reference-000000000002" || fixture.delivery.inputs[2].SourceRef != "source-reference-000000000003" {
		t.Fatalf("eager and later selections did not use issued candidates: source calls=%d opens=%d inputs=%#v", fixture.delivery.sourceCalls, fixture.delivery.openCalls, fixture.delivery.inputs)
	}
	for _, source := range initial.MediaSources[1:] {
		if source.Id == "" || strings.Contains(source.Id, "source-reference") {
			t.Fatalf("non-first source ID is not opaque: %q", source.Id)
		}
	}
}

func TestPlaybackInfoRejectsForgedAndCrossOwnerCandidateIDs(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-000000000002", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	profile := `"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}`
	listed := fixture.playbackInfo(http.MethodPost, `{`+profile+`}`)
	var initial PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	original := fixture.authentication.session
	fixture.authentication.session.ID = "77777777-7777-4777-8777-777777777777"
	crossOwner := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,%s}`, initial.MediaSources[1].Id, profile))
	fixture.authentication.session = original
	forged := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,%s}`, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", profile))
	if crossOwner.Code != http.StatusNotFound || forged.Code != http.StatusNotFound || fixture.delivery.openCalls != 1 || fixture.delivery.sourceCalls != 3 || len(fixture.delivery.inputs) != 1 || fixture.delivery.inputs[0].SourceRef != "source-reference-000000000001" {
		t.Fatalf("candidate isolation failed after eager first open: cross=%d forged=%d opens=%d inputs=%#v source calls=%d", crossOwner.Code, forged.Code, fixture.delivery.openCalls, fixture.delivery.inputs, fixture.delivery.sourceCalls)
	}
}

func TestPlaybackInfoChangedStartTimeTicksReplacesOpenedDelivery(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "source-reference-000000000001", Name: "Tick First", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-000000000002", Name: "Tick Second", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	fixture.delivery.handles = []playback.DeliveryHandle{opaquePlaybackHandleNamed(t, "tick-one"), opaquePlaybackHandleNamed(t, "tick-two")}
	profile := `"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}`
	listed := fixture.playbackInfo(http.MethodPost, `{`+profile+`}`)
	var initial PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	for _, ticks := range []int64{1, 2} {
		selected := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,"StartTimeTicks":%d,%s}`, initial.MediaSources[1].Id, ticks, profile))
		if selected.Code != http.StatusOK {
			t.Fatalf("select at %d ticks status=%d body=%s", ticks, selected.Code, selected.Body.String())
		}
	}
	if fixture.delivery.openCalls != 3 || fixture.delivery.closeCalls != 1 || len(fixture.delivery.inputs) != 3 ||
		fixture.delivery.inputs[0].SourceRef != "source-reference-000000000001" || fixture.delivery.inputs[1].SourceRef != "source-reference-000000000002" || fixture.delivery.inputs[0].StartSeconds != 0 || fixture.delivery.inputs[1].StartSeconds != 0 || fixture.delivery.inputs[2].StartSeconds != 0 {
		t.Fatalf("tick-precise replacement after eager open: opens=%d closes=%d inputs=%#v", fixture.delivery.openCalls, fixture.delivery.closeCalls, fixture.delivery.inputs)
	}
}

func TestStreamEagerlyOpensFirstThenSelectedSourceAndPreservesRangeHEADAndSeeking(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			fixture := newPlaybackFixture(t)
			fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
				{SourceRef: "source-reference-000000000001", Name: "First", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
				{SourceRef: "source-reference-000000000002", Name: "Second", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
			}}
			listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`)
			if listed.Code != http.StatusOK {
				t.Fatalf("PlaybackInfo status=%d body=%s", listed.Code, listed.Body.String())
			}
			var info PlaybackInfoResponse
			if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(method, "/Videos/"+fixture.item.ID+"/stream?PlaySessionId="+info.PlaySessionId+"&MediaSourceId="+info.MediaSources[1].Id+"&StartTimeTicks=900000000", nil)
			request.SetPathValue("id", fixture.item.ID)
			request.Header.Set("X-Emby-Token", fixture.token)
			request.Header.Set("Range", "bytes=1024-2047")
			response := httptest.NewRecorder()
			fixture.handler.handleStream(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("stream status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.delivery.openCalls != 2 || fixture.delivery.inputs[0].SourceRef != "source-reference-000000000001" || fixture.delivery.inputs[1].SourceRef != "source-reference-000000000002" || fixture.delivery.inputs[1].StartSeconds != 90 {
				t.Fatalf("eager first and selected stream opens = calls %d inputs %#v", fixture.delivery.openCalls, fixture.delivery.inputs)
			}
			if fixture.delivery.servedMethod != method || fixture.delivery.servedRange != "bytes=1024-2047" || fixture.delivery.servedStartTicks != "900000000" || fixture.delivery.servedAPIKey != info.PlaySessionId {
				t.Fatalf("serve semantics method=%q range=%q ticks=%q scopedKey=%t", fixture.delivery.servedMethod, fixture.delivery.servedRange, fixture.delivery.servedStartTicks, fixture.delivery.servedAPIKey == info.PlaySessionId)
			}
		})
	}
}

func TestRemuxOnlyPlaybackAdmitsNonDirectMKVWithoutEncoding(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{
		SourceRef: "remux-only-source-reference", StableIdentity: "remux-only-source", Protocol: "http", Container: "mkv", ExpiresAt: fixture.now.Add(time.Hour),
	}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"EnableDirectPlay":false,"EnableDirectStream":true,"EnableTranscoding":false,"AllowVideoStreamCopy":true,"AllowAudioStreamCopy":true,"DeviceProfile":{"TranscodingProfiles":[{"Container":"mp4","VideoCodec":"av1","AudioCodec":"opus","Protocol":"hls","Context":"Streaming","Type":"Video"},{"Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","Context":"Streaming","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || listed.Code != http.StatusOK || len(info.MediaSources) != 1 {
		t.Fatalf("remux-only MKV playback info status=%d info=%+v err=%v body=%s", listed.Code, info, err, listed.Body.String())
	}
	if !info.MediaSources[0].SupportsDirectStream || info.MediaSources[0].SupportsTranscoding ||
		!strings.Contains(info.MediaSources[0].DirectStreamUrl, "/master.m3u8?") || info.MediaSources[0].TranscodingUrl != "" {
		t.Fatalf("remux-only MKV advertised wrong processing support: %+v", info.MediaSources[0])
	}
	request := httptest.NewRequest(http.MethodGet, info.MediaSources[0].DirectStreamUrl, nil)
	request.SetPathValue("id", fixture.item.ID)
	response := httptest.NewRecorder()
	fixture.handler.handleStream(response, request)
	if response.Code != http.StatusNoContent || len(fixture.delivery.inputs) != 1 {
		t.Fatalf("remux-only MKV stream status=%d opens=%d body=%s", response.Code, len(fixture.delivery.inputs), response.Body.String())
	}
	input := fixture.delivery.inputs[0]
	if input.AllowTranscoding || input.Capabilities.HLSSegmentContainer != "ts" ||
		!containsFold(input.Capabilities.ProcessingModes, "remux") || containsFold(input.Capabilities.ProcessingModes, "transcode") || containsFold(input.Capabilities.ProcessingModes, "transcode_audio") ||
		len(input.Capabilities.MediaProfiles) != 1 || input.Capabilities.MediaProfiles[0] != (playback.MediaProfile{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"}) {
		t.Fatalf("remux-only capabilities admitted encoding or lost the selected HLS profile: %+v", input)
	}
}

func TestStaticStreamWithoutPlaybackInfoRequiresGeneralCredentialAndFailsClosedOnForeignSource(t *testing.T) {
	for _, test := range []struct {
		name          string
		credential    bool
		mediaID       string
		omitMedia     bool
		playSessionID string
		wantStatus    int
		wantOpen      int
		wantSources   int
	}{
		{name: "primary item alias", credential: true, wantStatus: http.StatusNoContent, wantOpen: 1, wantSources: 1},
		{name: "client-generated play session", credential: true, playSessionID: "client-generated-session", wantStatus: http.StatusNoContent, wantOpen: 1, wantSources: 1},
		{name: "missing credential", wantStatus: http.StatusUnauthorized},
		{name: "foreign media source", credential: true, mediaID: "99999999-9999-4999-8999-999999999999", wantStatus: http.StatusNotFound, wantSources: 1},
		{name: "reserved stale session with missing media source", credential: true, omitMedia: true, playSessionID: "rvp_stale-play-session-0001", wantStatus: http.StatusNotFound},
		{name: "reserved stale session with malformed media source", credential: true, mediaID: "short", playSessionID: "rvp_stale-play-session-0002", wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlaybackFixture(t)
			fixture.authentication.sessions = map[string]AuthenticatedSession{fixture.token: fixture.authentication.session}
			fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "static-source-reference", StableIdentity: "static-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
			values := url.Values{"Static": {"true"}}
			if !test.omitMedia {
				mediaID := test.mediaID
				if mediaID == "" {
					mediaID = fixture.item.ID
				}
				values.Set("MediaSourceId", mediaID)
			}
			if test.playSessionID != "" {
				values.Set("PlaySessionId", test.playSessionID)
			}
			if test.credential {
				values.Set("api_key", fixture.token)
			}
			request := httptest.NewRequest(http.MethodGet, "/Videos/"+fixture.item.ID+"/stream.mp4?"+values.Encode(), nil)
			request.SetPathValue("id", fixture.item.ID)
			request.Header.Set("Range", "bytes=5-9")
			response := httptest.NewRecorder()
			fixture.handler.handleStream(response, request)
			if response.Code != test.wantStatus || fixture.delivery.openCalls != test.wantOpen || fixture.delivery.serveCalls != test.wantOpen || fixture.delivery.sourceCalls != test.wantSources {
				t.Fatalf("static stream status=%d sources=%d open=%d serve=%d body=%s", response.Code, fixture.delivery.sourceCalls, fixture.delivery.openCalls, fixture.delivery.serveCalls, response.Body.String())
			}
			if test.wantOpen == 1 && (fixture.delivery.servedAPIKey == fixture.token || fixture.delivery.servedAPIKey == "" || fixture.delivery.servedRange != "bytes=5-9" || len(fixture.handler.playSessions.entries) != 1) {
				t.Fatalf("static stream leaked/rejected capability: key=%q range=%q sessions=%d", fixture.delivery.servedAPIKey, fixture.delivery.servedRange, len(fixture.handler.playSessions.entries))
			}
			if test.wantOpen == 1 && test.mediaID == "" {
				retry := httptest.NewRequest(http.MethodHead, "/Videos/"+fixture.item.ID+"/stream.mp4?"+values.Encode(), nil)
				retry.SetPathValue("id", fixture.item.ID)
				retryResponse := httptest.NewRecorder()
				fixture.handler.handleStream(retryResponse, retry)
				if retryResponse.Code != http.StatusNoContent || fixture.delivery.openCalls != 1 || fixture.delivery.serveCalls != 2 || len(fixture.handler.playSessions.entries) != 1 {
					t.Fatalf("static retry status=%d open=%d serve=%d sessions=%d", retryResponse.Code, fixture.delivery.openCalls, fixture.delivery.serveCalls, len(fixture.handler.playSessions.entries))
				}
			}
		})
	}
}

func TestMasterPlaylistUsesNegotiatedCapabilityAndDownloadUsesGeneralCredential(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "master-source-reference", StableIdentity: "master-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}],"TranscodingProfiles":[{"Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || listed.Code != http.StatusOK || len(info.MediaSources) != 1 {
		t.Fatalf("playback info=%+v status=%d err=%v", info, listed.Code, err)
	}
	if info.MediaSources[0].TranscodingContainer != "ts" || !strings.Contains(info.MediaSources[0].TranscodingUrl, "/master.m3u8?") || strings.Contains(info.MediaSources[0].TranscodingUrl, fixture.token) {
		t.Fatalf("master DTO=%+v", info.MediaSources[0])
	}
	fixture.authentication.sessions = map[string]AuthenticatedSession{fixture.token: fixture.authentication.session}
	master := httptest.NewRequest(http.MethodGet, info.MediaSources[0].TranscodingUrl, nil)
	master.SetPathValue("id", fixture.item.ID)
	masterResponse := httptest.NewRecorder()
	fixture.handler.handleStream(masterResponse, master)
	if masterResponse.Code != http.StatusNoContent || fixture.delivery.servedPath != "/Videos/"+fixture.item.ID+"/master.m3u8" || fixture.authentication.revalidateCalls != 1 {
		t.Fatalf("master status=%d path=%q revalidations=%d", masterResponse.Code, fixture.delivery.servedPath, fixture.authentication.revalidateCalls)
	}
	childID := strings.Repeat("c", 32)
	childValues := url.Values{"MediaSourceId": {info.MediaSources[0].Id}}
	child := httptest.NewRequest(http.MethodGet, "/Videos/"+fixture.item.ID+"/hls1/"+info.PlaySessionId+"/"+childID+".ts?"+childValues.Encode(), nil)
	child.SetPathValue("id", fixture.item.ID)
	child.SetPathValue("playlistId", info.PlaySessionId)
	child.SetPathValue("segmentId", childID)
	child.SetPathValue("container", "ts")
	childResponse := httptest.NewRecorder()
	fixture.handler.handleStream(childResponse, child)
	if childResponse.Code != http.StatusNoContent || fixture.delivery.servedChildID != childID || fixture.authentication.revalidateCalls != 2 {
		t.Fatalf("child status=%d id=%q revalidations=%d", childResponse.Code, fixture.delivery.servedChildID, fixture.authentication.revalidateCalls)
	}

	downloadFixture := newPlaybackFixture(t)
	downloadFixture.authentication.sessions = map[string]AuthenticatedSession{downloadFixture.token: downloadFixture.authentication.session}
	downloadFixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "download-source-reference", StableIdentity: "download-source", Protocol: "http", Container: "mp4", ExpiresAt: downloadFixture.now.Add(time.Hour)}}}
	download := httptest.NewRequest(http.MethodHead, "/Items/"+downloadFixture.item.ID+"/Download?api_key="+url.QueryEscape(downloadFixture.token), nil)
	download.SetPathValue("id", downloadFixture.item.ID)
	downloadResponse := httptest.NewRecorder()
	downloadFixture.handler.handleDownload(downloadResponse, download)
	if downloadResponse.Code != http.StatusNoContent || downloadFixture.delivery.servedMethod != http.MethodHead || downloadFixture.delivery.servedPath != "/Items/"+downloadFixture.item.ID+"/Download" || downloadFixture.delivery.servedAPIKey == downloadFixture.token {
		t.Fatalf("download status=%d method=%q path=%q scoped=%t", downloadResponse.Code, downloadFixture.delivery.servedMethod, downloadFixture.delivery.servedPath, downloadFixture.delivery.servedAPIKey != downloadFixture.token)
	}
}

func TestPlaybackInfoAdvertisesScopedCapabilityAndStreamFallsBackToNegotiatedSession(t *testing.T) {
	for _, test := range []struct {
		name                   string
		removeCredential       bool
		profileQueryCredential bool
		headerKind             string
		unknownSession         bool
		wantStatus             int
		wantRevalidation       int
	}{
		{name: "scoped query capability", wantStatus: http.StatusNoContent, wantRevalidation: 1},
		{name: "profile query credential", profileQueryCredential: true, wantStatus: http.StatusNoContent},
		{name: "credential dropped by player", removeCredential: true, wantStatus: http.StatusNoContent, wantRevalidation: 1},
		{name: "play session copied into header", headerKind: "play", wantStatus: http.StatusNoContent, wantRevalidation: 1},
		{name: "stale player header masks query", profileQueryCredential: true, headerKind: "stale", wantStatus: http.StatusNoContent, wantRevalidation: 1},
		{name: "active foreign credential never falls back", headerKind: "foreign", wantStatus: http.StatusNotFound},
		{name: "unknown play session without credential", removeCredential: true, unknownSession: true, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlaybackFixture(t)
			fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "negotiated-session-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
			listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","AudioCodec":"aac","Type":"Video"}]}}`)
			var info PlaybackInfoResponse
			if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || listed.Code != http.StatusOK || len(info.MediaSources) != 1 {
				t.Fatalf("playback info status=%d result=%+v err=%v", listed.Code, info, err)
			}
			if info.MediaSources[0].Protocol != "File" || !strings.HasPrefix(info.MediaSources[0].Path, "/rivune/") || strings.ContainsAny(info.MediaSources[0].Path, "?#") {
				t.Fatalf("descriptive Path exposed transport authority: %+v", info.MediaSources[0])
			}
			streamURL, err := url.Parse(info.MediaSources[0].DirectStreamUrl)
			if err != nil {
				t.Fatal(err)
			}
			if streamURL.Query().Get("api_key") != info.PlaySessionId || streamURL.Query().Get("PlaySessionId") != info.PlaySessionId || fixture.token == info.PlaySessionId {
				t.Fatalf("advertised scoped capability=%q playSession=%q directStreamUrl=%q", streamURL.Query().Get("api_key"), info.PlaySessionId, info.MediaSources[0].DirectStreamUrl)
			}
			fixture.authentication.sessions = map[string]AuthenticatedSession{fixture.token: fixture.authentication.session}
			foreignToken := "rivune_jf_" + strings.Repeat("B", 43)
			if test.headerKind == "foreign" {
				fixture.authentication.sessions[foreignToken] = playbackSessionTestOtherOwner(fixture.authentication.session, "foreign-compat-session", "foreign-native-session")
			}
			values := streamURL.Query()
			if test.profileQueryCredential {
				values.Set("api_key", fixture.token)
			}
			if test.removeCredential {
				deleteQueryFold(values, "api_key")
			}
			if test.unknownSession {
				values.Set("PlaySessionId", strings.Repeat("z", 32))
			}
			streamURL.RawQuery = values.Encode()
			request := httptest.NewRequest(http.MethodGet, streamURL.String(), nil)
			request.SetPathValue("id", fixture.item.ID)
			switch test.headerKind {
			case "play":
				request.Header.Set("X-Emby-Token", info.PlaySessionId)
			case "stale":
				request.Header.Set("X-Emby-Token", "rivune_jf_"+strings.Repeat("C", 43))
			case "foreign":
				request.Header.Set("X-Emby-Token", foreignToken)
			}
			response := httptest.NewRecorder()
			fixture.handler.handleStream(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("stream status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			wantServed := test.wantStatus == http.StatusNoContent
			if (fixture.delivery.serveCalls == 1) != wantServed || fixture.delivery.openCalls != 1 || fixture.authentication.revalidateCalls != test.wantRevalidation {
				t.Fatalf("eagerOpen=%d serve=%d revalidate=%d wantServed=%t wantRevalidation=%d", fixture.delivery.openCalls, fixture.delivery.serveCalls, fixture.authentication.revalidateCalls, wantServed, test.wantRevalidation)
			}
			if wantServed && (fixture.delivery.servedAPIKey != info.PlaySessionId || fixture.delivery.servedAPIKeyCount != 1 || fixture.delivery.servedProfileQueryCredential || fixture.delivery.servedProfileHeaderCredential) {
				t.Fatalf("delivery scopedKey=%t apiKeys=%d queryCredential=%t headerCredential=%t", fixture.delivery.servedAPIKey == info.PlaySessionId, fixture.delivery.servedAPIKeyCount, fixture.delivery.servedProfileQueryCredential, fixture.delivery.servedProfileHeaderCredential)
			}
		})
	}
}

func TestRevokedBackingSessionRejectsScopedStreamBeforeAdditionalOpen(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "revoked-capability-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || len(info.MediaSources) != 1 {
		t.Fatalf("playback info=%+v err=%v", info, err)
	}
	fixture.authentication.revalidateErr = ErrInvalidCompatCredential
	streamURL, err := url.Parse(info.MediaSources[0].DirectStreamUrl)
	if err != nil {
		t.Fatal(err)
	}
	values := streamURL.Query()
	deleteQueryFold(values, "api_key")
	streamURL.RawQuery = values.Encode()
	request := httptest.NewRequest(http.MethodGet, streamURL.String(), nil)
	request.SetPathValue("id", fixture.item.ID)
	response := httptest.NewRecorder()
	fixture.handler.handleStream(response, request)
	if response.Code != http.StatusUnauthorized || fixture.authentication.revalidateCalls != 1 || fixture.delivery.openCalls != 1 || fixture.delivery.serveCalls != 0 || fixture.delivery.closeCalls != 1 || len(fixture.handler.playSessions.entries) != 0 {
		t.Fatalf("status=%d revalidate=%d open=%d serve=%d close=%d entries=%d", response.Code, fixture.authentication.revalidateCalls, fixture.delivery.openCalls, fixture.delivery.serveCalls, fixture.delivery.closeCalls, len(fixture.handler.playSessions.entries))
	}
}

func TestTransientStreamRevalidationFailurePreservesScopedSession(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "transient-capability-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil || len(info.MediaSources) != 1 {
		t.Fatalf("playback info=%+v err=%v", info, err)
	}
	fixture.authentication.revalidateErr = errors.New("database temporarily unavailable")
	streamURL, err := url.Parse(info.MediaSources[0].DirectStreamUrl)
	if err != nil {
		t.Fatal(err)
	}
	values := streamURL.Query()
	deleteQueryFold(values, "api_key")
	streamURL.RawQuery = values.Encode()
	request := httptest.NewRequest(http.MethodGet, streamURL.String(), nil)
	request.SetPathValue("id", fixture.item.ID)
	response := httptest.NewRecorder()
	fixture.handler.handleStream(response, request)
	if response.Code != http.StatusInternalServerError || fixture.delivery.openCalls != 1 || fixture.delivery.serveCalls != 0 || fixture.handler.playSessions.entries[info.PlaySessionId] == nil {
		t.Fatalf("status=%d open=%d serve=%d sessionPreserved=%t", response.Code, fixture.delivery.openCalls, fixture.delivery.serveCalls, fixture.handler.playSessions.entries[info.PlaySessionId] != nil)
	}
}

func TestStreamRejectsSourceMismatchAndCrossSessionOrProfileReplay(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mediaID string
		mutate        func(*AuthenticatedSession)
	}{
		{name: "source mismatch", mediaID: "00000000-0000-4000-8000-000000000999"},
		{name: "compat session", mediaID: info.MediaSources[0].Id, mutate: func(session *AuthenticatedSession) { session.ID = "77777777-7777-4777-8777-777777777777" }},
		{name: "profile", mediaID: info.MediaSources[0].Id, mutate: func(session *AuthenticatedSession) {
			profile := "88888888-8888-4888-8888-888888888888"
			session.ProfileID = profile
			session.Principal.ActiveProfileID = &profile
		}},
		{name: "native session", mediaID: info.MediaSources[0].Id, mutate: func(session *AuthenticatedSession) {
			session.Principal.SessionID = "99999999-9999-4999-8999-999999999999"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := fixture.authentication.session
			if test.mutate != nil {
				test.mutate(&fixture.authentication.session)
			}
			request := httptest.NewRequest(http.MethodGet, "/Videos/"+fixture.item.ID+"/stream?PlaySessionId="+info.PlaySessionId+"&MediaSourceId="+test.mediaID, nil)
			request.SetPathValue("id", fixture.item.ID)
			request.Header.Set("X-Emby-Token", fixture.token)
			response := httptest.NewRecorder()
			fixture.handler.handleStream(response, request)
			fixture.authentication.session = original
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if fixture.delivery.openCalls != 1 || fixture.delivery.serveCalls != 0 {
		t.Fatalf("replay triggered delivery beyond eager negotiation: open=%d serve=%d", fixture.delivery.openCalls, fixture.delivery.serveCalls)
	}
}

func TestStreamCancellationPreservesDeliveryForRetry(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fixture.delivery.serveErr = context.Canceled
	request := httptest.NewRequest(http.MethodGet, "/Videos/"+fixture.item.ID+"/stream?PlaySessionId="+info.PlaySessionId+"&MediaSourceId="+info.MediaSources[0].Id, nil).WithContext(ctx)
	request.SetPathValue("id", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	fixture.handler.handleStream(httptest.NewRecorder(), request)
	if fixture.delivery.closeCalls != 0 || fixture.delivery.serveCalls != 1 || fixture.handler.playSessions.entries[info.PlaySessionId] == nil {
		t.Fatalf("canceled stream retry state: serve=%d close=%d preserved=%t", fixture.delivery.serveCalls, fixture.delivery.closeCalls, fixture.handler.playSessions.entries[info.PlaySessionId] != nil)
	}
}

func TestTerminalStreamFailureClosesDelivery(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{{SourceRef: "terminal-source-reference", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}}}
	listed := fixture.playbackInfo(http.MethodPost, `{"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}}`)
	var info PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	fixture.delivery.serveErr = playback.ErrSessionNotFound
	request := httptest.NewRequest(http.MethodGet, "/Videos/"+fixture.item.ID+"/stream?PlaySessionId="+info.PlaySessionId+"&MediaSourceId="+info.MediaSources[0].Id, nil)
	request.SetPathValue("id", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	fixture.handler.handleStream(httptest.NewRecorder(), request)
	if fixture.delivery.serveCalls != 1 || fixture.delivery.closeCalls != 1 || fixture.handler.playSessions.entries[info.PlaySessionId] != nil {
		t.Fatalf("terminal stream cleanup: serve=%d close=%d preserved=%t", fixture.delivery.serveCalls, fixture.delivery.closeCalls, fixture.handler.playSessions.entries[info.PlaySessionId] != nil)
	}
}

func TestPlaySessionRegistryExpiryEvictionAndMultiSourcePingCleanup(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return fixture.now }
	registry.limit = 1
	registry.idleTTL = time.Minute
	options := []playback.SourceOption{
		{SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-000000000002", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ping(fixture.authentication.session, playID); err != nil {
		t.Fatalf("multi-source ping: %v", err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, options[:1]); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.closeCalls != 1 {
		t.Fatalf("eviction closes=%d, want 1", fixture.delivery.closeCalls)
	}
	secondID := ""
	for id := range registry.entries {
		secondID = id
	}
	var secondMedia string
	for _, id := range registry.entries[secondID].sourceOrder {
		secondMedia = id
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, secondID, secondMedia, 0); err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(2 * time.Minute)
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 2 || len(registry.entries) != 0 || len(fixture.delivery.pinned) != 0 {
		t.Fatalf("expiry cleanup closes=%d entries=%d pins=%#v", fixture.delivery.closeCalls, len(registry.entries), fixture.delivery.pinned)
	}
}
func TestPlaySessionRegistryOptionalMediaSourceRequiresUniqueOpenSource(t *testing.T) {
	registerTwo := func(t *testing.T, fixture *playbackFixture) (string, []playSourceDescriptor) {
		t.Helper()
		options := []playback.SourceOption{
			{SourceRef: "source-reference-event-one", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
			{SourceRef: "source-reference-event-two", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		}
		playID, descriptors, err := fixture.handler.playSessions.register(
			context.Background(), fixture.authentication.session, fixture.item.ID,
			playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}}, false, options,
		)
		if err != nil || len(descriptors) != 2 {
			t.Fatalf("register sources=%#v err=%v", descriptors, err)
		}
		return playID, descriptors
	}

	t.Run("single source", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		option := playback.SourceOption{SourceRef: "source-reference-event-single", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
		playID, descriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option})
		if err != nil || len(descriptors) != 1 {
			t.Fatalf("register sources=%#v err=%v", descriptors, err)
		}
		if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
			t.Fatal(err)
		}
		binding, err := fixture.handler.playSessions.resolveAndTouch(fixture.authentication.session, fixture.item.ID, playID, "")
		if err != nil || binding.MediaSourceID != descriptors[0].ID {
			t.Fatalf("binding=%+v err=%v", binding, err)
		}
	})

	t.Run("one open", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		playID, descriptors := registerTwo(t, fixture)
		if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[1].ID, 0); err != nil {
			t.Fatal(err)
		}
		entry := fixture.handler.playSessions.entries[playID]
		entry.lastSeenAt = fixture.now.Add(-time.Minute)
		binding, err := fixture.handler.playSessions.resolveAndTouch(fixture.authentication.session, fixture.item.ID, playID, "")
		if err != nil || binding.MediaSourceID != descriptors[1].ID || !entry.lastSeenAt.Equal(fixture.now) {
			t.Fatalf("binding=%+v lastSeen=%v err=%v", binding, entry.lastSeenAt, err)
		}
	})

	t.Run("none open", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		playID, _ := registerTwo(t, fixture)
		entry := fixture.handler.playSessions.entries[playID]
		lastSeen := entry.lastSeenAt
		if _, err := fixture.handler.playSessions.resolveAndTouch(fixture.authentication.session, fixture.item.ID, playID, ""); !errors.Is(err, errPlaySessionNotFound) || !entry.lastSeenAt.Equal(lastSeen) {
			t.Fatalf("err=%v lastSeen=%v want=%v", err, entry.lastSeenAt, lastSeen)
		}
	})

	t.Run("source from another play session", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		targetPlayID, _ := registerTwo(t, fixture)
		foreignPlayID, foreignDescriptors := registerTwo(t, fixture)
		if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, foreignPlayID, foreignDescriptors[0].ID, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.handler.playSessions.resolveAndTouch(fixture.authentication.session, fixture.item.ID, targetPlayID, ""); !errors.Is(err, errPlaySessionNotFound) {
			t.Fatalf("cross-session source error=%v", err)
		}
	})

	t.Run("multiple open", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		playID, descriptors := registerTwo(t, fixture)
		for _, descriptor := range descriptors {
			if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptor.ID, 0); err != nil {
				t.Fatal(err)
			}
		}
		entry := fixture.handler.playSessions.entries[playID]
		lastSeen := entry.lastSeenAt
		if _, err := fixture.handler.playSessions.resolveAndTouch(fixture.authentication.session, fixture.item.ID, playID, ""); !errors.Is(err, errPlaySessionNotFound) || !entry.lastSeenAt.Equal(lastSeen) {
			t.Fatalf("err=%v lastSeen=%v want=%v", err, entry.lastSeenAt, lastSeen)
		}
	})

	t.Run("foreign owner", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		playID, descriptors := registerTwo(t, fixture)
		if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
			t.Fatal(err)
		}
		foreign := playbackSessionTestRelogin(fixture.authentication.session, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
		if _, err := fixture.handler.playSessions.resolveAndTouch(foreign, fixture.item.ID, playID, ""); !errors.Is(err, errPlaySessionNotFound) {
			t.Fatalf("foreign owner error=%v", err)
		}
	})

	t.Run("expired session", func(t *testing.T) {
		fixture := newPlaybackFixture(t)
		playID, descriptors := registerTwo(t, fixture)
		if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
			t.Fatal(err)
		}
		fixture.handler.playSessions.entries[playID].expiresAt = fixture.now
		if _, err := fixture.handler.playSessions.resolveAndTouch(fixture.authentication.session, fixture.item.ID, playID, ""); !errors.Is(err, errPlaySessionNotFound) {
			t.Fatalf("expired session error=%v", err)
		}
	})
}

func TestPlaySessionRegistryConcurrentOpenAndCloseAreSingleShot(t *testing.T) {
	fixture := newPlaybackFixture(t)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	fixture.delivery.openGate = gate
	fixture.delivery.openStarted = started
	option := playback.SourceOption{SourceRef: "source-reference-000000000001", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	playID, descriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	const callers = 16
	errorsByCaller := make(chan error, callers)
	for range callers {
		go func() {
			_, _, _, openErr := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0)
			errorsByCaller <- openErr
		}()
	}
	<-started
	close(gate)
	for range callers {
		if openErr := <-errorsByCaller; openErr != nil {
			t.Fatalf("concurrent open: %v", openErr)
		}
	}
	if fixture.delivery.openCalls != 1 {
		t.Fatalf("native Open calls=%d, want one", fixture.delivery.openCalls)
	}
	closed := make(chan error, 2)
	for range 2 {
		go func() {
			closed <- fixture.handler.playSessions.close(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID)
		}()
	}
	first, second := <-closed, <-closed
	if (first == nil) == (second == nil) || fixture.delivery.closeCalls != 1 {
		t.Fatalf("concurrent close errors=(%v,%v) native closes=%d", first, second, fixture.delivery.closeCalls)
	}
}

func TestPlaySessionRegistryCloseCancelsInFlightOpen(t *testing.T) {
	fixture := newPlaybackFixture(t)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	fixture.delivery.openGate = gate
	fixture.delivery.openStarted = started
	option := playback.SourceOption{SourceRef: "source-reference-opening", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	playID, descriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		_, _, _, openErr := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0)
		opened <- openErr
	}()
	<-started
	if err := fixture.handler.playSessions.closeSession(context.Background(), fixture.authentication.session); err != nil {
		t.Fatal(err)
	}
	select {
	case openErr := <-opened:
		if !errors.Is(openErr, context.Canceled) {
			t.Fatalf("in-flight open error=%v", openErr)
		}
	case <-time.After(time.Second):
		t.Fatal("closing the play session did not cancel its in-flight open")
	}
	if len(fixture.handler.playSessions.entries) != 0 || len(fixture.delivery.pinned) != 0 {
		t.Fatalf("closed registry retained entries=%d pins=%#v", len(fixture.handler.playSessions.entries), fixture.delivery.pinned)
	}
}

func TestPlaySessionRegistrySeekSwapReuseAndRollback(t *testing.T) {
	fixture := newPlaybackFixture(t)
	first := opaquePlaybackHandleNamed(t, "seek-first")
	second := opaquePlaybackHandleNamed(t, "seek-second")
	fixture.delivery.handles = []playback.DeliveryHandle{first, second}
	fixture.delivery.openErrors = []error{nil, nil, playback.ErrMediaSourceFailed}
	option := playback.SourceOption{SourceRef: "source-reference-seek", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	playID, descriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	_, opened, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0)
	if err != nil || !reflect.DeepEqual(opened, first) {
		t.Fatalf("initial open handle=%#v err=%v", opened, err)
	}
	_, reused, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0)
	if err != nil || !reflect.DeepEqual(reused, first) || fixture.delivery.openCalls != 1 {
		t.Fatalf("unchanged start did not reuse: handle=%#v opens=%d err=%v", reused, fixture.delivery.openCalls, err)
	}
	_, replacement, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, SecondsToTicks(60))
	if err != nil || !reflect.DeepEqual(replacement, second) || fixture.delivery.openCalls != 2 || fixture.delivery.closeCalls != 1 ||
		len(fixture.delivery.events) < 3 || !reflect.DeepEqual(fixture.delivery.events[:3], []string{"open:1", "open:2", "close:1"}) {
		t.Fatalf("seek replacement was not open-swap-close: handle=%#v opens=%d closes=%d events=%#v err=%v", replacement, fixture.delivery.openCalls, fixture.delivery.closeCalls, fixture.delivery.events, err)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, SecondsToTicks(120)); !errors.Is(err, playback.ErrMediaSourceFailed) {
		t.Fatalf("failed replacement error=%v", err)
	}
	_, retained, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, SecondsToTicks(60))
	if err != nil || !reflect.DeepEqual(retained, second) || fixture.delivery.openCalls != 3 || fixture.delivery.closeCalls != 2 {
		t.Fatalf("failed replacement did not retain old delivery and close the failed one: handle=%#v opens=%d closes=%d err=%v", retained, fixture.delivery.openCalls, fixture.delivery.closeCalls, err)
	}
}

func TestPlaySessionRegistryRefreshDuringOpenDiscardsStaleDelivery(t *testing.T) {
	fixture := newPlaybackFixture(t)
	staleHandle := opaquePlaybackHandleNamed(t, "refresh-stale")
	freshHandle := opaquePlaybackHandleNamed(t, "refresh-current")
	fixture.delivery.handles = []playback.DeliveryHandle{staleHandle, freshHandle}
	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	fixture.delivery.openGate = gate
	fixture.delivery.openStarted = started
	initialOption := playback.SourceOption{
		SourceRef: "source-reference-before-refresh", StableIdentity: "stable-refresh-source",
		Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour),
	}
	initialCapabilities := playback.Capabilities{
		StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, MaximumVideoBitrateKbps: 8000,
	}
	playID, descriptors, err := fixture.handler.playSessions.register(
		context.Background(), fixture.authentication.session, fixture.item.ID, initialCapabilities, false, []playback.SourceOption{initialOption},
	)
	if err != nil {
		t.Fatal(err)
	}

	type openResult struct {
		binding playSessionBinding
		handle  playback.DeliveryHandle
		err     error
	}
	opened := make(chan openResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		binding, handle, _, openErr := fixture.handler.playSessions.openAndTouch(ctx, fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0)
		opened <- openResult{binding: binding, handle: handle, err: openErr}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("initial native Open did not start")
	}

	refreshedCapabilities := clonePlaybackCapabilities(initialCapabilities)
	refreshedCapabilities.MaximumVideoBitrateKbps = 1000
	refreshedOption := initialOption
	refreshedOption.SourceRef = "source-reference-after-refresh"
	if reusedID, _, ok := fixture.handler.playSessions.reuseCandidate(
		fixture.authentication.session, fixture.item.ID, descriptors[0].ID, refreshedCapabilities, false, []playback.SourceOption{refreshedOption},
	); !ok || reusedID != playID {
		close(gate)
		t.Fatalf("concurrent refresh failed: id=%q ok=%t", reusedID, ok)
	}
	close(gate)

	var result openResult
	select {
	case result = <-opened:
	case <-ctx.Done():
		t.Fatal("open did not finish after binding refresh")
	}
	if result.err != nil || result.binding.MediaSourceID != descriptors[0].ID || !reflect.DeepEqual(result.handle, freshHandle) {
		t.Fatalf("open returned stale binding: binding=%+v handle=%#v err=%v", result.binding, result.handle, result.err)
	}
	if fixture.delivery.openCalls != 2 || len(fixture.delivery.inputs) != 2 ||
		fixture.delivery.inputs[0].SourceRef != initialOption.SourceRef || fixture.delivery.inputs[1].SourceRef != refreshedOption.SourceRef ||
		fixture.delivery.inputs[0].Capabilities.MaximumVideoBitrateKbps != 8000 || fixture.delivery.inputs[1].Capabilities.MaximumVideoBitrateKbps != 1000 {
		t.Fatalf("open did not retry current binding: calls=%d inputs=%#v", fixture.delivery.openCalls, fixture.delivery.inputs)
	}
	if fixture.delivery.closeCalls != 1 || len(fixture.delivery.closedHandles) != 1 || !reflect.DeepEqual(fixture.delivery.closedHandles[0], staleHandle) {
		t.Fatalf("stale delivery cleanup calls=%d handles=%#v", fixture.delivery.closeCalls, fixture.delivery.closedHandles)
	}
}

func TestPlaySessionRegistryCapabilityChangeReplacesOpenedDelivery(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.handles = []playback.DeliveryHandle{opaquePlaybackHandleNamed(t, "capability-first"), opaquePlaybackHandleNamed(t, "capability-second")}
	option := playback.SourceOption{SourceRef: "source-reference-capability", StableIdentity: "stable-capability", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	initial := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, MaximumVideoBitrateKbps: 8000}
	playID, descriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, initial, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	restricted := clonePlaybackCapabilities(initial)
	restricted.MaximumVideoBitrateKbps = 1000
	restrictedOption := option
	restrictedOption.SourceRef = "source-reference-capability-restricted"
	if reusedID, _, ok := fixture.handler.playSessions.reuseCandidate(fixture.authentication.session, fixture.item.ID, descriptors[0].ID, restricted, false, []playback.SourceOption{restrictedOption}); !ok || reusedID != playID {
		t.Fatalf("candidate reuse failed: id=%q ok=%t", reusedID, ok)
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.openCalls != 2 || fixture.delivery.closeCalls != 1 || fixture.delivery.inputs[1].Capabilities.MaximumVideoBitrateKbps != 1000 ||
		fixture.delivery.inputs[1].SourceRef != restrictedOption.SourceRef {
		t.Fatalf("capability change did not replace delivery: opens=%d closes=%d inputs=%#v", fixture.delivery.openCalls, fixture.delivery.closeCalls, fixture.delivery.inputs)
	}
	fixture.handler.playSessions.reuseCandidate(fixture.authentication.session, fixture.item.ID, descriptors[0].ID, restricted, false, []playback.SourceOption{restrictedOption})
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.openCalls != 2 {
		t.Fatalf("unchanged capabilities reopened delivery: %d", fixture.delivery.openCalls)
	}
}

func TestPlaySessionRegistryRefreshMatchesReorderedCandidatesByIdentity(t *testing.T) {
	fixture := newPlaybackFixture(t)
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	initial := []playback.SourceOption{
		{ID: "stream-1", AddonID: "addon", ManifestID: "manifest", StreamIndex: 0, StableIdentity: "stable-a", SourceRef: "old-a", Name: "A", Protocol: "http", Container: "mp4", ReportedHeight: 480, ExpiresAt: fixture.now.Add(time.Hour)},
		{ID: "stream-2", AddonID: "addon", ManifestID: "manifest", StreamIndex: 1, StableIdentity: "stable-b", SourceRef: "old-b", Name: "B", Protocol: "http", Container: "mp4", ReportedHeight: 720, ExpiresAt: fixture.now.Add(time.Hour)},
		{ID: "stream-3", AddonID: "addon", ManifestID: "manifest", StreamIndex: 2, StableIdentity: "stable-c", SourceRef: "old-c", Name: "C", Protocol: "http", Container: "mp4", ReportedHeight: 1080, ExpiresAt: fixture.now.Add(time.Hour)},
	}
	playID, descriptors, err := fixture.handler.playSessions.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, initial)
	if err != nil {
		t.Fatal(err)
	}
	fresh := []playback.SourceOption{
		{ID: "stream-1", AddonID: "addon", ManifestID: "manifest", StreamIndex: 2, StableIdentity: "stable-c", SourceRef: "new-c", Name: "https://provider.invalid/secret-c", Protocol: "http", Container: "mp4", ReportedHeight: 1080, ExpiresAt: fixture.now.Add(time.Hour)},
		{ID: "stream-2", AddonID: "addon", ManifestID: "manifest", StreamIndex: 0, StableIdentity: "stable-a", SourceRef: "new-a", Name: "Bearer secret-a", Protocol: "http", Container: "mp4", ReportedHeight: 480, ExpiresAt: fixture.now.Add(time.Hour)},
		{ID: "stream-3", AddonID: "addon", ManifestID: "manifest", StreamIndex: 1, StableIdentity: "stable-b", SourceRef: "new-b", Name: "Cookie=secret-b", Protocol: "http", Container: "mp4", ReportedHeight: 2160, ExpiresAt: fixture.now.Add(time.Hour)},
	}
	refreshedCapabilities := clonePlaybackCapabilities(capabilities)
	refreshedCapabilities.MaximumVideoBitrateKbps = 1000
	reusedID, refreshed, ok := fixture.handler.playSessions.reuseCandidate(fixture.authentication.session, fixture.item.ID, descriptors[1].ID, refreshedCapabilities, false, fresh)
	if !ok || reusedID != playID || len(refreshed) != 3 || refreshed[1].ID != descriptors[1].ID || refreshed[1].Name != "Source 2 · 2160p" {
		t.Fatalf("reordered refresh lost safe identity: id=%q ok=%t descriptors=%#v", reusedID, ok, refreshed)
	}
	for _, descriptor := range refreshed {
		if strings.Contains(descriptor.Name, "provider.invalid") || strings.Contains(descriptor.Name, "Bearer") || strings.Contains(descriptor.Name, "Cookie") {
			t.Fatalf("reordered refresh disclosed provider label: %#v", refreshed)
		}
	}
	if _, _, _, err := fixture.handler.playSessions.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[1].ID, 0); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.openCalls != 1 || fixture.delivery.inputs[0].SourceRef != "new-b" {
		t.Fatalf("reordered selection opened wrong candidate: %#v", fixture.delivery.inputs)
	}
}

func TestPlaybackInfoRejectsCapabilityRefreshWithoutStableIdentityRatherThanRemap(t *testing.T) {
	fixture := newPlaybackFixture(t)
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "source-a", Name: "A", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-b", Name: "B", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	initialProfile := `"MaxStreamingBitrate":8000000,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}`
	listed := fixture.playbackInfo(http.MethodPost, `{`+initialProfile+`}`)
	var initial PlaybackInfoResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &initial); err != nil || len(initial.MediaSources) != 2 {
		t.Fatalf("initial candidates=%#v err=%v", initial, err)
	}
	fixture.delivery.sources = playback.SourceList{Sources: []playback.SourceOption{
		{SourceRef: "fresh-b", Name: "B", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "fresh-a", Name: "A", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}}
	restrictedProfile := `"MaxStreamingBitrate":1000000,"DeviceProfile":{"DirectPlayProfiles":[{"Container":"mp4","VideoCodec":"h264","Type":"Video"}]}`
	selected := fixture.playbackInfo(http.MethodPost, fmt.Sprintf(`{"MediaSourceId":%q,%s}`, initial.MediaSources[0].Id, restrictedProfile))
	if selected.Code != http.StatusNotFound || fixture.delivery.openCalls != 1 || len(fixture.delivery.inputs) != 1 || fixture.delivery.inputs[0].SourceRef != "source-a" {
		t.Fatalf("unstable reordered refresh status=%d eagerOpens=%d inputs=%#v body=%s", selected.Code, fixture.delivery.openCalls, fixture.delivery.inputs, selected.Body.String())
	}
	entry := fixture.handler.playSessions.entries[initial.PlaySessionId]
	if entry == nil || entry.sources[initial.MediaSources[0].Id].sourceRef != "source-a" {
		t.Fatal("failed refresh changed the initially issued source binding")
	}
}

func TestPlaySessionRegistryReloginsShareStableOwnerQuota(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 3
	registry.ownerLimit = 1
	firstOwner := fixture.authentication.session
	secondOwner := playbackSessionTestRelogin(firstOwner, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	option := playback.SourceOption{SourceRef: "source-reference-relogin-quota", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	registerAndOpen := func(owner AuthenticatedSession) string {
		t.Helper()
		playID, descriptors, err := registry.register(context.Background(), owner, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := registry.openAndTouch(context.Background(), owner, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
			t.Fatal(err)
		}
		return playID
	}
	firstPlayID := registerAndOpen(firstOwner)
	secondPlayID := registerAndOpen(secondOwner)
	if registry.entries[firstPlayID] != nil || registry.entries[secondPlayID] == nil || len(registry.entries) != 1 {
		t.Fatalf("relogin quota entries=%d first=%t second=%t", len(registry.entries), registry.entries[firstPlayID] != nil, registry.entries[secondPlayID] != nil)
	}
	if registry.ownerCountLocked(firstOwner) != 1 || registry.ownerCountLocked(secondOwner) != 1 {
		t.Fatalf("stable owner counts first=%d second=%d", registry.ownerCountLocked(firstOwner), registry.ownerCountLocked(secondOwner))
	}
	if fixture.delivery.closeCalls != 1 || len(fixture.delivery.closedSessions) != 1 || fixture.delivery.closedSessions[0] != firstOwner.Principal.SessionID {
		t.Fatalf("relogin eviction closes=%d sessions=%#v", fixture.delivery.closeCalls, fixture.delivery.closedSessions)
	}
	entry := registry.entries[secondPlayID]
	mediaID := entry.sourceOrder[0]
	if _, err := registry.resolveAndTouch(firstOwner, fixture.item.ID, secondPlayID, mediaID); !errors.Is(err, errPlaySessionNotFound) {
		t.Fatalf("stable quota identity widened exact lookup ownership: %v", err)
	}
}

func TestPlaySessionRegistryOwnerQuotaNeverEvictsAnotherOwner(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 2
	registry.ownerLimit = 1
	ownerA := fixture.authentication.session
	ownerB := playbackSessionTestOtherOwner(ownerA, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	option := playback.SourceOption{SourceRef: "source-reference-quota", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	registerAndOpen := func(owner AuthenticatedSession) string {
		t.Helper()
		playID, descriptors, err := registry.register(context.Background(), owner, fixture.item.ID, capabilities, false, []playback.SourceOption{option})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := registry.openAndTouch(context.Background(), owner, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
			t.Fatal(err)
		}
		return playID
	}
	registerAndOpen(ownerA)
	ownerBPlayID := registerAndOpen(ownerB)
	registerAndOpen(ownerA)
	registerAndOpen(ownerA)
	if len(registry.entries) != 2 || registry.entries[ownerBPlayID] == nil {
		t.Fatalf("owner flooding displaced another owner: entries=%d owner B present=%t", len(registry.entries), registry.entries[ownerBPlayID] != nil)
	}
	for _, sessionID := range fixture.delivery.closedSessions {
		if sessionID != ownerA.Principal.SessionID {
			t.Fatalf("owner flooding closed foreign session %q; closes=%#v", sessionID, fixture.delivery.closedSessions)
		}
	}
	if registry.ownerCountLocked(ownerA) != 1 || registry.ownerCountLocked(ownerB) != 1 {
		t.Fatalf("per-owner quota not enforced: A=%d B=%d", registry.ownerCountLocked(ownerA), registry.ownerCountLocked(ownerB))
	}
}

func TestDeviceProfileCacheNeverEvictsAnotherOwnerAtGlobalLimit(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 1
	registry.ownerLimit = 2
	registry.userLimit = 2
	ownerA := fixture.authentication.session
	ownerB := playbackSessionTestOtherOwner(ownerA, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	profile := conservativeCompatibilityProfile()
	if !registry.setDeviceProfile(ownerA, profile) {
		t.Fatal("first owner profile was rejected")
	}
	if registry.setDeviceProfile(ownerB, profile) {
		t.Fatal("global saturation admitted a second owner by evicting the first")
	}
	if _, ok := registry.deviceProfile(ownerA); !ok {
		t.Fatal("global saturation evicted the existing owner")
	}
	if _, ok := registry.deviceProfile(ownerB); ok || len(registry.deviceProfiles) != 1 {
		t.Fatalf("unexpected device profile cache state: ownerB=%t entries=%d", ok, len(registry.deviceProfiles))
	}
}

func TestDeviceProfileCacheReloginsShareStableOwnerQuota(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 3
	registry.ownerLimit = 1
	first := fixture.authentication.session
	second := playbackSessionTestRelogin(first, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	profile := conservativeCompatibilityProfile()
	if !registry.setDeviceProfile(first, profile) || !registry.setDeviceProfile(second, profile) {
		t.Fatal("stable owner profile admission failed")
	}
	if _, ok := registry.deviceProfile(first); ok {
		t.Fatal("stable owner quota retained the older relogin")
	}
	if _, ok := registry.deviceProfile(second); !ok || len(registry.deviceProfiles) != 1 {
		t.Fatalf("stable owner profile missing=%t entries=%d", !ok, len(registry.deviceProfiles))
	}
}

func TestPlaySessionRegistryGlobalSaturationRejectsWithoutCrossOwnerClose(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 1
	registry.ownerLimit = 2
	ownerA := fixture.authentication.session
	ownerB := playbackSessionTestOtherOwner(ownerA, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	option := playback.SourceOption{SourceRef: "source-reference-capacity", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	playID, descriptors, err := registry.register(context.Background(), ownerA, fixture.item.ID, capabilities, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), ownerA, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.register(context.Background(), ownerB, fixture.item.ID, capabilities, false, []playback.SourceOption{option}); !errors.Is(err, playback.ErrMediaCapacityReached) {
		t.Fatalf("global saturation error=%v", err)
	}
	if registry.entries[playID] == nil || fixture.delivery.closeCalls != 0 {
		t.Fatalf("global saturation terminated existing owner: present=%t closes=%d", registry.entries[playID] != nil, fixture.delivery.closeCalls)
	}
}

func TestPlaySessionRegistryAdmissionPinRollbackOnGlobalRejection(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 1
	registry.ownerLimit = 2
	registry.userLimit = 2
	ownerA := fixture.authentication.session
	ownerB := playbackSessionTestOtherOwner(ownerA, "foreign-compat", "foreign-native")
	optionA := playback.SourceOption{SourceRef: "source-a", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	optionB := playback.SourceOption{SourceRef: "source-b", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	if _, _, err := registry.register(context.Background(), ownerA, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{optionA}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.register(context.Background(), ownerB, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{optionB}); !errors.Is(err, playback.ErrMediaCapacityReached) {
		t.Fatalf("global rejection error=%v", err)
	}
	if fixture.delivery.pinCalls != 2 || fixture.delivery.unpinCalls != 1 || fixture.delivery.pinned[optionA.SourceRef] != 1 || fixture.delivery.pinned[optionB.SourceRef] != 0 {
		t.Fatalf("pin rollback calls=%d/%d pins=%#v", fixture.delivery.pinCalls, fixture.delivery.unpinCalls, fixture.delivery.pinned)
	}
}

func TestPlaySessionRegistryUserAggregateAcrossThirtyTwoDevicesLeavesOtherAccount(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.limit = 64
	registry.userLimit = 8
	registry.ownerLimit = 2
	base := fixture.authentication.session
	option := playback.SourceOption{SourceRef: "shared-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	for device := range 32 {
		owner := playbackSessionTestRelogin(base, fmt.Sprintf("compat-%d", device), fmt.Sprintf("native-%d", device))
		owner.Client.DeviceID = fmt.Sprintf("device-%d", device)
		owner.Principal.DeviceID = fmt.Sprintf("native-device-%d", device)
		if _, _, err := registry.register(context.Background(), owner, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option}); err != nil {
			t.Fatalf("device %d registration: %v", device, err)
		}
	}
	if count := registry.userCountLocked(base.Principal.UserID); count != registry.userLimit || len(registry.entries) != registry.userLimit {
		t.Fatalf("shared user entries=%d total=%d", count, len(registry.entries))
	}
	foreign := playbackSessionTestOtherOwner(base, "foreign-compat", "foreign-native")
	foreignOption := playback.SourceOption{SourceRef: "foreign-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	foreignID, _, err := registry.register(context.Background(), foreign, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{foreignOption})
	if err != nil || registry.entries[foreignID] == nil || len(registry.entries) != registry.userLimit+1 {
		t.Fatalf("foreign admission id=%q err=%v entries=%d", foreignID, err, len(registry.entries))
	}
}

func TestPlaySessionRegistryCleanupFanoutIsBounded(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.cleanupWorkers = 3
	registry.cleanupTimeout = time.Second
	options := make([]playback.SourceOption, 12)
	fixture.delivery.handles = make([]playback.DeliveryHandle, len(options))
	for index := range options {
		options[index] = playback.SourceOption{SourceRef: fmt.Sprintf("cleanup-source-%d", index), Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
		fixture.delivery.handles[index] = opaquePlaybackHandleNamed(t, fmt.Sprintf("cleanup-%d", index))
	}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptor.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	gate := make(chan struct{})
	started := make(chan struct{}, len(options))
	fixture.delivery.closeGate = gate
	fixture.delivery.closeStarted = started
	done := make(chan struct{})
	go func() {
		registry.closeAll(context.Background())
		close(done)
	}()
	for range registry.cleanupWorkers {
		select {
		case <-started:
		case <-time.After(250 * time.Millisecond):
			close(gate)
			t.Fatal("cleanup workers did not start")
		}
	}
	fixture.delivery.mu.Lock()
	maximum := fixture.delivery.closeMaxActive
	fixture.delivery.mu.Unlock()
	if maximum != registry.cleanupWorkers {
		close(gate)
		t.Fatalf("cleanup max concurrency=%d want=%d", maximum, registry.cleanupWorkers)
	}
	select {
	case <-started:
		close(gate)
		t.Fatal("cleanup exceeded worker bound while workers were blocked")
	case <-time.After(25 * time.Millisecond):
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bounded cleanup did not finish")
	}
	if fixture.delivery.closeCalls != len(options) || len(fixture.delivery.pinned) != 0 {
		t.Fatalf("cleanup closes=%d pins=%#v", fixture.delivery.closeCalls, fixture.delivery.pinned)
	}
}

func TestPlaySessionRegistryRetainsFailedCloseUntilRetrySucceeds(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return fixture.now }
	registry.cleanupRetryBase = time.Second
	registry.cleanupRetryMax = time.Second
	closeFailure := errors.New("native close unavailable")
	fixture.delivery.closeErrors = []error{closeFailure, nil}
	option := playback.SourceOption{SourceRef: "retry-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	entry := registry.entries[playID]
	if err := registry.closeSession(context.Background(), fixture.authentication.session); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.closeCalls != 1 || len(registry.cleanupPending) != 1 || len(registry.entries) != 0 || entry.sources[descriptors[0].ID].handle.Valid() {
		t.Fatalf("failed close ownership calls=%d pending=%d entries=%d attached=%t", fixture.delivery.closeCalls, len(registry.cleanupPending), len(registry.entries), entry.sources[descriptors[0].ID].handle.Valid())
	}
	if fixture.delivery.unpinCalls != 1 || len(fixture.delivery.pinned) != 0 {
		t.Fatalf("failed close pin release calls=%d pins=%#v", fixture.delivery.unpinCalls, fixture.delivery.pinned)
	}
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 1 {
		t.Fatalf("retry ignored backoff: calls=%d", fixture.delivery.closeCalls)
	}
	fixture.now = fixture.now.Add(time.Second)
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 2 || len(registry.cleanupPending) != 0 || fixture.delivery.unpinCalls != 1 {
		t.Fatalf("successful retry calls=%d pending=%d unpins=%d", fixture.delivery.closeCalls, len(registry.cleanupPending), fixture.delivery.unpinCalls)
	}
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 2 {
		t.Fatalf("successful handle closed again: calls=%d", fixture.delivery.closeCalls)
	}
}

func TestPlaySessionRegistryRepeatedCloseFailuresRespectBackoffAndDeadline(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return fixture.now }
	registry.cleanupRetryBase = time.Second
	registry.cleanupRetryMax = 2 * time.Second
	registry.cleanupRetryTTL = 5 * time.Second
	fixture.delivery.closeErr = errors.New("persistent native close failure")
	handle := opaquePlaybackHandleNamed(t, "deadline")
	registry.closeHandle(context.Background(), fixture.authentication.session.Principal, handle)
	registry.closeHandle(context.Background(), fixture.authentication.session.Principal, handle)
	for range 3 {
		registry.reap(context.Background())
	}
	if fixture.delivery.closeCalls != 1 || len(registry.cleanupPending) != 1 {
		t.Fatalf("immediate retry storm calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
	fixture.now = fixture.now.Add(time.Second)
	registry.reap(context.Background())
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 2 {
		t.Fatalf("first backoff calls=%d", fixture.delivery.closeCalls)
	}
	fixture.now = fixture.now.Add(2 * time.Second)
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 3 || len(registry.cleanupPending) != 1 {
		t.Fatalf("bounded retry calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
	fixture.now = fixture.now.Add(2 * time.Second)
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 3 || len(registry.cleanupPending) != 0 {
		t.Fatalf("absolute retry deadline calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
}

func TestPlaySessionRegistryExpiredCloseRetriesAndNotFoundCompletes(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return fixture.now }
	registry.cleanupRetryBase = time.Second
	registry.cleanupRetryMax = time.Second
	fixture.delivery.closeErrors = []error{errors.New("close failed"), nil}
	option := playback.SourceOption{SourceRef: "expired-retry-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	registry.entries[playID].expiresAt = fixture.now
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 1 || len(registry.cleanupPending) != 1 || fixture.delivery.unpinCalls != 1 {
		t.Fatalf("expired cleanup calls=%d pending=%d unpins=%d", fixture.delivery.closeCalls, len(registry.cleanupPending), fixture.delivery.unpinCalls)
	}
	fixture.now = fixture.now.Add(time.Second)
	registry.reap(context.Background())
	if fixture.delivery.closeCalls != 2 || len(registry.cleanupPending) != 0 || fixture.delivery.unpinCalls != 1 {
		t.Fatalf("expired retry calls=%d pending=%d unpins=%d", fixture.delivery.closeCalls, len(registry.cleanupPending), fixture.delivery.unpinCalls)
	}
	fixture.delivery.closeErrors = nil
	fixture.delivery.closeErr = playback.ErrSessionNotFound
	registry.closeHandle(context.Background(), fixture.authentication.session.Principal, opaquePlaybackHandleNamed(t, "already-gone"))
	if fixture.delivery.closeCalls != 3 || len(registry.cleanupPending) != 0 {
		t.Fatalf("not-found cleanup calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
}

func TestPlaySessionRegistryRunRetriesCleanupAtBackoffWithoutWaitingForReaper(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return time.Now().UTC() }
	registry.reapPeriod = time.Hour
	registry.cleanupRetryBase = 10 * time.Millisecond
	registry.cleanupRetryMax = 10 * time.Millisecond
	registry.cleanupTimeout = 250 * time.Millisecond
	fixture.delivery.closeErrors = []error{errors.New("transient close failure"), nil}
	finished := make(chan struct{}, 2)
	fixture.delivery.closeFinished = finished
	registry.closeHandle(context.Background(), fixture.authentication.session.Principal, opaquePlaybackHandleNamed(t, "runtime-retry"))
	select {
	case <-finished:
	default:
		t.Fatal("initial failed close did not finish")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		registry.run(ctx)
		close(done)
	}()
	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		cancel()
		t.Fatal("cleanup retry waited for the reaper instead of its backoff")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("registry run did not stop after retry test")
	}
	fixture.delivery.mu.Lock()
	closeCalls := fixture.delivery.closeCalls
	fixture.delivery.mu.Unlock()
	registry.cleanupMu.Lock()
	pending := len(registry.cleanupPending)
	registry.cleanupMu.Unlock()
	if closeCalls != 2 || pending != 0 {
		t.Fatalf("runtime retry calls=%d pending=%d", closeCalls, pending)
	}
}
func TestPlaySessionRegistryShutdownDrainsPastInitialCleanupTimeout(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = advancingPlaybackTestClock(fixture.now)
	registry.cleanupTimeout = 10 * time.Millisecond
	registry.cleanupRetryBase = 25 * time.Millisecond
	registry.cleanupRetryMax = 25 * time.Millisecond
	registry.cleanupRetryTTL = 250 * time.Millisecond
	fixture.delivery.closeErrors = []error{errors.New("native close unavailable"), nil}
	finished := make(chan struct{}, 2)
	fixture.delivery.closeFinished = finished
	option := playback.SourceOption{SourceRef: "shutdown-drain-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		registry.run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-finished:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("initial generation cleanup did not finish")
	}
	select {
	case <-done:
		t.Fatal("generation retired before its pending close retry")
	default:
	}
	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("generation did not retry cleanup after the initial cleanup timeout")
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("generation did not retire after the successful cleanup retry")
	}
	fixture.delivery.mu.Lock()
	closeCalls := fixture.delivery.closeCalls
	fixture.delivery.mu.Unlock()
	registry.cleanupMu.Lock()
	pending, owned := len(registry.cleanupPending), len(registry.cleanupOwned)
	registry.cleanupMu.Unlock()
	if closeCalls != 2 || pending != 0 || owned != 0 {
		t.Fatalf("drained generation closes=%d pending=%d owned=%d", closeCalls, pending, owned)
	}
}

func TestPlaySessionRegistryShutdownAbandonsPermanentFailureAtRetryTTL(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return time.Now().UTC() }
	registry.cleanupTimeout = 5 * time.Millisecond
	registry.cleanupRetryBase = 2 * time.Millisecond
	registry.cleanupRetryMax = 2 * time.Millisecond
	registry.cleanupRetryTTL = 25 * time.Millisecond
	fixture.delivery.closeErr = errors.New("permanent native close failure")
	registry.closeHandle(context.Background(), fixture.authentication.session.Principal, opaquePlaybackHandleNamed(t, "permanent-shutdown-failure"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		registry.run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("permanent cleanup failure outlived its retry TTL")
	}
	registry.cleanupMu.Lock()
	pending, owned := len(registry.cleanupPending), len(registry.cleanupOwned)
	registry.cleanupMu.Unlock()
	fixture.delivery.mu.Lock()
	closeCalls := fixture.delivery.closeCalls
	maximum := fixture.delivery.closeMaxActive
	fixture.delivery.mu.Unlock()
	if closeCalls < 2 || closeCalls > 32 || maximum > registry.cleanupWorkers || pending != 0 || owned != 0 {
		t.Fatalf("TTL cleanup closes=%d maxActive=%d pending=%d owned=%d", closeCalls, maximum, pending, owned)
	}
}

func TestPlaySessionRegistryShutdownRetainsGenerationForInFlightOpenHandle(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = advancingPlaybackTestClock(fixture.now)
	registry.cleanupTimeout = 5 * time.Millisecond
	registry.cleanupRetryTTL = 250 * time.Millisecond
	option := playback.SourceOption{SourceRef: "in-flight-retirement-source", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, []playback.SourceOption{option})
	if err != nil {
		t.Fatal(err)
	}
	openGate := make(chan struct{})
	openStarted := make(chan struct{}, 1)
	fixture.delivery.openGate = openGate
	fixture.delivery.openStarted = openStarted
	fixture.delivery.openIgnoreContext = true
	openResult := make(chan error, 1)
	go func() {
		_, _, _, openErr := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0)
		openResult <- openErr
	}()
	select {
	case <-openStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("in-flight native open did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		registry.run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		close(openGate)
		t.Fatal("retired generation dropped its in-flight cleanup reservation")
	case <-time.After(25 * time.Millisecond):
	}
	close(openGate)
	select {
	case openErr := <-openResult:
		if !errors.Is(openErr, errPlaySessionNotFound) {
			t.Fatalf("retired in-flight open error=%v", openErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("retired in-flight open did not release its handle")
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("generation did not retire after the in-flight handle closed")
	}
	registry.cleanupMu.Lock()
	pending, owned, reservations := len(registry.cleanupPending), len(registry.cleanupOwned), registry.cleanupReservations
	registry.cleanupMu.Unlock()
	fixture.delivery.mu.Lock()
	closeCalls := fixture.delivery.closeCalls
	fixture.delivery.mu.Unlock()
	if closeCalls != 1 || pending != 0 || owned != 0 || reservations != 0 {
		t.Fatalf("in-flight retirement closes=%d pending=%d owned=%d reservations=%d", closeCalls, pending, owned, reservations)
	}
}

func TestPlaySessionRegistryCleanupCapacityRejectsBeforeOpeningUnownedHandle(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.cleanupQueueLimit = 1
	options := []playback.SourceOption{
		{SourceRef: "capacity-source-one", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "capacity-source-two", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, playback.Capabilities{}, false, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptors[1].ID, 0); !errors.Is(err, playback.ErrMediaCapacityReached) {
		t.Fatalf("cleanup capacity open error=%v", err)
	}
	if fixture.delivery.openCalls != 1 || len(registry.cleanupOwned) != 1 || len(registry.cleanupPending) != 0 {
		t.Fatalf("cleanup capacity opens=%d owned=%d pending=%d", fixture.delivery.openCalls, len(registry.cleanupOwned), len(registry.cleanupPending))
	}
	if err := registry.closeSession(context.Background(), fixture.authentication.session); err != nil {
		t.Fatal(err)
	}
	if fixture.delivery.closeCalls != 1 || len(registry.cleanupOwned) != 0 || len(registry.cleanupPending) != 0 {
		t.Fatalf("cleanup capacity release closes=%d owned=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupOwned), len(registry.cleanupPending))
	}
}

func TestPlaySessionRegistryShutdownRetriesPendingCloseBestEffort(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = func() time.Time { return fixture.now }
	registry.cleanupRetryBase = time.Millisecond
	registry.cleanupRetryMax = time.Millisecond
	fixture.delivery.closeErrors = []error{errors.New("close failed before shutdown"), nil}
	registry.closeHandle(context.Background(), fixture.authentication.session.Principal, opaquePlaybackHandleNamed(t, "shutdown-retry"))
	if fixture.delivery.closeCalls != 1 || len(registry.cleanupPending) != 1 {
		t.Fatalf("pre-shutdown cleanup calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
	registry.closeAll(context.Background())
	if fixture.delivery.closeCalls != 2 || len(registry.cleanupPending) != 0 {
		t.Fatalf("shutdown retry calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
}

func TestPlaySessionRegistryShutdownWaitsForClaimedCloseThenRetries(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.now = advancingPlaybackTestClock(fixture.now)
	registry.cleanupTimeout = 250 * time.Millisecond
	registry.cleanupRetryBase = time.Millisecond
	registry.cleanupRetryMax = time.Millisecond
	fixture.delivery.closeErrors = []error{errors.New("in-flight close failed"), nil}
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	fixture.delivery.closeGate = gate
	handle := opaquePlaybackHandleNamed(t, "claimed-shutdown")
	fixture.delivery.closeStarted = started
	firstDone := make(chan struct{})
	go func() {
		registry.closeHandle(context.Background(), fixture.authentication.session.Principal, handle)
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		close(gate)
		t.Fatal("initial cleanup was not claimed")
	}
	shutdownDone := make(chan struct{})
	go func() {
		registry.closeAll(context.Background())
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		close(gate)
		t.Fatal("shutdown returned while cleanup claim was in flight")
	case <-time.After(10 * time.Millisecond):
	}
	close(gate)
	select {
	case <-firstDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("initial claimed cleanup did not finish")
	}
	select {
	case <-shutdownDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("shutdown did not retry completed failed claim")
	}
	if fixture.delivery.closeCalls != 2 || len(registry.cleanupPending) != 0 {
		t.Fatalf("claimed shutdown retry calls=%d pending=%d", fixture.delivery.closeCalls, len(registry.cleanupPending))
	}
}

func TestPlaySessionRegistryShutdownHasTotalDeadlineAndIdempotentCleanup(t *testing.T) {
	fixture := newPlaybackFixture(t)
	registry := fixture.handler.playSessions
	registry.cleanupTimeout = 100 * time.Millisecond
	registry.cleanupRetryTTL = 100 * time.Millisecond
	fixture.delivery.handles = []playback.DeliveryHandle{opaquePlaybackHandleNamed(t, "shutdown-one"), opaquePlaybackHandleNamed(t, "shutdown-two")}
	options := []playback.SourceOption{
		{SourceRef: "source-reference-shutdown-1", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
		{SourceRef: "source-reference-shutdown-2", Protocol: "http", Container: "mp4", ExpiresAt: fixture.now.Add(time.Hour)},
	}
	capabilities := playback.Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}}
	playID, descriptors, err := registry.register(context.Background(), fixture.authentication.session, fixture.item.ID, capabilities, false, options)
	if err != nil {
		t.Fatal(err)
	}
	entry := registry.entries[playID]
	for _, descriptor := range descriptors {
		if _, _, _, err := registry.openAndTouch(context.Background(), fixture.authentication.session, fixture.item.ID, playID, descriptor.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	closeGate := make(chan struct{})
	fixture.delivery.closeGate = closeGate
	closeStarted := make(chan struct{}, 2)
	fixture.delivery.closeStarted = closeStarted
	closeFinished := make(chan struct{}, 4)
	fixture.delivery.closeFinished = closeFinished
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	startedAt := time.Now()
	go func() {
		registry.run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		close(closeGate)
		t.Fatal("registry shutdown exceeded its total cleanup deadline")
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		close(closeGate)
		t.Fatalf("registry shutdown took %s", elapsed)
	}
	for range 2 {
		select {
		case <-closeStarted:
		case <-time.After(100 * time.Millisecond):
			close(closeGate)
			t.Fatal("shutdown returned before invoking every delivery close")
		}
	}
	if fixture.delivery.closeCalls != 2 || len(registry.entries) != 0 || len(fixture.delivery.pinned) != 0 {
		close(closeGate)
		t.Fatalf("shutdown cleanup calls=%d entries=%d pins=%#v", fixture.delivery.closeCalls, len(registry.entries), fixture.delivery.pinned)
	}
	for _, source := range entry.sources {
		if source.handle.Valid() {
			close(closeGate)
			t.Fatal("shutdown left a delivery handle attached")
		}
	}
	close(closeGate)
	for range 2 {
		select {
		case <-closeFinished:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timed-out shutdown close did not finish")
		}
	}
	registry.cleanupTimeout = 500 * time.Millisecond
	registry.closeAll(context.Background())
	registry.cleanupMu.Lock()
	pendingAfterRetry := len(registry.cleanupPending)
	pendingStates := make([]string, 0, pendingAfterRetry)
	for _, item := range registry.cleanupPending {
		pendingStates = append(pendingStates, fmt.Sprintf("claimed=%t attempts=%d next=%s deadline=%s", item.claimed, item.attempts, item.nextAttempt, item.deadline))
	}
	registry.cleanupMu.Unlock()
	fixture.delivery.mu.Lock()
	afterRetry := fixture.delivery.closeCalls
	fixture.delivery.mu.Unlock()
	if pendingAfterRetry != 0 {
		t.Fatalf("shutdown retries left pending handles=%d calls=%d states=%v", pendingAfterRetry, afterRetry, pendingStates)
	}
	registry.closeAll(context.Background())
	if fixture.delivery.closeCalls != afterRetry {
		t.Fatalf("idempotent close repeated successful native cleanup: before=%d after=%d", afterRetry, fixture.delivery.closeCalls)
	}
}

func playbackSessionTestRelogin(base AuthenticatedSession, compatSessionID, nativeSessionID string) AuthenticatedSession {
	owner := base
	owner.ID = compatSessionID
	owner.Principal = clonePrincipal(base.Principal)
	owner.Principal.SessionID = nativeSessionID
	return owner
}

func playbackSessionTestOtherOwner(base AuthenticatedSession, compatSessionID, nativeSessionID string) AuthenticatedSession {
	owner := playbackSessionTestRelogin(base, compatSessionID, nativeSessionID)
	owner.Principal.UserID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	owner.Client.DeviceID = "other-device-id"
	owner.Principal.DeviceID = "other-native-device"
	return owner
}

type playbackFixture struct {
	now            time.Time
	token          string
	item           watchstate.CatalogTitle
	authentication *fakeCompatPlaybackAuthentication
	delivery       *fakeCompatPlaybackDelivery
	handler        *Handler
}

func newPlaybackFixture(t *testing.T) *playbackFixture {
	t.Helper()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	profileID := "22222222-2222-4222-8222-222222222222"
	session := AuthenticatedSession{
		ID: "33333333-3333-4333-8333-333333333333", ProfileID: profileID, ProfileName: "Main", Client: ClientIdentity{Client: "Generic Client", Device: "Living Room", DeviceID: "device-id", Version: "1.0"}, ExpiresAt: now.Add(24 * time.Hour),
		Principal: auth.Principal{SessionID: "44444444-4444-4444-8444-444444444444", UserID: "55555555-5555-4555-8555-555555555555", DeviceID: "native-device", ActiveProfileID: &profileID},
	}
	token := "rivune_jf_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	authentication := &fakeCompatPlaybackAuthentication{session: session}
	runtimeMinutes := 121
	item := watchstate.CatalogTitle{ID: "11111111-1111-4111-8111-111111111111", MediaType: "movie", ResourceID: "tt-provider-token-secret", RuntimeMinutes: &runtimeMinutes}
	delivery := &fakeCompatPlaybackDelivery{handle: opaquePlaybackHandle(t), now: now}
	catalog := &fakeCompatPlaybackCatalog{item: item}
	handler := &Handler{authentication: authentication, catalog: catalog, playback: delivery}
	handler.playSessions = newPlaySessionRegistry(delivery)
	handler.playSessions.now = func() time.Time { return now }
	return &playbackFixture{now: now, token: token, item: item, authentication: authentication, delivery: delivery, handler: handler}
}

func (fixture *playbackFixture) playbackInfo(method, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/Items/"+fixture.item.ID+"/PlaybackInfo", bytes.NewBufferString(body))
	request.SetPathValue("id", fixture.item.ID)
	request.Header.Set("X-Emby-Token", fixture.token)
	response := httptest.NewRecorder()
	fixture.handler.handlePlaybackInfo(response, request)
	return response
}

type fakeCompatPlaybackAuthentication struct {
	session         AuthenticatedSession
	sessions        map[string]AuthenticatedSession
	revalidateErr   error
	revalidateCalls int
}

func (*fakeCompatPlaybackAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, errors.New("not used")
}
func (authentication *fakeCompatPlaybackAuthentication) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	if authentication.sessions != nil {
		session, exists := authentication.sessions[token]
		if !exists {
			return AuthenticatedSession{}, ErrInvalidCompatCredential
		}
		return session, nil
	}
	return authentication.session, nil
}
func (authentication *fakeCompatPlaybackAuthentication) Revalidate(_ context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	authentication.revalidateCalls++
	if authentication.revalidateErr != nil {
		return AuthenticatedSession{}, authentication.revalidateErr
	}
	if !sameAuthenticatedSessionOwner(expected, authentication.session) {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return authentication.session, nil
}

func (*fakeCompatPlaybackAuthentication) Logout(context.Context, AuthenticatedSession) error {
	return nil
}

type fakeCompatPlaybackCatalog struct {
	item   watchstate.CatalogTitle
	series *watchstate.CatalogTitle
}

func (catalog *fakeCompatPlaybackCatalog) GetCatalogTitle(_ context.Context, _ auth.Principal, id string) (watchstate.CatalogTitle, error) {
	if id == catalog.item.ID {
		return catalog.item, nil
	}
	if catalog.series != nil && id == catalog.series.ID {
		return *catalog.series, nil
	}
	return watchstate.CatalogTitle{}, watchstate.ErrNotFound
}
func (*fakeCompatPlaybackCatalog) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, errors.New("not used")
}

func advancingPlaybackTestClock(base time.Time) func() time.Time {
	startedAt := time.Now()
	return func() time.Time { return base.Add(time.Since(startedAt)) }
}

type fakeCompatPlaybackDelivery struct {
	mu                            sync.Mutex
	now                           time.Time
	sources                       playback.SourceList
	handle                        playback.DeliveryHandle
	handles                       []playback.DeliveryHandle
	openErrors                    []error
	closeErrors                   []error
	closeErr                      error
	sourceCalls                   int
	sourceInputs                  []playback.SourcesInput
	inputs                        []playback.ResolveInput
	openCalls                     int
	serveCalls                    int
	serveAssetCalls               int
	servedAssetID                 string
	closeCalls                    int
	closedHandles                 []playback.DeliveryHandle
	closedSessions                []string
	events                        []string
	serveErr                      error
	servedMethod                  string
	servedPath                    string
	servedRange                   string
	servedStartTicks              string
	servedAPIKey                  string
	servedAPIKeyCount             int
	servedProfileQueryCredential  bool
	servedProfileHeaderCredential bool
	openGate                      <-chan struct{}
	openIgnoreContext             bool
	openStarted                   chan<- struct{}
	servedChildID                 string
	closeGate                     <-chan struct{}
	closeStarted                  chan<- struct{}
	closeFinished                 chan<- struct{}
	pinCalls                      int
	unpinCalls                    int
	pinErr                        error
	pinned                        map[string]int
	closeActive                   int
	closeMaxActive                int
}

func (delivery *fakeCompatPlaybackDelivery) Sources(_ context.Context, _ auth.Principal, input playback.SourcesInput) (playback.SourceList, error) {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	delivery.sourceCalls++
	delivery.sourceInputs = append(delivery.sourceInputs, input)
	return delivery.sources, nil
}
func (delivery *fakeCompatPlaybackDelivery) SourcesAndPin(ctx context.Context, principal auth.Principal, input playback.SourcesInput) (playback.SourceList, error) {
	list, err := delivery.Sources(ctx, principal, input)

	if err != nil {
		return playback.SourceList{}, err
	}
	if err := delivery.PinSourceReferences(principal, sourceOptionReferenceIDs(list.Sources)); err != nil {
		return playback.SourceList{}, err
	}
	return list, nil
}
func (delivery *fakeCompatPlaybackDelivery) PinSourceReferences(_ auth.Principal, references []string) error {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	delivery.pinCalls++
	if delivery.pinErr != nil {
		return delivery.pinErr
	}
	if delivery.pinned == nil {
		delivery.pinned = make(map[string]int)
	}
	for _, reference := range references {
		delivery.pinned[reference]++
	}
	return nil
}
func (delivery *fakeCompatPlaybackDelivery) UnpinSourceReferences(_ auth.Principal, references []string) {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	delivery.unpinCalls++
	for _, reference := range references {
		if delivery.pinned[reference] <= 1 {
			delete(delivery.pinned, reference)
		} else {
			delivery.pinned[reference]--
		}
	}
}
func (delivery *fakeCompatPlaybackDelivery) Open(ctx context.Context, _ auth.Principal, input playback.ResolveInput) (playback.Delivery, error) {
	delivery.mu.Lock()
	call := delivery.openCalls
	delivery.openCalls++
	delivery.inputs = append(delivery.inputs, input)
	delivery.events = append(delivery.events, fmt.Sprintf("open:%d", call+1))
	gate, started := delivery.openGate, delivery.openStarted
	ignoreContext := delivery.openIgnoreContext
	handle := delivery.handle
	if call < len(delivery.handles) {
		handle = delivery.handles[call]
	}
	var openErr error
	if call < len(delivery.openErrors) {
		openErr = delivery.openErrors[call]
	}
	delivery.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		if ignoreContext {
			<-gate
		} else {
			select {
			case <-gate:
			case <-ctx.Done():
				return playback.Delivery{}, ctx.Err()
			}
		}
	}
	if openErr != nil {
		return playback.Delivery{Handle: handle}, openErr
	}
	selectedAudio := 2
	return playback.Delivery{
		Handle: handle,
		Session: playback.Session{
			SelectedSourceID: "native-selected", SelectedAudioTrack: &selectedAudio,
			Sources: []playback.Source{{
				ID: "native-selected", Mode: "direct", Protocol: "http", Container: "mp4", URL: "https://provider.invalid/video?token=provider-secret",
				Media: &playback.MediaInspection{
					Container: "mp4", DurationSeconds: 3600, HDRFormat: "hdr10",
					VideoTracks:    []playback.MediaTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, BitrateKbps: 4000}},
					AudioTracks:    []playback.MediaTrack{{Index: 1, Codec: "truehd", Language: "en", Channels: 8, BitrateKbps: 3000}, {Index: 2, Codec: "eac3", Language: "fr", Channels: 6, BitrateKbps: 768}},
					SubtitleTracks: []playback.MediaTrack{{Index: 3, Codec: "srt", Language: "fr"}},
				},
			}},
			Subtitles: []playback.Subtitle{
				{
					ID: "embedded-subtitle-3", Language: "fr", Delivery: "external",
					URL: "/api/v1/playback/session/asset/embedded-subtitle-3?token=playback-capability",
				},
				{
					ID: "subtitle-1", Language: "en", Forced: true, Delivery: "external",
					URL: "https://provider.invalid/subtitle.vtt?native_token=provider-secret",
				},
			},
		},
	}, nil
}
func (delivery *fakeCompatPlaybackDelivery) Serve(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle) error {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if !handle.Valid() {
		return playback.ErrSessionNotFound
	}
	delivery.serveCalls++
	delivery.servedMethod = request.Method
	delivery.servedPath = request.URL.Path
	delivery.servedRange = request.Header.Get("Range")
	delivery.servedStartTicks = request.URL.Query().Get("StartTimeTicks")
	delivery.servedAPIKey = request.URL.Query().Get("api_key")
	for name, entries := range request.URL.Query() {
		switch {
		case strings.EqualFold(name, "api_key"):
			delivery.servedAPIKeyCount += len(entries)
		case strings.EqualFold(name, "X-Emby-Token"), strings.EqualFold(name, "X-MediaBrowser-Token"):
			delivery.servedProfileQueryCredential = true
		}
	}
	delivery.servedChildID = request.URL.Query().Get("RivuneChildId")
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token", "X-Emby-Authorization", "X-MediaBrowser-Authorization", "Authorization"} {
		if len(request.Header.Values(name)) != 0 {
			delivery.servedProfileHeaderCredential = true
		}
	}

	if delivery.serveErr != nil {
		return delivery.serveErr
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}
func (delivery *fakeCompatPlaybackDelivery) ServeAsset(response http.ResponseWriter, request *http.Request, handle playback.DeliveryHandle, assetID string) error {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if !handle.Valid() {
		return playback.ErrSessionNotFound
	}
	delivery.serveAssetCalls++
	delivery.servedAssetID = assetID
	delivery.servedMethod = request.Method
	delivery.servedPath = request.URL.Path
	delivery.servedRange = request.Header.Get("Range")
	delivery.servedStartTicks = request.URL.Query().Get("StartTimeTicks")
	delivery.servedAPIKey = request.URL.Query().Get("api_key")
	if delivery.serveErr != nil {
		return delivery.serveErr
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (delivery *fakeCompatPlaybackDelivery) Close(ctx context.Context, principal auth.Principal, handle playback.DeliveryHandle) error {
	delivery.mu.Lock()
	call := delivery.closeCalls
	delivery.closeCalls++
	delivery.closeActive++
	if delivery.closeActive > delivery.closeMaxActive {
		delivery.closeMaxActive = delivery.closeActive
	}
	delivery.closedHandles = append(delivery.closedHandles, handle)
	delivery.closedSessions = append(delivery.closedSessions, principal.SessionID)
	delivery.events = append(delivery.events, fmt.Sprintf("close:%d", delivery.closeCalls))
	gate, started := delivery.closeGate, delivery.closeStarted
	closeErr := delivery.closeErr
	if call < len(delivery.closeErrors) {
		closeErr = delivery.closeErrors[call]
	}
	delivery.mu.Unlock()
	defer func() {
		delivery.mu.Lock()
		delivery.closeActive--
		finished := delivery.closeFinished
		delivery.mu.Unlock()
		if finished != nil {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	}()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return closeErr
}

// DeliveryHandle deliberately has no public constructor. The unsafe setup is
// confined to this black-box facade test so production APIs remain opaque.
func opaquePlaybackHandle(t *testing.T) playback.DeliveryHandle {
	return opaquePlaybackHandleNamed(t, "default")
}

func opaquePlaybackHandleNamed(t *testing.T, name string) playback.DeliveryHandle {
	t.Helper()
	handle := playback.DeliveryHandle{}
	value := reflect.ValueOf(&handle).Elem()
	for fieldName, fieldValue := range map[string]string{"sessionID": "native-session-" + name, "assetID": "native-asset-" + name, "token": "native-secret-" + name} {
		field := value.FieldByName(fieldName)
		if !field.IsValid() {
			t.Fatalf("playback handle field %q missing", fieldName)
		}
		writable := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
		writable.SetString(fieldValue)
	}
	if !handle.Valid() {
		t.Fatal("test playback handle is invalid")
	}
	return handle
}

func TestPlaybackDTOAndOpaqueRegistryFormattingDoNotLeakSecrets(t *testing.T) {
	fixture := newPlaybackFixture(t)
	encoded, err := json.Marshal(MediaSourceInfo{Id: fixture.item.ID, Path: "/Videos/item/stream?PlaySessionId=opaque"})
	if err != nil {
		t.Fatal(err)
	}
	values := []string{string(encoded), fmt.Sprintf("%+v", fixture.delivery.handle), fmt.Sprintf("%+v", playSessionBinding{ItemID: fixture.item.ID})}
	for _, value := range values {
		for _, secret := range []string{"native-secret-token", "native-asset-id", "provider-secret"} {
			if strings.Contains(value, secret) {
				t.Fatalf("formatted compatibility value disclosed %q: %s", secret, value)
			}
		}
	}
}
