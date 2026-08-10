package playback

import "testing"

func TestAdaptiveTranscodeReadRateUsesBoundedPoolPressure(t *testing.T) {
	tests := []struct {
		name          string
		maximum       float64
		active, limit int
		seekable      bool
		want          float64
	}{
		{name: "idle pool bursts", maximum: 2, active: 1, limit: 4, want: 2},
		{name: "quarter pressure is moderate", maximum: 2, active: 2, limit: 4, want: 1.5},
		{name: "half pressure slows", maximum: 2, active: 2, limit: 3, want: 1.25},
		{name: "saturated pool is real time", maximum: 2, active: 4, limit: 4, want: 1},
		{name: "configured ceiling wins", maximum: 1.1, active: 2, limit: 3, want: 1.1},
		{name: "single worker keeps configured rate", maximum: 3, active: 1, limit: 1, want: 3},
		{name: "seekable generation stays real time", maximum: 4, active: 1, limit: 4, seekable: true, want: 1},
		{name: "invalid ceiling fails to safe default", maximum: 0, active: 1, limit: 4, want: defaultTranscodeMaximumReadRate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adaptiveTranscodeReadRate(test.maximum, test.active, test.limit, test.seekable); got != test.want {
				t.Fatalf("adaptive read rate = %v, want %v", got, test.want)
			}
		})
	}
}
