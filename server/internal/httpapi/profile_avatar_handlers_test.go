package httpapi

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/profile"
)

type observedAvatarRequestBody struct {
	reads int
}

func (body *observedAvatarRequestBody) Read([]byte) (int, error) {
	body.reads++
	return 0, io.EOF
}

func TestProfileAvatarPresetCatalogAndImage(t *testing.T) {
	api := authenticatedProfileAPI(&fakeProfileService{})
	catalogRequest := httptest.NewRequest(http.MethodGet, "/api/v1/profile-avatars", nil)
	catalogRequest.Header.Set("Authorization", "Bearer access-token")
	catalogResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("expected preset catalog status 200, got %d", catalogResponse.Code)
	}
	var catalog struct {
		Presets []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"presets"`
	}
	decodeResponse(t, catalogResponse, &catalog)
	if len(catalog.Presets) < 16 || catalog.Presets[0].ID != "aurora" || catalog.Presets[0].URL != "/api/v1/profile-avatars/aurora" {
		t.Fatalf("unexpected preset catalog: %+v", catalog.Presets)
	}

	imageRequest := httptest.NewRequest(http.MethodGet, catalog.Presets[0].URL, nil)
	imageResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(imageResponse, imageRequest)
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" || !strings.HasPrefix(imageResponse.Body.String(), "<svg") {
		t.Fatalf("unexpected preset image response: status=%d type=%q body=%q", imageResponse.Code, imageResponse.Header().Get("Content-Type"), imageResponse.Body.String())
	}
	if imageResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected preset cache policy: %q", imageResponse.Header().Get("Cache-Control"))
	}
}

func TestSetProfileAvatarPresetReturnsUpdatedProfile(t *testing.T) {
	service := &fakeProfileService{avatarPresetValue: profile.Profile{
		ID: "profile-id", Name: "Main", CanManage: true, AvatarKind: "preset", AvatarPreset: "violet",
	}}
	api := authenticatedProfileAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/profiles/profile-id/avatar/preset", bytes.NewBufferString(`{"presetId":"violet"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.updatedID != "profile-id" || service.avatarPresetID != "violet" {
		t.Fatalf("unexpected preset update: id=%q preset=%q", service.updatedID, service.avatarPresetID)
	}
	var body profileResponse
	decodeResponse(t, response, &body)
	if body.Avatar.Kind != "preset" || body.Avatar.PresetID != "violet" || body.Avatar.URL != "/api/v1/profile-avatars/violet" {
		t.Fatalf("unexpected avatar response: %+v", body.Avatar)
	}
}

func TestUploadAndReadCustomProfileAvatar(t *testing.T) {
	input := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			input.SetNRGBA(x, y, color.NRGBA{R: 40, G: 90, B: 220, A: 255})
		}
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, input); err != nil {
		t.Fatalf("encode avatar: %v", err)
	}
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	part, err := writer.CreateFormFile("image", "avatar.png")
	if err != nil {
		t.Fatalf("create image form part: %v", err)
	}
	if _, err := part.Write(imageBytes.Bytes()); err != nil {
		t.Fatalf("write image form part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	service := &fakeProfileService{avatarImageValue: profile.Profile{
		ID: "profile-id", Name: "Main", CanManage: true, AvatarKind: "custom", AvatarPreset: "aurora",
	}}
	api := authenticatedProfileAPI(service)
	uploadRequest := httptest.NewRequest(http.MethodPut, "/api/v1/profiles/profile-id/avatar", &requestBody)
	uploadRequest.Header.Set("Authorization", "Bearer access-token")
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("expected upload status 200, got %d: %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if service.avatarAuthorizationID != "profile-id" {
		t.Fatalf("avatar upload preflight profile = %q, want profile-id", service.avatarAuthorizationID)
	}
	if service.updatedID != "profile-id" || !bytes.Equal(service.avatarImageData, imageBytes.Bytes()) {
		t.Fatal("uploaded image was not passed to the profile service")
	}
	var profileBody profileResponse
	decodeResponse(t, uploadResponse, &profileBody)
	if profileBody.Avatar.Kind != "custom" || profileBody.Avatar.PresetID != "" || profileBody.Avatar.URL != "/api/v1/profiles/profile-id/avatar" {
		t.Fatalf("unexpected custom avatar response: %+v", profileBody.Avatar)
	}

	updatedAt := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	service.avatarValue = profile.AvatarImage{ContentType: "image/png", Data: []byte("normalized-png"), UpdatedAt: updatedAt}
	readRequest := httptest.NewRequest(http.MethodGet, "/api/v1/profiles/profile-id/avatar", nil)
	readRequest.Header.Set("Authorization", "Bearer access-token")
	readResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK || readResponse.Header().Get("Content-Type") != "image/png" || readResponse.Body.String() != "normalized-png" {
		t.Fatalf("unexpected custom avatar response: status=%d type=%q body=%q", readResponse.Code, readResponse.Header().Get("Content-Type"), readResponse.Body.String())
	}
	if readResponse.Header().Get("Last-Modified") != updatedAt.Format(http.TimeFormat) {
		t.Fatalf("unexpected Last-Modified: %q", readResponse.Header().Get("Last-Modified"))
	}
}

func TestUploadProfileAvatarRejectsNonManagerBeforeReadingBody(t *testing.T) {
	service := &fakeProfileService{avatarAuthorizationErr: profile.ErrNotFound}
	api := authenticatedProfileAPI(service)
	requestBody := &observedAvatarRequestBody{}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/profiles/profile-id/avatar", requestBody)
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "multipart/form-data; boundary=unread")
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected opaque status 404, got %d: %s", response.Code, response.Body.String())
	}
	if service.avatarAuthorizationID != "profile-id" {
		t.Fatalf("avatar upload preflight profile = %q, want profile-id", service.avatarAuthorizationID)
	}
	if requestBody.reads != 0 {
		t.Fatalf("unauthorized avatar request body reads = %d, want zero", requestBody.reads)
	}
	if service.updatedID != "" || service.avatarImageData != nil {
		t.Fatal("unauthorized avatar reached normalization/persistence service path")
	}
}

func TestAvatarNormalizationCapacityErrorIsRetryable(t *testing.T) {
	api := &API{}
	response := httptest.NewRecorder()

	api.writeProfileAvatarError(response, "normalize profile avatar", profile.ErrAvatarNormalizationBusy)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", response.Header().Get("Retry-After"))
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "avatar_processing_busy" {
		t.Fatalf("error code = %q, want avatar_processing_busy", body.Error.Code)
	}
}

func TestProfileAvatarValidationErrorsAreStable(t *testing.T) {
	service := &fakeProfileService{avatarPresetErr: profile.ErrInvalidInput}
	api := authenticatedProfileAPI(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/profiles/profile-id/avatar/preset", bytes.NewBufferString(`{"presetId":"missing"}`))
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d", response.Code)
	}
	var body errorEnvelope
	decodeResponse(t, response, &body)
	if body.Error.Code != "invalid_profile_avatar" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}
