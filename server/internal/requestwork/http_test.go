package requestwork

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRequestIDAcceptsSafeValueAndReplacesInvalidValues(t *testing.T) {
	const supplied = "edge-01:request_42.v1"
	ctx, requestID := WithRequestID(context.Background(), supplied)
	if requestID != supplied || RequestID(ctx) != supplied {
		t.Fatalf("accepted request ID = %q context=%q", requestID, RequestID(ctx))
	}
	boundary := strings.Repeat("z", 128)
	boundaryContext, boundaryID := WithRequestID(context.Background(), boundary)
	if boundaryID != boundary || RequestID(boundaryContext) != boundary {
		t.Fatalf("128-character request ID was replaced: %q", boundaryID)
	}

	invalid := []string{"", "has whitespace", "line\nbreak", "quoted\"value", "slash/value", "comma,value", strings.Repeat("a", 129), "nonascii-é"}
	seen := make(map[string]struct{}, len(invalid))
	for _, value := range invalid {
		ctx, generated := WithRequestID(context.Background(), value)
		if len(generated) != 32 || !ValidRequestID(generated) || generated == value || RequestID(ctx) != generated {
			t.Fatalf("replacement for %q = %q context=%q", value, generated, RequestID(ctx))
		}
		if _, duplicate := seen[generated]; duplicate {
			t.Fatalf("generated duplicate request ID %q", generated)
		}
		seen[generated] = struct{}{}
	}
}

func TestPropagateRequestIDCopiesContextValueOnly(t *testing.T) {
	ctx, requestID := WithRequestID(context.Background(), "caller-request-7")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://provider.example/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	PropagateRequestID(request)
	if got := request.Header.Get(RequestIDHeader); got != requestID {
		t.Fatalf("outbound request ID = %q, want %q", got, requestID)
	}

	withoutContext, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://provider.example/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	withoutContext.Header.Set(RequestIDHeader, "transport-owned")
	PropagateRequestID(withoutContext)
	if got := withoutContext.Header.Get(RequestIDHeader); got != "transport-owned" {
		t.Fatalf("helper changed header without context: %q", got)
	}
}

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
