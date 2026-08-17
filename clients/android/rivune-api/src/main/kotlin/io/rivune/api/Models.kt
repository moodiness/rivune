package io.rivune.api

import java.util.UUID
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.decodeFromJsonElement

object RivuneProtocol {
    const val VERSION: Int = 20
}

enum class DiscoveryCapability(val identifier: String) {
    BOUNDED_AGGREGATE_RESOURCES("bounded-aggregate-resources"),
    PROFILE_ARCHIVES_V1("profile-archives-v1"),
    REQUEST_CORRELATION("request-correlation"),
}

private const val MAX_DISCOVERY_CAPABILITIES = 64
private const val MAX_DISCOVERY_CAPABILITY_LENGTH = 64
private val discoveryCapabilityPattern = Regex("^[a-z0-9]+(?:-[a-z0-9]+)*$")

private fun isSafeDiscoveryCapabilityIdentifier(identifier: String): Boolean =
    identifier.length in 1..MAX_DISCOVERY_CAPABILITY_LENGTH && discoveryCapabilityPattern.matches(identifier)

internal fun normalizedDiscoveryCapabilities(element: JsonElement?): List<String> {
    val values = element as? JsonArray ?: return emptyList()
    val capabilities = ArrayList<String>(minOf(values.size, MAX_DISCOVERY_CAPABILITIES))
    val seen = HashSet<String>(minOf(values.size, MAX_DISCOVERY_CAPABILITIES))
    for (value in values) {
        val primitive = value as? JsonPrimitive ?: continue
        if (!primitive.isString) continue
        val identifier = primitive.content
        if (!isSafeDiscoveryCapabilityIdentifier(identifier) || !seen.add(identifier)) continue
        capabilities.add(identifier)
        if (capabilities.size == MAX_DISCOVERY_CAPABILITIES) break
    }
    return capabilities
}

internal object DiscoveryCapabilitiesSerializer : KSerializer<List<String>> {
    private val delegate = ListSerializer(String.serializer())
    override val descriptor: SerialDescriptor = delegate.descriptor

    override fun serialize(encoder: Encoder, value: List<String>) = delegate.serialize(encoder, value)

    override fun deserialize(decoder: Decoder): List<String> {
        if (decoder is JsonDecoder) return normalizedDiscoveryCapabilities(decoder.decodeJsonElement())
        val values = delegate.deserialize(decoder)
        val capabilities = ArrayList<String>(minOf(values.size, MAX_DISCOVERY_CAPABILITIES))
        val seen = HashSet<String>(minOf(values.size, MAX_DISCOVERY_CAPABILITIES))
        for (identifier in values) {
            if (!isSafeDiscoveryCapabilityIdentifier(identifier) || !seen.add(identifier)) continue
            capabilities.add(identifier)
            if (capabilities.size == MAX_DISCOVERY_CAPABILITIES) break
        }
        return capabilities
    }
}

object UUIDSerializer : KSerializer<UUID> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("UUID", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: UUID) = encoder.encodeString(value.toString())
    override fun deserialize(decoder: Decoder): UUID = UUID.fromString(decoder.decodeString())
}

@Serializable
data class Discovery(
    val name: String,
    val serverVersion: String,
    val protocolVersion: Int,
    val apiBaseUrl: String,
    val setupRequired: Boolean,
    val setupCompleted: Boolean? = null,
    val demoAvailable: Boolean? = null,
    val timezone: String,
    val interfaceLanguage: String,
    @Serializable(with = DiscoveryCapabilitiesSerializer::class)
    val capabilities: List<String> = emptyList(),
) {
    fun supportsCapability(capability: DiscoveryCapability): Boolean = capability.identifier in capabilities

    val supportsProfileArchives: Boolean
        get() = supportsCapability(DiscoveryCapability.PROFILE_ARCHIVES_V1)
}

@Serializable
enum class AuthorizationScope {
    @SerialName("global_admin")
    GLOBAL_ADMIN,
    @SerialName("category")
    CATEGORY,
}

@Serializable
data class CategoryRef(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val name: String,
    val color: String?,
    val icon: String?,
)

private fun validateAuthorizationContext(
    authorizationScope: AuthorizationScope,
    category: CategoryRef?,
) {
    require(
        when (authorizationScope) {
            AuthorizationScope.GLOBAL_ADMIN -> category == null
            AuthorizationScope.CATEGORY -> category != null
        },
    ) {
        "global_admin authorization must not have a category, and category authorization must have a category"
    }
}

@Serializable
data class Category(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val name: String,
    val description: String?,
    val color: String?,
    val icon: String?,
    val position: Int,
    val isDefault: Boolean,
    val profileCount: Long,
    val deviceCount: Long,
    val createdAt: String,
    val updatedAt: String,
)

@Serializable
data class CategoryList(val categories: List<Category>)

sealed interface PatchField<out T> {
    data object Omitted : PatchField<Nothing>
    data object Null : PatchField<Nothing>
    data class Value<T>(val value: T) : PatchField<T>
}

@Serializable
data class CategoryCreateRequest(
    val name: String,
    val description: String? = null,
    val color: String? = null,
    val icon: String? = null,
)

data class CategoryUpdateRequest(
    val name: String? = null,
    val description: PatchField<String> = PatchField.Omitted,
    val color: PatchField<String> = PatchField.Omitted,
    val icon: PatchField<String> = PatchField.Omitted,
    val isDefault: Boolean? = null,
)

@Serializable
data class LoginDevice(
    @Serializable(with = UUIDSerializer::class) val id: UUID? = null,
    val name: String,
    val platform: String,
)

@Serializable
data class Device(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val name: String,
    val platform: String,
    @Serializable(with = UUIDSerializer::class) val categoryId: UUID,
    val category: CategoryRef,
    val internalNote: String?,
    val approvedAt: String?,
    val lastSeenAt: String?,
    val createdAt: String,
    val updatedAt: String,
)

@Serializable
data class DeviceList(val devices: List<Device>)

data class DeviceUpdateRequest(
    val name: String? = null,
    @Serializable(with = UUIDSerializer::class) val categoryId: UUID? = null,
    val internalNote: PatchField<String> = PatchField.Omitted,
)

@Serializable
data class LoginRequest(val username: String, val password: String, val device: LoginDevice)

@Serializable
data class TokenPair(
    val tokenType: String,
    val accessToken: String,
    val accessTokenExpiresAt: String,
    val refreshToken: String,
    val refreshTokenExpiresAt: String,
    @Serializable(with = UUIDSerializer::class) val sessionId: UUID,
    @Serializable(with = UUIDSerializer::class) val deviceId: UUID,
    val authorizationScope: AuthorizationScope,
    val category: CategoryRef?,
) {
    init {
        validateAuthorizationContext(authorizationScope, category)
    }
}

@Serializable
data class Account(
    val user: AccountUser,
    val session: AccountSession,
    val profiles: List<Profile>,
    val maintenance: MaintenanceSettings,
)

@Serializable
data class MaintenanceSettings(
    val enabled: Boolean,
    val message: String?,
)

@Serializable
data class AccountUser(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val username: String,
    val role: String,
)

@Serializable
data class AccountSession(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    @Serializable(with = UUIDSerializer::class) val deviceId: UUID,
    val activeProfile: ActiveProfileGrant?,
    val authorizationScope: AuthorizationScope,
    val category: CategoryRef?,
) {
    init {
        validateAuthorizationContext(authorizationScope, category)
    }
}

@Serializable
data class ActiveProfileGrant(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val expiresAt: String,
)

@Serializable
data class SessionList(val sessions: List<Session>)

@Serializable
data class Session(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    @Serializable(with = UUIDSerializer::class) val deviceId: UUID,
    val deviceName: String,
    val platform: String,
    val ipAddress: String?,
    val createdAt: String,
    val lastSeenAt: String,
    val current: Boolean,
    val authorizationScope: AuthorizationScope,
    val category: CategoryRef?,
) {
    init {
        validateAuthorizationContext(authorizationScope, category)
    }
}

@Serializable
data class ProfileSessionList(val sessions: List<ProfileSession>)

@Serializable
data class ProfileSession(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    @Serializable(with = UUIDSerializer::class) val userId: UUID,
    val username: String,
    @Serializable(with = UUIDSerializer::class) val deviceId: UUID,
    val deviceName: String,
    val platform: String,
    val ipAddress: String?,
    val createdAt: String,
    val lastSeenAt: String,
    val profileGrantExpiresAt: String,
    val current: Boolean,
    val authorizationScope: AuthorizationScope,
    val category: CategoryRef?,
) {
    init {
        validateAuthorizationContext(authorizationScope, category)
    }
}

@Serializable
data class ProfileList(val profiles: List<Profile>)

@Serializable
data class Profile(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val name: String,
    val description: String? = null,
    @Serializable(with = UUIDSerializer::class) val categoryId: UUID,
    val category: CategoryRef,
    val isChild: Boolean,
    val hasPin: Boolean,
    val canManage: Boolean,
    val enabled: Boolean,
    val availableFrom: String?,
    val availableUntil: String?,
    val accessStartTime: String?,
    val accessEndTime: String?,
    val accessTimezone: String,
    val accessible: Boolean,
    val avatar: ProfileAvatar,
)

@Serializable
data class ProfileAvatar(val kind: String, val presetId: String? = null, val url: String)

@Serializable
data class ProfileSelection(val profile: Profile, val expiresAt: String, val profileContext: String)

@Serializable
data class CategoryOrderRequest(
    val categoryIds: List<@Serializable(with = UUIDSerializer::class) UUID>,
)

@Serializable
data class ProfileCategoryMoveRequest(
    val profileIds: List<@Serializable(with = UUIDSerializer::class) UUID>,
    @Serializable(with = UUIDSerializer::class) val categoryId: UUID,
)

@Serializable
data class DeviceCategoryMoveRequest(
    val deviceIds: List<@Serializable(with = UUIDSerializer::class) UUID>,
    @Serializable(with = UUIDSerializer::class) val categoryId: UUID,
)

@Serializable
data class DeviceAuthorizationRequest(val deviceName: String, val platform: String)

@Serializable
data class DeviceAuthorizationResponse(
    val deviceCode: String,
    val userCode: String,
    val verificationUri: String,
    val verificationUriComplete: String,
    val expiresAt: String,
    val intervalSeconds: Int,
)

@Serializable
data class DeviceCodeApprovalRequest(
    val userCode: String,
    @Serializable(with = UUIDSerializer::class) val categoryId: UUID,
    val deviceName: String? = null,
    val internalNote: String? = null,
)

@Serializable
data class DeviceCodeTokenRequest(val deviceCode: String)

@Serializable
data class SettingsValues(
    val allowTranscoding: Boolean? = null,
    val transcoding: String? = null,
    val maximumCastMembers: Int? = null,
    val maximumResolution: String? = null,
    val preferDirectPlay: Boolean? = null,
    val audioLanguage: String? = null,
    val subtitleLanguage: String? = null,
    val forcedSubtitleLanguage: String? = null,
    val autoplayNextEpisode: Boolean? = null,
    val skipIntroEnabled: Boolean? = null,
    val skipRecapEnabled: Boolean? = null,
    val skipOutroEnabled: Boolean? = null,
    val metadataLanguage: String? = null,
)

data class ProfileSettingsUpdate(
    val maximumResolution: PatchField<String> = PatchField.Omitted,
    val preferDirectPlay: PatchField<Boolean> = PatchField.Omitted,
    val audioLanguage: PatchField<String> = PatchField.Omitted,
    val metadataLanguage: PatchField<String> = PatchField.Omitted,
    val subtitleLanguage: PatchField<String> = PatchField.Omitted,
    val forcedSubtitleLanguage: PatchField<String> = PatchField.Omitted,
    val autoplayNextEpisode: PatchField<Boolean> = PatchField.Omitted,
    val skipIntroEnabled: PatchField<Boolean> = PatchField.Omitted,
    val skipRecapEnabled: PatchField<Boolean> = PatchField.Omitted,
    val skipOutroEnabled: PatchField<Boolean> = PatchField.Omitted,
    val transcoding: PatchField<String> = PatchField.Omitted,
)

@Serializable
data class SettingsLayer(
    val schemaVersion: Int,
    val settings: SettingsValues,
    val updatedAt: String? = null,
)

@Serializable
data class EffectiveSettingsSources(
    val allowTranscoding: String? = null,
    val transcoding: String? = null,
    val maximumCastMembers: String? = null,
    val maximumResolution: String? = null,
    val preferDirectPlay: String? = null,
    val audioLanguage: String? = null,
    val subtitleLanguage: String? = null,
    val forcedSubtitleLanguage: String? = null,
    val autoplayNextEpisode: String? = null,
    val skipIntroEnabled: String? = null,
    val skipRecapEnabled: String? = null,
    val skipOutroEnabled: String? = null,
    val metadataLanguage: String? = null,
)

@Serializable
data class EffectiveSettings(
    val schemaVersion: Int,
    val settings: SettingsValues,
    val sources: EffectiveSettingsSources,
)

@Serializable
enum class MediaType {
    @SerialName("movie") MOVIE,
    @SerialName("series") SERIES,
    @SerialName("season") SEASON,
    @SerialName("episode") EPISODE,
}

@Serializable
enum class CalendarEventMediaType {
    @SerialName("movie") MOVIE,
    @SerialName("episode") EPISODE,
}

@Serializable
data class CalendarEventList(val events: List<CalendarEvent>)

@Serializable
data class CalendarEvent(
    val id: String,
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val mediaType: CalendarEventMediaType,
    val title: String,
    val releaseDate: String,
    val posterUrl: String? = null,
    val resourceId: String? = null,
    val resourceProvider: String? = null,
    val seriesTitle: String? = null,
    @Serializable(with = UUIDSerializer::class) val seriesId: UUID? = null,
    @Serializable(with = UUIDSerializer::class) val seasonId: UUID? = null,
    val seasonNumber: Int? = null,
    val episodeNumber: Int? = null,
)

@Serializable
enum class SeriesMappingProvider {
    @SerialName("tmdb") TMDB,
    @SerialName("tvdb") TVDB;

    val wireValue: String get() = name.lowercase()
}

@Serializable
data class Genre(val id: Int, val name: String)

@Serializable
data class CastMember(
    val id: String,
    val name: String,
    val character: String? = null,
    val profileUrl: String? = null,
)

@Serializable
data class Movie(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val mediaType: MediaType,
    val title: String,
    val originalTitle: String,
    val originalLanguage: String,
    val overview: String,
    val releaseDate: String? = null,
    val posterUrl: String? = null,
    val backdropUrl: String? = null,
    val logoUrl: String? = null,
    val tagline: String? = null,
    val runtimeMinutes: Int? = null,
    val genres: List<Genre>,
    val cast: List<CastMember>,
    val voteAverage: Double,
    val voteCount: Int,
    val externalIds: Map<String, String>,
)

@Serializable
data class Series(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val mediaType: MediaType,
    val name: String,
    val originalName: String,
    val originalLanguage: String,
    val overview: String,
    val firstAirDate: String? = null,
    val lastAirDate: String? = null,
    val posterUrl: String? = null,
    val backdropUrl: String? = null,
    val logoUrl: String? = null,
    val tagline: String? = null,
    val status: String? = null,
    val numberOfSeasons: Int? = null,
    val numberOfEpisodes: Int? = null,
    val genres: List<Genre>,
    val cast: List<CastMember>,
    val voteAverage: Double,
    val voteCount: Int,
    val seasons: List<SeasonSummary>,
    val aliases: List<SeriesAlias>,
    val episodeOrders: List<EpisodeOrder>,
    val selectedEpisodeOrderId: String? = null,
    val mappingProvider: SeriesMappingProvider,
    val externalIds: Map<String, String>,
)

@Serializable
data class SeriesAlias(val language: String, val name: String)

@Serializable
data class EpisodeOrder(val id: String, val name: String, val type: String, val isDefault: Boolean)

@Serializable
data class SeasonSummary(
    val id: String,
    val mediaType: MediaType,
    @Serializable(with = UUIDSerializer::class) val seriesId: UUID,
    val name: String,
    val overview: String,
    val seasonNumber: Int,
    val episodeCount: Int,
    val airDate: String? = null,
    val posterUrl: String? = null,
    val backdropUrl: String? = null,
    val voteAverage: Double,
    val externalIds: Map<String, String>,
)

@Serializable
data class Season(
    val id: String,
    val mediaType: MediaType,
    @Serializable(with = UUIDSerializer::class) val seriesId: UUID,
    val name: String,
    val overview: String,
    val seasonNumber: Int,
    val airDate: String? = null,
    val posterUrl: String? = null,
    val backdropUrl: String? = null,
    val voteAverage: Double,
    val episodes: List<Episode>,
    val externalIds: Map<String, String>,
)

@Serializable
data class Episode(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val mediaType: MediaType,
    val seasonId: String,
    val name: String,
    val overview: String,
    val seasonNumber: Int,
    val episodeNumber: Int,
    val airDate: String? = null,
    val stillUrl: String? = null,
    val backdropUrl: String? = null,
    val runtimeMinutes: Int? = null,
    val voteAverage: Double,
    val voteCount: Int,
    val externalIds: Map<String, String>,
)

@Serializable
data class TrailerList(val trailers: List<Trailer>)

@Serializable
data class Trailer(
    val youtubeId: String,
    val name: String,
    val language: String,
    val isFallback: Boolean,
    val captionPreference: String? = null,
)

@Serializable
data class PlaybackMediaProfile(
    val container: String,
    val videoCodec: String,
    val audioCodec: String? = null,
    val maximumVideoBitDepth: Int? = null,
)

@Serializable
enum class PlaybackProcessingMode {
    @SerialName("remux") REMUX,
    @SerialName("transcode_audio") TRANSCODE_AUDIO,
    @SerialName("transcode") TRANSCODE,
}

@Serializable
enum class PlaybackSubtitleMode {
    @SerialName("external") EXTERNAL,
    @SerialName("burn") BURN,
}

@Serializable
data class PlaybackCapabilities(
    val streamingProtocols: List<String>,
    val containers: List<String>,
    val videoCodecs: List<String>? = null,
    val audioCodecs: List<String>? = null,
    val hdrFormats: List<String>? = null,
    val processingModes: List<PlaybackProcessingMode>? = null,
    val maximumHeight: Int? = null,
    val maximumVideoBitrateKbps: Int? = null,
    val maximumAudioChannels: Int? = null,
    val subtitleModes: List<PlaybackSubtitleMode>? = null,
    val mediaProfiles: List<PlaybackMediaProfile>? = null,
    val externalPlayers: List<String>? = null,
)

@Serializable
data class PlaybackSourceList(
    @Serializable(with = PlaybackSourceOptionListSerializer::class)
    val sources: List<PlaybackSourceOption> = emptyList(),
    @Serializable(with = PlaybackProviderErrorListSerializer::class)
    val providerErrors: List<PlaybackProviderError> = emptyList(),
)

@Serializable
data class PlaybackSourceOption(
    val id: String,
    val sourceRef: String,
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val addonName: String? = null,
    val manifestId: String,
    val streamIndex: Int,
    val name: String,
    val description: String? = null,
    val filename: String? = null,
    val protocol: String,
    val mode: PlaybackMode? = null,
    val container: String? = null,
    val expiresAt: String,
    val stableIdentity: String = "",
)

@Serializable
enum class PlaybackMode {
    @SerialName("direct") DIRECT,
    @SerialName("remux") REMUX,
    @SerialName("transcode_audio") TRANSCODE_AUDIO,
    @SerialName("transcode") TRANSCODE,
    @SerialName("youtube") YOUTUBE,
    @SerialName("external") EXTERNAL,
}

@Serializable
enum class PlaybackDecisionReason {
    @SerialName("direct_supported") DIRECT_SUPPORTED,
    @SerialName("remux_required") REMUX_REQUIRED,
    @SerialName("audio_transcode_required") AUDIO_TRANSCODE_REQUIRED,
    @SerialName("video_transcode_required") VIDEO_TRANSCODE_REQUIRED,
    @SerialName("subtitle_burn_required") SUBTITLE_BURN_REQUIRED,
}

@Serializable
enum class PlaybackTrackAction {
    @SerialName("copy") COPY,
    @SerialName("transcode") TRANSCODE,
}

@Serializable
enum class PlaybackSubtitleAction {
    @SerialName("none") NONE,
    @SerialName("external") EXTERNAL,
    @SerialName("copy") COPY,
    @SerialName("burn") BURN,
}

@Serializable
enum class PlaybackMediaTimeline {
    @SerialName("absolute") ABSOLUTE,
    @SerialName("relative") RELATIVE,
}

@Serializable
enum class PlaybackMediaTrackType {
    @SerialName("video") VIDEO,
    @SerialName("audio") AUDIO,
    @SerialName("subtitle") SUBTITLE,
}

@Serializable
enum class PlaybackSubtitleDelivery {
    @SerialName("external") EXTERNAL,
    @SerialName("burn") BURN,
}

@Serializable
data class PlaybackPreparation(
    val sourceRef: String,
    val mode: PlaybackMode,
    val protocol: String,
    val container: String? = null,
    val media: PlaybackMediaInspection? = null,
    val subtitleCount: Int,
    val expiresAt: String,
    val decision: PlaybackDecision? = null,
)

@Serializable
data class PlaybackSession(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val selectedSourceId: String,
    val selectedAudioTrack: Int? = null,
    val selectedSubtitleId: String? = null,
    @Serializable(with = PlaybackSourceListSerializer::class)
    val sources: List<PlaybackSource> = emptyList(),
    @Serializable(with = PlaybackSubtitleListSerializer::class)
    val subtitles: List<PlaybackSubtitle> = emptyList(),
    @Serializable(with = PlaybackProviderErrorListSerializer::class)
    val providerErrors: List<PlaybackProviderError> = emptyList(),
    val expiresAt: String,
)

@Serializable
data class PlaybackSource(
    val id: String,
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String,
    val name: String? = null,
    val title: String? = null,
    val mode: PlaybackMode,
    val url: String? = null,
    val ytId: String? = null,
    val infoHash: String? = null,
    val fileIndex: Int? = null,
    val protocol: String,
    val container: String? = null,
    val mediaTimeline: PlaybackMediaTimeline? = null,
    val compatible: Boolean,
    val media: PlaybackMediaInspection? = null,
    val decision: PlaybackDecision? = null,
)

@Serializable
data class PlaybackMediaInspection(
    val container: String? = null,
    val durationSeconds: Double? = null,
    val hdrFormat: String? = null,
    @Serializable(with = PlaybackMediaTrackListSerializer::class)
    val videoTracks: List<PlaybackMediaTrack> = emptyList(),
    @Serializable(with = PlaybackMediaTrackListSerializer::class)
    val audioTracks: List<PlaybackMediaTrack> = emptyList(),
    @Serializable(with = PlaybackMediaTrackListSerializer::class)
    val subtitleTracks: List<PlaybackMediaTrack> = emptyList(),
)

@Serializable
data class PlaybackDecision(
    val reason: PlaybackDecisionReason,
    val videoAction: PlaybackTrackAction,
    val audioAction: PlaybackTrackAction,
    val subtitleAction: PlaybackSubtitleAction,
    val toneMapping: Boolean,
    val source: PlaybackDecisionSource? = null,
    val target: PlaybackDecisionTarget? = null,
)

@Serializable
data class PlaybackDecisionSource(
    val container: String? = null,
    val videoCodec: String? = null,
    val audioCodec: String? = null,
    val height: Int? = null,
    val videoBitrateKbps: Int? = null,
    val hdrFormat: String? = null,
)

@Serializable
data class PlaybackDecisionTarget(
    val protocol: String? = null,
    val container: String? = null,
    val videoCodec: String? = null,
    val audioCodec: String? = null,
    val height: Int? = null,
    val videoBitDepth: Int? = null,
    val videoBitrateKbps: Int? = null,
)

@Serializable
data class PlaybackMediaTrack(
    val index: Int,
    val type: PlaybackMediaTrackType,
    val codec: String,
    val profile: String? = null,
    val language: String? = null,
    val forced: Boolean? = null,
    val title: String? = null,
    val width: Int? = null,
    val height: Int? = null,
    val channels: Int? = null,
)


@Serializable
data class PlaybackSubtitle(
    val id: String,
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String,
    val language: String? = null,
    val forced: Boolean? = null,
    val default: Boolean? = null,
    val delivery: PlaybackSubtitleDelivery? = null,
    val url: String? = null,
)

@Serializable
data class PlaybackProviderError(
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String,
    val code: String,
    val message: String,
)

internal abstract class NullAsEmptyListSerializer<Element>(elementSerializer: KSerializer<Element>) : KSerializer<List<Element>> {
    private val delegate = ListSerializer(elementSerializer)
    override val descriptor: SerialDescriptor = delegate.descriptor

    override fun serialize(encoder: Encoder, value: List<Element>) = delegate.serialize(encoder, value)

    override fun deserialize(decoder: Decoder): List<Element> {
        if (decoder !is JsonDecoder) return delegate.deserialize(decoder)
        val element = decoder.decodeJsonElement()
        return if (element is JsonNull) emptyList() else decoder.json.decodeFromJsonElement(delegate, element)
    }
}

internal object PlaybackMediaTrackListSerializer : NullAsEmptyListSerializer<PlaybackMediaTrack>(PlaybackMediaTrack.serializer())
internal object PlaybackSourceOptionListSerializer : NullAsEmptyListSerializer<PlaybackSourceOption>(PlaybackSourceOption.serializer())
internal object PlaybackSourceListSerializer : NullAsEmptyListSerializer<PlaybackSource>(PlaybackSource.serializer())
internal object PlaybackSubtitleListSerializer : NullAsEmptyListSerializer<PlaybackSubtitle>(PlaybackSubtitle.serializer())
internal object PlaybackProviderErrorListSerializer : NullAsEmptyListSerializer<PlaybackProviderError>(PlaybackProviderError.serializer())

@Serializable
enum class PlaybackMarkerType {
    @SerialName("intro") INTRO,
    @SerialName("recap") RECAP,
    @SerialName("outro") OUTRO,
}

@Serializable
data class PlaybackMarker(
    val type: PlaybackMarkerType,
    val startSeconds: Double,
    val endSeconds: Double,
    val confidence: Double,
    val submissionCount: Int,
)

@Serializable
data class PlaybackMarkerList(val markers: List<PlaybackMarker>)

@Serializable
data class PlaybackActivity(
    val summary: PlaybackActivitySummary,
    val diagnostics: PlaybackMediaDiagnostics,
    val sessions: List<PlaybackActivitySession>,
    val jobs: List<PlaybackMediaJob>,
    val sessionsTruncated: Boolean,
    val jobsTruncated: Boolean,
)

@Serializable
data class PlaybackActivitySummary(
    val activeSessions: Int,
    val activeJobs: Int,
    val processingSlots: Int,
    val processingLimit: Int,
    val storageBytes: Long,
    val storageLimitBytes: Long,
)

@Serializable
enum class PlaybackHardwareAcceleration {
    @SerialName("unknown") UNKNOWN,
    @SerialName("auto") AUTO,
    @SerialName("software") SOFTWARE,
    @SerialName("hybrid") HYBRID,
    @SerialName("vaapi") VAAPI,
    @SerialName("qsv") QSV,
    @SerialName("nvenc") NVENC,
    @SerialName("amf") AMF,
}

@Serializable
enum class PlaybackVideoCodec {
    @SerialName("h264") H264,
    @SerialName("hevc") HEVC,
    @SerialName("av1") AV1,
}

@Serializable
enum class PlaybackPreferredVideoCodec {
    @SerialName("auto") AUTO,
    @SerialName("h264") H264,
    @SerialName("hevc") HEVC,
    @SerialName("av1") AV1,
}

@Serializable
enum class PlaybackQualityPreset {
    @SerialName("speed") SPEED,
    @SerialName("balanced") BALANCED,
    @SerialName("quality") QUALITY,
}

@Serializable
enum class PlaybackToneMapBackend {
    @SerialName("vulkan") VULKAN,
    @SerialName("vaapi") VAAPI,
    @SerialName("hybrid") HYBRID,
    @SerialName("software") SOFTWARE,
}

@Serializable
data class PlaybackMediaDiagnostics(
    val ffmpegVersion: String,
    val ffprobeVersion: String,
    val hardwareAcceleration: PlaybackHardwareAcceleration,
    val videoEncoder: String,
    val preferredVideoCodec: PlaybackPreferredVideoCodec,
    val encodeCodecs: List<PlaybackVideoCodec>,
    val decodeCodecs: List<PlaybackVideoCodec>,
    val hevcMain10: Boolean? = null,
    val qualityPreset: PlaybackQualityPreset,
    val hardwareToneMap: Boolean,
    val toneMapBackend: PlaybackToneMapBackend,
    val transcodeThreads: Int,
    val maximumReadRate: Double,
    val totals: PlaybackMediaProcessTotals,
    val pools: PlaybackMediaDiagnosticPools,
)

@Serializable
data class PlaybackMediaProcessTotals(
    val started: Long,
    val succeeded: Long,
    val failed: Long,
    val softwareFallbacks: Long,
)

@Serializable
data class PlaybackMediaDiagnosticPools(
    val process: PlaybackMediaDiagnosticPool,
    val probe: PlaybackMediaDiagnosticPool,
    val subtitle: PlaybackMediaDiagnosticPool,
    val trickplay: PlaybackMediaDiagnosticPool,
)

@Serializable
data class PlaybackMediaDiagnosticPool(val active: Int, val limit: Int)

@Serializable
enum class PlaybackActivityMode {
    @SerialName("direct") DIRECT,
    @SerialName("remux") REMUX,
    @SerialName("transcode_audio") TRANSCODE_AUDIO,
    @SerialName("transcode") TRANSCODE,
    @SerialName("unknown") UNKNOWN,
}

@Serializable
data class PlaybackActivityExternalIds(
    val imdb: String? = null,
    val tmdb: String? = null,
    val tvdb: String? = null,
)

@Serializable
data class PlaybackActivityExternalIdMediaTypes(
    val imdb: MediaType? = null,
    val tmdb: MediaType? = null,
    val tvdb: MediaType? = null,
)

@Serializable
data class PlaybackActivitySession(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val titleId: String? = null,
    val artworkUrl: String? = null,
    val externalIds: PlaybackActivityExternalIds? = null,
    val externalIdMediaTypes: PlaybackActivityExternalIdMediaTypes? = null,
    val title: String,
    val mediaType: String,
    val mode: PlaybackActivityMode,
    val decision: PlaybackDecision? = null,
    val username: String,
    @Serializable(with = UUIDSerializer::class) val profileId: UUID,
    val profile: String,
    val device: String,
    val platform: String,
    val processing: Boolean,
    val positionSeconds: Int,
    val durationSeconds: Int,
    val createdAt: String,
    val lastSeenAt: String,
    val expiresAt: String,
)

@Serializable
enum class PlaybackMediaJobState {
    @SerialName("processing") PROCESSING,
    @SerialName("complete") COMPLETE,
    @SerialName("failed") FAILED,
}

@Serializable
enum class PlaybackMediaJobErrorClass {
    @SerialName("capacity") CAPACITY,
    @SerialName("source") SOURCE,
    @SerialName("processing") PROCESSING,
    @SerialName("storage") STORAGE,
    @SerialName("timeout") TIMEOUT,
    @SerialName("cancelled") CANCELLED,
    @SerialName("unknown") UNKNOWN,
}

@Serializable
data class PlaybackMediaJob(
    @Serializable(with = UUIDSerializer::class) val sessionId: UUID? = null,
    val assetId: String,
    val mode: String,
    val state: PlaybackMediaJobState,
    val errorClass: PlaybackMediaJobErrorClass? = null,
    val prewarming: Boolean,
    val progressPercent: Double? = null,
    val speed: Double? = null,
    val startupDurationSeconds: Double? = null,
    val createdAt: String,
    val lastSeenAt: String,
)

@Serializable
enum class PlaybackProgressMediaType {
    @SerialName("movie") MOVIE,
    @SerialName("episode") EPISODE,
}

@Serializable
data class PlaybackProgress(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val mediaType: PlaybackProgressMediaType,
    val positionSeconds: Int,
    val durationSeconds: Int,
    val completed: Boolean,
    val version: Long,
    val lastWatchedAt: String,
    val updatedAt: String,
)

@Serializable
data class PlaybackProgressBatchRequest(
    val titleIds: List<@Serializable(with = UUIDSerializer::class) UUID>,
)

@Serializable
data class PlaybackProgressBatchItem(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val progress: PlaybackProgress?,
)

@Serializable
data class PlaybackProgressBatch(val items: List<PlaybackProgressBatchItem>)

@Serializable
data class SetWatchedBatchItem(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val completed: Boolean,
    val expectedVersion: Long,
)

@Serializable
data class SetWatchedBatchRequest(val items: List<SetWatchedBatchItem>)

@Serializable
data class SetWatchedBatchResultItem(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val progress: PlaybackProgress,
)

@Serializable
data class SetWatchedBatchResult(val items: List<SetWatchedBatchResultItem>)

@Serializable
data class UpdatePlaybackProgressRequest(
    val positionSeconds: Int,
    val durationSeconds: Int,
    val completed: Boolean,
    val expectedVersion: Long,
)

@Serializable
data class CompletionRequest(val expectedVersion: Long)

@Serializable
enum class ContinueWatchingReason {
    @SerialName("resume") RESUME,
    @SerialName("next_episode") NEXT_EPISODE,
}

@Serializable
data class ContinueWatchingItem(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val mediaType: PlaybackProgressMediaType,
    @Serializable(with = UUIDSerializer::class) val seriesId: UUID? = null,
    @Serializable(with = UUIDSerializer::class) val seasonId: UUID? = null,
    val seasonNumber: Int? = null,
    val episodeNumber: Int? = null,
    val positionSeconds: Int,
    val durationSeconds: Int,
    val version: Long,
    val reason: ContinueWatchingReason,
    val lastWatchedAt: String,
)

@Serializable
data class ContinueWatchingPage(val items: List<ContinueWatchingItem>)

@Serializable
data class CollectionList(val collections: List<Collection>)

@Serializable
enum class CollectionViewMode {
    @SerialName("tabbed_grid") TABBED_GRID,
    @SerialName("rows") ROWS,
    @SerialName("follow_layout") FOLLOW_LAYOUT,
}

@Serializable
enum class CollectionTileShape {
    @SerialName("poster") POSTER,
    @SerialName("landscape") LANDSCAPE,
    @SerialName("square") SQUARE,
}

@Serializable
enum class CollectionSourceView {
    @SerialName("merged") MERGED,
    @SerialName("categories") CATEGORIES,
    @SerialName("folders") FOLDERS,
}

@Serializable
enum class CollectionSourceKind {
    @SerialName("addon_catalog") ADDON_CATALOG,
    @SerialName("tmdb") TMDB,
    @SerialName("trakt") TRAKT,
    @SerialName("mdblist") MDBLIST,
}

@Serializable
data class Collection(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val title: String,
    val backdropImageUrl: String? = null,
    val heroEnabled: Boolean,
    val pinToTop: Boolean,
    val focusGlowEnabled: Boolean,
    val viewMode: CollectionViewMode,
    val folderCoverShape: CollectionTileShape,
    val folders: List<CollectionFolder>,
    val profileIds: List<@Serializable(with = UUIDSerializer::class) UUID>,
    val categoryIds: List<@Serializable(with = UUIDSerializer::class) UUID>,
    val position: Int,
    val version: Int,
    val createdAt: String,
    val updatedAt: String,
)

@Serializable
data class CollectionFolder(
    @Serializable(with = UUIDSerializer::class) val id: UUID? = null,
    val title: String,
    val tileShape: CollectionTileShape,
    val sourceView: CollectionSourceView? = null,
    val coverImageUrl: String? = null,
    val coverEmoji: String? = null,
    val titleLogoUrl: String? = null,
    val heroBackdropUrl: String? = null,
    val heroVideoUrl: String? = null,
    val focusGifUrl: String? = null,
    val focusGifEnabled: Boolean,
    val hideTitle: Boolean,
    val sources: List<CollectionSource>,
)

@Serializable
data class CollectionSource(
    @Serializable(with = UUIDSerializer::class) val id: UUID? = null,
    val kind: CollectionSourceKind,
    val title: String,
    val addonCatalog: CollectionAddonCatalogSource? = null,
    val tmdb: CollectionTMDBSource? = null,
    val trakt: CollectionTraktSource? = null,
    val mdblist: CollectionMDBListSource? = null,
)

@Serializable
data class CollectionAddonCatalogSource(
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String? = null,
    val type: String,
    val catalogId: String,
    val extra: List<CollectionExtraValue>? = null,
)

@Serializable
data class CollectionExtraValue(val name: String, val value: String)

@Serializable
enum class CollectionTMDBSourceType {
    @SerialName("list") LIST,
    @SerialName("company") COMPANY,
    @SerialName("network") NETWORK,
    @SerialName("collection") COLLECTION,
    @SerialName("person") PERSON,
    @SerialName("director") DIRECTOR,
    @SerialName("discover") DISCOVER,
}

@Serializable
enum class CollectionTMDBMediaType {
    @SerialName("movie") MOVIE,
    @SerialName("series") SERIES,
    @SerialName("both") BOTH,
}

@Serializable
enum class CollectionTMDBSort {
    @SerialName("original") ORIGINAL,
    @SerialName("popularity.desc") POPULARITY_DESC,
    @SerialName("vote_average.desc") VOTE_AVERAGE_DESC,
    @SerialName("vote_count.desc") VOTE_COUNT_DESC,
    @SerialName("release_date.desc") RELEASE_DATE_DESC,
    @SerialName("first_air_date.desc") FIRST_AIR_DATE_DESC,
}

@Serializable
data class CollectionTMDBSource(
    val sourceType: CollectionTMDBSourceType,
    val tmdbId: Long? = null,
    val mediaType: CollectionTMDBMediaType,
    val sort: CollectionTMDBSort,
    val filters: CollectionTMDBFilters,
)

@Serializable
data class CollectionTMDBFilters(
    val genres: List<Long>? = null,
    val releaseDateFrom: String? = null,
    val releaseDateTo: String? = null,
    val voteAverageMin: Double? = null,
    val voteAverageMax: Double? = null,
    val voteCountMin: Int? = null,
    val originalLanguage: String? = null,
    val originCountry: String? = null,
    val keywords: List<Long>? = null,
    val companies: List<Long>? = null,
    val networks: List<Long>? = null,
    val year: Int? = null,
    val watchRegion: String? = null,
    val watchProviders: List<Long>? = null,
)

@Serializable
enum class CollectionTraktMediaType {
    @SerialName("movie") MOVIE,
    @SerialName("series") SERIES,
}

@Serializable
enum class CollectionTraktSortBy {
    @SerialName("rank") RANK,
    @SerialName("added") ADDED,
    @SerialName("title") TITLE,
    @SerialName("released") RELEASED,
    @SerialName("runtime") RUNTIME,
    @SerialName("popularity") POPULARITY,
    @SerialName("percentage") PERCENTAGE,
    @SerialName("votes") VOTES,
}

@Serializable
enum class CollectionSortOrder {
    @SerialName("asc") ASC,
    @SerialName("desc") DESC,
}

@Serializable
data class CollectionTraktSource(
    val listId: Long,
    val mediaType: CollectionTraktMediaType,
    val sortBy: CollectionTraktSortBy,
    val sortHow: CollectionSortOrder,
)

@Serializable
enum class CollectionMDBListMediaType {
    @SerialName("movie") MOVIE,
    @SerialName("series") SERIES,
}

@Serializable
enum class CollectionMDBListSort {
    @SerialName("added") ADDED,
    @SerialName("budget") BUDGET,
    @SerialName("imdbpopular") IMDBPOPULAR,
    @SerialName("imdbrating") IMDBRATING,
    @SerialName("imdbvotes") IMDBVOTES,
    @SerialName("last_air_date") LAST_AIR_DATE,
    @SerialName("letterrating") LETTERRATING,
    @SerialName("lettervotes") LETTERVOTES,
    @SerialName("metacritic") METACRITIC,
    @SerialName("myanimelist") MYANIMELIST,
    @SerialName("random") RANDOM,
    @SerialName("rank") RANK,
    @SerialName("released") RELEASED,
    @SerialName("releasedigital") RELEASEDIGITAL,
    @SerialName("revenue") REVENUE,
    @SerialName("rogerebert") ROGEREBERT,
    @SerialName("rtaudience") RTAUDIENCE,
    @SerialName("rtomatoes") RTOMATOES,
    @SerialName("runtime") RUNTIME,
    @SerialName("score") SCORE,
    @SerialName("score_average") SCORE_AVERAGE,
    @SerialName("sort_title") SORT_TITLE,
    @SerialName("title") TITLE,
    @SerialName("tmdbpopular") TMDBPOPULAR,
    @SerialName("usort") USORT,
}

@Serializable
data class CollectionMDBListSource(
    val listId: Long,
    val mediaType: CollectionMDBListMediaType,
    val sort: CollectionMDBListSort,
    val order: CollectionSortOrder,
)

@Serializable
data class ResolvedCollectionFolder(
    @Serializable(with = UUIDSerializer::class) val collectionId: UUID,
    val folder: CollectionFolder,
    val sourcePosterUrls: Map<String, String>? = null,
    val items: List<CollectionItem>,
    val page: Int,
    val hasMore: Boolean,
    val errors: List<CollectionSourceFailure>,
)

@Serializable
data class CollectionItem(
    val id: String,
    val mediaType: String,
    val title: String,
    val posterUrl: String? = null,
    val backgroundUrl: String? = null,
    val logoUrl: String? = null,
    val description: String? = null,
    val releaseInfo: String? = null,
    val released: String? = null,
    val voteAverage: Double? = null,
    val voteCount: Int? = null,
    val popularity: Double? = null,
    val externalIds: Map<String, String>,
    val sources: List<CollectionSourceReference>,
    val raw: JsonElement? = null,
)

@Serializable
data class CollectionSourceReference(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val kind: CollectionSourceKind,
    val title: String,
    @Serializable(with = UUIDSerializer::class) val addonId: UUID? = null,
    val manifestId: String? = null,
    val catalogId: String? = null,
)

@Serializable
enum class CollectionSourceFailureCode {
    @SerialName("collection_provider_unavailable") COLLECTION_PROVIDER_UNAVAILABLE,
    @SerialName("collection_addon_not_found") COLLECTION_ADDON_NOT_FOUND,
    @SerialName("collection_source_unsupported") COLLECTION_SOURCE_UNSUPPORTED,
    @SerialName("collection_source_timeout") COLLECTION_SOURCE_TIMEOUT,
    @SerialName("collection_source_failed") COLLECTION_SOURCE_FAILED,
}

@Serializable
data class CollectionSourceFailure(
    @Serializable(with = UUIDSerializer::class) val sourceId: UUID,
    val kind: CollectionSourceKind,
    val code: CollectionSourceFailureCode,
    val message: String,
)

@Serializable
data class StremioExtraProperty(
    val name: String,
    val isRequired: Boolean? = null,
    val default: String? = null,
    val options: List<String>? = null,
    val optionsLimit: Int? = null,
)

@Serializable
data class StremioManifestCatalog(
    val type: String,
    val id: String,
    val name: String? = null,
    val genres: List<String>? = null,
    val extra: List<StremioExtraProperty>? = null,
    val extraRequired: List<String>? = null,
    val extraSupported: List<String>? = null,
)

@Serializable
data class AddonCatalogDescriptorList(val catalogs: List<AddonCatalogDescriptor>)

@Serializable
data class AddonCatalogDescriptor(
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val addonName: String? = null,
    val addonLogoUrl: String? = null,
    val manifestId: String,
    val position: Int,
    val catalog: StremioManifestCatalog,
    val addonCatalog: Boolean,
    val searchable: Boolean,
)

@Serializable
data class AddonCachePolicy(
    val maxAgeSeconds: Long? = null,
    val staleWhileRevalidateSeconds: Long? = null,
    val staleIfErrorSeconds: Long? = null,
)

@Serializable
data class AddonExtraValue(val name: String, val value: String)

@Serializable
data class AddonResourceResult(
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String,
    val resource: String,
    val type: String,
    val id: String,
    val payload: JsonObject,
    val cache: AddonCachePolicy,
    val extra: List<AddonExtraValue>? = null,
)

@Serializable
data class AddonResourceFailure(
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String,
    val code: String,
    val message: String,
)

@Serializable
data class AddonResourceBatch(
    val results: List<AddonResourceResult>,
    val errors: List<AddonResourceFailure>,
)

@Serializable
enum class TitleMediaType {
    @SerialName("movie") MOVIE,
    @SerialName("series") SERIES,
    @SerialName("tv") TV;

    val wireValue: String get() = name.lowercase()
}

@Serializable
data class TitleResolveInput(
    val mediaType: TitleMediaType,
    val provider: String,
    val externalId: String? = null,
    val resourceId: String,
    val title: String,
    val posterUrl: String? = null,
    val backgroundUrl: String? = null,
    val releaseInfo: String? = null,
    val released: String? = null,
    @Serializable(with = UUIDSerializer::class) val sourceAddonId: UUID? = null,
    val sourceCatalogId: String? = null,
    val sourceName: String? = null,
    val country: String? = null,
    val language: String? = null,
    val category: String? = null,
)

@Serializable
data class TitleReference(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val mediaType: TitleMediaType,
    val provider: String,
    val externalId: String,
    val resourceId: String,
    val title: String,
    val posterUrl: String? = null,
    val backgroundUrl: String? = null,
    val releaseInfo: String? = null,
    @Serializable(with = UUIDSerializer::class) val sourceAddonId: UUID? = null,
    val sourceCatalogId: String? = null,
    val sourceName: String? = null,
    val country: String? = null,
    val language: String? = null,
    val category: String? = null,
)

@Serializable
data class CustomSeriesResolveInput(
    @Serializable(with = UUIDSerializer::class) val sourceAddonId: UUID,
    val sourceType: String,
    val series: CustomSeriesSnapshot,
    val videos: List<CustomVideoSnapshot>,
)

@Serializable
data class CustomSeriesSnapshot(
    val resourceId: String,
    val title: String,
    val posterUrl: String? = null,
    val backgroundUrl: String? = null,
    val releaseInfo: String? = null,
)

@Serializable
data class CustomVideoSnapshot(
    val resourceId: String,
    val title: String? = null,
    val seasonNumber: Int,
    val episodeNumber: Int,
    val thumbnailUrl: String? = null,
    val backgroundUrl: String? = null,
    val releaseInfo: String? = null,
    val released: String? = null,
)

@Serializable
data class CustomSeriesResolveResult(
    val series: CustomSeriesReference,
    val seasons: List<CustomSeasonReference>,
    val videos: List<CustomVideoReference>,
)

@Serializable
data class CustomSeriesReference(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val resourceId: String,
)

@Serializable
data class CustomSeasonReference(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val seasonNumber: Int,
)

@Serializable
data class CustomVideoReference(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val resourceId: String,
    @Serializable(with = UUIDSerializer::class) val seasonTitleId: UUID,
    val seasonNumber: Int,
    val episodeNumber: Int,
)

@Serializable
data class LibraryItem(
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
    val mediaType: TitleMediaType,
    val provider: String? = null,
    val externalId: String? = null,
    val resourceId: String? = null,
    val title: String? = null,
    val posterUrl: String? = null,
    val backgroundUrl: String? = null,
    val releaseInfo: String? = null,
    @Serializable(with = UUIDSerializer::class) val sourceAddonId: UUID? = null,
    val sourceCatalogId: String? = null,
    val sourceName: String? = null,
    val country: String? = null,
    val language: String? = null,
    val category: String? = null,
    val available: Boolean,
    val addedAt: String,
    val updatedAt: String,
)

@Serializable
data class LibraryPage(
    val items: List<LibraryItem>,
    val page: Int,
    val totalPages: Int,
    val totalResults: Int,
)

@Serializable
data class TVLibraryIdentity(
    @Serializable(with = UUIDSerializer::class) val sourceAddonId: UUID,
    val resourceId: String,
)

@Serializable
data class TVLibraryMembershipRequest(val identities: List<TVLibraryIdentity>)

@Serializable
data class TVLibraryMembership(
    @Serializable(with = UUIDSerializer::class) val sourceAddonId: UUID,
    val resourceId: String,
    @Serializable(with = UUIDSerializer::class) val titleId: UUID,
)

@Serializable
data class TVLibraryMembershipResult(val items: List<TVLibraryMembership>)

@Serializable
data class SessionNotificationList(val notifications: List<SessionNotification>)

@Serializable
data class SessionNotification(
    val id: String,
    val message: String,
    val senderUsername: String,
    val createdAt: String,
)
