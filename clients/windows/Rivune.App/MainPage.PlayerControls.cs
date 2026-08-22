using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Rivune.Windows;
using Rivune.App.ViewModels;
using Windows.Media.Playback;

namespace Rivune.App;

public sealed partial class MainPage
{
    private static readonly double[] PlaybackRates = [0.5, 0.75, 1, 1.25, 1.5, 2];
    private int? _preferredAudioTrack;
    private string? _preferredSubtitleId;
    private int _playbackRateIndex = 2;
    private int _aspectIndex;
    private MenuFlyout? _playbackRateFlyout;
    private ToggleMenuFlyoutItem[]? _playbackRateItems;
    private MenuFlyout? _audioTrackFlyout;
    private ToggleMenuFlyoutItem[]? _audioTrackItems;
    private MenuFlyout? _subtitleFlyout;
    private ToggleMenuFlyoutItem[]? _subtitleItems;
    private bool _playerControlsLocked;
    private bool _trackRestarting;
    private CancellationTokenSource? _trackRestartCancellation;
    private PlaybackSource? _activePlaybackSource;
    private IReadOnlyList<PlaybackSourceOption> _sourceOptions = [];
    private IReadOnlyList<PlaybackMarker> _playbackMarkers = [];
    private Button? _markerSkipButton;
    private int _activeMarkerIndex = -1;
    private MediaTarget? _nextEpisodeTarget;
    private bool _advancingEpisode;
    private bool _autoStartNextEpisode;
    private Guid? _preferredSourceAddonId;
    private readonly HashSet<int> _autoSkippedMarkerIndexes = [];

    private void UpdateSourceOptions(IReadOnlyList<PlaybackSourceOption> options)
    {
        _sourceOptions = options;
        foreach (var button in SourceFilters.Children.OfType<ToggleButton>()) ForgetZoomButton(button);
        SourceFilters.Children.Clear();
        AddSourceFilter("All", null, isChecked: true);
        foreach (var (addonId, label) in SourceAddonFilters(options))
            AddSourceFilter(label, addonId, isChecked: false);
        ApplySourceFilter(null);
    }

    private void AddSourceFilter(string label, Guid? addonId, bool isChecked)
    {
        var button = new ToggleButton
        {
            Content = label,
            Tag = addonId,
            IsChecked = isChecked,
            Style = (Style)Application.Current.Resources["RivuneSourceFilterToggle"],
        };
        button.Click += SourceFilter_Click;
        ConfigureZoomButton(button);
        SourceFilters.Children.Add(button);
    }

    private void SourceFilter_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not ToggleButton selected) return;
        foreach (var button in SourceFilters.Children.OfType<ToggleButton>()) button.IsChecked = button == selected;
        ApplySourceFilter(selected.Tag as Guid?);
    }

    private void ApplySourceFilter(Guid? addonId)
    {
        SourceList.SelectedItem = null;
        _state.SelectedSource = null;
        var downloading = _offlineDownloadTask is { IsCompleted: false };
        PlaySourceButton.IsEnabled = false;
        PlaySourceButton.Visibility = Visibility.Collapsed;
        DownloadSourceButton.IsEnabled = downloading;
        DownloadSourceButton.Visibility = downloading ? Visibility.Visible : Visibility.Collapsed;
        var filtered = addonId is null ? _sourceOptions : _sourceOptions.Where(value => value.AddonId == addonId).ToArray();
        SourceList.ItemsSource = filtered;
        SourceStatus.Text = filtered.Count == 0
            ? (_sourceOptions.Count == 0 ? "No streams found." : "No sources are available from this add-on.")
            : string.Empty;
        DispatcherQueue.TryEnqueue(() =>
        {
            Control target = filtered.Count > 0 ? SourceList : RefreshSourcesButton;
            target.Focus(FocusState.Programmatic);
        });
    }

    private static IReadOnlyList<(Guid AddonId, string Label)> SourceAddonFilters(IReadOnlyList<PlaybackSourceOption> options)
    {
        var representatives = new Dictionary<Guid, PlaybackSourceOption>();
        foreach (var option in options)
        {
            if (!representatives.TryGetValue(option.AddonId, out var current) ||
                (string.IsNullOrWhiteSpace(current.AddonName) && !string.IsNullOrWhiteSpace(option.AddonName)))
                representatives[option.AddonId] = option;
        }
        var baseLabels = representatives.Values.ToDictionary(
            option => option.AddonId,
            option => SourceAddonLabel(option.AddonName, option.ManifestId, option.AddonId));
        var duplicateLabels = baseLabels.Values.GroupBy(value => value, StringComparer.OrdinalIgnoreCase)
            .Where(group => group.Count() > 1)
            .Select(group => group.Key)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);
        return representatives.Values.Select(option =>
        {
            var label = baseLabels[option.AddonId];
            return (option.AddonId, duplicateLabels.Contains(label) ? $"{label} · {option.AddonId:D}" : label);
        }).ToArray();
    }

    public static string SourceAddonLabel(string? addonName, string manifestId, Guid addonId) =>
        !string.IsNullOrWhiteSpace(addonName) ? addonName.Trim() :
        !string.IsNullOrWhiteSpace(manifestId) ? manifestId.Trim() : addonId.ToString("D");

    public static string SourceFooter(
        string? addonName,
        string manifestId,
        Guid addonId,
        PlaybackMode? mode,
        string protocol,
        string? container) =>
        $"{SourceAddonLabel(addonName, manifestId, addonId)} · {SourceDetails(mode, protocol, container)}";

    private void SetSourceRefreshLoading(bool loading)
    {
        RefreshSourcesButton.IsEnabled = !loading;
        RefreshSourcesIcon.Visibility = loading ? Visibility.Collapsed : Visibility.Visible;
        RefreshSourcesProgress.Visibility = loading ? Visibility.Visible : Visibility.Collapsed;
        RefreshSourcesProgress.IsActive = loading;
    }

    private async void RefreshSources_Click(object sender, RoutedEventArgs e)
    {
        var request = _sourceRequest;
        if (request is null) return;
        SetSourceRefreshLoading(true);
        var generation = _state.Transition(AppPhase.Sources);
        _state.SelectedSource = null;
        _state.Preparation = null;
        SourceList.SelectedItem = null;
        SourceList.ItemsSource = null;
        SourceBanner.IsOpen = false;
        SourceProgress.IsActive = true;
        PlaySourceButton.IsEnabled = false;
        var downloading = _offlineDownloadTask is { IsCompleted: false };
        DownloadSourceButton.IsEnabled = downloading;
        DownloadSourceButton.Visibility = downloading ? Visibility.Visible : Visibility.Collapsed;
        try
        {
            await LoadSourcesAsync(request.MediaType, request.ResourceId, request.TitleId, generation, request.AddonId, request.TracksProgress);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception) { if (_state.IsCurrent(generation)) SetSourceFailure(exception); }
        finally
        {
            SetSourceRefreshLoading(false);
            if (_state.IsCurrent(generation)) SourceProgress.IsActive = false;
        }
    }

    private async void PlayerAudio_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        var session = _state.PlaybackSession;
        var source = _activePlaybackSource;
        var tracks = source?.Media?.AudioTracks ?? [];
        if (tracks.Count == 0)
        {
            await ShowDialogAsync(Dialog("Audio", "This source does not expose selectable audio tracks.", "Close"));
            return;
        }
        if (session is null || source is null) return;

        _audioTrackFlyout = new MenuFlyout { Placement = FlyoutPlacementMode.Top };
        _audioTrackItems = new ToggleMenuFlyoutItem[tracks.Count];
        for (var index = 0; index < tracks.Count; index++)
        {
            var track = tracks[index];
            var label = AudioTrackLabel(track);
            var item = new ToggleMenuFlyoutItem
            {
                Text = label,
                Tag = new PlaybackAudioChoice(session, source, track.Index, label),
                IsChecked = track.Index == _preferredAudioTrack,
            };
            item.Click += AudioTrackItem_Click;
            _audioTrackItems[index] = item;
            _audioTrackFlyout.Items.Add(item);
        }

        UpdateTrackButtonLabels();
        _audioTrackFlyout.ShowAt(PlayerAudioButton);
        NotePlayerInteraction();
    }

    private async void PlayerSubtitles_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        var session = _state.PlaybackSession;
        if (session is null || session.Subtitles.Count == 0)
        {
            await ShowDialogAsync(Dialog("Subtitles", "No external subtitle track is available for this source.", "Close"));
            return;
        }

        _subtitleFlyout = new MenuFlyout { Placement = FlyoutPlacementMode.Top };
        _subtitleItems = new ToggleMenuFlyoutItem[session.Subtitles.Count + 1];
        AddSubtitleItem(session, null, "Off", 0);
        for (var index = 0; index < session.Subtitles.Count; index++)
        {
            var subtitle = session.Subtitles[index];
            AddSubtitleItem(session, subtitle.Id, SubtitleLabel(subtitle), index + 1);
        }

        UpdateTrackButtonLabels();
        _subtitleFlyout.ShowAt(PlayerSubtitlesButton);
        NotePlayerInteraction();
    }

    private void AddSubtitleItem(PlaybackSession session, string? id, string label, int index)
    {
        var item = new ToggleMenuFlyoutItem
        {
            Text = label,
            Tag = new PlaybackSubtitleChoice(session, id, label),
            IsChecked = string.Equals(id, _preferredSubtitleId, StringComparison.Ordinal),
        };
        item.Click += SubtitleItem_Click;
        _subtitleItems![index] = item;
        _subtitleFlyout!.Items.Add(item);
    }

    private async void AudioTrackItem_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked ||
            sender is not ToggleMenuFlyoutItem item ||
            item.Tag is not PlaybackAudioChoice choice ||
            !ReferenceEquals(_state.PlaybackSession, choice.Session) ||
            !ReferenceEquals(_activePlaybackSource, choice.Source) ||
            !ContainsItem(_audioTrackItems, item)) return;

        if (_trackRestarting)
        {
            SynchronizeAudioTrackChecks(_preferredAudioTrack);
            return;
        }
        SynchronizeAudioTrackChecks(choice.Index);
        NotePlayerInteraction();
        if (choice.Index == _preferredAudioTrack) return;
        _preferredAudioTrack = choice.Index;
        UpdateTrackButtonLabels(audioLabel: choice.Label);
        await RestartPlaybackWithTracksAsync();
    }

    private async void SubtitleItem_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked ||
            sender is not ToggleMenuFlyoutItem item ||
            item.Tag is not PlaybackSubtitleChoice choice ||
            !ReferenceEquals(_state.PlaybackSession, choice.Session) ||
            !ContainsItem(_subtitleItems, item)) return;

        if (_trackRestarting)
        {
            SynchronizeSubtitleChecks(_preferredSubtitleId);
            return;
        }
        SynchronizeSubtitleChecks(choice.Id);
        NotePlayerInteraction();
        if (string.Equals(choice.Id, _preferredSubtitleId, StringComparison.Ordinal)) return;
        _preferredSubtitleId = choice.Id;
        UpdateTrackButtonLabels(subtitleLabel: choice.Label);
        await RestartPlaybackWithTracksAsync();
    }

    private void SynchronizeAudioTrackChecks(int? selectedIndex)
    {
        if (_audioTrackItems is null) return;
        foreach (var item in _audioTrackItems)
            item.IsChecked = item.Tag is PlaybackAudioChoice choice && choice.Index == selectedIndex;
    }

    private void SynchronizeSubtitleChecks(string? selectedId)
    {
        if (_subtitleItems is null) return;
        foreach (var item in _subtitleItems)
            item.IsChecked = item.Tag is PlaybackSubtitleChoice choice &&
                string.Equals(choice.Id, selectedId, StringComparison.Ordinal);
    }

    private static bool ContainsItem(ToggleMenuFlyoutItem[]? items, ToggleMenuFlyoutItem item)
    {
        if (items is null) return false;
        foreach (var candidate in items)
            if (ReferenceEquals(candidate, item)) return true;
        return false;
    }

    private void UpdateTrackButtonLabels(string? audioLabel = null, string? subtitleLabel = null)
    {
        if (audioLabel is null && _activePlaybackSource?.Media?.AudioTracks is { } tracks)
        {
            foreach (var track in tracks)
            {
                if (track.Index != _preferredAudioTrack) continue;
                audioLabel = AudioTrackLabel(track);
                break;
            }
        }
        audioLabel ??= "Default";
        ToolTipService.SetToolTip(PlayerAudioButton, $"Audio: {audioLabel}");
        AutomationProperties.SetName(PlayerAudioButton, $"Audio track, {audioLabel}");

        if (subtitleLabel is null)
        {
            subtitleLabel = "Off";
            if (_preferredSubtitleId is not null && _state.PlaybackSession is { } session)
            {
                foreach (var subtitle in session.Subtitles)
                {
                    if (!string.Equals(subtitle.Id, _preferredSubtitleId, StringComparison.Ordinal)) continue;
                    subtitleLabel = SubtitleLabel(subtitle);
                    break;
                }
            }
        }
        ToolTipService.SetToolTip(PlayerSubtitlesButton, $"Subtitles: {subtitleLabel}");
        AutomationProperties.SetName(PlayerSubtitlesButton, $"Subtitles, {subtitleLabel}");
    }

    private static string AudioTrackLabel(PlaybackMediaTrack track) =>
        $"{track.Language ?? "Unknown language"} · {track.Title ?? track.Codec}{(track.Channels is int channels ? $" · {channels} channels" : string.Empty)}";

    private static string SubtitleLabel(PlaybackSubtitle subtitle) =>
        $"{subtitle.Language ?? "Unknown language"}{(subtitle.Forced == true ? " · Forced" : string.Empty)}";

    private async Task<Exception?> RestartPlaybackWithTracksAsync(int? requestedPosition = null, bool showRecovery = true)
    {
        var selectedSource = _state.SelectedSource;
        var currentSession = _state.PlaybackSession;
        var client = _state.Client;
        if (selectedSource is null || currentSession is null || client is null)
            return new InvalidOperationException("The current source cannot be restarted.");
        var previousAudioTrack = currentSession.SelectedAudioTrack;
        var previousSubtitleId = currentSession.SelectedSubtitleId;

        CancellationTokenSource restartCancellation;
        Task? observedEnding;
        long restartGeneration;
        lock (_endingSync)
        {
            if (_trackRestarting || _playerReturnTask is not null || _closed ||
                _endingTask is { IsCompleted: false } ||
                !ReferenceEquals(_state.PlaybackSession, currentSession))
                return new OperationCanceledException("Playback is already transitioning.");
            _trackRestarting = true;
            restartCancellation = new CancellationTokenSource();
            _trackRestartCancellation = restartCancellation;
            observedEnding = _endingTask;
            restartGeneration = _state.GenerationId;
        }

        var position = requestedPosition ?? (int)Math.Max(0, AbsolutePlaybackPosition(_mediaPlayer.PlaybackSession.Position));
        PlaybackSession? replacement = null;
        MediaPlayer? playerBeforeReplacement = null;
        var replacementAssigned = false;
        var adopted = false;
        var oldSessionStopped = false;
        string? recoveryMessage = null;
        Exception? restartFailure = null;
        try
        {
            SetPlayerStatus("Applying track selection…", busy: true);
            using (var resolveCancellation = CancellationTokenSource.CreateLinkedTokenSource(_state.Token, restartCancellation.Token))
            {
                replacement = await client.ResolvePlaybackAsync(
                    selectedSource.SourceRef,
                    _progressTitleId.ToString("D"),
                    _preferredAudioTrack,
                    _preferredSubtitleId ?? "none",
                    position,
                    resolveCancellation.Token);
            }
            restartCancellation.Token.ThrowIfCancellationRequested();
            if (!_state.IsCurrent(restartGeneration)) throw new OperationCanceledException();
            var source = replacement.Sources.FirstOrDefault(candidate =>
                candidate.Id == replacement.SelectedSourceId && candidate.Compatible && candidate.Url is not null &&
                (candidate.Protocol.Equals("hls", StringComparison.OrdinalIgnoreCase) || candidate.Protocol.Equals("http", StringComparison.OrdinalIgnoreCase)))
                ?? throw new InvalidOperationException("The selected track combination is not compatible with native HTTP/HLS playback.");
            await StopSessionOnceAsync(currentSession, client);
            oldSessionStopped = true;
            restartCancellation.Token.ThrowIfCancellationRequested();
            if (!_state.IsCurrent(restartGeneration)) throw new OperationCanceledException();

            lock (_endingSync)
            {
                restartCancellation.Token.ThrowIfCancellationRequested();
                if (_playerReturnTask is not null || _closed || !_state.IsCurrent(restartGeneration) ||
                    !ReferenceEquals(_endingTask, observedEnding) ||
                    !ReferenceEquals(_state.PlaybackSession, currentSession))
                    throw new OperationCanceledException(restartCancellation.Token);

                _state.PlaybackSession = replacement;
                _endingTask = null;
                replacementAssigned = true;
            }

            _playbackCompleted = false;
            playerBeforeReplacement = _mediaPlayer;
            _mediaPlayer.Pause();
            restartCancellation.Token.ThrowIfCancellationRequested();
            if (!_state.IsCurrent(restartGeneration)) throw new OperationCanceledException();
            ClearMediaSource();
            await ShowPlayerAsync(source, position, restartCancellation.Token, preservePlaybackRate: true);
            restartCancellation.Token.ThrowIfCancellationRequested();
            _preferredAudioTrack = replacement.SelectedAudioTrack;
            _preferredSubtitleId = replacement.SelectedSubtitleId;
            SynchronizeAudioTrackChecks(_preferredAudioTrack);
            SynchronizeSubtitleChecks(_preferredSubtitleId);
            UpdateTrackButtonLabels();
            adopted = true;
        }
        catch (OperationCanceledException exception) { restartFailure = exception; }
        catch (Exception exception) when (restartCancellation.IsCancellationRequested) { restartFailure = exception; }
        catch (Exception exception)
        {
            restartFailure = exception;
            SetPlayerStatus(FriendlyError(exception), liveSetting: AutomationLiveSetting.Assertive);
            if (oldSessionStopped && showRecovery) recoveryMessage = FriendlyError(exception);
        }
        finally
        {
            if (replacement is not null && !adopted)
            {
                if (playerBeforeReplacement is not null && !ReferenceEquals(playerBeforeReplacement, _mediaPlayer))
                {
                    _mediaPlayer.Pause();
                    ClearMediaSource();
                }
                try { await client.StopPlaybackAsync(replacement.Id, CancellationToken.None); }
                catch (Exception) { }
                if (replacementAssigned && ReferenceEquals(_state.PlaybackSession, replacement))
                    _state.PlaybackSession = currentSession;
            }
            if (!oldSessionStopped && !adopted)
            {
                _preferredAudioTrack = previousAudioTrack;
                _preferredSubtitleId = previousSubtitleId;
                SynchronizeAudioTrackChecks(_preferredAudioTrack);
                SynchronizeSubtitleChecks(_preferredSubtitleId);
                UpdateTrackButtonLabels();
            }
            lock (_endingSync)
            {
                if (ReferenceEquals(_trackRestartCancellation, restartCancellation))
                {
                    _trackRestartCancellation = null;
                    _trackRestarting = false;
                }
            }
            var restartWasCancelled = restartCancellation.IsCancellationRequested;
            restartCancellation.Dispose();
            if (recoveryMessage is not null && !restartWasCancelled && !_closed)
                await ShowPlaybackRecoveryAsync(recoveryMessage, position);
        }
        return adopted ? null : restartFailure ?? new InvalidOperationException("Playback could not be restarted.");
    }

    private async void PlayerNext_Click(object sender, RoutedEventArgs e) => await AdvanceToNextEpisodeAsync();

    private async Task AdvanceToNextEpisodeAsync()
    {
        if (_playerControlsLocked || _advancingEpisode || _nextEpisodeTarget is null) return;
        _advancingEpisode = true;
        var target = _nextEpisodeTarget;
        PlayerNextButton.IsEnabled = false;
        SetPlayerStatus("Loading next episode…", busy: true);
        try
        {
            await EndPlaybackAsync(completed: true, returnToDashboard: false);
            _preferredSourceAddonId = _state.SelectedSource?.AddonId;
            _autoStartNextEpisode = true;
            _state.ClearPlayback();
            var reuseSeason = _detailSeason?.Episodes.Any(episode => episode.Id == target.TitleId) == true;
            await OpenEpisodeTargetAsync(target, openSources: true, reuseSeasonContext: reuseSeason);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            SetPlayerStatus(FriendlyError(exception), liveSetting: AutomationLiveSetting.Assertive);
        }
        finally
        {
            _advancingEpisode = false;
            PlayerNextButton.IsEnabled = true;
        }
    }

    private void PlayerAspect_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        _aspectIndex = (_aspectIndex + 1) % 3;
        ApplyPlayerAspect();
        NotePlayerInteraction();
    }

    private void ApplyPlayerAspect()
    {
        string label;
        switch (_aspectIndex)
        {
            case 1:
                PlayerElement.Stretch = Stretch.Fill;
                PlayerAspectIcon.Glyph = "\uE799";
                label = "Fill";
                break;
            case 2:
                PlayerElement.Stretch = Stretch.UniformToFill;
                PlayerAspectIcon.Glyph = "\uE71E";
                label = "Zoom";
                break;
            default:
                PlayerElement.Stretch = Stretch.Uniform;
                PlayerAspectIcon.Glyph = "\uE9A6";
                label = "Fit";
                break;
        }

        ToolTipService.SetToolTip(PlayerAspectButton, $"Video aspect: {label}");
        AutomationProperties.SetName(PlayerAspectButton, $"Video aspect, {label.ToLowerInvariant()}");
    }

    private void PlayerSpeed_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        EnsurePlaybackRateFlyout();
        SetPlaybackRate(_playbackRateIndex, applyToPlayer: false);
        _playbackRateFlyout!.ShowAt(PlayerSpeedButton);
        NotePlayerInteraction();
    }

    private void EnsurePlaybackRateFlyout()
    {
        if (_playbackRateFlyout is not null) return;

        _playbackRateFlyout = new MenuFlyout { Placement = FlyoutPlacementMode.Top };
        _playbackRateItems = new ToggleMenuFlyoutItem[PlaybackRates.Length];
        for (var index = 0; index < PlaybackRates.Length; index++)
        {
            var item = new ToggleMenuFlyoutItem
            {
                Text = PlaybackRateLabel(index),
                Tag = index,
            };
            item.Click += PlaybackRateItem_Click;
            _playbackRateItems[index] = item;
            _playbackRateFlyout.Items.Add(item);
        }
    }

    private void PlaybackRateItem_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked || sender is not ToggleMenuFlyoutItem item || item.Tag is not int index) return;
        SetPlaybackRate(index);
        NotePlayerInteraction();
    }

    private void SetPlaybackRate(int index, bool applyToPlayer = true)
    {
        if ((uint)index >= (uint)PlaybackRates.Length) throw new ArgumentOutOfRangeException(nameof(index));

        _playbackRateIndex = index;
        if (applyToPlayer) _mediaPlayer.PlaybackSession.PlaybackRate = PlaybackRates[index];

        var label = PlaybackRateLabel(index);
        PlayerSpeedIcon.Glyph = index == 2 ? "\uEC57" : "\uEC58";
        ToolTipService.SetToolTip(PlayerSpeedButton, $"Playback speed: {label}");
        AutomationProperties.SetName(PlayerSpeedButton, $"Playback speed, {label[..^1]} times");

        if (_playbackRateItems is null) return;
        for (var itemIndex = 0; itemIndex < _playbackRateItems.Length; itemIndex++)
            _playbackRateItems[itemIndex].IsChecked = itemIndex == index;
    }

    private static string PlaybackRateLabel(int index) => index switch
    {
        0 => "0.5×",
        1 => "0.75×",
        2 => "1×",
        3 => "1.25×",
        4 => "1.5×",
        5 => "2×",
        _ => throw new ArgumentOutOfRangeException(nameof(index)),
    };

    private void PlayerLock_Click(object sender, RoutedEventArgs e)
    {
        var unlocking = _playerControlsLocked;
        SetPlayerControlsLocked(!unlocking, focusTransport: unlocking);
    }

    private void SetPlayerControlsLocked(bool locked, bool focusTransport = false)
    {
        _playerControlsLocked = locked;
        TimelineSlider.IsEnabled = !locked;
        PlayerChrome.Visibility = locked ? Visibility.Collapsed : Visibility.Visible;
        PlayerUnlockButton.Visibility = locked ? Visibility.Visible : Visibility.Collapsed;
        if (locked)
        {
            _chromeTimer.Stop();
            PlayerUnlockButton.Focus(FocusState.Programmatic);
        }
        else
        {
            RevealPlayerChrome(focusTransport);
        }
    }

    private static IEnumerable<DependencyObject> Descendants(DependencyObject root)
    {
        for (var index = 0; index < VisualTreeHelper.GetChildrenCount(root); index++)
        {
            var child = VisualTreeHelper.GetChild(root, index);
            yield return child;
            foreach (var descendant in Descendants(child)) yield return descendant;
        }
    }

    private async Task LoadPlayerContextAsync(long generation)
    {
        UpdateTrackButtonLabels();
        _playbackMarkers = [];
        _activeMarkerIndex = -1;
        _autoSkippedMarkerIndexes.Clear();
        _nextEpisodeTarget = null;
        PlayerNextButton.Visibility = Visibility.Collapsed;
        RemoveMarkerSkipButton();
        var client = _state.Client;
        if (client is null) return;
        try
        {
            if (_effectiveSettings is null && _state.Profile is { Id: var profileId })
                _effectiveSettings = await client.GetEffectiveProfileSettingsAsync(profileId, _state.Token);
            if (!_state.IsCurrent(generation)) return;
        }
        catch (OperationCanceledException) { return; }
        catch (Exception) { }

        try
        {
            if (_detailTarget is { MediaType: "episode", TitleId: Guid episodeId } && _detailSeries is not null && _detailSeason is not null)
            {
                var next = await NextEpisodeResolver.ResolveAsync(
                    _detailSeries,
                    _detailSeason,
                    episodeId,
                    id => client.GetSeasonAsync(id, _detailSeries.MappingProvider, cancellationToken: _state.Token));
                if (_state.IsCurrent(generation) && next is not null)
                {
                    _nextEpisodeTarget = EpisodeTarget(_detailSeries, next);
                    PlayerNextButton.Visibility = Visibility.Visible;
                }
            }
        }
        catch (OperationCanceledException) { return; }
        catch (Exception) { }

        try
        {
            if (_detailTarget is not { MediaType: "episode", SeriesImdbId: { Length: > 0 } imdbId, SeasonNumber: int season, EpisodeNumber: int episode }) return;
            var markers = await client.GetPlaybackMarkersAsync(imdbId, season, episode, _state.Token);
            if (_state.IsCurrent(generation)) _playbackMarkers = markers.Markers;
        }
        catch (OperationCanceledException) { }
        catch (Exception) { }
    }

    private void UpdateMarkerSkipAction(double absolutePosition)
    {
        var settings = _effectiveSettings?.Settings;
        var active = -1;
        if (settings is not null)
        {
            for (var index = 0; index < _playbackMarkers.Count; index++)
            {
                var marker = _playbackMarkers[index];
                if (!double.IsFinite(marker.StartSeconds) || !double.IsFinite(marker.EndSeconds) || marker.StartSeconds < 0 ||
                    marker.EndSeconds <= marker.StartSeconds || absolutePosition < marker.StartSeconds || absolutePosition >= marker.EndSeconds)
                    continue;
                var enabled = marker.Type switch
                {
                    PlaybackMarkerType.Intro => settings.SkipIntroEnabled,
                    PlaybackMarkerType.Recap => settings.SkipRecapEnabled,
                    PlaybackMarkerType.Outro => settings.SkipOutroEnabled,
                    _ => false,
                };
                if (enabled) active = index;
                break;
            }
        }
        if (active < 0)
        {
            _activeMarkerIndex = -1;
            RemoveMarkerSkipButton();
            return;
        }
        var activeMarker = _playbackMarkers[active];
        var autoSkip = activeMarker.Type switch
        {
            PlaybackMarkerType.Intro => _devicePreferences.AutoSkipIntro,
            PlaybackMarkerType.Recap => _devicePreferences.AutoSkipRecap,
            PlaybackMarkerType.Outro => _devicePreferences.AutoSkipOutro,
            _ => false,
        };
        if (autoSkip && _autoSkippedMarkerIndexes.Add(active))
        {
            _mediaPlayer.PlaybackSession.Position = MediaPlaybackPosition(activeMarker.EndSeconds);
            _activeMarkerIndex = -1;
            RemoveMarkerSkipButton();
            return;
        }
        _activeMarkerIndex = active;
        _markerSkipButton ??= CreateMarkerSkipButton();
        _markerSkipButton.Content = activeMarker.Type switch
        {
            PlaybackMarkerType.Intro => "Skip intro",
            PlaybackMarkerType.Recap => "Skip recap",
            PlaybackMarkerType.Outro => "Skip outro",
            _ => "Skip",
        };
        _markerSkipButton.IsEnabled = !_playerControlsLocked;
        if (!PlayerOptionActions.Children.Contains(_markerSkipButton)) PlayerOptionActions.Children.Insert(0, _markerSkipButton);
    }

    private void ResetAutoSkippedMarkersAfterSeek(double targetSeconds) =>
        _autoSkippedMarkerIndexes.RemoveWhere(index =>
            index < 0 || index >= _playbackMarkers.Count || targetSeconds < _playbackMarkers[index].StartSeconds);

    private Button CreateMarkerSkipButton()
    {
        var button = new Button { Style = (Style)Application.Current.Resources["RivunePrimaryButton"] };
        button.Click += (_, _) =>
        {
            if (_playerControlsLocked || _activeMarkerIndex < 0 || _activeMarkerIndex >= _playbackMarkers.Count) return;
            var end = _playbackMarkers[_activeMarkerIndex].EndSeconds;
            _mediaPlayer.PlaybackSession.Position = MediaPlaybackPosition(end);
            NotePlayerInteraction();
            _activeMarkerIndex = -1;
            RemoveMarkerSkipButton();
        };
        return button;
    }

    private void RemoveMarkerSkipButton()
    {
        if (_markerSkipButton is not null) PlayerOptionActions.Children.Remove(_markerSkipButton);
    }

    private sealed record PlaybackAudioChoice(PlaybackSession Session, PlaybackSource Source, int Index, string Label);
    private sealed record PlaybackSubtitleChoice(PlaybackSession Session, string? Id, string Label);

}
