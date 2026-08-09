# Compatibilité Jellyfin 10.11.11

## Portée, baseline et niveau de preuve

Ce document décrit la surface de compatibilité de Rivune telle qu'elle est **observée dans le code et les tests du dépôt**. Il ne certifie la compatibilité d'aucun client. En particulier, la présence d'une route, un test de dispatch, une réponse HTTP 2xx ou une ressemblance de DTO ne prouvent ni la parité avec Jellyfin, ni un parcours Infuse, VidHub ou Streamyfin complet.

Baseline reproductible :

| Élément | Référence épinglée | Licence / provenance | Usage |
|---|---|---|---|
| Jellyfin Server | `v10.11.11`, commit `1fbd8739292cce610231be93daf43368733edf63` | GPL-2.0-only | Oracle comportemental; aucun code copié. |
| Image oracle | `jellyfin/jellyfin:10.11.11@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db` | image officielle; packaging GPL-3.0 | Harness différentiel. Manifestes observés : amd64 `sha256:0b901391a662862eddb5dc55d244d7883cbb6236ef5b9a6ea82abc78a89819f0`, arm64 `sha256:7536c1009c6ea50dadd2b244165efb357504ca0f2670abefbceb1c773cc7e13d`. |
| OpenAPI oracle | `/api-docs/openapi.json` extrait de l'image épinglée | généré par Jellyfin 10.11.11 | Le flux web « stable » public pointe désormais vers 12.0.0 et ne doit pas servir de baseline 10.11.11. |
| Rivune | dépôt courant | Apache-2.0 (`LICENSE`) | Implémentation indépendante. |
| Assets du harness | assets synthétiques Rivune listés dans `NOTICE` | Apache-2.0; aucun matériau tiers | Seuls médias autorisés pour les comparaisons de ce jalon. |

Hashes SHA-256 des assets synthétiques autorisés :

- `demo-720p.mp4`: `ec7cbf6fae17c35b166df5b651ce5ebf37138c52d54372cd886773173c5274ec`
- `demo-360p.mp4`: `e99c55a611c28bc0c8b2e1bc061cbd0b86a4180cbc3f42155bf3fc73fcf96c45`
- `demo.en.vtt`: `213f23304649d6a91f5312e1981aaf6633269ed674e49991c7e7f0bc725f6407`
- `demo.fr.vtt`: `bcb1b8b9f6b012397eb9091f655f1a129dd1becedfc2182d5100b849751f8951`
- `artwork.svg`: `42a2e34b6439b84d267b6bf8bfad83ce52c17051d4e2860f8f1925f31045ff7f`

Références externes étudiées sans reprise de code : Silo Server `fe11b2601dd339398ea9b74b18d5d0eebe6c16db` (AGPL-3.0), Remux `266af94690cdd71f970d270f349ffaba240eb15d` (AGPL-3.0), Streamyfin `6c4ee1c6252738d4d86c3cf07d3321ab1c771508` et `@jellyfin/sdk` 0.13.0 / `94695753de92d3777f8b7f07960d0b7b145fa67e` (MPL-2.0). Ces sources servent uniquement à consigner des comportements et attentes de consommateurs. Les vidéos de test officielles Jellyfin sont CC BY-SA 4.0 et ne font pas partie de ce jalon.

### Légende

- **Requête** : `E` = sous-ensemble envoyé compris et borné; `P` = paramètres ou précédences partiels; `M` = entrée minimale; `S` = route de sonde/stub, entrée acceptée mais fonctionnalité absente.
- **Réponse** : `E` = forme locale utile et testée; `P` = DTO/champs partiels; `M` = valeur conservatrice/minimale; `S` = contenant vide, `false` ou 204 intentionnel.
- **Fonction** : `F` = fonction projetée réellement exécutée; `M` = shim minimal/no-op; `S` = stub honnête.
- **Statut** : `✅ local` = contrat Rivune testé, **pas** parité Jellyfin ni validation client; `🟡 partiel` = écart connu ou fonction réduite; `🔴 écart` = comportement incompatible observé. Une ligne `S` est toujours `🟡 partiel`, jamais `✅`.
- **Clients** : `A` amorçage commun, `B` navigation/catalogue, `P` lecture, `T` état de lecture. Tous sont **[INFERENCE]**, sauf `G` qui désigne les clients synthétiques Generic A/B/C des tests. `SF` indique un endpoint effectivement lu dans le source Streamyfin, mais pas un parcours réussi contre Rivune. Aucun code de client ne signifie « client validé ».
- **Tests** : `U` dispatch exhaustif root + `/emby` et installation; `O` parité route/OpenAPI locale; `D` discovery/auth; `B` bootstrap; `C` catalogue; `CS` surface catalogue; `I` artwork; `P` playback; `T` state; `Q` séquences HTTP synthétiques. `U,O` seuls prouvent la présence, pas la sémantique.

Les 119 lignes ci-dessous couvrent exactement `routeDefinitions` dans `server/internal/jellyfin/routes.go`. La numérotation 1–115 est conservée pour les routes historiques et les quatre RouteSpec ajoutées depuis sont consignées en fin de table; l'ordre des lignes n'est donc plus l'ordre source. Chaque route accepte aussi le préfixe exact `/emby`; cette variante n'est pas une RouteSpec supplémentaire.

### Index des preuves

| Code / domaine | Source de preuve locale | Limite de la preuve |
|---|---|---|
| Routes, `U` | `server/internal/jellyfin/routes.go`; `routes_test.go` | Exhaustivité du dispatch et installation, pas le contenu des réponses. |
| Contrat, `O` | `protocol/jellyfin-compat-openapi.yaml`; `openapi_contract_test.go` | Égalité méthode/path/operationId locale, pas validation runtime des échanges. |
| Auth/discovery, `D` | `discovery_auth_http.go`, `auth.go`, `store.go`, `credentials.go` et tests associés | Oracles Rivune synthétiques, aucun SDK/client réel. |
| Bootstrap, `B` | `server/internal/jellyfin/bootstrap_http.go`, `display_preferences.go`, `bootstrap_registry.go`, `session_realtime.go` et tests associés | Sessions/capabilities/préférences locaux; socket borné avec keepalive, abonnement `Sessions` et notifications `UserDataChanged`/`LibraryChanged`, sans parité temps réel complète. |
| Catalogue, `C`/`CS` | `catalog_handlers.go`, `catalog.go`, `query.go`, `catalog_*_test.go` | Assertions ciblées, sans golden Jellyfin 10.11.11. |
| Artwork, `I` | `server/internal/jellyfin/artwork.go`, `server/internal/artwork/service.go` et tests associés | Métadonnées, GET/HEAD, transformations et cache conditionnel locaux; aucun client image réel. |
| Playback, `P` | `playback.go`, `registry.go`, `media_segments.go`, `server/internal/playback`, tests playback | Logique, sécurité et cas FFmpeg réels locaux; aucun parcours d'un lecteur nommé. |
| État, `T` | `state.go`, `watchstate`, `state_http_test.go`, `user_workflow_http_test.go` | Effets locaux persistants, favoris indépendants, feeds Resume/NextUp et frontières de profil testés; pas de client nommé réel. |
| Séquences, `Q` | `http_sequence_test.go` | Generic Client A/B/C uniquement; aucun Infuse/VidHub/Streamyfin. |
| Baseline officielle | [release Jellyfin v10.11.11](https://github.com/jellyfin/jellyfin/releases/tag/v10.11.11), [image officielle](https://hub.docker.com/r/jellyfin/jellyfin), OpenAPI extrait de l'image | La présence officielle d'un endpoint ne prédit pas le mode média choisi. |
| Consommateur Streamyfin | `streamyfin/streamyfin` au commit et SDK épinglés ci-dessus | Inspection source et observations rapportées, pas certification de l'application. |

## Matrice exhaustive des 119 RouteSpec

| # | Endpoint | Rivune route | Handler | Requête | Réponse | Fonction | Clients | Tests | Statut |
|---:|---|---|---|:---:|:---:|:---:|---|---|---|
| 1 | Public system info | `GET /System/Info/Public` | `handlePublicSystemInfo` | E | P | F | A,G | U,O,D,Q | ✅ local |
| 2 | System ping | `GET /System/Ping` | `handleSystemPing` | E | M | F | A | U,O,D | ✅ local |
| 3 | System ping POST | `POST /System/Ping` | `handleSystemPing` | E | M | F | A,G | U,O,Q | ✅ local |
| 4 | Network endpoint | `GET /System/Endpoint` | `handleSystemEndpoint` | E | E | F | A | U,O,D | ✅ local — `IsLocal`/`IsInNetwork` dérivés uniquement de l'adresse cliente de confiance |
| 5 | Quick Connect enabled | `GET /QuickConnect/Enabled` | `handleQuickConnectEnabled` | S | S | S | A,SF | U,O,D | 🟡 partiel — Quick Connect désactivé |
| 6 | Authenticate by name | `POST /Users/AuthenticateByName` | `handleAuthenticateByName` | P | P | F | A,G | U,O,D,Q | 🟡 partiel — credential de profil UUID, pas compte natif |
| 7 | Public users | `GET /Users/Public` | `handlePublicUsers` | S | S | S | A | U,O,D | 🟡 partiel — tableau vide anti-énumération |
| 8 | Authenticated system info | `GET /System/Info` | `handleSystemInfo` | E | M | M | A | U,O,D | 🟡 partiel — DTO public minimal |
| 9 | Current user | `GET /Users/Me` | `handleCurrentUser` | E | P | F | A,G | U,O,D,Q | ✅ local |
| 10 | User by id | `GET /Users/{id}` | `handleUser` | E | P | F | A | U,O,D | ✅ local |
| 11 | Users | `GET /Users` | `handleUsers` | E | P | M | A | U,O,B | 🟡 partiel — profil courant seulement |
| 12 | User image alias | `GET /UserImage` | `handleUserImage` | E | P | F | B | U,O,I | ✅ local |
| 13 | User image alias HEAD | `HEAD /UserImage` | `handleUserImage` | E | P | F | B | U,O,I | ✅ local |
| 14 | User primary image | `GET /Users/{userId}/Images/Primary` | `handleUserImage` | E | P | F | B | U,O,I | ✅ local |
| 15 | User primary image HEAD | `HEAD /Users/{userId}/Images/Primary` | `handleUserImage` | E | P | F | B | U,O,I | ✅ local |
| 16 | Sessions | `GET /Sessions` | `handleSessions` | P | P | F | A,SF | U,O,B,T | 🟡 partiel — sessions mémoire du même compte/profil, multi-device, filtres bornés et now-playing local |
| 17 | Logout | `POST /Sessions/Logout` | `handleLogout` | E | E | F | A,G | U,O,D,B,Q | ✅ local |
| 18 | Full capabilities | `POST /Sessions/Capabilities/Full` | `handleSessionCapabilitiesFull` | P | E | F | P,SF | U,O,B,P | ✅ local |
| 19 | Capabilities | `POST /Sessions/Capabilities` | `handleSessionCapabilities` | P | E | F | P,G | U,O,B,Q | ✅ local |
| 20 | Active encodings | `DELETE /Videos/ActiveEncodings` | `handleActiveEncodings` | P | E | F | P | U,O,B | ✅ local |
| 21 | Client log | `POST /ClientLog/Document` | `handleClientLog` | P | S | M | A | U,O,B | 🟡 partiel — contenu jeté |
| 22 | WebSocket | `GET /socket` | `handleSocket` | P | P | F | A,T,SF | U,O,B,T | 🟡 partiel — keepalive, abonnement `Sessions`, `UserDataChanged` et `LibraryChanged`; événements Jellyfin incomplets |
| 23 | SyncPlay list | `GET /SyncPlay/List` | `handleSyncPlayList` | S | S | S | P | U,O,D | 🟡 partiel — `[]` |
| 24 | Bitrate test | `GET /Playback/BitrateTest` | `handlePlaybackBitrateTest` | P | E | F | P | U,O,D | ✅ local |
| 25 | Plugins | `GET /Plugins` | `handlePlugins` | S | S | S | A | U,O,D | 🟡 partiel — `[]` |
| 26 | Packages | `GET /Packages` | `handlePackages` | S | S | S | A | U,O,D | 🟡 partiel — `[]` |
| 27 | Branding configuration | `GET /Branding/Configuration` | `handleBrandingConfiguration` | S | S | S | A | U,O,D | 🟡 partiel — objet vide |
| 28 | Branding splash | `GET /Branding/Splashscreen` | `handleBrandingSplashscreen` | S | S | S | A | U,O,D | 🟡 partiel — 204 |
| 29 | Display preferences | `GET /DisplayPreferences/{displayPreferencesId}` | `handleDisplayPreferences` | E | E | F | B | U,O,B | ✅ local — persistance compte/profil/client/id |
| 30 | Update display preferences | `POST /DisplayPreferences/{displayPreferencesId}` | `handleDisplayPreferencesUpdate` | E | E | F | B | U,O,B | ✅ local — JSON borné et durable |
| 31 | Grouping options | `GET /UserViews/GroupingOptions` | `handleGroupingOptions` | S | S | S | B | U,O,B | 🟡 partiel — `[]` |
| 32 | User views | `GET /Users/{id}/Views` | `handleUserViews` | P | P | F | B,G | U,O,C,Q | 🟡 partiel — projection locale |
| 33 | User views alias | `GET /UserViews` | `handleViews` | P | P | F | B | U,O,C | 🟡 partiel — projection locale |
| 34 | Virtual folders | `GET /Library/VirtualFolders` | `handleVirtualFolders` | P | P | F | B | U,O,C | 🟡 partiel — locations vides |
| 35 | Selectable media folders | `GET /Library/SelectableMediaFolders` | `handleSelectableMediaFolders` | P | P | M | B | U,O,C | 🟡 partiel — alias VirtualFolders |
| 36 | Items | `GET /Items` | `handleItems` | P | P | F | B,G,SF | U,O,C,Q | 🟡 partiel — matrice locale bornée de filtres/Fields/tris; valeurs non projetées rejetées |
| 37 | User items legacy | `GET /Users/{id}/Items` | `handleUserItems` | P | P | F | B,G | U,O,C,Q | 🟡 partiel — même matrice locale, liée au profil |
| 38 | Latest items | `GET /Items/Latest` | `handleLatestItems` | P | P | F | B,SF | U,O,C | 🟡 partiel — `Limit` jusqu'à 1008 servi par lectures internes bornées à 200 |
| 39 | User latest legacy | `GET /Users/{id}/Items/Latest` | `handleUserLatestItems` | P | P | F | B | U,O,C | 🟡 partiel — même limite haute bornée, liée au profil |
| 40 | Item detail | `GET /Items/{id}` | `handleItem` | P | P | F | B,P | U,O,C | 🟡 partiel — BaseItemDto réduit |
| 41 | User item detail | `GET /Users/{userId}/Items/{itemId}` | `handleUserItem` | P | P | F | B,P,G,SF | U,O,C,P,Q | 🟡 partiel — BaseItemDto réduit |
| 42 | Seasons | `GET /Shows/{seriesId}/Seasons` | `handleSeasons` | P | P | F | B,G | U,O,C,Q | ✅ local |
| 43 | Episodes | `GET /Shows/{seriesId}/Episodes` | `handleEpisodes` | P | P | F | B,G | U,O,C,Q | ✅ local |
| 44 | Search hints | `GET /Search/Hints` | `handleSearchHints` | P | P | F | B,G | U,O,C,Q | 🟡 partiel — recherche locale récursive/paginée, total et `SearchHintDto` projetés |
| 45 | User search hints | `GET /Users/{id}/Search/Hints` | `handleUserSearchHints` | P | P | F | B | U,O,C | 🟡 partiel — même projection, liée au profil |
| 46 | Item filters legacy | `GET /Items/Filters` | `handleItemsFilters` | P | P | F | B | U,O,CS | 🟡 partiel — genres/années locaux |
| 47 | Item filters v2 | `GET /Items/Filters2` | `handleItemsFilters2` | P | P | F | B | U,O,CS | 🟡 partiel — tags vides |
| 48 | Suggestions | `GET /Items/Suggestions` | `handleSuggestions` | P | P | F | B | U,O,CS | 🟡 partiel — algorithme local |
| 49 | Similar items | `GET /Items/{itemId}/Similar` | `handleSimilarItems` | P | P | F | B | U,O,CS | 🟡 partiel — similarité locale |
| 50 | Similar movies | `GET /Movies/{itemId}/Similar` | `handleSimilarItems` | P | P | F | B | U,O,CS | 🟡 partiel — alias générique |
| 51 | Similar shows | `GET /Shows/{itemId}/Similar` | `handleSimilarItems` | P | P | F | B | U,O,CS | 🟡 partiel — alias générique |
| 52 | Genres | `GET /Genres` | `handleGenres` | P | P | F | B | U,O,CS | 🟡 partiel — facettes synthétiques |
| 53 | Genre detail | `GET /Genres/{genreName}` | `handleGenre` | P | P | F | B | U,O,CS | 🟡 partiel |
| 54 | Persons | `GET /Persons` | `handlePersons` | P | P | F | B | U,O,CS | 🟡 partiel — extrait du catalogue |
| 55 | Person detail | `GET /Persons/{name}` | `handlePerson` | P | P | F | B | U,O,CS | 🟡 partiel |
| 56 | Studios | `GET /Studios` | `handleStudios` | P | P | F | B | U,O,CS | 🟡 partiel — facettes studios locales, bornées, filtrées et stables |
| 57 | Artists | `GET /Artists` | `handleEmptyCatalogDomain` | S | S | S | B | U,O,CS | 🟡 partiel — QueryResult vide |
| 58 | Upcoming shows | `GET /Shows/Upcoming` | `handleUpcomingShows` | P | P | F | B | U,O,CS | 🟡 partiel — projection locale |
| 59 | Movie recommendations | `GET /Movies/Recommendations` | `handleMovieRecommendations` | P | P | F | B | U,O,CS | 🟡 partiel — groupes locaux |
| 60 | Media segments | `GET /MediaSegments/{itemId}` | `handleMediaSegments` | P | P | F | P,SF | U,O,CS,P | 🟡 partiel — intro/recap/outro provider pour épisodes identifiables; indisponible sinon |
| 61 | Theme media | `GET /Items/{itemId}/ThemeMedia` | `handleThemeMedia` | S | S | S | B | U,O,CS | 🟡 partiel — groupes vides |
| 62 | Theme songs | `GET /Items/{itemId}/ThemeSongs` | `handleThemeSongs` | S | S | S | B | U,O,CS | 🟡 partiel — résultat vide |
| 63 | Special features | `GET /Items/{itemId}/SpecialFeatures` | `handleSpecialFeatures` | S | S | S | B | U,O,CS | 🟡 partiel — liste vide |
| 64 | Intros | `GET /Items/{itemId}/Intros` | `handleIntros` | S | S | S | B | U,O,CS | 🟡 partiel — QueryResult vide |
| 65 | Local trailers | `GET /Items/{itemId}/LocalTrailers` | `handleLocalTrailers` | S | S | S | B | U,O,CS | 🟡 partiel — liste vide |
| 66 | Legacy theme media | `GET /Users/{userId}/Items/{itemId}/ThemeMedia` | `handleThemeMedia` | S | S | S | B | U,O,CS | 🟡 partiel — groupes vides |
| 67 | Legacy theme songs | `GET /Users/{userId}/Items/{itemId}/ThemeSongs` | `handleThemeSongs` | S | S | S | B | U,O,CS | 🟡 partiel — résultat vide |
| 68 | Legacy special features | `GET /Users/{userId}/Items/{itemId}/SpecialFeatures` | `handleSpecialFeatures` | S | S | S | B | U,O,CS | 🟡 partiel — liste vide |
| 69 | Legacy intros | `GET /Users/{userId}/Items/{itemId}/Intros` | `handleIntros` | S | S | S | B | U,O,CS | 🟡 partiel — QueryResult vide |
| 70 | Legacy local trailers | `GET /Users/{userId}/Items/{itemId}/LocalTrailers` | `handleLocalTrailers` | S | S | S | B | U,O,CS | 🟡 partiel — liste vide |
| 71 | Item image | `GET /Items/{id}/Images/{type}` | `handleImage` | P | P | F | B,G,SF | U,O,I,Q | 🟡 partiel — types localisés; resize/fill/quality, ETag et 304 conditionnel locaux |
| 72 | Item image HEAD | `HEAD /Items/{id}/Images/{type}` | `handleImage` | P | P | F | B,G | U,O,I,Q | 🟡 partiel — mêmes headers/ETag/304, sans corps |
| 73 | Indexed image | `GET /Items/{id}/Images/{type}/{index}` | `handleIndexedImage` | P | P | F | B,SF | U,O,I | 🟡 partiel — index 0 seulement |
| 74 | Indexed image HEAD | `HEAD /Items/{id}/Images/{type}/{index}` | `handleIndexedImage` | P | P | F | B | U,O,I | 🟡 partiel — index 0 seulement |
| 75 | PlaybackInfo GET | `GET /Items/{id}/PlaybackInfo` | `handlePlaybackInfo` | P | P | F | P,G | U,O,P,Q | 🟡 partiel — profil conservateur |
| 76 | PlaybackInfo POST | `POST /Items/{id}/PlaybackInfo` | `handlePlaybackInfo` | P | P | F | P,SF | U,O,P | 🟡 partiel — profils DirectPlay/Transcoding/Codec/Container vidéo bornés; `ResponseProfiles` incomplets |
| 77 | User PlaybackInfo GET | `GET /Users/{userId}/Items/{id}/PlaybackInfo` | `handlePlaybackInfo` | P | P | F | P | U,O,P | 🟡 partiel |
| 78 | User PlaybackInfo POST | `POST /Users/{userId}/Items/{id}/PlaybackInfo` | `handlePlaybackInfo` | P | P | F | P | U,O,P | 🟡 partiel — même sous-ensemble DeviceProfile, lié au profil |
| 79 | Video stream | `GET /Videos/{id}/stream` | `handleStream` | P | P | F | P,G,SF | U,O,P,Q | 🟡 partiel — proxy direct/session-scoped |
| 80 | Video stream HEAD | `HEAD /Videos/{id}/stream` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 81 | Container stream | `GET /Videos/{id}/stream.{container}` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 82 | Container stream HEAD | `HEAD /Videos/{id}/stream.{container}` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 83 | HLS master | `GET /Videos/{id}/master.m3u8` | `handleStream` | P | P | F | P,SF | U,O,P | 🟡 partiel — mono-variant |
| 84 | HLS master HEAD | `HEAD /Videos/{id}/master.m3u8` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 85 | HLS media playlist | `GET /Videos/{id}/main.m3u8` | `handleStream` | P | P | F | P,SF | U,O,P | 🟡 partiel — EVENT/fenêtre longue à valider |
| 86 | HLS media playlist HEAD | `HEAD /Videos/{id}/main.m3u8` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 87 | HLS1 segment | `GET /Videos/{id}/hls1/{playlistId}/{segmentId}.{container}` | `handleStream` | P | P | F | P,SF | U,O,P | 🟡 partiel — e2e lecteur absent |
| 88 | HLS1 segment HEAD | `HEAD /Videos/{id}/hls1/{playlistId}/{segmentId}.{container}` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 89 | Legacy HLS segment | `GET /Videos/{id}/hls/{playlistId}/{segmentId}.{container}` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 90 | Legacy HLS segment HEAD | `HEAD /Videos/{id}/hls/{playlistId}/{segmentId}.{container}` | `handleStream` | P | P | F | P | U,O,P | 🟡 partiel |
| 91 | Subtitle stream | `GET /Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/Stream.{format}` | `handleSubtitleStream` | P | P | F | P,SF | U,O,P | 🟡 partiel — VTT uniquement; asset lié à MediaSource/PlaySession, GET/Range testés |
| 92 | Subtitle stream HEAD | `HEAD /Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/Stream.{format}` | `handleSubtitleStream` | P | P | F | P | U,O,P | 🟡 partiel — HEAD et headers locaux |
| 93 | Subtitle stream at ticks | `GET /Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/{startPositionTicks}/Stream.{format}` | `handleSubtitleStream` | P | P | F | P | U,O,P | 🟡 partiel — variante seek VTT bornée et testée |
| 94 | Subtitle stream at ticks HEAD | `HEAD /Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/{startPositionTicks}/Stream.{format}` | `handleSubtitleStream` | P | P | F | P | U,O,P | 🟡 partiel — mêmes contraintes, sans corps |
| 95 | Download | `GET /Items/{id}/Download` | `handleDownload` | E | E | F | P,SF | U,O,P | ✅ local — source fichier direct résolu, octets/ranges/headers testés; aucune URL provider |
| 96 | Download HEAD | `HEAD /Items/{id}/Download` | `handleDownload` | E | E | F | P | U,O,P | ✅ local — mêmes headers, sans corps |
| 97 | Playback started | `POST /Sessions/Playing` | `handlePlaying` | P | E | F | T,G,SF | U,O,T,Q | 🟡 partiel — position, seek, pause, mute, pistes, source et méthode conservés dans la session locale |
| 98 | Playback progress | `POST /Sessions/Playing/Progress` | `handlePlayingProgress` | P | E | F | T,G,SF | U,O,T,Q | 🟡 partiel — même état local et publication temps réel |
| 99 | Playback stopped | `POST /Sessions/Playing/Stopped` | `handlePlayingStopped` | P | E | F | T,G,SF | U,O,T,Q | ✅ local |
| 100 | Playback ping | `POST /Sessions/Playing/Ping` | `handlePlayingPing` | P | E | F | T | U,O,T | ✅ local |
| 101 | Legacy played | `POST /Users/{userId}/PlayedItems/{itemId}` | `handlePlayedItem` | E | P | F | T | U,O,T | ✅ local |
| 102 | Legacy unplayed | `DELETE /Users/{userId}/PlayedItems/{itemId}` | `handlePlayedItem` | E | P | F | T | U,O,T | ✅ local |
| 103 | Played | `POST /UserPlayedItems/{itemId}` | `handleUserPlayedItem` | E | P | F | T,SF | U,O,T | ✅ local |
| 104 | Unplayed | `DELETE /UserPlayedItems/{itemId}` | `handleUserPlayedItem` | E | P | F | T,SF | U,O,T | ✅ local |
| 105 | User data | `GET /UserItems/{itemId}/UserData` | `handleUserData` | E | E | F | T | U,O,T | ✅ local — DTO local complet pris en charge |
| 106 | Update user data | `POST /UserItems/{itemId}/UserData` | `handleUserData` | E | E | F | T,SF | U,O,T | ✅ local — mutation atomique absent/null/valeur de tous les champs supportés |
| 107 | Legacy user data | `GET /Users/{userId}/Items/{itemId}/UserData` | `handleLegacyUserData` | E | E | F | T | U,O,T | ✅ local — même DTO lié au profil |
| 108 | Legacy update user data | `POST /Users/{userId}/Items/{itemId}/UserData` | `handleLegacyUserData` | E | E | F | T | U,O,T | ✅ local — même mutation atomique liée au profil |
| 109 | Favorite | `POST /UserFavoriteItems/{itemId}` | `handleFavoriteItem` | E | P | F | T | U,O,T,Q | ✅ local — état favori indépendant de la bibliothèque |
| 110 | Unfavorite | `DELETE /UserFavoriteItems/{itemId}` | `handleFavoriteItem` | E | P | F | T | U,O,T,Q | ✅ local — appartenance bibliothèque préservée |
| 111 | Legacy favorite | `POST /Users/{userId}/FavoriteItems/{itemId}` | `handleLegacyFavoriteItem` | E | P | F | T | U,O,T,Q | ✅ local — identité profil liée au credential |
| 112 | Legacy unfavorite | `DELETE /Users/{userId}/FavoriteItems/{itemId}` | `handleLegacyFavoriteItem` | E | P | F | T | U,O,T,Q | ✅ local — appartenance bibliothèque préservée |
| 113 | Legacy resume | `GET /Users/{id}/Items/Resume` | `handleResumeItems` | P | P | F | B,T | U,O,T,Q | 🟡 partiel — page/total/ordre exacts, limite de continuation à 100 |
| 114 | Resume | `GET /UserItems/Resume` | `handleUserResumeItems` | P | P | F | B,T | U,O,T,Q | 🟡 partiel — UserId lié et champs généraux field-gated |
| 115 | Next up | `GET /Shows/NextUp` | `handleNextUp` | P | P | F | B,T,SF | U,O,T,Q | 🟡 partiel — inclut le premier épisode des séries jamais commencées; pagination/SeriesId/Fields locaux |
| 116 | Viewing | `POST /Sessions/Viewing` | `handleViewing` | E | E | F | A,T | U,O,B | ✅ local — item autorisé projeté dans `NowViewingItem`; `SessionId` étranger refusé |
| 117 | Trickplay image | `GET /Videos/{itemId}/Trickplay/{width}/{index}.jpg` | `handleTrickplayImage` | P | E | F | P,SF | U,O,P | 🟡 partiel — JPEG généré à la demande, authentifié et lié à la source; largeur/index bornés |
| 118 | Trickplay image HEAD | `HEAD /Videos/{itemId}/Trickplay/{width}/{index}.jpg` | `handleTrickplayImage` | P | E | F | P | U,O,P | 🟡 partiel — HEAD, Range, ETag et cache locaux testés |
| 119 | Item image metadata | `GET /Items/{id}/Images` | `handleImageInfos` | P | P | F | B | U,O,I | 🟡 partiel — images localisées autorisées, index 0; dimensions/taille si connues |

## Endpoints de consommateurs observés mais absents de Rivune

« Observé » signifie ici lu dans le source du consommateur indiqué, pas exécuté avec succès contre Rivune. Une absence est distincte d'une RouteSpec stub : la requête ne peut pas être dispatchée.

| Priorité | Endpoint absent | Preuve consommateur | Effet attendu |
|---|---|---|---|
| P0 | `POST /QuickConnect/Initiate` | Streamyfin appelle le SDK Quick Connect | Le flux Quick Connect Streamyfin ne peut pas démarrer; Rivune annonce correctement `Enabled=false`. |
| P0 | `GET /QuickConnect/Connect` | polling Streamyfin avec `Secret` | Même flux bloqué. |
| P0 | `POST /Users/AuthenticateWithQuickConnect` | finalisation Streamyfin | Même flux bloqué. |
| P1 | `GET /Audio/{id}/stream` et `stream.{container}` | lecteur audio Streamyfin | La surface Rivune ne prend en charge que la vidéo; audio-only absent. |
| P1 | `GET /Audio/{id}/universal` | référence comportementale Remux/clients audio | Aucun fallback UniversalAudio. |
| P1 | `POST /LiveStreams/Close` | teardown Live TV Streamyfin | Live TV et fermeture tuner hors surface actuelle. |
| P1 | `GET /Episode/{id}/IntroTimestamps` | fallback segments Streamyfin | Quand MediaSegments est indisponible, le premier fallback reste absent. |
| P1 | `GET /Episode/{id}/Timestamps` | second fallback segments Streamyfin | Intro/outro indisponibles. |
| P1 | `GET /Items/{id}/file` | référence Remux/clients de fichier | Alias de fichier absent; `/Items/{id}/Download` existe. |
| P1 | `GET /Videos/{id}/main/stream.m3u8` | référence Remux de lecteurs HLS | Seul `/main.m3u8` est exposé. |
| Hors scope déclaré | `GET /` et assets Jellyfin Web | Jellyfin Media Player/Desktop charge Jellyfin Web du serveur | Client Desktop/Media Player explicitement non compatible; ne pas interpréter comme route oubliée. |

## Écarts P0 et P1

Les niveaux ci-dessous expriment le risque produit actuel. La colonne preuve sépare les contrats locaux, les observations historiques d'un consommateur et les inférences d'impact; une correction locale n'est jamais présentée comme une validation client.

| Priorité | Écart | Preuve | Conséquence |
|---|---|---|---|
| P0 | Les URLs item anonymes restent en 401, même avec un tag projeté; Streamyfin construit des requêtes image sans header d'authentification. | **[OBSERVÉ tests + consommateur; exécution antérieure rapportée]** `artwork_test.go`, `catalog_collections_test.go` et source Streamyfin. | Home Streamyfin susceptible de rester sans posters; aucune nouvelle exécution client ne qualifie les formes exactes encore affectées. |
| P0 | `/Items` ordinaire accepte une limite demandée jusqu'à 1008 mais ne rend qu'au plus 200 éléments; tris limités à `Name`/`SortName` et Fields Jellyfin non exhaustifs. | **[OBSERVÉ code/tests + consommateur]** `query.go`, `catalog_handlers.go` et limites hautes émises par Streamyfin. | Troncature possible si le client ne pagine pas sur la réponse; ordre ou projection divergents. `/Items/Latest` sert bien 1000/1008 par lectures internes de 200. |
| P1 | `System/Info` reste un DTO public réduit; `LocalAddress` reprend la PublicURL configurée et peut être vide sans configuration. | **[OBSERVÉ code/tests]** | Bootstrap exigeant d'autres champs possiblement incomplet **[INFERENCE]**. |
| P1 | `/Sessions` et le socket sont mémoire/profile-scoped et couvrent un sous-ensemble d'événements et de contrôles. | **[OBSERVÉ code/tests]** now-playing, `Sessions`, `UserDataChanged`, `LibraryChanged`; pas de contrôle distant complet. | UI admin/session multi-device partielle; synchronisation d'un client nommé non prouvée. |
| P1 | MediaSegments dépend d'un épisode avec coordonnées série/saison/épisode et ID IMDb; les deux fallbacks Episode timestamps restent absents. | **[OBSERVÉ code + consommateur]** | Intro/outro indisponibles lorsque le provider n'a pas de marqueur ou d'identité exploitable. |
| P1 | Une collection triée peut encore parcourir jusqu'à 1000 pages de 200 par dossier, malgré des lots concurrents bornés à 8. | **[OBSERVÉ code/tests]**; `Limit=0` compte désormais les médias filtrés sans compter les dossiers. | Latence/charge élevée sur grandes collections **[INFERENCE]**. |
| P1 | CodecProfiles et ContainerProfiles sont traduits, mais `ResponseProfiles` et certaines propriétés/conditions Jellyfin restent hors contrat. | **[OBSERVÉ code/tests]** | Faux direct-play/remux/transcode possibles selon le profil client. |
| P1 | Audio-only, UniversalAudio, Live TV et Quick Connect complet ne sont pas dans la surface. | **[OBSERVÉ routes + consommateur]** | Workflows Streamyfin correspondants indisponibles. |
| P1 | Les images supportent resize/fill/quality, HEAD, ETag et 304, mais les index >0 et l'accès anonyme sûr restent absents. | **[OBSERVÉ code/tests]** | Backdrops multiples absents; blocage des clients qui n'authentifient pas l'artwork. |
| P1 | Le pipeline HLS local sert playlists et children remux/transcode avec FFmpeg réel et playlist glissante de 120 segments; l'ancien échec child Streamyfin n'a pas été rejoué. | **[OBSERVÉ tests locaux + observation consommateur antérieure]** | Aucun défaut local reproduit actuellement, mais aucune preuve que le player Streamyfin réussit le parcours. |
| P1 | Le smoke oracle archivé ne compare exactement que logout; dix autres étapes sont volontairement `per-target`/skipped. | **[OBSERVÉ artefact]** `scripts/jellyfin-compat/artifacts/oracle-smoke/recompare/summary.json`. | Aucune parité générale Jellyfin 10.11.11 ni compatibilité client ne peut être déduite. |

## Workflows consommateurs

### Infuse — hypothèse de workflow, non validation

Aucune trace Infuse, fixture Infuse ou exécution d'Infuse n'existe dans le dépôt. Remux déclare viser Infuse et documente des choix comme le proxy d'image sans redirect; cela reste une référence AGPL et une déclaration tierce, pas une preuve Rivune. Les corrections locales de cache image, HLS et sélection de pistes ne changent pas ce niveau de preuve.

| Étape attendue | Surface Rivune | État de preuve |
|---|---|---|
| Discovery, login, user/views | PublicInfo → AuthenticateByName → Me → Views | Parcours générique testé; attribution à Infuse **[INFERENCE]**. Login UUID de profil diverge d'un Jellyfin ordinaire. |
| Scan série/film | Items → Seasons/Episodes → détails → images | Routes et matrice locale de filtres/Fields testées; pages Items ordinaires bornées à 200 et images anonymes refusées. |
| Négociation | POST PlaybackInfo avec DeviceProfile, pistes et bitrate | Profils DirectPlay/Transcoding/Codec/Container locaux testés; `ResponseProfiles` et conditions Jellyfin restent partiels. |
| Lecture directe/remux | stream/range ou master → main → segments | Range et remux/transcode HLS testés localement avec FFmpeg réel; aucun player Infuse réel. |
| Pistes/sous-titres | DeliveryUrl VTT et changements de pistes | Conversion et autorité testées; comportement Infuse **[INFERENCE]**. |
| État | Playing → Progress → Stopped | Position, pause, mute, pistes et méthode testées dans la session locale; comportement Infuse **[INFERENCE]**. |

Conclusion : **Infuse non validé**.

### VidHub — hypothèse de workflow, non validation

Aucun source, paquet, trace ou fixture VidHub n'a été audité. Les seules mentions locales « vidhub » ne démontrent pas un consommateur. Le parcours ci-dessous est donc entièrement **[INFERENCE]** à partir d'un lecteur Jellyfin-type : discovery/login → views/items/hierarchy → images → PlaybackInfo → stream/HLS → état. Les risques principaux sont le scan de collections triées, les pages Items ordinaires plafonnées à 200, les images anonymes/index >0 et la négociation DeviceProfile partielle. Aucun statut de la matrice ne doit être lu comme « VidHub compatible ».

Conclusion : **VidHub non validé**.

### Streamyfin — endpoints observés, correctifs locaux non rejoués

Le source Streamyfin et son SDK verrouillé ont été lus. Cela prouve les requêtes et champs consommés, pas un parcours réussi. Les observations d'échec antérieures sont distinguées des contrats locaux ajoutés depuis.

| Workflow observé | Requêtes/champs | État Rivune |
|---|---|---|
| Auth classique | AuthenticateByName, current user | Partiel : credential de profil UUID. Quick Connect complet reste absent. |
| Home/catalogue | Items/Latest/NextUp avec limites configurables, filtres, genres, années, ratings, Fields | `Latest` 1000/1008 est servi en chunks; `Items` ordinaire reste plafonné à 200 et la matrice Jellyfin n'est pas exhaustive. |
| Images | URL directe `/Items/.../Images/...` sans header, parfois sans tag | **Écart P0 maintenu** : les formes anonymes restent 401. Resize/fill/quality, ETag, HEAD et 304 fonctionnent après authentification. |
| Vidéo | POST PlaybackInfo; lit seulement `MediaSources[0]`, `TranscodingUrl`, `RequiredHttpHeaders`, pistes | Négociation locale plus riche mais toujours partielle; aucun payload runtime Streamyfin rejoué. |
| Direct | `/Videos/{id}/stream?...` avec token/query et headers requis | Direct/range testé localement et en smoke synthétique, pas avec le player Streamyfin. |
| HLS | TranscodingUrl → master → child/segments | Remux/transcode et children jouables testés localement avec FFmpeg réel; l'ancien échec client n'a pas été rejoué. |
| Sous-titres/audio | DeliveryUrl, indexes, DeliveryMethod; `/Audio/...` pour audio-only | VTT GET/HEAD/Range/seek local; endpoints audio-only absents. |
| État | Viewing, Playing/Progress/Stopped, 10 s côté natif | Position, pause, mute, pistes, méthode et UserData supportés localement; two-way client non validé. |
| Segments/trickplay | MediaSegments puis fallbacks; Trickplay sheets | MediaSegments provider-bound et trickplay JPEG GET/HEAD présents; fallbacks Episode absents; aucun affichage Streamyfin validé. |
| Téléchargement | PlaybackInfo puis Download ou URL transcodée réécrite | Download direct authentifié/range/HEAD testé localement; sources provider-only et conversion refusées, sans téléchargement Streamyfin réel. |
| Sessions/socket | Sessions `activeWithinSeconds=360`, Full capabilities, socket | NowPlaying et événements `Sessions`/`UserDataChanged`/`LibraryChanged` locaux; surface temps réel partielle. |

Conclusion : **Streamyfin non validé**. Le blocage anonyme des images et le plafond de 200 sur `/Items` ordinaire restent directement étayés; les correctifs HLS, limites `Latest`, UserData, MediaSegments et trickplay n'ont pas été rejoués dans l'application.

## Méthode de preuve différentielle

Le statut de chaque cellule doit évoluer seulement par l'une des preuves suivantes, du plus faible au plus fort :

1. **Présence** : `routeDefinitions` et parité avec l'OpenAPI Rivune (`U,O`).
2. **Contrat local** : test handler vérifiant statut, headers, corps/champs, effet de bord et erreurs; une fake 204 ne qualifie que le routage/autorité.
3. **Oracle 10.11.11** : même requête exécutée sur l'image épinglée et Rivune avec les mêmes assets synthétiques; comparaison des statuts, content-types, headers contractuels, types/nullabilité/champs JSON et invariants.
4. **Parcours média** : PlaybackInfo → URL émise → master/main/init/segments ou stream/range → start/progress/stop, avec FFmpeg réel et octets synthétiques.
5. **Consommateur nommé** : trace scrubbed issue d'une version épinglée du client, rejouée par le harness; seulement ce niveau autorise une affirmation limitée à ce workflow et cette version.

Le harness ne doit normaliser que les valeurs explicitement non déterministes : IDs/tokens/timestamps, URL d'hôte et ordre documenté non contractuel. Il ne doit pas supprimer des champs, convertir `null` en absence, trier arbitrairement des listes sémantiques, accepter un statut différent ou masquer un header de cache/auth. Chaque normalisation porte un pointeur JSON, une raison et est appliquée séparément à chaque cible. Les captures sont isolées par cible; aucun token, mot de passe, URL provider ou header secret n'est persisté dans snapshots, erreurs ou logs.

Commandes contractuelles du jalon :

```text
go run ./cmd/jellyfin-compat validate -manifest ../scripts/jellyfin-compat/requests.json
go run ./cmd/jellyfin-compat run -manifest ../scripts/jellyfin-compat/requests.json -target upstream=URL -target rivune=URL -out DIR
go run ./cmd/jellyfin-compat compare -left DIR/upstream -right DIR/rivune -out DIR/diff
```

La baseline est acceptée seulement si le manifest est valide, les deux exécutions terminent sans secret persisté, puis le comparateur produit un diff explicite. Un `observedGap` documenté peut conserver un écart attendu; il ne transforme jamais cet écart en parité.

Un smoke archivé exécute les deux cibles pour ping, discovery, endpoint réseau, login, user/views, items, recherche, `HEAD` artwork, PlaybackInfo et logout. Son `recompare/summary.json` contient `compared=1`, `matched=1` pour logout et dix étapes `per-target`/skipped. Il prouve l'exécution et la conservation de captures scrubbed, pas la parité générale.

## Notes performance et sécurité

### Performance

- Les pages `/Items` ordinaires sont bornées à 200. `/Items/Latest` accepte 1000/1008 mais assemble la réponse par lectures internes de 200.
- `Path`, `MediaSources` et les autres champs optionnels généraux sont field-gated; ils ne sont plus fabriqués lorsque non demandés.
- La résolution triée de collections peut encore parcourir jusqu'à 200 000 entrées par dossier avant de rendre une fenêtre bornée; les résolutions sont parallélisées au plus par lots de 8.
- PlaybackInfo peut ouvrir/prober séquentiellement jusqu'à 15 sources, 15 s chacune au pire initial.
- `directorySize` parcourt le workspace HLS sous mutex à l'admission puis périodiquement; le coût croît avec jobs × fichiers.
- La playlist HLS locale est glissante et bornée à 120 segments avec suppression; les capabilities children tournent sous une borne testée.
- Les sous-titres convertis peuvent être bufferisés jusqu'à 16 MiB; le probe jusqu'à 4 MiB. Les limites sont utiles mais ne constituent pas un benchmark.
- Aucun benchmark, budget p95, profil d'allocations ou soak test Jellyfin n'a été observé. Toute affirmation de performance serait donc une **[INFERENCE]**.

### Sécurité

- Frontière d'identité : token compat lié au profil actif; UserId étranger devient 404. Les tokens opaques de 32 octets ne sont stockés que hashés.
- Désactiver la façade révoque toutes les sessions compat actives; la réactivation doit terminer la révocation de la génération précédente avant publication. Un échec bloque la transition.
- Les credentials amont, URL/headers provider et sourceRef restent internes. Les child URLs utilisent un PlaySessionId bearer lié owner/item/source/TTL; HTTPS reste obligatoire.
- Le playback vérifie owner, item, source et session; les cibles FFmpeg passent par une passerelle locale signée avec contrôle SSRF, redirects et protocoles.
- Les corps, queries, profils, playlists, sorties ffprobe, sous-titres, sessions et sockets sont bornés et échouent fermés.
- Les authentifications identiques sont coalescées; les opérations distinctes, fetches artwork, fichiers temporaires et transformations ont des quotas concurrents et rejettent à saturation.
- L'artwork anonyme reste refusé, même avec tag projeté; une credential explicitement invalide ne peut pas être contournée. Une future capability anonyme devrait rester opaque, item-bound, profile-safe et expirable.
- Le socket revalide le token et borne leases/messages; ses notifications locales restent un sous-ensemble fonctionnel, pas une parité Jellyfin.
- Les références AGPL/GPL/MPL ont été observées uniquement pour spécifier des comportements. Aucun code, test substantiel, table DTO expressive ou fixture sous ces licences ne doit être copié dans l'implémentation Apache-2.0.

## Limites de cette matrice

Cette matrice est evidence-backed au niveau dépôt et sources externes citées. Le smoke oracle archivé prouve 11 captures ciblées mais une seule comparaison exacte (`logout`); les dix autres étapes restent `per-target`/skipped. Les goldens HTTP runtime et scénarios FFmpeg réels sont des contrats Rivune locaux, jamais des affirmations « identique à Jellyfin 10.11.11 ». Infuse, VidHub et Streamyfin sont tous explicitement **non validés**. La promotion d'une route ou d'un workflow exige un résultat épinglé, reproductible et scrubbed au niveau correspondant de la méthode ci-dessus.
