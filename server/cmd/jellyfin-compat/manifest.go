package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strconv"
	"strings"
)

const manifestVersion = 1

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var stepIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const (
	hardMaxResponseBytes int64 = 64 << 20
	hardMaxTimeoutMS           = 5 * 60 * 1000
)

type Manifest struct {
	Version  int      `json:"version"`
	Defaults Defaults `json:"defaults"`
	Steps    []Step   `json:"steps"`
}

type Defaults struct {
	TimeoutMS        int               `json:"timeoutMs"`
	MaxResponseBytes int64             `json:"maxResponseBytes"`
	Headers          map[string]string `json:"headers"`
}

type Step struct {
	ID           string    `json:"id"`
	Clients      []string  `json:"clients"`
	Status       string    `json:"status"`
	Request      Request   `json:"request"`
	Expect       Expect    `json:"expect"`
	Captures     []Capture `json:"captures"`
	Canonicalize []Rule    `json:"canonicalize"`
	Compare      string    `json:"compare"`
	ObservedGap  string    `json:"observedGap,omitempty"`
}

type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	JSON    json.RawMessage   `json:"json,omitempty"`
}

type Expect struct {
	Statuses     []int    `json:"statuses"`
	ContentTypes []string `json:"contentTypes,omitempty"`
	MaxBytes     *int64   `json:"maxBytes,omitempty"`
}

type Capture struct {
	Name    string `json:"name"`
	Pointer string `json:"pointer"`
	Secret  *bool  `json:"secret"`
}

type Rule struct {
	Op       string          `json:"op"`
	Pointer  string          `json:"pointer"`
	Value    json.RawMessage `json:"value,omitempty"`
	Optional bool            `json:"optional,omitempty"`
	Reason   string          `json:"reason"`
}

func decodeManifest(data []byte) (*Manifest, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, err
	}
	if err := validateManifestFieldNames(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, err
	}
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func validateManifestFieldNames(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if err := rejectUnknownKeys(root, "", "version", "defaults", "steps"); err != nil {
		return err
	}
	if defaults, ok := root["defaults"].(map[string]any); ok {
		if err := rejectUnknownKeys(defaults, "/defaults", "timeoutMs", "maxResponseBytes", "headers"); err != nil {
			return err
		}
	}
	steps, _ := root["steps"].([]any)
	for stepIndex, rawStep := range steps {
		step, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		stepPath := "/steps/" + strconv.Itoa(stepIndex)
		if err := rejectUnknownKeys(step, stepPath, "id", "clients", "status", "request", "expect", "captures", "canonicalize", "compare", "observedGap"); err != nil {
			return err
		}
		if err := rejectNullField(step, stepPath, "observedGap"); err != nil {
			return err
		}
		if request, ok := step["request"].(map[string]any); ok {
			if err := rejectUnknownKeys(request, stepPath+"/request", "method", "path", "headers", "json"); err != nil {
				return err
			}
			if err := rejectNullField(request, stepPath+"/request", "headers"); err != nil {
				return err
			}
		}
		if expect, ok := step["expect"].(map[string]any); ok {
			if err := rejectUnknownKeys(expect, stepPath+"/expect", "statuses", "contentTypes", "maxBytes"); err != nil {
				return err
			}
			for _, field := range []string{"contentTypes", "maxBytes"} {
				if err := rejectNullField(expect, stepPath+"/expect", field); err != nil {
					return err
				}
			}
		}
		captures, _ := step["captures"].([]any)
		for captureIndex, rawCapture := range captures {
			if capture, ok := rawCapture.(map[string]any); ok {
				if err := rejectUnknownKeys(capture, stepPath+"/captures/"+strconv.Itoa(captureIndex), "name", "pointer", "secret"); err != nil {
					return err
				}
				for _, field := range []string{"name", "pointer"} {
					if _, ok := capture[field].(string); !ok {
						return fmt.Errorf("field %q at %s/captures/%d is required and must be a string", field, stepPath, captureIndex)
					}
				}
				if _, ok := capture["secret"].(bool); !ok {
					return fmt.Errorf("field \"secret\" at %s/captures/%d is required and must be a boolean", stepPath, captureIndex)
				}
			}
		}
		rules, _ := step["canonicalize"].([]any)
		for ruleIndex, rawRule := range rules {
			if rule, ok := rawRule.(map[string]any); ok {
				if err := rejectUnknownKeys(rule, stepPath+"/canonicalize/"+strconv.Itoa(ruleIndex), "op", "pointer", "value", "optional", "reason"); err != nil {
					return err
				}
				for _, field := range []string{"op", "pointer", "reason"} {
					if _, ok := rule[field].(string); !ok {
						return fmt.Errorf("field %q at %s/canonicalize/%d is required and must be a string", field, stepPath, ruleIndex)
					}
				}
				if err := rejectNullField(rule, stepPath+"/canonicalize/"+strconv.Itoa(ruleIndex), "optional"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rejectUnknownKeys(object map[string]any, path string, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name := range object {
		if _, ok := allowedSet[name]; !ok {
			if path == "" {
				path = "/"
			}
			return fmt.Errorf("unknown field %q at %s", name, path)
		}
	}
	return nil
}

func rejectNullField(object map[string]any, path, name string) error {
	if value, present := object[name]; present && value == nil {
		return fmt.Errorf("field %q at %s must not be null", name, path)
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("manifest contains trailing JSON value")
	}
	return fmt.Errorf("manifest contains trailing data: %w", err)
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, "$"); err != nil {
		return err
	}
	if token, err := decoder.Token(); err == nil {
		return fmt.Errorf("trailing JSON token %v", token)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, at string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON at %s: %w", at, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("invalid object at %s: %w", at, err)
			}
			name, ok := nameToken.(string)
			if !ok {
				return fmt.Errorf("invalid object key at %s", at)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate JSON key %q at %s", name, at)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder, at+"/"+escapePointerToken(name)); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid object ending at %s", at)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := scanJSONValue(decoder, at+"/"+strconv.Itoa(index)); err != nil {
				return err
			}
			index++
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid array ending at %s", at)
		}
	default:
		return fmt.Errorf("unexpected delimiter %q at %s", delim, at)
	}
	return nil
}

func validateManifest(manifest *Manifest) error {
	if manifest.Version != manifestVersion {
		return fmt.Errorf("version must be %d", manifestVersion)
	}
	if manifest.Defaults.TimeoutMS <= 0 || manifest.Defaults.TimeoutMS > hardMaxTimeoutMS {
		return fmt.Errorf("defaults.timeoutMs must be between 1 and %d", hardMaxTimeoutMS)
	}
	if manifest.Defaults.MaxResponseBytes <= 0 || manifest.Defaults.MaxResponseBytes > hardMaxResponseBytes {
		return fmt.Errorf("defaults.maxResponseBytes must be between 1 and %d", hardMaxResponseBytes)
	}
	if manifest.Defaults.Headers == nil {
		return errors.New("defaults.headers is required and must be an object")
	}
	defaultHeaderNames := make(map[string]struct{}, len(manifest.Defaults.Headers))
	for name, value := range manifest.Defaults.Headers {
		if err := validateHeader(name, value); err != nil {
			return fmt.Errorf("defaults.headers: %w", err)
		}
		folded := strings.ToLower(name)
		if _, duplicate := defaultHeaderNames[folded]; duplicate {
			return fmt.Errorf("defaults.headers contains duplicate case-insensitive name %q", name)
		}
		defaultHeaderNames[folded] = struct{}{}
		if err := validateTemplates(value, map[string]struct{}{}); err != nil {
			return fmt.Errorf("defaults.headers[%q]: %w", name, err)
		}
	}
	if len(manifest.Steps) == 0 {
		return errors.New("steps must not be empty")
	}
	stepIDs := make(map[string]struct{})
	captureNames := make(map[string]struct{})
	captureEnvironmentNames := make(map[string]struct{})
	for index := range manifest.Steps {
		step := &manifest.Steps[index]
		prefix := fmt.Sprintf("steps[%d]", index)
		foldedID := strings.ToLower(step.ID)
		if !stepIDPattern.MatchString(step.ID) || step.ID == "." || step.ID == ".." {
			return fmt.Errorf("%s.id is not a safe snapshot identifier", prefix)
		}
		if _, exists := stepIDs[foldedID]; exists {
			return fmt.Errorf("%s.id duplicates another step", prefix)
		}
		stepIDs[foldedID] = struct{}{}
		if len(step.Clients) == 0 {
			return fmt.Errorf("%s.clients must not be empty", prefix)
		}
		for _, client := range step.Clients {
			if strings.TrimSpace(client) == "" {
				return fmt.Errorf("%s.clients contains an empty value", prefix)
			}
		}
		switch step.Status {
		case "required":
			if step.ObservedGap != "" {
				return fmt.Errorf("%s.observedGap is only valid for observed-gap status", prefix)
			}
		case "observed-gap":
			if strings.TrimSpace(step.ObservedGap) == "" {
				return fmt.Errorf("%s.observedGap is required for observed-gap status", prefix)
			}
		default:
			return fmt.Errorf("%s.status must be required or observed-gap", prefix)
		}
		method := strings.ToUpper(step.Request.Method)
		if !isHTTPToken(method) {
			return fmt.Errorf("%s.request.method is invalid", prefix)
		}
		step.Request.Method = method
		if step.Request.Path == "" || !strings.HasPrefix(step.Request.Path, "/") || strings.HasPrefix(step.Request.Path, "//") || strings.Contains(step.Request.Path, "#") {
			return fmt.Errorf("%s.request.path must be an origin-relative path without a fragment", prefix)
		}
		if err := validateTemplates(step.Request.Path, captureNames); err != nil {
			return fmt.Errorf("%s.request.path: %w", prefix, err)
		}
		requestHeaderNames := make(map[string]struct{}, len(step.Request.Headers))
		for name, value := range step.Request.Headers {
			if err := validateHeader(name, value); err != nil {
				return fmt.Errorf("%s.request.headers: %w", prefix, err)
			}
			folded := strings.ToLower(name)
			if _, duplicate := requestHeaderNames[folded]; duplicate {
				return fmt.Errorf("%s.request.headers contains duplicate case-insensitive name %q", prefix, name)
			}
			requestHeaderNames[folded] = struct{}{}
			if err := validateTemplates(value, captureNames); err != nil {
				return fmt.Errorf("%s.request.headers[%q]: %w", prefix, name, err)
			}
		}
		if len(step.Request.JSON) > 0 {
			requestJSON, err := decodeRawJSON(step.Request.JSON)
			if err != nil {
				return fmt.Errorf("%s.request.json: %w", prefix, err)
			}
			if err := walkStrings(requestJSON, func(value string) error {
				return validateTemplates(value, captureNames)
			}); err != nil {
				return fmt.Errorf("%s.request.json: %w", prefix, err)
			}
		}
		if len(step.Expect.Statuses) == 0 {
			return fmt.Errorf("%s.expect.statuses must not be empty", prefix)
		}
		seenStatus := make(map[int]struct{})
		for _, status := range step.Expect.Statuses {
			if status < 100 || status > 599 {
				return fmt.Errorf("%s.expect.statuses contains invalid status %d", prefix, status)
			}
			if _, duplicate := seenStatus[status]; duplicate {
				return fmt.Errorf("%s.expect.statuses contains duplicate %d", prefix, status)
			}
			seenStatus[status] = struct{}{}
		}
		if step.Expect.MaxBytes != nil && (*step.Expect.MaxBytes < 0 || *step.Expect.MaxBytes > manifest.Defaults.MaxResponseBytes) {
			return fmt.Errorf("%s.expect.maxBytes must be between 0 and defaults.maxResponseBytes", prefix)
		}
		for _, contentType := range step.Expect.ContentTypes {
			if _, _, err := mime.ParseMediaType(contentType); err != nil {
				return fmt.Errorf("%s.expect.contentTypes contains invalid media type %q", prefix, contentType)
			}
		}
		if step.Captures == nil {
			return fmt.Errorf("%s.captures is required and must be an array", prefix)
		}
		if step.Canonicalize == nil {
			return fmt.Errorf("%s.canonicalize is required and must be an array", prefix)
		}
		for captureIndex, capture := range step.Captures {
			if !identifierPattern.MatchString(capture.Name) {
				return fmt.Errorf("%s.captures[%d].name is invalid", prefix, captureIndex)
			}
			if _, exists := captureNames[capture.Name]; exists {
				return fmt.Errorf("%s.captures[%d].name duplicates an earlier capture", prefix, captureIndex)
			}
			environmentName := environmentToken(capture.Name)
			if _, exists := captureEnvironmentNames[environmentName]; exists {
				return fmt.Errorf("%s.captures[%d].name collides in environment variables", prefix, captureIndex)
			}
			captureEnvironmentNames[environmentName] = struct{}{}
			if capture.Secret == nil {
				return fmt.Errorf("%s.captures[%d].secret is required and must be a boolean", prefix, captureIndex)
			}
			if _, err := parsePointer(capture.Pointer); err != nil {
				return fmt.Errorf("%s.captures[%d].pointer: %w", prefix, captureIndex, err)
			}
			captureNames[capture.Name] = struct{}{}
		}
		for ruleIndex := range step.Canonicalize {
			rule := &step.Canonicalize[ruleIndex]
			if _, err := parsePointer(rule.Pointer); err != nil {
				return fmt.Errorf("%s.canonicalize[%d].pointer: %w", prefix, ruleIndex, err)
			}
			if strings.TrimSpace(rule.Reason) == "" {
				return fmt.Errorf("%s.canonicalize[%d].reason is required", prefix, ruleIndex)
			}
			switch rule.Op {
			case "replace":
				if len(rule.Value) == 0 {
					return fmt.Errorf("%s.canonicalize[%d].value is required for replace", prefix, ruleIndex)
				}
				var value any
				decoder := json.NewDecoder(bytes.NewReader(rule.Value))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					return fmt.Errorf("%s.canonicalize[%d].value: %w", prefix, ruleIndex, err)
				}
			case "remove", "sort", "normalize-url-host":
				if len(rule.Value) != 0 {
					return fmt.Errorf("%s.canonicalize[%d].value is only valid for replace", prefix, ruleIndex)
				}
			default:
				return fmt.Errorf("%s.canonicalize[%d].op is invalid", prefix, ruleIndex)
			}
		}
		if step.Compare != "exact" && step.Compare != "semantic" && step.Compare != "per-target" {
			return fmt.Errorf("%s.compare must be exact, semantic, or per-target", prefix)
		}
	}
	return nil
}

func validateHeader(name, value string) error {
	if !isHTTPToken(name) {
		return fmt.Errorf("invalid header name %q", name)
	}
	for _, character := range []byte(value) {
		if (character < ' ' && character != '\t') || character == 0x7f {
			return fmt.Errorf("header %q contains an invalid control character", name)
		}
	}
	return nil
}

func isHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
			return false
		}
	}
	return true
}

func validateTemplates(value string, available map[string]struct{}) error {
	for offset := 0; ; {
		start := strings.Index(value[offset:], "{{")
		if start < 0 {
			if strings.Contains(value[offset:], "}}") {
				return errors.New("unmatched template closing delimiter")
			}
			return nil
		}
		start += offset
		if strings.Contains(value[offset:start], "}}") {
			return errors.New("unmatched template closing delimiter")
		}
		endRelative := strings.Index(value[start+2:], "}}")
		if endRelative < 0 {
			return errors.New("unclosed template")
		}
		end := start + 2 + endRelative
		template := value[start+2 : end]
		if strings.HasPrefix(template, "secret:") {
			name := strings.TrimPrefix(template, "secret:")
			if !identifierPattern.MatchString(name) {
				return fmt.Errorf("invalid secret template %q", template)
			}
		} else {
			if !identifierPattern.MatchString(template) {
				return fmt.Errorf("invalid capture template %q", template)
			}
			if _, exists := available[template]; !exists {
				return fmt.Errorf("capture %q is not available from an earlier step", template)
			}
		}
		offset = end + 2
	}
}

func decodeRawJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func walkStrings(value any, visit func(string) error) error {
	switch typed := value.(type) {
	case string:
		return visit(typed)
	case []any:
		for _, item := range typed {
			if err := walkStrings(item, visit); err != nil {
				return err
			}
		}
	case map[string]any:
		for name, item := range typed {
			if err := visit(name); err != nil {
				return err
			}
			if err := walkStrings(item, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func parsePointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("RFC6901 pointer must be empty or start with /")
	}
	raw := strings.Split(pointer[1:], "/")
	result := make([]string, len(raw))
	for index, token := range raw {
		var builder strings.Builder
		for i := 0; i < len(token); i++ {
			if token[i] != '~' {
				builder.WriteByte(token[i])
				continue
			}
			if i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1') {
				return nil, fmt.Errorf("invalid RFC6901 escape in token %q", token)
			}
			i++
			if token[i] == '0' {
				builder.WriteByte('~')
			} else {
				builder.WriteByte('/')
			}
		}
		result[index] = builder.String()
	}
	return result, nil
}

func escapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
