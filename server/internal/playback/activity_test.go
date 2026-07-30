package playback

import (
	"encoding/json"
	"testing"
)

func TestActivityModeUsesActualProcessingContract(t *testing.T) {
	tests := []struct {
		name   string
		assets []storedAsset
		want   string
	}{
		{name: "direct", assets: []storedAsset{{Kind: "stream"}}, want: "direct"},
		{name: "remux", assets: []storedAsset{{Kind: processingRemux}}, want: processingRemux},
		{name: "audio", assets: []storedAsset{{Kind: processingTranscodeAudio}}, want: processingTranscodeAudio},
		{name: "video", assets: []storedAsset{{Kind: processingTranscode}}, want: processingTranscode},
		{name: "subtitle then video", assets: []storedAsset{{Kind: assetKindEmbeddedSubtitle}, {Kind: processingTranscode}}, want: processingTranscode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.assets)
			if err != nil {
				t.Fatal(err)
			}
			if got := activityMode(encoded); got != test.want {
				t.Fatalf("activity mode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFFmpegDiagnosticsReportSlotPressure(t *testing.T) {
	processor := &FFmpegProcessor{slots: make(chan struct{}, 2)}
	processor.slots <- struct{}{}
	if processor.ActiveProcesses() != 1 || processor.ProcessLimit() != 2 {
		t.Fatalf("unexpected processor diagnostics: active=%d limit=%d", processor.ActiveProcesses(), processor.ProcessLimit())
	}
}
