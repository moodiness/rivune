package playback

import "sync/atomic"

type MediaProcessTotals struct {
	Started           uint64 `json:"started"`
	Succeeded         uint64 `json:"succeeded"`
	Failed            uint64 `json:"failed"`
	SoftwareFallbacks uint64 `json:"softwareFallbacks"`
}

type ffmpegProcessMetrics struct {
	started           atomic.Uint64
	succeeded         atomic.Uint64
	failed            atomic.Uint64
	softwareFallbacks atomic.Uint64
}

func (metrics *ffmpegProcessMetrics) snapshot() MediaProcessTotals {
	if metrics == nil {
		return MediaProcessTotals{}
	}
	return MediaProcessTotals{
		Started:           metrics.started.Load(),
		Succeeded:         metrics.succeeded.Load(),
		Failed:            metrics.failed.Load(),
		SoftwareFallbacks: metrics.softwareFallbacks.Load(),
	}
}
