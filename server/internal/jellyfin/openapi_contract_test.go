package jellyfin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	oasvalidator "github.com/pb33f/libopenapi-validator"

	"github.com/moodiness/rivune/server/internal/watchstate"
)

func TestCompatibilityOpenAPIMatchesEveryRegisteredRoute(t *testing.T) {
	specification, err := os.ReadFile("../../../protocol/jellyfin-compat-openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document, err := libopenapi.NewDocument(specification)
	if err != nil {
		t.Fatalf("parse compatibility OpenAPI: %v", err)
	}
	model, err := document.BuildV3Model()
	if err != nil {
		t.Fatalf("build compatibility OpenAPI: %v", err)
	}
	validator, validationErrors := oasvalidator.NewValidator(document)
	if len(validationErrors) > 0 {
		t.Fatalf("initialize compatibility OpenAPI validator: %v", errors.Join(validationErrors...))
	}
	if valid, validationErrors := validator.ValidateDocument(); !valid {
		joined := make([]error, len(validationErrors))
		for index, validationError := range validationErrors {
			joined[index] = validationError
		}
		t.Fatalf("invalid compatibility OpenAPI: %v", errors.Join(joined...))
	}

	runtimeRoutes := make(map[string]struct{}, len(routeDefinitions))
	for _, definition := range routeDefinitions {
		key := definition.Method + " " + definition.Pattern
		if _, duplicate := runtimeRoutes[key]; duplicate {
			t.Fatalf("duplicate runtime route %s", key)
		}
		runtimeRoutes[key] = struct{}{}
	}

	contractRoutes := make(map[string]struct{}, len(routeDefinitions))
	operationIDs := make(map[string]string, len(routeDefinitions))
	for path, item := range model.Model.Paths.PathItems.FromOldest() {
		for method, operation := range item.GetOperations().FromOldest() {
			key := strings.ToUpper(method) + " " + path
			contractRoutes[key] = struct{}{}
			if operation.OperationId == "" {
				t.Errorf("%s has no operationId", key)
				continue
			}
			if previous, duplicate := operationIDs[operation.OperationId]; duplicate {
				t.Errorf("operationId %q is shared by %s and %s", operation.OperationId, previous, key)
			} else {
				operationIDs[operation.OperationId] = key
			}
		}
	}

	missing := routeSetDifference(runtimeRoutes, contractRoutes)
	extra := routeSetDifference(contractRoutes, runtimeRoutes)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("compatibility OpenAPI route mismatch\nmissing: %v\nextra: %v", missing, extra)
	}
	if len(contractRoutes) != len(routeDefinitions) {
		t.Fatalf("compatibility OpenAPI operations = %d, runtime routes = %d", len(contractRoutes), len(routeDefinitions))
	}
}

func TestImageInfosRuntimeResponseMatchesOpenAPIAndRejectsPrivateFields(t *testing.T) {
	specification, err := os.ReadFile("../../../protocol/jellyfin-compat-openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document, err := libopenapi.NewDocument(specification)
	if err != nil {
		t.Fatalf("parse compatibility OpenAPI: %v", err)
	}
	validator, validationErrors := oasvalidator.NewValidator(document)
	if len(validationErrors) != 0 {
		t.Fatalf("initialize compatibility OpenAPI validator: %v", errors.Join(validationErrors...))
	}
	primaryTag := strings.Repeat("a", 64)
	handler, _, _ := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne: {ID: artworkItemOne, MediaType: "movie", PosterURL: localizedArtworkPrefix + primaryTag},
		},
	}, map[string]string{localizedArtworkPrefix + primaryTag: primaryTag})
	request := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images", nil)
	request.Header.Set("X-Emby-Token", artworkTokenOne)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if valid, failures := validator.ValidateHttpResponse(request, response.Result()); !valid {
		t.Fatalf("runtime image infos violate OpenAPI: %v body=%s", failures, response.Body.String())
	}

	privateResponse := httptest.NewRecorder()
	privateResponse.Header().Set("Content-Type", "application/json; charset=utf-8")
	privateResponse.WriteHeader(http.StatusOK)
	_, _ = privateResponse.WriteString(`[{"ImageType":"Primary","ImageIndex":0,"ImageTag":"` + primaryTag + `","Path":"https://provider.invalid/private?token=secret"}]`)
	privateRequest := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images", nil)
	privateRequest.Header.Set("X-Emby-Token", artworkTokenOne)
	if valid, _ := validator.ValidateHttpResponse(privateRequest, privateResponse.Result()); valid {
		t.Fatal("image infos OpenAPI accepted a private provider Path")
	}
}

func routeSetDifference(left, right map[string]struct{}) []string {
	result := make([]string, 0)
	for route := range left {
		if _, ok := right[route]; !ok {
			result = append(result, route)
		}
	}
	sort.Strings(result)
	return result
}
