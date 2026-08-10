package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const externalMediaTestEnvironment = "RIVUNE_TEST_EXTERNAL_MEDIA"

type externalMediaFixture struct {
	processor *FFmpegProcessor
	video     string
	mp4       string
	subtitle  string
}

func newExternalMediaFixture(t *testing.T) externalMediaFixture {
	t.Helper()
	if os.Getenv(externalMediaTestEnvironment) != "1" {
		t.Skip("set RIVUNE_TEST_EXTERNAL_MEDIA=1 to run FFmpeg byte-path tests")
	}
	ffmpegPath := strings.TrimSpace(os.Getenv("RIVUNE_FFMPEG_PATH"))
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	ffprobePath := strings.TrimSpace(os.Getenv("RIVUNE_FFPROBE_PATH"))
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	processor, err := NewFFmpegProcessor(ffmpegPath, ffprobePath, 1, 1, FFmpegOptions{HardwareAcceleration: "software"})
	if err != nil {
		t.Fatalf("initialize external media processor: %v", err)
	}

	directory := t.TempDir()
	video := filepath.Join(directory, "two-audio-tracks.mkv")
	runExternalMediaCommand(t, processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=4",
		"-map", "0:v:0", "-map", "1:a:0", "-map", "2:a:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10",
		"-c:a", "aac", "-b:a", "96k",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:0", "title=low-tone",
		"-metadata:s:a:1", "language=spa", "-metadata:s:a:1", "title=high-tone",
		"-shortest", video,
	)
	mp4 := filepath.Join(directory, "direct.mp4")
	runExternalMediaCommand(t, processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", video,
		"-map", "0", "-c", "copy", "-movflags", "+faststart", mp4,
	)
	subtitle := filepath.Join(directory, "unicode.srt")
	contents := "1\n00:00:00,250 --> 00:00:01,250\nPoczątek — Zażółć gęślą jaźń\n\n2\n00:00:02,500 --> 00:00:03,750\nŚrodek ✓ po seeku\n"
	if err := os.WriteFile(subtitle, []byte(contents), 0o600); err != nil {
		t.Fatalf("write external subtitle fixture: %v", err)
	}
	return externalMediaFixture{processor: processor, video: video, mp4: mp4, subtitle: subtitle}
}

func runExternalMediaCommand(t *testing.T, path string, arguments ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, arguments...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("external media command %s failed: %v: %s", filepath.Base(path), err, stderr.String())
	}
	return stdout.Bytes()
}

func externalMediaTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type externalProbeStream struct {
	Index          int    `json:"index"`
	CodecType      string `json:"codec_type"`
	CodecName      string `json:"codec_name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	Channels       int    `json:"channels"`
	PixelFormat    string `json:"pix_fmt"`
	ColorTransfer  string `json:"color_transfer"`
	ColorPrimaries string `json:"color_primaries"`
	ColorSpace     string `json:"color_space"`
}

type externalProbeResult struct {
	Streams []externalProbeStream `json:"streams"`
	Format struct {
		Name string `json:"format_name"`
	} `json:"format"`
}

func probeExternalMedia(t *testing.T, fixture externalMediaFixture, input string) externalProbeResult {
	t.Helper()
	output := runExternalMediaCommand(t, fixture.processor.ffprobePath,
		"-v", "error", "-show_entries", "stream=index,codec_type,codec_name,width,height,channels,pix_fmt,color_transfer,color_primaries,color_space:format=format_name", "-of", "json", input,
	)
	var result externalProbeResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode ffprobe output: %v: %s", err, output)
	}
	return result
}

func TestExternalMediaDirectMP4ProbeAndRangeBytePath(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	inspection, err := fixture.processor.Probe(externalMediaTestContext(t), storedAsset{URL: fixture.mp4, Container: "mp4"})
	if err != nil {
		t.Fatalf("probe direct MP4 through playback processor: %v", err)
	}
	if inspection.Container != "mp4" || len(inspection.VideoTracks) != 1 || inspection.VideoTracks[0].Codec != "h264" || len(inspection.AudioTracks) != 2 || inspection.AudioTracks[0].Index == inspection.AudioTracks[1].Index {
		t.Fatalf("unexpected direct MP4 inspection: %+v", inspection)
	}
	contents, err := os.ReadFile(fixture.mp4)
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		file, openErr := os.Open(fixture.mp4)
		if openErr != nil {
			http.Error(response, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil {
			http.Error(response, "fixture unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(response, request, "direct.mp4", info.ModTime(), file)
	}))
	defer origin.Close()
	service := &Service{client: origin.Client()}

	ranges := []struct {
		name   string
		header string
		start  int
		end    int
	}{
		{name: "beginning", header: "bytes=0-63", start: 0, end: 64},
		{name: "middle", header: fmt.Sprintf("bytes=%d-%d", len(contents)/2, len(contents)/2+63), start: len(contents) / 2, end: len(contents)/2 + 64},
		{name: "end", header: "bytes=-64", start: len(contents) - 64, end: len(contents)},
	}
	for _, test := range ranges {
		t.Run(test.name, func(t *testing.T) {
			incoming := httptest.NewRequest(http.MethodGet, "http://rivune.test/direct.mp4", nil)
			incoming.Header.Set("Range", test.header)
			upstream, fetchErr := service.fetchAsset(incoming.Context(), incoming, storedAsset{URL: origin.URL + "/direct.mp4"}, origin.URL+"/direct.mp4")
			if fetchErr != nil {
				t.Fatalf("fetch ranged playback bytes: %v", fetchErr)
			}
			defer upstream.Body.Close()
			var delivered bytes.Buffer
			if copyErr := copyPlaybackAsset(&delivered, upstream.Body); copyErr != nil {
				t.Fatalf("deliver ranged playback bytes: %v", copyErr)
			}
			if upstream.StatusCode != http.StatusPartialContent || !bytes.Equal(delivered.Bytes(), contents[test.start:test.end]) {
				t.Fatalf("range %q status=%d bytes=%d want=%d", test.header, upstream.StatusCode, delivered.Len(), test.end-test.start)
			}
		})
	}

	headProbe := httptest.NewRequest(http.MethodHead, "http://rivune.test/direct.mp4", nil)
	headProbe.Header.Set("Range", "bytes=0-63")
	headUpstream, fetchErr := service.fetchAsset(headProbe.Context(), headProbe, storedAsset{URL: origin.URL + "/direct.mp4", Container: "mp4"}, origin.URL+"/direct.mp4")
	if fetchErr != nil {
		t.Fatalf("fetch HEAD playback probe: %v", fetchErr)
	}
	defer headUpstream.Body.Close()
	if headUpstream.Request.Method != http.MethodGet || headUpstream.StatusCode != http.StatusPartialContent ||
		headUpstream.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-63/%d", len(contents)) ||
		headUpstream.Header.Get("Accept-Ranges") != "bytes" || headUpstream.Header.Get("Content-Length") != "64" {
		t.Fatalf("HEAD probe method=%s status=%d headers=%v", headUpstream.Request.Method, headUpstream.StatusCode, headUpstream.Header)
	}
}

func TestExternalMediaHLSRemuxAndVideoTranscodeProducePlayableChildren(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	for _, test := range []struct {
		name       string
		asset      storedAsset
		wantWidth  int
		wantHeight int
	}{
		{
			name:      "remux",
			asset:     storedAsset{ID: "remux", Kind: processingRemux, URL: fixture.video, Container: "mkv", HLSSegmentContainer: "ts"},
			wantWidth: 160, wantHeight: 90,
		},
		{
			name: "video transcode",
			asset: storedAsset{
				ID: "transcode", Kind: processingTranscode, URL: fixture.video, Container: "mkv", HLSSegmentContainer: "mp4",
				TargetHeight: 64, VideoBitrateKbps: 300, MaximumAudioChannels: 2,
				Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{Height: 90}, Target: &PlaybackDecisionTarget{VideoCodec: "h264", AudioCodec: "aac"}},
			},
			wantWidth: 114, wantHeight: 64,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), test.asset, directory); err != nil {
				t.Fatalf("process HLS %s: %v", test.name, err)
			}
			playlistPath := filepath.Join(directory, "index.m3u8")
			playlist, err := os.ReadFile(playlistPath)
			if err != nil || !bytes.Contains(playlist, []byte("#EXTM3U")) || !bytes.Contains(playlist, []byte("#EXTINF")) {
				t.Fatalf("read generated HLS playlist: err=%v playlist=%s", err, playlist)
			}
			probe := probeExternalMedia(t, fixture, playlistPath)
			var videoCodec, audioCodec string
			var width, height int
			for _, stream := range probe.Streams {
				switch stream.CodecType {
				case "video":
					videoCodec, width, height = stream.CodecName, stream.Width, stream.Height
				case "audio":
					audioCodec = stream.CodecName
				}
			}
			if videoCodec != "h264" || audioCodec != "aac" || width != test.wantWidth || height != test.wantHeight {
				t.Fatalf("generated HLS codecs/dimensions video=%s audio=%s size=%dx%d probe=%+v", videoCodec, audioCodec, width, height, probe)
			}
			child := firstExternalPlaylistChild(t, playlist)
			childBytes, err := os.ReadFile(filepath.Join(directory, child))
			if err != nil || len(childBytes) < 512 {
				t.Fatalf("read generated HLS child %q: bytes=%d err=%v", child, len(childBytes), err)
			}
		})
	}
}

func TestExternalMediaEmbeddedASSBurnProducesPlayableHLS(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	source := filepath.Join(t.TempDir(), "embedded-ass.mkv")
	runExternalMediaCommand(t, fixture.processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", fixture.video, "-i", filepath.Join("testdata", "sample.ass"),
		"-map", "0:v:0", "-map", "0:a:0", "-map", "1:s:0",
		"-c:v", "copy", "-c:a", "copy", "-c:s", "ass", source,
	)
	subtitleIndex := 2
	directory := t.TempDir()
	err := fixture.processor.ProcessHLS(externalMediaTestContext(t), storedAsset{
		ID: "ass-burn", Kind: processingTranscode, URL: source, Container: "mkv", HLSSegmentContainer: "ts",
		SubtitleTrackIndex: &subtitleIndex, SubtitleTrackType: subtitleBurnText, SubtitleTrackOrdinal: 0,
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "h264", Height: 90}},
	}, directory)
	if err != nil {
		t.Fatalf("burn embedded ASS: %v", err)
	}
	controlDirectory := t.TempDir()
	if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), storedAsset{
		ID: "ass-control", Kind: processingTranscode, URL: source, Container: "mkv", HLSSegmentContainer: "ts",
		Decision: &PlaybackDecision{Source: &PlaybackDecisionSource{VideoCodec: "h264", Height: 90}},
	}, controlDirectory); err != nil {
		t.Fatalf("create ASS control HLS: %v", err)
	}
	playlist := filepath.Join(directory, "index.m3u8")
	probe := probeExternalMedia(t, fixture, playlist)
	if len(probe.Streams) == 0 || probe.Streams[0].CodecType != "video" || probe.Streams[0].CodecName != "h264" {
		t.Fatalf("ASS burn output is not playable H.264 HLS: %+v", probe)
	}
	decodeFrame := func(input string) []byte {
		return runExternalMediaCommand(t, fixture.processor.ffmpegPath,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-ss", "1.5", "-i", input,
			"-map", "0:v:0", "-frames:v", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1",
		)
	}
	burnedFrame := decodeFrame(playlist)
	controlFrame := decodeFrame(filepath.Join(controlDirectory, "index.m3u8"))
	if len(burnedFrame) == 0 || bytes.Equal(burnedFrame, controlFrame) {
		t.Fatal("ASS cue did not change the decoded video frame")
	}
}

func TestExternalMediaAudioTranscodeSelectsEachOverlappingTrack(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	inspection, err := fixture.processor.Probe(externalMediaTestContext(t), storedAsset{URL: fixture.video, Container: "mkv"})
	if err != nil || len(inspection.AudioTracks) != 2 {
		t.Fatalf("probe multi-track fixture: tracks=%+v err=%v", inspection.AudioTracks, err)
	}
	frequencies := make([]float64, 0, 2)
	for _, track := range inspection.AudioTracks {
		directory := t.TempDir()
		index := track.Index
		asset := storedAsset{ID: "audio-" + strconv.Itoa(index), Kind: processingTranscodeAudio, URL: fixture.video, Container: "mkv", HLSSegmentContainer: "ts", AudioTrackIndex: &index, MaximumAudioChannels: 1}
		if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), asset, directory); err != nil {
			t.Fatalf("transcode audio track %d: %v", index, err)
		}
		playlist := filepath.Join(directory, "index.m3u8")
		probe := probeExternalMedia(t, fixture, playlist)
		var audioStreams int
		for _, stream := range probe.Streams {
			if stream.CodecType == "audio" && stream.CodecName == "aac" {
				audioStreams++
			}
		}
		if audioStreams != 1 {
			t.Fatalf("selected track %d produced %d AAC streams: %+v", index, audioStreams, probe)
		}
		pcm := runExternalMediaCommand(t, fixture.processor.ffmpegPath,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-i", playlist,
			"-map", "0:a:0", "-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1",
		)
		frequencies = append(frequencies, decodedToneFrequency(pcm, 8000))
	}
	if len(frequencies) != 2 || frequencies[0] < 400 || frequencies[0] > 480 || frequencies[1] < 820 || frequencies[1] > 940 {
		t.Fatalf("selected audio payload frequencies=%v, want approximately [440 880]", frequencies)
	}
}

func TestExternalMediaMultichannelAACDownmixesToStereo(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	source := filepath.Join(t.TempDir(), "multichannel.mkv")
	runExternalMediaCommand(t, fixture.processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=3",
		"-f", "lavfi", "-i", "aevalsrc=0.1*sin(2*PI*220*t)|0.1*sin(2*PI*330*t)|0.1*sin(2*PI*440*t)|0.1*sin(2*PI*550*t)|0.1*sin(2*PI*660*t)|0.1*sin(2*PI*770*t):s=48000:d=3:c=5.1",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10",
		"-c:a", "aac", "-b:a", "384k", "-shortest", source,
	)
	directory := t.TempDir()
	if err := fixture.processor.ProcessHLS(externalMediaTestContext(t), storedAsset{
		ID: "downmix", Kind: processingTranscodeAudio, URL: source, Container: "mkv",
		HLSSegmentContainer: "ts", MaximumAudioChannels: 2,
	}, directory); err != nil {
		t.Fatalf("downmix multichannel AAC: %v", err)
	}
	playlist := filepath.Join(directory, "index.m3u8")
	probe := probeExternalMedia(t, fixture, playlist)
	var audio *externalProbeStream
	for index := range probe.Streams {
		if probe.Streams[index].CodecType == "audio" {
			audio = &probe.Streams[index]
			break
		}
	}
	if audio == nil || audio.CodecName != "aac" || audio.Channels != 2 {
		t.Fatalf("downmix output audio = %+v, streams=%+v", audio, probe.Streams)
	}
	pcm := runExternalMediaCommand(t, fixture.processor.ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-i", playlist,
		"-map", "0:a:0", "-ac", "2", "-ar", "8000", "-f", "s16le", "pipe:1",
	)
	if len(pcm) < 2*2*8000 {
		t.Fatalf("downmixed stereo payload is too short: %d bytes", len(pcm))
	}
}

func decodedToneFrequency(pcm []byte, sampleRate int) float64 {
	if len(pcm) < sampleRate*2 {
		return 0
	}
	samples := len(pcm) / 2
	start := sampleRate / 4
	end := samples
	if end > start+sampleRate*2 {
		end = start + sampleRate*2
	}
	crossings := 0
	previous := int16(binary.LittleEndian.Uint16(pcm[start*2:]))
	for index := start + 1; index < end; index++ {
		current := int16(binary.LittleEndian.Uint16(pcm[index*2:]))
		if previous <= 0 && current > 0 {
			crossings++
		}
		previous = current
	}
	return float64(crossings) * float64(sampleRate) / float64(end-start)
}

func TestExternalMediaUTF8SubtitleConversionHonorsSeek(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	service := &Service{processor: fixture.processor}
	completeResponse := httptest.NewRecorder()
	completeRequest := httptest.NewRequest(http.MethodGet, "/subtitle.vtt", nil).WithContext(externalMediaTestContext(t))
	if err := service.proxyConvertedSubtitle(completeResponse, completeRequest, storedAsset{URL: fixture.subtitle}); err != nil {
		t.Fatalf("deliver complete external subtitle: %v", err)
	}
	complete := strings.ReplaceAll(completeResponse.Body.String(), "\r\n", "\n")
	firstCue := "00:00.250 --> 00:01.250\nPoczątek — Zażółć gęślą jaźń"
	middleCue := "00:02.500 --> 00:03.750\nŚrodek ✓ po seeku"
	if completeResponse.Code != http.StatusOK ||
		completeResponse.Header().Get("Cache-Control") != "no-store" ||
		completeResponse.Header().Get("Content-Type") != "text/vtt; charset=utf-8" ||
		completeResponse.Header().Get("X-Content-Type-Options") != "nosniff" ||
		completeResponse.Header().Get("Content-Length") != strconv.Itoa(completeResponse.Body.Len()) ||
		!utf8.ValidString(complete) || !strings.HasPrefix(complete, "WEBVTT\n\n") ||
		!strings.Contains(complete, firstCue) || !strings.Contains(complete, middleCue) {
		t.Fatalf("delivered subtitle has invalid headers or decoded cues: status=%d headers=%v payload=%q", completeResponse.Code, completeResponse.Header(), complete)
	}

	soughtResponse := httptest.NewRecorder()
	soughtRequest := httptest.NewRequest(http.MethodGet, "/subtitle.vtt", nil).WithContext(externalMediaTestContext(t))
	if err := service.proxyConvertedSubtitle(soughtResponse, soughtRequest, storedAsset{URL: fixture.subtitle, StartSeconds: 2}); err != nil {
		t.Fatalf("deliver sought external subtitle: %v", err)
	}
	sought := strings.ReplaceAll(soughtResponse.Body.String(), "\r\n", "\n")
	rebasedMiddleCue := "00:00.500 --> 00:01.750\nŚrodek ✓ po seeku"
	if soughtResponse.Code != http.StatusOK ||
		soughtResponse.Header().Get("Cache-Control") != "no-store" ||
		soughtResponse.Header().Get("Content-Type") != "text/vtt; charset=utf-8" ||
		soughtResponse.Header().Get("X-Content-Type-Options") != "nosniff" ||
		soughtResponse.Header().Get("Content-Length") != strconv.Itoa(soughtResponse.Body.Len()) ||
		!utf8.ValidString(sought) || !strings.HasPrefix(sought, "WEBVTT\n\n") ||
		strings.Contains(sought, "Początek") || strings.Contains(sought, middleCue) ||
		!strings.Contains(sought, rebasedMiddleCue) {
		t.Fatalf("subtitle seek returned invalid headers or decoded cues: status=%d headers=%v payload=%q", soughtResponse.Code, soughtResponse.Header(), sought)
	}
}

func TestExternalMediaHLSDeliveryRejectsOutOfOrderChildThenServesBytes(t *testing.T) {
	fixture := newExternalMediaFixture(t)
	service, err := NewService(nil, nil, fixture.processor, MediaOptions{TempDirectory: t.TempDir(), MaxStorageBytes: 64 << 20, IdleTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	asset := storedAsset{ID: "delivery-remux", Kind: processingRemux, URL: fixture.video, Container: "mkv", HLSSegmentContainer: "ts", DurationSeconds: 4}
	const sessionID = "external-media-delivery"
	defer service.stopHLSSession(sessionID)

	outOfOrder := httptest.NewRequest(http.MethodGet, "/compat/segment?file=segment-000000.ts", nil)
	outOfOrderResponse := httptest.NewRecorder()
	if err := service.serveHLS(outOfOrderResponse, outOfOrder, sessionID, "token", asset, nil); err != ErrSessionNotFound {
		t.Fatalf("out-of-order HLS child error=%v, want ErrSessionNotFound", err)
	}

	masterRequest := httptest.NewRequest(http.MethodGet, "/compat/master.m3u8?file=master.m3u8", nil)
	masterResponse := httptest.NewRecorder()
	if err := service.serveHLS(masterResponse, masterRequest, sessionID, "token", asset, nil); err != nil {
		t.Fatalf("serve generated HLS master: %v", err)
	}
	if masterResponse.Code != http.StatusOK || !strings.Contains(masterResponse.Body.String(), "index.m3u8") {
		t.Fatalf("master status=%d payload=%q", masterResponse.Code, masterResponse.Body.String())
	}
	indexRequest := httptest.NewRequest(http.MethodGet, "/compat/index.m3u8?file=index.m3u8", nil)
	indexResponse := httptest.NewRecorder()
	if err := service.serveHLS(indexResponse, indexRequest, sessionID, "token", asset, nil); err != nil {
		t.Fatalf("serve generated HLS index: %v", err)
	}
	childURL := firstExternalPlaylistChild(t, indexResponse.Body.Bytes())
	parsedChild := httptest.NewRequest(http.MethodGet, childURL, nil)
	childName := parsedChild.URL.Query().Get("file")
	if childName == "" {
		t.Fatalf("rewritten HLS child has no file capability: %q", childURL)
	}
	childRequest := httptest.NewRequest(http.MethodGet, "/compat/child?file="+childName, nil)
	childResponse := httptest.NewRecorder()
	if err := service.serveHLS(childResponse, childRequest, sessionID, "token", asset, nil); err != nil {
		t.Fatalf("serve generated HLS child: %v", err)
	}
	if childResponse.Code != http.StatusOK || childResponse.Body.Len() < 512 || childResponse.Header().Get("Content-Type") != "video/mp2t" {
		t.Fatalf("child status=%d type=%q bytes=%d", childResponse.Code, childResponse.Header().Get("Content-Type"), childResponse.Body.Len())
	}
}

func firstExternalPlaylistChild(t *testing.T, playlist []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(playlist), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	t.Fatalf("playlist has no child reference: %s", playlist)
	return ""
}
