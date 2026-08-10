package playback

import "math"

const defaultTranscodeMaximumReadRate = 1.5

func normalizedTranscodeMaximumReadRate(maximum float64) float64 {
	if math.IsNaN(maximum) || math.IsInf(maximum, 0) || maximum < 1 || maximum > 4 {
		return defaultTranscodeMaximumReadRate
	}
	return maximum
}

// adaptiveTranscodeReadRate bounds FFmpeg input speed using only the local
// process-pool pressure. It never slows below real time: doing so would make
// an otherwise healthy live client consume output faster than it is produced.
func adaptiveTranscodeReadRate(maximum float64, active, limit int) float64 {
	maximum = normalizedTranscodeMaximumReadRate(maximum)
	if limit <= 1 || active <= 1 {
		return maximum
	}

	otherWorkers := active - 1
	availablePeers := limit - 1
	if otherWorkers > availablePeers {
		otherWorkers = availablePeers
	}
	pressure := float64(otherWorkers) / float64(availablePeers)
	switch {
	case pressure >= 0.75:
		return 1
	case pressure >= 0.5:
		return math.Min(maximum, 1.25)
	case pressure >= 0.25:
		return math.Min(maximum, 1.5)
	default:
		return maximum
	}
}

// seekableTranscodeReadRate preserves a bounded surplus without letting a
// complete generation evict the segment a continuously playing client needs.
func seekableTranscodeReadRate(maximum, remainingSeconds float64) float64 {
	maximum = normalizedTranscodeMaximumReadRate(maximum)
	if maximum <= 1 || remainingSeconds <= 0 || math.IsNaN(remainingSeconds) || math.IsInf(remainingSeconds, 0) {
		return 1
	}
	if hlsProductionLeadSegments <= 0 {
		return 1
	}
	windowBudgetSeconds := float64(hlsProductionLeadSegments * hlsSegmentDurationSeconds)
	return math.Min(maximum, 1+windowBudgetSeconds/remainingSeconds)
}
