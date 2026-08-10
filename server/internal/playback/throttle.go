package playback

import "math"

const defaultTranscodeMaximumReadRate = 1.5

// adaptiveTranscodeReadRate bounds FFmpeg input burst speed using only the
// local process-pool pressure. It never slows below real time: doing so would
// make an otherwise healthy live client consume output faster than it is
// produced. Seekable sliding generations stay at real time so they cannot
// evict the next sequential segment before the client requests it.
func adaptiveTranscodeReadRate(maximum float64, active, limit int, seekable bool) float64 {
	if seekable {
		return 1
	}
	if math.IsNaN(maximum) || math.IsInf(maximum, 0) || maximum < 1 || maximum > 4 {
		maximum = defaultTranscodeMaximumReadRate
	}
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
