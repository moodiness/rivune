package profile

import (
	"errors"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/password"
)

func TestHashPINUsesPasswordHasher(t *testing.T) {
	pin := "2468"
	hash, err := hashPIN(&pin)
	if err != nil {
		t.Fatalf("hash PIN: %v", err)
	}
	if hash == nil || *hash == pin {
		t.Fatal("PIN was not hashed")
	}
	matches, err := password.Verify(pin, *hash)
	if err != nil {
		t.Fatalf("verify PIN hash: %v", err)
	}
	if !matches {
		t.Fatal("valid PIN did not match its hash")
	}
}

func TestHashPINRejectsInvalidFormats(t *testing.T) {
	for _, pin := range []string{"123", "123456789", "12ab", "１２３４"} {
		t.Run(pin, func(t *testing.T) {
			if _, err := hashPIN(&pin); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid PIN error for %q, got %v", pin, err)
			}
		})
	}
}

func TestHashPINAllowsNoPIN(t *testing.T) {
	hash, err := hashPIN(nil)
	if err != nil || hash != nil {
		t.Fatalf("expected nil PIN to remain unset, got hash %v and error %v", hash, err)
	}
}

func TestProfileAccessibleHonorsDisabledState(t *testing.T) {
	value := Profile{Enabled: false, AccessTimezone: "UTC"}
	if profileAccessible(value, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("disabled profile was accessible")
	}
}

func TestValidateAccessAllowsOvernightHours(t *testing.T) {
	value := Profile{Enabled: true, AccessStartTime: new("20:00"), AccessEndTime: new("08:00"), AccessTimezone: "America/Los_Angeles"}
	if err := validateAccess(value); err != nil {
		t.Fatalf("overnight hours were rejected: %v", err)
	}
}

func TestAccessScheduleDetectsDateAndHourRestrictions(t *testing.T) {
	if hasAccessSchedule(Profile{}) {
		t.Fatal("unrestricted profile was treated as scheduled")
	}
	for _, value := range []Profile{
		{AvailableFrom: new("2026-08-01")},
		{AvailableUntil: new("2026-08-31")},
		{AccessStartTime: new("08:00"), AccessEndTime: new("20:00")},
	} {
		if !hasAccessSchedule(value) {
			t.Fatalf("profile restriction was ignored: %+v", value)
		}
	}
}

func TestEnsureUnrestrictedProfilePreventsLockout(t *testing.T) {
	if err := ensureUnrestrictedProfile(1); err != nil {
		t.Fatalf("existing unrestricted profile was rejected: %v", err)
	}
	if err := ensureUnrestrictedProfile(0); !errors.Is(err, ErrLastUnrestrictedProfile) {
		t.Fatalf("expected lockout prevention error, got %v", err)
	}
}
