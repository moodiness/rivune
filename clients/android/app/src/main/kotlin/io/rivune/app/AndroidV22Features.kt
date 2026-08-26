package io.rivune.app

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.rivune.api.CaptionsPreference
import io.rivune.api.FocusIndicatorsPreference
import io.rivune.api.HighContrastPreference
import io.rivune.api.ReducedMotionPreference
import io.rivune.app.ui.components.RivuneFocusSurface
import io.rivune.app.ui.components.RivunePrimaryButton
import io.rivune.app.ui.components.RivuneSecondaryButton
import io.rivune.app.ui.components.RivuneTextButton
import io.rivune.app.ui.theme.RivuneSpacing

private enum class V22Panel { QUEUE, SEARCHES, SMART, INBOX, INCIDENTS, ACCESSIBILITY }

@Composable
internal fun V22FeatureLauncher(
    state: RivuneUiState,
    viewModel: RivuneViewModel,
    modifier: Modifier = Modifier,
) {
    var panel by remember(state.activeProfile?.id) { mutableStateOf<V22Panel?>(null) }
    val features = state.viewer.features
    FlowRow(
        modifier = modifier.widthIn(max = 620.dp),
        horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
    ) {
        FeatureButton(stringResource(R.string.v22_queue), state.isTv) { panel = V22Panel.QUEUE }
        if (state.viewer.selectedTab == ViewerTab.SEARCH) {
            FeatureButton(stringResource(R.string.v22_saved_searches), state.isTv) { panel = V22Panel.SEARCHES }
        }
        if (state.viewer.selectedTab == ViewerTab.LIBRARY) {
            FeatureButton(stringResource(R.string.v22_smart_collections), state.isTv) { panel = V22Panel.SMART }
        }
        FeatureButton(stringResource(R.string.v22_inbox, features.notifications.count { it.readAt == null }), state.isTv) {
            panel = V22Panel.INBOX
        }
        if (state.activeProfile?.canManage == true) {
            FeatureButton(stringResource(R.string.v22_operations, features.incidents.count { it.acknowledgedAt == null }), state.isTv) {
                panel = V22Panel.INCIDENTS
            }
        }
        FeatureButton(stringResource(R.string.v22_accessibility), state.isTv) { panel = V22Panel.ACCESSIBILITY }
    }
    when (panel) {
        V22Panel.QUEUE -> FeatureDialog(R.string.v22_queue, { panel = null }) {
            FeatureLoadStatus(features.queueLoad, V22FeatureKind.QUEUE, state.isTv, viewModel::retryV22Feature)
            FeatureMutationStatus(features)
            if (features.queueLoad.loaded && features.queue?.items.isNullOrEmpty()) Text(stringResource(R.string.v22_queue_empty))
            features.queue?.items?.forEach { item ->
                val position = stringResource(R.string.v22_queue_position, item.position + 1)
                FeatureRow(item.title, position, state.isTv) {
                    val description = stringResource(R.string.v22_remove_queue_description, item.title, item.position + 1)
                    RivuneTextButton(
                        stringResource(R.string.v22_remove),
                        { viewModel.removeQueueItem(item) },
                        isTv = state.isTv,
                        modifier = Modifier.semantics { contentDescription = description },
                    )
                }
            }
        }
        V22Panel.SEARCHES -> FeatureDialog(R.string.v22_saved_searches, { panel = null }) {
            FeatureLoadStatus(features.savedSearchLoad, V22FeatureKind.SAVED_SEARCHES, state.isTv, viewModel::retryV22Feature)
            FeatureMutationStatus(features)
            if (features.savedSearchLoad.loaded && features.savedSearches.isEmpty()) Text(stringResource(R.string.v22_saved_searches_empty))
            RivunePrimaryButton(
                label = stringResource(R.string.v22_save_current_search),
                onClick = viewModel::saveCurrentSearch,
                enabled = state.viewer.search.query.trim().length >= 2 && !features.mutationInFlight,
                isTv = state.isTv,
            )
            features.savedSearches.forEach { saved ->
                FeatureRow(saved.name, saved.query, state.isTv) {
                    val runDescription = stringResource(R.string.v22_run_search_description, saved.name)
                    val removeDescription = stringResource(R.string.v22_remove_search_description, saved.name)
                    RivuneSecondaryButton(stringResource(R.string.v22_run), { viewModel.runSavedSearch(saved); panel = null }, isTv = state.isTv, modifier = Modifier.semantics { contentDescription = runDescription })
                    RivuneTextButton(stringResource(R.string.v22_remove), { viewModel.deleteSavedSearch(saved) }, isTv = state.isTv, modifier = Modifier.semantics { contentDescription = removeDescription })
                }
            }
        }
        V22Panel.SMART -> FeatureDialog(R.string.v22_smart_collections, { panel = null }) {
            FeatureLoadStatus(features.smartCollectionLoad, V22FeatureKind.SMART_COLLECTIONS, state.isTv, viewModel::retryV22Feature)
            FeatureMutationStatus(features)
            if (features.smartCollectionLoad.loaded && features.smartCollections.isEmpty()) Text(stringResource(R.string.v22_smart_collections_empty))
            RivunePrimaryButton(
                label = stringResource(R.string.v22_create_smart_collection),
                onClick = viewModel::createSmartCollectionForLibrary,
                enabled = state.viewer.library.mediaType != null && !features.mutationInFlight,
                isTv = state.isTv,
            )
            features.smartCollections.forEach { smart ->
                FeatureRow(smart.name, stringResource(R.string.v22_revision, smart.revision), state.isTv) {
                    val openDescription = stringResource(R.string.v22_open_smart_description, smart.name)
                    val removeDescription = stringResource(R.string.v22_remove_smart_description, smart.name)
                    RivuneSecondaryButton(stringResource(R.string.v22_open), { viewModel.openSmartCollection(smart) }, isTv = state.isTv, modifier = Modifier.semantics { contentDescription = openDescription })
                    RivuneTextButton(stringResource(R.string.v22_remove), { viewModel.deleteSmartCollection(smart) }, isTv = state.isTv, modifier = Modifier.semantics { contentDescription = removeDescription })
                }
            }
            features.smartCollectionPage?.let { page ->
                Text(stringResource(R.string.v22_smart_results, page.total), style = MaterialTheme.typography.titleMedium)
                page.items.take(20).forEach { Text(it.title, style = MaterialTheme.typography.bodyMedium) }
            }
        }
        V22Panel.INBOX -> FeatureDialog(R.string.v22_inbox_title, { panel = null }) {
            FeatureLoadStatus(features.inboxLoad, V22FeatureKind.INBOX, state.isTv, viewModel::retryV22Feature)
            FeatureMutationStatus(features)
            if (features.inboxLoad.loaded && features.notifications.isEmpty()) Text(stringResource(R.string.v22_inbox_empty))
            features.notifications.forEach { notification ->
                val identity = mediaNotificationIdentity(notification)
                FeatureRow(notification.title, notification.kind.name.lowercase().replace('_', ' '), state.isTv) {
                    val readDescription = stringResource(R.string.v22_read_notification_description, notification.title, identity)
                    val dismissDescription = stringResource(R.string.v22_dismiss_notification_description, notification.title, identity)
                    if (notification.readAt == null) RivuneSecondaryButton(
                        stringResource(R.string.v22_mark_read),
                        { viewModel.acknowledgeMediaNotification(notification, false) },
                        isTv = state.isTv,
                        modifier = Modifier.semantics { contentDescription = readDescription },
                    )
                    RivuneTextButton(
                        stringResource(R.string.v22_dismiss),
                        { viewModel.acknowledgeMediaNotification(notification, true) },
                        isTv = state.isTv,
                        modifier = Modifier.semantics { contentDescription = dismissDescription },
                    )
                }
            }
        }
        V22Panel.INCIDENTS -> FeatureDialog(R.string.v22_operations_title, { panel = null }) {
            FeatureLoadStatus(features.incidentLoad, V22FeatureKind.INCIDENTS, state.isTv, viewModel::retryV22Feature)
            FeatureMutationStatus(features)
            if (features.incidentLoad.loaded && features.incidents.isEmpty()) Text(stringResource(R.string.v22_incidents_empty))
            features.incidents.forEach { incident ->
                // Only the protocol's safe labels and timestamps are rendered. No endpoint, token, or raw error exists in this model.
                FeatureRow(
                    incident.addonName,
                    stringResource(R.string.v22_incident_summary, incident.code.name.lowercase(), incident.state.name.lowercase(), incident.occurrenceCount),
                    state.isTv,
                ) {
                    if (incident.acknowledgedAt == null) {
                        val description = stringResource(R.string.v22_ack_incident_description, incident.addonName, incident.code.name.lowercase())
                        RivuneSecondaryButton(
                            stringResource(R.string.v22_acknowledge),
                            { viewModel.acknowledgeIncident(incident) },
                            isTv = state.isTv,
                            modifier = Modifier.semantics { contentDescription = description },
                        )
                    }
                }
            }
        }
        V22Panel.ACCESSIBILITY -> FeatureDialog(R.string.v22_accessibility, { panel = null }) {
            FeatureLoadStatus(features.accessibilityLoad, V22FeatureKind.ACCESSIBILITY, state.isTv, viewModel::retryV22Feature)
            val preferences = features.accessibility
            FeatureMutationStatus(features)
            if (features.accessibilityLoad.loaded && preferences == null) Text(stringResource(R.string.v22_accessibility_unavailable)) else if (preferences != null) {
                ChoiceRow(stringResource(R.string.v22_reduced_motion), preferences.reducedMotion.name, state.isTv) {
                    viewModel.updateAccessibility { current ->
                        current.copy(reducedMotion = when (current.reducedMotion) {
                            ReducedMotionPreference.SYSTEM -> ReducedMotionPreference.REDUCE
                            ReducedMotionPreference.REDUCE -> ReducedMotionPreference.NO_PREFERENCE
                            ReducedMotionPreference.NO_PREFERENCE -> ReducedMotionPreference.SYSTEM
                        })
                    }
                }
                ChoiceRow(stringResource(R.string.v22_high_contrast), preferences.highContrast.name, state.isTv) {
                    viewModel.updateAccessibility { current ->
                        current.copy(highContrast = when (current.highContrast) {
                            HighContrastPreference.SYSTEM -> HighContrastPreference.MORE
                            HighContrastPreference.MORE -> HighContrastPreference.STANDARD
                            HighContrastPreference.STANDARD -> HighContrastPreference.SYSTEM
                        })
                    }
                }
                ChoiceRow(stringResource(R.string.v22_text_scale), "${preferences.textScale}%", state.isTv) {
                    viewModel.updateAccessibility { current -> current.copy(textScale = when (current.textScale) { 100 -> 115; 115 -> 130; else -> 100 }) }
                }
                ChoiceRow(stringResource(R.string.v22_captions), preferences.captions.name, state.isTv) {
                    viewModel.updateAccessibility { current -> current.copy(captions = when (current.captions) {
                        CaptionsPreference.SYSTEM -> CaptionsPreference.ON
                        CaptionsPreference.ON -> CaptionsPreference.OFF
                        CaptionsPreference.OFF -> CaptionsPreference.SYSTEM
                    }) }
                }
                ChoiceRow(stringResource(R.string.v22_audio_description), if (preferences.audioDescription) stringResource(R.string.v22_on) else stringResource(R.string.v22_off), state.isTv) {
                    viewModel.updateAccessibility { it.copy(audioDescription = !it.audioDescription) }
                }
                ChoiceRow(stringResource(R.string.v22_focus_indicators), preferences.focusIndicators.name, state.isTv) {
                    viewModel.updateAccessibility { it.copy(focusIndicators = if (it.focusIndicators == FocusIndicatorsPreference.STANDARD) FocusIndicatorsPreference.ENHANCED else FocusIndicatorsPreference.STANDARD) }
                }
            }
        }
        null -> Unit
    }
}

@Composable
private fun FeatureButton(label: String, isTv: Boolean, onClick: () -> Unit) {
    RivuneFocusSurface(onClick = onClick, isTv = isTv) {
        Text(label, modifier = Modifier.padding(horizontal = RivuneSpacing.sm, vertical = RivuneSpacing.xs), style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
private fun FeatureDialog(title: Int, onDismiss: () -> Unit, content: @Composable () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(title)) },
        text = {
            LazyColumn(
                modifier = Modifier.fillMaxWidth().heightIn(max = 560.dp),
                verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md),
            ) { item { Column(verticalArrangement = Arrangement.spacedBy(RivuneSpacing.md)) { content() } } }
        },
        confirmButton = { RivuneTextButton(stringResource(R.string.v22_close), onDismiss) },
    )
}

@Composable
private fun FeatureRow(title: String, body: String, isTv: Boolean, actions: @Composable RowScope.() -> Unit) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs)) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Row(horizontalArrangement = Arrangement.spacedBy(RivuneSpacing.xs), content = actions)
    }
}

@Composable
private fun ChoiceRow(label: String, value: String, isTv: Boolean, onClick: () -> Unit) {
    RivuneFocusSurface(onClick = onClick, isTv = isTv, modifier = Modifier.fillMaxWidth()) {
        Row(modifier = Modifier.fillMaxWidth().padding(RivuneSpacing.sm), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(label)
            Text(value.lowercase().replace('_', '-'), color = MaterialTheme.colorScheme.primary)
        }
    }
}
@Composable
private fun FeatureMutationStatus(features: V22FeatureState) {
    when {
        features.mutationInFlight -> Text(
            stringResource(R.string.viewer_saving_change),
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        )
        features.conflict -> Text(
            stringResource(R.string.v22_conflict),
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Assertive },
        )
        features.failure != null -> Text(
            stringResource(R.string.v22_error),
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Assertive },
        )
    }
}


@Composable
private fun FeatureLoadStatus(
    state: V22LoadState,
    kind: V22FeatureKind,
    isTv: Boolean,
    retry: (V22FeatureKind) -> Unit,
) {
    when {
        state.loading -> Text(stringResource(R.string.v22_loading), modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite })
        state.failure != null -> Column(
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Assertive },
            verticalArrangement = Arrangement.spacedBy(RivuneSpacing.xs),
        ) {
            Text(stringResource(R.string.v22_error), color = MaterialTheme.colorScheme.error)
            RivuneSecondaryButton(stringResource(R.string.viewer_retry), { retry(kind) }, isTv = isTv)
        }
    }
}

internal fun mediaNotificationIdentity(notification: io.rivune.api.MediaNotification): String = buildString {
    append(notification.kind.name.lowercase().replace('_', ' '))
    notification.releaseDate?.let { append(", ").append(it) }
    notification.seasonNumber?.let { append(", season ").append(it) }
    notification.episodeNumber?.let { append(", episode ").append(it) }
}
