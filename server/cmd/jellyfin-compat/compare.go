package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const maxComparisonFileBytes int64 = 4*hardMaxResponseBytes + 1<<20

type comparisonSummary struct {
	Version     int                  `json:"version"`
	Compared    int                  `json:"compared"`
	Matched     int                  `json:"matched"`
	Skipped     []string             `json:"skipped"`
	Differences []string             `json:"differences"`
	Mismatches  []comparisonMismatch `json:"mismatches"`
}

type comparisonMismatch struct {
	Step     string `json:"step"`
	Artifact string `json:"artifact"`
	Path     string `json:"path"`
	Left     string `json:"left"`
	Right    string `json:"right"`
}

func compareSnapshots(leftDirectory, rightDirectory, outputDirectory string) (comparisonSummary, error) {
	var summary comparisonSummary
	summary.Version = manifestVersion
	summary.Skipped = make([]string, 0)
	summary.Differences = make([]string, 0)
	summary.Mismatches = make([]comparisonMismatch, 0)
	if leftDirectory == "" || rightDirectory == "" || outputDirectory == "" {
		return summary, errors.New("compare requires -left, -right, and -out")
	}
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		return summary, fmt.Errorf("create comparison output: %w", err)
	}
	if err := os.Chmod(outputDirectory, 0o700); err != nil {
		return summary, fmt.Errorf("secure comparison output: %w", err)
	}
	entries, err := os.ReadDir(outputDirectory)
	if err != nil {
		return summary, fmt.Errorf("read comparison output: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		managed := name == "summary.json" || strings.HasSuffix(name, ".http.diff") || strings.HasSuffix(name, ".json.diff")
		if entry.IsDir() || !managed {
			continue
		}
		if err := os.Remove(filepath.Join(outputDirectory, name)); err != nil {
			return summary, fmt.Errorf("remove stale comparison output: %w", err)
		}
	}
	leftMeta, err := readSnapshotMeta(filepath.Join(leftDirectory, "_meta.json"))
	if err != nil {
		return summary, fmt.Errorf("read left metadata: %w", err)
	}
	rightMeta, err := readSnapshotMeta(filepath.Join(rightDirectory, "_meta.json"))
	if err != nil {
		return summary, fmt.Errorf("read right metadata: %w", err)
	}
	if err := matchingMetadata(leftMeta, rightMeta); err != nil {
		return summary, err
	}
	for _, step := range leftMeta.Steps {
		if step.Compare == "per-target" || step.Skipped == "preseeded" {
			summary.Skipped = append(summary.Skipped, step.ID)
			continue
		}
		summary.Compared++
		stepDifferent := false
		for _, extension := range []string{".http", ".json"} {
			left, err := readBoundedFile(filepath.Join(leftDirectory, step.ID+extension), maxComparisonFileBytes)
			if err != nil {
				return summary, fmt.Errorf("read left snapshot %q: %w", step.ID+extension, err)
			}
			right, err := readBoundedFile(filepath.Join(rightDirectory, step.ID+extension), maxComparisonFileBytes)
			if err != nil {
				return summary, fmt.Errorf("read right snapshot %q: %w", step.ID+extension, err)
			}
			if step.Compare != "semantic" && bytes.Equal(left, right) {
				continue
			}
			var mismatches []comparisonMismatch
			if step.Compare == "semantic" {
				mismatches, err = compareSemanticSnapshots(step.ID, extension, left, right)
				if err != nil {
					return summary, fmt.Errorf("compare semantic snapshot %q: %w", step.ID+extension, err)
				}
			} else {
				mismatches = []comparisonMismatch{{
					Step: step.ID, Artifact: strings.TrimPrefix(extension, "."), Path: "/",
					Left: "<different bytes>", Right: "<different bytes>",
				}}
			}
			if len(mismatches) == 0 {
				continue
			}
			stepDifferent = true
			summary.Mismatches = append(summary.Mismatches, mismatches...)
			var diff []byte
			if step.Compare == "semantic" {
				diff = renderSemanticDiff(step.ID+extension, mismatches)
			} else {
				diff = renderDiff(step.ID+extension, left, right)
			}
			if err := atomicWrite(filepath.Join(outputDirectory, step.ID+extension+".diff"), diff); err != nil {
				return summary, fmt.Errorf("write diff for %q: %w", step.ID+extension, err)
			}
		}
		if stepDifferent {
			summary.Differences = append(summary.Differences, step.ID)
		} else {
			summary.Matched++
		}
	}
	encoded, err := marshalCanonical(summary)
	if err != nil {
		return summary, fmt.Errorf("encode comparison summary: %w", err)
	}
	if err := atomicWrite(filepath.Join(outputDirectory, "summary.json"), encoded); err != nil {
		return summary, fmt.Errorf("write comparison summary: %w", err)
	}
	if len(summary.Differences) > 0 {
		return summary, fmt.Errorf("%d compared step(s) differ", len(summary.Differences))
	}
	return summary, nil
}

func readSnapshotMeta(path string) (snapshotMeta, error) {
	var metadata snapshotMeta
	data, err := readBoundedFile(path, maxManifestBytes)
	if err != nil {
		return metadata, err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return metadata, err
	}
	var raw any
	rawDecoder := json.NewDecoder(bytes.NewReader(data))
	rawDecoder.UseNumber()
	if err := rawDecoder.Decode(&raw); err != nil {
		return metadata, err
	}
	if root, ok := raw.(map[string]any); ok {
		if err := rejectUnknownKeys(root, "", "version", "steps"); err != nil {
			return metadata, err
		}
		if steps, ok := root["steps"].([]any); ok {
			for index, rawStep := range steps {
				if step, ok := rawStep.(map[string]any); ok {
					if err := rejectUnknownKeys(step, "/steps/"+fmt.Sprint(index), "id", "compare", "skipped"); err != nil {
						return metadata, err
					}
					if skipped, present := step["skipped"]; present {
						if value, ok := skipped.(string); !ok || (value != "" && value != "preseeded") {
							return metadata, fmt.Errorf("invalid skipped metadata at step %d", index)
						}
					}
				}
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return metadata, err
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return metadata, err
	}
	if metadata.Version != manifestVersion || len(metadata.Steps) == 0 {
		return metadata, errors.New("invalid snapshot metadata version or empty steps")
	}
	seen := make(map[string]struct{})
	for _, step := range metadata.Steps {
		if !stepIDPattern.MatchString(step.ID) || step.ID == "." || step.ID == ".." {
			return metadata, fmt.Errorf("invalid snapshot step ID %q", step.ID)
		}
		folded := strings.ToLower(step.ID)
		if _, exists := seen[folded]; exists {
			return metadata, fmt.Errorf("duplicate snapshot step ID %q", step.ID)
		}
		seen[folded] = struct{}{}
		if step.Compare != "exact" && step.Compare != "semantic" && step.Compare != "per-target" {
			return metadata, fmt.Errorf("invalid compare mode for step %q", step.ID)
		}
		if step.Skipped != "" && step.Skipped != "preseeded" {
			return metadata, fmt.Errorf("invalid skipped mode for step %q", step.ID)
		}
	}
	return metadata, nil
}

func matchingMetadata(left, right snapshotMeta) error {
	if len(left.Steps) != len(right.Steps) {
		return errors.New("snapshot metadata step counts differ")
	}
	for index := range left.Steps {
		if left.Steps[index] != right.Steps[index] {
			return fmt.Errorf("snapshot metadata differs at step %d", index)
		}
	}
	return nil
}

func renderDiff(name string, left, right []byte) []byte {
	leftLines := splitLines(left)
	rightLines := splitLines(right)
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- left/%s\n+++ right/%s\n", name, name)
	for _, line := range leftLines {
		builder.WriteByte('-')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	for _, line := range rightLines {
		builder.WriteByte('+')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}

type semanticHTTP struct {
	Status  int
	Headers map[string][]string
}

func compareSemanticSnapshots(step, extension string, left, right []byte) ([]comparisonMismatch, error) {
	artifact := strings.TrimPrefix(extension, ".")
	switch extension {
	case ".json":
		leftValue, err := decodeComparisonJSON(left)
		if err != nil {
			return nil, fmt.Errorf("left JSON: %w", err)
		}
		rightValue, err := decodeComparisonJSON(right)
		if err != nil {
			return nil, fmt.Errorf("right JSON: %w", err)
		}
		mismatches := make([]comparisonMismatch, 0)
		compareJSONValue(step, artifact, "", leftValue, rightValue, &mismatches)
		return mismatches, nil
	case ".http":
		leftHTTP, err := parseSemanticHTTP(left)
		if err != nil {
			return nil, fmt.Errorf("left HTTP: %w", err)
		}
		rightHTTP, err := parseSemanticHTTP(right)
		if err != nil {
			return nil, fmt.Errorf("right HTTP: %w", err)
		}
		return compareHTTPValue(step, artifact, leftHTTP, rightHTTP), nil
	default:
		return nil, fmt.Errorf("unsupported artifact extension %q", extension)
	}
}

func decodeComparisonJSON(data []byte) (any, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
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

func compareJSONValue(step, artifact, path string, left, right any, mismatches *[]comparisonMismatch) {
	leftObject, leftIsObject := left.(map[string]any)
	rightObject, rightIsObject := right.(map[string]any)
	if leftIsObject && rightIsObject {
		keys := make([]string, 0, len(leftObject)+len(rightObject))
		seen := make(map[string]struct{}, len(leftObject)+len(rightObject))
		for key := range leftObject {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
		for key := range rightObject {
			if _, exists := seen[key]; !exists {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := path + "/" + escapePointerToken(key)
			leftChild, leftExists := leftObject[key]
			rightChild, rightExists := rightObject[key]
			if !leftExists {
				appendMismatch(mismatches, step, artifact, childPath, "<missing>", displayComparisonValue(rightChild))
			} else if !rightExists {
				appendMismatch(mismatches, step, artifact, childPath, displayComparisonValue(leftChild), "<missing>")
			} else {
				compareJSONValue(step, artifact, childPath, leftChild, rightChild, mismatches)
			}
		}
		return
	}
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if leftIsArray && rightIsArray {
		common := len(leftArray)
		if len(rightArray) < common {
			common = len(rightArray)
		}
		for index := range common {
			compareJSONValue(step, artifact, path+"/"+strconv.Itoa(index), leftArray[index], rightArray[index], mismatches)
		}
		for index := common; index < len(leftArray); index++ {
			appendMismatch(mismatches, step, artifact, path+"/"+strconv.Itoa(index), displayComparisonValue(leftArray[index]), "<missing>")
		}
		for index := common; index < len(rightArray); index++ {
			appendMismatch(mismatches, step, artifact, path+"/"+strconv.Itoa(index), "<missing>", displayComparisonValue(rightArray[index]))
		}
		return
	}
	if leftIsObject != rightIsObject || leftIsArray != rightIsArray || !comparisonScalarEqual(left, right) {
		appendMismatch(mismatches, step, artifact, comparisonPath(path), displayComparisonValue(left), displayComparisonValue(right))
	}
}

func comparisonScalarEqual(left, right any) bool {
	leftNumber, leftIsNumber := left.(json.Number)
	rightNumber, rightIsNumber := right.(json.Number)
	if leftIsNumber && rightIsNumber {
		leftRational, leftOK := new(big.Rat).SetString(leftNumber.String())
		rightRational, rightOK := new(big.Rat).SetString(rightNumber.String())
		return leftOK && rightOK && leftRational.Cmp(rightRational) == 0
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func displayComparisonValue(value any) string {
	switch value.(type) {
	case map[string]any:
		return "<object>"
	case []any:
		return "<array>"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<unrepresentable>"
	}
	return string(encoded)
}

func parseSemanticHTTP(data []byte) (semanticHTTP, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return semanticHTTP{}, errors.New("empty snapshot")
	}
	statusFields := strings.Fields(lines[0])
	if len(statusFields) < 2 || statusFields[0] != "HTTP/1.1" {
		return semanticHTTP{}, errors.New("invalid status line")
	}
	status, err := strconv.Atoi(statusFields[1])
	if err != nil || status < 100 || status > 599 {
		return semanticHTTP{}, errors.New("invalid status code")
	}
	result := semanticHTTP{Status: status, Headers: make(map[string][]string)}
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if !found || !isHTTPToken(name) {
			return semanticHTTP{}, fmt.Errorf("invalid header line %q", line)
		}
		canonicalName := textproto.CanonicalMIMEHeaderKey(name)
		result.Headers[canonicalName] = append(result.Headers[canonicalName], strings.TrimSpace(value))
	}
	for name := range result.Headers {
		sort.Strings(result.Headers[name])
	}
	return result, nil
}

func compareHTTPValue(step, artifact string, left, right semanticHTTP) []comparisonMismatch {
	mismatches := make([]comparisonMismatch, 0)
	if left.Status != right.Status {
		appendMismatch(&mismatches, step, artifact, "/status", strconv.Itoa(left.Status), strconv.Itoa(right.Status))
	}
	names := make([]string, 0, len(left.Headers)+len(right.Headers))
	seen := make(map[string]struct{}, len(left.Headers)+len(right.Headers))
	for name := range left.Headers {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for name := range right.Headers {
		if _, exists := seen[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := "/headers/" + escapePointerToken(name)
		leftValues, leftExists := left.Headers[name]
		rightValues, rightExists := right.Headers[name]
		if !leftExists {
			appendMismatch(&mismatches, step, artifact, path, "<missing>", displayComparisonValue(rightValues))
			continue
		}
		if !rightExists {
			appendMismatch(&mismatches, step, artifact, path, displayComparisonValue(leftValues), "<missing>")
			continue
		}
		common := len(leftValues)
		if len(rightValues) < common {
			common = len(rightValues)
		}
		for index := range common {
			if leftValues[index] != rightValues[index] {
				appendMismatch(&mismatches, step, artifact, path+"/"+strconv.Itoa(index), strconv.Quote(leftValues[index]), strconv.Quote(rightValues[index]))
			}
		}
		for index := common; index < len(leftValues); index++ {
			appendMismatch(&mismatches, step, artifact, path+"/"+strconv.Itoa(index), strconv.Quote(leftValues[index]), "<missing>")
		}
		for index := common; index < len(rightValues); index++ {
			appendMismatch(&mismatches, step, artifact, path+"/"+strconv.Itoa(index), "<missing>", strconv.Quote(rightValues[index]))
		}
	}
	return mismatches
}

func appendMismatch(mismatches *[]comparisonMismatch, step, artifact, path, left, right string) {
	*mismatches = append(*mismatches, comparisonMismatch{
		Step: step, Artifact: artifact, Path: comparisonPath(path), Left: left, Right: right,
	})
}

func comparisonPath(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func renderSemanticDiff(name string, mismatches []comparisonMismatch) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- left/%s\n+++ right/%s\n", name, name)
	for _, mismatch := range mismatches {
		fmt.Fprintf(&builder, "@@ %s @@\n-%s\n+%s\n", mismatch.Path, mismatch.Left, mismatch.Right)
	}
	return []byte(builder.String())
}

func splitLines(data []byte) []string {
	trimmed := strings.TrimSuffix(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d byte limit", limit)
	}
	return data, nil
}
