package jellyfin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maximumCompatAuthorizationHeaderBytes  = 2048
	maximumCompatTokenHeaderBytes          = 128
	maximumCompatRawQueryBytes             = 3*MaximumQueryBytes + 2*MaximumQueryParameters
	maximumCompatSourceDeviceIDRunes       = 1024
	clientIdentityFailureHeaderBounds      = "header_bounds"
	clientIdentityFailureAuthorization     = "authorization_syntax"
	clientIdentityFailureAuthorizationNone = "authorization_absent"
	clientIdentityFailureClientMissing     = "client_missing"
	clientIdentityFailureDeviceMissing     = "device_missing"
	clientIdentityFailureDeviceIDMissing   = "device_id_missing"
	clientIdentityFailureVersionMissing    = "version_missing"
	clientIdentityFailureFieldBounds       = "field_bounds"
)

var ErrInvalidCompatAuthorization = errors.New("invalid compatibility authorization")

var compatAuthorizationHeaders = []string{
	"X-Emby-Authorization",
	"X-MediaBrowser-Authorization",
	"Authorization",
}

func ParseClientIdentity(headers http.Header) (ClientIdentity, error) {
	identity, _, err := parseClientIdentity(headers)
	return identity, err
}

func parseClientIdentity(headers http.Header) (ClientIdentity, string, error) {
	if !boundedCompatHeaders(headers) {
		return ClientIdentity{}, clientIdentityFailureHeaderBounds, ErrInvalidCompatAuthorization
	}
	parameters, found, err := collectAuthorizationParameters(headers)
	if err != nil {
		return ClientIdentity{}, clientIdentityFailureAuthorization, ErrInvalidCompatAuthorization
	}
	if !found {
		return ClientIdentity{}, clientIdentityFailureAuthorizationNone, ErrInvalidCompatAuthorization
	}
	client, okClient := parameters["client"]
	if !okClient {
		return ClientIdentity{}, clientIdentityFailureClientMissing, ErrInvalidCompatAuthorization
	}
	device, okDevice := parameters["device"]
	if !okDevice {
		return ClientIdentity{}, clientIdentityFailureDeviceMissing, ErrInvalidCompatAuthorization
	}
	deviceID, okDeviceID := parameters["deviceid"]
	if !okDeviceID {
		return ClientIdentity{}, clientIdentityFailureDeviceIDMissing, ErrInvalidCompatAuthorization
	}
	version, okVersion := parameters["version"]
	if !okVersion {
		return ClientIdentity{}, clientIdentityFailureVersionMissing, ErrInvalidCompatAuthorization
	}
	deviceID, okDeviceID = canonicalCompatDeviceID(deviceID)
	if !okDeviceID {
		return ClientIdentity{}, clientIdentityFailureFieldBounds, ErrInvalidCompatAuthorization
	}
	identity, err := normalizeClientIdentity(ClientIdentity{
		Client: client, Device: device, DeviceID: deviceID, Version: version,
	})
	if err != nil {
		return ClientIdentity{}, clientIdentityFailureFieldBounds, ErrInvalidCompatAuthorization
	}
	return identity, "", nil
}

func canonicalCompatDeviceID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if boundedUTF8(value, 1, 128) {
		return value, true
	}
	if !boundedUTF8(value, 129, maximumCompatSourceDeviceIDRunes) {
		return "", false
	}
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:]), true
}

// ParseCompatToken follows Jellyfin's canonical credential precedence. Query
// credentials remain accepted for every compatibility route; clients differ on
// whether they attach ApiKey globally or only to media URLs. The allowQuery
// argument is retained for source compatibility and no longer narrows parsing.
func ParseCompatToken(request *http.Request, _ bool) (string, error) {
	token, found, err := extractCompatToken(request)
	if err != nil || !found {
		return "", ErrInvalidCompatAuthorization
	}
	if _, ok := compatCredentialDigest(token); !ok {
		return "", ErrInvalidCompatAuthorization
	}
	return token, nil
}

// extractCompatToken returns the first credential transport Jellyfin would
// inspect. It deliberately does not validate Rivune's credential audience so
// stream routes can fall back to an already-negotiated PlaySessionId when an
// external player replaces the auth header with a playback capability.
func extractCompatToken(request *http.Request) (string, bool, error) {
	if request == nil || !boundedCompatHeaders(request.Header) || len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		return "", false, ErrInvalidCompatAuthorization
	}
	for _, name := range []string{"Authorization", "X-Emby-Authorization", "X-MediaBrowser-Authorization"} {
		values := request.Header.Values(name)
		if len(values) == 0 {
			continue
		}
		token, found, err := tokenFromAuthorizationValue(values[0], name == "Authorization")
		if err != nil {
			return "", false, err
		}
		if found {
			return token, true, nil
		}
	}
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		values := request.Header.Values(name)
		if len(values) == 0 || values[0] == "" {
			continue
		}
		token := strings.TrimSpace(values[0])
		if token == "" || strings.ContainsAny(token, ",\r\n\t ") {
			return "", false, ErrInvalidCompatAuthorization
		}
		return token, true, nil
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return "", false, ErrInvalidCompatAuthorization
	}
	for _, name := range []string{"ApiKey", "api_key"} {
		token, found, scalarErr := queryScalar(query, name)
		if scalarErr != nil {
			return "", false, ErrInvalidCompatAuthorization
		}
		if !found {
			continue
		}
		token = strings.TrimSpace(token)
		if token == "" {
			return "", false, ErrInvalidCompatAuthorization
		}
		return token, true, nil
	}
	return "", false, nil
}

func tokenFromAuthorizationValue(value string, schemeRequired bool) (string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || containsHeaderControl(value) {
		return "", false, ErrInvalidCompatAuthorization
	}
	if split := strings.IndexFunc(value, unicode.IsSpace); split >= 0 && strings.EqualFold(value[:split], "Bearer") {
		token := strings.TrimSpace(value[split:])
		if token == "" || strings.ContainsAny(token, ",\r\n\t ") {
			return "", false, ErrInvalidCompatAuthorization
		}
		return token, true, nil
	}
	parameters, relevant, err := parseAuthorizationValue(value, schemeRequired)
	if err != nil || !relevant {
		return "", false, err
	}
	token := strings.TrimSpace(parameters["token"])
	if token == "" {
		return "", false, nil
	}
	if strings.ContainsAny(token, ",\r\n\t ") {
		return "", false, ErrInvalidCompatAuthorization
	}
	return token, true, nil
}

func firstAuthorizationParameters(headers http.Header) (map[string]string, bool, error) {
	if !boundedCompatHeaders(headers) {
		return nil, false, ErrInvalidCompatAuthorization
	}
	for _, name := range []string{"Authorization", "X-Emby-Authorization", "X-MediaBrowser-Authorization"} {
		values := headers.Values(name)
		if len(values) == 0 {
			continue
		}
		value := strings.TrimSpace(values[0])
		if split := strings.IndexFunc(value, unicode.IsSpace); split >= 0 && strings.EqualFold(value[:split], "Bearer") {
			return nil, false, nil
		}
		parameters, relevant, err := parseAuthorizationValue(value, name == "Authorization")
		if err != nil {
			return nil, false, err
		}
		if relevant {
			return parameters, true, nil
		}
	}
	return nil, false, nil
}

func boundedCompatHeaders(headers http.Header) bool {
	for _, name := range compatAuthorizationHeaders {
		values := headers.Values(name)
		if len(values) > 1 {
			return false
		}
		if len(values) == 1 && len(values[0]) > maximumCompatAuthorizationHeaderBytes {
			return false
		}
	}
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		values := headers.Values(name)
		if len(values) > 1 {
			return false
		}
		if len(values) == 1 && len(values[0]) > maximumCompatTokenHeaderBytes {
			return false
		}
	}
	return true
}

func collectAuthorizationParameters(headers http.Header) (map[string]string, bool, error) {
	collected := make(map[string]string)
	found := false
	for _, name := range compatAuthorizationHeaders {
		values := headers.Values(name)
		if len(values) > 1 {
			return nil, false, ErrInvalidCompatAuthorization
		}
		if len(values) == 0 {
			continue
		}
		parameters, relevant, err := parseAuthorizationValue(values[0], name == "Authorization")
		if err != nil {
			return nil, false, err
		}
		if !relevant {
			continue
		}
		found = true
		for key, value := range parameters {
			if existing, exists := collected[key]; exists && existing != value {
				return nil, false, ErrInvalidCompatAuthorization
			}
			collected[key] = value
		}
	}
	return collected, found, nil
}

func parseAuthorizationValue(value string, schemeRequired bool) (map[string]string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || containsHeaderControl(value) {
		return nil, false, ErrInvalidCompatAuthorization
	}

	parametersText := value
	if split := strings.IndexFunc(value, unicode.IsSpace); split >= 0 {
		scheme := value[:split]
		if strings.EqualFold(scheme, "MediaBrowser") || strings.EqualFold(scheme, "Emby") {
			parametersText = strings.TrimSpace(value[split:])
		} else if schemeRequired {
			return nil, false, nil
		}
	} else if schemeRequired {
		return nil, false, nil
	}
	if parametersText == "" {
		return nil, false, ErrInvalidCompatAuthorization
	}
	parameters, err := parseAuthorizationParameters(parametersText)
	if err != nil {
		return nil, false, err
	}
	return parameters, true, nil
}

func parseAuthorizationParameters(value string) (map[string]string, error) {
	parameters := make(map[string]string)
	for position := 0; position < len(value); {
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		if position == len(value) {
			break
		}
		keyStart := position
		for position < len(value) && isAuthorizationKeyByte(value[position]) {
			position++
		}
		if position == keyStart {
			return nil, ErrInvalidCompatAuthorization
		}
		key := strings.ToLower(value[keyStart:position])
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		if position == len(value) || value[position] != '=' {
			return nil, ErrInvalidCompatAuthorization
		}
		position++
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		parsed, next, err := parseAuthorizationParameterValue(value, position)
		if err != nil {
			return nil, err
		}
		position = next
		// Jellyfin clients commonly advertise Token="" before authentication.
		// Required identity fields still reject empties, and token authentication validates its dedicated credential format later.
		if containsHeaderControl(parsed) || parsed == "" && key != "token" {
			return nil, ErrInvalidCompatAuthorization
		}
		if _, duplicate := parameters[key]; duplicate {
			return nil, ErrInvalidCompatAuthorization
		}
		parameters[key] = parsed
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		if position < len(value) {
			if value[position] != ',' {
				return nil, ErrInvalidCompatAuthorization
			}
			position++
			for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
				position++
			}
			if position == len(value) || value[position] == ',' {
				return nil, ErrInvalidCompatAuthorization
			}
		}
	}
	if len(parameters) == 0 {
		return nil, ErrInvalidCompatAuthorization
	}
	return parameters, nil
}

func parseAuthorizationParameterValue(value string, position int) (string, int, error) {
	if position >= len(value) {
		return "", position, ErrInvalidCompatAuthorization
	}
	if value[position] != '"' {
		start := position
		for position < len(value) && value[position] != ',' {
			position++
		}
		return strings.TrimSpace(value[start:position]), position, nil
	}
	position++
	var parsed strings.Builder
	for position < len(value) {
		current := value[position]
		position++
		switch current {
		case '"':
			return parsed.String(), position, nil
		case '\\':
			if position >= len(value) || (value[position] != '\\' && value[position] != '"') {
				return "", position, ErrInvalidCompatAuthorization
			}
			parsed.WriteByte(value[position])
			position++
		default:
			parsed.WriteByte(current)
		}
	}
	return "", position, ErrInvalidCompatAuthorization
}

func isAuthorizationKeyByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-'
}

func containsHeaderControl(value string) bool {
	for _, current := range value {
		if current < 0x20 && current != '\t' || current == 0x7f {
			return true
		}
	}
	return false
}
