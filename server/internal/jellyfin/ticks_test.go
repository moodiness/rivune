package jellyfin

import (
	"math"
	"testing"
	"time"
)

func TestTickConversionsUseJellyfinUnits(t *testing.T) {
	if got, want := SecondsToTicks(7), int64(70_000_000); got != want {
		t.Fatalf("SecondsToTicks(7) = %d, want %d", got, want)
	}
	if got, want := MinutesToTicks(3), int64(1_800_000_000); got != want {
		t.Fatalf("MinutesToTicks(3) = %d, want %d", got, want)
	}
	if got, want := DurationToTicks(1500*time.Millisecond), int64(15_000_000); got != want {
		t.Fatalf("DurationToTicks(1.5s) = %d, want %d", got, want)
	}
	if got, want := TicksToSeconds(19_999_999), int64(1); got != want {
		t.Fatalf("TicksToSeconds truncation = %d, want %d", got, want)
	}
}

func TestTickConversionsSaturateWithoutOverflow(t *testing.T) {
	if got := SecondsToTicks(math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("overflowing seconds = %d, want MaxInt64", got)
	}
	if got := MinutesToTicks(math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("overflowing minutes = %d, want MaxInt64", got)
	}
	if got := TicksToDuration(math.MaxInt64); got != time.Duration(math.MaxInt64) {
		t.Fatalf("overflowing duration = %v, want maximum duration", got)
	}
	for _, got := range []int64{SecondsToTicks(-1), MinutesToTicks(-1), TicksToSeconds(-1), int64(TicksToDuration(-1))} {
		if got != 0 {
			t.Fatalf("negative input converted to %d, want zero", got)
		}
	}
}
