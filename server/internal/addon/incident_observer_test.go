package addon

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
)

type capturedIncidentObserver struct {
	profileID string
	addonID   string
	addonName string
	code      string
	successes int
}

func (observer *capturedIncidentObserver) RecordFailure(_ context.Context, profileID, addonID, addonName, code string) error {
	observer.profileID, observer.addonID, observer.addonName, observer.code = profileID, addonID, addonName, code
	return nil
}

func (observer *capturedIncidentObserver) RecordSuccess(_ context.Context, profileID, addonID string) error {
	observer.profileID, observer.addonID = profileID, addonID
	observer.successes++
	return nil
}

func authPrincipalWithProfile(profileID string) auth.Principal {
	return auth.Principal{ActiveProfileID: &profileID, ActiveProfileCanManage: true}
}

func TestIncidentObserverReceivesOnlyClosedSafeFailureCodes(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	addonID := "22222222-2222-4222-8222-222222222222"
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "timeout", err: context.DeadlineExceeded, code: "timeout"},
		{name: "unavailable", err: fmt.Errorf("https://private.invalid/?token=secret: %w", ErrProviderUnavailable), code: "unavailable"},
		{name: "invalid response", err: fmt.Errorf("private response body: %w", ErrInvalidResponse), code: "invalid_response"},
		{name: "unhealthy", err: errors.New("dial private.invalid: connection refused"), code: "unhealthy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer := &capturedIncidentObserver{}
			service := NewService(nil, nil, discardLogger())
			service.SetIncidentObserver(observer)
			service.recordIncident(context.Background(), authPrincipalWithProfile(profileID), addonID, "Safe name", test.err)
			if observer.profileID != profileID || observer.addonID != addonID || observer.addonName != "Safe name" || observer.code != test.code {
				t.Fatalf("captured incident = %+v", observer)
			}
			for _, forbidden := range []string{"private.invalid", "token", "secret", "response body", "connection refused"} {
				if observer.code == forbidden || observer.addonName == forbidden {
					t.Fatalf("captured incident exposed private detail %q: %+v", forbidden, observer)
				}
			}
		})
	}
}

func TestIncidentObserverIgnoresClientCancellationAndRecordsRecovery(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	observer := &capturedIncidentObserver{}
	service := NewService(nil, nil, discardLogger())
	service.SetIncidentObserver(observer)
	principal := authPrincipalWithProfile(profileID)
	service.recordIncident(context.Background(), principal, "22222222-2222-4222-8222-222222222222", "Safe name", context.Canceled)
	if observer.code != "" || observer.successes != 0 {
		t.Fatalf("client cancellation recorded incident: %+v", observer)
	}
	service.recordIncident(context.Background(), principal, "22222222-2222-4222-8222-222222222222", "Safe name", nil)
	if observer.successes != 1 {
		t.Fatalf("success count = %d, want 1", observer.successes)
	}
}
