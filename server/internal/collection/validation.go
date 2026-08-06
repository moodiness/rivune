package collection

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maximumCollections      = 100
	maximumFolders          = 100
	maximumSourcesPerFolder = 20
	maximumExtrasPerSource  = 32

	MaximumImportDocumentBytes = 16 * 1024 * 1024
	maximumImportFolders       = 1000
	maximumImportSources       = 5000
	maximumImportExtras        = 10000
	maximumImportFilterValues  = 10000
	maximumImportProfileIDs    = 1000
	maximumImportArtworkKeys   = 4096
	maximumImportStringBytes   = 4 * 1024 * 1024
)

var (
	datePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}$`)
	regionPattern   = regexp.MustCompile(`^[A-Za-z]{2}$`)
)

func validateImportDocumentBudget(document ExportDocument) error {
	var folders, sources, extras, filterValues, assignmentIDs, artworkKeys, stringBytes int
	addCount := func(total *int, value, limit int) bool {
		if value < 0 || value > limit-*total {
			return false
		}
		*total += value
		return true
	}
	addString := func(value string) bool {
		return addCount(&stringBytes, len(value), maximumImportStringBytes)
	}
	addArtwork := func(value string) bool {
		if !addString(value) {
			return false
		}
		if strings.HasPrefix(value, "/api/v1/artwork/") {
			return addCount(&artworkKeys, 1, maximumImportArtworkKeys)
		}
		return true
	}
	for collectionIndex := range document.Collections {
		input := &document.Collections[collectionIndex]
		if !addCount(&folders, len(input.Folders), maximumImportFolders) ||
			!addCount(&assignmentIDs, len(input.ProfileIDs)+len(input.CategoryIDs), maximumImportProfileIDs) ||
			!addString(input.Title) || !addArtwork(input.BackdropImageURL) ||
			!addString(input.ViewMode) || !addString(input.FolderCoverShape) {
			return invalid("collection import exceeds the document complexity limit")
		}
		for _, profileID := range input.ProfileIDs {
			if !addString(profileID) {
				return invalid("collection import exceeds the document complexity limit")
			}
		}
		for _, categoryID := range input.CategoryIDs {
			if !addString(categoryID) {
				return invalid("collection import exceeds the document complexity limit")
			}
		}
		for folderIndex := range input.Folders {
			folder := &input.Folders[folderIndex]
			if !addCount(&sources, len(folder.Sources), maximumImportSources) ||
				!addString(folder.ID) || !addString(folder.Title) || !addString(folder.TileShape) ||
				!addString(folder.SourceView) || !addArtwork(folder.CoverImageURL) ||
				!addString(folder.CoverEmoji) || !addArtwork(folder.TitleLogoURL) ||
				!addArtwork(folder.HeroBackdropURL) || !addString(folder.HeroVideoURL) ||
				!addString(folder.FocusGIFURL) {
				return invalid("collection import exceeds the document complexity limit")
			}
			for sourceIndex := range folder.Sources {
				source := &folder.Sources[sourceIndex]
				if !addString(source.ID) || !addString(source.Kind) || !addString(source.Title) {
					return invalid("collection import exceeds the document complexity limit")
				}
				if source.AddonCatalog != nil {
					settings := source.AddonCatalog
					if !addCount(&extras, len(settings.Extra), maximumImportExtras) ||
						!addString(settings.AddonID) || !addString(settings.ManifestID) ||
						!addString(settings.Type) || !addString(settings.CatalogID) {
						return invalid("collection import exceeds the document complexity limit")
					}
					for _, extra := range settings.Extra {
						if !addString(extra.Name) || !addString(extra.Value) {
							return invalid("collection import exceeds the document complexity limit")
						}
					}
				}
				if source.TMDB != nil {
					settings := source.TMDB
					filters := &settings.Filters
					if !addCount(&filterValues, len(filters.Genres), maximumImportFilterValues) ||
						!addCount(&filterValues, len(filters.Keywords), maximumImportFilterValues) ||
						!addCount(&filterValues, len(filters.Companies), maximumImportFilterValues) ||
						!addCount(&filterValues, len(filters.Networks), maximumImportFilterValues) ||
						!addCount(&filterValues, len(filters.WatchProviders), maximumImportFilterValues) ||
						!addString(settings.SourceType) || !addString(settings.MediaType) ||
						!addString(settings.Sort) || !addString(filters.ReleaseDateFrom) ||
						!addString(filters.ReleaseDateTo) || !addString(filters.OriginalLanguage) ||
						!addString(filters.OriginCountry) || !addString(filters.WatchRegion) {
						return invalid("collection import exceeds the document complexity limit")
					}
				}
				if source.Trakt != nil &&
					(!addString(source.Trakt.MediaType) || !addString(source.Trakt.SortBy) || !addString(source.Trakt.SortHow)) {
					return invalid("collection import exceeds the document complexity limit")
				}
				if source.MDBList != nil &&
					(!addString(source.MDBList.MediaType) || !addString(source.MDBList.Sort) || !addString(source.MDBList.Order)) {
					return invalid("collection import exceeds the document complexity limit")
				}
			}
		}
	}
	return nil
}

func normalizeAndValidate(input SaveInput, updating bool) (SaveInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.BackdropImageURL = strings.TrimSpace(input.BackdropImageURL)
	input.ViewMode = strings.ToLower(strings.TrimSpace(input.ViewMode))
	input.FolderCoverShape = strings.ToLower(strings.TrimSpace(input.FolderCoverShape))
	if input.FolderCoverShape == "" {
		input.FolderCoverShape = TileShapePoster
	}
	if input.ViewMode == "" {
		input.ViewMode = ViewModeTabbedGrid
	}
	if !validText(input.Title, 1, 120) {
		return SaveInput{}, invalid("collection title must contain 1 to 120 characters")
	}
	if err := validateURL(input.BackdropImageURL, "collection backdrop"); err != nil {
		return SaveInput{}, err
	}
	if input.ViewMode != ViewModeTabbedGrid && input.ViewMode != ViewModeRows && input.ViewMode != ViewModeFollowLayout {
		return SaveInput{}, invalid("unsupported collection view mode")
	}
	if input.FolderCoverShape != TileShapePoster && input.FolderCoverShape != TileShapeLandscape && input.FolderCoverShape != TileShapeSquare {
		return SaveInput{}, invalid("unsupported folder cover shape")
	}
	if len(input.Folders) < 1 || len(input.Folders) > maximumFolders {
		return SaveInput{}, invalid("a collection must contain 1 to 100 folders")
	}
	seenFolders := make(map[string]struct{}, len(input.Folders))
	seenSources := make(map[string]struct{})
	for folderIndex := range input.Folders {
		folder := &input.Folders[folderIndex]
		folder.Title = strings.TrimSpace(folder.Title)
		folder.TileShape = strings.ToLower(strings.TrimSpace(folder.TileShape))
		if folder.TileShape == "" {
			folder.TileShape = TileShapeSquare
		}
		folder.SourceView = strings.ToLower(strings.TrimSpace(folder.SourceView))
		if folder.SourceView == "" {
			folder.SourceView = SourceViewMerged
		}
		if !validText(folder.Title, 1, 120) {
			return SaveInput{}, invalid("folder title must contain 1 to 120 characters")
		}
		if folder.TileShape != TileShapePoster && folder.TileShape != TileShapeLandscape && folder.TileShape != TileShapeSquare {
			return SaveInput{}, invalid("unsupported folder tile shape")
		}
		if folder.SourceView != SourceViewMerged && folder.SourceView != SourceViewCategories && folder.SourceView != SourceViewFolders {
			return SaveInput{}, invalid("unsupported folder source view")
		}
		if folder.ID == "" {
			folder.ID = newUUID()
		} else if !validUUID(folder.ID) {
			return SaveInput{}, invalid("folder ID must be a UUID")
		}
		if _, duplicate := seenFolders[folder.ID]; duplicate {
			return SaveInput{}, invalid("folder IDs must be unique")
		}
		seenFolders[folder.ID] = struct{}{}
		folder.CoverImageURL = strings.TrimSpace(folder.CoverImageURL)
		folder.CoverEmoji = strings.TrimSpace(folder.CoverEmoji)
		folder.TitleLogoURL = strings.TrimSpace(folder.TitleLogoURL)
		folder.HeroBackdropURL = strings.TrimSpace(folder.HeroBackdropURL)
		folder.HeroVideoURL = strings.TrimSpace(folder.HeroVideoURL)
		folder.FocusGIFURL = strings.TrimSpace(folder.FocusGIFURL)
		for label, value := range map[string]string{
			"folder cover": folder.CoverImageURL, "folder title logo": folder.TitleLogoURL,
			"folder hero backdrop": folder.HeroBackdropURL, "folder hero video": folder.HeroVideoURL,
			"folder focus GIF": folder.FocusGIFURL,
		} {
			if err := validateURL(value, label); err != nil {
				return SaveInput{}, err
			}
		}
		if utf8.RuneCountInString(folder.CoverEmoji) > 16 {
			return SaveInput{}, invalid("folder cover emoji must not exceed 16 characters")
		}
		if folder.FocusGIFEnabled && folder.FocusGIFURL == "" {
			return SaveInput{}, invalid("focus GIF URL is required when focus GIF is enabled")
		}
		if len(folder.Sources) < 1 || len(folder.Sources) > maximumSourcesPerFolder {
			return SaveInput{}, invalid("each folder must contain 1 to 20 sources")
		}
		for sourceIndex := range folder.Sources {
			source := &folder.Sources[sourceIndex]
			if source.ID == "" {
				source.ID = newUUID()
			} else if !validUUID(source.ID) {
				return SaveInput{}, invalid("source ID must be a UUID")
			}
			if _, duplicate := seenSources[source.ID]; duplicate {
				return SaveInput{}, invalid("source IDs must be unique within a collection")
			}
			seenSources[source.ID] = struct{}{}
			if err := normalizeSource(source); err != nil {
				return SaveInput{}, fmt.Errorf("%w: folder %q: %v", ErrInvalidInput, folder.Title, err)
			}
		}
	}
	if updating && input.ExpectedVersion < 1 {
		return SaveInput{}, invalid("expectedVersion must be at least 1")
	}
	return input, nil
}

func normalizeSource(source *Source) error {
	source.Kind = strings.ToLower(strings.TrimSpace(source.Kind))
	source.Title = strings.TrimSpace(source.Title)
	if !validText(source.Title, 1, 120) {
		return errorsText("source title must contain 1 to 120 characters")
	}
	switch source.Kind {
	case SourceKindAddonCatalog:
		if source.AddonCatalog == nil || source.TMDB != nil || source.Trakt != nil || source.MDBList != nil {
			return errorsText("addon_catalog source must contain only addonCatalog settings")
		}
		settings := source.AddonCatalog
		settings.AddonID = strings.TrimSpace(settings.AddonID)
		settings.ManifestID = strings.TrimSpace(settings.ManifestID)
		settings.Type = strings.TrimSpace(settings.Type)
		settings.CatalogID = strings.TrimSpace(settings.CatalogID)
		if !validUUID(settings.AddonID) || settings.ManifestID != "" && !validText(settings.ManifestID, 1, 512) || !validText(settings.Type, 1, 256) || !validText(settings.CatalogID, 1, 2048) {
			return errorsText("addon catalog reference is invalid")
		}
		if len(settings.Extra) > maximumExtrasPerSource {
			return errorsText("addon catalog source has too many extras")
		}
		seen := make(map[string]struct{}, len(settings.Extra))
		for index := range settings.Extra {
			settings.Extra[index].Name = strings.TrimSpace(settings.Extra[index].Name)
			settings.Extra[index].Value = strings.TrimSpace(settings.Extra[index].Value)
			if !validText(settings.Extra[index].Name, 1, 256) || !validText(settings.Extra[index].Value, 1, 4096) {
				return errorsText("addon catalog extra is invalid")
			}
			if _, duplicate := seen[settings.Extra[index].Name]; duplicate {
				return errorsText("addon catalog extra names must be unique")
			}
			seen[settings.Extra[index].Name] = struct{}{}
		}
	case SourceKindTMDB:
		if source.TMDB == nil || source.AddonCatalog != nil || source.Trakt != nil || source.MDBList != nil {
			return errorsText("tmdb source must contain only TMDB settings")
		}
		return normalizeTMDB(source.TMDB)
	case SourceKindTrakt:
		if source.Trakt == nil || source.AddonCatalog != nil || source.TMDB != nil || source.MDBList != nil {
			return errorsText("trakt source must contain only Trakt settings")
		}
		return normalizeTrakt(source.Trakt)
	case SourceKindMDBList:
		if source.MDBList == nil || source.AddonCatalog != nil || source.TMDB != nil || source.Trakt != nil {
			return errorsText("mdblist source must contain only MDBList settings")
		}
		return normalizeMDBList(source.MDBList)
	default:
		return errorsText("unsupported source kind")
	}
	return nil
}

func normalizeTMDB(source *TMDBSource) error {
	source.SourceType = strings.ToLower(strings.TrimSpace(source.SourceType))
	source.MediaType = strings.ToLower(strings.TrimSpace(source.MediaType))
	source.Sort = strings.ToLower(strings.TrimSpace(source.Sort))
	if source.SourceType == "" {
		source.SourceType = "discover"
	}
	if source.MediaType == "" {
		source.MediaType = MediaTypeMovie
	}
	if source.Sort == "" {
		source.Sort = "popularity.desc"
	}
	allowedTypes := map[string]bool{"list": true, "collection": true, "company": true, "network": true, "discover": true, "person": true, "director": true}
	if !allowedTypes[source.SourceType] {
		return errorsText("unsupported TMDB source type")
	}
	if source.MediaType != MediaTypeMovie && source.MediaType != MediaTypeSeries && source.MediaType != MediaTypeBoth {
		return errorsText("TMDB media type must be movie, series, or both")
	}
	if source.SourceType != "discover" && (source.TMDBID == nil || *source.TMDBID < 1) {
		return errorsText("TMDB source ID is required")
	}
	switch source.SourceType {
	case "network":
		source.MediaType = MediaTypeSeries
	case "list", "collection":
		source.MediaType = MediaTypeMovie
	}
	allowedSort := map[string]bool{
		"original": true, "popularity.desc": true, "vote_average.desc": true,
		"vote_count.desc": true, "release_date.desc": true, "first_air_date.desc": true,
	}
	if !allowedSort[source.Sort] {
		return errorsText("unsupported TMDB sort")
	}
	filters := &source.Filters
	if filters.ReleaseDateFrom != "" && !datePattern.MatchString(filters.ReleaseDateFrom) || filters.ReleaseDateTo != "" && !datePattern.MatchString(filters.ReleaseDateTo) {
		return errorsText("TMDB dates must use YYYY-MM-DD")
	}
	if filters.VoteAverageMin != nil && (*filters.VoteAverageMin < 0 || *filters.VoteAverageMin > 10) || filters.VoteAverageMax != nil && (*filters.VoteAverageMax < 0 || *filters.VoteAverageMax > 10) {
		return errorsText("TMDB vote averages must be between 0 and 10")
	}
	if filters.VoteAverageMin != nil && filters.VoteAverageMax != nil && *filters.VoteAverageMin > *filters.VoteAverageMax {
		return errorsText("TMDB minimum vote average exceeds maximum")
	}
	if filters.VoteCountMin != nil && *filters.VoteCountMin < 0 {
		return errorsText("TMDB minimum vote count must not be negative")
	}
	if filters.Year != nil && (*filters.Year < 1870 || *filters.Year > 2200) {
		return errorsText("TMDB year is outside the supported range")
	}
	filters.OriginalLanguage = strings.ToLower(strings.TrimSpace(filters.OriginalLanguage))
	filters.OriginCountry = strings.ToUpper(strings.TrimSpace(filters.OriginCountry))
	filters.WatchRegion = strings.ToUpper(strings.TrimSpace(filters.WatchRegion))
	if filters.OriginalLanguage != "" && !languagePattern.MatchString(filters.OriginalLanguage) || filters.OriginCountry != "" && !regionPattern.MatchString(filters.OriginCountry) || filters.WatchRegion != "" && !regionPattern.MatchString(filters.WatchRegion) {
		return errorsText("TMDB language and regions are invalid")
	}
	for _, values := range [][]int64{filters.Genres, filters.Keywords, filters.Companies, filters.Networks, filters.WatchProviders} {
		if len(values) > 100 {
			return errorsText("TMDB filter contains too many IDs")
		}
		seen := make(map[int64]struct{}, len(values))
		for _, value := range values {
			if value < 1 {
				return errorsText("TMDB filter IDs must be positive")
			}
			if _, duplicate := seen[value]; duplicate {
				return errorsText("TMDB filter IDs must be unique")
			}
			seen[value] = struct{}{}
		}
	}
	return nil
}

func normalizeTrakt(source *TraktSource) error {
	source.MediaType = strings.ToLower(strings.TrimSpace(source.MediaType))
	source.SortBy = strings.ToLower(strings.TrimSpace(source.SortBy))
	source.SortHow = strings.ToLower(strings.TrimSpace(source.SortHow))
	if source.MediaType == "" {
		source.MediaType = MediaTypeMovie
	}
	if source.SortBy == "" {
		source.SortBy = "rank"
	}
	if source.SortHow == "" {
		source.SortHow = "asc"
	}
	if source.ListID < 1 || source.MediaType != MediaTypeMovie && source.MediaType != MediaTypeSeries {
		return errorsText("Trakt list ID and media type are invalid")
	}
	allowedSort := map[string]bool{"rank": true, "added": true, "title": true, "released": true, "runtime": true, "popularity": true, "percentage": true, "votes": true}
	if !allowedSort[source.SortBy] || source.SortHow != "asc" && source.SortHow != "desc" {
		return errorsText("unsupported Trakt sort")
	}
	return nil
}

func normalizeMDBList(source *MDBListSource) error {
	source.MediaType = strings.ToLower(strings.TrimSpace(source.MediaType))
	source.Sort = strings.ToLower(strings.TrimSpace(source.Sort))
	source.Order = strings.ToLower(strings.TrimSpace(source.Order))
	if source.MediaType == "" {
		source.MediaType = MediaTypeMovie
	}
	if source.Sort == "" {
		source.Sort = "rank"
	}
	if source.Order == "" {
		source.Order = "asc"
	}
	if source.ListID < 1 || source.MediaType != MediaTypeMovie && source.MediaType != MediaTypeSeries {
		return errorsText("MDBList list ID and media type are invalid")
	}
	switch source.Sort {
	case "added", "budget", "imdbpopular", "imdbrating", "imdbvotes", "last_air_date",
		"letterrating", "lettervotes", "metacritic", "myanimelist", "random", "rank",
		"released", "releasedigital", "revenue", "rogerebert", "rtaudience", "rtomatoes",
		"runtime", "score", "score_average", "sort_title", "title", "tmdbpopular", "usort":
	default:
		return errorsText("unsupported MDBList sort")
	}
	if source.Order != "asc" && source.Order != "desc" {
		return errorsText("unsupported MDBList sort order")
	}
	return nil
}

func validateURL(value, label string) error {
	if value == "" {
		return nil
	}
	if len(value) > 8192 {
		return invalid(label + " URL is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return invalid(label + " must be an absolute HTTP(S) URL without credentials or fragment")
	}
	return nil
}

func validText(value string, minimum, maximum int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= minimum && utf8.RuneCountInString(value) <= maximum
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, message)
}

type errorsText string

func (message errorsText) Error() string { return string(message) }

func newUUID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
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
