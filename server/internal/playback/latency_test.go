package playback

import "testing"

func TestNormalizeMediaOptionsBoundsInitialBuffer(t *testing.T) {
	for _, test := range []struct {
		name  string
		input int
		want  int
	}{
		{name: "default", want: 12},
		{name: "minimum", input: 3, want: 3},
		{name: "maximum", input: 30, want: 30},
		{name: "below minimum", input: 2, want: 12},
		{name: "above maximum", input: 31, want: 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeMediaOptions(MediaOptions{TempDirectory: t.TempDir(), InitialBufferSeconds: test.input})
			if got.InitialBufferSeconds != test.want {
				t.Fatalf("initial HLS buffer = %d, want %d", got.InitialBufferSeconds, test.want)
			}
		})
	}
}
