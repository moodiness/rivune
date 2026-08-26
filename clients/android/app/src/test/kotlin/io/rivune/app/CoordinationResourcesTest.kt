package io.rivune.app

import java.io.File
import javax.xml.parsers.DocumentBuilderFactory
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

class CoordinationResourcesTest {
    private val resourceRoot = sequenceOf(
        File("src/main/res"),
        File("clients/android/app/src/main/res"),
    ).first(File::isDirectory)

    private val coordinationStrings = setOf(
        "viewer_coordination_devices_title",
        "viewer_coordination_play",
        "viewer_coordination_pause",
        "viewer_coordination_match_position",
        "viewer_coordination_stop",
        "viewer_coordination_room_start",
        "viewer_coordination_room_join",
        "viewer_coordination_room_label",
        "viewer_coordination_room_default",
        "viewer_coordination_room_leave",
        "viewer_coordination_join_title",
        "viewer_coordination_room_code",
        "viewer_coordination_join_confirm",
        "viewer_coordination_cancel",
    )

    @Test
    fun everySupportedLocaleDefinesAllCoordinationResources() {
        val localeDirectories = listOf("values", "values-fr", "values-de", "values-es", "values-it", "values-pt-rBR")

        localeDirectories.forEach { directory ->
            val resources = parseResources(File(resourceRoot, "$directory/strings.xml"))
            assertEquals(
                coordinationStrings,
                resources.strings.keys.intersect(coordinationStrings),
                "$directory must define every coordination string",
            )
            assertTrue(
                resources.plurals["viewer_coordination_watching_count"].orEmpty().containsAll(setOf("one", "other")),
                "$directory must define singular and plural watching counts",
            )
        }
    }

    @Test
    fun nonEnglishLocalesDoNotFallBackToEnglishCoordinationActions() {
        val english = parseResources(File(resourceRoot, "values/strings.xml"))
        val actionKeys = setOf(
            "viewer_coordination_devices_title",
            "viewer_coordination_play",
            "viewer_coordination_match_position",
            "viewer_coordination_stop",
            "viewer_coordination_room_start",
            "viewer_coordination_room_join",
            "viewer_coordination_room_leave",
            "viewer_coordination_join_title",
            "viewer_coordination_room_code",
            "viewer_coordination_join_confirm",
        )

        listOf("values-fr", "values-de", "values-es", "values-it", "values-pt-rBR").forEach { directory ->
            val localized = parseResources(File(resourceRoot, "$directory/strings.xml"))
            actionKeys.forEach { key ->
                assertNotEquals(english.strings[key], localized.strings[key], "$directory must localize $key")
            }
        }
    }
}

private data class ParsedResources(
    val strings: Map<String, String>,
    val plurals: Map<String, Set<String>>,
)

private fun parseResources(file: File): ParsedResources {
    val document = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file)
    val strings = document.getElementsByTagName("string").let { nodes ->
        buildMap {
            repeat(nodes.length) { index ->
                val node = nodes.item(index)
                put(node.attributes.getNamedItem("name").nodeValue, node.textContent.trim())
            }
        }
    }
    val plurals = document.getElementsByTagName("plurals").let { nodes ->
        buildMap {
            repeat(nodes.length) { index ->
                val node = nodes.item(index)
                val quantities = buildSet {
                    val children = node.childNodes
                    repeat(children.length) { childIndex ->
                        val child = children.item(childIndex)
                        if (child.nodeName == "item") add(child.attributes.getNamedItem("quantity").nodeValue)
                    }
                }
                put(node.attributes.getNamedItem("name").nodeValue, quantities)
            }
        }
    }
    return ParsedResources(strings, plurals)
}
