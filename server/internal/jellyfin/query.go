package jellyfin

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	DefaultQueryLimit      = 100
	MaximumQueryLimit      = 200
	MaximumStartIndex      = 1_000_000
	MaximumSearchTermBytes = 256
	MaximumQueryBytes      = 8_192
	MaximumQueryParameters = 64
	MaximumQueryListValues = 100
	MaximumQueryValueBytes = 256
)

var ErrInvalidQuery = errors.New("invalid jellyfin query")

type ItemQuery struct {
	SearchTerm       string
	ParentId         string
	StartIndex       int
	Limit            int
	Recursive        bool
	IncludeItemTypes []string
	Fields           []string
	SortBy           []string
	SortOrder        string
	EnableUserData   bool
	Ids              []string
}

// ParseItemQuery accepts Jellyfin query names with ASCII-insensitive casing.
// Scalar parameters must occur exactly once; conflicting casing is therefore
// rejected rather than silently choosing an attacker-controlled value.
func ParseItemQuery(values url.Values) (ItemQuery, error) {
	query := ItemQuery{
		Limit:          DefaultQueryLimit,
		SortOrder:      "Ascending",
		EnableUserData: true,
	}
	if err := validateQueryBudget(values); err != nil {
		return ItemQuery{}, err
	}

	var err error
	if query.SearchTerm, err = boundedString(values, "SearchTerm", MaximumSearchTermBytes); err != nil {
		return ItemQuery{}, err
	}
	query.SearchTerm = strings.TrimSpace(query.SearchTerm)

	if query.ParentId, err = boundedString(values, "ParentId", MaximumQueryValueBytes); err != nil {
		return ItemQuery{}, err
	}
	if query.ParentId != "" {
		parent, parseErr := parseUUID(query.ParentId)
		if parseErr != nil {
			return ItemQuery{}, invalidQuery("ParentId must be a UUID")
		}
		query.ParentId = formatUUID(parent)
	}

	if query.StartIndex, err = boundedInteger(values, "StartIndex", 0, MaximumStartIndex, 0); err != nil {
		return ItemQuery{}, err
	}
	if query.Limit, err = boundedInteger(values, "Limit", 1, MaximumQueryLimit, DefaultQueryLimit); err != nil {
		return ItemQuery{}, err
	}
	if query.Recursive, err = booleanValue(values, "Recursive", false); err != nil {
		return ItemQuery{}, err
	}
	if query.EnableUserData, err = booleanValue(values, "EnableUserData", true); err != nil {
		return ItemQuery{}, err
	}
	if query.IncludeItemTypes, err = commaSeparated(values, "IncludeItemTypes"); err != nil {
		return ItemQuery{}, err
	}
	if query.Fields, err = commaSeparated(values, "Fields"); err != nil {
		return ItemQuery{}, err
	}
	if query.SortBy, err = commaSeparated(values, "SortBy"); err != nil {
		return ItemQuery{}, err
	}
	if query.Ids, err = canonicalIDs(values, "Ids"); err != nil {
		return ItemQuery{}, err
	}

	rawSortOrder, err := boundedString(values, "SortOrder", MaximumQueryValueBytes)
	if err != nil {
		return ItemQuery{}, err
	}
	if rawSortOrder != "" {
		switch {
		case strings.EqualFold(rawSortOrder, "Ascending"):
			query.SortOrder = "Ascending"
		case strings.EqualFold(rawSortOrder, "Descending"):
			query.SortOrder = "Descending"
		default:
			return ItemQuery{}, invalidQuery("SortOrder must be Ascending or Descending")
		}
	}
	return query, nil
}

func validateQueryBudget(values url.Values) error {
	parameters := 0
	bytes := 0
	for name, entries := range values {
		parameters += len(entries)
		bytes += len(name)
		for _, entry := range entries {
			bytes += len(entry)
		}
		if parameters > MaximumQueryParameters || bytes > MaximumQueryBytes {
			return invalidQuery("query exceeds its size limit")
		}
	}
	return nil
}

func queryScalar(values url.Values, name string) (string, bool, error) {
	var found []string
	for actualName, entries := range values {
		if strings.EqualFold(actualName, name) {
			found = append(found, entries...)
		}
	}
	if len(found) == 0 {
		return "", false, nil
	}
	if len(found) != 1 {
		return "", false, invalidQuery(name + " must occur once")
	}
	return found[0], true, nil
}

func boundedString(values url.Values, name string, maximum int) (string, error) {
	value, found, err := queryScalar(values, name)
	if err != nil || !found {
		return "", err
	}
	if len(value) > maximum {
		return "", invalidQuery(name + " exceeds its size limit")
	}
	return value, nil
}

func boundedInteger(values url.Values, name string, minimum, maximum, fallback int) (int, error) {
	raw, found, err := queryScalar(values, name)
	if err != nil {
		return 0, err
	}
	if !found {
		return fallback, nil
	}
	value, parseErr := strconv.ParseInt(raw, 10, 32)
	if parseErr != nil || value < int64(minimum) || value > int64(maximum) {
		return 0, invalidQuery(fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum))
	}
	return int(value), nil
}

func booleanValue(values url.Values, name string, fallback bool) (bool, error) {
	raw, found, err := queryScalar(values, name)
	if err != nil {
		return false, err
	}
	if !found {
		return fallback, nil
	}
	value, parseErr := strconv.ParseBool(raw)
	if parseErr != nil {
		return false, invalidQuery(name + " must be true or false")
	}
	return value, nil
}

func commaSeparated(values url.Values, name string) ([]string, error) {
	rawValues := make([]string, 0, 1)
	for actualName, entries := range values {
		if strings.EqualFold(actualName, name) {
			rawValues = append(rawValues, entries...)
		}
	}
	if len(rawValues) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		if raw == "" {
			return nil, invalidQuery(name + " contains an invalid value")
		}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" || len(part) > MaximumQueryValueBytes {
				return nil, invalidQuery(name + " contains an invalid value")
			}
			result = append(result, part)
			if len(result) > MaximumQueryListValues {
				return nil, invalidQuery(name + " contains too many values")
			}
		}
	}
	return result, nil
}

func canonicalIDs(values url.Values, name string) ([]string, error) {
	parts, err := commaSeparated(values, name)
	if err != nil || len(parts) == 0 {
		return parts, err
	}
	for index, part := range parts {
		value, parseErr := parseUUID(part)
		if parseErr != nil {
			return nil, invalidQuery(name + " must contain UUIDs")
		}
		parts[index] = formatUUID(value)
	}
	return parts, nil
}

func invalidQuery(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidQuery, message)
}
