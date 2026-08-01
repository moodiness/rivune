package httpapi

import "context"

func (a *API) RunTracking(ctx context.Context) {
	if a.tracking != nil {
		a.tracking.Run(ctx)
	}
}
