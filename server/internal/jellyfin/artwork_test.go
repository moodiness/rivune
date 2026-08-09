package jellyfin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	artworkdomain "github.com/moodiness/rivune/server/internal/artwork"
	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	artworkTokenOne       = "rivune_jf_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	artworkTokenTwo       = "rivune_jf_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	artworkProfileOne     = "10000000-0000-4000-8000-000000000001"
	artworkProfileTwo     = "20000000-0000-4000-8000-000000000002"
	artworkItemOne        = "30000000-0000-4000-8000-000000000003"
	artworkItemStale      = "40000000-0000-4000-8000-000000000004"
	artworkItemInvalidKey = "50000000-0000-4000-8000-000000000005"
)

type artworkAuthentication struct {
	sessions map[string]AuthenticatedSession
}

func (*artworkAuthentication) Login(context.Context, CompatLoginInput) (LoginResult, error) {
	return LoginResult{}, errors.New("login is not used by artwork handlers")
}

func (authentication *artworkAuthentication) Authenticate(_ context.Context, token string) (AuthenticatedSession, error) {
	session, ok := authentication.sessions[token]
	if !ok {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return session, nil
}
func (*artworkAuthentication) Revalidate(_ context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	return expected, nil
}

func (*artworkAuthentication) Logout(context.Context, AuthenticatedSession) error {
	return errors.New("logout is not used by artwork handlers")
}

type artworkCatalog struct {
	items map[string]map[string]watchstate.CatalogTitle
	calls int
}

func (catalog *artworkCatalog) GetCatalogTitle(_ context.Context, principal auth.Principal, itemID string) (watchstate.CatalogTitle, error) {
	catalog.calls++
	if principal.ActiveProfileID == nil {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	item, ok := catalog.items[*principal.ActiveProfileID][itemID]
	if !ok {
		return watchstate.CatalogTitle{}, watchstate.ErrNotFound
	}
	return item, nil
}

func (*artworkCatalog) ListCatalogItems(context.Context, auth.Principal, watchstate.CatalogQuery) (watchstate.CatalogPage, error) {
	return watchstate.CatalogPage{}, errors.New("list is not used by artwork handlers")
}

type enrichedArtworkCatalog struct {
	*artworkCatalog
	enriched watchstate.CatalogTitle
}

func (catalog *enrichedArtworkCatalog) EnrichCatalogTitle(_ context.Context, _ auth.Principal, title watchstate.CatalogTitle) (watchstate.CatalogTitle, error) {
	if title.ID != catalog.enriched.ID {
		return title, nil
	}
	return catalog.enriched, nil
}

type artworkDelivery struct {
	keys       map[string]string
	body       []byte
	lookup     []string
	servedKeys []string
	metadata   map[string]artworkdomain.ImageMetadata
}

func (delivery *artworkDelivery) LookupKey(_ context.Context, materialized string) (string, bool) {
	delivery.lookup = append(delivery.lookup, materialized)
	key, ok := delivery.keys[materialized]
	return key, ok
}

func (delivery *artworkDelivery) DescribeKey(_ context.Context, key string) (artworkdomain.ImageMetadata, bool) {
	value, found := delivery.metadata[key]
	return value, found
}

func (delivery *artworkDelivery) ServeKey(response http.ResponseWriter, request *http.Request, key string) {
	delivery.servedKeys = append(delivery.servedKeys, key)
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("Content-Length", "8")
	response.Header().Set("ETag", `"`+key+`"`)
	response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(delivery.body)
	}
}

func TestImageInfosAreAuthenticatedProfileScopedAndOpaque(t *testing.T) {
	primaryTag := strings.Repeat("a", 64)
	backdropTag := strings.Repeat("b", 64)
	logoTag := strings.Repeat("c", 64)
	bannerTag := strings.Repeat("d", 64)
	artTag := strings.Repeat("e", 64)
	privateURL := "https://provider.invalid/private/poster.jpg?token=provider-secret"
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne: {
				ID: artworkItemOne, MediaType: "movie",
				PosterURL: localizedArtworkPrefix + primaryTag, BackgroundURL: localizedArtworkPrefix + backdropTag,
				LogoURL: localizedArtworkPrefix + logoTag, BannerURL: localizedArtworkPrefix + bannerTag,
				ArtURL: localizedArtworkPrefix + artTag,
			},
			artworkItemStale: {ID: artworkItemStale, MediaType: "series", PosterURL: privateURL},
		},
	}, map[string]string{
		localizedArtworkPrefix + primaryTag: primaryTag, localizedArtworkPrefix + backdropTag: backdropTag,
		localizedArtworkPrefix + logoTag: logoTag, localizedArtworkPrefix + bannerTag: bannerTag,
		localizedArtworkPrefix + artTag: artTag,
	})
	delivery.metadata = map[string]artworkdomain.ImageMetadata{
		primaryTag: {Width: 1200, Height: 1800, Size: 8192},
	}

	request := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images", nil)
	request.Header.Set("X-Emby-Token", artworkTokenOne)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var infos []ImageInfo
	if err := json.Unmarshal(response.Body.Bytes(), &infos); err != nil {
		t.Fatalf("decode image infos: %v", err)
	}
	wantTypes := []string{"Primary", "Backdrop", "Logo", "Thumb", "Banner", "Art"}
	wantTags := []string{primaryTag, backdropTag, logoTag, primaryTag, bannerTag, artTag}
	if response.Code != http.StatusOK || len(infos) != len(wantTypes) {
		t.Fatalf("image infos status=%d infos=%+v body=%s", response.Code, infos, response.Body.String())
	}
	for index := range wantTypes {
		if infos[index].ImageType != wantTypes[index] || infos[index].ImageIndex != 0 || infos[index].ImageTag != wantTags[index] {
			t.Fatalf("image info %d=%+v want type=%s tag=%s", index, infos[index], wantTypes[index], wantTags[index])
		}
	}
	if infos[0].Width == nil || *infos[0].Width != 1200 || infos[0].Height == nil || *infos[0].Height != 1800 ||
		infos[0].Size == nil || *infos[0].Size != 8192 || infos[3].Width == nil || *infos[3].Width != 1200 {
		t.Fatalf("authoritative cached image metadata was not projected: %+v", infos)
	}
	body := response.Body.String()
	for _, forbidden := range []string{"Path", "Url", "URL", "provider.invalid", "provider-secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("image infos exposed %q: %s", forbidden, body)
		}
	}
	delete(delivery.keys, localizedArtworkPrefix+primaryTag)
	removedRequest := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images", nil)
	removedRequest.Header.Set("X-Emby-Token", artworkTokenOne)
	removedResponse := httptest.NewRecorder()
	handler.ServeHTTP(removedResponse, removedRequest)
	var afterRemoval []ImageInfo
	if err := json.Unmarshal(removedResponse.Body.Bytes(), &afterRemoval); err != nil || removedResponse.Code != http.StatusOK ||
		len(afterRemoval) != 4 || afterRemoval[0].ImageType != "Backdrop" || afterRemoval[0].ImageTag != backdropTag ||
		afterRemoval[1].ImageType != "Logo" || afterRemoval[2].ImageType != "Banner" || afterRemoval[3].ImageType != "Art" {
		t.Fatalf("removed image metadata status=%d infos=%+v error=%v", removedResponse.Code, afterRemoval, err)
	}

	emptyRequest := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemStale+"/Images", nil)
	emptyRequest.Header.Set("X-Emby-Token", artworkTokenOne)
	emptyResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyResponse, emptyRequest)
	if emptyResponse.Code != http.StatusOK || emptyResponse.Body.String() != "[]\n" {
		t.Fatalf("unprojected image infos status=%d body=%q", emptyResponse.Code, emptyResponse.Body.String())
	}

	callsBefore := catalog.calls
	crossProfile := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images?UserId="+artworkProfileTwo, nil)
	crossProfile.Header.Set("X-Emby-Token", artworkTokenOne)
	crossProfileResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossProfileResponse, crossProfile)
	if crossProfileResponse.Code != http.StatusNotFound || catalog.calls != callsBefore {
		t.Fatalf("cross-profile image infos status=%d catalog calls=%d want %d", crossProfileResponse.Code, catalog.calls, callsBefore)
	}

	foreignItem := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images", nil)
	foreignItem.Header.Set("X-Emby-Token", artworkTokenTwo)
	foreignResponse := httptest.NewRecorder()
	handler.ServeHTTP(foreignResponse, foreignItem)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign image infos status=%d body=%q", foreignResponse.Code, foreignResponse.Body.String())
	}
	if len(delivery.lookup) != 10 || len(delivery.servedKeys) != 0 {
		t.Fatalf("image metadata registration checks=%v served=%v", delivery.lookup, delivery.servedKeys)
	}
}

func TestArtworkHEADUsesAuthoritativeEnrichedTitleArtwork(t *testing.T) {
	primaryTag := strings.Repeat("a", 64)
	handler, baseCatalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne: {ID: artworkItemOne, MediaType: "movie", Title: "Linked title without a persisted poster"},
		},
	}, map[string]string{localizedArtworkPrefix + primaryTag: primaryTag})
	handler.catalog = &enrichedArtworkCatalog{
		artworkCatalog: baseCatalog,
		enriched: watchstate.CatalogTitle{
			ID: artworkItemOne, MediaType: "movie", Title: "Authoritative detail",
			PosterURL: localizedArtworkPrefix + primaryTag,
		},
	}
	request := httptest.NewRequest(http.MethodHead, "/Items/"+artworkItemOne+"/Images/Primary", nil)
	request.Header.Set("X-Emby-Token", artworkTokenOne)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("ETag") != `"`+primaryTag+`"` ||
		len(delivery.servedKeys) != 1 || delivery.servedKeys[0] != primaryTag {
		t.Fatalf("enriched artwork HEAD status=%d headers=%v body=%q served=%v", response.Code, response.Header(), response.Body.String(), delivery.servedKeys)
	}
}

func TestArtworkAuthenticationRejectionDiagnosticIsScrubbed(t *testing.T) {
	handler, _, _ := newArtworkHandler(t, nil, nil)
	handler.authentication = &artworkAuthentication{sessions: map[string]AuthenticatedSession{}}
	var logs bytes.Buffer
	handler.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	validPrivateTag := strings.Repeat("e", 64)

	tests := []struct {
		name                    string
		target                  string
		indexed                 bool
		wantType                string
		wantTagPresent          bool
		wantTagValid            bool
		wantCredentialTransport bool
		forbidden               []string
	}{
		{
			name: "missing tag and credential", target: "/Items/" + artworkItemOne + "/Images/Primary",
			wantType: "Primary", forbidden: []string{artworkItemOne},
		},
		{
			name: "invalid credential", target: "/Items/" + artworkItemOne + "/Images/Backdrop?api_key=" + artworkTokenOne,
			wantType: "Backdrop", wantCredentialTransport: true,
			forbidden: []string{artworkTokenOne, artworkItemOne},
		},
		{
			name: "valid tag with invalid credential", target: "/Items/" + artworkItemOne + "/Images/Thumb/0?tag=" + validPrivateTag + "&ApiKey=" + artworkTokenOne,
			indexed: true, wantType: "Thumb", wantTagPresent: true, wantTagValid: true, wantCredentialTransport: true,
			forbidden: []string{validPrivateTag, artworkTokenOne, artworkItemOne},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs.Reset()
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.SetPathValue("id", artworkItemOne)
			request.SetPathValue("type", test.wantType)
			if test.indexed {
				request.SetPathValue("index", "0")
			}
			response := httptest.NewRecorder()
			if test.indexed {
				handler.handleIndexedImage(response, request)
			} else {
				handler.handleImage(response, request)
			}
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("authentication rejection status=%d body=%s", response.Code, response.Body.String())
			}
			logged := strings.TrimSpace(logs.String())
			for _, forbidden := range test.forbidden {
				if strings.Contains(logged, forbidden) {
					t.Fatalf("image diagnostic disclosed %q: %s", forbidden, logged)
				}
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(logged), &event); err != nil {
				t.Fatalf("decode image diagnostic: %v log=%s", err, logged)
			}
			if event["msg"] != compatImageAuthenticationRejectedMessage || event["image_type"] != test.wantType || event["indexed"] != test.indexed ||
				event["tag_present"] != test.wantTagPresent || event["tag_valid"] != test.wantTagValid || event["credential_transport_present"] != test.wantCredentialTransport {
				t.Fatalf("image diagnostic fields=%#v", event)
			}
			for key := range event {
				switch key {
				case "time", "level", "msg", "image_type", "indexed", "tag_present", "tag_valid", "credential_transport_present":
				default:
					t.Fatalf("image diagnostic exposed unexpected field %q: %#v", key, event)
				}
			}
		})
	}
}

func TestArtworkImageQueryIsBoundedAndRejectsUnsupportedSemantics(t *testing.T) {
	handler, catalog, delivery := newArtworkHandler(t, nil, nil)
	oversized := strings.Repeat("q", maximumCompatRawQueryBytes+1)
	tests := []string{
		"tag=" + oversized,
		"tag=not-a-key",
		"width=0",
		"width=0400",
		"quality=101",
		"fillWidth=10000&fillHeight=10000",
		"width=400&maxWidth=400",
		"fillWidth=400&maxHeight=400",
		"maxWidth=400&MAXWIDTH=400",
		"unsupported=value",
	}
	for _, query := range tests {
		request := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images/Primary?"+query, nil)
		request.SetPathValue("id", artworkItemOne)
		request.SetPathValue("type", "Primary")
		request.Header.Set("X-Emby-Token", artworkTokenOne)
		response := httptest.NewRecorder()
		handler.handleImage(response, request)
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("query %q status=%d headers=%v body=%q", query, response.Code, response.Header(), response.Body.String())
		}
	}
	if catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 0 {
		t.Fatalf("invalid image queries reached services: catalog=%d lookup=%v served=%v", catalog.calls, delivery.lookup, delivery.servedKeys)
	}
}

func TestArtworkGETHEADSelectsEveryRegisteredOrdinaryTitleImageType(t *testing.T) {
	poster := "https://provider.invalid/private/poster.png?token=secret"
	background := localizedArtworkPrefix + strings.Repeat("b", 64)
	logo := localizedArtworkPrefix + strings.Repeat("c", 64)
	banner := localizedArtworkPrefix + strings.Repeat("d", 64)
	art := localizedArtworkPrefix + strings.Repeat("e", 64)
	posterKey := strings.Repeat("a", 64)
	backgroundKey := strings.Repeat("b", 64)
	logoKey := strings.Repeat("c", 64)
	bannerKey := strings.Repeat("d", 64)
	artKey := strings.Repeat("e", 64)
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne: {
				ID: artworkItemOne, MediaType: "episode", PosterURL: poster, BackgroundURL: background,
				LogoURL: logo, BannerURL: banner, ArtURL: art,
			},
		},
	}, map[string]string{poster: posterKey, background: backgroundKey, logo: logoKey, banner: bannerKey, art: artKey})

	types := []struct {
		imageType, key, source string
	}{
		{"Primary", posterKey, poster},
		{"Thumb", posterKey, poster},
		{"Backdrop", backgroundKey, background},
		{"Logo", logoKey, logo},
		{"Banner", bannerKey, banner},
		{"Art", artKey, art},
	}
	for _, image := range types {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(strings.ToLower(image.imageType+"-"+method), func(t *testing.T) {
				indexed := method == http.MethodHead
				path := "/Items/" + artworkItemOne + "/Images/" + image.imageType
				if indexed {
					path += "/0"
				}
				request := httptest.NewRequest(method, path+"?api_key="+artworkTokenOne+"&maxWidth=600&maxHeight=900&quality=90", nil)
				request.SetPathValue("id", artworkItemOne)
				request.SetPathValue("type", image.imageType)
				if indexed {
					request.SetPathValue("index", "0")
				}
				response := httptest.NewRecorder()
				if indexed {
					handler.handleIndexedImage(response, request)
				} else {
					handler.handleImage(response, request)
				}
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" ||
					response.Header().Get("Content-Length") != "8" || response.Header().Get("ETag") != `"`+image.key+`"` ||
					response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Header().Get("Location") != "" {
					t.Fatalf("response status=%d headers=%v", response.Code, response.Header())
				}
				if method == http.MethodGet && response.Body.String() != "pngbytes" || method == http.MethodHead && response.Body.Len() != 0 {
					t.Fatalf("%s %s body=%q", method, image.imageType, response.Body.String())
				}
				if got := delivery.lookup[len(delivery.lookup)-1]; got != image.source {
					t.Fatalf("looked up %q, want %q", got, image.source)
				}
				if got := delivery.servedKeys[len(delivery.servedKeys)-1]; got != image.key {
					t.Fatalf("served key %q, want %q", got, image.key)
				}
			})
		}
	}
	if catalog.calls != len(types)*2 {
		t.Fatalf("catalog calls = %d", catalog.calls)
	}
}

func TestArtworkTaggedGETHEADShareAuthorizedDeliveryContract(t *testing.T) {
	posterKey := strings.Repeat("a", 64)
	backdropKey := strings.Repeat("b", 64)
	poster := localizedArtworkPrefix + posterKey
	backdrop := localizedArtworkPrefix + backdropKey
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne: {ID: artworkItemOne, MediaType: "movie", PosterURL: poster, BackgroundURL: backdrop},
		},
	}, map[string]string{poster: posterKey, backdrop: backdropKey})

	for _, test := range []struct {
		imageType string
		key       string
	}{
		{imageType: "Primary", key: posterKey},
		{imageType: "Thumb", key: posterKey},
		{imageType: "Backdrop", key: backdropKey},
	} {
		for _, indexed := range []bool{false, true} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				path := "/Items/" + artworkItemOne + "/Images/" + test.imageType
				if indexed {
					path += "/0"
				}
				request := httptest.NewRequest(method, path+"?tag="+test.key, nil)
				request.SetPathValue("id", artworkItemOne)
				request.SetPathValue("type", test.imageType)
				if indexed {
					request.SetPathValue("index", "0")
				}
				request.Header.Set("X-Emby-Token", artworkTokenOne)
				response := httptest.NewRecorder()
				if indexed {
					handler.handleIndexedImage(response, request)
				} else {
					handler.handleImage(response, request)
				}
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" ||
					response.Header().Get("Content-Length") != "8" || response.Header().Get("ETag") != `"`+test.key+`"` ||
					response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
					t.Fatalf("%s %s indexed=%t status=%d headers=%v", method, test.imageType, indexed, response.Code, response.Header())
				}
				if method == http.MethodGet && response.Body.String() != "pngbytes" || method == http.MethodHead && response.Body.Len() != 0 {
					t.Fatalf("%s %s indexed=%t body=%q", method, test.imageType, indexed, response.Body.String())
				}
			}
		}
	}
	if catalog.calls != 12 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 12 {
		t.Fatalf("authorized tagged delivery catalog=%d lookup=%v served=%d", catalog.calls, delivery.lookup, len(delivery.servedKeys))
	}
}

func TestArtworkTagIsOnlyAnAuthenticatedItemBoundCacheSelector(t *testing.T) {
	privateURL := "https://provider.invalid/private/poster.png?token=provider-secret"
	otherPrivateURL := "https://provider.invalid/private/other.png?token=other-secret"
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(privateURL)))
	otherKey := fmt.Sprintf("%x", sha256.Sum256([]byte(otherPrivateURL)))
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne:   {ID: artworkItemOne, MediaType: "movie", PosterURL: privateURL},
			artworkItemStale: {ID: artworkItemStale, MediaType: "movie", PosterURL: otherPrivateURL},
		},
		artworkProfileTwo: {},
	}, map[string]string{privateURL: key, otherPrivateURL: otherKey})

	request := func(target, tokenHeader string) *httptest.ResponseRecorder {
		imageRequest := httptest.NewRequest(http.MethodGet, target, nil)
		parts := strings.Split(strings.TrimPrefix(imageRequest.URL.Path, "/Items/"), "/")
		imageRequest.SetPathValue("id", parts[0])
		imageRequest.SetPathValue("type", parts[2])
		if tokenHeader != "" {
			imageRequest.Header.Set("X-Emby-Token", tokenHeader)
		}
		response := httptest.NewRecorder()
		handler.handleImage(response, imageRequest)
		for _, secret := range []string{"provider.invalid", "provider-secret", "other-secret"} {
			if strings.Contains(response.Body.String(), secret) || strings.Contains(fmt.Sprint(response.Header()), secret) {
				t.Fatalf("artwork response leaked %q: status=%d headers=%v body=%q", secret, response.Code, response.Header(), response.Body.String())
			}
		}
		return response
	}

	anonymous := request("/Items/"+artworkItemOne+"/Images/Primary?tag="+key+"&maxWidth=500&quality=90", "")
	if anonymous.Code != http.StatusUnauthorized || catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 0 {
		t.Fatalf("anonymous recomputed tag status=%d catalog=%d lookup=%v served=%v", anonymous.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	crossProfile := request("/Items/"+artworkItemOne+"/Images/Primary?tag="+key, artworkTokenTwo)
	if crossProfile.Code != http.StatusNotFound || catalog.calls != 1 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 0 {
		t.Fatalf("cross-profile tag status=%d catalog=%d lookup=%v served=%v", crossProfile.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	wrongItem := request("/Items/"+artworkItemStale+"/Images/Primary?tag="+key, artworkTokenOne)
	if wrongItem.Code != http.StatusNotFound || catalog.calls != 2 || len(delivery.lookup) != 1 || len(delivery.servedKeys) != 0 {
		t.Fatalf("wrong-item tag status=%d catalog=%d lookup=%v served=%v", wrongItem.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	wrongType := request("/Items/"+artworkItemOne+"/Images/Backdrop?tag="+key, artworkTokenOne)
	if wrongType.Code != http.StatusNotFound || catalog.calls != 3 || len(delivery.lookup) != 1 || len(delivery.servedKeys) != 0 {
		t.Fatalf("wrong-type tag status=%d catalog=%d lookup=%v served=%v", wrongType.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	mismatched := request("/Items/"+artworkItemOne+"/Images/Primary?tag="+otherKey, artworkTokenOne)
	if mismatched.Code != http.StatusNotFound || catalog.calls != 4 || len(delivery.lookup) != 2 || len(delivery.servedKeys) != 0 {
		t.Fatalf("mismatched tag status=%d catalog=%d lookup=%v served=%v", mismatched.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	queryCredential := request("/Items/"+artworkItemOne+"/Images/Primary?tag="+key+"&api_key="+artworkTokenOne, "")
	if queryCredential.Code != http.StatusOK || queryCredential.Header().Get("ETag") != `"`+key+`"` || queryCredential.Body.String() != "pngbytes" ||
		catalog.calls != 5 || len(delivery.lookup) != 3 || len(delivery.servedKeys) != 1 || delivery.servedKeys[0] != key {
		t.Fatalf("query-bound tag status=%d catalog=%d lookup=%v served=%v", queryCredential.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
	headerCredential := request("/Items/"+artworkItemOne+"/Images/Primary?tag="+key, artworkTokenOne)
	if headerCredential.Code != http.StatusOK || headerCredential.Header().Get("ETag") != `"`+key+`"` || headerCredential.Body.String() != "pngbytes" ||
		catalog.calls != 6 || len(delivery.lookup) != 4 || len(delivery.servedKeys) != 2 || delivery.servedKeys[1] != key {
		t.Fatalf("header-bound tag status=%d catalog=%d lookup=%v served=%v", headerCredential.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}

	for _, test := range []struct {
		name        string
		target      string
		headerName  string
		headerValue string
		wantStatus  int
	}{
		{name: "invalid api key", target: "?tag=" + key + "&api_key=rivune_at_native", wantStatus: http.StatusUnauthorized},
		{name: "invalid compact api key", target: "?tag=" + key + "&ApiKey=rivune_at_native", wantStatus: http.StatusUnauthorized},
		{name: "malformed api key", target: "?tag=" + key + "&api_key=%ZZ", wantStatus: http.StatusBadRequest},
		{name: "unsupported authorization", target: "?tag=" + key, headerName: "Authorization", headerValue: "Bearer invalid", wantStatus: http.StatusUnauthorized},
		{name: "empty authorization token", target: "?tag=" + key, headerName: "X-Emby-Authorization", headerValue: `MediaBrowser Token=""`, wantStatus: http.StatusUnauthorized},
		{name: "empty token header", target: "?tag=" + key, headerName: "X-Emby-Token", headerValue: "", wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalidCredential := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images/Primary"+test.target, nil)
			invalidCredential.SetPathValue("id", artworkItemOne)
			invalidCredential.SetPathValue("type", "Primary")
			if test.headerName != "" {
				invalidCredential.Header.Set(test.headerName, test.headerValue)
			}
			invalidResponse := httptest.NewRecorder()
			handler.handleImage(invalidResponse, invalidCredential)
			if invalidResponse.Code != test.wantStatus || len(delivery.servedKeys) != 2 {
				t.Fatalf("invalid credential fallback status=%d want=%d served=%v", invalidResponse.Code, test.wantStatus, delivery.servedKeys)
			}
		})
	}
}

func TestTaglessArtworkRequiresCredential(t *testing.T) {
	handler, catalog, delivery := newArtworkHandler(t, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images/Primary", nil)
	request.SetPathValue("id", artworkItemOne)
	request.SetPathValue("type", "Primary")
	response := httptest.NewRecorder()
	handler.handleImage(response, request)
	if response.Code != http.StatusUnauthorized || len(delivery.servedKeys) != 0 || catalog.calls != 0 || len(delivery.lookup) != 0 {
		t.Fatalf("tagless projected artwork status=%d catalog=%d lookup=%v served=%v", response.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
}

func TestArtworkRejectsInvalidSelectorsAndUnregisteredSources(t *testing.T) {
	poster := "https://provider.invalid/private/poster.png?token=secret"
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {
			artworkItemOne:        {ID: artworkItemOne, MediaType: "movie", PosterURL: poster},
			artworkItemStale:      {ID: artworkItemStale, MediaType: "season", PosterURL: "/api/v1/artwork/" + strings.Repeat("c", 64)},
			artworkItemInvalidKey: {ID: artworkItemInvalidKey, MediaType: "series", PosterURL: "/api/v1/artwork/not-a-key"},
		},
	}, nil)

	tests := []struct {
		name      string
		itemID    string
		imageType string
		index     string
		token     string
		header    string
		profile   string
	}{
		{name: "unregistered key", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenOne},
		{name: "stale local key", itemID: artworkItemStale, imageType: "Primary", token: artworkTokenOne},
		{name: "unsupported type", itemID: artworkItemOne, imageType: "Screenshot", token: artworkTokenOne},
		{name: "unknown item", itemID: "60000000-0000-4000-8000-000000000006", imageType: "Primary", token: artworkTokenOne},
		{name: "cross profile item", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenTwo},
		{name: "missing supported type", itemID: artworkItemOne, imageType: "Logo", token: artworkTokenOne},
		{name: "negative index", itemID: artworkItemOne, imageType: "Primary", index: "-1", token: artworkTokenOne},
		{name: "out of range index", itemID: artworkItemOne, imageType: "Backdrop", index: "1", token: artworkTokenOne},
		{name: "non decimal index", itemID: artworkItemOne, imageType: "Primary", index: "00", token: artworkTokenOne},
		{name: "native token", itemID: artworkItemOne, imageType: "Primary", token: "rivune_at_native"},
		{name: "header precedence", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenOne, header: artworkTokenTwo},
		{name: "invalid profile selector", itemID: artworkItemOne, imageType: "Primary", token: artworkTokenOne, profile: artworkProfileTwo},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			indexed := test.index != ""
			path := "/Items/" + test.itemID + "/Images/" + test.imageType
			if indexed {
				path += "/" + test.index
			}
			if test.token != "" {
				path += "?api_key=" + test.token
			}
			if test.profile != "" {
				path += "&UserId=" + test.profile
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.SetPathValue("id", test.itemID)
			request.SetPathValue("type", test.imageType)
			request.SetPathValue("index", test.index)
			if test.header != "" {
				request.Header.Set("X-Emby-Token", test.header)
			}
			response := httptest.NewRecorder()
			if indexed {
				handler.handleIndexedImage(response, request)
			} else {
				handler.handleImage(response, request)
			}
			want := http.StatusNotFound
			if test.name == "native token" {
				want = http.StatusUnauthorized
			}
			challenge := response.Header().Get("WWW-Authenticate")
			if want == http.StatusUnauthorized && challenge != "MediaBrowser" || want != http.StatusUnauthorized && challenge != "" {
				t.Fatalf("response challenge = %q for status %d", challenge, want)
			}
			if response.Code != want || response.Header().Get("Location") != "" || strings.Contains(response.Body.String(), "provider.invalid") || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("response = %d %q headers=%v", response.Code, response.Body.String(), response.Header())
			}
		})
	}
	if len(delivery.servedKeys) != 0 {
		t.Fatalf("invalid requests served keys: %v", delivery.servedKeys)
	}
	if catalog.calls == 0 {
		t.Fatal("authorized item cases did not use CatalogReader")
	}
}

func TestArtworkRejectsDuplicateQueryTokenBeforeCatalog(t *testing.T) {
	handler, catalog, delivery := newArtworkHandler(t, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/Items/"+artworkItemOne+"/Images/Primary?api_key="+artworkTokenOne+"&api_key="+artworkTokenOne, nil)
	request.SetPathValue("id", artworkItemOne)
	request.SetPathValue("type", "Primary")
	response := httptest.NewRecorder()
	handler.handleImage(response, request)
	if response.Code != http.StatusUnauthorized || catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 0 {
		t.Fatalf("ambiguous auth response=%d catalog=%d lookup=%v served=%v", response.Code, catalog.calls, delivery.lookup, delivery.servedKeys)
	}
}

func TestUserImageIsDeterministicProfileScopedAndPrivate(t *testing.T) {
	providerURL := "https://provider.invalid/private/avatar.png?token=provider-secret"
	handler, catalog, delivery := newArtworkHandler(t, map[string]map[string]watchstate.CatalogTitle{
		artworkProfileOne: {artworkItemOne: {ID: artworkItemOne, MediaType: "movie", PosterURL: providerURL}},
	}, map[string]string{providerURL: strings.Repeat("a", 64)})

	paths := []string{
		"/UserImage?uSeRiD=" + artworkProfileOne,
		"/Users/" + artworkProfileOne + "/Images/Primary",
	}
	avatarBody := ""
	avatarETag := ""
	for _, prefix := range []string{"", "/emby"} {
		for _, path := range paths {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				request := httptest.NewRequest(method, prefix+path, nil)
				request.Header.Set("X-Emby-Token", artworkTokenOne)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" ||
					response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "private, no-cache" ||
					response.Header().Get("ETag") == "" || response.Header().Get("Location") != "" {
					t.Fatalf("%s %s status=%d headers=%v", method, prefix+path, response.Code, response.Header())
				}
				if method == http.MethodGet {
					if response.Body.Len() == 0 || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
						t.Fatalf("GET %s length=%q body=%q", prefix+path, response.Header().Get("Content-Length"), response.Body.String())
					}
					if avatarBody == "" {
						avatarBody, avatarETag = response.Body.String(), response.Header().Get("ETag")
					} else if response.Body.String() != avatarBody || response.Header().Get("ETag") != avatarETag {
						t.Fatalf("avatar changed across equivalent routes: etag=%q body=%q", response.Header().Get("ETag"), response.Body.String())
					}
				} else if response.Body.Len() != 0 || response.Header().Get("Content-Length") != strconv.Itoa(len(avatarBody)) {
					t.Fatalf("HEAD %s length=%q body=%q", prefix+path, response.Header().Get("Content-Length"), response.Body.String())
				}
				exposed := response.Body.String()
				for name, values := range response.Header() {
					exposed += name + strings.Join(values, ",")
				}
				for _, forbidden := range []string{artworkProfileOne, "Profile One", "user-one", "native-one", artworkTokenOne, "provider.invalid", "provider-secret"} {
					if strings.Contains(exposed, forbidden) {
						t.Fatalf("avatar response exposed %q: %s", forbidden, exposed)
					}
				}
			}
		}
	}

	notModifiedRequest := httptest.NewRequest(http.MethodGet, "/emby/UserImage", nil)
	notModifiedRequest.Header.Set("X-Emby-Token", artworkTokenOne)
	notModifiedRequest.Header.Set("If-None-Match", `"unrelated", W/`+avatarETag)
	notModifiedResponse := httptest.NewRecorder()
	handler.ServeHTTP(notModifiedResponse, notModifiedRequest)
	if notModifiedResponse.Code != http.StatusNotModified || notModifiedResponse.Body.Len() != 0 ||
		notModifiedResponse.Header().Get("ETag") != avatarETag || notModifiedResponse.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("conditional avatar status=%d headers=%v body=%q", notModifiedResponse.Code, notModifiedResponse.Header(), notModifiedResponse.Body.String())
	}

	otherRequest := httptest.NewRequest(http.MethodGet, "/UserImage", nil)
	otherRequest.Header.Set("X-Emby-Token", artworkTokenTwo)
	otherResponse := httptest.NewRecorder()
	handler.ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusOK || otherResponse.Body.String() == avatarBody || otherResponse.Header().Get("ETag") == avatarETag {
		t.Fatalf("different profile avatar was not distinct: status=%d etag=%q body=%q", otherResponse.Code, otherResponse.Header().Get("ETag"), otherResponse.Body.String())
	}

	for _, path := range []string{"/UserImage?USERID=" + artworkProfileTwo, "/Users/" + artworkProfileTwo + "/Images/Primary"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Emby-Token", artworkTokenOne)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), artworkProfileTwo) {
			t.Fatalf("foreign avatar %s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/UserImage", "/Users/" + artworkProfileOne + "/Images/Primary"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "MediaBrowser" {
			t.Fatalf("unauthenticated avatar %s status=%d headers=%v", path, response.Code, response.Header())
		}
	}
	for _, path := range []string{
		"/UserImage?UserId=not-a-uuid",
		"/UserImage?UserId=" + artworkProfileOne + "&userid=" + artworkProfileOne,
		"/UserImage?padding=" + strings.Repeat("x", MaximumQueryBytes),
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Emby-Token", artworkTokenOne)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid avatar query %s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
	if catalog.calls != 0 || len(delivery.lookup) != 0 || len(delivery.servedKeys) != 0 {
		t.Fatalf("user image reached provider artwork: catalog=%d lookup=%v served=%v", catalog.calls, delivery.lookup, delivery.servedKeys)
	}
}

func newArtworkHandler(t *testing.T, items map[string]map[string]watchstate.CatalogTitle, keys map[string]string) (*Handler, *artworkCatalog, *artworkDelivery) {
	t.Helper()
	profileOne := artworkProfileOne
	profileTwo := artworkProfileTwo
	authentication := &artworkAuthentication{sessions: map[string]AuthenticatedSession{
		artworkTokenOne: {
			ID: "session-one", ProfileID: profileOne, ProfileName: "Profile One", ExpiresAt: time.Now().Add(time.Hour),
			Principal: auth.Principal{SessionID: "native-one", UserID: "user-one", ActiveProfileID: &profileOne},
		},
		artworkTokenTwo: {
			ID: "session-two", ProfileID: profileTwo, ProfileName: "Profile Two", ExpiresAt: time.Now().Add(time.Hour),
			Principal: auth.Principal{SessionID: "native-two", UserID: "user-two", ActiveProfileID: &profileTwo},
		},
	}}
	catalog := &artworkCatalog{items: items}
	delivery := &artworkDelivery{keys: keys, body: []byte("pngbytes")}
	serverID, err := ParseServerID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("parse artwork server ID: %v", err)
	}
	handler, err := New(Dependencies{ServerInfo: ServerInfo{ID: serverID, Name: "Rivune"}, Authentication: authentication, Catalog: catalog, Artwork: delivery})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}
	return handler, catalog, delivery
}
