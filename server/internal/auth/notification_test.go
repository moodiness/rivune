package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBroadcastSessionNotificationRequiresAdministratorBeforeStorage(t *testing.T) {
	service := &Service{}
	_, err := service.BroadcastSessionNotification(context.Background(), Principal{Role: "member"}, "a2cf8952-1250-4caf-94de-909f58bdc35e", "Hello")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestBroadcastSessionNotificationValidatesBoundedPlainTextBeforeStorage(t *testing.T) {
	service := &Service{}
	principal := Principal{Role: "admin"}
	for _, test := range []struct {
		name    string
		key     string
		message string
	}{
		{name: "missing key", message: "Hello"},
		{name: "invalid key", key: "not-a-uuid", message: "Hello"},
		{name: "blank message", key: "a2cf8952-1250-4caf-94de-909f58bdc35e", message: "  "},
		{name: "too long", key: "a2cf8952-1250-4caf-94de-909f58bdc35e", message: strings.Repeat("é", maximumSessionNotificationLength+1)},
		{name: "invalid utf8", key: "a2cf8952-1250-4caf-94de-909f58bdc35e", message: string([]byte{0xff})},
		{name: "nul byte", key: "a2cf8952-1250-4caf-94de-909f58bdc35e", message: "hello\x00world"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.BroadcastSessionNotification(context.Background(), principal, test.key, test.message)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestAcknowledgeSessionNotificationRejectsInvalidIdentifiersBeforeStorage(t *testing.T) {
	service := &Service{}
	for _, notificationID := range []int64{-1, 0} {
		if err := service.AcknowledgeSessionNotification(context.Background(), Principal{}, notificationID); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("id %d: expected ErrInvalidInput, got %v", notificationID, err)
		}
	}
}
