using System.Runtime.CompilerServices;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Rivune.App.ViewModels;
using Rivune.Windows;

namespace Rivune.App;

public sealed partial class MainPage
{
    private readonly ProtocolV22WorkspaceViewModel _protocolV22 = new();
    private readonly ReadingQueueController _readingQueueController = new();
    private EffectiveAccessibilitySettings? _profileAccessibilityEffective;
    private readonly ConditionalWeakTable<TextBlock, OriginalFontSize> _unscaledTextSizes = new();
    private bool _accessibilityVisualRefreshQueued;
    private readonly ConditionalWeakTable<ComboBox, object> _accessibleComboBoxes = new();
    private sealed record OriginalFontSize(double Value);

    private async Task RenderAccessibilitySettingsAsync()
    {
        if (_state.Client is not { } client || _state.Profile is not { Id: var profileId })
        {
            SettingsPanelHost.Children.Add(CreateSettingsError("Select a profile to load accessibility preferences."));
            return;
        }
        var generation = _state.GenerationId;
        try
        {
            var preferences = await client.GetProfileAccessibilityPreferencesAsync(profileId, _state.Token);
            if (!_state.IsCurrent(generation) || _activeSettingsCategory != "Accessibility") return;
            _protocolV22.ApplyAccessibility(preferences);
            ApplyProfileAccessibility(preferences);
            BuildAccessibilityControls(preferences);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (_state.IsCurrent(generation) && _activeSettingsCategory == "Accessibility")
                SettingsPanelHost.Children.Add(CreateSettingsError(FriendlyError(exception)));
        }
    }

    private void BuildAccessibilityControls(AccessibilityPreferencesDocument preferences)
    {
        SettingsPanelHost.Children.Clear();
        AddAccessibilityChoice("Reduced motion", "System follows the Windows animation setting.", preferences.ReducedMotion,
            Enum.GetValues<ReducedMotionPreference>(), value => preferences with { ReducedMotion = value });
        AddAccessibilityChoice("High contrast", "System follows Windows high contrast without changing the operating-system setting.", preferences.HighContrast,
            Enum.GetValues<HighContrastPreference>(), value => preferences with { HighContrast = value });
        AddAccessibilityChoice("Text scale", "Scale Rivune text for this profile.", preferences.TextScale,
            new[] { 100, 115, 130 }, value => preferences with { TextScale = value });
        AddAccessibilityChoice("Captions", "System preserves the current Windows and player caption choice.", preferences.Captions,
            Enum.GetValues<CaptionsPreference>(), value => preferences with { Captions = value });
        AddAccessibilityChoice("Audio description", "Prefer an available audio-description track.", preferences.AudioDescription,
            new[] { false, true }, value => preferences with { AudioDescription = value });
        AddAccessibilityChoice("Focus indicators", "Enhanced adds a thicker keyboard focus outline.", preferences.FocusIndicators,
            Enum.GetValues<FocusIndicatorsPreference>(), value => preferences with { FocusIndicators = value });
        QueueAccessibilityVisualRefresh();
    }

    private void AddAccessibilityChoice<T>(string title, string description, T selected, IReadOnlyList<T> choices, Func<T, AccessibilityPreferencesDocument> update) where T : notnull
    {
        var panel = PreferencePanel(title, description);
        var combo = new ComboBox { Header = title, MinWidth = 240, ItemsSource = choices, SelectedItem = selected, IsEnabled = _state.Profile?.CanManage == true };
        AutomationProperties.SetName(combo, $"{title}. Current value: {AccessibilityLabel(selected)}");
        combo.SelectionChanged += async (_, _) =>
        {
            if (combo.SelectedItem is not T value || EqualityComparer<T>.Default.Equals(value, selected)) return;
            await SaveAccessibilityAsync(update(value));
        };
        panel.Children.Add(combo);
        SettingsPanelHost.Children.Add(panel);
    }

    private static string AccessibilityLabel<T>(T value) => value?.ToString()?.Replace("NoPreference", "No preference", StringComparison.Ordinal) ?? string.Empty;

    private async Task SaveAccessibilityAsync(AccessibilityPreferencesDocument update)
    {
        if (_state.Client is not { } client || _state.Profile is not { Id: var profileId, CanManage: true }) return;
        try
        {
            var saved = await client.UpdateProfileAccessibilityPreferencesAsync(profileId, update, _state.Token);
            _protocolV22.ApplyAccessibility(saved);
            ApplyProfileAccessibility(saved);
            if (_activeSettingsCategory == "Accessibility") BuildAccessibilityControls(saved);
        }
        catch (RivuneServerException exception) when (exception.StatusCode == 409)
        {
            _protocolV22.MarkConflict("Accessibility preferences changed on another device. Reload before saving again.");
            SettingsPanelHost.Children.Insert(0, CreateSettingsError(_protocolV22.Failure!));
        }
        catch (Exception exception)
        {
            SettingsPanelHost.Children.Insert(0, CreateSettingsError(FriendlyError(exception)));
        }
    }

    private void ApplyProfileAccessibility(AccessibilityPreferencesDocument preferences)
    {
        var system = new CurrentSystemAccessibility(_uiSettings.AnimationsEnabled, IsHighContrastActive(), _preferredSubtitleId is not null);
        var effective = AccessibilityPreferencesPolicy.Resolve(preferences, system);
        _profileAccessibilityEffective = effective;
        if (effective.ReducedMotion) _heroTimer.Stop();
        ApplyCurrentAccessibilityVisuals();
        QueueAccessibilityVisualRefresh();
    }

    private void ApplyCurrentAccessibilityVisuals()
    {
        if (_profileAccessibilityEffective is not { } effective) return;
        var policy = AccessibilityVisualPolicy.From(effective);
        ApplyTextScale(Root, policy);
        ApplyFocusIndicators(Root, policy);
    }

    private void QueueAccessibilityVisualRefresh()
    {
        if (_profileAccessibilityEffective is null || _accessibilityVisualRefreshQueued) return;
        _accessibilityVisualRefreshQueued = true;
        DispatcherQueue.TryEnqueue(() =>
        {
            _accessibilityVisualRefreshQueued = false;
            Root.UpdateLayout();
            ApplyCurrentAccessibilityVisuals();
        });
    }

    private void ApplyTextScale(DependencyObject node, AccessibilityVisualPolicy policy)
    {
        if (node is TextBlock text)
        {
            var original = _unscaledTextSizes.GetValue(text, value => new OriginalFontSize(value.FontSize));
            text.FontSize = policy.ScaleFont(original.Value);
        }
        for (var index = 0; index < VisualTreeHelper.GetChildrenCount(node); index++)
            ApplyTextScale(VisualTreeHelper.GetChild(node, index), policy);
    }

    private void ApplyFocusIndicators(DependencyObject node, AccessibilityVisualPolicy policy)
    {
        if (node is Control control)
        {
            control.FocusVisualPrimaryThickness = new Thickness(policy.FocusPrimaryThickness);
            control.FocusVisualSecondaryThickness = new Thickness(policy.FocusSecondaryThickness);
        }
        if (node is ComboBox comboBox && _accessibleComboBoxes.TryAdd(comboBox, new object()))
            comboBox.DropDownOpened += AccessibleComboBox_DropDownOpened;
        for (var index = 0; index < VisualTreeHelper.GetChildrenCount(node); index++)
            ApplyFocusIndicators(VisualTreeHelper.GetChild(node, index), policy);
    }

    private void AccessibleComboBox_DropDownOpened(object? sender, object args)
    {
        if (_profileAccessibilityEffective is null || XamlRoot is null) return;
        DispatcherQueue.TryEnqueue(() =>
        {
            if (_profileAccessibilityEffective is not { } effective || XamlRoot is null) return;
            var policy = AccessibilityVisualPolicy.From(effective);
            foreach (var popup in VisualTreeHelper.GetOpenPopupsForXamlRoot(XamlRoot))
            {
                if (popup.Child is not { } child) continue;
                ApplyTextScale(child, policy);
                ApplyFocusIndicators(child, policy);
            }
        });
    }

    private sealed record CurrentSystemAccessibility(bool AnimationsEnabled, bool HighContrast, bool CaptionsEnabled) : ISystemAccessibilitySettings
    {
        public bool ReducedMotion => !AnimationsEnabled;
    }

    private async Task RenderProfileFeatureSettingsAsync()
    {
        SettingsPanelHost.Children.Clear();
        var live = new TextBlock { Text = "Loading queue and alerts…", TextWrapping = TextWrapping.Wrap };
        AutomationProperties.SetLiveSetting(live, AutomationLiveSetting.Polite);
        SettingsPanelHost.Children.Add(live);
        if (_state.Client is not { } client || _state.Profile is not { Id: var profileId, CanManage: var canManage })
        {
            live.Text = "Select a profile to load queue and alerts.";
            return;
        }
        await _protocolV22.LoadAsync(client, profileId, _state.Token, includeIncidents: canManage);
        if (_activeSettingsCategory != "Queue & alerts") return;
        SettingsPanelHost.Children.Clear();
        if (_protocolV22.Failure is { } failure)
        {
            var bar = new InfoBar { IsOpen = true, IsClosable = false, Message = failure, Severity = _protocolV22.State == FeatureLoadState.Offline ? InfoBarSeverity.Warning : InfoBarSeverity.Error };
            AutomationProperties.SetName(bar, failure);
            SettingsPanelHost.Children.Add(bar);
        }
        var refresh = new Button { Content = "Refresh queue and alerts", HorizontalAlignment = HorizontalAlignment.Left };
        AutomationProperties.SetName(refresh, "Refresh queue, saved searches, smart collections, notifications, and incidents");
        refresh.Click += async (_, _) => await RenderProfileFeatureSettingsAsync();
        SettingsPanelHost.Children.Add(refresh);
        BuildQueueSection(client, profileId);
        BuildSavedSearchSection(client);
        BuildSmartCollectionSection(client);
        BuildNotificationSection(client);
        BuildIncidentSection(client);
        QueueAccessibilityVisualRefresh();
    }

    private void BuildQueueSection(RivuneApiClient client, Guid profileId)
    {
        var section = FeatureSection("Reading queue", "Ordered, profile-synchronized queue. Conflicts require an explicit refresh.");
        var title = new TextBox { Header = "Title", PlaceholderText = "Title", MaxLength = 240 };
        var resource = new TextBox { Header = "Resource identifier", PlaceholderText = "Provider resource ID", MaxLength = 512 };
        var type = new ComboBox { Header = "Media type", ItemsSource = Enum.GetValues<QueueMediaType>(), SelectedItem = QueueMediaType.Movie };
        var add = new Button { Content = "Add to queue", IsEnabled = _state.Profile?.CanManage == true };
        add.Click += async (_, _) =>
        {
            if (string.IsNullOrWhiteSpace(title.Text) || string.IsNullOrWhiteSpace(resource.Text)) return;
            var operation = _readingQueueController.Begin(_protocolV22.QueueRevision);
            await RunFeatureMutationAsync(async () =>
            {
                await _readingQueueController.AddAsync(client, profileId, operation, (QueueMediaType)type.SelectedItem, resource.Text.Trim(), title.Text.Trim(), null, null, null, _state.Token);
                await ReloadQueueAsync(client, profileId);
            });
        };
        section.Children.Add(title); section.Children.Add(resource); section.Children.Add(type); section.Children.Add(add);
        foreach (var item in _protocolV22.Queue)
        {
            var row = FeatureRow($"{item.Position + 1}. {item.Title}", item.MediaType.ToString());
            if (item.Position > 0)
            {
                var earlier = new Button { Content = "Move earlier", IsEnabled = _state.Profile?.CanManage == true };
                AutomationProperties.SetName(earlier, $"Move {item.Title} earlier in queue");
                earlier.Click += async (_, _) =>
                {
                    var ordered = _protocolV22.Queue.Select(value => value.Id).ToList();
                    var index = ordered.IndexOf(item.Id);
                    (ordered[index - 1], ordered[index]) = (ordered[index], ordered[index - 1]);
                    var operation = _readingQueueController.Begin(_protocolV22.QueueRevision);
                    await RunFeatureMutationAsync(async () => { await _readingQueueController.ReorderAsync(client, profileId, operation, ordered, _state.Token); await ReloadQueueAsync(client, profileId); });
                };
                row.Children.Add(earlier);
            }
            var remove = new Button { Content = "Remove", IsEnabled = _state.Profile?.CanManage == true };
            AutomationProperties.SetName(remove, $"Remove {item.Title} from queue");
            remove.Click += async (_, _) =>
            {
                var operation = _readingQueueController.Begin(_protocolV22.QueueRevision);
                await RunFeatureMutationAsync(async () => { await _readingQueueController.RemoveAsync(client, profileId, item.Id, operation, _state.Token); await ReloadQueueAsync(client, profileId); });
            };
            row.Children.Add(remove); section.Children.Add(row);
        }
        SettingsPanelHost.Children.Add(section);
    }

    private async Task ReloadQueueAsync(RivuneApiClient client, Guid profileId)
    {
        _protocolV22.ApplyQueue(await client.GetReadingQueueAsync(profileId, _state.Token));
        if (_activeSettingsCategory == "Queue & alerts") await RenderProfileFeatureSettingsAsync();
    }

    private void BuildSavedSearchSection(RivuneApiClient client)
    {
        var section = FeatureSection("Saved searches", "Save a profile search with optimistic revisions.");
        var name = new TextBox { Header = "Name", MaxLength = 120 };
        var query = new TextBox { Header = "Search text", MaxLength = 256 };
        var save = new Button { Content = "Save search", IsEnabled = _state.Profile?.CanManage == true };
        save.Click += async (_, _) => await RunFeatureMutationAsync(async () =>
        {
            if (string.IsNullOrWhiteSpace(name.Text) || string.IsNullOrWhiteSpace(query.Text)) return;
            await client.CreateSavedSearchAsync(new SavedSearchInput(name.Text.Trim(), query.Text.Trim(), SavedSearchSort.Relevance), _state.Token);
            await RenderProfileFeatureSettingsAsync();
        });
        section.Children.Add(name); section.Children.Add(query); section.Children.Add(save);
        foreach (var saved in _protocolV22.SavedSearches)
        {
            var row = FeatureRow(saved.Name, $"{saved.Query} · {saved.Sort}");
            var delete = new Button { Content = "Delete", IsEnabled = _state.Profile?.CanManage == true };
            AutomationProperties.SetName(delete, $"Delete saved search {saved.Name}");
            delete.Click += async (_, _) => await RunFeatureMutationAsync(async () => { await client.DeleteSavedSearchAsync(saved.Id, saved.Revision, _state.Token); await RenderProfileFeatureSettingsAsync(); });
            row.Children.Add(delete); section.Children.Add(row);
        }
        SettingsPanelHost.Children.Add(section);
    }

    private void BuildSmartCollectionSection(RivuneApiClient client)
    {
        var section = FeatureSection("Smart collections", "Rules use the closed Rivune rule builder; free-form SQL and expressions are never accepted.");
        var name = new TextBox { Header = "Name", MaxLength = 120 };
        var genre = new TextBox { Header = "Genre equals", MaxLength = 128 };
        var create = new Button { Content = "Create smart collection", IsEnabled = _state.Profile?.CanManage == true };
        create.Click += async (_, _) => await RunFeatureMutationAsync(async () =>
        {
            if (string.IsNullOrWhiteSpace(name.Text) || string.IsNullOrWhiteSpace(genre.Text)) return;
            SmartRule rule = new SmartGenreRule(SmartTextOperator.Equals, genre.Text.Trim());
            await client.CreateSmartCollectionAsync(new SmartCollectionInput(name.Text.Trim(), rule, SmartCollectionSort.Title), _state.Token);
            await RenderProfileFeatureSettingsAsync();
        });
        section.Children.Add(name); section.Children.Add(genre); section.Children.Add(create);
        foreach (var collection in _protocolV22.SmartCollections)
        {
            var row = FeatureRow(collection.Name, $"Revision {collection.Revision}");
            var delete = new Button { Content = "Delete", IsEnabled = _state.Profile?.CanManage == true };
            AutomationProperties.SetName(delete, $"Delete smart collection {collection.Name}");
            delete.Click += async (_, _) => await RunFeatureMutationAsync(async () => { await client.DeleteSmartCollectionAsync(collection.Id, collection.Revision, _state.Token); await RenderProfileFeatureSettingsAsync(); });
            row.Children.Add(delete); section.Children.Add(row);
        }
        SettingsPanelHost.Children.Add(section);
    }

    private void BuildNotificationSection(RivuneApiClient client)
    {
        var section = FeatureSection("Media notifications", "Upcoming calendar, season, episode, and movie alerts. Read and dismiss are profile-scoped.");
        foreach (var notification in _protocolV22.Notifications)
        {
            var row = FeatureRow(notification.Title, NotificationKindLabel(notification.Kind) + (notification.ReadAt is null ? " · Unread" : " · Read"));
            var read = new Button { Content = "Mark read", IsEnabled = notification.ReadAt is null };
            var dismiss = new Button { Content = "Dismiss" };
            AutomationProperties.SetName(read, $"Mark {notification.Title} notification as read");
            AutomationProperties.SetName(dismiss, $"Dismiss {notification.Title} notification");
            read.Click += async (_, _) => await RunFeatureMutationAsync(async () => { await client.AcknowledgeMediaNotificationAsync(notification.Id, MediaNotificationAcknowledgementState.Read, _state.Token); await RenderProfileFeatureSettingsAsync(); });
            dismiss.Click += async (_, _) => await RunFeatureMutationAsync(async () => { await client.AcknowledgeMediaNotificationAsync(notification.Id, MediaNotificationAcknowledgementState.Dismissed, _state.Token); await RenderProfileFeatureSettingsAsync(); });
            row.Children.Add(read); row.Children.Add(dismiss); section.Children.Add(row);
        }
        foreach (var subscription in _protocolV22.NotificationSubscriptions)
        {
            var row = FeatureRow($"Following {subscription.TitleId:D}", $"{subscription.Timezone} · {subscription.LeadDays} day lead");
            var unfollow = new Button { Content = "Unfollow" };
            AutomationProperties.SetName(unfollow, $"Stop following title {subscription.TitleId:D}");
            unfollow.Click += async (_, _) => await RunFeatureMutationAsync(async () => { await client.UnfollowMediaNotificationsAsync(subscription.TitleId, _state.Token); await RenderProfileFeatureSettingsAsync(); });
            row.Children.Add(unfollow); section.Children.Add(row);
        }
        var titleId = new TextBox { Header = "Library title ID to follow", PlaceholderText = "UUID" };
        var follow = new Button { Content = "Follow title" };
        follow.Click += async (_, _) => await RunFeatureMutationAsync(async () =>
        {
            if (!Guid.TryParse(titleId.Text, out var id)) throw new InvalidOperationException("Enter a valid library title ID.");
            var timezone = _state.Discovery?.Timezone ?? "UTC";
            await client.FollowMediaNotificationsAsync(id, new MediaNotificationFollowInput(timezone, 90, 1), _state.Token);
            await RenderProfileFeatureSettingsAsync();
        });
        section.Children.Add(titleId); section.Children.Add(follow);
        SettingsPanelHost.Children.Add(section);
    }

    private void BuildIncidentSection(RivuneApiClient client)
    {
        var section = FeatureSection("Add-on incidents", "Bounded safe classifications only; provider URLs, tokens, and raw errors are not displayed.");
        foreach (var incident in _protocolV22.Incidents.Take(500))
        {
            var label = SafeIncidentPresentation.Label(incident);
            var row = FeatureRow(label, $"{incident.OccurrenceCount} occurrence(s) · Last: {incident.LastOccurredAt}");
            if (incident.AcknowledgedAt is null)
            {
                var acknowledge = new Button { Content = "Acknowledge", IsEnabled = _state.Profile?.CanManage == true };
                AutomationProperties.SetName(acknowledge, $"Acknowledge incident for {label}");
                acknowledge.Click += async (_, _) => await RunFeatureMutationAsync(async () => { await client.AcknowledgeAddonIncidentAsync(incident.Id, _state.Token); await RenderProfileFeatureSettingsAsync(); });
                row.Children.Add(acknowledge);
            }
            section.Children.Add(row);
        }
        SettingsPanelHost.Children.Add(section);
    }

    private async Task RunFeatureMutationAsync(Func<Task> mutation)
    {
        try { await mutation(); }
        catch (RivuneServerException exception) when (exception.StatusCode == 409)
        {
            _protocolV22.MarkConflict("The profile changed on another device. Refresh before retrying.");
            SettingsPanelHost.Children.Insert(0, CreateSettingsError(_protocolV22.Failure!));
        }
        catch (HttpRequestException)
        {
            SettingsPanelHost.Children.Insert(0, CreateSettingsError("Rivune is offline. The change was not synchronized."));
        }
        catch (Exception exception)
        {
            SettingsPanelHost.Children.Insert(0, CreateSettingsError(FriendlyError(exception)));
        }
        finally
        {
            QueueAccessibilityVisualRefresh();
        }
    }

    private static StackPanel FeatureSection(string title, string description)
    {
        var section = new StackPanel { Spacing = 10, Padding = new Thickness(16) };
        var heading = new TextBlock { Text = title, TextWrapping = TextWrapping.Wrap };
        AutomationProperties.SetHeadingLevel(heading, AutomationHeadingLevel.Level2);
        section.Children.Add(heading);
        section.Children.Add(new TextBlock { Text = description, TextWrapping = TextWrapping.Wrap });
        return section;
    }

    private static StackPanel FeatureRow(string name, string description)
    {
        var row = new StackPanel { Spacing = 6, Padding = new Thickness(8), TabFocusNavigation = KeyboardNavigationMode.Local };
        var label = new TextBlock { Text = name, TextWrapping = TextWrapping.Wrap };
        AutomationProperties.SetName(label, name);
        row.Children.Add(label);
        row.Children.Add(new TextBlock { Text = description, TextWrapping = TextWrapping.Wrap });
        return row;
    }

    private static string NotificationKindLabel(MediaNotificationKind kind) => kind switch
    {
        MediaNotificationKind.CalendarEventUpcoming => "Upcoming calendar event",
        MediaNotificationKind.SeasonAvailable => "Season available",
        MediaNotificationKind.EpisodeAvailable => "Episode available",
        MediaNotificationKind.MovieRelease => "Movie release",
        _ => "Media notification",
    };
}
