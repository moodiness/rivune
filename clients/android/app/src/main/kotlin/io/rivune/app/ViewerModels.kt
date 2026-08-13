package io.rivune.app

import io.rivune.api.Episode
import io.rivune.api.LibraryItem
import io.rivune.api.Movie
import io.rivune.api.PlaybackProgress
import io.rivune.api.PlaybackSourceOption
import io.rivune.api.Series
import io.rivune.api.SettingsValues
import io.rivune.api.Season
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
)

data class MediaDetailState(
    val target: MediaTarget,
    val titleId: UUID,
    val movie: Movie? = null,
    val series: Series? = null,
    val season: Season? = null,
    val progress: PlaybackProgress? = null,
    val episodeProgress: Map<UUID, PlaybackProgress> = emptyMap(),
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
)

data class SourcePickerState(
    val target: MediaTarget,
    val titleId: UUID,
    val progress: PlaybackProgress?,
    val options: List<PlaybackSourceOption>,
    val partial: Boolean,
)

data class PlayerSubtitlePresentation(
    val id: String,
    val label: String,
    val language: String?,
    val url: String,
    val selected: Boolean = false,
)

data class PlayerPresentation(
    val key: String,
    val sessionId: UUID,
    val titleId: UUID,
    val title: String,
    val mediaUrl: String,
    val protocol: String,
    val container: String?,
    val startPositionMs: Long,
    val durationSeconds: Int,
    val expectedProgressVersion: Long,
    val subtitles: List<PlayerSubtitlePresentation> = emptyList(),
    val externalPlayer: ExternalPlayerApp? = null,
)
data class ProfilePreferencesState(
    val settings: SettingsValues? = null,
    val canEdit: Boolean = false,
)


data class ViewerState(
    val selectedTab: ViewerTab = ViewerTab.HOME,
    val continueWatching: List<MediaTarget> = emptyList(),
    val search: SearchState = SearchState(),
    val library: LibraryState = LibraryState(),
    val detail: MediaDetailState? = null,
    val sourcePicker: SourcePickerState? = null,
    val player: PlayerPresentation? = null,
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
        seasonId = seasonId,
        seasonNumber = seasonNumber,
        episodeNumber = episodeNumber,
    )
}
