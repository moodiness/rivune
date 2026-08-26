package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/accessibility"
	"github.com/moodiness/rivune/server/internal/auth"
)

type fakeAccessibilityService struct {
	document accessibility.Document
	err      error
	input    accessibility.UpdateInput
	profile  string
}

func (service *fakeAccessibilityService) Get(_ context.Context, _ auth.Principal, profileID string) (accessibility.Document, error) {
	service.profile = profileID
	return service.document, service.err
}

func (service *fakeAccessibilityService) Update(_ context.Context, _ auth.Principal, profileID string, input accessibility.UpdateInput) (accessibility.Document, error) {
	service.profile, service.input = profileID, input
	return service.document, service.err
}

func TestAccessibilityPreferencesReturnsDocumentAndETag(t *testing.T) {
	service := &fakeAccessibilityService{document: accessibility.Document{Revision: 9, Preferences: accessibility.Defaults()}}
	api := testAPI(&fakeInstanceService{})
	api.accessibility = service
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/11111111-1111-4111-8111-111111111111/accessibility-preferences", nil)
	request.SetPathValue("profileId", "11111111-1111-4111-8111-111111111111")
	response := httptest.NewRecorder()

	api.accessibilityPreferences(response, request, auth.Principal{})

	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"9"` {
		t.Fatalf("response status=%d ETag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	if service.profile != "11111111-1111-4111-8111-111111111111" || !bytes.Contains(response.Body.Bytes(), []byte(`"textScale":100`)) {
		t.Fatalf("profile=%q body=%s", service.profile, response.Body.String())
	}
}

func TestUpdateAccessibilityPreferencesStrictlyDecodesAndMapsDomainErrors(t *testing.T) {
	valid := `{"revision":4,"reducedMotion":"reduce","highContrast":"more","textScale":115,"captions":"on","audioDescription":true,"focusIndicators":"enhanced"}`
	for _, test := range []struct {
		name   string
		body   string
		err    error
		status int
		code   string
	}{
		{name: "success", body: valid, status: http.StatusOK},
		{name: "unknown field", body: `{"revision":0,"reducedMotion":"system","highContrast":"system","textScale":100,"captions":"system","audioDescription":false,"focusIndicators":"standard","deviceId":"secret"}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "invalid enum", body: valid, err: accessibility.ErrInvalidInput, status: http.StatusUnprocessableEntity, code: "invalid_accessibility_preferences"},
		{name: "stale", body: valid, err: accessibility.ErrConflict, status: http.StatusConflict, code: "accessibility_preferences_conflict"},
		{name: "wrong profile", body: valid, err: accessibility.ErrActiveProfileRequired, status: http.StatusConflict, code: "profile_selection_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAccessibilityService{document: accessibility.Document{Revision: 5, Preferences: accessibility.Defaults()}, err: test.err}
			api := testAPI(&fakeInstanceService{})
			api.accessibility = service
			request := httptest.NewRequest(http.MethodPut, "/api/v1/profiles/profile/accessibility-preferences", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.SetPathValue("profileId", "profile")
			response := httptest.NewRecorder()

			api.updateAccessibilityPreferences(response, request, auth.Principal{})

			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if test.code != "" && !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+test.code+`"`)) {
				t.Fatalf("response missing code %q: %s", test.code, response.Body.String())
			}
			if test.name == "success" && (service.input.Revision != 4 || service.input.TextScale != 115 || !service.input.AudioDescription) {
				t.Fatalf("decoded input = %+v", service.input)
			}
		})
	}
}
