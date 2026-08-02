package httpapi

import "context"

func (a *API) RunArtworkWarmup(ctx context.Context) {
	a.artwork.RunWarmup(ctx)
}
