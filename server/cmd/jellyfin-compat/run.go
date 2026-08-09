package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const redacted = "[REDACTED]"

var snapshotResponseHeaders = map[string]struct{}{
	"Accept-Ranges":  {},
	"Cache-Control":  {},
	"Content-Length": {},
	"Content-Range":  {},
	"Content-Type":   {},
	"Etag":           {},
	"Last-Modified":  {},
	"Location":       {},
}

type targetSpec struct {
	Name string
	URL  *url.URL
}

type scrubber struct {
	secrets []string
}

func (s *scrubber) Add(value string) {
	if value == "" || value == redacted || slices.Contains(s.secrets, value) {
		return
	}
	s.secrets = append(s.secrets, value)
	sort.Slice(s.secrets, func(i, j int) bool { return len(s.secrets[i]) > len(s.secrets[j]) })
}

func (s *scrubber) Text(value string) string {
	for _, secret := range s.secrets {
		value = replaceSecretRepresentations(value, secret, redacted)
	}
	return value
}

func replaceSecretRepresentations(value, secret, replacement string) string {
	if secret == "" {
		return value
	}
	var builder strings.Builder
	for offset := 0; offset < len(value); {
		if end, matched := matchSecretRepresentation(value, offset, secret); matched {
			builder.WriteString(replacement)
			offset = end
			continue
		}
		builder.WriteByte(value[offset])
		offset++
	}
	return builder.String()
}

func matchSecretRepresentation(value string, offset int, secret string) (int, bool) {
	position := offset
	for secretIndex := 0; secretIndex < len(secret); secretIndex++ {
		if position >= len(value) {
			return offset, false
		}
		if value[position] == '%' && position+2 < len(value) {
			high, highOK := hexNibble(value[position+1])
			low, lowOK := hexNibble(value[position+2])
			if highOK && lowOK && high<<4|low == secret[secretIndex] {
				position += 3
				continue
			}
		}
		if secret[secretIndex] == ' ' && value[position] == '+' {
			position++
			continue
		}
		if value[position] != secret[secretIndex] {
			return offset, false
		}
		position++
	}
	return position, true
}

func hexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func (s *scrubber) Value(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return s.Text(typed), nil
	case json.Number:
		if s.Text(typed.String()) != typed.String() {
			return redacted, nil
		}
		return typed, nil
	case bool:
		text := strconv.FormatBool(typed)
		if s.Text(text) != text {
			return redacted, nil
		}
		return typed, nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			transformed, err := s.Value(item)
			if err != nil {
				return nil, err
			}
			result[index] = transformed
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for name, item := range typed {
			transformedName := s.Text(name)
			if _, collision := result[transformedName]; collision {
				return nil, fmt.Errorf("secret scrubbing causes duplicate JSON object key %q", transformedName)
			}
			transformed, err := s.Value(item)
			if err != nil {
				return nil, err
			}
			result[transformedName] = transformed
		}
		return result, nil
	case nil:
		if s.Text("null") != "null" {
			return redacted, nil
		}
		return nil, nil
	default:
		return value, nil
	}
}

type snapshotMeta struct {
	Version int                `json:"version"`
	Steps   []snapshotStepMeta `json:"steps"`
}

type snapshotStepMeta struct {
	ID      string `json:"id"`
	Compare string `json:"compare"`
	Skipped string `json:"skipped,omitempty"`
}

type targetRunConfig struct {
	secretValues       map[string]string
	seededCaptures     map[string]string
	secretSeededValues []string
	preseededSteps     map[string]struct{}
}

type bodySummary struct {
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

func parseTarget(value string) (targetSpec, error) {
	name, rawURL, found := strings.Cut(value, "=")
	if !found || !identifierPattern.MatchString(name) {
		return targetSpec{}, errors.New("target must use NAME=http[s]://host form with an identifier name")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return targetSpec{}, fmt.Errorf("target %q must use an absolute http or https URL", name)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return targetSpec{}, fmt.Errorf("target %q URL must not contain credentials, query, or fragment", name)
	}
	escapedPath := strings.TrimRight(parsed.EscapedPath(), "/")
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return targetSpec{}, fmt.Errorf("target %q URL path is invalid", name)
	}
	parsed.Path = decodedPath
	parsed.RawPath = escapedPath
	return targetSpec{Name: name, URL: parsed}, nil
}

func runManifest(ctx context.Context, manifest *Manifest, targets []targetSpec, outputRoot string, getenv func(string) (string, bool)) error {
	if len(targets) != 2 {
		return errors.New("run requires exactly two -target values")
	}
	if strings.EqualFold(targets[0].Name, targets[1].Name) {
		return errors.New("target names must be distinct")
	}
	if outputRoot == "" {
		return errors.New("run requires -out")
	}
	configs := make([]targetRunConfig, len(targets))
	for index, target := range targets {
		config, err := preflightTarget(manifest, target, getenv)
		if err != nil {
			return err
		}
		configs[index] = config
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return fmt.Errorf("create output root: %w", err)
	}
	for _, target := range targets {
		directory := filepath.Join(outputRoot, target.Name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("target %q: create output directory: %w", target.Name, err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("target %q: secure output directory: %w", target.Name, err)
		}
		if err := cleanTargetDirectory(directory); err != nil {
			return fmt.Errorf("target %q: clean stale snapshots: %w", target.Name, err)
		}
	}
	for index, target := range targets {
		if err := runTarget(ctx, manifest, target, filepath.Join(outputRoot, target.Name), configs[index]); err != nil {
			return err
		}
	}
	return nil
}

func runTarget(ctx context.Context, manifest *Manifest, target targetSpec, directory string, config targetRunConfig) error {
	captures := make(map[string]string, len(config.seededCaptures))
	for name, value := range config.seededCaptures {
		captures[name] = value
	}
	scrub := &scrubber{}
	for _, value := range config.secretValues {
		scrub.Add(value)
	}
	for _, value := range config.secretSeededValues {
		scrub.Add(value)
	}
	cachedGetenv := func(name string) (string, bool) {
		value, exists := config.secretValues[name]
		return value, exists
	}
	client := &http.Client{
		Timeout: time.Duration(manifest.Defaults.TimeoutMS) * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	metadata := snapshotMeta{Version: manifestVersion, Steps: make([]snapshotStepMeta, 0, len(manifest.Steps))}
	for _, step := range manifest.Steps {
		if _, preseeded := config.preseededSteps[step.ID]; preseeded {
			metadata.Steps = append(metadata.Steps, snapshotStepMeta{ID: step.ID, Compare: step.Compare, Skipped: "preseeded"})
			continue
		}
		if err := executeStep(ctx, client, manifest.Defaults, step, target, directory, captures, scrub, cachedGetenv); err != nil {
			return fmt.Errorf("target %q step %q: %s", target.Name, step.ID, scrub.Text(err.Error()))
		}
		metadata.Steps = append(metadata.Steps, snapshotStepMeta{ID: step.ID, Compare: step.Compare})
	}
	encoded, err := marshalCanonical(metadata)
	if err != nil {
		return fmt.Errorf("target %q: encode metadata: %w", target.Name, err)
	}
	if int64(len(encoded)) > maxManifestBytes {
		return fmt.Errorf("target %q: metadata exceeds %d byte limit", target.Name, maxManifestBytes)
	}
	if err := atomicWrite(filepath.Join(directory, "_meta.json"), encoded); err != nil {
		return fmt.Errorf("target %q: write metadata: %w", target.Name, err)
	}
	return nil
}
func preflightTarget(manifest *Manifest, target targetSpec, getenv func(string) (string, bool)) (targetRunConfig, error) {
	config := targetRunConfig{
		secretValues:   make(map[string]string),
		seededCaptures: make(map[string]string),
		preseededSteps: make(map[string]struct{}),
	}
	for _, step := range manifest.Steps {
		seededCount := 0
		for _, capture := range step.Captures {
			environmentName := "JFCOMPAT_" + environmentToken(target.Name) + "_CAPTURE_" + environmentToken(capture.Name)
			value, exists := getenv(environmentName)
			if !exists || value == "" {
				continue
			}
			seededCount++
			config.seededCaptures[capture.Name] = value
			if *capture.Secret {
				config.secretSeededValues = append(config.secretSeededValues, value)
			}
		}
		if seededCount > 0 && seededCount != len(step.Captures) {
			return targetRunConfig{}, fmt.Errorf("target %q step %q: partial capture preseed (%d of %d); set every capture or none", target.Name, step.ID, seededCount, len(step.Captures))
		}
		if len(step.Captures) > 0 && seededCount == len(step.Captures) {
			config.preseededSteps[step.ID] = struct{}{}
		}
	}
	requiredNames := manifestSecretNames(manifest, config.preseededSteps)
	required := make(map[string]struct{}, len(requiredNames))
	for _, name := range requiredNames {
		required[name] = struct{}{}
	}
	for _, name := range manifestSecretNames(manifest, nil) {
		environmentName := "JFCOMPAT_" + environmentToken(target.Name) + "_" + environmentToken(name)
		value, exists := getenv(environmentName)
		if !exists || value == "" {
			if _, needed := required[name]; needed {
				return targetRunConfig{}, fmt.Errorf("target %q: required secret environment variable %s is not set", target.Name, environmentName)
			}
			continue
		}
		config.secretValues[environmentName] = value
	}
	return config, nil
}

func cleanTargetDirectory(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		managed := strings.HasSuffix(name, ".http") || strings.HasSuffix(name, ".json")
		if entry.IsDir() || !managed {
			continue
		}
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	return nil
}

func manifestSecretNames(manifest *Manifest, preseededSteps map[string]struct{}) []string {
	names := make(map[string]struct{})
	collect := func(value string) error {
		for offset := 0; ; {
			startRelative := strings.Index(value[offset:], "{{secret:")
			if startRelative < 0 {
				break
			}
			start := offset + startRelative + len("{{secret:")
			endRelative := strings.Index(value[start:], "}}")
			if endRelative < 0 {
				break
			}
			end := start + endRelative
			names[value[start:end]] = struct{}{}
			offset = end + 2
		}
		return nil
	}
	activeStep := false
	for _, step := range manifest.Steps {
		if _, preseeded := preseededSteps[step.ID]; preseeded {
			continue
		}
		activeStep = true
		_ = collect(step.Request.Path)
		for _, value := range step.Request.Headers {
			_ = collect(value)
		}
		if len(step.Request.JSON) > 0 {
			requestJSON, err := decodeRawJSON(step.Request.JSON)
			if err == nil {
				_ = walkStrings(requestJSON, collect)
			}
		}
	}
	if activeStep {
		for _, value := range manifest.Defaults.Headers {
			_ = collect(value)
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func executeStep(ctx context.Context, client *http.Client, defaults Defaults, step Step, target targetSpec, directory string, captures map[string]string, scrub *scrubber, getenv func(string) (string, bool)) error {
	expand := func(value string) (string, error) {
		return expandTemplates(value, captures, target.Name, scrub, getenv)
	}
	requestPath, err := expand(step.Request.Path)
	if err != nil {
		return err
	}
	reference, err := url.Parse(requestPath)
	if err != nil || !strings.HasPrefix(requestPath, "/") || strings.HasPrefix(requestPath, "//") || strings.Contains(requestPath, "#") || (reference != nil && (reference.IsAbs() || reference.Host != "" || reference.Fragment != "" || reference.Opaque != "")) {
		return errors.New("expanded request path is not origin-relative")
	}
	requestURL, err := url.Parse(strings.TrimRight(target.URL.String(), "/") + requestPath)
	if err != nil {
		return errors.New("expanded request URL is invalid")
	}
	var body io.Reader
	if len(step.Request.JSON) > 0 {
		requestJSON, err := decodeRawJSON(step.Request.JSON)
		if err != nil {
			return fmt.Errorf("decode request JSON: %w", err)
		}
		expandedJSON, err := transformStrings(requestJSON, expand)
		if err != nil {
			return err
		}
		encoded, err := marshalCanonical(expandedJSON)
		if err != nil {
			return fmt.Errorf("encode request JSON: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestContext, cancel := context.WithTimeout(ctx, time.Duration(defaults.TimeoutMS)*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, step.Request.Method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	headers := make(map[string]string, len(defaults.Headers)+len(step.Request.Headers))
	for name, value := range defaults.Headers {
		headers[http.CanonicalHeaderKey(name)] = value
	}
	for name, value := range step.Request.Headers {
		headers[http.CanonicalHeaderKey(name)] = value
	}
	for name, value := range headers {
		expanded, err := expand(value)
		if err != nil {
			return fmt.Errorf("expand header %q: %w", name, err)
		}
		if err := validateHeader(name, expanded); err != nil {
			return err
		}
		request.Header.Set(name, expanded)
	}
	if len(step.Request.JSON) > 0 && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %s", scrub.Text(err.Error()))
	}
	defer response.Body.Close()
	limit := defaults.MaxResponseBytes
	if step.Expect.MaxBytes != nil {
		limit = *step.Expect.MaxBytes
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read response: %s", scrub.Text(err.Error()))
	}
	if int64(len(responseBody)) > limit {
		return fmt.Errorf("response exceeds %d byte limit", limit)
	}
	if !slices.Contains(step.Expect.Statuses, response.StatusCode) {
		return fmt.Errorf("unexpected status %d; expected one of %v", response.StatusCode, step.Expect.Statuses)
	}
	mediaType := response.Header.Get("Content-Type")
	if parsed, _, parseErr := mime.ParseMediaType(mediaType); parseErr == nil {
		mediaType = strings.ToLower(parsed)
	}
	if len(step.Expect.ContentTypes) > 0 && !matchesContentType(mediaType, step.Expect.ContentTypes) {
		return fmt.Errorf("unexpected content type %q", scrub.Text(mediaType))
	}
	var responseJSON any
	isJSON := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	if isJSON {
		if err := rejectDuplicateJSONKeys(responseBody); err != nil {
			return fmt.Errorf("invalid JSON response: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(responseBody))
		decoder.UseNumber()
		if err := decoder.Decode(&responseJSON); err != nil {
			return fmt.Errorf("decode JSON response: %w", err)
		}
		if err := rejectTrailingJSON(decoder); err != nil {
			return fmt.Errorf("decode JSON response: %w", err)
		}
	}
	if !isJSON && len(step.Canonicalize) > 0 {
		return errors.New("canonicalization requires a JSON response")
	}
	for _, capture := range step.Captures {
		if !isJSON {
			return fmt.Errorf("capture %q requires a JSON response", capture.Name)
		}
		value, found, err := pointerValue(responseJSON, capture.Pointer)
		if err != nil {
			return fmt.Errorf("capture %q: %w", capture.Name, err)
		}
		if !found {
			return fmt.Errorf("capture %q pointer was not found", capture.Name)
		}
		text, err := captureText(value)
		if err != nil {
			return fmt.Errorf("capture %q: %w", capture.Name, err)
		}
		captures[capture.Name] = text
		if *capture.Secret {
			scrub.Add(text)
		}
	}
	if isJSON {
		for _, rule := range step.Canonicalize {
			responseJSON, err = applyRule(responseJSON, rule, scrub)
			if err != nil {
				return fmt.Errorf("canonicalize %s at %q: %w", rule.Op, rule.Pointer, err)
			}
		}
	}
	var snapshotBody any
	bodyKind := "sha256"
	if isJSON {
		snapshotBody, err = scrub.Value(responseJSON)
		if err != nil {
			return fmt.Errorf("scrub JSON response: %w", err)
		}
		bodyKind = "json"
	} else {
		digest := sha256.Sum256(responseBody)
		snapshotBody = bodySummary{Length: len(responseBody), SHA256: hex.EncodeToString(digest[:])}
	}
	jsonSnapshot, err := marshalCanonical(snapshotBody)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	httpSnapshot := makeHTTPSnapshot(response, bodyKind, scrub)
	if int64(len(jsonSnapshot)) > maxComparisonFileBytes || int64(len(httpSnapshot)) > maxComparisonFileBytes {
		return fmt.Errorf("canonical snapshot exceeds %d byte comparison limit", maxComparisonFileBytes)
	}
	if err := atomicWrite(filepath.Join(directory, step.ID+".json"), jsonSnapshot); err != nil {
		return fmt.Errorf("write JSON snapshot: %w", err)
	}
	if err := atomicWrite(filepath.Join(directory, step.ID+".http"), httpSnapshot); err != nil {
		return fmt.Errorf("write HTTP snapshot: %w", err)
	}
	return nil
}

func matchesContentType(actual string, expected []string) bool {
	for _, candidate := range expected {
		candidateType, _, err := mime.ParseMediaType(candidate)
		if err != nil {
			candidateType = candidate
		}
		candidateType = strings.ToLower(candidateType)
		if candidateType == "*/*" || candidateType == actual {
			return true
		}
		if strings.HasSuffix(candidateType, "/*") && strings.HasPrefix(actual, strings.TrimSuffix(candidateType, "*")) {
			return true
		}
	}
	return false
}

func expandTemplates(value string, captures map[string]string, targetName string, scrub *scrubber, getenv func(string) (string, bool)) (string, error) {
	var result strings.Builder
	for offset := 0; offset < len(value); {
		startRelative := strings.Index(value[offset:], "{{")
		if startRelative < 0 {
			result.WriteString(value[offset:])
			break
		}
		start := offset + startRelative
		result.WriteString(value[offset:start])
		endRelative := strings.Index(value[start+2:], "}}")
		if endRelative < 0 {
			return "", errors.New("unclosed template")
		}
		end := start + 2 + endRelative
		name := value[start+2 : end]
		if strings.HasPrefix(name, "secret:") {
			secretName := strings.TrimPrefix(name, "secret:")
			environmentName := "JFCOMPAT_" + environmentToken(targetName) + "_" + environmentToken(secretName)
			secret, exists := getenv(environmentName)
			if !exists || secret == "" {
				return "", fmt.Errorf("required secret environment variable %s is not set", environmentName)
			}
			scrub.Add(secret)
			result.WriteString(secret)
		} else {
			capture, exists := captures[name]
			if !exists {
				return "", fmt.Errorf("capture %q is unavailable", name)
			}
			result.WriteString(capture)
		}
		offset = end + 2
	}
	return result.String(), nil
}

func environmentToken(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' {
			character -= 'a' - 'A'
		}
		if (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			result.WriteRune(character)
		} else {
			result.WriteByte('_')
		}
	}
	return result.String()
}

func transformStrings(value any, transform func(string) (string, error)) (any, error) {
	switch typed := value.(type) {
	case string:
		return transform(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			transformed, err := transformStrings(item, transform)
			if err != nil {
				return nil, err
			}
			result[index] = transformed
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for name, item := range typed {
			transformedName, err := transform(name)
			if err != nil {
				return nil, err
			}
			if _, duplicate := result[transformedName]; duplicate {
				return nil, fmt.Errorf("templates produce duplicate JSON object key %q", transformedName)
			}
			transformed, err := transformStrings(item, transform)
			if err != nil {
				return nil, err
			}
			result[transformedName] = transformed
		}
		return result, nil
	default:
		return value, nil
	}
}

func pointerValue(root any, pointer string) (any, bool, error) {
	tokens, err := parsePointer(pointer)
	if err != nil {
		return nil, false, err
	}
	current := root
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			var found bool
			current, found = typed[token]
			if !found {
				return nil, false, nil
			}
		case []any:
			index, err := pointerArrayIndex(token, len(typed))
			if err != nil {
				return nil, false, err
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func pointerArrayIndex(token string, length int) (int, error) {
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, fmt.Errorf("invalid array index %q", token)
	}
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 || index >= length {
		return 0, fmt.Errorf("array index %q is out of bounds", token)
	}
	return index, nil
}

func captureText(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case nil:
		return "null", nil
	default:
		return "", errors.New("captured value must be a string, number, boolean, or null")
	}
}

func applyRule(root any, rule Rule, scrub *scrubber) (any, error) {
	tokens, err := parsePointer(rule.Pointer)
	if err != nil {
		return nil, err
	}
	var replacement any
	if rule.Op == "replace" {
		decoder := json.NewDecoder(bytes.NewReader(rule.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&replacement); err != nil {
			return nil, err
		}
	}
	updated, found, err := mutatePointer(root, tokens, rule.Op, replacement, scrub)
	if err != nil {
		return nil, err
	}
	if !found && !rule.Optional {
		return nil, errors.New("pointer was not found")
	}
	return updated, nil
}

func mutatePointer(current any, tokens []string, operation string, replacement any, scrub *scrubber) (any, bool, error) {
	if len(tokens) == 0 {
		switch operation {
		case "replace":
			return replacement, true, nil
		case "remove":
			return nil, true, nil
		case "sort":
			array, ok := current.([]any)
			if !ok {
				return current, true, errors.New("sort target is not an array")
			}
			type sortItem struct {
				value     any
				canonical []byte
			}
			items := make([]sortItem, len(array))
			for index, value := range array {
				scrubbed, err := scrub.Value(value)
				if err != nil {
					return current, true, err
				}
				canonical, err := marshalCanonical(scrubbed)
				if err != nil {
					return current, true, err
				}
				items[index] = sortItem{value: value, canonical: canonical}
			}
			sort.SliceStable(items, func(i, j int) bool {
				return bytes.Compare(items[i].canonical, items[j].canonical) < 0
			})
			for index := range array {
				array[index] = items[index].value
			}
			return array, true, nil
		case "normalize-url-host":
			text, ok := current.(string)
			if !ok {
				return current, true, errors.New("normalize-url-host target is not a string")
			}
			parsed, err := url.Parse(text)
			if err != nil {
				return current, true, fmt.Errorf("parse URL: %w", err)
			}
			if parsed.Host == "" {
				return current, true, nil
			}
			parsed.Scheme = "http"
			parsed.Host = "target.invalid"
			parsed.User = nil
			return parsed.String(), true, nil
		default:
			return current, true, fmt.Errorf("unsupported operation %q", operation)
		}
	}
	token := tokens[0]
	switch typed := current.(type) {
	case map[string]any:
		child, exists := typed[token]
		if !exists {
			return current, false, nil
		}
		if len(tokens) == 1 && operation == "remove" {
			delete(typed, token)
			return current, true, nil
		}
		updated, found, err := mutatePointer(child, tokens[1:], operation, replacement, scrub)
		if err != nil || !found {
			return current, found, err
		}
		typed[token] = updated
		return current, true, nil
	case []any:
		index, err := pointerArrayIndex(token, len(typed))
		if err != nil {
			return current, false, nil
		}
		if len(tokens) == 1 && operation == "remove" {
			return append(typed[:index], typed[index+1:]...), true, nil
		}
		updated, found, err := mutatePointer(typed[index], tokens[1:], operation, replacement, scrub)
		if err != nil || !found {
			return current, found, err
		}
		typed[index] = updated
		return current, true, nil
	default:
		return current, false, nil
	}
}

func marshalCanonical(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func makeHTTPSnapshot(response *http.Response, bodyKind string, scrub *scrubber) []byte {
	var builder strings.Builder
	statusText := http.StatusText(response.StatusCode)
	fmt.Fprintf(&builder, "HTTP/1.1 %d", response.StatusCode)
	if statusText != "" {
		builder.WriteByte(' ')
		builder.WriteString(statusText)
	}
	builder.WriteByte('\n')
	names := make([]string, 0)
	for name := range response.Header {
		canonical := http.CanonicalHeaderKey(name)
		if bodyKind == "json" && (canonical == "Content-Length" || canonical == "Etag" || canonical == "Last-Modified") {
			continue
		}
		if _, allowed := snapshotResponseHeaders[canonical]; allowed {
			names = append(names, canonical)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		values := slices.Clone(response.Header.Values(name))
		for index, value := range values {
			values[index] = scrub.Text(value)
		}
		slices.Sort(values)
		for _, value := range values {
			fmt.Fprintf(&builder, "%s: %s\n", name, value)
		}
	}
	fmt.Fprintf(&builder, "X-Jellyfin-Compat-Body: %s\n\n", bodyKind)
	return []byte(builder.String())
}

func atomicWrite(path string, data []byte) (returnErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".jellyfin-compat-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); returnErr == nil && closeErr != nil {
				returnErr = closeErr
			}
		}
		if returnErr != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
