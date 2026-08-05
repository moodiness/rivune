package playback

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/auth"
)

type cardinalityResourceFetcher struct {
	payload []byte
}

func (fetcher *cardinalityResourceFetcher) FetchPlaybackResource(_ context.Context, _ auth.Principal, addonID string, _ addon.ResourcePath) (addon.ResourceResult, error) {
	return addon.ResourceResult{AddonID: addonID, ManifestID: "org.example.cardinality", Payload: fetcher.payload}, nil
}

func (fetcher *cardinalityResourceFetcher) FetchAllPlaybackResources(_ context.Context, _ auth.Principal, _ addon.ResourcePath) (addon.ResourceBatch, error) {
	return addon.ResourceBatch{Results: []addon.ResourceResult{{AddonID: "cardinality-addon", ManifestID: "org.example.cardinality", Payload: fetcher.payload}}}, nil
}

func playbackStreamResponsePayload(count int) []byte {
	var payload bytes.Buffer
	payload.WriteString(`{"streams":[`)
	for index := range count {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"name":"Source `)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`","url":"https://media.example/`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`.mp4"}`)
	}
	payload.WriteString(`]}`)
	return payload.Bytes()
}

func playbackSubtitleResponsePayload(count int) []byte {
	var payload bytes.Buffer
	payload.WriteString(`{"subtitles":[`)
	for index := range count {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"id":"subtitle-`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`","url":"https://media.example/`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`.vtt","lang":"en"}`)
	}
	payload.WriteString(`]}`)
	return payload.Bytes()
}

func TestSourcesRejectsOverLimitProviderBeforeReferenceWork(t *testing.T) {
	for _, test := range []struct {
		name                string
		count               int
		wantError           bool
		wantSources         int
		wantReferences      int
		wantProfileTxBegins int
	}{
		{name: "at limit", count: addon.MaximumProviderStreams, wantSources: addon.MaximumProviderStreams, wantReferences: addon.MaximumProviderStreams, wantProfileTxBegins: 2},
		{name: "over limit", count: addon.MaximumProviderStreams + 1, wantError: true, wantProfileTxBegins: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := time.Now().UTC()
			profileID := "profile-id"
			grantExpiresAt := current.Add(time.Hour)
			profileTxBegins := 0
			store := newSourceReferenceStore(func() time.Time { return current })
			service := &Service{
				addons:     &cardinalityResourceFetcher{payload: playbackStreamResponsePayload(test.count)},
				now:        func() time.Time { return current },
				references: store,
				profileTxFactory: func(context.Context, auth.Principal) (playbackProfileTransaction, error) {
					profileTxBegins++
					return testPlaybackProfileTransaction{}, nil
				},
			}
			list, err := service.Sources(context.Background(), auth.Principal{
				Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
				SessionID: "session-id", ActiveProfileID: &profileID, ProfileGrantExpiresAt: &grantExpiresAt,
			}, SourcesInput{
				MediaType: "movie", AddonID: "cardinality-addon", ResourceID: "tt-cardinality",
				Capabilities: Capabilities{StreamingProtocols: []string{"http"}, Containers: []string{"mp4"}},
			})
			if test.wantError {
				if !errors.Is(err, ErrProviderUnavailable) {
					t.Fatalf("over-limit sources error = %v", err)
				}
			} else if err != nil {
				t.Fatalf("sources at provider limit: %v", err)
			}
			if len(list.Sources) != test.wantSources {
				t.Fatalf("returned sources = %d, want %d", len(list.Sources), test.wantSources)
			}
			if len(store.entries) != test.wantReferences {
				t.Fatalf("stored references = %d, want %d", len(store.entries), test.wantReferences)
			}
			if profileTxBegins != test.wantProfileTxBegins {
				t.Fatalf("profile transactions begun = %d, want %d", profileTxBegins, test.wantProfileTxBegins)
			}
		})
	}
}

func TestNormalizeStreamsRejectsAggregateLimitWithoutPartialResults(t *testing.T) {
	batchAtLimit := addon.ResourceBatch{Results: []addon.ResourceResult{
		{AddonID: "first", ManifestID: "first", Payload: playbackStreamResponsePayload(addon.MaximumProviderStreams)},
		{AddonID: "second", ManifestID: "second", Payload: playbackStreamResponsePayload(addon.MaximumProviderStreams)},
	}}
	sources, assets, err := normalizeStreams(batchAtLimit, Capabilities{})
	if err != nil {
		t.Fatalf("normalize aggregate at limit: %v", err)
	}
	if len(sources) != maximumAggregateProviderStreams || len(assets) != maximumAggregateProviderStreams {
		t.Fatalf("aggregate at limit produced sources=%d assets=%d", len(sources), len(assets))
	}

	overLimit := batchAtLimit
	overLimit.Results = append(overLimit.Results, addon.ResourceResult{AddonID: "third", ManifestID: "third", Payload: playbackStreamResponsePayload(1)})
	sources, assets, err = normalizeStreams(overLimit, Capabilities{})
	if !errors.Is(err, addon.ErrInvalidResponse) {
		t.Fatalf("aggregate over-limit error = %v", err)
	}
	if sources != nil || assets != nil {
		t.Fatalf("aggregate rejection returned partial sources=%d assets=%d", len(sources), len(assets))
	}
}

func TestNormalizeSubtitlesRejectsAggregateLimitWithoutPartialResults(t *testing.T) {
	batchAtLimit := addon.ResourceBatch{Results: []addon.ResourceResult{
		{AddonID: "first", ManifestID: "first", Payload: playbackSubtitleResponsePayload(addon.MaximumProviderSubtitles)},
		{AddonID: "second", ManifestID: "second", Payload: playbackSubtitleResponsePayload(addon.MaximumProviderSubtitles)},
	}}
	subtitles, assets, err := normalizeSubtitles(batchAtLimit)
	if err != nil {
		t.Fatalf("normalize subtitle aggregate at limit: %v", err)
	}
	if len(subtitles) != maximumAggregateProviderSubtitles || len(assets) != maximumAggregateProviderSubtitles {
		t.Fatalf("subtitle aggregate at limit produced subtitles=%d assets=%d", len(subtitles), len(assets))
	}

	overLimit := batchAtLimit
	overLimit.Results = append(overLimit.Results, addon.ResourceResult{AddonID: "third", ManifestID: "third", Payload: playbackSubtitleResponsePayload(1)})
	subtitles, assets, err = normalizeSubtitles(overLimit)
	if !errors.Is(err, addon.ErrInvalidResponse) {
		t.Fatalf("subtitle aggregate over-limit error = %v", err)
	}
	if subtitles != nil || assets != nil {
		t.Fatalf("subtitle aggregate rejection returned partial subtitles=%d assets=%d", len(subtitles), len(assets))
	}
}

func TestSourceReferenceStoreRejectsOversizedBatchAtomically(t *testing.T) {
	current := time.Now().UTC()
	store := newSourceReferenceStore(func() time.Time { return current })
	seed, err := store.put(sourceReference{AuthSessionID: "seed"})
	if err != nil {
		t.Fatalf("seed source reference: %v", err)
	}
	oversized := make([]sourceReference, maximumSourceReferences+1)
	if _, err := store.putAll(oversized); err == nil {
		t.Fatal("oversized source-reference batch was accepted")
	}
	if len(store.entries) != 1 {
		t.Fatalf("oversized batch changed store size to %d", len(store.entries))
	}
	if _, exists := store.entries[seed.ID]; !exists {
		t.Fatal("oversized batch removed the existing source reference")
	}
}
