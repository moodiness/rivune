package httpapi

import (
	"context"
	"testing"

	"github.com/moodiness/rivune/server/internal/providers"
	"github.com/moodiness/rivune/server/internal/settings"
)

func TestIntegrationRuntimeCoordinatorPublishesOnlyCompleteNewerGenerations(t *testing.T) {
	initial, err := providers.Build(providers.Credentials{Revision: 5}, providers.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := providers.NewRuntime(initial, providers.BuildOptions{})
	coordinator := newIntegrationRuntimeCoordinator(runtime)
	if err := coordinator.PublishIntegrations(context.Background(), settings.IntegrationCredentials{Revision: 4}); err != nil {
		t.Fatal(err)
	}
	if runtime.Current().Revision != 5 {
		t.Fatalf("stale credentials replaced revision with %d", runtime.Current().Revision)
	}
	if err := coordinator.PublishIntegrations(context.Background(), settings.IntegrationCredentials{Revision: 6}); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Current()
	if snapshot.Revision != 6 || snapshot.Metadata.Generation != 6 || snapshot.Collection.Generation != 6 || snapshot.Watchstate.Generation != 6 || snapshot.Tracking.Generation != 6 {
		t.Fatalf("provider generations were not published atomically: %+v", snapshot)
	}
}
