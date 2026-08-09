package requestwork

import (
	"context"
	"io"
	"sync/atomic"
)

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
