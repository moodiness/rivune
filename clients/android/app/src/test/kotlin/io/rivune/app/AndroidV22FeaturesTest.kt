package io.rivune.app

import io.rivune.api.MediaNotification
import io.rivune.api.MediaNotificationKind
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

class AndroidV22FeaturesTest {
    @Test
    fun sameTitleNotificationsHaveDistinctKindDateOrPositionIdentity() {
        val titleId = UUID.randomUUID()
        val movie = notification(
            titleId = titleId,
            kind = MediaNotificationKind.MOVIE_RELEASE,
            releaseDate = "2026-08-26",
        )
        val episode = notification(
            titleId = titleId,
            kind = MediaNotificationKind.EPISODE_AVAILABLE,
            seasonNumber = 2,
            episodeNumber = 7,
        )

        val movieIdentity = mediaNotificationIdentity(movie)
        val episodeIdentity = mediaNotificationIdentity(episode)
        assertNotEquals(movieIdentity, episodeIdentity)
        assertTrue("2026-08-26" in movieIdentity)
        assertTrue("season 2" in episodeIdentity)
        assertTrue("episode 7" in episodeIdentity)
    }

    private fun notification(
        titleId: UUID,
        kind: MediaNotificationKind,
        releaseDate: String? = null,
        seasonNumber: Int? = null,
        episodeNumber: Int? = null,
    ) = MediaNotification(
        id = kind.name,
        kind = kind,
        titleId = titleId,
        title = "Same title",
        releaseDate = releaseDate,
        seasonNumber = seasonNumber,
        episodeNumber = episodeNumber,
        availableAt = "2026-08-26T12:00:00Z",
        createdAt = "2026-08-25T12:00:00Z",
    )
}
