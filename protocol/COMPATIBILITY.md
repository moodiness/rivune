# Protocol compatibility

The current Rivune wire protocol is **20**. Clients must call `GET /.well-known/rivune`, use the returned `apiBaseUrl`, and reject any `protocolVersion` they do not implement. The `/api/v1` namespace and `serverVersion` do not imply compatibility.

Optional discovery capabilities are additive. Official clients currently recognize `profile-archives-v1`, `request-correlation`, and `bounded-aggregate-resources`, and ignore unknown capability names.

## Version 20

Version 20 is a clean cutover from v19:

- a Jellyfin username is the stable UUID of one profile, never a display or account name;
- profile application secrets are shown once, stored only as hashes, and never fall back to account passwords or PINs;
- rotating or revoking a profile secret revokes that profile's Jellyfin sessions;
- Quick Connect is device-bound, expires after 10 minutes, requires approval for a manageable profile, and exchanges once into that profile's compatibility session.

## Earlier retained contracts

| Version | Retained boundary |
| --- | --- |
| 19 | Web profile selection returns an opaque, tab-bound `profileContext`; missing, stale, or cross-tab contexts require reselection. |
| 18 | Access categories are server-authoritative; tokens carry immutable global/category scope; profile or device moves revoke incompatible sessions. |
| 17 | Interface language is discovered and profile-aware; richer artwork remains optional. |
| 16 | Trailers are plural; series mapping is explicit; season and playback source references are opaque; add-on search has an unambiguous route. |

There are no aliases for removed pre-v16 routes or response shapes. Upgrade incompatible clients and servers together.

## Change policy

A protocol increment is required when a change removes or renames a route/property, changes a type or meaning, adds a required request property, makes an optional response property required, changes authentication, or changes identifier interpretation.

Without an increment, a server may add endpoints, optional properties, capabilities, or structured error codes while all existing v20 behavior remains valid. Clients must ignore unknown response properties and capability names and handle unknown error codes generically, but still reject missing or invalid required properties.

Access tokens are bearer tokens. Refresh tokens rotate on use. A retryable transport/server failure preserves the last credential set; only a definitive `401 invalid_refresh_token` clears it. Native clients store credentials in platform-secure storage.

## Client release check

1. Discover the server and require protocol 20.
2. Use the discovered API base URL.
3. Exercise login/refresh, account and profile selection, metadata, trailers, source listing, playback preparation/resolution, and stop.
4. Verify TMDB and TVDB hierarchies and preserve opaque IDs unchanged.
5. Confirm unknown optional properties and errors do not crash the client.
