# Rivune protocol compatibility

The current Rivune wire protocol is **version 16**. The HTTP namespace remains `/api/v1`; the namespace and protocol version are independent. Clients must discover a server through `GET /.well-known/rivune` before using authenticated API routes and must compare the returned `protocolVersion` with the version they implement.

## Version 16 cutover

Version 16 is a clean cutover. A v15 client is not compatible with a v16 server, and a v16 client must not silently continue against another protocol version.

Breaking changes in v16 include:

- **Trailers are plural.** The title trailer route is `GET /api/v1/metadata/titles/{titleId}/trailers`, not the former singular `/trailer` route. A successful response is a `TrailerList` object with a required `trailers` array rather than one trailer. The array contains one to five curated choices in preference order. Clients should initially select the first item while allowing the user to choose another.
- **Trailer requests support series seasons.** `seasonNumber` may be supplied for a series and must be omitted for a movie. `language` controls preferred trailer audio; `captionLanguage` is only a YouTube caption preference and is not a guarantee that captions exist. Localized choices may include English fallbacks identified by `isFallback`.
- **Series mapping is explicit.** Series and season metadata requests accept `mappingProvider=tmdb|tvdb`, and a series response reports the hierarchy actually returned in `mappingProvider`. The effective profile setting is `seriesMappingProvider`. Clients must pass the same mapping provider when loading a season selected from that series.
- **Mapped season identifiers are opaque.** TMDB season identifiers may be UUIDs, while TVDB-mapped season identifiers may use an opaque `tvdb:` form. Clients must not parse, synthesize, or require a UUID for a season ID; they must send the returned identifier unchanged. Episode and season ordering comes from the selected hierarchy.
- **Playback uses opaque source references.** Clients list sources, then pass the returned `sourceRef` to prepare or resolve. Provider URLs and private headers remain on the server and are never reconstructed client-side.
- **Addon search is unambiguous.** Catalog search uses `GET /api/v1/addons/catalogs/search/{type}` instead of `/addons/search/{type}`, which could collide with the installed-addon refresh route.

There are no compatibility aliases for the singular trailer route or pre-v16 trailer response. Upgrade clients and servers together.

## Compatibility policy

- A released client declares one implemented protocol integer. The Apple, Android, and Windows clients in this repository implement v16.
- A client must reject discovery when `protocolVersion` differs from its implemented version and present an upgrade-required error. It must not infer compatibility from `serverVersion` or `/api/v1`.
- A server may add endpoints, optional response properties, optional request properties, or new error codes without incrementing the protocol version when existing v16 behavior remains valid. Clients must ignore unknown response properties and handle unknown structured error codes generically.
- Removing or renaming a route or property, changing a property's type or meaning, adding a required request property, making an optional response property required, changing authentication semantics, or changing identifier interpretation requires a new protocol version.
- Within v16, an existing required response property remains required. Contract tests validate representative real handler responses against `openapi.yaml`; changes to those handlers and schemas must land together.
- Token rotation is part of the v16 contract. Access tokens are bearer tokens, refresh tokens are single-use and replaced on refresh, and native clients must persist credentials in platform-secure storage. A failed refresh clears the local credential set and requires authentication again.
- Server releases must continue serving the documented version until all bundled clients implement a newer version. A future server that supports multiple versions must advertise the selected protocol explicitly; clients must never assume an undocumented range.

## Client release checklist

1. Discover the candidate server and require `protocolVersion == 16`.
2. Resolve `apiBaseUrl` from discovery; do not construct it from the browser or app origin after discovery.
3. Exercise login or refresh, account/profile selection, movie and series metadata, plural trailers, source listing, preparation, resolution, and playback stop.
4. Verify both TMDB and TVDB series hierarchies and preserve mapped season IDs verbatim.
5. Decode unknown response properties and error codes without crashing, while still rejecting missing or invalid required properties.
