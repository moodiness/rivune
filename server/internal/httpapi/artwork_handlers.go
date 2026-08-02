package httpapi

import (
	"net/http"
)

func (a *API) artworkAsset(response http.ResponseWriter, request *http.Request) {
	if a.artwork == nil {
		http.Error(response, "artwork not found", http.StatusNotFound)
		return
	}
	a.artwork.ServeHTTP(response, request)
}
