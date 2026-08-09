package addon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/moodiness/rivune/server/internal/requestwork"
)

func TestObservedTransportAggregatesSuccessFailureAndPayloadBytes(t *testing.T) {
	providerFailure := errors.New("provider failure")
	calls := 0
	transport := observeTransport(functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		calls++
		if calls == 1 {
			return json.RawMessage(`{"streams":[]}`), CachePolicy{}, nil
		}
		return json.RawMessage(`{"partial":true}`), CachePolicy{}, providerFailure
	}})
	ctx, counters := requestwork.WithCounters(context.Background())

	first, _, err := transport.Resource(ctx, "https://provider.invalid/private?token=secret", ResourcePath{})
	if err != nil {
		t.Fatalf("successful transport call: %v", err)
	}
	second, _, err := transport.Resource(ctx, "https://provider.invalid/private?token=secret", ResourcePath{})
	if !errors.Is(err, providerFailure) {
		t.Fatalf("failed transport call = %v", err)
	}

	snapshot := counters.Snapshot()
	wantBytes := int64(len(first) + len(second))
	if snapshot.OutboundCalls != 2 || snapshot.UpstreamBytes != wantBytes || snapshot.OutboundDuration < 0 {
		t.Fatalf("outbound snapshot = %+v, want calls=2 bytes=%d", snapshot, wantBytes)
	}
}
