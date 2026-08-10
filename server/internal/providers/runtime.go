package providers

import (
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/collection"
	collectionmdblist "github.com/moodiness/rivune/server/internal/collection/mdblist"
	collectiontrakt "github.com/moodiness/rivune/server/internal/collection/trakt"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/metadata/fanart"
	"github.com/moodiness/rivune/server/internal/metadata/tmdb"
	"github.com/moodiness/rivune/server/internal/metadata/tvdb"
	"github.com/moodiness/rivune/server/internal/tracking"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

// Credentials is the plaintext, revisioned credential handoff used only while
// composing a new immutable provider generation.
type Credentials struct {
	TMDBAccessToken   string
	FanartAPIKey      string
	MDBListAPIKey     string
	TVDBAPIKey        string
	TVDBPIN           string
	TraktClientID     string
	TraktClientSecret string
	SimklClientID     string
	Revision          int64
}

type BuildOptions struct {
	Pool             *pgxpool.Pool
	MetadataCacheTTL time.Duration
	Logger           *slog.Logger
	HTTPClient       *http.Client
}

// Snapshot contains every credential-backed provider facet from one database
// revision. It contains clients, never plaintext credentials.
type Snapshot struct {
	Revision   int64
	Metadata   metadata.ProviderSet
	Collection collection.ProviderSet
	Watchstate watchstate.ProviderSet
	Tracking   tracking.ProviderSet
}

func Build(credentials Credentials, options BuildOptions) (Snapshot, error) {
	if credentials.Revision < 0 {
		return Snapshot{}, errors.New("provider credential revision must not be negative")
	}
	if credentials.TVDBPIN != "" && credentials.TVDBAPIKey == "" {
		return Snapshot{}, errors.New("TVDB PIN requires an API key")
	}
	if credentials.TraktClientSecret != "" && credentials.TraktClientID == "" {
		return Snapshot{}, errors.New("Trakt client secret requires a client ID")
	}

	var primary metadata.Provider
	var collectionTMDB collection.TMDBProvider
	var artworkMetadata collection.ArtworkMetadataProvider
	var resolver metadata.ExternalIDResolver
	if credentials.TMDBAccessToken != "" {
		client := tmdb.New(credentials.TMDBAccessToken, options.HTTPClient)
		primary = client
		collectionTMDB = client
		artworkMetadata = client
		resolver = client
	}
	var television metadata.TelevisionEnricher
	if credentials.TVDBAPIKey != "" {
		// Every build owns a fresh client and therefore a fresh TVDB token cache.
		television = tvdb.New(credentials.TVDBAPIKey, credentials.TVDBPIN, options.HTTPClient)
	}
	var artwork metadata.ArtworkEnricher
	var fanartEnricher collection.FanartEnricher
	if credentials.FanartAPIKey != "" {
		client := fanart.NewCached(credentials.FanartAPIKey, options.HTTPClient, options.Pool, options.MetadataCacheTTL, options.Logger)
		artwork = client
		fanartEnricher = client
	}
	var traktCollection collection.TraktProvider
	if credentials.TraktClientID != "" {
		traktCollection = collectiontrakt.New(credentials.TraktClientID, options.HTTPClient)
	}
	var mdblistCollection collection.MDBListProvider
	if credentials.MDBListAPIKey != "" {
		mdblistCollection = collectionmdblist.New(credentials.MDBListAPIKey, options.HTTPClient)
	}
	trackingRuntime := tracking.NewRuntime(credentials.TraktClientID, credentials.TraktClientSecret, credentials.SimklClientID, options.HTTPClient)

	return Snapshot{
		Revision:   credentials.Revision,
		Metadata:   metadata.NewProviderSet(credentials.Revision, primary, television, artwork),
		Collection: collection.NewProviderSet(credentials.Revision, collectionTMDB, traktCollection, mdblistCollection, artworkMetadata, resolver, fanartEnricher),
		Watchstate: watchstate.NewProviderSet(credentials.Revision, primary, resolver),
		Tracking:   tracking.NewProviderSet(credentials.Revision, trackingRuntime),
	}, nil
}

// Runtime is the single atomic publication point shared by all provider
// consumers. Each consumer operation loads exactly one subsystem view from the
// same complete Snapshot generation.
type Runtime struct {
	current atomic.Pointer[Snapshot]
	options BuildOptions
}

var (
	_ metadata.ProviderSource   = (*Runtime)(nil)
	_ collection.ProviderSource = (*Runtime)(nil)
	_ watchstate.ProviderSource = (*Runtime)(nil)
	_ tracking.ProviderSource   = (*Runtime)(nil)
)

func NewRuntime(initial Snapshot, options BuildOptions) *Runtime {
	runtime := &Runtime{options: options}
	runtime.Publish(initial)
	return runtime
}

func (runtime *Runtime) Current() Snapshot {
	if runtime == nil {
		return Snapshot{}
	}
	current := runtime.current.Load()
	if current == nil {
		return Snapshot{}
	}
	return *current
}

func (runtime *Runtime) Publish(snapshot Snapshot) {
	if snapshot.Metadata.Generation != snapshot.Revision ||
		snapshot.Collection.Generation != snapshot.Revision ||
		snapshot.Watchstate.Generation != snapshot.Revision ||
		snapshot.Tracking.Generation != snapshot.Revision {
		return
	}
	if runtime == nil {
		return
	}
	complete := snapshot
	for {
		current := runtime.current.Load()
		if current != nil && current.Revision > complete.Revision {
			return
		}
		if runtime.current.CompareAndSwap(current, &complete) {
			return
		}
	}
}

func (runtime *Runtime) RebuildAndPublish(credentials Credentials) error {
	if runtime == nil {
		return errors.New("provider runtime is nil")
	}
	snapshot, err := Build(credentials, runtime.options)
	if err != nil {
		return err
	}
	runtime.Publish(snapshot)
	return nil
}

func (runtime *Runtime) MetadataProviders() metadata.ProviderSet {
	return runtime.Current().Metadata
}

func (runtime *Runtime) CollectionProviders() collection.ProviderSet {
	return runtime.Current().Collection
}

func (runtime *Runtime) WatchstateProviders() watchstate.ProviderSet {
	return runtime.Current().Watchstate
}

func (runtime *Runtime) TrackingProviders() tracking.ProviderSet {
	return runtime.Current().Tracking
}
