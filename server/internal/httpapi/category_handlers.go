package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/category"
)

type categoryRefResponse struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
	Icon  *string `json:"icon"`
}
type categoryResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  *string   `json:"description"`
	Color        *string   `json:"color"`
	Icon         *string   `json:"icon"`
	Position     int       `json:"position"`
	IsDefault    bool      `json:"isDefault"`
	ProfileCount int64     `json:"profileCount"`
	DeviceCount  int64     `json:"deviceCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type deviceResponse struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Platform     string              `json:"platform"`
	CategoryID   string              `json:"categoryId"`
	Category     categoryRefResponse `json:"category"`
	InternalNote *string             `json:"internalNote"`
	ApprovedAt   time.Time           `json:"approvedAt"`
	LastSeenAt   *time.Time          `json:"lastSeenAt"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

func (a *API) listCategories(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	items, err := a.categories.List(r.Context(), categoryActor(principal))
	if writeCategoryError(a, w, err, "list categories") {
		return
	}
	response := make([]categoryResponse, 0, len(items))
	for _, item := range items {
		response = append(response, newCategoryResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": response})
}
func (a *API) createCategory(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
		Icon        *string `json:"icon"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := a.categories.Create(r.Context(), categoryActor(principal), category.CreateInput{Name: request.Name, Description: request.Description, Color: request.Color, Icon: request.Icon})
	if writeCategoryError(a, w, err, "create category") {
		return
	}
	writeJSON(w, http.StatusCreated, newCategoryResponse(created))
}
func (a *API) updateCategory(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Name        nullableString `json:"name"`
		Description nullableString `json:"description"`
		Color       nullableString `json:"color"`
		Icon        nullableString `json:"icon"`
		IsDefault   nullableBool   `json:"isDefault"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Name.Set && request.Name.Value == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_category", "name cannot be null")
		return
	}
	if request.IsDefault.Set && (request.IsDefault.Value == nil || !*request.IsDefault.Value) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_category", "isDefault may only be set to true")
		return
	}
	updated, err := a.categories.Update(r.Context(), categoryActor(principal), r.PathValue("categoryId"), category.UpdateInput{
		Name: request.Name.Value, DescriptionSet: request.Description.Set, Description: request.Description.Value,
		ColorSet: request.Color.Set, Color: request.Color.Value, IconSet: request.Icon.Set, Icon: request.Icon.Value,
		MakeDefault: request.IsDefault.Value != nil && *request.IsDefault.Value,
	})
	if writeCategoryError(a, w, err, "update category") {
		return
	}
	writeJSON(w, http.StatusOK, newCategoryResponse(updated))
}
func (a *API) deleteCategory(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		ReassignToCategoryID nullableString `json:"reassignToCategoryId"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := a.categories.Delete(r.Context(), categoryActor(principal), r.PathValue("categoryId"), request.ReassignToCategoryID.Value)
	if writeCategoryError(a, w, err, "delete category") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) reorderCategories(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		CategoryIDs []string `json:"categoryIds"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, err := a.categories.Reorder(r.Context(), categoryActor(principal), request.CategoryIDs)
	if writeCategoryError(a, w, err, "reorder categories") {
		return
	}
	response := make([]categoryResponse, 0, len(items))
	for _, item := range items {
		response = append(response, newCategoryResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": response})
}
func (a *API) moveProfilesToCategory(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		ProfileIDs []string `json:"profileIds"`
		CategoryID string   `json:"categoryId"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := a.categories.MoveProfiles(r.Context(), categoryActor(principal), request.ProfileIDs, request.CategoryID)
	if writeCategoryError(a, w, err, "move profiles to category") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) listDevices(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	var categoryID *string
	if values, present := r.URL.Query()["categoryId"]; present {
		value := ""
		if len(values) > 0 {
			value = strings.TrimSpace(values[0])
		}
		categoryID = &value
	}
	items, err := a.categories.ListDevices(r.Context(), categoryActor(principal), categoryID)
	if writeCategoryError(a, w, err, "list devices") {
		return
	}
	response := make([]deviceResponse, 0, len(items))
	for _, item := range items {
		response = append(response, newDeviceResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": response})
}
func (a *API) updateDevice(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Name         nullableString `json:"name"`
		CategoryID   nullableString `json:"categoryId"`
		InternalNote nullableString `json:"internalNote"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if (request.Name.Set && request.Name.Value == nil) || (request.CategoryID.Set && request.CategoryID.Value == nil) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_device", "name and categoryId cannot be null")
		return
	}
	updated, err := a.categories.UpdateDevice(r.Context(), categoryActor(principal), r.PathValue("deviceId"), category.DeviceUpdateInput{Name: request.Name.Value, CategoryID: request.CategoryID.Value, InternalNoteSet: request.InternalNote.Set, InternalNote: request.InternalNote.Value})
	if writeCategoryError(a, w, err, "update device") {
		return
	}
	writeJSON(w, http.StatusOK, newDeviceResponse(updated))
}
func (a *API) moveDevicesToCategory(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		DeviceIDs  []string `json:"deviceIds"`
		CategoryID string   `json:"categoryId"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := a.categories.MoveDevices(r.Context(), categoryActor(principal), request.DeviceIDs, request.CategoryID)
	if writeCategoryError(a, w, err, "move devices to category") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeCategoryError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, category.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_category_request", strings.TrimPrefix(err.Error(), category.ErrInvalidInput.Error()+": "))
	case errors.Is(err, category.ErrForbidden):
		writeError(w, http.StatusForbidden, "global_admin_required", "Global administrator access is required")
	case errors.Is(err, category.ErrNotFound):
		writeError(w, http.StatusNotFound, "category_resource_not_found", "The requested category resource does not exist")
	case errors.Is(err, category.ErrDefaultCategory):
		writeError(w, http.StatusConflict, "default_category_immutable", "The default category cannot be deleted")
	case errors.Is(err, category.ErrConflict):
		writeError(w, http.StatusConflict, "category_name_conflict", "A category with that normalized name already exists")
	case errors.Is(err, category.ErrReassignmentRequired):
		writeError(w, http.StatusConflict, "category_reassignment_required", "Choose another category before deleting this category")
	default:
		a.internalError(w, operation, err)
	}
	return true
}
func newCategoryRefResponse(value category.CategoryRef) categoryRefResponse {
	return categoryRefResponse{ID: value.ID, Name: value.Name, Color: value.Color, Icon: value.Icon}
}
func newCategoryResponse(value category.Category) categoryResponse {
	return categoryResponse{ID: value.ID, Name: value.Name, Description: value.Description, Color: value.Color, Icon: value.Icon, Position: value.Position, IsDefault: value.IsDefault, ProfileCount: value.ProfileCount, DeviceCount: value.DeviceCount, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
func newDeviceResponse(value category.Device) deviceResponse {
	return deviceResponse{ID: value.ID, Name: value.Name, Platform: value.Platform, CategoryID: value.Category.ID, Category: newCategoryRefResponse(value.Category), InternalNote: value.InternalNote, ApprovedAt: value.ApprovedAt, LastSeenAt: value.LastSeenAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
func categoryActor(principal auth.Principal) category.Actor {
	return category.Actor{UserID: principal.UserID, GlobalAdministrator: principal.IsGlobalAdministrator()}
}
