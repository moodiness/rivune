package playback

import (
	"math"
	"testing"
)

func TestAdaptiveTranscodeReadRateUsesBoundedPoolPressure(t *testing.T) {
	tests := []struct {
		name          string
		maximum       float64
		active, limit int
		want          float64
	}{
		{name: "idle pool bursts", maximum: 2, active: 1, limit: 4, want: 2},
		{name: "quarter pressure is moderate", maximum: 2, active: 2, limit: 4, want: 1.5},
		{name: "half pressure slows", maximum: 2, active: 2, limit: 3, want: 1.25},
		{name: "saturated pool is real time", maximum: 2, active: 4, limit: 4, want: 1},
		{name: "configured ceiling wins", maximum: 1.1, active: 2, limit: 3, want: 1.1},
		{name: "single worker keeps configured rate", maximum: 3, active: 1, limit: 1, want: 3},
		{name: "invalid ceiling fails to safe default", maximum: 0, active: 1, limit: 4, want: defaultTranscodeMaximumReadRate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adaptiveTranscodeReadRate(test.maximum, test.active, test.limit); got != test.want {
				t.Fatalf("adaptive read rate = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSeekableTranscodeReadRateUsesRetainedWindowBudget(t *testing.T) {
	remainingSeconds := float64(2 * 60 * 60)
	got := seekableTranscodeReadRate(defaultTranscodeMaximumReadRate, remainingSeconds)
	windowBudgetSeconds := float64(hlsProductionLeadSegments * hlsSegmentDurationSeconds)
	leadAtCompletion := remainingSeconds - remainingSeconds/got
	if got <= 1 || got >= defaultTranscodeMaximumReadRate || leadAtCompletion > windowBudgetSeconds {
		t.Fatalf("seekable read rate = %v, completion lead = %v, window budget = %v", got, leadAtCompletion, windowBudgetSeconds)
	}
}

func TestHLSProductionAndSharedJoinBudgetsFitRetainedWindow(t *testing.T) {
	if hlsProductionLeadSegments <= 0 || hlsSharedJoinSegments <= 0 ||
		hlsProductionLeadSegments+hlsSharedJoinSegments != hlsUsableWindowSegments ||
		hlsUsableWindowSegments+hlsDeleteThreshold+hlsSharedWorkerSafetySegments != hlsRetainedSegments {
		t.Fatalf("invalid HLS segment budgets: production=%d join=%d usable=%d retained=%d", hlsProductionLeadSegments, hlsSharedJoinSegments, hlsUsableWindowSegments, hlsRetainedSegments)
	}
}

func TestSeekableTranscodeReadRateBoundsInvalidAndShortSources(t *testing.T) {
	tests := []struct {
		name               string
		maximum, remaining float64
		want               float64
	}{
		{name: "short source reaches ceiling", maximum: 1.25, remaining: 60, want: 1.25},
		{name: "real-time ceiling stays real time", maximum: 1, remaining: 7200, want: 1},
		{name: "invalid ceiling uses default", maximum: 0, remaining: 60, want: defaultTranscodeMaximumReadRate},
		{name: "zero remaining stays real time", maximum: 1.5, remaining: 0, want: 1},
		{name: "non-finite remaining stays real time", maximum: 1.5, remaining: math.Inf(1), want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := seekableTranscodeReadRate(test.maximum, test.remaining); got != test.want {
				t.Fatalf("seekable read rate = %v, want %v", got, test.want)
			}
		})
	}
}
