package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/settings"
)

const integrationCredentialsMaximumBytes int64 = 32 * 1024

type integrationCredentialsPatchRequest struct {
	TMDBAccessToken   nullableString `json:"tmdbAccessToken,omitempty"`
	FanartAPIKey      nullableString `json:"fanartApiKey,omitempty"`
	MDBListAPIKey     nullableString `json:"mdblistApiKey,omitempty"`
	TVDBAPIKey        nullableString `json:"tvdbApiKey,omitempty"`
	TVDBPIN           nullableString `json:"tvdbPin,omitempty"`
	TraktClientID     nullableString `json:"traktClientId,omitempty"`
	TraktClientSecret nullableString `json:"traktClientSecret,omitempty"`
	SimklClientID     nullableString `json:"simklClientId,omitempty"`
}

func (a *API) integrationSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !principal.IsGlobalAdministrator() {
		writeError(w, http.StatusForbidden, "settings_forbidden", "This account cannot read integration settings")
		return
	}
	status, err := a.integrationConfiguration.IntegrationStatus(r.Context(), principal)
	if writeSettingsError(a, w, err, "read integration settings") {
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) updateIntegrationSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !principal.IsGlobalAdministrator() {
		writeError(w, http.StatusForbidden, "settings_forbidden", "This account cannot modify integration settings")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var request integrationCredentialsPatchRequest
	if err := decodeJSONLimit(w, r, &request, integrationCredentialsMaximumBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Integration credentials must be a valid JSON object")
		return
	}
	if err := validateIntegrationCredentialsPatch(request); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_settings", err.Error())
		return
	}
	status, err := a.integrationConfiguration.UpdateIntegrationCredentials(r.Context(), principal, integrationCredentialsPatch(request))
	if writeSettingsError(a, w, err, "update integration settings") {
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func validateIntegrationCredentialsPatch(request integrationCredentialsPatchRequest) error {
	fields := []struct {
		name    string
		value   nullableString
		maximum int
	}{
		{"tmdbAccessToken", request.TMDBAccessToken, 4096},
		{"fanartApiKey", request.FanartAPIKey, 4096},
		{"mdblistApiKey", request.MDBListAPIKey, 4096},
		{"tvdbApiKey", request.TVDBAPIKey, 4096},
		{"tvdbPin", request.TVDBPIN, 256},
		{"traktClientId", request.TraktClientID, 512},
		{"traktClientSecret", request.TraktClientSecret, 4096},
		{"simklClientId", request.SimklClientID, 512},
	}
	provided := false
	for _, field := range fields {
		if !field.value.Set {
			continue
		}
		provided = true
		if field.value.Value != nil && (strings.TrimSpace(*field.value.Value) == "" || utf8.RuneCountInString(*field.value.Value) > field.maximum) {
			return settingsCredentialValidationError(field.name)
		}
	}
	if !provided {
		return settingsCredentialValidationError("credential")
	}
	return nil
}

func settingsCredentialValidationError(name string) error {
	return &credentialValidationError{name: name}
}

type credentialValidationError struct{ name string }

func (err *credentialValidationError) Error() string {
	if err.name == "credential" {
		return "At least one integration credential must be provided"
	}
	return err.name + " must be null or a nonempty string within its size limit"
}

func integrationCredentialsPatch(request integrationCredentialsPatchRequest) settings.IntegrationCredentialsPatch {
	return settings.IntegrationCredentialsPatch{
		TMDBAccessToken:   settings.OptionalCredential{Set: request.TMDBAccessToken.Set, Value: request.TMDBAccessToken.Value},
		FanartAPIKey:      settings.OptionalCredential{Set: request.FanartAPIKey.Set, Value: request.FanartAPIKey.Value},
		MDBListAPIKey:     settings.OptionalCredential{Set: request.MDBListAPIKey.Set, Value: request.MDBListAPIKey.Value},
		TVDBAPIKey:        settings.OptionalCredential{Set: request.TVDBAPIKey.Set, Value: request.TVDBAPIKey.Value},
		TVDBPIN:           settings.OptionalCredential{Set: request.TVDBPIN.Set, Value: request.TVDBPIN.Value},
		TraktClientID:     settings.OptionalCredential{Set: request.TraktClientID.Set, Value: request.TraktClientID.Value},
		TraktClientSecret: settings.OptionalCredential{Set: request.TraktClientSecret.Set, Value: request.TraktClientSecret.Value},
		SimklClientID:     settings.OptionalCredential{Set: request.SimklClientID.Set, Value: request.SimklClientID.Value},
	}
}

func (a *API) configurationAudit(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !principal.IsGlobalAdministrator() {
		writeError(w, http.StatusForbidden, "settings_forbidden", "This account cannot read configuration audit events")
		return
	}
	cursor, limit, ok := parseConfigurationAuditPage(w, r)
	if !ok {
		return
	}
	page, err := a.integrationConfiguration.ListAuditEvents(r.Context(), principal, cursor, limit)
	if writeSettingsError(a, w, err, "read configuration audit events") {
		return
	}
	filtered := page.Events[:0]
	for _, event := range page.Events {
		if event.Action == "settings.updated" || event.Action == "integrations.updated" {
			filtered = append(filtered, event)
		}
	}
	page.Events = filtered
	writeJSON(w, http.StatusOK, page)
}

func parseConfigurationAuditPage(w http.ResponseWriter, r *http.Request) (*int64, int, bool) {
	query := r.URL.Query()
	var cursor *int64
	if values, present := query["cursor"]; present {
		if len(values) != 1 || values[0] == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "cursor must be a positive integer")
			return nil, 0, false
		}
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "cursor must be a positive integer")
			return nil, 0, false
		}
		cursor = &value
	}
	limit := settings.DefaultAuditLimit
	if values, present := query["limit"]; present {
		if len(values) != 1 || values[0] == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
			return nil, 0, false
		}
		value, err := strconv.Atoi(values[0])
		if err != nil || value < 1 || value > settings.MaximumAuditLimit {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
			return nil, 0, false
		}
		limit = value
	}
	return cursor, limit, true
}
