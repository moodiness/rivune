package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func catalogPayloadWithItems(count int) []byte {
	var payload bytes.Buffer
	payload.Grow(len(`{"metas":[]}`) + count*3)
	payload.WriteString(`{"metas":[`)
	for index := range count {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{}`)
	}
	payload.WriteString(`]}`)
	return payload.Bytes()
}

func nestedCatalogPayload(depth int) []byte {
	var payload strings.Builder
	payload.WriteString(`{"metas":[`)
	for level := 2; level < depth; level++ {
		payload.WriteString(`{"x":`)
	}
	payload.WriteString(`null`)
	for level := 2; level < depth; level++ {
		payload.WriteByte('}')
	}
	payload.WriteString(`]}`)
	return []byte(payload.String())
}

func catalogObjectWithFields(count int) []byte {
	var object strings.Builder
	object.WriteByte('{')
	for index := range count {
		if index > 0 {
			object.WriteByte(',')
		}
		object.WriteString(`"x":0`)
	}
	object.WriteByte('}')
	return []byte(`{"metas":[` + object.String() + `]}`)
}

func jsonObjectOfSize(size int) string {
	const fields = 4
	const empty = `{"a":"","b":"","c":"","d":""}`
	remaining := size - len(empty)
	if remaining < 0 {
		panic("requested JSON object is smaller than its structure")
	}
	values := make([]string, fields)
	for index := range values {
		share := remaining / (fields - index)
		values[index] = strings.Repeat("x", share)
		remaining -= share
	}
	return `{"a":"` + values[0] + `","b":"` + values[1] + `","c":"` + values[2] + `","d":"` + values[3] + `"}`
}

func TestCatalogComplexityItemBoundaryIsAtomic(t *testing.T) {
	exact := catalogPayloadWithItems(MaximumCatalogItems)
	for _, resource := range []string{"catalog", "addon_catalog"} {
		if err := validateResourceResponse(resource, exact); err != nil {
			t.Fatalf("%s response at item limit: %v", resource, err)
		}
	}
	if safe, err := SanitizeExposablePayload(exact); err != nil || len(safe) == 0 {
		t.Fatalf("sanitize response at item limit: bytes=%d error=%v", len(safe), err)
	}

	overLimit := catalogPayloadWithItems(MaximumCatalogItems + 1)
	for _, resource := range []string{"catalog", "addon_catalog"} {
		if err := validateResourceResponse(resource, overLimit); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("%s item N+1 error = %v", resource, err)
		}
	}
	if safe, err := SanitizeExposablePayload(overLimit); !errors.Is(err, ErrInvalidResponse) || safe != nil {
		t.Fatalf("sanitize item N+1 = bytes %d, error %v", len(safe), err)
	}
}

func TestCatalogComplexityProductionBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		exact []byte
		over  []byte
	}{
		{
			name:  "depth",
			exact: nestedCatalogPayload(maximumExposableJSONDepth),
			over:  nestedCatalogPayload(maximumExposableJSONDepth + 1),
		},
		{
			name:  "object fields",
			exact: catalogObjectWithFields(maximumExposableJSONObjectFields),
			over:  catalogObjectWithFields(maximumExposableJSONObjectFields + 1),
		},
		{
			name:  "string bytes",
			exact: []byte(`{"metas":[{"id":"` + strings.Repeat("x", maximumExposableJSONStringBytes) + `"}]}`),
			over:  []byte(`{"metas":[{"id":"` + strings.Repeat("x", maximumExposableJSONStringBytes+1) + `"}]}`),
		},
		{
			name:  "array element bytes",
			exact: []byte(`{"metas":[` + jsonObjectOfSize(maximumExposableJSONArrayElementBytes) + `]}`),
			over:  []byte(`{"metas":[` + jsonObjectOfSize(maximumExposableJSONArrayElementBytes+1) + `]}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateResourceResponse("catalog", test.exact); err != nil {
				t.Fatalf("exact limit response: %v", err)
			}
			if err := validateResourceResponse("catalog", test.over); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("N+1 response error = %v", err)
			}
		})
	}
}

func TestExposablePayloadAggregateBudgetsExactAndNPlusOne(t *testing.T) {
	base := exposableJSONPolicy{
		maximumDepth:                8,
		maximumNodes:                32,
		maximumObjectFields:         8,
		maximumArrayItems:           8,
		maximumStringBytes:          16,
		maximumAggregateStringBytes: 16,
		maximumNumberBytes:          16,
		maximumArrayElementBytes:    64,
	}
	tests := []struct {
		name   string
		policy exposableJSONPolicy
		exact  string
		over   string
	}{
		{name: "nodes", policy: func() exposableJSONPolicy { value := base; value.maximumNodes = 3; return value }(), exact: `[0,0]`, over: `[0,0,0]`},
		{name: "depth", policy: func() exposableJSONPolicy { value := base; value.maximumDepth = 2; return value }(), exact: `[[0]]`, over: `[[[0]]]`},
		{name: "object fields", policy: func() exposableJSONPolicy { value := base; value.maximumObjectFields = 2; return value }(), exact: `{"a":0,"b":0}`, over: `{"a":0,"b":0,"c":0}`},
		{name: "array items", policy: func() exposableJSONPolicy { value := base; value.maximumArrayItems = 2; return value }(), exact: `[0,0]`, over: `[0,0,0]`},
		{name: "string bytes", policy: func() exposableJSONPolicy { value := base; value.maximumStringBytes = 4; return value }(), exact: `"abcd"`, over: `"abcde"`},
		{name: "aggregate string bytes", policy: func() exposableJSONPolicy { value := base; value.maximumAggregateStringBytes = 8; return value }(), exact: `["abcd","efgh"]`, over: `["abcd","efghi"]`},
		{name: "number bytes", policy: func() exposableJSONPolicy { value := base; value.maximumNumberBytes = 4; return value }(), exact: `1000`, over: `10000`},
		{name: "array element bytes", policy: func() exposableJSONPolicy { value := base; value.maximumArrayElementBytes = 6; return value }(), exact: `["abcd"]`, over: `["abcde"]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateExposablePayloadComplexityWithPolicy(context.Background(), []byte(test.exact), test.policy); err != nil {
				t.Fatalf("exact budget: %v", err)
			}
			if err := validateExposablePayloadComplexityWithPolicy(context.Background(), []byte(test.over), test.policy); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("N+1 budget error = %v", err)
			}
		})
	}
}

func TestExposablePayloadPreflightCancellationAndOpaqueErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateExposablePayloadComplexity(ctx, catalogPayloadWithItems(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preflight error = %v", err)
	}

	secret := "https://provider.example/private?token=do-not-echo"
	hostile := []byte(`{"metas":[{"label":"` + secret + `","nested":` + string(nestedCatalogPayload(maximumExposableJSONDepth+1)) + `}]}`)
	if err := validateExposablePayloadComplexity(context.Background(), hostile); !errors.Is(err, ErrInvalidResponse) || strings.Contains(err.Error(), secret) {
		t.Fatalf("hostile payload error = %v", err)
	}
}

func TestSanitizeExposablePayloadRejectsMalformedInputAtomically(t *testing.T) {
	for _, payload := range []json.RawMessage{
		nil,
		json.RawMessage(`[`),
		json.RawMessage(`{"metas":[{"x":`),
		json.RawMessage("{\"metas\":[\"\\q\"]}"),
		json.RawMessage(`{"metas":[1,]}`),
	} {
		safe, err := SanitizeExposablePayload(payload)
		if !errors.Is(err, ErrInvalidResponse) || safe != nil {
			t.Fatalf("malformed payload %q = bytes %d, error %v", payload, len(safe), err)
		}
	}
}
