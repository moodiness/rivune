package watchstate

import (
	"context"
	"fmt"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
)

// SmartCatalogRule is an internal closed predicate tree. Callers construct it
// only from validated domain rules; every value is a bound query parameter.
type SmartCatalogRule struct {
	Type     string
	Operator string
	Value    string
	Values   []string
	Number   float64
	Rules    []SmartCatalogRule
}

type SmartCatalogQuery struct {
	Rule   SmartCatalogRule
	Sort   string
	Offset int
	Limit  int
}

type SmartCatalogPage struct {
	Items []CatalogTitle
	Total int
}

// ListSmartCatalogItems filters, counts, sorts and pages inside PostgreSQL. Its
// query count and allocation are bounded by page size, not catalogue size.
func (s *Service) ListSmartCatalogItems(ctx context.Context, principal auth.Principal, query SmartCatalogQuery) (SmartCatalogPage, error) {
	if query.Offset < 0 || query.Offset > maximumCatalogOffset || query.Limit < 1 || query.Limit > 100 {
		return SmartCatalogPage{}, fmt.Errorf("%w: invalid smart catalog page", ErrInvalidInput)
	}
	predicate, arguments, err := compileSmartCatalogRule(query.Rule, 2)
	if err != nil { return SmartCatalogPage{}, err }
	sortSQL, err := smartCatalogSortSQL(query.Sort)
	if err != nil { return SmartCatalogPage{}, err }
	offsetParameter := len(arguments) + 2
	limitParameter := offsetParameter + 1

	tx, err := s.pool.Begin(ctx)
	if err != nil { return SmartCatalogPage{}, fmt.Errorf("begin smart catalog query: %w", err) }
	defer func() { _ = tx.Rollback(ctx) }()
	profileID, err := authorizedActiveProfileID(ctx, tx, principal)
	if err != nil { return SmartCatalogPage{}, err }
	queryArguments := make([]any, 0, len(arguments)+3)
	queryArguments = append(queryArguments, profileID)
	queryArguments = append(queryArguments, arguments...)
	queryArguments = append(queryArguments, query.Offset, query.Limit)
	rows, err := tx.Query(ctx, `
		/* watchstate.smart_catalog_items */
		WITH accessible_titles AS (`+accessibleTitlesSQL+`), candidates AS MATERIALIZED (
			SELECT title.id, title.display_title, title.release_date, title.release_info,
			       library.added_at, metadata.payload,
			       CASE WHEN jsonb_typeof(metadata.payload -> 'voteAverage') = 'number'
			                  AND length(metadata.payload ->> 'voteAverage') <= 16
			                  AND (metadata.payload ->> 'voteAverage') ~ '^[0-9]+([.][0-9]+)?$'
			            THEN (metadata.payload ->> 'voteAverage')::double precision END AS rating,
			       COALESCE(EXTRACT(YEAR FROM title.release_date)::integer,
			          CASE WHEN left(COALESCE(title.release_info, ''), 4) ~ '^[0-9]{4}$' THEN left(title.release_info, 4)::integer END) AS production_year
			FROM titles title
			JOIN accessible_titles accessible ON accessible.id = title.id
			JOIN profile_library library ON library.profile_id = $1::uuid AND library.title_id = title.id
			LEFT JOIN LATERAL (
				SELECT payload FROM title_metadata
				WHERE title_id = title.id
				ORDER BY updated_at DESC, provider, language LIMIT 1
			) metadata ON true
			WHERE title.parent_id IS NULL AND (`+predicate+`)
		), counted AS (
			SELECT count(*)::int AS total FROM candidates
		), selected AS (
			SELECT id, row_number() OVER (ORDER BY `+sortSQL+`, id) AS ordinal
			FROM candidates
			ORDER BY `+sortSQL+`, id
			LIMIT $`+fmt.Sprint(limitParameter)+` OFFSET $`+fmt.Sprint(offsetParameter)+`
		)
		SELECT COALESCE(selected.id::text, ''), counted.total
		FROM counted LEFT JOIN selected ON true
		ORDER BY selected.ordinal NULLS LAST
	`, queryArguments...)
	if err != nil { return SmartCatalogPage{}, fmt.Errorf("query smart catalog page: %w", err) }
	ids := make([]string, 0, query.Limit)
	total := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id, &total); err != nil { rows.Close(); return SmartCatalogPage{}, fmt.Errorf("scan smart catalog page: %w", err) }
		if id != "" { ids = append(ids, id) }
	}
	rows.Close()
	if err := rows.Err(); err != nil { return SmartCatalogPage{}, fmt.Errorf("iterate smart catalog page: %w", err) }
	if err := tx.Commit(ctx); err != nil { return SmartCatalogPage{}, fmt.Errorf("commit smart catalog query: %w", err) }
	if len(ids) == 0 { return SmartCatalogPage{Items: make([]CatalogTitle, 0), Total: total}, nil }
	items, err := s.GetCatalogTitles(ctx, principal, ids)
	if err != nil { return SmartCatalogPage{}, err }
	byID := make(map[string]CatalogTitle, len(items))
	for _, item := range items { byID[item.ID] = item }
	ordered := make([]CatalogTitle, 0, len(ids))
	for _, id := range ids { if item, ok := byID[id]; ok { ordered = append(ordered, item) } }
	return SmartCatalogPage{Items: ordered, Total: total}, nil
}

func compileSmartCatalogRule(rule SmartCatalogRule, next int) (string, []any, error) {
	switch rule.Type {
	case "all", "any":
		parts := make([]string, 0, len(rule.Rules)); arguments := make([]any, 0)
		for _, child := range rule.Rules {
			part, values, err := compileSmartCatalogRule(child, next+len(arguments))
			if err != nil { return "", nil, err }
			parts, arguments = append(parts, "("+part+")"), append(arguments, values...)
		}
		if len(parts) == 0 { return "", nil, fmt.Errorf("%w: empty smart catalog rule", ErrInvalidInput) }
		joiner := " AND "; if rule.Type == "any" { joiner = " OR " }
		return strings.Join(parts, joiner), arguments, nil
	case "media_type":
		return fmt.Sprintf("title.media_type = ANY($%d::text[])", next), []any{rule.Values}, nil
	case "year":
		return numericSmartPredicate("COALESCE(EXTRACT(YEAR FROM title.release_date)::integer, CASE WHEN left(COALESCE(title.release_info, ''), 4) ~ '^[0-9]{4}$' THEN left(title.release_info, 4)::integer END)", rule.Operator, next, rule.Number)
	case "rating":
		return numericSmartPredicate("CASE WHEN jsonb_typeof(metadata.payload -> 'voteAverage') = 'number' AND length(metadata.payload ->> 'voteAverage') <= 16 AND (metadata.payload ->> 'voteAverage') ~ '^[0-9]+([.][0-9]+)?$' THEN (metadata.payload ->> 'voteAverage')::double precision END", rule.Operator, next, rule.Number)
	case "genre":
		match := fmt.Sprintf("EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(metadata.payload -> 'genres') = 'array' THEN metadata.payload -> 'genres' ELSE '[]'::jsonb END) genre WHERE lower(btrim(genre ->> 'name')) = lower($%d::text))", next)
		if rule.Operator == "not_equals" { match = "NOT ("+match+")" }
		return match, []any{rule.Value}, nil
	case "status":
		match := fmt.Sprintf("replace(lower(btrim(COALESCE(metadata.payload ->> 'status', ''))), ' ', '_') = $%d::text", next)
		if rule.Operator == "not_equals" { match = "NOT ("+match+")" }
		return match, []any{rule.Value}, nil
	case "source":
		match := fmt.Sprintf("lower($%d::text) = ANY(ARRAY[lower(COALESCE(title.resource_provider, '')), lower(COALESCE(title.source_addon_id::text, '')), lower(COALESCE(title.source_name, ''))])", next)
		if rule.Operator == "not_equals" { match = "NOT ("+match+")" }
		return match, []any{rule.Value}, nil
	default:
		return "", nil, fmt.Errorf("%w: unsupported smart catalog rule", ErrInvalidInput)
	}
}

func numericSmartPredicate(expression, operator string, parameter int, value float64) (string, []any, error) {
	comparison := map[string]string{"equals": "=", "gte": ">=", "lte": "<="}[operator]
	if comparison == "" { return "", nil, fmt.Errorf("%w: unsupported numeric smart catalog operator", ErrInvalidInput) }
	return fmt.Sprintf("%s %s $%d::double precision", expression, comparison, parameter), []any{value}, nil
}

func smartCatalogSortSQL(sort string) (string, error) {
	switch sort {
	case "title": return "lower(COALESCE(display_title, '')) COLLATE \"C\" ASC", nil
	case "year": return "production_year DESC NULLS LAST", nil
	case "rating": return "rating DESC NULLS LAST", nil
	case "added": return "added_at DESC", nil
	default: return "", fmt.Errorf("%w: unsupported smart catalog sort", ErrInvalidInput)
	}
}
