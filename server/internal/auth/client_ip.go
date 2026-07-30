package auth

import "context"

type clientIPContextKey struct{}

func WithClientIP(ctx context.Context, address string) context.Context {
	if address == "" {
		return ctx
	}
	return context.WithValue(ctx, clientIPContextKey{}, address)
}

func clientIPFromContext(ctx context.Context) string {
	address, _ := ctx.Value(clientIPContextKey{}).(string)
	return address
}
