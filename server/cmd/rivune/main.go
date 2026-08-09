package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moodiness/rivune/server/internal/config"
	"github.com/moodiness/rivune/server/internal/database"
	"github.com/moodiness/rivune/server/internal/httpapi"
	"github.com/moodiness/rivune/server/internal/netguard"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := netguard.ConfigureNAT64Prefixes(cfg.NAT64Prefixes); err != nil {
		return fmt.Errorf("configure NAT64 egress policy: %w", err)
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()

	pool, err := database.Open(startupContext, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(startupContext, pool); err != nil {
		return err
	}

	api, err := httpapi.New(startupContext, cfg, pool, logger, version)
	if err != nil {
		return fmt.Errorf("initialize HTTP API: %w", err)
	}
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	maintenanceContext, cancelMaintenance := context.WithCancel(shutdownContext)
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		api.RunMaintenance(maintenanceContext)
	}()
	defer func() {
		cancelMaintenance()
		<-maintenanceDone
	}()

	trackingContext, cancelTracking := context.WithCancel(shutdownContext)
	trackingDone := make(chan struct{})
	go func() {
		defer close(trackingDone)
		api.RunTracking(trackingContext)
	}()
	defer func() {
		cancelTracking()
		<-trackingDone
	}()
	compatContext, cancelCompat := newJellyfinCompatibilityContext()
	compatDone := make(chan struct{})
	go func() {
		defer close(compatDone)
		api.RunJellyfinCompatibility(compatContext)
	}()
	defer func() {
		cancelCompat()
		<-compatDone
	}()

	serverError := make(chan error, 1)
	go func() {
		logger.Info("Rivune server listening", "address", cfg.ListenAddress, "version", version)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-shutdownContext.Done():
		logger.Info("shutting down Rivune server")
		gracefulContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return shutdownHTTPBeforeJellyfinCompatibility(
			func() error { return server.Shutdown(gracefulContext) }, cancelCompat, compatDone,
		)
	}
}

func newJellyfinCompatibilityContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func shutdownHTTPBeforeJellyfinCompatibility(shutdown func() error, cancelCompatibility context.CancelFunc, compatibilityDone <-chan struct{}) error {
	err := shutdown()
	cancelCompatibility()
	<-compatibilityDone
	return err
}
