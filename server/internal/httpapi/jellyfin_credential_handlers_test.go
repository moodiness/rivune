package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/jellyfin"
)

const credentialHandlerProfileID = "e1000000-0000-4000-8000-000000000001"

type fakeJellyfinCredentialService struct {
	status     jellyfin.CredentialStatus
	credential jellyfin.ProfileCredential
	err        error
	operation  string
	principal  auth.Principal
	profileID  string
}

func (f *fakeJellyfinCredentialService) Status(_ context.Context, principal auth.Principal, profileID string) (jellyfin.CredentialStatus, error) {
	f.operation, f.principal, f.profileID = "status", principal, profileID
	return f.status, f.err
}

func (f *fakeJellyfinCredentialService) Create(_ context.Context, principal auth.Principal, profileID string) (jellyfin.ProfileCredential, error) {
	f.operation, f.principal, f.profileID = "create", principal, profileID
	return f.credential, f.err
}

func (f *fakeJellyfinCredentialService) Rotate(_ context.Context, principal auth.Principal, profileID string) (jellyfin.ProfileCredential, error) {
	f.operation, f.principal, f.profileID = "rotate", principal, profileID
	return f.credential, f.err
}

func (f *fakeJellyfinCredentialService) Revoke(_ context.Context, principal auth.Principal, profileID string) error {
	f.operation, f.principal, f.profileID = "revoke", principal, profileID
	return f.err
}

func TestJellyfinCredentialStatusNeverReturnsPasswordAndRetainsRevokedUsername(t *testing.T) {
	service := &fakeJellyfinCredentialService{}
	api := credentialHandlerAPI(service)

	before := serveCredentialRequest(api, http.MethodGet, "/api/v1/profiles/"+credentialHandlerProfileID+"/jellyfin-credential", "")
	if before.Code != http.StatusOK || strings.Contains(before.Body.String(), "username") || strings.Contains(before.Body.String(), "password") || before.Body.String() != "{\"active\":false,\"canIssue\":false,\"generation\":0}\n" {
		t.Fatalf("never-created credential status = %d %s", before.Code, before.Body.String())
	}

	revokedAt := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	service.status = jellyfin.CredentialStatus{
		Username: credentialHandlerProfileID, CanIssue: true, Generation: 3, CreatedAt: revokedAt.Add(-time.Hour), RevokedAt: &revokedAt,
	}
	revoked := serveCredentialRequest(api, http.MethodGet, "/api/v1/profiles/"+credentialHandlerProfileID+"/jellyfin-credential", "")
	body := revoked.Body.String()
	if revoked.Code != http.StatusOK || !strings.Contains(body, `"username":"`+credentialHandlerProfileID+`"`) || !strings.Contains(body, `"active":false`) || !strings.Contains(body, `"canIssue":true`) || strings.Contains(body, "password") {
		t.Fatalf("revoked credential status = %d %s", revoked.Code, body)
	}
	if service.operation != "status" || service.profileID != credentialHandlerProfileID || service.principal.UserID != "credential-manager" {
		t.Fatalf("status authorization context = operation %q profile %q principal %+v", service.operation, service.profileID, service.principal)
	}
}

func TestJellyfinCredentialCreateAndRotateReturnOneShotNoStoreSecret(t *testing.T) {
	createdAt := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	service := &fakeJellyfinCredentialService{credential: jellyfin.ProfileCredential{
		CredentialStatus: jellyfin.CredentialStatus{
			Username: credentialHandlerProfileID, Active: true, CanIssue: true, Generation: 1, CreatedAt: createdAt,
		},
		Password: "rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}}
	api := credentialHandlerAPI(service)

	for _, test := range []struct {
		name, path, operation string
		status                int
	}{
		{name: "create", path: "/api/v1/profiles/" + credentialHandlerProfileID + "/jellyfin-credential", operation: "create", status: http.StatusCreated},
		{name: "rotate", path: "/api/v1/profiles/" + credentialHandlerProfileID + "/jellyfin-credential/rotate", operation: "rotate", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveCredentialRequest(api, http.MethodPost, test.path, "")
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" || service.operation != test.operation {
				t.Fatalf("%s response = status %d cache %q operation %q body %s", test.name, response.Code, response.Header().Get("Cache-Control"), service.operation, response.Body.String())
			}
			body := response.Body.String()
			if !strings.Contains(body, `"username":"`+credentialHandlerProfileID+`"`) || !strings.Contains(body, `"password":"rivune_jfa_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`) || !strings.Contains(body, `"active":true`) || !strings.Contains(body, `"canIssue":true`) {
				t.Fatalf("%s response omitted one-shot credential: %s", test.name, body)
			}
		})
	}
}

func TestJellyfinCredentialRevokeAndErrorsUseStableContracts(t *testing.T) {
	service := &fakeJellyfinCredentialService{}
	api := credentialHandlerAPI(service)
	path := "/api/v1/profiles/" + credentialHandlerProfileID + "/jellyfin-credential"

	revoked := serveCredentialRequest(api, http.MethodDelete, path, "")
	if revoked.Code != http.StatusNoContent || service.operation != "revoke" {
		t.Fatalf("revoke response = %d operation %q body %s", revoked.Code, service.operation, revoked.Body.String())
	}

	for _, test := range []struct {
		err    error
		method string
		path   string
		status int
		code   string
	}{
		{err: jellyfin.ErrCredentialForbidden, method: http.MethodGet, path: path, status: http.StatusForbidden, code: "jellyfin_credential_forbidden"},
		{err: jellyfin.ErrCredentialNotFound, method: http.MethodPost, path: path + "/rotate", status: http.StatusNotFound, code: "jellyfin_credential_not_found"},
		{err: jellyfin.ErrCredentialExists, method: http.MethodPost, path: path, status: http.StatusConflict, code: "jellyfin_credential_exists"},
	} {
		service.err = test.err
		response := serveCredentialRequest(api, test.method, test.path, "")
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("error %v response = %d %s", test.err, response.Code, response.Body.String())
		}
	}

	service.err = nil
	invalidBody := serveCredentialRequest(api, http.MethodPost, path, `{}`)
	if invalidBody.Code != http.StatusBadRequest || !strings.Contains(invalidBody.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("non-empty create body = %d %s", invalidBody.Code, invalidBody.Body.String())
	}
}

func credentialHandlerAPI(service jellyfinCredentialService) *API {
	api := testAPI(&fakeInstanceService{})
	api.jellyfinCredentials = service
	api.auth = &fakeAuthService{principal: auth.Principal{
		UserID: "credential-manager", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}}
	return api
}

func serveCredentialRequest(api *API, method, path, body string) *httptest.ResponseRecorder {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}
