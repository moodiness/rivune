package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestBeginDeviceAuthorizationReturnsPairingURLs(t *testing.T) {
	service := &fakeAuthService{deviceAuthorization: auth.DeviceAuthorization{
		DeviceCode: "rivune_dc_secret",
		UserCode:   "ABCD-EFGH",
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
		Interval:   5 * time.Second,
	}}
	api := testAPI(&fakeInstanceService{})
	api.config.PublicURL = "https://media.example"
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-code", bytes.NewBufferString(`{"deviceName":"Living Room","platform":"tvos"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		UserCode                string `json:"userCode"`
		VerificationURI         string `json:"verificationUri"`
		VerificationURIComplete string `json:"verificationUriComplete"`
		IntervalSeconds         int64  `json:"intervalSeconds"`
	}
	decodeResponse(t, response, &body)
	if body.UserCode != "ABCD-EFGH" || body.VerificationURI != "https://media.example/pair" || body.VerificationURIComplete != "https://media.example/pair?code=ABCD-EFGH" || body.IntervalSeconds != 5 {
		t.Fatalf("unexpected pairing response: %+v", body)
	}
}

func TestDeviceAuthorizationPendingUsesStableError(t *testing.T) {
	service := &fakeAuthService{exchangeErr: auth.ErrDeviceAuthorizationPending}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-code/token", bytes.NewBufferString(`{"deviceCode":"rivune_dc_pending"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", response.Code)
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "authorization_pending" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestDeviceAuthorizationSlowDownSetsRetryAfter(t *testing.T) {
	service := &fakeAuthService{exchangeErr: auth.ErrDeviceAuthorizationSlowDown}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-code/token", bytes.NewBufferString(`{"deviceCode":"rivune_dc_pending"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "5" {
		t.Fatalf("expected retryable 429, got %d with Retry-After %q", response.Code, response.Header().Get("Retry-After"))
	}
}

func TestApproveDeviceAuthorizationPassesUserCode(t *testing.T) {
	service := &fakeAuthService{principal: auth.Principal{UserID: "user-id", Role: "member"}}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-code/approve", bytes.NewBufferString(`{"userCode":"ABCD-EFGH"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || service.approvedUserCode != "ABCD-EFGH" {
		t.Fatalf("unexpected approval result: status=%d code=%q", response.Code, service.approvedUserCode)
	}
}
