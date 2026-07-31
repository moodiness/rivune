package httpapi

import (
	"context"
	"log/slog"
	"time"
)

const maintenanceInterval = 5 * time.Minute

type authMaintenanceService interface {
	Cleanup(context.Context) error
}

type playbackMaintenanceService interface {
	Cleanup(context.Context) error
}

// RunMaintenance performs an immediate cleanup and then repeats until ctx is canceled.
func (a *API) RunMaintenance(ctx context.Context) {
	runMaintenance(ctx, a.logger, a.authMaintenance, a.playbackMaintenance, maintenanceInterval)
}

func runMaintenance(ctx context.Context, logger *slog.Logger, authService authMaintenanceService, playbackService playbackMaintenanceService, interval time.Duration) {
	if ctx.Err() != nil {
		return
	}
	runMaintenancePass(ctx, logger, authService, playbackService)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runMaintenancePass(ctx, logger, authService, playbackService)
		}
	}
}

func runMaintenancePass(ctx context.Context, logger *slog.Logger, authService authMaintenanceService, playbackService playbackMaintenanceService) {
	if logger == nil {
		logger = slog.Default()
	}
	if authService != nil {
		if err := authService.Cleanup(ctx); err != nil && ctx.Err() == nil {
			logger.Error("scheduled authentication cleanup failed", "error", err)
		}
	}
	if ctx.Err() != nil {
		return
	}
	if playbackService != nil {
		if err := playbackService.Cleanup(ctx); err != nil && ctx.Err() == nil {
			logger.Error("scheduled playback cleanup failed", "error", err)
		}
	}
}
