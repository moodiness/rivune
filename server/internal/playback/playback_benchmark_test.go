package playback

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	benchmarkPlaylistOutput []byte
	benchmarkDirectoryBytes int64
	benchmarkPlaybackMode   string
	benchmarkDecision       *PlaybackDecision
)

func BenchmarkRewritePlaylist(b *testing.B) {
	base, err := url.Parse("https://media.example/hls/master.m3u8")
	if err != nil {
		b.Fatal(err)
	}
	for _, references := range []int{8, 120} {
		var playlist strings.Builder
		playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
		for index := range references {
			playlist.WriteString("#EXTINF:3.000,\n")
			fmt.Fprintf(&playlist, "segment-%06d.m4s\n", index)
		}
		input := []byte(playlist.String())
		b.Run(fmt.Sprintf("references=%d", references), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			b.ResetTimer()
			for range b.N {
				output, rewriteErr := rewritePlaylist(input, base, func(string) string {
					return "/api/v1/playback/assets/segment?token=synthetic"
				})
				if rewriteErr != nil {
					b.Fatal(rewriteErr)
				}
				benchmarkPlaylistOutput = output
			}
		})
	}
}

func BenchmarkDirectorySize(b *testing.B) {
	root := b.TempDir()
	const (
		directories   = 4
		filesPerDir   = 8
		bytesPerFile  = 4096
		expectedBytes = directories * filesPerDir * bytesPerFile
	)
	contents := make([]byte, bytesPerFile)
	for directory := range directories {
		path := filepath.Join(root, fmt.Sprintf("job-%02d", directory))
		if err := os.Mkdir(path, 0o700); err != nil {
			b.Fatal(err)
		}
		for file := range filesPerDir {
			if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("segment-%02d.m4s", file)), contents, 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	if size := directorySize(root); size != expectedBytes {
		b.Fatalf("directory size = %d, want %d", size, expectedBytes)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkDirectoryBytes = directorySize(root)
	}
}

func BenchmarkPlaybackDecision(b *testing.B) {
	capabilities := Capabilities{
		StreamingProtocols:   []string{"http", "hls"},
		Containers:           []string{"mp4"},
		VideoCodecs:          []string{"h264"},
		AudioCodecs:          []string{"aac"},
		ProcessingModes:      []string{processingRemux, processingTranscodeAudio, processingTranscode},
		MaximumAudioChannels: 2,
	}
	benchmarks := []struct {
		name       string
		container  string
		videoCodec string
		audioCodec string
		channels   int
		want       string
	}{
		{name: "direct", container: "mp4", videoCodec: "h264", audioCodec: "aac", channels: 2, want: "direct"},
		{name: "remux", container: "mkv", videoCodec: "h264", audioCodec: "aac", channels: 2, want: processingRemux},
		{name: "transcode_audio", container: "mkv", videoCodec: "h264", audioCodec: "dts", channels: 6, want: processingTranscodeAudio},
		{name: "transcode", container: "mkv", videoCodec: "vp9", audioCodec: "dts", channels: 6, want: processingTranscode},
	}
	for _, benchmark := range benchmarks {
		inspection := MediaInspection{
			Container:   benchmark.container,
			VideoTracks: []MediaTrack{{Codec: benchmark.videoCodec, Height: 1080}},
			AudioTracks: []MediaTrack{{Codec: benchmark.audioCodec, Channels: benchmark.channels}},
		}
		source := Source{Mode: "direct", Protocol: "http", Container: benchmark.container}
		mode, decision := playbackMode(source, inspection, capabilities)
		if mode != benchmark.want || decision == nil {
			b.Fatalf("playback mode = %q, decision = %+v, want %q", mode, decision, benchmark.want)
		}
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkPlaybackMode, benchmarkDecision = playbackMode(source, inspection, capabilities)
			}
		})
	}
}
