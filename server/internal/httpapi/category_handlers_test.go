package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/category"
)

type fakeCategoryService struct {
	categories  []category.Category
	devices     []category.Device
	createErr   error
	deleteErr   error
	moveErr     error
	updateInput category.UpdateInput
	updateCalls int
}

func (service *fakeCategoryService) List(context.Context, category.Actor) ([]category.Category, error) {
	return service.categories, nil
}
func (service *fakeCategoryService) Create(context.Context, category.Actor, category.CreateInput) (category.Category, error) {
	return category.Category{}, service.createErr
}
func (service *fakeCategoryService) Update(_ context.Context, _ category.Actor, _ string, input category.UpdateInput) (category.Category, error) {
	service.updateInput = input
	service.updateCalls++
	return category.Category{ID: "01234567-89ab-cdef-0123-456789abcdef", Name: "Studio", IsDefault: input.MakeDefault}, nil
}
func (service *fakeCategoryService) Delete(context.Context, category.Actor, string, *string) error {
	return service.deleteErr
}
func (service *fakeCategoryService) Reorder(context.Context, category.Actor, []string) ([]category.Category, error) {
	return service.categories, nil
}
func (*fakeCategoryService) MoveProfile(context.Context, category.Actor, string, string) error {
	return nil
}
func (service *fakeCategoryService) MoveProfiles(context.Context, category.Actor, []string, string) error {
	return service.moveErr
}
func (service *fakeCategoryService) ListDevices(context.Context, category.Actor, *string) ([]category.Device, error) {
	return service.devices, nil
}
func (*fakeCategoryService) UpdateDevice(context.Context, category.Actor, string, category.DeviceUpdateInput) (category.Device, error) {
	return category.Device{}, nil
}
func (*fakeCategoryService) MoveDevice(context.Context, category.Actor, string, string) error {
	return nil
}
func (service *fakeCategoryService) MoveDevices(context.Context, category.Actor, []string, string) error {
	return service.moveErr
}

func TestListCategoriesIncludesCountsAndNullableIdentityFields(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	service := &fakeCategoryService{categories: []category.Category{{
		ID: "01234567-89ab-cdef-0123-456789abcdef", Name: "Uncategorized", Position: 0,
		IsDefault: true, ProfileCount: 3, DeviceCount: 2, CreatedAt: now, UpdatedAt: now,
	}}}
	api := &API{categories: service}
	response := httptest.NewRecorder()
	api.listCategories(response, httptest.NewRequest("GET", "/api/v1/categories", nil), globalAdminPrincipal())
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{`"profileCount":3`, `"deviceCount":2`, `"color":null`, `"icon":null`, `"isDefault":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("category response missing %s: %s", expected, body)
		}
	}
}

func TestCreateCategoryMapsNormalizedNameConflict(t *testing.T) {
	api := &API{categories: &fakeCategoryService{createErr: category.ErrConflict}}
	request := httptest.NewRequest("POST", "/api/v1/categories", strings.NewReader(`{"name":"clients"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.createCategory(response, request, globalAdminPrincipal())
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":"category_name_conflict"`) {
		t.Fatalf("unexpected conflict response %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdateCategoryOnlyAcceptsTrueDefaultPromotion(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantCode  int
		wantCalls int
	}{
		{name: "promote", body: `{"isDefault":true}`, wantCode: 200, wantCalls: 1},
		{name: "false", body: `{"isDefault":false}`, wantCode: 422},
		{name: "null", body: `{"isDefault":null}`, wantCode: 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeCategoryService{}
			api := &API{categories: service}
			request := httptest.NewRequest("PATCH", "/api/v1/categories/01234567-89ab-cdef-0123-456789abcdef", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.SetPathValue("categoryId", "01234567-89ab-cdef-0123-456789abcdef")
			response := httptest.NewRecorder()

			api.updateCategory(response, request, globalAdminPrincipal())

			if response.Code != test.wantCode || service.updateCalls != test.wantCalls {
				t.Fatalf("unexpected default promotion result: status=%d calls=%d body=%s", response.Code, service.updateCalls, response.Body.String())
			}
			if test.wantCalls == 1 && !service.updateInput.MakeDefault {
				t.Fatal("default promotion did not reach the category service")
			}
		})
	}
}

func TestDeleteDefaultCategoryReturnsImmutableConflict(t *testing.T) {
	api := &API{categories: &fakeCategoryService{deleteErr: category.ErrDefaultCategory}}
	request := httptest.NewRequest("DELETE", "/api/v1/categories/01234567-89ab-cdef-0123-456789abcdef", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("categoryId", "01234567-89ab-cdef-0123-456789abcdef")
	response := httptest.NewRecorder()
	api.deleteCategory(response, request, globalAdminPrincipal())
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":"default_category_immutable"`) {
		t.Fatalf("unexpected default deletion response %d: %s", response.Code, response.Body.String())
	}
}

func TestBulkMoveMapsAtomicNotFoundAndValidationErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{{"missing profile", category.ErrNotFound, 404, "category_resource_not_found"},
		{"invalid batch", errors.Join(category.ErrInvalidInput, errors.New("duplicate profileIds")), 422, "invalid_category_request"}} {
		t.Run(test.name, func(t *testing.T) {
			api := &API{categories: &fakeCategoryService{moveErr: test.err}}
			request := httptest.NewRequest("POST", "/api/v1/profiles/category-moves", strings.NewReader(`{"profileIds":["01234567-89ab-cdef-0123-456789abcdef"],"categoryId":"fedcba98-7654-3210-fedc-ba9876543210"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.moveProfilesToCategory(response, request, globalAdminPrincipal())
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("unexpected move error response %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func globalAdminPrincipal() auth.Principal {
	return auth.Principal{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator}
}
