package category

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	iconPattern  = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	foldName     = cases.Fold()
)

func normalizeAndValidateName(value string) (string, string, error) {
	display := collapseWhitespace(norm.NFKC.String(value))
	if !validText(display, 1, 80) {
		return "", "", invalid("name must contain 1 to 80 characters")
	}
	return display, collapseWhitespace(foldName.String(display)), nil
}

func normalizeOptionalText(value *string, maximum int, field string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(norm.NFKC.String(*value))
	if !utf8.ValidString(normalized) || strings.ContainsRune(normalized, '\x00') || utf8.RuneCountInString(normalized) > maximum {
		return nil, invalid(fmt.Sprintf("%s must contain at most %d characters", field, maximum))
	}
	return &normalized, nil
}

func normalizeColor(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(*value))
	if !colorPattern.MatchString(normalized) {
		return nil, invalid("color must use #RRGGBB format")
	}
	return &normalized, nil
}

func normalizeIcon(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if len(normalized) > 64 || !iconPattern.MatchString(normalized) {
		return nil, invalid("icon must be a lowercase hyphen slug of at most 64 characters")
	}
	return &normalized, nil
}

func normalizeDeviceName(value string) (string, error) {
	value = collapseWhitespace(norm.NFKC.String(value))
	if !validText(value, 1, 120) {
		return "", invalid("name must contain 1 to 120 characters")
	}
	return value, nil
}

func collapseWhitespace(value string) string { return strings.Join(strings.Fields(value), " ") }

func validText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && length >= minimum && length <= maximum
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func canonicalUUID(value string) string {
	return strings.ToLower(value)
}

func sameUUID(left, right string) bool {
	return canonicalUUID(left) == canonicalUUID(right)
}

func validateMoveIDs(ids []string, categoryID, field string) error {
	if len(ids) == 0 || !validUUID(categoryID) {
		return invalid(field + " must be non-empty and categoryId must be valid")
	}
	seen := make(map[string]struct{}, len(ids))
	for index, id := range ids {
		if !validUUID(id) {
			return invalid(field + " must contain valid identifiers")
		}
		id = canonicalUUID(id)
		ids[index] = id
		if _, duplicate := seen[id]; duplicate {
			return invalid(field + " must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidInput, message) }

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
