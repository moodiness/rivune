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

func TestDeviceAuthorizationExchangeRejectsCategoryClaims(t *testing.T) {
	service := &fakeAuthService{}
	api := testAPI(&fakeInstanceService{})
	api.auth = service
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/device-code/token",
		bytes.NewBufferString(`{"deviceCode":"rivune_dc_pending","categoryId":"attacker-category"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected category claim to be rejected with 400, got %d: %s", response.Code, response.Body.String())
	}
	if service.exchangedDeviceCode != "" {
		t.Fatal("untrusted category payload reached device authorization exchange")
	}
}

func TestApproveDeviceAuthorizationDistinguishesDeviceNamePresence(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantStatus     int
		wantDeviceName string
	}{
		{
			name:           "value",
			body:           `{"userCode":"ABCD-EFGH","categoryId":"category-id","deviceName":"Cinema","internalNote":"Projector"}`,
			wantStatus:     http.StatusNoContent,
			wantDeviceName: "Cinema",
		},
		{
			name:       "omitted",
			body:       `{"userCode":"ABCD-EFGH","categoryId":"category-id","internalNote":"Projector"}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "null",
			body:       `{"userCode":"ABCD-EFGH","categoryId":"category-id","deviceName":null,"internalNote":"Projector"}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAuthService{principal: auth.Principal{UserID: "user-id", Role: "member"}}
			api := testAPI(&fakeInstanceService{})
			api.auth = service
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/device-code/approve",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			if test.name == "null" {
				if service.approvalInput.UserCode != "" {
					t.Fatal("explicit null deviceName reached the authorization service")
				}
				var body errorEnvelope
				decodeResponse(t, response, &body)
				if body.Error.Code != "invalid_device_approval" || body.Error.Message != "deviceName cannot be null" {
					t.Fatalf("unexpected error response: %+v", body.Error)
				}
				return
			}
			input := service.approvalInput
			if input.UserCode != "ABCD-EFGH" || input.CategoryID != "category-id" ||
				input.InternalNote == nil || *input.InternalNote != "Projector" {
				t.Fatalf("unexpected approval input: %+v", input)
			}
			if test.wantDeviceName == "" {
				if input.DeviceName != nil {
					t.Fatalf("omitted deviceName became %+v", input.DeviceName)
				}
				return
			}
			if input.DeviceName == nil || *input.DeviceName != test.wantDeviceName {
				t.Fatalf("unexpected deviceName service input: %+v", input.DeviceName)
			}
		})
	}
}
