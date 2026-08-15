package jellyfin

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	DefaultQueryLimit       = 100
	MaximumQueryLimit       = 200
	MaximumLatestQueryLimit = 1008
	MaximumStartIndex       = 1_000_000
	MaximumSearchTermBytes  = 256
	MaximumQueryBytes       = 8_192
	MaximumQueryParameters  = 64
	MaximumQueryListValues  = 100
	MaximumQueryValueBytes  = 256
)

var ErrInvalidQuery = errors.New("invalid jellyfin query")

type ItemQuery struct {
	SearchTerm             string
	ParentId               string
	StartIndex             int
	Limit                  int
	RequestedLimit         int
	Recursive              bool
	IncludeItemTypes       []string
	ExcludeItemTypes       []string
	MediaTypes             []string
	Filters                []string
	Fields                 []string
	SortBy                 []string
	SortOrder              string
	EnableUserData         bool
	Ids                    []string
	IsPlayed               *bool
	IsFavorite             *bool
	IsResumable            *bool
	MinCommunityRating     *float64
	HasSubtitles           *bool
	Genres                 []string
	GenreIds               []string
	Years                  []int
	OfficialRatings        []string
	Tags                   []string
	PersonIds              []string
	Studios                []string
	HasTrailer             *bool
	EnableImages           bool
	EnableImageTypes       []string
	ImageTypeLimit         int
	EnableTotalRecordCount bool
}

// ParseItemQuery accepts Jellyfin query names with ASCII-insensitive casing.
// Scalar parameters must occur exactly once; conflicting casing is therefore
// rejected rather than silently choosing an attacker-controlled value.
func ParseItemQuery(values url.Values) (ItemQuery, error) {
	query := ItemQuery{
		Limit:                  DefaultQueryLimit,
		RequestedLimit:         DefaultQueryLimit,
		SortOrder:              "Ascending",
		EnableUserData:         true,
		EnableImages:           true,
		ImageTypeLimit:         1,
		EnableTotalRecordCount: true,
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
	if query.RequestedLimit, err = boundedInteger(values, "Limit", 0, MaximumLatestQueryLimit, DefaultQueryLimit); err != nil {
		return ItemQuery{}, err
	}
	query.Limit = min(query.RequestedLimit, MaximumQueryLimit)
	if query.Recursive, err = booleanValue(values, "Recursive", false); err != nil {
		return ItemQuery{}, err
	}
	if query.EnableUserData, err = booleanValue(values, "EnableUserData", true); err != nil {
		return ItemQuery{}, err
	}
	if query.EnableImages, err = booleanValue(values, "EnableImages", true); err != nil {
		return ItemQuery{}, err
	}
	if query.EnableTotalRecordCount, err = booleanValue(values, "EnableTotalRecordCount", true); err != nil {
		return ItemQuery{}, err
	}
	if query.ImageTypeLimit, err = boundedInteger(values, "ImageTypeLimit", 0, MaximumQueryListValues, 1); err != nil {
		return ItemQuery{}, err
	}
	for name, destination := range map[string]*[]string{
		"IncludeItemTypes": &query.IncludeItemTypes,
		"ExcludeItemTypes": &query.ExcludeItemTypes,
		"MediaTypes":       &query.MediaTypes,
		"Filters":          &query.Filters,
		"Fields":           &query.Fields,
		"SortBy":           &query.SortBy,
		"GenreIds":         &query.GenreIds,
		"PersonIds":        &query.PersonIds,
		"EnableImageTypes": &query.EnableImageTypes,
	} {
		if *destination, err = commaSeparated(values, name); err != nil {
			return ItemQuery{}, err
		}
	}
	for name, destination := range map[string]*[]string{
		"Genres":          &query.Genres,
		"OfficialRatings": &query.OfficialRatings,
		"Tags":            &query.Tags,
		"Studios":         &query.Studios,
	} {
		if *destination, err = pipeSeparated(values, name); err != nil {
			return ItemQuery{}, err
		}
	}
	if query.Ids, err = canonicalIDs(values, "Ids"); err != nil {
		return ItemQuery{}, err
	}
	if query.Years, err = integerList(values, "Years", 1, 9999); err != nil {
		return ItemQuery{}, err
	}
	if query.IsPlayed, err = optionalBooleanValue(values, "IsPlayed"); err != nil {
		return ItemQuery{}, err
	}
	if query.IsFavorite, err = optionalBooleanValue(values, "IsFavorite"); err != nil {
		return ItemQuery{}, err
	}
	if query.IsResumable, err = optionalBooleanValue(values, "IsResumable"); err != nil {
		return ItemQuery{}, err
	}
	if query.HasTrailer, err = optionalBooleanValue(values, "HasTrailer"); err != nil {
		return ItemQuery{}, err
	}
	if query.MinCommunityRating, err = optionalBoundedFloat(values, "MinCommunityRating", 0, 10); err != nil {
		return ItemQuery{}, err
	}
	if query.HasSubtitles, err = optionalBooleanValue(values, "HasSubtitles"); err != nil {
		return ItemQuery{}, err
	}
	filters := query.Filters[:0]
	for _, filter := range query.Filters {
		switch strings.ToLower(filter) {
		case "isplayed", "isunplayed", "isfavorite", "isresumable", "isfolder", "isnotfolder":
			filters = append(filters, strings.ToLower(filter))
		}
	}
	query.Filters = filters
	fields := query.Fields[:0]
	for _, field := range query.Fields {
		if supportedItemField(field) {
			fields = append(fields, field)
		}
	}
	query.Fields = fields
	imageTypes := query.EnableImageTypes[:0]
	for _, imageType := range query.EnableImageTypes {
		if supportedItemImageType(imageType) {
			imageTypes = append(imageTypes, imageType)
		}
	}
	query.EnableImageTypes = imageTypes

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

func requestedItemQuery(query ItemQuery) ItemQuery {
	query.Limit = query.RequestedLimit
	return query
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
	if !validQueryText(value) || len(value) > maximum {
		return "", invalidQuery(name + " contains an invalid value")
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

func optionalBooleanValue(values url.Values, name string) (*bool, error) {
	raw, found, err := queryScalar(values, name)
	if err != nil || !found {
		return nil, err
	}
	value, parseErr := strconv.ParseBool(raw)
	if parseErr != nil {
		return nil, invalidQuery(name + " must be true or false")
	}
	return &value, nil
}

func optionalBoundedFloat(values url.Values, name string, minimum, maximum float64) (*float64, error) {
	raw, ok, err := queryScalar(values, name)
	if err != nil || !ok {
		return nil, err
	}
	value, parseErr := strconv.ParseFloat(raw, 64)
	if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		return nil, invalidQuery(fmt.Sprintf("%s must be between %g and %g", name, minimum, maximum))
	}
	return &value, nil
}

func commaSeparated(values url.Values, name string) ([]string, error) {
	return delimitedValues(values, name, ',')
}

func pipeSeparated(values url.Values, name string) ([]string, error) {
	return delimitedValues(values, name, '|')
}

func delimitedValues(values url.Values, name string, separator rune) ([]string, error) {
	matchingNames := make([]string, 0, 1)
	for actualName := range values {
		if strings.EqualFold(actualName, name) {
			matchingNames = append(matchingNames, actualName)
		}
	}
	sort.Strings(matchingNames)
	rawValues := make([]string, 0, len(matchingNames))
	for _, actualName := range matchingNames {
		rawValues = append(rawValues, values[actualName]...)
	}
	if len(rawValues) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		for _, part := range strings.Split(raw, string(separator)) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !validQueryText(part) || len(part) > MaximumQueryValueBytes {
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

func validQueryText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func supportedItemField(value string) bool {
	switch {
	case strings.EqualFold(value, "AirTime"), strings.EqualFold(value, "BasicSyncInfo"),
		strings.EqualFold(value, "CanDelete"), strings.EqualFold(value, "CanDownload"),
		strings.EqualFold(value, "ChannelImage"), strings.EqualFold(value, "ChannelInfo"),
		strings.EqualFold(value, "Chapters"), strings.EqualFold(value, "ChildCount"),
		strings.EqualFold(value, "CumulativeRunTimeTicks"), strings.EqualFold(value, "CustomRating"),
		strings.EqualFold(value, "DateCreated"), strings.EqualFold(value, "DateLastMediaAdded"),
		strings.EqualFold(value, "DateLastRefreshed"), strings.EqualFold(value, "DateLastSaved"),
		strings.EqualFold(value, "DisplayPreferencesId"), strings.EqualFold(value, "EnableMediaSourceDisplay"),
		strings.EqualFold(value, "Etag"), strings.EqualFold(value, "ExternalEtag"),
		strings.EqualFold(value, "ExternalSeriesId"), strings.EqualFold(value, "ExternalUrls"),
		strings.EqualFold(value, "ExtraIds"), strings.EqualFold(value, "Genres"),
		strings.EqualFold(value, "Height"), strings.EqualFold(value, "HomePageUrl"),
		strings.EqualFold(value, "InheritedParentalRatingValue"), strings.EqualFold(value, "IsHD"),
		strings.EqualFold(value, "ItemCounts"), strings.EqualFold(value, "LocalTrailerCount"),
		strings.EqualFold(value, "MediaSourceCount"), strings.EqualFold(value, "MediaSources"),
		strings.EqualFold(value, "MediaStreams"), strings.EqualFold(value, "OriginalTitle"),
		strings.EqualFold(value, "Overview"), strings.EqualFold(value, "ParentId"),
		strings.EqualFold(value, "Path"), strings.EqualFold(value, "People"),
		strings.EqualFold(value, "PlayAccess"), strings.EqualFold(value, "PresentationUniqueKey"),
		strings.EqualFold(value, "ProductionLocations"), strings.EqualFold(value, "ProviderIds"),
		strings.EqualFold(value, "PrimaryImageAspectRatio"), strings.EqualFold(value, "RecursiveItemCount"),
		strings.EqualFold(value, "RefreshState"), strings.EqualFold(value, "RemoteTrailers"),
		strings.EqualFold(value, "ScreenshotImageTags"), strings.EqualFold(value, "SeasonUserData"),
		strings.EqualFold(value, "SeriesPresentationUniqueKey"), strings.EqualFold(value, "SeriesPrimaryImage"),
		strings.EqualFold(value, "SeriesStudio"), strings.EqualFold(value, "ServiceName"),
		strings.EqualFold(value, "Settings"), strings.EqualFold(value, "SortName"),
		strings.EqualFold(value, "SpecialEpisodeNumbers"), strings.EqualFold(value, "SpecialFeatureCount"),
		strings.EqualFold(value, "Studios"), strings.EqualFold(value, "SyncInfo"),
		strings.EqualFold(value, "Taglines"), strings.EqualFold(value, "Tags"),
		strings.EqualFold(value, "ThemeSongIds"), strings.EqualFold(value, "ThemeVideoIds"),
		strings.EqualFold(value, "Trickplay"), strings.EqualFold(value, "Width"):
		return true
	default:
		return false
	}
}

func supportedItemImageType(value string) bool {
	for _, imageType := range compatImageTypes {
		if strings.EqualFold(value, imageType) {
			return true
		}
	}
	return false
}

func integerList(values url.Values, name string, minimum, maximum int) ([]int, error) {
	parts, err := commaSeparated(values, name)
	if err != nil || len(parts) == 0 {
		return nil, err
	}
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		value, parseErr := strconv.ParseInt(part, 10, 32)
		if parseErr != nil || value < int64(minimum) || value > int64(maximum) {
			return nil, invalidQuery(fmt.Sprintf("%s values must be between %d and %d", name, minimum, maximum))
		}
		result = append(result, int(value))
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
