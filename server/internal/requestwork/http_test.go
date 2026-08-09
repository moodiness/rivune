package requestwork

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestObservedBodyCountsBytesOnceAtEOForClose(t *testing.T) {
	ctx, counters := WithCounters(context.Background())
	started := Now()
	BeginOutbound(ctx, started)
	body := ObserveBody(ctx, io.NopCloser(strings.NewReader("private-provider-payload")))
	payload, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read observed body: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close observed body: %v", err)
	}

	snapshot := counters.Snapshot()
	if snapshot.OutboundCalls != 1 || snapshot.UpstreamBytes != int64(len(payload)) || snapshot.OutboundDuration < 0 {
		t.Fatalf("observed body snapshot = %+v", snapshot)
	}
}
