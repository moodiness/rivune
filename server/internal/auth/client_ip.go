package auth

import "context"

type clientIPContextKey struct{}

func WithClientIP(ctx context.Context, address string) context.Context {
	if address == "" {
		return ctx
	}
	return context.WithValue(ctx, clientIPContextKey{}, address)
}

// ClientIP returns the canonical address installed by the trusted-proxy-aware
// HTTP middleware. It never reads forwarding headers directly.
func ClientIP(ctx context.Context) string {
	address, _ := ctx.Value(clientIPContextKey{}).(string)
	return address
}
