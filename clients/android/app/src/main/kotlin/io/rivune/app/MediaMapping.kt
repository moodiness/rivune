package io.rivune.app

import io.rivune.api.AddonCatalogDescriptor
import io.rivune.api.AddonResourceBatch
import io.rivune.api.AddonResourceResult
import io.rivune.api.CollectionItem
import io.rivune.api.LibraryItem
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull

internal fun CollectionItem.toMediaTarget(): MediaTarget {
    val addonSource = sources.firstOrNull { it.addonId != null }
    return MediaTarget(
        id = id,
        resourceId = id,
        mediaType = mediaType,
        title = title,
        externalIds = externalIds,
        sourceAddonId = addonSource?.addonId,
        sourceCatalogId = addonSource?.catalogId,
        sourceName = addonSource?.title,
        posterUrl = posterUrl,
        backgroundUrl = backgroundUrl,
        logoUrl = logoUrl,
        description = description,
        releaseInfo = releaseInfo,
        released = released,
    )
}
internal fun CollectionItem.toSemanticMediaTarget(): MediaTarget {
    val provider = listOf("tmdb", "imdb", "tvdb", "trakt").firstOrNull { !externalIds[it].isNullOrBlank() }
    return toMediaTarget().copy(
        titleId = id.toUuidOrNull(),
        provider = provider,
        externalId = provider?.let(externalIds::get),
    )
}


internal fun titleReleaseDate(value: String?): String? {
    val normalized = value?.trim()?.takeIf(String::isNotEmpty) ?: return null
    val parsed = runCatching {
        if ('T' in normalized) {
            DateTimeFormatter.ISO_DATE_TIME.parse(normalized, LocalDate::from)
        } else {
            LocalDate.parse(normalized)
        }
    }.getOrNull() ?: return null
    return parsed.toString()
}

internal fun LibraryItem.toMediaTarget(untitled: String = "Untitled"): MediaTarget = MediaTarget(
    id = resourceId ?: externalId ?: titleId.toString(),
    resourceId = resourceId ?: externalId ?: titleId.toString(),
    mediaType = mediaType.wireValue,
    title = title?.takeIf(String::isNotBlank) ?: untitled,
    titleId = titleId,
    provider = provider,
    externalId = externalId,
    externalIds = provider?.let { providerValue -> externalId?.let { externalIdValue -> mapOf(providerValue to externalIdValue) } }.orEmpty(),
    sourceAddonId = sourceAddonId,
    sourceCatalogId = sourceCatalogId,
    sourceName = sourceName,
    posterUrl = posterUrl,
    backgroundUrl = backgroundUrl,
    releaseInfo = releaseInfo,
    country = country,
    language = language,
    category = category,
    available = available,
)

internal fun AddonResourceBatch.toMediaTargets(descriptors: List<AddonCatalogDescriptor>): List<MediaTarget> {
    val output = ArrayList<MediaTarget>()
    val seen = HashSet<String>()
    for (result in results) {
        val descriptor = descriptors.firstOrNull {
            it.addonId == result.addonId &&
                it.manifestId == result.manifestId &&
                it.catalog.type == result.type &&
                it.catalog.id == result.id
        }
        for (target in result.toMediaTargets(descriptor)) {
            val key = if (target.mediaType in GLOBAL_MEDIA_TYPES) {
                "${target.mediaType}:${target.id}"
            } else {
                "${target.mediaType}:${target.sourceAddonId}:${target.resourceId}"
            }
            if (seen.add(key)) output += target
        }
    }
    return output
}

internal fun AddonResourceBatch.hasFullPage(pageSize: Int): Boolean = results.any { result ->
    (result.payload["metas"] as? JsonArray)?.size == pageSize
}

private fun AddonResourceResult.toMediaTargets(descriptor: AddonCatalogDescriptor?): List<MediaTarget> {
    val metas = payload["metas"] as? JsonArray ?: return emptyList()
    return metas.mapNotNull { candidate ->
        val meta = candidate as? JsonObject ?: return@mapNotNull null
        val rawId = meta.string("id") ?: return@mapNotNull null
        val mediaType = meta.string("type") ?: type
        val resourceId = meta.string("resourceId") ?: rawId
        val addonScoped = mediaType == "tv" || mediaType !in GLOBAL_MEDIA_TYPES
        MediaTarget(
            id = resourceId,
            resourceId = resourceId,
            mediaType = mediaType,
            title = meta.string("name", "title") ?: "Untitled",
            sourceAddonId = if (addonScoped) meta.string("sourceAddonId")?.toUuidOrNull() ?: addonId else addonId,
            sourceCatalogId = if (addonScoped) meta.string("sourceCatalogId", "catalogId") ?: id else id,
            sourceName = meta.string("sourceName", "source") ?: descriptor?.addonName,
            posterUrl = meta.string("poster", "posterUrl") ?: if (mediaType == "tv") meta.string("background", "backgroundUrl", "backdrop", "logo", "logoUrl") else null,
            backgroundUrl = meta.string("background", "backgroundUrl", "backdrop") ?: if (mediaType == "tv") meta.string("poster", "posterUrl", "logo", "logoUrl") else null,
            logoUrl = meta.string("logo", "logoUrl"),
            description = meta.string("description", "overview"),
            releaseInfo = meta.string("releaseInfo"),
            released = meta.string("released"),
            country = if (mediaType == "tv") meta.string("country", "countryCode") else null,
            language = if (mediaType == "tv") meta.string("language", "lang") else null,
            category = if (mediaType == "tv") meta.string("category", "genre") else null,
            available = meta.boolean("available") != false,
        )
    }
}

private fun JsonObject.string(vararg keys: String): String? {
    for (key in keys) {
        val value = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.content?.trim()
        if (!value.isNullOrEmpty()) return value
    }
    return null
}

private fun JsonObject.boolean(key: String): Boolean? = (this[key] as? JsonPrimitive)?.booleanOrNull

private fun String.toUuidOrNull() = runCatching { java.util.UUID.fromString(this) }.getOrNull()

private val GLOBAL_MEDIA_TYPES = setOf("movie", "series", "episode")
