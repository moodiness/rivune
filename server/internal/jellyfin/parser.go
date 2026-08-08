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
	maximumCompatQueryBytes                = 2048
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

// ParseCompatToken accepts query authentication only for explicitly scoped
// media and image routes. A token repeated in the query is always ambiguous;
// distinct header transports may repeat only the exact same credential.
func ParseCompatToken(request *http.Request, allowQuery bool) (string, error) {
	if request == nil {
		return "", ErrInvalidCompatAuthorization
	}
	if !boundedCompatHeaders(request.Header) || len(request.URL.RawQuery) > maximumCompatQueryBytes {
		return "", ErrInvalidCompatAuthorization
	}
	if values := request.Header.Values("Authorization"); len(values) == 1 {
		_, relevant, err := parseAuthorizationValue(values[0], true)
		if err != nil || !relevant {
			return "", ErrInvalidCompatAuthorization
		}
	}
	candidates := make([]string, 0, 4)
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		values := request.Header.Values(name)
		if len(values) > 1 {
			return "", ErrInvalidCompatAuthorization
		}
		if len(values) == 1 {
			value := strings.TrimSpace(values[0])
			if value == "" || strings.ContainsAny(value, ",\r\n\t ") {
				return "", ErrInvalidCompatAuthorization
			}
			candidates = append(candidates, value)
		}
	}

	parameters, found, err := collectAuthorizationParameters(request.Header)
	if err != nil {
		return "", ErrInvalidCompatAuthorization
	}
	if found {
		if token, ok := parameters["token"]; ok && token != "" {
			candidates = append(candidates, token)
		}
	}

	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return "", ErrInvalidCompatAuthorization
	}
	queryToken, hasQueryToken, queryTokenErr := queryScalar(query, "api_key")
	if queryTokenErr != nil {
		return "", ErrInvalidCompatAuthorization
	}
	if hasQueryToken {
		if !allowQuery || strings.TrimSpace(queryToken) == "" {
			return "", ErrInvalidCompatAuthorization
		}
		candidates = append(candidates, strings.TrimSpace(queryToken))
	}
	if len(candidates) == 0 {
		return "", ErrInvalidCompatAuthorization
	}
	selected := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate != selected {
			return "", ErrInvalidCompatAuthorization
		}
	}
	if _, ok := compatCredentialDigest(selected); !ok {
		return "", ErrInvalidCompatAuthorization
	}
	return selected, nil
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
