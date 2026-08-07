package jellyfin

import (
	"math"
	"time"
)

const (
	TicksPerSecond = int64(10_000_000)
	TicksPerMinute = 60 * TicksPerSecond
)

// SecondsToTicks converts a non-negative whole-second value to Jellyfin ticks.
// Negative inputs become zero and values too large to represent saturate.
func SecondsToTicks(seconds int64) int64 {
	return multiplyTicks(seconds, TicksPerSecond)
}

// MinutesToTicks converts non-negative metadata minutes to Jellyfin ticks.
func MinutesToTicks(minutes int64) int64 {
	return multiplyTicks(minutes, TicksPerMinute)
}

// DurationToTicks converts a duration without an intermediate multiplication.
func DurationToTicks(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64(duration / (100 * time.Nanosecond))
}

// TicksToSeconds converts ticks to whole seconds. Negative values become zero.
func TicksToSeconds(ticks int64) int64 {
	if ticks <= 0 {
		return 0
	}
	return ticks / TicksPerSecond
}

// TicksToDuration converts ticks to a duration and saturates at the maximum
// representable time.Duration instead of overflowing the multiplication.
func TicksToDuration(ticks int64) time.Duration {
	if ticks <= 0 {
		return 0
	}
	const maximumDurationTicks = int64(math.MaxInt64 / int64(100*time.Nanosecond))
	if ticks > maximumDurationTicks {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ticks) * 100 * time.Nanosecond
}

func multiplyTicks(value, scale int64) int64 {
	if value <= 0 {
		return 0
	}
	if value > math.MaxInt64/scale {
		return math.MaxInt64
	}
	return value * scale
}
