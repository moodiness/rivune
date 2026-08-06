package io.rivune.api

import java.util.UUID
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder

object RivuneProtocol {
    const val VERSION: Int = 19
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
    val timezone: String,
    val interfaceLanguage: String,
)

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
enum class SeriesMappingProvider {
    @SerialName("tmdb") TMDB,
    @SerialName("tvdb") TVDB;

    val wireValue: String get() = name.lowercase()
}

@Serializable
data class Genre(val id: Int, val name: String)

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
    val voteAverage: Double,
    val voteCount: Int,
    val seasons: List<SeasonSummary>,
    val aliases: List<SeriesAlias>,
    val episodeOrders: List<EpisodeOrder>,
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
)

@Serializable
data class PlaybackCapabilities(
    val streamingProtocols: List<String>,
    val containers: List<String>,
    val videoCodecs: List<String>? = null,
    val audioCodecs: List<String>? = null,
    val hdrFormats: List<String>? = null,
    val externalPlayers: List<String>? = null,
    val processingModes: List<String>? = null,
    val maximumHeight: Int? = null,
    val maximumVideoBitrateKbps: Int? = null,
    val maximumAudioChannels: Int? = null,
    val subtitleModes: List<String>? = null,
    val mediaProfiles: List<PlaybackMediaProfile>? = null,
)

@Serializable
data class PlaybackSourceList(
    val sources: List<PlaybackSourceOption>,
    val providerErrors: List<PlaybackProviderError>,
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
    val container: String? = null,
    val expiresAt: String,
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
    val sources: List<PlaybackSource>,
    val subtitles: List<PlaybackSubtitle>,
    val providerErrors: List<PlaybackProviderError>,
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
    val compatible: Boolean,
    val media: PlaybackMediaInspection? = null,
    val decision: PlaybackDecision? = null,
)

@Serializable
data class PlaybackMediaInspection(
    val container: String? = null,
    val durationSeconds: Double? = null,
    val hdrFormat: String? = null,
    val videoTracks: List<PlaybackMediaTrack>,
    val audioTracks: List<PlaybackMediaTrack>,
    val subtitleTracks: List<PlaybackMediaTrack>,
)

@Serializable
data class PlaybackDecision(
    val reason: String,
    val videoAction: String,
    val audioAction: String,
    val subtitleAction: String,
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
    val videoBitrateKbps: Int? = null,
)

@Serializable
data class PlaybackMediaTrack(
    val index: Int,
    val type: String,
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
    val delivery: String? = null,
    val url: String? = null,
)

@Serializable
data class PlaybackProviderError(
    @Serializable(with = UUIDSerializer::class) val addonId: UUID,
    val manifestId: String,
    val code: String,
    val message: String,
)

@Serializable
data class PlaybackActivity(
    val summary: PlaybackActivitySummary,
    val diagnostics: PlaybackMediaDiagnostics,
    val sessions: List<PlaybackActivitySession>,
    val jobs: List<PlaybackMediaJob>,
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
data class PlaybackMediaDiagnostics(
    val videoEncoder: String,
    val hardwareToneMap: Boolean,
)

@Serializable
data class PlaybackActivitySession(
    @Serializable(with = UUIDSerializer::class) val id: UUID,
    val titleId: String? = null,
    val artworkUrl: String? = null,
    val externalIds: Map<String, String>? = null,
    val externalIdMediaTypes: Map<String, String>? = null,
    val title: String,
    val mediaType: String,
    val mode: String,
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
data class PlaybackMediaJob(
    @Serializable(with = UUIDSerializer::class) val sessionId: UUID? = null,
    val assetId: String,
    val mode: String,
    val state: String,
    val prewarming: Boolean,
    val progressPercent: Double? = null,
    val speed: Double? = null,
    val createdAt: String,
    val lastSeenAt: String,
)
