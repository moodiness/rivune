using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Rivune.App.ViewModels;
using Rivune.Windows;
using Windows.Media.Core;

namespace Rivune.App;

public sealed partial class MainPage
{
    private void RefreshOfflineProfiles()
    {
        if (OfflineProfileActions is null) return;
        OfflineProfileActions.Children.Clear();
        var profiles = _offlineMediaStore?.Profiles() ?? [];
        foreach (var profile in profiles)
        {
            var button = new Button
            {
                Tag = profile,
                Style = (Style)Application.Current.Resources["RivuneLabeledActionButton"],
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Left,
                MinHeight = 48,
                Content = LabeledActionContent(profile.Name, profile.RequiresPin ? "\uE72E" : "\uE896"),
            };
            AutomationProperties.SetName(button, UiFormat("Open downloads for {0}", profile.Name));
            button.Click += OfflineProfile_Click;
            ConfigureZoomButton(button);
            OfflineProfileActions.Children.Add(button);
        }
        OfflineProfilesPanel.Visibility = profiles.Count == 0 ? Visibility.Collapsed : Visibility.Visible;
    }

    private async void OfflineProfile_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: OfflineProfileGate profile }) return;
        await OpenOfflineProfileAsync(profile);
    }

    private async Task OpenOfflineProfileAsync(OfflineProfileGate profile)
    {
        var store = _offlineMediaStore;
        if (store is null || store.Profile(profile.Scope) != profile) return;
        string? pin = null;
        if (profile.RequiresPin)
        {
            pin = await PromptOfflinePinAsync(profile.Name);
            if (pin is null) return;
        }
        if (!store.Unlock(profile.Scope, pin))
        {
            await ShowUpdateDialogAsync("Downloads remain locked", "That PIN was not accepted for this downloaded profile.");
            return;
        }
        _offlineScope = profile.Scope;
        _offlineOnlySession = _state.Client is null || _state.Profile is null;
        LoadOfflineItems();
        ShowOfflineDashboard(profile.Name);
    }

    private async Task<string?> PromptOfflinePinAsync(string profileName)
    {
        var pin = new PasswordBox
        {
            Header = "PIN",
            MaxLength = 8,
            PasswordChar = "●",
            FlowDirection = FlowDirection.LeftToRight,
            InputScope = new InputScope { Names = { new InputScopeName(InputScopeNameValue.Number) } },
        };
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = UiFormat("Unlock downloads for {0}", profileName),
            Content = pin,
            PrimaryButtonText = "Unlock",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            IsPrimaryButtonEnabled = false,
        };
        var filtering = false;
        pin.PasswordChanged += (_, _) =>
        {
            if (filtering) return;
            var digits = new string(pin.Password.Where(char.IsDigit).Take(8).ToArray());
            if (!StringComparer.Ordinal.Equals(pin.Password, digits))
            {
                filtering = true;
                pin.Password = digits;
                filtering = false;
            }
            dialog.IsPrimaryButtonEnabled = digits.Length is >= 4 and <= 8;
        };
        try
        {
            if (await ShowDialogAsync(dialog) != ContentDialogResult.Primary) return null;
            return new string(pin.Password.Where(char.IsDigit).ToArray());
        }
        finally { pin.Password = string.Empty; }
    }

    private void RegisterOfflineProfile(Profile profile, string? pin)
    {
        var store = _offlineMediaStore;
        if (store is null || CurrentServerOrigin() is not { } origin) return;
        try
        {
            _offlineScope = store.RegisterProfile(origin, profile, pin);
            _offlineOnlySession = false;
            LoadOfflineItems();
            RefreshOfflineProfiles();
        }
        catch (Exception exception) { DisableOfflineStorage(exception); }
    }

    private void RestoreOfflineProfile(Profile profile)
    {
        var store = _offlineMediaStore;
        if (store is null || CurrentServerOrigin() is not { } origin) return;
        try
        {
            _offlineScope = store.OpenRestoredProfile(origin, profile);
            _offlineOnlySession = false;
            LoadOfflineItems();
            RefreshOfflineProfiles();
        }
        catch (Exception exception) { DisableOfflineStorage(exception); }
    }

    private void DisableOfflineStorage(Exception exception)
    {
        _offlineMediaStore?.Dispose();
        _offlineMediaStore = null;
        _offlineScope = null;
        _offlineItems = [];
        _offlineOnlySession = false;
        _devicePreferencesFailure = string.Join(" ", new[]
        {
            _devicePreferencesFailure,
            UiFormat("Offline storage: {0}", FriendlyError(exception)),
        }.Where(value => !string.IsNullOrWhiteSpace(value)));
    }

    private Uri? CurrentServerOrigin()
    {
        if (!Uri.TryCreate(ServerAddressBox.Text, UriKind.Absolute, out var origin) || !origin.IsAbsoluteUri) return null;
        return origin;
    }

    private async Task OfferOfflineUnlockForActiveProfileAsync()
    {
        if (_offlineScope is not null || _state.Profile is not { } profile || _offlineMediaStore is null ||
            CurrentServerOrigin() is not { } origin) return;
        var scope = OfflineMediaStore.ScopeFor(origin, profile.Id);
        var gate = _offlineMediaStore.Profiles().FirstOrDefault(value => StringComparer.Ordinal.Equals(value.Scope, scope));
        if (gate is null) return;
        var pin = gate.RequiresPin ? await PromptOfflinePinAsync(profile.Name) : null;
        if (gate.RequiresPin && pin is null) return;
        if (_offlineMediaStore.Unlock(scope, pin))
        {
            _offlineScope = scope;
            LoadOfflineItems();
            RebuildHomeSections(_viewerCollections, _continueWatchingTargets, _recommendationTargets);
        }
    }

    private void LoadOfflineItems()
    {
        if (_offlineMediaStore is null || _offlineScope is null)
        {
            _offlineItems = [];
            return;
        }
        try { _offlineItems = _offlineMediaStore.Items(_offlineScope); }
        catch { _offlineItems = []; }
        RefreshOfflineProfiles();
    }

    private void LockOfflineAccess()
    {
        _offlineDownloadCancellation?.Cancel();
        StopOfflinePlayback();
        _offlineMediaStore?.Lock();
        _offlineScope = null;
        _offlineItems = [];
        _offlineOnlySession = false;
        RefreshOfflineProfiles();
    }

    private void ShowOfflineDashboard(string profileName)
    {
        StopPlaybackCoordinationPolling();
        _state.Transition(AppPhase.Catalogue);
        _selectedViewerTab = ViewerTab.Home;
        _viewerCollections = [];
        _continueWatchingTargets = [];
        _recommendationTargets = [];
        _heroTargets = [];
        HeroPanel.Visibility = Visibility.Collapsed;
        DashboardBanner.IsOpen = false;
        DashboardRetryButton.Visibility = Visibility.Collapsed;
        DashboardProgress.IsActive = false;
        DashboardLoadingStatus.Visibility = Visibility.Collapsed;
        CompactProfileInitial.Text = ProfileInitial(profileName);
        DockProfileInitial.Text = ProfileInitial(profileName);
        AutomationProperties.SetName(ProfileMenuButton, UiFormat("Offline downloads for {0}", profileName));
        AutomationProperties.SetName(DockAccountButton, UiFormat("Offline downloads for {0}", profileName));
        SetOnlineNavigationEnabled(false);
        RebuildHomeSections([], [], []);
        ShowOnly(DashboardView);
        ShowViewerTab(ViewerTab.Home);
    }

    private void SetOnlineNavigationEnabled(bool enabled)
    {
        foreach (var control in new Control[]
                 {
                     SearchNav, LibraryNav, CalendarNav,
                     BottomSearchNav, BottomLibraryNav, BottomCalendarNav,
                 }) control.IsEnabled = enabled;
    }

    private FrameworkElement CreateOfflineMediaRow()
    {
        var section = new StackPanel { Spacing = 12 };
        section.Children.Add(new TextBlock
        {
            Text = UiText("Downloads"),
            Style = (Style)Application.Current.Resources["RivuneTitleLargeTextStyle"],
        });
        var row = HorizontalList();
        foreach (var item in _offlineItems) row.Items.Add(CreateOfflineMediaCard(item));
        section.Children.Add(row);
        return section;
    }

    private Button CreateOfflineMediaCard(OfflineMediaItem item)
    {
        var width = _tvInputMode ? TvLandscapeCardWidth : LandscapeCardWidth;
        var button = CreateArtworkCard(
            item.Title,
            artworkUrl: null,
            width,
            width / LandscapeCardAspectRatio,
            hideTitle: false,
            enabled: item.State == OfflineMediaState.Ready,
            fallbackText: item.Title.Length == 0 ? "R" : item.Title[..1].ToUpperInvariant());
        button.Tag = item;
        button.Click += OfflineMedia_Click;
        AutomationProperties.SetName(button, item.State == OfflineMediaState.Ready
            ? UiFormat("Play downloaded {0}, {1}", item.Title, FormatBytes(item.SizeBytes))
            : UiFormat("Downloaded {0}, {1}, {2}", item.Title, FormatBytes(item.SizeBytes), item.State.ToString()));
        var menu = new MenuFlyout();
        var remove = new MenuFlyoutItem { Text = UiText("Delete download"), Icon = new FontIcon { Glyph = "\uE74D" }, Tag = item };
        remove.Click += RemoveOfflineMedia_Click;
        menu.Items.Add(remove);
        button.ContextFlyout = menu;
        return button;
    }

    private async void OfflineMedia_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: OfflineMediaItem item }) return;
        try { await PlayOfflineAsync(item); }
        catch (Exception exception)
        {
            DashboardBanner.Severity = InfoBarSeverity.Error;
            DashboardBanner.Message = UiFormat("Downloaded media could not be opened: {0}", FriendlyError(exception));
            DashboardBanner.IsOpen = true;
            LoadOfflineItems();
            RebuildHomeSections(_viewerCollections, _continueWatchingTargets, _recommendationTargets);
        }
    }

    private async void RemoveOfflineMedia_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not MenuFlyoutItem { Tag: OfflineMediaItem item } || _offlineMediaStore is null || _offlineScope is null) return;
        var dialog = Dialog("Delete download?", UiFormat("Delete the encrypted offline copy of {0}?", item.Title), "Delete");
        if (await ShowDialogAsync(dialog) != ContentDialogResult.Primary) return;
        try
        {
            _offlineMediaStore.Remove(_offlineScope, item);
            LoadOfflineItems();
            RebuildHomeSections(_viewerCollections, _continueWatchingTargets, _recommendationTargets);
        }
        catch (Exception exception)
        {
            DashboardBanner.Severity = InfoBarSeverity.Error;
            DashboardBanner.Message = FriendlyError(exception);
            DashboardBanner.IsOpen = true;
        }
    }

    private async Task PlayOfflineAsync(OfflineMediaItem item, int? requestedPositionSeconds = null)
    {
        if (_offlineMediaStore is null || _offlineScope is null || !_offlineItems.Contains(item)) return;
        StopOfflinePlayback();
        _offlinePlaybackServer = _offlineMediaStore.StartPlayback(_offlineScope, item);
        _activeOfflineItem = item;
        _tracksProgress = true;
        _progressTitleId = item.TitleId;
        _progressVersion = 0;
        _playbackCompleted = false;
        _playbackTitle = item.Title;
        _sourceReturnView = DashboardView;
        _playerReturnView = DashboardView;
        _state.PlaybackSession = null;
        _state.SelectedSource = null;
        _state.Preparation = null;
        lock (_endingSync)
        {
            _endingTask = null;
            _playerReturnTask = null;
            _diagnosticPlaybackActive = false;
        }
        var startSeconds = requestedPositionSeconds ?? (item.Completed ? 0 : (int)Math.Clamp(item.PositionMilliseconds / 1_000, 0, int.MaxValue));
        var source = new PlaybackSource
        {
            Id = $"offline:{item.Id:D}",
            AddonId = Guid.Empty,
            ManifestId = "offline",
            Name = item.Title,
            Title = item.Title,
            Mode = PlaybackMode.Direct,
            Url = _offlinePlaybackServer.PlaybackUri.AbsoluteUri,
            Protocol = "http",
            Container = item.Container,
            MediaTimeline = PlaybackMediaTimeline.Absolute,
            Compatible = true,
        };
        try { await ShowOfflinePlayerAsync(source, startSeconds); }
        catch
        {
            _diagnostics.Record(DiagnosticEventCode.PlaybackFailed);
            StopOfflinePlayback();
            throw;
        }
    }

    private async Task ShowOfflinePlayerAsync(PlaybackSource source, int startSeconds)
    {
        _activePlaybackSource = source;
        var generation = _state.Transition(AppPhase.Player);
        _playbackMarkers = [];
        _activeMarkerIndex = -1;
        RemoveMarkerSkipButton();
        _nextEpisodeTarget = null;
        PlayerNextButton.Visibility = Visibility.Collapsed;
        _aspectIndex = _devicePreferences.VideoAspectIndex;
        ApplyPlayerAspect();
        AutomationProperties.SetName(PlayPauseButton, "Play");
        SourceOverlay.Visibility = Visibility.Collapsed;
        DetailBackButton.Visibility = Visibility.Visible;
        ShowOnly(PlayerView);
        SetPlayerControlsLocked(false);
        SetPlaybackRate(2, applyToPlayer: false);
        UpdatePlayerPresenterActions();
        RevealPlayerChrome(focusTransport: _tvInputMode);
        PlayerTitle.Text = _playbackTitle ?? "Offline playback";
        SetPlayerStatus("Opening encrypted download…", busy: true);
        _timeline.Reset(source.MediaTimeline, startSeconds, source.Media?.DurationSeconds);
        _lastQueuedPosition = startSeconds - 10;
        TimelineSlider.Minimum = _timeline.OffsetSeconds;
        ReplaceMediaPlayerForSource();
        _mediaSource = MediaSource.CreateFromUri(new Uri(source.Url!));
        _mediaPlayer.Source = _mediaSource;
        _mediaPlayer.PlaybackSession.PlaybackRate = PlaybackRates[_playbackRateIndex];
        if (startSeconds > 0) _mediaPlayer.PlaybackSession.Position = TimeSpan.FromSeconds(startSeconds);
        lock (_endingSync)
        {
            if (_endingTask is not null || _playerReturnTask is not null || _closed)
                throw new OperationCanceledException();
            _mediaPlayer.Play();
            _positionTimer.Start();
            _diagnosticPlaybackActive = true;
            _diagnostics.Record(DiagnosticEventCode.PlaybackStarted);
        }
        StartPlayerStartupWatchdog(generation);
        UpdateTrackButtonLabels();
        PlayerAudioButton.IsEnabled = false;
        PlayerSubtitlesButton.IsEnabled = false;
        await Task.CompletedTask;
    }

    private async Task RestartOfflinePlaybackAsync(int positionSeconds)
    {
        var item = _activeOfflineItem;
        if (item is null) return;
        await PlayOfflineAsync(item, positionSeconds);
    }

    private void StopOfflinePlayback(bool clearItem = true)
    {
        _offlinePlaybackServer?.Dispose();
        _offlinePlaybackServer = null;
        if (clearItem) _activeOfflineItem = null;
        PlayerAudioButton.IsEnabled = true;
        PlayerSubtitlesButton.IsEnabled = true;
    }

    private Task WriteOfflineProgressSnapshotAsync(ProgressSnapshot snapshot)
    {
        var store = _offlineMediaStore;
        var scope = _offlineScope;
        var item = _activeOfflineItem;
        if (store is null || scope is null || item is null) return Task.CompletedTask;
        return Task.Run(() =>
        {
            var updated = store.UpdateProgress(
                scope,
                item.Id,
                snapshot.Position * 1_000L,
                snapshot.Duration * 1_000L,
                snapshot.Completed);
            DispatcherQueue.TryEnqueue(() =>
            {
                if (_activeOfflineItem?.Id == updated.Id) _activeOfflineItem = updated;
                _offlineItems = _offlineItems.Select(value => value.Id == updated.Id ? updated : value).ToArray();
            });
        });
    }
}
