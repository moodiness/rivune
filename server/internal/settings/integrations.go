package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

var integrationNames = []string{
	"tmdbAccessToken", "fanartApiKey", "mdblistApiKey", "tvdbApiKey", "tvdbPin",
	"traktClientId", "traktClientSecret", "simklClientId",
}

type IntegrationCredentials struct {
	TMDBAccessToken   string
	FanartAPIKey      string
	MDBListAPIKey     string
	TVDBAPIKey        string
	TVDBPIN           string
	TraktClientID     string
	TraktClientSecret string
	SimklClientID     string
	Revision          int64
}

func (IntegrationCredentials) MarshalJSON() ([]byte, error) {
	return nil, errors.New("integration credentials cannot be serialized")
}

func (IntegrationCredentials) String() string   { return "[REDACTED integration credentials]" }
func (IntegrationCredentials) GoString() string { return "[REDACTED integration credentials]" }

type IntegrationPublisher interface {
	PublishIntegrations(context.Context, IntegrationCredentials) error
}

type OptionalCredential struct {
	Set   bool
	Value *string
}

type IntegrationCredentialsPatch struct {
	TMDBAccessToken   OptionalCredential
	FanartAPIKey      OptionalCredential
	MDBListAPIKey     OptionalCredential
	TVDBAPIKey        OptionalCredential
	TVDBPIN           OptionalCredential
	TraktClientID     OptionalCredential
	TraktClientSecret OptionalCredential
	SimklClientID     OptionalCredential
}

type IntegrationCredentialStatus struct {
	Configured bool       `json:"configured"`
	UpdatedAt  *time.Time `json:"updatedAt"`
}

type IntegrationCredentialStatuses struct {
	TMDBAccessToken   IntegrationCredentialStatus `json:"tmdbAccessToken"`
	FanartAPIKey      IntegrationCredentialStatus `json:"fanartApiKey"`
	MDBListAPIKey     IntegrationCredentialStatus `json:"mdblistApiKey"`
	TVDBAPIKey        IntegrationCredentialStatus `json:"tvdbApiKey"`
	TVDBPIN           IntegrationCredentialStatus `json:"tvdbPin"`
	TraktClientID     IntegrationCredentialStatus `json:"traktClientId"`
	TraktClientSecret IntegrationCredentialStatus `json:"traktClientSecret"`
	SimklClientID     IntegrationCredentialStatus `json:"simklClientId"`
}

type IntegrationProviders struct {
	TMDB    bool `json:"tmdb"`
	TVDB    bool `json:"tvdb"`
	Fanart  bool `json:"fanart"`
	MDBList bool `json:"mdblist"`
	Trakt   bool `json:"trakt"`
	Simkl   bool `json:"simkl"`
}

type IntegrationStatus struct {
	Revision    int64                         `json:"revision"`
	Credentials IntegrationCredentialStatuses `json:"credentials"`
	Providers   IntegrationProviders          `json:"providers"`
}

type storedCredential struct {
	ciphertext    []byte
	cipherVersion int
	keyVersion    int
	generation    int64
	updatedAt     time.Time
}

func (s *Service) LoadIntegrationCredentials(ctx context.Context) (IntegrationCredentials, error) {
	if s.keyring == nil {
		return IntegrationCredentials{}, errors.New("integration credential encryption is not configured")
	}
	var publicID string
	var revision int64
	err := s.pool.QueryRow(ctx, `SELECT public_id::text, configuration_revision FROM instances WHERE id = 1`).Scan(&publicID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return IntegrationCredentials{}, nil
	}
	if err != nil {
		return IntegrationCredentials{}, fmt.Errorf("load integration credential instance: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT name, ciphertext, COALESCE(cipher_version, 0), COALESCE(encryption_key_version, 0), generation
		FROM instance_integration_credentials WHERE instance_id = 1
	`)
	if err != nil {
		return IntegrationCredentials{}, fmt.Errorf("load integration credentials: %w", err)
	}
	defer rows.Close()
	values := make(map[string]string, len(integrationNames))
	for rows.Next() {
		var name string
		var stored storedCredential
		if err := rows.Scan(&name, &stored.ciphertext, &stored.cipherVersion, &stored.keyVersion, &stored.generation); err != nil {
			return IntegrationCredentials{}, fmt.Errorf("scan integration credential: %w", err)
		}
		if stored.ciphertext == nil {
			continue
		}
		plaintext, err := s.keyring.Decrypt(secretcrypto.Envelope{Ciphertext: stored.ciphertext, CipherVersion: stored.cipherVersion, KeyVersion: stored.keyVersion}, integrationAAD(publicID, name, stored.generation, stored.keyVersion))
		if err != nil {
			return IntegrationCredentials{}, errors.New("stored integration credential could not be decrypted")
		}
		values[name] = string(plaintext)
	}
	if err := rows.Err(); err != nil {
		return IntegrationCredentials{}, fmt.Errorf("iterate integration credentials: %w", err)
	}
	return credentialsFromMap(values, revision), nil
}

func (s *Service) IntegrationStatus(ctx context.Context, principal auth.Principal) (IntegrationStatus, error) {
	if !principal.IsGlobalAdministrator() {
		return IntegrationStatus{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IntegrationStatus{}, fmt.Errorf("begin integration status query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAdministrator(ctx, tx, principal); err != nil {
		return IntegrationStatus{}, err
	}
	status, err := queryIntegrationStatus(ctx, tx)
	if err != nil {
		return IntegrationStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return IntegrationStatus{}, fmt.Errorf("commit integration status query: %w", err)
	}
	return status, nil
}

func (s *Service) UpdateIntegrationCredentials(ctx context.Context, principal auth.Principal, patch IntegrationCredentialsPatch) (IntegrationStatus, error) {
	if !principal.IsGlobalAdministrator() {
		return IntegrationStatus{}, ErrForbidden
	}
	patches := integrationPatchMap(patch)
	changed := make([]string, 0, len(patches))
	for name, value := range patches {
		if !value.Set {
			continue
		}
		changed = append(changed, name)
		if value.Value != nil && *value.Value == "" {
			return IntegrationStatus{}, fmt.Errorf("%w: credential must not be empty", ErrInvalidInput)
		}
	}
	if len(changed) == 0 {
		return IntegrationStatus{}, fmt.Errorf("%w: at least one integration credential must be provided", ErrInvalidInput)
	}
	if s.keyring == nil {
		return IntegrationStatus{}, errors.New("integration credential encryption is not configured")
	}
	sort.Strings(changed)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IntegrationStatus{}, fmt.Errorf("begin integration credential update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAdministrator(ctx, tx, principal); err != nil {
		return IntegrationStatus{}, err
	}
	var publicID string
	if err := tx.QueryRow(ctx, `SELECT public_id::text FROM instances WHERE id = 1 FOR UPDATE`).Scan(&publicID); err != nil {
		return IntegrationStatus{}, fmt.Errorf("lock integration credential instance: %w", err)
	}
	stored, values, err := s.loadIntegrationCredentialsForUpdate(ctx, tx, publicID)
	if err != nil {
		return IntegrationStatus{}, err
	}
	for name, value := range patches {
		if !value.Set {
			continue
		}
		if value.Value == nil {
			delete(values, name)
		} else {
			values[name] = *value.Value
		}
	}
	if values["tvdbPin"] != "" && values["tvdbApiKey"] == "" {
		return IntegrationStatus{}, fmt.Errorf("%w: tvdbPin requires tvdbApiKey", ErrInvalidInput)
	}
	if values["traktClientSecret"] != "" && values["traktClientId"] == "" {
		return IntegrationStatus{}, fmt.Errorf("%w: traktClientSecret requires traktClientId", ErrInvalidInput)
	}

	for _, name := range changed {
		value := patches[name]
		generation := int64(1)
		if previous, ok := stored[name]; ok {
			generation = previous.generation + 1
		}
		if value.Value == nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO instance_integration_credentials
				(instance_id, name, ciphertext, cipher_version, encryption_key_version, generation, updated_at)
				VALUES (1, $1, NULL, NULL, NULL, $2, now())
				ON CONFLICT (instance_id, name) DO UPDATE SET ciphertext = NULL, cipher_version = NULL,
				encryption_key_version = NULL, generation = EXCLUDED.generation, updated_at = EXCLUDED.updated_at
			`, name, generation); err != nil {
				return IntegrationStatus{}, fmt.Errorf("clear integration credential: %w", err)
			}
			continue
		}
		keyVersion := s.keyring.ActiveVersion()
		envelope, err := s.keyring.Encrypt([]byte(*value.Value), integrationAAD(publicID, name, generation, keyVersion))
		if err != nil {
			return IntegrationStatus{}, errors.New("integration credential could not be encrypted")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_integration_credentials
			(instance_id, name, ciphertext, cipher_version, encryption_key_version, generation, updated_at)
			VALUES (1, $1, $2, $3, $4, $5, now())
			ON CONFLICT (instance_id, name) DO UPDATE SET
			ciphertext = EXCLUDED.ciphertext, cipher_version = EXCLUDED.cipher_version,
			encryption_key_version = EXCLUDED.encryption_key_version, generation = EXCLUDED.generation,
			updated_at = EXCLUDED.updated_at
		`, name, envelope.Ciphertext, envelope.CipherVersion, envelope.KeyVersion, generation); err != nil {
			return IntegrationStatus{}, fmt.Errorf("store integration credential: %w", err)
		}
	}
	revision, err := incrementConfigurationRevision(ctx, tx)
	if err != nil {
		return IntegrationStatus{}, err
	}
	status, err := queryIntegrationStatus(ctx, tx)
	if err != nil {
		return IntegrationStatus{}, err
	}
	status.Revision = revision
	snapshot, err := json.Marshal(configuredSnapshot(status.Credentials))
	if err != nil {
		return IntegrationStatus{}, fmt.Errorf("encode redacted integration audit snapshot: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, revision, principal.UserID, "integrations.updated", changed, snapshot); err != nil {
		return IntegrationStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return IntegrationStatus{}, fmt.Errorf("commit integration credential update: %w", err)
	}

	credentials := credentialsFromMap(values, revision)
	if err := s.publishIntegrations(ctx, credentials); err != nil {
		s.scheduleIntegrationReconciliation()
	}
	return status, nil
}

func (s *Service) loadIntegrationCredentialsForUpdate(ctx context.Context, tx pgx.Tx, publicID string) (map[string]storedCredential, map[string]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT name, ciphertext, COALESCE(cipher_version, 0), COALESCE(encryption_key_version, 0), generation, updated_at
		FROM instance_integration_credentials WHERE instance_id = 1 FOR UPDATE
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("lock integration credentials: %w", err)
	}
	defer rows.Close()
	stored := make(map[string]storedCredential, len(integrationNames))
	values := make(map[string]string, len(integrationNames))
	for rows.Next() {
		var name string
		var value storedCredential
		if err := rows.Scan(&name, &value.ciphertext, &value.cipherVersion, &value.keyVersion, &value.generation, &value.updatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan integration credential: %w", err)
		}
		stored[name] = value
		if value.ciphertext == nil {
			continue
		}
		plaintext, err := s.keyring.Decrypt(secretcrypto.Envelope{Ciphertext: value.ciphertext, CipherVersion: value.cipherVersion, KeyVersion: value.keyVersion}, integrationAAD(publicID, name, value.generation, value.keyVersion))
		if err != nil {
			return nil, nil, errors.New("stored integration credential could not be decrypted")
		}
		values[name] = string(plaintext)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate integration credentials: %w", err)
	}
	return stored, values, nil
}

func queryIntegrationStatus(ctx context.Context, querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}) (IntegrationStatus, error) {
	var status IntegrationStatus
	if err := querier.QueryRow(ctx, `SELECT configuration_revision FROM instances WHERE id = 1`).Scan(&status.Revision); err != nil {
		return IntegrationStatus{}, fmt.Errorf("query integration revision: %w", err)
	}
	rows, err := querier.Query(ctx, `SELECT name, ciphertext IS NOT NULL, updated_at FROM instance_integration_credentials WHERE instance_id = 1`)
	if err != nil {
		return IntegrationStatus{}, fmt.Errorf("query integration status: %w", err)
	}
	defer rows.Close()
	statuses := make(map[string]IntegrationCredentialStatus, len(integrationNames))
	for rows.Next() {
		var name string
		var configured bool
		var updated time.Time
		if err := rows.Scan(&name, &configured, &updated); err != nil {
			return IntegrationStatus{}, fmt.Errorf("scan integration status: %w", err)
		}
		updatedCopy := updated
		statuses[name] = IntegrationCredentialStatus{Configured: configured, UpdatedAt: &updatedCopy}
	}
	if err := rows.Err(); err != nil {
		return IntegrationStatus{}, fmt.Errorf("iterate integration status: %w", err)
	}
	status.Credentials = statusStruct(statuses)
	status.Providers = providerStatus(status.Credentials)
	return status, nil
}

func integrationAAD(publicID, name string, generation int64, keyVersion int) []byte {
	return []byte("rivune:integration:" + publicID + ":" + name + ":" + strconv.FormatInt(generation, 10) + ":" + strconv.Itoa(keyVersion))
}

func integrationPatchMap(p IntegrationCredentialsPatch) map[string]OptionalCredential {
	return map[string]OptionalCredential{
		"tmdbAccessToken": p.TMDBAccessToken, "fanartApiKey": p.FanartAPIKey,
		"mdblistApiKey": p.MDBListAPIKey, "tvdbApiKey": p.TVDBAPIKey, "tvdbPin": p.TVDBPIN,
		"traktClientId": p.TraktClientID, "traktClientSecret": p.TraktClientSecret, "simklClientId": p.SimklClientID,
	}
}

func credentialsFromMap(v map[string]string, revision int64) IntegrationCredentials {
	return IntegrationCredentials{TMDBAccessToken: v["tmdbAccessToken"], FanartAPIKey: v["fanartApiKey"], MDBListAPIKey: v["mdblistApiKey"], TVDBAPIKey: v["tvdbApiKey"], TVDBPIN: v["tvdbPin"], TraktClientID: v["traktClientId"], TraktClientSecret: v["traktClientSecret"], SimklClientID: v["simklClientId"], Revision: revision}
}

func statusStruct(v map[string]IntegrationCredentialStatus) IntegrationCredentialStatuses {
	return IntegrationCredentialStatuses{TMDBAccessToken: v["tmdbAccessToken"], FanartAPIKey: v["fanartApiKey"], MDBListAPIKey: v["mdblistApiKey"], TVDBAPIKey: v["tvdbApiKey"], TVDBPIN: v["tvdbPin"], TraktClientID: v["traktClientId"], TraktClientSecret: v["traktClientSecret"], SimklClientID: v["simklClientId"]}
}

func providerStatus(s IntegrationCredentialStatuses) IntegrationProviders {
	return IntegrationProviders{TMDB: s.TMDBAccessToken.Configured, TVDB: s.TVDBAPIKey.Configured, Fanart: s.FanartAPIKey.Configured, MDBList: s.MDBListAPIKey.Configured, Trakt: s.TraktClientID.Configured && s.TraktClientSecret.Configured, Simkl: s.SimklClientID.Configured}
}

func configuredSnapshot(s IntegrationCredentialStatuses) map[string]bool {
	return map[string]bool{"tmdbAccessToken": s.TMDBAccessToken.Configured, "fanartApiKey": s.FanartAPIKey.Configured, "mdblistApiKey": s.MDBListAPIKey.Configured, "tvdbApiKey": s.TVDBAPIKey.Configured, "tvdbPin": s.TVDBPIN.Configured, "traktClientId": s.TraktClientID.Configured, "traktClientSecret": s.TraktClientSecret.Configured, "simklClientId": s.SimklClientID.Configured}
}

func (s *Service) publishIntegrations(ctx context.Context, credentials IntegrationCredentials) error {
	s.publisherMu.RLock()
	publisher := s.publisher
	s.publisherMu.RUnlock()
	if publisher == nil {
		return nil
	}
	return publisher.PublishIntegrations(ctx, credentials)
}

func (s *Service) scheduleIntegrationReconciliation() {
	if !s.reconciling.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.reconciling.Store(false)
		for {
			timer := time.NewTimer(30 * time.Second)
			<-timer.C
			credentials, err := s.LoadIntegrationCredentials(context.Background())
			if err == nil && s.publishIntegrations(context.Background(), credentials) == nil {
				return
			}
		}
	}()
}
