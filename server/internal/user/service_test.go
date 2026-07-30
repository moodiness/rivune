package user

import (
	"errors"
	"testing"
)

func TestValidateUserInputs(t *testing.T) {
	if err := validateUsername("alice"); err != nil {
		t.Fatalf("valid username rejected: %v", err)
	}
	if err := validatePassword("long-enough-password"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	for _, role := range []string{"admin", "member"} {
		if err := validateRole(role); err != nil {
			t.Fatalf("valid role %q rejected: %v", role, err)
		}
	}
}

func TestValidateUserInputsRejectsInvalidValues(t *testing.T) {
	if err := validateUsername("ab"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected short username rejection, got %v", err)
	}
	if err := validatePassword("short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected short password rejection, got %v", err)
	}
	if err := validateRole("owner"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unknown role rejection, got %v", err)
	}
}
