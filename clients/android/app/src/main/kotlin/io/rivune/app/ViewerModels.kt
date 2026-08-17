package io.rivune.app

import io.rivune.api.CastMember
import io.rivune.api.Episode
import io.rivune.api.LibraryItem
import io.rivune.api.Movie
import io.rivune.api.PlaybackProgress
import io.rivune.api.PlaybackSourceOption
import io.rivune.api.Series
import io.rivune.api.EffectiveSettings
import io.rivune.api.EffectiveSettingsSources
import io.rivune.api.SettingsValues
import io.rivune.api.Season
import io.rivune.api.Trailer
import java.util.UUID

enum class ViewerTab {
    HOME,
    SEARCH,
    LIBRARY,
    CALENDAR,
}

enum class ViewerLoading {
    HOME,
    FOLDER,
    SEARCH,
    SEARCH_MORE,
    LIBRARY,
    LIBRARY_MORE,
    CALENDAR,
    DETAIL,
    SEASON,
    SOURCES,
    PLAYER,
    ACTION,
    PREFERENCES,
}

data class MediaTarget(
    val id: String,
    val mediaType: String,
    val title: String,
    val titleId: UUID? = null,
    val resourceId: String = id,
    val provider: String? = null,
    val externalId: String? = null,
    val externalIds: Map<String, String> = emptyMap(),
    val sourceAddonId: UUID? = null,
    val sourceCatalogId: String? = null,
    val sourceName: String? = null,
    val posterUrl: String? = null,
    val backgroundUrl: String? = null,
    val logoUrl: String? = null,
    val resumePositionSeconds: Int = 0,
    val durationSeconds: Int = 0,
    val description: String? = null,
    val releaseInfo: String? = null,
    val released: String? = null,
    val country: String? = null,
    val language: String? = null,
    val category: String? = null,
    val available: Boolean = true,
    val seriesId: UUID? = null,
    val seasonId: String? = null,
    val seasonNumber: Int? = null,
    val episodeNumber: Int? = null,
    val seriesImdbId: String? = null,
    val runtimeMinutes: Int? = null,
    val rating: Double? = null,
)

data class MediaDetailState(
    val target: MediaTarget,
    val titleId: UUID,
    val movie: Movie? = null,
    val series: Series? = null,
    val season: Season? = null,
    val cast: List<CastMember> = emptyList(),
    val progress: PlaybackProgress? = null,
    val episodeProgress: Map<UUID, PlaybackProgress> = emptyMap(),
    val trailers: List<Trailer> = emptyList(),
    val seasonTrailers: List<Trailer> = emptyList(),
    val inLibrary: Boolean = false,
)

data class SearchState(
    val query: String = "",
    val items: List<MediaTarget> = emptyList(),
    val page: Int = 0,
    val hasMore: Boolean = false,
    val partial: Boolean = false,
)

data class LibraryState(
    val items: List<LibraryItem> = emptyList(),
    val page: Int = 0,
    val totalPages: Int = 0,
    val totalResults: Int = 0,
    val mediaType: String? = null,
    val availableTypes: Set<String> = emptySet(),
)

data class PlaybackMarkerRequest(
    val imdbId: String,
    val season: Int,
    val episode: Int,
)

data class SourcePickerState(
    val target: MediaTarget,
    val titleId: UUID,
    val progress: PlaybackProgress?,
    val options: List<PlaybackSourceOption>,
    val partial: Boolean,
    val playerSource: PlaybackSourceOption? = null,
    val nextEpisode: MediaTarget? = null,
    val markerRequest: PlaybackMarkerRequest? = null,
)

data class PlayerSubtitlePresentation(
    val id: String,
    val label: String,
    val language: String?,
    val url: String,
    val selected: Boolean = false,
)

enum class EmbeddedPlayerEngine {
    MEDIA3,
    MPV,
}

enum class EmbeddedPlayerPreference {
    AUTOMATIC,
    MEDIA3,
    MPV,
}

enum class PlayerEngineFailureReason {
    PLAYBACK_ERROR,
    STARTUP_TIMEOUT,
}

data class PlayerEngineFailure(
    val positionMs: Long,
    val fallbackEligible: Boolean,
    val reason: PlayerEngineFailureReason = PlayerEngineFailureReason.PLAYBACK_ERROR,
)

data class PlayerFailureState(
    val playerKey: String,
    val sessionId: UUID,
    val failure: PlayerEngineFailure,
)

internal fun PlayerFailureState.matches(player: PlayerPresentation): Boolean =
    playerKey == player.key && sessionId == player.sessionId

data class PlayerPresentation(
    val key: String,
    val sessionId: UUID,
    val titleId: UUID,
    val title: String,
    val mediaUrl: String,
    val protocol: String,
    val container: String?,
    val mediaTimeline: io.rivune.api.PlaybackMediaTimeline?,
    val startPositionMs: Long,
    val timelineStartPositionMs: Long,
    val durationSeconds: Int,
    val expectedProgressVersion: Long,
    val engine: EmbeddedPlayerEngine,
    val fallbackAllowed: Boolean,
    val subtitles: List<PlayerSubtitlePresentation> = emptyList(),
    val externalPlayer: ExternalPlayerApp? = null,
    val nextEpisode: MediaTarget? = null,
    val markers: List<io.rivune.api.PlaybackMarker> = emptyList(),
)

internal fun shouldAutomaticallyFallbackToMpv(
    presentation: PlayerPresentation,
    failure: PlayerEngineFailure,
): Boolean = presentation.externalPlayer == null &&
    presentation.engine == EmbeddedPlayerEngine.MEDIA3 &&
    presentation.fallbackAllowed &&
    failure.fallbackEligible

internal fun PlayerPresentation.fallbackToMpv(
    failure: PlayerEngineFailure,
    newKey: String,
): PlayerPresentation? = takeIf { shouldAutomaticallyFallbackToMpv(it, failure) }?.copy(
    key = newKey,
    engine = EmbeddedPlayerEngine.MPV,
    fallbackAllowed = false,
    startPositionMs = failure.positionMs.coerceAtLeast(0L),
    externalPlayer = null,
)

internal fun PlayerPresentation.recoveryEmbeddedPreference(): EmbeddedPlayerPreference = when {
    engine == EmbeddedPlayerEngine.MPV -> EmbeddedPlayerPreference.MPV
    fallbackAllowed -> EmbeddedPlayerPreference.AUTOMATIC
    else -> EmbeddedPlayerPreference.MEDIA3
}

internal fun io.rivune.api.PlaybackSourceOption.matchesRecoverySource(
    previous: io.rivune.api.PlaybackSourceOption,
): Boolean = stableIdentity.isNotBlank() &&
    stableIdentity == previous.stableIdentity &&
    addonId == previous.addonId &&
    manifestId == previous.manifestId
data class ProfilePreferencesState(
    val effective: EffectiveSettings? = null,
    val canEdit: Boolean = false,
) {
    val settings: SettingsValues?
        get() = effective?.settings
    val sources: EffectiveSettingsSources?
        get() = effective?.sources
}


data class HomeHeroSlide(
    val item: io.rivune.api.CollectionItem,
    val fallbackBackdropUrl: String?,
    val fallbackLogoUrl: String?,
)

data class ViewerState(
    val selectedTab: ViewerTab = ViewerTab.HOME,
    val continueWatching: List<MediaTarget> = emptyList(),
    val heroSlides: List<HomeHeroSlide> = emptyList(),
    val search: SearchState = SearchState(),
    val library: LibraryState = LibraryState(),
    val detail: MediaDetailState? = null,
    val detailHistory: List<MediaDetailState> = emptyList(),
    val sourcePicker: SourcePickerState? = null,
    val sourcePickerVisible: Boolean = false,
    val player: PlayerPresentation? = null,
    val playerFailure: PlayerFailureState? = null,
    val preferences: ProfilePreferencesState? = null,
    val loading: ViewerLoading? = null,
    val inlineFailure: UiFailure? = null,
)

internal fun Episode.toMediaTarget(series: Series, fallback: MediaTarget): MediaTarget {
    val resourceId = when {
        !series.externalIds["imdb"].isNullOrBlank() -> "${series.externalIds.getValue("imdb")}:$seasonNumber:$episodeNumber"
        !externalIds["imdb"].isNullOrBlank() -> externalIds.getValue("imdb")
        !externalIds["tvdb"].isNullOrBlank() -> "tvdb:${externalIds.getValue("tvdb")}"
        !series.externalIds["tmdb"].isNullOrBlank() -> "tmdb:${series.externalIds.getValue("tmdb")}:$seasonNumber:$episodeNumber"
        else -> "${fallback.resourceId}:$seasonNumber:$episodeNumber"
    }
    return MediaTarget(
        id = resourceId,
        resourceId = resourceId,
        mediaType = "episode",
        title = name.ifBlank { "Episode $episodeNumber" },
        titleId = id,
        externalIds = externalIds,
        posterUrl = stillUrl ?: fallback.posterUrl,
        backgroundUrl = backdropUrl ?: stillUrl ?: fallback.backgroundUrl,
        description = overview,
        releaseInfo = airDate,
        released = airDate,
        seriesId = series.id,
        seriesImdbId = series.externalIds["imdb"],
        seasonId = seasonId,
        seasonNumber = seasonNumber,
        episodeNumber = episodeNumber,
        runtimeMinutes = runtimeMinutes,
        rating = voteAverage.takeIf { it > 0.0 },
    )
}

internal suspend fun resolveNextEpisodeTarget(
    series: Series,
    currentSeason: Season,
    currentEpisodeId: UUID,
    fallback: MediaTarget,
    loadSeason: suspend (String) -> Season,
): MediaTarget? {
    val currentIndex = currentSeason.episodes.indexOfFirst { it.id == currentEpisodeId }
    if (currentIndex < 0) return null
    currentSeason.episodes.getOrNull(currentIndex + 1)?.let { return it.toMediaTarget(series, fallback) }

    val orderedSeasons = series.seasons.sortedWith(compareBy({ it.seasonNumber }, { it.id }))
    val seasonIndex = orderedSeasons.indexOfFirst { it.id == currentSeason.id }
    if (seasonIndex < 0) return null
    for (summary in orderedSeasons.drop(seasonIndex + 1).filter { it.episodeCount > 0 }) {
        loadSeason(summary.id).episodes.firstOrNull()?.let { return it.toMediaTarget(series, fallback) }
    }
    return null
}
