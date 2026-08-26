package coordination

import (
	"errors"
	"testing"
)

func TestOperationLoadAndTerminalResultContracts(t *testing.T) {
	position := int64(0)
	item := &PlaybackItem{TitleID: "22222222-2222-4222-8222-222222222222"}
	service := &Service{}
	valid := CommandInput{OperationID: "11111111-1111-4111-8111-111111111111", Command: "load", Mode: "handoff", Item: item, PositionMilliseconds: &position}
	if _, err := service.normalizeCommand(valid); err != nil {
		t.Fatalf("valid handoff load rejected: %v", err)
	}
	valid.Mode = ""
	if _, err := service.normalizeCommand(valid); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("load without mode error = %v", err)
	}
	for _, result := range []CommandResultInput{{Status: "applied", Code: "applied"}, {Status: "failed", Code: "stale_target"}, {Status: "expired", Code: "expired"}} {
		if !validCommandResult(result) {
			t.Fatalf("valid result rejected: %+v", result)
		}
	}
	for _, result := range []CommandResultInput{{Status: "applied", Code: "execution_failed"}, {Status: "pending", Code: "applied"}, {Status: "failed", Code: "raw_error"}} {
		if validCommandResult(result) {
			t.Fatalf("invalid result accepted: %+v", result)
		}
	}
}
