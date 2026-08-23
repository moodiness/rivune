package httpapi

import (
	"net/http"
	"strings"
)

const (
	tvPlatformHeader    = "X-Rivune-TV-Platform"
	tvCORSMaxAge        = "600"
	tvCORSExposeHeaders = "Retry-After"
)

var tvCORSAllowedMethods = [...]string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

var tvCORSAllowedHeaders = [...]string{
	"Authorization",
	"Content-Type",
	profileContextHeader,
	tvPlatformHeader,
}

var (
	tvCORSMethods = strings.Join(tvCORSAllowedMethods[:], ", ")
	tvCORSHeaders = strings.Join(tvCORSAllowedHeaders[:], ", ")
)

func tvCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasOpaqueOrigin(r.Header) {
			next.ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodOptions {
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			if !validTVPreflight(r.Header) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", "null")
			w.Header().Set("Access-Control-Allow-Methods", tvCORSMethods)
			w.Header().Set("Access-Control-Allow-Headers", tvCORSHeaders)
			w.Header().Set("Access-Control-Expose-Headers", tvCORSExposeHeaders)
			w.Header().Set("Access-Control-Max-Age", tvCORSMaxAge)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Add("Vary", "Origin")
		w.Header().Add("Vary", tvPlatformHeader)
		if validTVPlatform(r.Header) {
			w.Header().Set("Access-Control-Expose-Headers", tvCORSExposeHeaders)
			w.Header().Set("Access-Control-Allow-Origin", "null")
		}
		next.ServeHTTP(w, r)
	})
}

func hasOpaqueOrigin(header http.Header) bool {
	values := header.Values("Origin")
	return len(values) == 1 && values[0] == "null"
}

func validTVPlatform(header http.Header) bool {
	values := header.Values(tvPlatformHeader)
	return len(values) == 1 && (values[0] == "webos" || values[0] == "tizen")
}

func validTVPreflight(header http.Header) bool {
	methods := header.Values("Access-Control-Request-Method")
	if len(methods) != 1 || !allowedTVMethod(strings.TrimSpace(methods[0])) {
		return false
	}

	requestedHeaders := header.Values("Access-Control-Request-Headers")
	if len(requestedHeaders) == 0 {
		return false
	}
	platformRequested := false
	for _, value := range requestedHeaders {
		for requested := range strings.SplitSeq(value, ",") {
			requested = strings.TrimSpace(requested)
			if requested == "" || !allowedTVHeader(requested) {
				return false
			}
			if strings.EqualFold(requested, tvPlatformHeader) {
				platformRequested = true
			}
		}
	}
	return platformRequested
}

func allowedTVMethod(requested string) bool {
	for _, allowed := range tvCORSAllowedMethods {
		if strings.EqualFold(requested, allowed) {
			return true
		}
	}
	return false
}

func allowedTVHeader(requested string) bool {
	for _, allowed := range tvCORSAllowedHeaders {
		if strings.EqualFold(requested, allowed) {
			return true
		}
	}
	return false
}
