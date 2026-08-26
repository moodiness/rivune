package playback

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlaybackDecisionPublicJSONRequiresReasonsAndHidesPipeline(t *testing.T) {
	decision := PlaybackDecision{
		Reason: decisionDirectSupported, Reasons: []string{}, VideoAction: "copy", AudioAction: "copy", SubtitleAction: "none",
		Pipeline: &PlaybackPipeline{HardwareAcceleration: "nvenc", Encoder: "h264_nvenc", ZeroCopy: true},
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if !strings.Contains(body, `"reasons":[]`) {
		t.Fatalf("decision omitted required empty reasons: %s", body)
	}
	for _, private := range []string{"pipeline", "nvenc", "h264_nvenc"} {
		if strings.Contains(body, private) {
			t.Fatalf("decision exposed private pipeline detail %q: %s", private, body)
		}
	}
}
