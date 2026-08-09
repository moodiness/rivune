package jellyfin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	compatRequestCompletedMessage = "jellyfin compatibility request completed"
	maximumCompatTraceQueryNames  = 32
	maximumCompatTraceNameBytes   = 64
	maximumCompatTraceJSONBytes   = 32 << 10
	maximumCompatTraceJSONFields  = 32
)

type routeTracer struct {
	logger *slog.Logger
	debug  bool
	route  Route
	method string
	next   http.Handler
}

func traceRoute(logger *slog.Logger, debug bool, definition RouteSpec, next http.Handler) http.Handler {
	if logger == nil {
		return next
	}
	return &routeTracer{
		logger: logger,
		debug:  debug,
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
	requestContext, counters := requestwork.WithCounters(request.Context())
	request = request.WithContext(requestContext)
	observed := &observedResponseWriter{ResponseWriter: response, captureJSON: tracer.debug}
	tracer.next.ServeHTTP(observed, request)
	status := observed.status
	if status == 0 {
		status = http.StatusOK
	}
	work := counters.Snapshot()
	if !tracer.debug {
		tracer.logger.LogAttrs(
			logContext,
			slog.LevelInfo,
			compatRequestCompletedMessage,
			slog.String("route", string(tracer.route)),
			slog.String("method", tracer.method),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(started)),
			slog.Int64("db_call_count", work.DBCalls),
			slog.Duration("db_duration", work.DBDuration),
			slog.Int64("outbound_call_count", work.OutboundCalls),
			slog.Duration("outbound_duration", work.OutboundDuration),
			slog.Int64("upstream_bytes", work.UpstreamBytes),
			slog.Int64("bytes", observed.bytes),
			slog.Bool("range_request", request.Header.Get("Range") != ""),
			slog.String("content_type", compatTraceContentType(observed.Header().Get("Content-Type"))),
		)
		return
	}
	contentType := compatTraceContentType(observed.Header().Get("Content-Type"))
	attrs := []slog.Attr{
		slog.String("route", string(tracer.route)),
		slog.String("method", tracer.method),
		slog.Int("status", status),
		slog.Duration("duration", time.Since(started)),
		slog.Int64("db_call_count", work.DBCalls),
		slog.Duration("db_duration", work.DBDuration),
		slog.Int64("outbound_call_count", work.OutboundCalls),
		slog.Duration("outbound_duration", work.OutboundDuration),
		slog.Int64("upstream_bytes", work.UpstreamBytes),
		slog.Int64("bytes", observed.bytes),
		slog.Bool("range_request", request.Header.Get("Range") != ""),
		slog.String("content_type", contentType),
	}
	attrs = append(attrs, debugTraceAttrs(request, observed)...)
	tracer.logger.LogAttrs(logContext, slog.LevelInfo, compatRequestCompletedMessage, attrs...)
}

func debugTraceAttrs(request *http.Request, response *observedResponseWriter) []slog.Attr {
	queryNames, queryCardinalities := compatTraceQueryShape(request)
	clientFamily, clientVersion, clientMetadata := compatTraceClientIdentity(request)
	shape, fields := compatTraceJSONShape(response.jsonPrefix)
	return []slog.Attr{
		slog.String("client_family", clientFamily),
		slog.String("client_version_major", clientVersion),
		slog.Bool("client_metadata_present", clientMetadata),
		slog.Any("query_names", queryNames),
		slog.Any("query_cardinalities", queryCardinalities),
		slog.Any("headers_present", compatTraceHeadersPresent(request.Header)),
		slog.Bool("range_response", response.Header().Get("Content-Range") != ""),
		slog.String("json_top_level", shape),
		slog.Any("json_fields", fields),
	}
}

func compatTraceQueryShape(request *http.Request) ([]string, []int) {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return []string{}, []int{}
	}
	query := request.URL.Query()
	rawNames := make([]string, 0, len(query))
	for name := range query {
		rawNames = append(rawNames, name)
	}
	sort.Strings(rawNames)
	if len(rawNames) > maximumCompatTraceQueryNames {
		rawNames = rawNames[:maximumCompatTraceQueryNames]
	}
	names := make([]string, 0, len(rawNames))
	cardinalities := make([]int, 0, len(rawNames))
	for _, name := range rawNames {
		names = append(names, boundedTraceName(name))
		cardinalities = append(cardinalities, len(query[name]))
	}
	return names, cardinalities
}

func compatTraceHeadersPresent(headers http.Header) []string {
	selected := []string{
		"Accept", "Authorization", "Content-Type", "If-None-Match", "Origin", "Range", "User-Agent",
		"X-Emby-Authorization", "X-Emby-Token", "X-MediaBrowser-Authorization", "X-MediaBrowser-Token",
	}
	present := make([]string, 0, len(selected))
	for _, name := range selected {
		if len(headers.Values(name)) != 0 {
			present = append(present, name)
		}
	}
	return present
}

func compatTraceClientIdentity(request *http.Request) (family string, version string, present bool) {
	if request == nil {
		return "unknown", "unknown", false
	}
	identity, err := ParseClientIdentity(request.Header)
	if err != nil {
		return "unknown", "unknown", false
	}
	return compatTraceClientFamily(identity.Client), compatTraceVersionMajor(identity.Version), true
}

func compatTraceClientFamily(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range []struct {
		needle string
		family string
	}{
		{"streamyfin", "streamyfin"},
		{"swiftfin", "swiftfin"},
		{"findroid", "findroid"},
		{"infuse", "infuse"},
		{"jellyfin web", "jellyfin-web"},
		{"jellyfin", "jellyfin"},
		{"kodi", "kodi"},
		{"generic client", "generic-client"},
	} {
		if strings.Contains(normalized, candidate.needle) {
			return candidate.family
		}
	}
	return "other"
}

func compatTraceVersionMajor(value string) string {
	value = strings.TrimSpace(value)
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' && end < 4 {
		end++
	}
	if end == 0 || end < len(value) && value[end] >= '0' && value[end] <= '9' {
		return "unknown"
	}
	return value[:end]
}

func boundedTraceName(value string) string {
	if value == "" || len(value) > maximumCompatTraceNameBytes {
		return "other"
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return "other"
	}
	return value
}

func compatTraceJSONShape(payload []byte) (string, []string) {
	if len(payload) == 0 {
		return "unavailable", []string{}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return "unavailable", []string{}
	}
	delimiter, structured := token.(json.Delim)
	if !structured {
		return "scalar", []string{}
	}
	if delimiter == '[' {
		return "array", []string{}
	}
	if delimiter != '{' {
		return "unavailable", []string{}
	}
	fields := make([]string, 0, maximumCompatTraceJSONFields)
	for decoder.More() && len(fields) < maximumCompatTraceJSONFields {
		field, fieldErr := decoder.Token()
		if fieldErr != nil {
			break
		}
		name, ok := field.(string)
		if !ok {
			break
		}
		fields = append(fields, boundedTraceName(name))
		if skipCompatTraceJSONValue(decoder, 0) != nil {
			break
		}
	}
	return "object", fields
}

func skipCompatTraceJSONValue(decoder *json.Decoder, depth int) error {
	if depth >= 16 {
		return io.ErrUnexpectedEOF
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, structured := token.(json.Delim)
	if !structured || delimiter != '{' && delimiter != '[' {
		return nil
	}
	for decoder.More() {
		if delimiter == '{' {
			if _, err := decoder.Token(); err != nil {
				return err
			}
		}
		if err := skipCompatTraceJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

type observedResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	captureJSON bool
	jsonPrefix  []byte
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
	written, err := response.ResponseWriter.Write(payload)
	response.bytes += int64(written)
	response.captureJSONPayload(payload[:written])
	return written, err
}

// ReadFrom retains the optimized copy path used for large artwork and media
// responses instead of forcing io.Copy to allocate its fallback buffer.
func (response *observedResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	if response.captureJSON && isCompatTraceJSON(response.Header().Get("Content-Type")) {
		source = io.TeeReader(source, compatTraceCaptureSink{response: response})
	}
	var written int64
	var err error
	if readerFrom, ok := response.ResponseWriter.(io.ReaderFrom); ok {
		written, err = readerFrom.ReadFrom(source)
	} else {
		written, err = io.Copy(response.ResponseWriter, source)
	}
	response.bytes += written
	return written, err
}

func (response *observedResponseWriter) captureJSONPayload(payload []byte) {
	if !response.captureJSON || !isCompatTraceJSON(response.Header().Get("Content-Type")) || len(response.jsonPrefix) >= maximumCompatTraceJSONBytes {
		return
	}
	remaining := maximumCompatTraceJSONBytes - len(response.jsonPrefix)
	if len(payload) > remaining {
		payload = payload[:remaining]
	}
	response.jsonPrefix = append(response.jsonPrefix, payload...)
}

func isCompatTraceJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))
}

func compatTraceContentType(contentType string) string {
	if strings.TrimSpace(contentType) == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || len(mediaType) > maximumCompatTraceNameBytes {
		return "invalid"
	}
	return strings.ToLower(mediaType)
}

type compatTraceCaptureSink struct {
	response *observedResponseWriter
}

func (sink compatTraceCaptureSink) Write(payload []byte) (int, error) {
	sink.response.captureJSONPayload(payload)
	return len(payload), nil
}

// Unwrap lets http.ResponseController reach every optional interface exposed by
// the original writer (full duplex, deadlines, hijacking, and flushing).
func (response *observedResponseWriter) Unwrap() http.ResponseWriter {
	return response.ResponseWriter
}

// Hijack preserves WebSocket upgrades through the route tracing wrapper.
func (response *observedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(response.ResponseWriter).Hijack()
}

// Flush also preserves the common direct http.Flusher assertion used by
// streaming handlers. ResponseController follows Unwrap on modern servers.
func (response *observedResponseWriter) Flush() {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	_ = http.NewResponseController(response.ResponseWriter).Flush()
}
