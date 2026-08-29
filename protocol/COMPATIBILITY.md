# Protocol compatibility

The current Rivune wire protocol is **22**. Clients must call `GET /.well-known/rivune`, use the returned `apiBaseUrl`, and reject any `protocolVersion` they do not implement. The `/api/v1` namespace and `serverVersion` do not imply compatibility.

Optional discovery capabilities are additive. The server currently advertises `bounded-aggregate-resources`, `profile-archives-v2`, `request-correlation`, `local-recommendations`, `semantic-search`, `playback-coordination`, `playback-command-results`, `playback-explanations`, and `addon-verifications`. Clients must ignore unknown capability names and must not infer an endpoint from `serverVersion`.

## Version 22

Version 22 is a clean cutover from v21. Protocol-21 clients and servers must be upgraded together:

- add-ons are installed only from durable, expiring, one-shot verification snapshots whose successful private transport URL is encrypted at rest and removed after consumption; failed verifications retain no URL; `addon-verifications` advertises this behavior;
- playback device commands use caller UUID operation IDs, target revisions, and idempotent terminal results; `playback-command-results` advertises result reporting;
- profile archives are strict version 2 documents with identity, presentation, continue dismissals, and atomic create; `profile-archives-v2` advertises this shape;
- playback decisions use bounded closed diagnostic reasons and never expose execution pipelines; `playback-explanations` advertises those reasons;
- browser sessions are distinct from native sessions: `/auth/web/*` keeps the rotating refresh credential only in the host-only HttpOnly `rivune_web_refresh` cookie, while JavaScript receives a tab-local access token. The cookie is `SameSite=Strict`, scoped to `/api/v1/auth/web/refresh`, and secure except on an accepted local HTTP origin. Each browser-auth mutation requires `X-Rivune-CSRF: 1` and an exact `Origin`. With a configured public HTTPS origin, a direct request may instead use an allowed private IP literal exactly matching its host; DNS aliases and public IPs are rejected for this fallback. Without a configured public URL, the exact effective direct or trusted-forwarded origin is required. When `Sec-Fetch-Site` is present, it must be `same-origin`; forwarded origin headers count only from configured trusted proxies;
- profile queues under `/profiles/{profileId}/queue` are durable, ordered, operation-ID idempotent, identity-deduplicated, and optimistic-revision controlled; replay records expire after 24 hours;
- `/playback/failovers` persists a bounded ordered list of opaque source references and advances only for closed eligible source failures; policy, authorization, and decode failures never advance;
- `/saved-searches` stores profile searches, while `/smart-collections` stores and evaluates a closed typed rules AST with optimistic revision control;
- `/operations/extension-incidents` returns a bounded, manager-only profile timeline of closed classifications and timestamps without upstream URLs, credentials, queries, bodies, or raw errors;
- session notifications use `/auth/notifications` with decimal polling cursors and explicit acknowledgement. Targeted sends and idempotent administrator broadcasts enqueue only for sessions addressed or active when accepted; the contract does not provide push delivery;
- media notifications use `/media-notification-subscriptions` and `/media-notifications` as a separate durable profile inbox, with at most 4,096 followed titles per profile. Following a title establishes existing seasons and episodes as a silent baseline; read and dismissal acknowledgements are idempotent, and dismissed or expired records are omitted from pages. Subscription maintenance is bounded and paginated, with a durable cursor so an interrupted worker resumes rather than silently skipping later profiles;
- `/profiles/{profileId}/accessibility-preferences` stores an optimistic-revision profile document for reduced motion, contrast, text scale, captions, audio description, and focus indicators. A client applies supported preferences without claiming unavailable native capability.

The new persistence is installed by embedded migrations `000082` through `000094`. Operators must take and verify an authenticated backup before the first v22 start. Migration `000092` deliberately removes pending short-lived add-on verification snapshots while replacing plaintext transport storage with encrypted envelopes, so clients must verify again after upgrade. Migration `000093` adds bounded playback-failover cleanup indexes; migration `000094` adds durable worker progress for media-notification maintenance. The server applies migrations before readiness; clients must wait for `/ready` and then rediscover the protocol rather than retrying old v21 shapes.

## Version 21

Version 21 was a clean cutover from v20:

- native device authorization requires a stable, opaque installation identifier;
- requesting another code for the same installation replaces its previous unconsumed code;
- pairing-capacity responses return the remaining release delay in `Retry-After`, which clients must treat as the complete delay rather than an increment.

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

Without an increment, a server may add endpoints, optional properties, capabilities, or structured error codes while all existing v22 behavior remains valid. Clients must ignore unknown response properties and capability names and handle unknown error codes generically, but still reject missing or invalid required properties.

Access tokens are bearer tokens. Refresh tokens rotate on use. A retryable transport/server failure preserves the last credential set; only a definitive `401 invalid_refresh_token` clears it. Native clients store credentials in platform-secure storage.

## Client release check

1. Discover the server and require protocol 22.
2. Use the discovered API base URL.
3. Exercise login/refresh, account and profile selection, metadata, trailers, source listing, playback preparation/resolution, and stop.
4. Verify TMDB and TVDB hierarchies and preserve opaque IDs unchanged.
5. Confirm unknown optional properties and errors do not crash the client.
6. Exercise add-on verification then one-shot install, incoming command completion and outgoing result lookup, queue mutation retries, failover revision conflicts, and saved-search/smart-collection revision conflicts.
7. Keep session notifications and media notifications as separate stores and cursors; verify read, dismiss, acknowledgement, expiry, and foreign-profile isolation.
8. Round-trip every accessibility preference value and preserve the server revision while degrading honestly when a native capability is unavailable.
