package httpapi

import (
	"context"
	"errors"
	"sync"

	"github.com/moodiness/rivune/server/internal/providers"
	"github.com/moodiness/rivune/server/internal/settings"
)

type integrationRuntimeCoordinator struct {
	mu      sync.Mutex
	runtime *providers.Runtime
}

func newIntegrationRuntimeCoordinator(runtime *providers.Runtime) *integrationRuntimeCoordinator {
	return &integrationRuntimeCoordinator{runtime: runtime}
}

func (coordinator *integrationRuntimeCoordinator) PublishIntegrations(_ context.Context, credentials settings.IntegrationCredentials) error {
	if coordinator == nil || coordinator.runtime == nil {
		return errors.New("integration runtime coordinator is not configured")
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if credentials.Revision <= coordinator.runtime.Current().Revision {
		return nil
	}
	return coordinator.runtime.RebuildAndPublish(providerCredentials(credentials))
}

func providerCredentials(credentials settings.IntegrationCredentials) providers.Credentials {
	return providers.Credentials{
		TMDBAccessToken:   credentials.TMDBAccessToken,
		FanartAPIKey:      credentials.FanartAPIKey,
		MDBListAPIKey:     credentials.MDBListAPIKey,
		TVDBAPIKey:        credentials.TVDBAPIKey,
		TVDBPIN:           credentials.TVDBPIN,
		TraktClientID:     credentials.TraktClientID,
		TraktClientSecret: credentials.TraktClientSecret,
		SimklClientID:     credentials.SimklClientID,
		Revision:          credentials.Revision,
	}
}
