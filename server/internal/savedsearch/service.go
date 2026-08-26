package savedsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	maximumNameRunes  = 120
	maximumQueryRunes = 256
	maximumRuleDepth  = 4
	maximumRuleLeaves = 32
	maximumSavedPerProfile = 100
)

var (
	savedSearchMediaTypes = map[string]bool{"": true, "movie": true, "series": true, "season": true, "episode": true, "video": true, "tv": true}
	savedSearchSorts      = map[string]bool{"relevance": true, "title": true, "year": true, "rating": true, "added": true}
	smartCollectionSorts  = map[string]bool{"title": true, "year": true, "rating": true, "added": true}
	statusValues          = map[string]bool{"planned": true, "released": true, "returning_series": true, "ended": true, "canceled": true, "in_production": true, "post_production": true, "pilot": true}
)

type Catalog interface {
	ListSmartCatalogItems(context.Context, auth.Principal, watchstate.SmartCatalogQuery) (watchstate.SmartCatalogPage, error)
}

type Service struct {
	pool    *pgxpool.Pool
	catalog Catalog
}

func NewService(pool *pgxpool.Pool, catalog Catalog) *Service {
	return &Service{pool: pool, catalog: catalog}
}

func (service *Service) ListSavedSearches(ctx context.Context, principal auth.Principal) ([]SavedSearch, error) {
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return nil, err }
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, query, COALESCE(media_type, ''), sort, revision, created_at, updated_at
		FROM profile_saved_searches WHERE profile_id = $1::uuid
		ORDER BY lower(name) COLLATE "C", id
		LIMIT $2
	`, profileID, maximumSavedPerProfile+1)
	if err != nil { return nil, fmt.Errorf("list saved searches: %w", err) }
	defer rows.Close()
	values := make([]SavedSearch, 0)
	for rows.Next() {
		value, scanErr := scanSavedSearch(rows)
		if scanErr != nil { rows.Close(); return nil, fmt.Errorf("scan saved search: %w", scanErr) }
		values = append(values, value)
		if len(values) > maximumSavedPerProfile { rows.Close(); return nil, fmt.Errorf("saved search capacity invariant exceeded") }
	}
	rows.Close()
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("iterate saved searches: %w", err) }
	if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit saved search list: %w", err) }
	return values, nil
}

func (service *Service) CreateSavedSearch(ctx context.Context, principal auth.Principal, input SavedSearchInput) (SavedSearch, error) {
	input, err := normalizeSavedSearch(input, false)
	if err != nil { return SavedSearch{}, err }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return SavedSearch{}, err }
	if err := ensureSavedResourceCapacity(ctx, tx, profileID, "saved"); err != nil { _ = tx.Rollback(ctx); return SavedSearch{}, err }
	defer func() { _ = tx.Rollback(ctx) }()
	value, err := scanSavedSearch(tx.QueryRow(ctx, `
		INSERT INTO profile_saved_searches (profile_id, name, query, media_type, sort)
		VALUES ($1::uuid, $2, $3, NULLIF($4, ''), $5)
		RETURNING id::text, name, query, COALESCE(media_type, ''), sort, revision, created_at, updated_at
	`, profileID, input.Name, input.Query, input.MediaType, input.Sort))
	if err != nil { return SavedSearch{}, mapWriteError("create saved search", err) }
	if err := tx.Commit(ctx); err != nil { return SavedSearch{}, fmt.Errorf("commit saved search creation: %w", err) }
	return value, nil
}

func (service *Service) UpdateSavedSearch(ctx context.Context, principal auth.Principal, id string, input SavedSearchInput) (SavedSearch, error) {
	input, err := normalizeSavedSearch(input, true)
	if err != nil { return SavedSearch{}, err }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return SavedSearch{}, err }
	defer func() { _ = tx.Rollback(ctx) }()
	value, err := scanSavedSearch(tx.QueryRow(ctx, `
		UPDATE profile_saved_searches SET name = $3, query = $4, media_type = NULLIF($5, ''), sort = $6,
		       revision = revision + 1, updated_at = now()
		WHERE id = $1::uuid AND profile_id = $2::uuid AND revision = $7
		RETURNING id::text, name, query, COALESCE(media_type, ''), sort, revision, created_at, updated_at
	`, id, profileID, input.Name, input.Query, input.MediaType, input.Sort, input.ExpectedRevision))
	if err == nil {
		if err := tx.Commit(ctx); err != nil { return SavedSearch{}, fmt.Errorf("commit saved search update: %w", err) }
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) { return SavedSearch{}, mapWriteError("update saved search", err) }
	return SavedSearch{}, service.classifyMissing(ctx, tx, "profile_saved_searches", id, profileID)
}

func (service *Service) DeleteSavedSearch(ctx context.Context, principal auth.Principal, id string, expectedRevision int64) error {
	if expectedRevision < 1 { return invalid("expectedRevision must be positive") }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM profile_saved_searches WHERE id = $1::uuid AND profile_id = $2::uuid AND revision = $3`, id, profileID, expectedRevision)
	if err != nil { return fmt.Errorf("delete saved search: %w", err) }
	if command.RowsAffected() != 1 { return service.classifyMissing(ctx, tx, "profile_saved_searches", id, profileID) }
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit saved search deletion: %w", err) }
	return nil
}

func (service *Service) ListSmartCollections(ctx context.Context, principal auth.Principal) ([]SmartCollection, error) {
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return nil, err }
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, rules, sort, revision, created_at, updated_at
		FROM profile_smart_collections WHERE profile_id = $1::uuid
		ORDER BY lower(name) COLLATE "C", id
		LIMIT $2
	`, profileID, maximumSavedPerProfile+1)
	if err != nil { return nil, fmt.Errorf("list smart collections: %w", err) }
	values := make([]SmartCollection, 0)
	defer rows.Close()
	for rows.Next() {
		value, scanErr := scanSmartCollection(rows)
		if scanErr != nil { rows.Close(); return nil, fmt.Errorf("scan smart collection: %w", scanErr) }
		values = append(values, value)
		if len(values) > maximumSavedPerProfile { rows.Close(); return nil, fmt.Errorf("smart collection capacity invariant exceeded") }
	}
	rows.Close()
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("iterate smart collections: %w", err) }
	if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit smart collection list: %w", err) }
	return values, nil
}

func (service *Service) CreateSmartCollection(ctx context.Context, principal auth.Principal, input SmartCollectionInput) (SmartCollection, error) {
	input, err := normalizeSmartCollection(input, false)
	if err != nil { return SmartCollection{}, err }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return SmartCollection{}, err }
	if err := ensureSavedResourceCapacity(ctx, tx, profileID, "smart"); err != nil { _ = tx.Rollback(ctx); return SmartCollection{}, err }
	defer func() { _ = tx.Rollback(ctx) }()
	rules, _ := json.Marshal(input.Rules)
	value, err := scanSmartCollection(tx.QueryRow(ctx, `
		INSERT INTO profile_smart_collections (profile_id, name, rules, sort)
		VALUES ($1::uuid, $2, $3::jsonb, $4)
		RETURNING id::text, name, rules, sort, revision, created_at, updated_at
	`, profileID, input.Name, rules, input.Sort))
	if err != nil { return SmartCollection{}, mapWriteError("create smart collection", err) }
	if err := tx.Commit(ctx); err != nil { return SmartCollection{}, fmt.Errorf("commit smart collection creation: %w", err) }
	return value, nil
}

func (service *Service) UpdateSmartCollection(ctx context.Context, principal auth.Principal, id string, input SmartCollectionInput) (SmartCollection, error) {
	input, err := normalizeSmartCollection(input, true)
	if err != nil { return SmartCollection{}, err }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return SmartCollection{}, err }
	defer func() { _ = tx.Rollback(ctx) }()
	rules, _ := json.Marshal(input.Rules)
	value, err := scanSmartCollection(tx.QueryRow(ctx, `
		UPDATE profile_smart_collections SET name = $3, rules = $4::jsonb, sort = $5,
		       revision = revision + 1, updated_at = now()
		WHERE id = $1::uuid AND profile_id = $2::uuid AND revision = $6
		RETURNING id::text, name, rules, sort, revision, created_at, updated_at
	`, id, profileID, input.Name, rules, input.Sort, input.ExpectedRevision))
	if err == nil {
		if err := tx.Commit(ctx); err != nil { return SmartCollection{}, fmt.Errorf("commit smart collection update: %w", err) }
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) { return SmartCollection{}, mapWriteError("update smart collection", err) }
	return SmartCollection{}, service.classifyMissing(ctx, tx, "profile_smart_collections", id, profileID)
}

func (service *Service) DeleteSmartCollection(ctx context.Context, principal auth.Principal, id string, expectedRevision int64) error {
	if expectedRevision < 1 { return invalid("expectedRevision must be positive") }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM profile_smart_collections WHERE id = $1::uuid AND profile_id = $2::uuid AND revision = $3`, id, profileID, expectedRevision)
	if err != nil { return fmt.Errorf("delete smart collection: %w", err) }
	if command.RowsAffected() != 1 { return service.classifyMissing(ctx, tx, "profile_smart_collections", id, profileID) }
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit smart collection deletion: %w", err) }
	return nil
}

func (service *Service) EvaluateSmartCollection(ctx context.Context, principal auth.Principal, id string, page, pageSize int) (SmartCollectionPage, error) {
	if page < 1 || page > 10000 || pageSize < 1 || pageSize > 100 { return SmartCollectionPage{}, invalid("page or pageSize is outside the supported range") }
	if service.catalog == nil { return SmartCollectionPage{}, errors.New("catalog service is unavailable") }
	tx, profileID, err := service.beginAuthorized(ctx, principal)
	if err != nil { return SmartCollectionPage{}, err }
	value, err := scanSmartCollection(tx.QueryRow(ctx, `
		SELECT id::text, name, rules, sort, revision, created_at, updated_at
		FROM profile_smart_collections WHERE id = $1::uuid AND profile_id = $2::uuid
	`, id, profileID))
	if errors.Is(err, pgx.ErrNoRows) { _ = tx.Rollback(ctx); return SmartCollectionPage{}, ErrNotFound }
	if err != nil { _ = tx.Rollback(ctx); return SmartCollectionPage{}, fmt.Errorf("load smart collection: %w", err) }
	if err := tx.Commit(ctx); err != nil { return SmartCollectionPage{}, fmt.Errorf("commit smart collection load: %w", err) }
	return service.evaluateSmartRule(ctx, principal, value, page, pageSize)
}

func (service *Service) evaluateSmartRule(ctx context.Context, principal auth.Principal, value SmartCollection, page, pageSize int) (SmartCollectionPage, error) {
	catalogPage, err := service.catalog.ListSmartCatalogItems(ctx, principal, watchstate.SmartCatalogQuery{
		Rule: smartCatalogRule(value.Rules), Sort: value.Sort, Offset: (page - 1) * pageSize, Limit: pageSize,
	})
	if err != nil { return SmartCollectionPage{}, fmt.Errorf("evaluate smart collection: %w", err) }
	totalPages := 0
	if catalogPage.Total != 0 { totalPages = (catalogPage.Total + pageSize - 1) / pageSize }
	return SmartCollectionPage{Items: catalogPage.Items, Page: page, PageSize: pageSize, Total: catalogPage.Total, TotalPages: totalPages}, nil
}

func smartCatalogRule(rule Rule) watchstate.SmartCatalogRule {
	value := watchstate.SmartCatalogRule{Type: rule.Type, Operator: rule.Operator, Value: rule.Value, Values: append([]string(nil), rule.Values...)}
	if rule.Number != nil { value.Number = *rule.Number }
	value.Rules = make([]watchstate.SmartCatalogRule, len(rule.Rules))
	for index := range rule.Rules { value.Rules[index] = smartCatalogRule(rule.Rules[index]) }
	return value
}

func (service *Service) beginAuthorized(ctx context.Context, principal auth.Principal) (pgx.Tx, string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) { return nil, "", ErrProfileRequired }
	tx, err := service.pool.Begin(ctx)
	if err != nil { return nil, "", fmt.Errorf("begin saved search authorization: %w", err) }
	profileID := *principal.ActiveProfileID
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil { _ = tx.Rollback(ctx); return nil, "", fmt.Errorf("authorize saved search profile: %w", err) }
	if !authorized { _ = tx.Rollback(ctx); return nil, "", ErrForbidden }
	valid, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil { _ = tx.Rollback(ctx); return nil, "", fmt.Errorf("lock saved search profile selection: %w", err) }
	if !valid { _ = tx.Rollback(ctx); return nil, "", ErrProfileRequired }
	return tx, profileID, nil
}
func ensureSavedResourceCapacity(ctx context.Context, tx pgx.Tx, profileID, kind string) error {
	var table, lockSuffix string
	switch kind {
	case "saved": table, lockSuffix = "profile_saved_searches", ":saved-searches"
	case "smart": table, lockSuffix = "profile_smart_collections", ":smart-collections"
	default: return errors.New("unknown saved resource capacity kind")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID+lockSuffix); err != nil {
		return fmt.Errorf("lock saved resource capacity: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM `+table+` WHERE profile_id = $1::uuid`, profileID).Scan(&count); err != nil {
		return fmt.Errorf("count saved resource capacity: %w", err)
	}
	if count >= maximumSavedPerProfile { return invalid("a profile cannot contain more than 100 saved resources of this type") }
	return nil
}


func (service *Service) classifyMissing(ctx context.Context, tx pgx.Tx, table, id, profileID string) error {
	var revision int64
	err := tx.QueryRow(ctx, `SELECT revision FROM `+table+` WHERE id = $1::uuid AND profile_id = $2::uuid`, id, profileID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) { return ErrNotFound }
	if err != nil { return fmt.Errorf("classify saved search mutation: %w", err) }
	return ErrConflict
}

type rowScanner interface { Scan(...any) error }

func scanSavedSearch(row rowScanner) (SavedSearch, error) {
	var value SavedSearch
	err := row.Scan(&value.ID, &value.Name, &value.Query, &value.MediaType, &value.Sort, &value.Revision, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func scanSmartCollection(row rowScanner) (SmartCollection, error) {
	var value SmartCollection
	var rules []byte
	if err := row.Scan(&value.ID, &value.Name, &rules, &value.Sort, &value.Revision, &value.CreatedAt, &value.UpdatedAt); err != nil { return SmartCollection{}, err }
	if err := json.Unmarshal(rules, &value.Rules); err != nil { return SmartCollection{}, fmt.Errorf("decode rules: %w", err) }
	return value, nil
}

func normalizeSavedSearch(input SavedSearchInput, update bool) (SavedSearchInput, error) {
	input.Name, input.Query = strings.TrimSpace(input.Name), strings.TrimSpace(input.Query)
	input.MediaType, input.Sort = strings.ToLower(strings.TrimSpace(input.MediaType)), strings.ToLower(strings.TrimSpace(input.Sort))
	if input.Sort == "" { input.Sort = "relevance" }
	if !validText(input.Name, maximumNameRunes) || !validText(input.Query, maximumQueryRunes) { return SavedSearchInput{}, invalid("name or query is outside the supported length") }
	if !savedSearchMediaTypes[input.MediaType] || !savedSearchSorts[input.Sort] { return SavedSearchInput{}, invalid("mediaType or sort is unsupported") }
	if update && input.ExpectedRevision < 1 { return SavedSearchInput{}, invalid("expectedRevision must be positive") }
	if !update && input.ExpectedRevision != 0 { return SavedSearchInput{}, invalid("expectedRevision is not accepted when creating") }
	return input, nil
}

func normalizeSmartCollection(input SmartCollectionInput, update bool) (SmartCollectionInput, error) {
	input.Name, input.Sort = strings.TrimSpace(input.Name), strings.ToLower(strings.TrimSpace(input.Sort))
	if input.Sort == "" { input.Sort = "title" }
	if !validText(input.Name, maximumNameRunes) || !smartCollectionSorts[input.Sort] { return SmartCollectionInput{}, invalid("name or sort is invalid") }
	leaves := 0
	if err := validateRule(&input.Rules, 1, &leaves); err != nil { return SmartCollectionInput{}, err }
	if update && input.ExpectedRevision < 1 { return SmartCollectionInput{}, invalid("expectedRevision must be positive") }
	if !update && input.ExpectedRevision != 0 { return SmartCollectionInput{}, invalid("expectedRevision is not accepted when creating") }
	return input, nil
}

func validateRule(rule *Rule, depth int, leaves *int) error {
	if depth > maximumRuleDepth { return invalid("rule nesting is too deep") }
	rule.Type, rule.Operator, rule.Value = strings.ToLower(strings.TrimSpace(rule.Type)), strings.ToLower(strings.TrimSpace(rule.Operator)), strings.TrimSpace(rule.Value)
	switch rule.Type {
	case "all", "any":
		if rule.Operator != "" || rule.Value != "" || rule.Number != nil || len(rule.Values) != 0 || len(rule.Rules) < 1 || len(rule.Rules) > 16 { return invalid("composite rule shape is invalid") }
		for index := range rule.Rules { if err := validateRule(&rule.Rules[index], depth+1, leaves); err != nil { return err } }
		return nil
	case "media_type":
		*leaves++
		if rule.Operator != "one_of" || rule.Value != "" || rule.Number != nil || len(rule.Rules) != 0 || len(rule.Values) < 1 || len(rule.Values) > 6 { return invalid("media_type rule shape is invalid") }
		seen := map[string]bool{}
		for index, value := range rule.Values {
			value = strings.ToLower(strings.TrimSpace(value)); rule.Values[index] = value
			if !savedSearchMediaTypes[value] || value == "" || seen[value] { return invalid("media_type rule value is invalid") }; seen[value] = true
		}
	case "year", "rating":
		*leaves++
		if !map[string]bool{"equals": true, "gte": true, "lte": true}[rule.Operator] || rule.Number == nil || rule.Value != "" || len(rule.Values) != 0 || len(rule.Rules) != 0 || math.IsNaN(*rule.Number) || math.IsInf(*rule.Number, 0) { return invalid("numeric rule shape is invalid") }
		if rule.Type == "year" && (*rule.Number < 1888 || *rule.Number > 2100 || math.Trunc(*rule.Number) != *rule.Number) { return invalid("year rule value is invalid") }
		if rule.Type == "rating" && (*rule.Number < 0 || *rule.Number > 10) { return invalid("rating rule value is invalid") }
	case "genre", "source", "status":
		*leaves++
		rule.Value = strings.ToLower(rule.Value)
		if !map[string]bool{"equals": true, "not_equals": true}[rule.Operator] || !validText(rule.Value, 128) || rule.Number != nil || len(rule.Values) != 0 || len(rule.Rules) != 0 { return invalid("text rule shape is invalid") }
		if rule.Type == "status" && !statusValues[normalizedToken(rule.Value)] { return invalid("status rule value is invalid") }
	default:
		return invalid("rule type is unsupported")
	}
	if *leaves > maximumRuleLeaves { return invalid("too many rules") }
	return nil
}


func normalizedToken(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "_")
}

func validText(value string, maximum int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidInput, message) }

func mapWriteError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" { return ErrConflict }
	if errors.As(err, &postgresError) && (postgresError.Code == "22P02" || postgresError.Code == "23514") { return ErrInvalidInput }
	return fmt.Errorf("%s: %w", operation, err)
}
