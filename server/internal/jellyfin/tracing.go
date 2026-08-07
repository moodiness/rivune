package jellyfin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const compatRequestCompletedMessage = "jellyfin compatibility request completed"

type routeTracer struct {
	logger *slog.Logger
	route  Route
	method string
	next   http.Handler
}

func traceRoute(logger *slog.Logger, definition RouteSpec, next http.Handler) http.Handler {
	if logger == nil {
		return next
	}
	return &routeTracer{
		logger: logger,
		route:  definition.Route,
		method: definition.Method,
		next:   next,
	}
}

func (tracer *routeTracer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	logContext := context.Background()
	if !tracer.logger.Enabled(logContext, slog.LevelInfo) {
		tracer.next.ServeHTTP(response, request)
		return
	}

	started := time.Now()
	observed := &observedResponseWriter{ResponseWriter: response}
	tracer.next.ServeHTTP(observed, request)
	status := observed.status
	if status == 0 {
		status = http.StatusOK
	}
	tracer.logger.LogAttrs(
		logContext,
		slog.LevelInfo,
		compatRequestCompletedMessage,
		slog.String("route", string(tracer.route)),
		slog.String("method", tracer.method),
		slog.Int("status", status),
		slog.Duration("duration", time.Since(started)),
	)
}

type observedResponseWriter struct {
	http.ResponseWriter
	status int
}

func (response *observedResponseWriter) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
	response.ResponseWriter.WriteHeader(status)
}

func (response *observedResponseWriter) Write(payload []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.ResponseWriter.Write(payload)
}

// ReadFrom retains the optimized copy path used for large artwork and media
// responses instead of forcing io.Copy to allocate its fallback buffer.
func (response *observedResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	if readerFrom, ok := response.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(source)
	}
	return io.Copy(response.ResponseWriter, source)
}

// Unwrap lets http.ResponseController reach every optional interface exposed by
// the original writer (full duplex, deadlines, hijacking, and flushing).
func (response *observedResponseWriter) Unwrap() http.ResponseWriter {
	return response.ResponseWriter
}

// Flush also preserves the common direct http.Flusher assertion used by
// streaming handlers. ResponseController follows Unwrap on modern servers.
func (response *observedResponseWriter) Flush() {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	_ = http.NewResponseController(response.ResponseWriter).Flush()
}
