package playback

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestPlaybackClonesPreserveEmptyJSONArrays(t *testing.T) {
	cloned := cloneMediaInspection(MediaInspection{
		VideoTracks: []MediaTrack{{Index: 0, Type: "video", Codec: "h264"}},
		AudioTracks: []MediaTrack{{Index: 1, Type: "audio", Codec: "aac"}},
	})
	if cloned.VideoTracks == nil || cloned.AudioTracks == nil || cloned.SubtitleTracks == nil {
		t.Fatalf("cloned media tracks must remain JSON arrays: %+v", cloned)
	}
	prepared := clonePreparedPlayback(preparedPlayback{})
	if prepared.subtitles == nil || prepared.providerErrors == nil {
		t.Fatalf("cloned playback lists must remain JSON arrays: %+v", prepared)
	}
}

func TestSourceReferenceStoreOwnerChurnNeverEvictsForeignReferences(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileID := "profile-a"
	ownerA := auth.Principal{SessionID: "session-a", UserID: "user-a", DeviceID: "device-a", ActiveProfileID: &profileID}
	profileB := "profile-b"
	ownerB := auth.Principal{SessionID: "session-b", UserID: "user-b", DeviceID: "device-b", ActiveProfileID: &profileB}

	originalA, err := store.putAll(ownerA, make([]sourceReference, maximumSourceReferencesPerOwner))
	if err != nil {
		t.Fatalf("fill owner A quota: %v", err)
	}
	foreign, err := store.put(ownerB, sourceReference{ResourceID: "foreign"})
	if err != nil {
		t.Fatalf("store owner B reference: %v", err)
	}
	replacementA, err := store.putAll(ownerA, make([]sourceReference, maximumSourceReferencesPerOwner))
	if err != nil {
		t.Fatalf("churn owner A quota: %v", err)
	}
	if len(store.entries) != maximumSourceReferencesPerOwner+1 {
		t.Fatalf("entries after owner churn = %d, want %d", len(store.entries), maximumSourceReferencesPerOwner+1)
	}
	if _, err := store.get(foreign.ID, ownerB); err != nil {
		t.Fatalf("owner A churn evicted owner B reference: %v", err)
	}
	for _, reference := range originalA {
		if _, exists := store.entries[reference.ID]; exists {
			t.Fatalf("old owner A reference %q survived a full deterministic quota replacement", reference.ID)
		}
	}
	for _, reference := range replacementA {
		if _, exists := store.entries[reference.ID]; !exists {
			t.Fatalf("replacement owner A reference %q was not stored", reference.ID)
		}
	}
}

func TestSourceReferenceStoreReloginSharesStableOwnerQuotaAndLookupRemainsExact(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileID := "profile-id"
	firstSession := auth.Principal{SessionID: "session-1", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	secondSession := firstSession
	secondSession.SessionID = "session-2"

	firstBatch, err := store.putAll(firstSession, make([]sourceReference, maximumSourceReferencesPerOwner))
	if err != nil {
		t.Fatalf("fill first session quota: %v", err)
	}
	forgedProfile := "forged-profile"
	newest, err := store.put(secondSession, sourceReference{
		AuthSessionID: "forged-session",
		ProfileID:     forgedProfile,
		Owner:         sourceReferenceOwner{UserID: "forged-user", ProfileID: forgedProfile, DeviceID: "forged-device"},
	})
	if err != nil {
		t.Fatalf("store relogin reference: %v", err)
	}
	if len(store.entries) != maximumSourceReferencesPerOwner {
		t.Fatalf("same stable owner used %d entries, want shared quota %d", len(store.entries), maximumSourceReferencesPerOwner)
	}
	if _, exists := store.entries[firstBatch[0].ID]; exists {
		t.Fatalf("deterministic owner eviction retained earliest identifier %q", firstBatch[0].ID)
	}
	wantOwner := sourceReferenceOwner{UserID: firstSession.UserID, ProfileID: profileID, DeviceID: firstSession.DeviceID}
	if newest.Owner != wantOwner || newest.AuthSessionID != secondSession.SessionID || newest.ProfileID != profileID {
		t.Fatalf("store trusted caller identity fields: owner=%+v session=%q profile=%q", newest.Owner, newest.AuthSessionID, newest.ProfileID)
	}
	if _, err := store.get(newest.ID, firstSession); err != ErrSourceReferenceExpired {
		t.Fatalf("cross-session lookup error = %v, want opaque expiry", err)
	}
	wrongProfile := secondSession
	otherProfileID := "other-profile"
	wrongProfile.ActiveProfileID = &otherProfileID
	if _, err := store.get(newest.ID, wrongProfile); err != ErrSourceReferenceExpired {
		t.Fatalf("cross-profile lookup error = %v, want opaque expiry", err)
	}
	if _, err := store.get(newest.ID, secondSession); err != nil {
		t.Fatalf("exact session/profile lookup failed: %v", err)
	}
	wrongDevice := secondSession
	wrongDevice.DeviceID = "other-device"
	if _, err := store.get(newest.ID, wrongDevice); err != ErrSourceReferenceExpired {
		t.Fatalf("cross-device lookup error = %v, want opaque expiry", err)
	}
	wrongUser := secondSession
	wrongUser.UserID = "other-user"
	if _, err := store.get(newest.ID, wrongUser); err != ErrSourceReferenceExpired {
		t.Fatalf("cross-user lookup error = %v, want opaque expiry", err)
	}
}

func TestSourceReferenceStoreDeviceAndProfileOwnersAreIsolated(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileA := "profile-a"
	base := auth.Principal{SessionID: "base-session", UserID: "user-id", DeviceID: "device-a", ActiveProfileID: &profileA}
	otherDevice := base
	otherDevice.SessionID = "device-session"
	otherDevice.DeviceID = "device-b"
	profileB := "profile-b"
	otherProfile := base
	otherProfile.SessionID = "profile-session"
	otherProfile.ActiveProfileID = &profileB

	if _, err := store.putAll(base, make([]sourceReference, maximumSourceReferencesPerOwner)); err != nil {
		t.Fatalf("fill base owner: %v", err)
	}
	deviceReference, err := store.put(otherDevice, sourceReference{})
	if err != nil {
		t.Fatalf("store other device: %v", err)
	}
	profileReference, err := store.put(otherProfile, sourceReference{})
	if err != nil {
		t.Fatalf("store other profile: %v", err)
	}
	if _, err := store.put(base, sourceReference{}); err != nil {
		t.Fatalf("churn base owner: %v", err)
	}
	if _, exists := store.entries[deviceReference.ID]; !exists {
		t.Fatal("base owner evicted the other device")
	}
	if _, exists := store.entries[profileReference.ID]; !exists {
		t.Fatal("base owner evicted the other profile")
	}
}

func TestSourceReferenceStoreGlobalSaturationRejectsWithoutMutation(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	for index := range maximumSourceReferences {
		identifier := fmt.Sprintf("existing-%04d", index)
		store.entries[identifier] = sourceReference{
			ID: identifier,
			Owner: sourceReferenceOwner{
				UserID:    fmt.Sprintf("user-%d", index/maximumSourceReferencesPerOwner),
				ProfileID: "profile-id",
				DeviceID:  "device-id",
			},
			ExpiresAt: now.Add(time.Hour),
		}
	}
	before := sourceReferenceStoreIdentifiers(store)
	profileID := "profile-id"
	newOwner := auth.Principal{SessionID: "new-session", UserID: "new-user", DeviceID: "new-device", ActiveProfileID: &profileID}
	if _, err := store.put(newOwner, sourceReference{}); err == nil {
		t.Fatal("globally saturated store accepted a new owner")
	}
	after := sourceReferenceStoreIdentifiers(store)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("global saturation mutated live references: before=%d after=%d", len(before), len(after))
	}
}

func TestSourceReferenceStoreRandomFailureIsAtomic(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileID := "profile-id"
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	seed, err := store.put(principal, sourceReference{ResourceID: "seed"})
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	before := sourceReferenceStoreIdentifiers(store)
	calls := 0
	store.newIdentifier = func() (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("random unavailable")
		}
		return "unused-identifier", nil
	}
	if _, err := store.putAll(principal, []sourceReference{{}, {}}); err == nil {
		t.Fatal("batch succeeded after identifier generation failure")
	}
	if calls != 2 {
		t.Fatalf("identifier generator calls = %d, want 2", calls)
	}
	if after := sourceReferenceStoreIdentifiers(store); !reflect.DeepEqual(after, before) {
		t.Fatalf("identifier generation failure mutated store: before=%v after=%v", before, after)
	}
	if stored := store.entries[seed.ID]; stored.ResourceID != "seed" {
		t.Fatalf("identifier failure changed seeded reference: %+v", stored)
	}
}

func TestSourceReferenceStoreExpiresBeforeAdmission(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	for index := range maximumSourceReferences {
		identifier := fmt.Sprintf("expired-%04d", index)
		store.entries[identifier] = sourceReference{ID: identifier, ExpiresAt: now}
	}
	profileID := "profile-id"
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	stored, err := store.put(principal, sourceReference{})
	if err != nil {
		t.Fatalf("admit after expiry: %v", err)
	}
	if len(store.entries) != 1 {
		t.Fatalf("expired entries remaining after admission = %d", len(store.entries)-1)
	}
	if _, err := store.get(stored.ID, principal); err != nil {
		t.Fatalf("new reference unavailable after expiry cleanup: %v", err)
	}
	now = now.Add(sourceReferenceTTL)
	if _, err := store.get(stored.ID, principal); err != ErrSourceReferenceExpired || len(store.entries) != 0 {
		t.Fatalf("reference at TTL boundary: err=%v entries=%d", err, len(store.entries))
	}
}

func TestSourceReferenceStorePinnedReferencesSurviveOwnerChurnAndRollbackAtomically(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileID := "profile-id"
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	references, err := store.putAll(principal, make([]sourceReference, maximumSourceReferencesPerOwner))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.pin(principal, []string{references[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.put(principal, sourceReference{ResourceID: "replacement"}); err != nil {
		t.Fatalf("churn around pinned reference: %v", err)
	}
	if _, exists := store.entries[references[0].ID]; !exists {
		t.Fatal("owner churn evicted the active pinned reference")
	}
	for _, reference := range references {
		if err := store.pin(principal, []string{reference.ID}); err != nil && reference.ID != references[1].ID {
			t.Fatalf("pin retained reference %q: %v", reference.ID, err)
		}
	}
	for identifier, reference := range store.entries {
		if store.pins[identifier] == 0 {
			if err := store.pin(principal, []string{reference.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := sourceReferenceStoreIdentifiers(store)
	if _, err := store.put(principal, sourceReference{}); err == nil {
		t.Fatal("fully pinned owner admitted a reference")
	}
	if after := sourceReferenceStoreIdentifiers(store); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed pinned admission mutated store: before=%v after=%v", before, after)
	}
}

func TestSourceReferenceStorePinnedReferenceExpiresBeforeFinalRelease(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	profileID := "profile-id"
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	reference, err := store.put(principal, sourceReference{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.pin(principal, []string{reference.ID, "missing-reference"}); err != ErrSourceReferenceExpired {
		t.Fatalf("mixed valid/invalid pin error=%v", err)
	}
	if store.pins[reference.ID] != 0 {
		t.Fatal("failed pin batch retained a partial pin")
	}
	if err := store.pin(principal, []string{reference.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.pin(principal, []string{reference.ID}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(sourceReferenceTTL)
	if _, err := store.get(reference.ID, principal); err != ErrSourceReferenceExpired {
		t.Fatalf("expired pinned reference remained usable: %v", err)
	}
	store.unpin(principal, []string{reference.ID})
	store.unpin(principal, []string{reference.ID})
	if _, exists := store.entries[reference.ID]; exists {
		t.Fatal("final release retained expired pinned reference")
	}
}

func TestSourceReferenceStoreUserAggregateAcrossThirtyTwoDevicesLeavesForeignCapacity(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileID := "profile-id"
	perDevice := maximumSourceReferencesPerUser / 32
	for device := range 32 {
		principal := auth.Principal{SessionID: fmt.Sprintf("session-%d", device), UserID: "shared-user", DeviceID: fmt.Sprintf("device-%d", device), ActiveProfileID: &profileID}
		references, err := store.putAll(principal, make([]sourceReference, perDevice))
		if err != nil {
			t.Fatalf("device %d admission: %v", device, err)
		}
		ids := make([]string, len(references))
		for index := range references {
			ids[index] = references[index].ID
		}
		if err := store.pin(principal, ids); err != nil {
			t.Fatalf("device %d pin: %v", device, err)
		}
	}
	before := sourceReferenceStoreIdentifiers(store)
	extra := auth.Principal{SessionID: "extra-session", UserID: "shared-user", DeviceID: "extra-device", ActiveProfileID: &profileID}
	if _, err := store.put(extra, sourceReference{}); err == nil {
		t.Fatal("one user exceeded its pinned aggregate quota")
	}
	if after := sourceReferenceStoreIdentifiers(store); !reflect.DeepEqual(after, before) {
		t.Fatal("aggregate rejection mutated pinned references")
	}
	foreign := auth.Principal{SessionID: "foreign-session", UserID: "foreign-user", DeviceID: "foreign-device", ActiveProfileID: &profileID}
	if _, err := store.put(foreign, sourceReference{}); err != nil {
		t.Fatalf("saturated user consumed foreign/global capacity: %v", err)
	}
}

func TestSourceReferenceStoreAtomicPinnedBatchRejectsConcurrentOwnerChurn(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	store.newIdentifier = sequentialSourceReferenceIdentifiers()
	profileID := "profile-id"
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	reserved, err := store.putAllPinned(principal, make([]sourceReference, maximumSourceReferencesPerOwner))
	if err != nil {
		t.Fatal(err)
	}
	before := sourceReferenceStoreIdentifiers(store)
	if _, err := store.putAll(principal, make([]sourceReference, maximumSourceReferencesPerOwner)); !errors.Is(err, ErrMediaCapacityReached) {
		t.Fatalf("concurrent churn error=%v", err)
	}
	if after := sourceReferenceStoreIdentifiers(store); !reflect.DeepEqual(after, before) {
		t.Fatal("concurrent churn invalidated an atomically reserved batch")
	}
	identifiers := make([]string, len(reserved))
	for index := range reserved {
		identifiers[index] = reserved[index].ID
	}
	store.unpin(principal, identifiers)
	if _, err := store.putAll(principal, make([]sourceReference, maximumSourceReferencesPerOwner)); err != nil {
		t.Fatalf("released reservation did not restore admission: %v", err)
	}
}

func TestSourceReferenceStoreSelectionCASReturnsCurrentOnLoss(t *testing.T) {
	now := time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)
	store := newSourceReferenceStore(func() time.Time { return now })
	profileID := "profile-id"
	principal := auth.Principal{SessionID: "session-id", UserID: "user-id", DeviceID: "device-id", ActiveProfileID: &profileID}
	oldAsset := storedAsset{ID: "source-id", URL: "https://media.example/old", Headers: map[string]string{"Authorization": "Bearer old"}}
	initial, err := store.put(principal, sourceReference{
		Source: Source{ID: "source-id", URL: oldAsset.URL}, Asset: &oldAsset,
	})
	if err != nil {
		t.Fatal(err)
	}
	newerAsset := storedAsset{ID: "source-id", URL: "https://media.example/u2", Headers: map[string]string{"Authorization": "Bearer u2"}}
	newerSource := initial.Source
	newerSource.URL = newerAsset.URL
	newer, replaced, err := store.replaceSelection(initial.ID, principal, initial.SelectionRevision, newerSource, &newerAsset)
	if err != nil || !replaced || newer.SelectionRevision != initial.SelectionRevision+1 || newer.TransportRevision != initial.TransportRevision+1 {
		t.Fatalf("newer CAS replacement: replaced=%v selectionRevision=%d transportRevision=%d err=%v", replaced, newer.SelectionRevision, newer.TransportRevision, err)
	}
	sameTransport, replaced, err := store.replaceSelection(initial.ID, principal, newer.SelectionRevision, newerSource, &newerAsset)
	if err != nil || !replaced || sameTransport.SelectionRevision != newer.SelectionRevision+1 || sameTransport.TransportRevision != newer.TransportRevision {
		t.Fatalf("same-transport CAS replacement: replaced=%v selectionRevision=%d transportRevision=%d err=%v", replaced, sameTransport.SelectionRevision, sameTransport.TransportRevision, err)
	}
	newer = sameTransport
	staleAsset := storedAsset{ID: "source-id", URL: "https://media.example/u1", Headers: map[string]string{"Authorization": "Bearer u1"}}
	staleSource := initial.Source
	staleSource.URL = staleAsset.URL
	current, replaced, err := store.replaceSelection(initial.ID, principal, initial.SelectionRevision, staleSource, &staleAsset)
	if err != nil || replaced {
		t.Fatalf("stale CAS replacement: replaced=%v err=%v", replaced, err)
	}
	if current.SelectionRevision != newer.SelectionRevision || current.TransportRevision != newer.TransportRevision || current.Asset == nil || current.Asset.URL != newerAsset.URL || current.Asset.Headers["Authorization"] != "Bearer u2" {
		t.Fatalf("stale CAS did not return current selection: selectionRevision=%d transportRevision=%d asset=%+v", current.SelectionRevision, current.TransportRevision, current.Asset)
	}
	current, expired, err := store.expireSelection(initial.ID, principal, initial.SelectionRevision)
	if err != nil || expired || current.SelectionRevision != newer.SelectionRevision {
		t.Fatalf("stale expiration did not converge: expired=%v revision=%d err=%v", expired, current.SelectionRevision, err)
	}
}

func TestStableSourceIdentityIgnoresProviderURLAndOrder(t *testing.T) {
	base := Source{AddonID: "addon-id", ManifestID: "manifest-id", Name: "1080p", Title: "Movie", Filename: "movie-1080p.mkv", URL: "https://one.example/video?token=secret", StreamIndex: 0}
	rotated := base
	rotated.URL = "https://two.example/renewed?token=other-secret"
	rotated.StreamIndex = 7
	if first, second := stableSourceIdentity(base), stableSourceIdentity(rotated); first == "" || first != second {
		t.Fatalf("URL/order rotation changed stable identity: %q != %q", first, second)
	}
	different := base
	different.Filename = "movie-720p.mkv"
	if stableSourceIdentity(different) == stableSourceIdentity(base) {
		t.Fatal("different URL-free stream identity collided")
	}
}

func sequentialSourceReferenceIdentifiers() func() (string, error) {
	index := 0
	return func() (string, error) {
		identifier := fmt.Sprintf("reference-%08d", index)
		index++
		return identifier, nil
	}
}

func sourceReferenceStoreIdentifiers(store *sourceReferenceStore) []string {
	identifiers := make([]string, 0, len(store.entries))
	for identifier := range store.entries {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	return identifiers
}

func TestCloneStoredAssetNormalizesHLSSegmentContainer(t *testing.T) {
	if got := cloneStoredAsset(storedAsset{}).HLSSegmentContainer; got != "ts" {
		t.Fatalf("default cloned HLS segment container = %q", got)
	}
	if got := cloneStoredAsset(storedAsset{HLSSegmentContainer: "mp4"}).HLSSegmentContainer; got != "mp4" {
		t.Fatalf("explicit cloned HLS segment container = %q", got)
	}
}
