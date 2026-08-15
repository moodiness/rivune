package requestwork

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"sync/atomic"
)

const RequestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

// WithRequestID attaches a safe caller-supplied request ID or a fresh 128-bit
// random ID when the supplied value is absent or invalid.
func WithRequestID(ctx context.Context, supplied string) (context.Context, string) {
	requestID := supplied
	if !ValidRequestID(requestID) {
		requestID = newRequestID()
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID), requestID
}

// RequestID returns the validated server request ID carried by ctx.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// ValidRequestID accepts only a bounded ASCII token. The allowlist excludes
// whitespace, controls, delimiters, and characters with log-format semantics.
func ValidRequestID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' ||
			character == '.' || character == ':' {
			continue
		}
		return false
	}
	return true
}

// PropagateRequestID copies the request ID from the outbound request context.
// It never invents an ID outside the inbound request boundary.
func PropagateRequestID(request *http.Request) {
	if request == nil {
		return
	}
	if requestID := RequestID(request.Context()); requestID != "" {
		request.Header.Set(RequestIDHeader, requestID)
	}
}

func newRequestID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(random[:])
}

// ObserveBody finishes an outbound observation after the response body is fully
// consumed or closed. It records only a byte count and retains no request or URL.
func ObserveBody(ctx context.Context, body io.ReadCloser) io.ReadCloser {
	if FromContext(ctx) == nil || body == nil {
		return body
	}
	return &observedBody{ctx: ctx, body: body}
}

type observedBody struct {
	ctx   context.Context
	body  io.ReadCloser
	bytes atomic.Int64
	ended atomic.Bool
}

func (body *observedBody) Read(buffer []byte) (int, error) {
	read, err := body.body.Read(buffer)
	if read > 0 {
		body.bytes.Add(int64(read))
	}
	if err == io.EOF {
		body.finish()
	}
	return read, err
}

func (body *observedBody) Close() error {
	err := body.body.Close()
	body.finish()
	return err
}

func (body *observedBody) finish() {
	if body.ended.CompareAndSwap(false, true) {
		EndOutbound(body.ctx, Now(), body.bytes.Load())
	}
}
