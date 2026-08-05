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

type operationsMaintenanceService interface {
	RunScheduled(context.Context) error
}

// RunMaintenance performs an immediate cleanup and then repeats until ctx is canceled.
func (a *API) RunMaintenance(ctx context.Context) {
	var calendarDone <-chan struct{}
	if a.calendarRefresh != nil {
		done := make(chan struct{})
		calendarDone = done
		go func() {
			defer close(done)
			a.calendarRefresh.Run(ctx)
		}()
	}
	runMaintenance(ctx, a.logger, a.authMaintenance, a.playbackMaintenance, maintenanceInterval, a.operations)
	if calendarDone != nil {
		<-calendarDone
	}
}

func runMaintenance(ctx context.Context, logger *slog.Logger, authService authMaintenanceService, playbackService playbackMaintenanceService, interval time.Duration, operationsServices ...operationsMaintenanceService) {
	if ctx.Err() != nil {
		return
	}
	runMaintenancePass(ctx, logger, authService, playbackService, operationsServices...)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runMaintenancePass(ctx, logger, authService, playbackService, operationsServices...)
		}
	}
}

func runMaintenancePass(ctx context.Context, logger *slog.Logger, authService authMaintenanceService, playbackService playbackMaintenanceService, operationsServices ...operationsMaintenanceService) {
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
	if ctx.Err() != nil {
		return
	}
	for _, operationsService := range operationsServices {
		if operationsService == nil {
			continue
		}
		if err := operationsService.RunScheduled(ctx); err != nil && ctx.Err() == nil {
			logger.Error("scheduled metadata refresh failed", "error", err)
		}
	}
}
