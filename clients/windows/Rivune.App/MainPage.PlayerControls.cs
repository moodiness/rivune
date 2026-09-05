using System.Diagnostics;
using Microsoft.Win32;
using System.Globalization;
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
    private Task<IReadOnlyList<ExternalPlayerApp>>? _externalPlayersTask;
    private readonly PlaybackFailoverController _playbackFailover = new();
    private bool _automaticFailoverInProgress;

    private async Task StartPlaybackFailoverAsync(PlaybackSourceOption selected, CancellationToken cancellationToken)
    {
        var client = _state.Client;
        if (client is null) return;
        var candidates = _sourceOptions
            .OrderByDescending(option => ReferenceEquals(option, selected))
            .Select(option => option.SourceRef)
            .Distinct(StringComparer.Ordinal)
            .Take(8)
            .ToArray();
        if (candidates.Length < 2) return;
        try
        {
            await _playbackFailover.StartAsync(client, candidates, selected.SourceRef, maximumAttempts: 3, cancellationToken);
        }
        catch (OperationCanceledException) { throw; }
        catch
        {
            // Playback remains available without pretending the failover state was synchronized.
        }
    }

    private async Task<bool> TryAutomaticPlaybackFailoverAsync(PlaybackFailoverError error, double failedPositionSeconds)
    {
        var client = _state.Client;
        if (client is null || _automaticFailoverInProgress || !PlaybackFailoverController.CanAdvance(error)) return false;
        _automaticFailoverInProgress = true;
        try
        {
            var next = await _playbackFailover.AdvanceAsync(client, error, failedPositionSeconds, _state.Token);
            await EndPlaybackAsync(completed: false, returnToDashboard: false);
            if (next is not { Status: PlaybackFailoverStatus.Active, CurrentSourceRef: { } sourceRef }) return false;
            var option = _sourceOptions.FirstOrDefault(value => StringComparer.Ordinal.Equals(value.SourceRef, sourceRef));
            if (option is null) return false;
            SetPlayerStatus("Trying the next source…", busy: true, liveSetting: AutomationLiveSetting.Assertive);
            var startSeconds = (int)Math.Clamp(next.PositionSeconds, 0, 86400);
            _state.SelectedSource = option;
            _state.Preparation = await client.PreparePlaybackAsync(sourceRef, startSeconds, _state.Token);
            var session = await client.ResolvePlaybackAsync(sourceRef, _progressTitleId.ToString("D"), startSeconds: startSeconds, cancellationToken: _state.Token);
            var resolved = session.Sources.FirstOrDefault(source => source.Id == session.SelectedSourceId && source.Compatible && source.Url is not null);
            if (resolved?.Url is null)
            {
                await client.StopPlaybackAsync(session.Id, CancellationToken.None);
                return false;
            }
            lock (_endingSync)
            {
                _endingTask = null;
                _playerReturnTask = null;
                _diagnosticPlaybackActive = false;
            }
            _playbackCompleted = false;
            _state.PlaybackSession = session;
            _preferredAudioTrack = session.SelectedAudioTrack;
            _preferredSubtitleId = session.SelectedSubtitleId;
            await ShowPlayerAsync(resolved, startSeconds);
            SetPlayerStatus("Playback continued with another source.", liveSetting: AutomationLiveSetting.Polite);
            return true;
        }
        catch (OperationCanceledException) { return false; }
        catch (RivuneServerException exception) when (exception.StatusCode == 409)
        {
            SetPlayerStatus("Source recovery changed on another device. Choose a source again.", liveSetting: AutomationLiveSetting.Assertive);
            return false;
        }
        catch
        {
            SetPlayerStatus("The next source could not be opened.", liveSetting: AutomationLiveSetting.Assertive);
            return false;
        }
        finally
        {
            _automaticFailoverInProgress = false;
        }
    }
    private async Task ApplyPlaybackAccessibilityAsync(RivuneApiClient client, PlaybackSession session, PlaybackSource source, CancellationToken cancellationToken)
    {
        var preferences = _protocolV22.Accessibility;
        if (preferences is null && _state.Profile is { Id: var profileId })
        {
            try
            {
                preferences = await client.GetProfileAccessibilityPreferencesAsync(profileId, cancellationToken);
                _protocolV22.ApplyAccessibility(preferences);
                ApplyProfileAccessibility(preferences);
            }
            catch (OperationCanceledException) { throw; }
            catch { return; }
        }
        if (preferences is null) return;
        var effective = _profileAccessibilityEffective;
        if (effective is null) return;
        if (preferences.Captions == CaptionsPreference.Off) _preferredSubtitleId = null;
        else if (effective.Value.CaptionsEnabled && _preferredSubtitleId is null)
            _preferredSubtitleId = session.Subtitles.FirstOrDefault(subtitle => subtitle.Default == true)?.Id ?? session.Subtitles.FirstOrDefault()?.Id;
        if (effective.Value.AudioDescription && source.Media is { } media)
        {
            var described = media.AudioTracks.FirstOrDefault(track =>
                track.Title?.Contains("audio description", StringComparison.OrdinalIgnoreCase) == true ||
                track.Title?.Contains("descriptive", StringComparison.OrdinalIgnoreCase) == true);
            if (described is not null) _preferredAudioTrack = described.Index;
        }
    }

    private async Task CancelPlaybackFailoverAsync()
    {
        if (_state.Client is not { } client) return;
        try { await _playbackFailover.CancelAsync(client, CancellationToken.None); }
        catch { }
    }

    private sealed record ExternalPlayerApp(string Name, string ExecutablePath);
    private sealed record ExternalPlayerSpec(string Name, string[] ExecutableNames, string[] RelativePaths);

    private static readonly ExternalPlayerSpec[] ExternalPlayerSpecs =
    [
        new("VLC media player", ["vlc.exe"], [@"VideoLAN\VLC\vlc.exe", @"Programs\VideoLAN\VLC\vlc.exe"]),
        new("mpv", ["mpv.exe"], [@"mpv\mpv.exe", @"Programs\mpv\mpv.exe", @"scoop\apps\mpv\current\mpv.exe"]),
        new("MPC-HC", ["mpc-hc64.exe", "mpc-hc.exe"], [@"MPC-HC\mpc-hc64.exe", @"MPC-HC\mpc-hc.exe", @"K-Lite Codec Pack\MPC-HC64\mpc-hc64.exe"]),
        new("MPC-BE", ["mpc-be64.exe", "mpc-be.exe"], [@"MPC-BE x64\mpc-be64.exe", @"MPC-BE\mpc-be64.exe", @"MPC-BE\mpc-be.exe"]),
        new("PotPlayer", ["PotPlayerMini64.exe", "PotPlayerMini.exe"], [@"DAUM\PotPlayer\PotPlayerMini64.exe", @"DAUM\PotPlayer\PotPlayerMini.exe"]),
        new("Kodi", ["kodi.exe"], [@"Kodi\kodi.exe", @"Programs\Kodi\kodi.exe"]),
        new("Plex", ["Plex.exe", "Plex HTPC.exe", "PlexMediaPlayer.exe"], [@"Plex\Plex\Plex.exe", @"Plex\Plex HTPC\Plex HTPC.exe", @"Plex Media Player\PlexMediaPlayer.exe"]),
    ];

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
        ExternalSourceButton.IsEnabled = false;
        ExternalSourceButton.Visibility = Visibility.Collapsed;
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

    private static bool SupportsExternalPlayer(PlaybackSourceOption source) =>
        source.Mode != PlaybackMode.Youtube &&
        (source.Protocol.Equals("http", StringComparison.OrdinalIgnoreCase) ||
         source.Protocol.Equals("hls", StringComparison.OrdinalIgnoreCase) ||
         source.Protocol.Equals("dash", StringComparison.OrdinalIgnoreCase));

    private async void ExternalSource_Click(object sender, RoutedEventArgs e)
    {
        var selectedSource = _state.SelectedSource;
        var client = _state.Client;
        if (selectedSource is null || client is null || !SupportsExternalPlayer(selectedSource)) return;

        IReadOnlyList<ExternalPlayerApp> players;
        try
        {
            players = await Task.Run(DetectExternalPlayers);
        }
        catch (Exception exception)
        {
            SetSourceFailure(exception);
            return;
        }
        if (players.Count == 0)
        {
            SourceStatus.Text = "No supported external video player is installed.";
            SourceBanner.Severity = InfoBarSeverity.Informational;
            SourceBanner.Message = "Install VLC, mpv, MPC-HC/MPC-BE, PotPlayer, Kodi, or Plex, then try again.";
            SourceBanner.IsOpen = true;
            await ShowDialogAsync(new ContentDialog
            {
                XamlRoot = XamlRoot,
                Title = "No external video player found",
                Content = "Install VLC, mpv, MPC-HC/MPC-BE, PotPlayer, Kodi, or Plex, then try again.",
                CloseButtonText = "Close",
            });
            return;
        }

        var player = await ChooseAsync("Choose an external player", players, value => value.Name);
        if (player is null || !ReferenceEquals(_state.SelectedSource, selectedSource) || !ReferenceEquals(_state.Client, client)) return;

        var generation = _state.Transition(AppPhase.Sources);
        PlaySourceButton.IsEnabled = false;
        ExternalSourceButton.IsEnabled = false;
        DownloadSourceButton.IsEnabled = false;
        SourceProgress.IsActive = true;
        SourceStatus.Text = UiFormat("Opening {0}…", player.Name);
        SourceBanner.IsOpen = false;
        PlaybackSession? session = null;
        try
        {
            var progress = _tracksProgress ? await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token) : null;
            var startSeconds = progress is null
                ? (int?)null
                : PlaybackProgressPolicy.StartSeconds(progress.PositionSeconds, progress.Completed);
            await client.PreparePlaybackAsync(
                selectedSource.SourceRef,
                startSeconds,
                _state.Token,
                externalPlayer: true);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(_state.SelectedSource, selectedSource)) return;
            session = await client.ResolvePlaybackAsync(
                selectedSource.SourceRef,
                _progressTitleId.ToString("D"),
                startSeconds: startSeconds,
                cancellationToken: _state.Token,
                externalPlayer: true);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(_state.SelectedSource, selectedSource)) return;

            var resolved = session.Sources.FirstOrDefault(value =>
                value.Id == session.SelectedSourceId && value.Compatible && value.Url is not null)
                ?? throw new InvalidOperationException("The resolved source has no URL for an external player.");
            var uri = client.ResolveResponseResourceUrl(resolved.Url!);
            if (!File.Exists(player.ExecutablePath))
                throw new InvalidOperationException($"{player.Name} is no longer installed at the detected location.");
            var startInfo = new ProcessStartInfo(player.ExecutablePath) { UseShellExecute = false };
            startInfo.ArgumentList.Add(uri.AbsoluteUri);
            using var process = Process.Start(startInfo)
                ?? throw new InvalidOperationException($"Windows could not start {player.Name}.");
            SourceStatus.Text = UiFormat("Opened in {0}.", player.Name);
            SourceBanner.Severity = InfoBarSeverity.Success;
            SourceBanner.Message = UiFormat("Playback was handed off to {0}.", player.Name);
            SourceBanner.IsOpen = true;
        }
        catch (OperationCanceledException) { }
        catch (RivuneServerException exception) when (exception.Code == "playback_source_expired" && _state.IsCurrent(generation))
        {
            await RefreshExpiredSourcesAsync();
        }
        catch (Exception exception)
        {
            if (_state.IsCurrent(generation)) SetSourceFailure(exception);
        }
        finally
        {
            if (session is not null)
            {
                try { await client.StopPlaybackAsync(session.Id, CancellationToken.None); }
                catch { }
            }
            if (_state.IsCurrent(generation))
            {
                SourceProgress.IsActive = false;
                PlaySourceButton.IsEnabled = _state.Preparation is not null;
                ExternalSourceButton.IsEnabled = SupportsExternalPlayer(selectedSource);
                DownloadSourceButton.IsEnabled = _state.Preparation is not null;
            }
        }
    }

    private static IReadOnlyList<ExternalPlayerApp> DetectExternalPlayers()
    {
        var roots = new[]
        {
            Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles),
            Environment.GetFolderPath(Environment.SpecialFolder.ProgramFilesX86),
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
        }.Where(value => !string.IsNullOrWhiteSpace(value)).Distinct(StringComparer.OrdinalIgnoreCase).ToArray();
        var pathDirectories = (Environment.GetEnvironmentVariable("PATH") ?? string.Empty)
            .Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
        var players = new List<ExternalPlayerApp>();
        foreach (var spec in ExternalPlayerSpecs)
        {
            var executable = FindRegisteredExecutable(spec.ExecutableNames)
                ?? spec.RelativePaths.SelectMany(relative => roots.Select(root => Path.Combine(root, relative))).FirstOrDefault(File.Exists)
                ?? spec.ExecutableNames.SelectMany(name => pathDirectories.Select(directory => Path.Combine(directory, name))).FirstOrDefault(File.Exists);
            if (executable is not null) players.Add(new ExternalPlayerApp(spec.Name, Path.GetFullPath(executable)));
        }
        return players;
    }

    private static string? FindRegisteredExecutable(IReadOnlyList<string> executableNames)
    {
        foreach (var name in executableNames)
        {
            foreach (var hive in new[] { RegistryHive.CurrentUser, RegistryHive.LocalMachine })
            {
                foreach (var view in new[] { RegistryView.Registry64, RegistryView.Registry32 })
                {
                    try
                    {
                        using var baseKey = RegistryKey.OpenBaseKey(hive, view);
                        using var key = baseKey.OpenSubKey($@"SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\{name}");
                        if (key?.GetValue(null) is string value)
                        {
                            var path = Environment.ExpandEnvironmentVariables(value).Trim().Trim('"');
                            if (File.Exists(path)) return path;
                        }
                    }
                    catch (Exception exception) when (exception is UnauthorizedAccessException or IOException or PlatformNotSupportedException or System.Security.SecurityException) { }
                }
            }
        }
        return null;
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
        ExternalSourceButton.IsEnabled = false;
        ExternalSourceButton.Visibility = Visibility.Collapsed;
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
        audioLabel ??= UiText("Default");
        ToolTipService.SetToolTip(PlayerAudioButton, UiFormat("Audio: {0}", audioLabel));
        AutomationProperties.SetName(PlayerAudioButton, UiFormat("Audio track, {0}", audioLabel));

        if (subtitleLabel is null)
        {
            subtitleLabel = UiText("Off");
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
        ToolTipService.SetToolTip(PlayerSubtitlesButton, UiFormat("Subtitles: {0}", subtitleLabel));
        AutomationProperties.SetName(PlayerSubtitlesButton, UiFormat("Subtitles, {0}", subtitleLabel));
    }

    private string AudioTrackLabel(PlaybackMediaTrack track) =>
        $"{track.Language ?? UiText("Unknown language")} · {track.Title ?? track.Codec}{(track.Channels is int channels ? " · " + UiFormat("{0} channels", channels) : string.Empty)}";

    private string SubtitleLabel(PlaybackSubtitle subtitle) =>
        $"{subtitle.Language ?? UiText("Unknown language")}{(subtitle.Forced == true ? " · " + UiText("Forced") : string.Empty)}";

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
            _diagnostics.Record(DiagnosticEventCode.PlaybackFailed);
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

        ToolTipService.SetToolTip(PlayerAspectButton, UiFormat("Video aspect: {0}", UiText(label)));
        AutomationProperties.SetName(PlayerAspectButton, UiFormat("Video aspect, {0}", UiText(label).ToLower(CultureInfo.CurrentCulture)));
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
        ToolTipService.SetToolTip(PlayerSpeedButton, UiFormat("Playback speed: {0}", label));
        AutomationProperties.SetName(PlayerSpeedButton, UiFormat("Playback speed, {0} times", label[..^1]));

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
                    _nextEpisodeTarget = EpisodeTarget(_detailSeries, next, _detailTarget);
                    PlayerNextButton.Visibility = Visibility.Visible;
                }
            }
        }
        catch (OperationCanceledException) { return; }
        catch (Exception) { }

        try
        {
            var target = _detailTarget;
            if (target is not { MediaType: "episode", SeriesImdbId: { Length: > 0 } imdbId, SeasonNumber: int season, EpisodeNumber: int episode } ||
                !MediaIdentity.CanLoadCanonicalMarkers(target)) return;
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
