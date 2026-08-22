using System.Globalization;
using System.Reflection;
using System.Text.Json;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls.Primitives;
using Rivune.App.ViewModels;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Rivune.Windows;
using Windows.System;

using Windows.UI.ViewManagement;
namespace Rivune.App;

public sealed partial class MainPage
{
    private const int SearchPageSize = 24;
    private const int LibraryPageSize = 100;
    private const int FolderPageSize = 48;
    private const double LandscapeCardWidth = 184;
    private const double TvLandscapeCardWidth = 180;
    private const double LandscapeCardAspectRatio = 16d / 9d;
    private const double HeroAspectRatio = 21d / 9d;
    private ViewerTab _selectedViewerTab = ViewerTab.Home;
    private IReadOnlyList<Collection> _viewerCollections = [];
    private IReadOnlyList<AddonCatalogDescriptor> _searchDescriptors = [];
    private Guid? _viewerProfileId;
    private readonly List<MediaTarget> _searchTargets = [];
    private string _searchQuery = string.Empty;
    private int _searchPage;
    private bool _searchHasMore;
    private readonly List<LibraryItem> _libraryItems = [];
    private TitleMediaType? _libraryType;
    private int _libraryPage;
    private int _libraryTotalPages;
    private DateTime _calendarMonth = new(DateTime.Today.Year, DateTime.Today.Month, 1);
    private MediaTarget? _heroTarget;
    private IReadOnlyList<MediaTarget> _heroTargets = [];
    private IReadOnlyList<MediaTarget> _continueWatchingTargets = [];
    private IReadOnlyList<MediaTarget> _recommendationTargets = [];
    private int _heroIndex;
    private readonly DispatcherTimer _heroTimer = new() { Interval = TimeSpan.FromSeconds(8) };
    private bool _heroRotationPaused;
    private readonly UISettings _uiSettings = new();
    private CancellationTokenSource? _heroSlideCancellation;
    private MediaTarget? _detailTarget;
    private TitleReference? _detailReference;
    private readonly Dictionary<Guid, PlaybackProgress?> _episodeProgress = [];
    private Movie? _detailMovie;
    private Series? _detailSeries;
    private Season? _detailSeason;
    private PlaybackProgress? _detailProgress;
    private IReadOnlyList<Episode> _seriesEpisodes = [];
    private bool _seriesWatchStateReady;
    private bool _detailInLibrary;
    private Func<Task>? _detailBackAction;
    private Func<Task>? _detailRetryAction;
    private string? _coordinationActionsFingerprint;
    private int _folderPage;
    private bool _folderHasMore;
    private const int FolderArtworkConcurrency = 4;
    private readonly SemaphoreSlim _folderArtworkGate = new(FolderArtworkConcurrency);
    private readonly Dictionary<FolderArtworkKey, string> _folderArtworkCache = [];
    private readonly Dictionary<FolderArtworkKey, FolderArtworkRequest> _folderArtworkTasks = [];
    private readonly Dictionary<FolderArtworkKey, HomeFolderRequest> _homeFolderTasks = [];

    private readonly List<CollectionItem> _folderItems = [];
    private ResolvedCollectionFolder? _resolvedFolder;
    private Guid? _folderSourceId;
    private string? _folderMediaFilter;
    private bool _catalogDetailLayout;
    private bool _updateCheckInProgress;
    private CancellationTokenSource? _updateOperationCancellation;
    private Task? _updateOperationTask;
    private bool _manualUpdateCheckRequested;
    private ListView? _horizontalDragList;
    private ScrollViewer? _horizontalDragScroller;
    private uint _horizontalDragPointerId;
    private double _horizontalDragStartX;
    private double _horizontalDragStartOffset;
    private bool _horizontalDragActive;
    private static readonly string CurrentAppVersion =
        typeof(App).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?
            .InformationalVersion.Split('+', 2)[0] ?? "0.0.0";

    private void InitializeViewerSurface()
    {
        BuildSettingsCategories();
        ShowViewerTab(ViewerTab.Home);
        SizeChanged += MainPage_SizeChanged;
        _heroTimer.Tick += HeroTimer_Tick;
    }

    private bool DeviceAnimationsEnabled => _devicePreferences.Motion switch
    {
        DeviceMotionPreference.Full => true,
        DeviceMotionPreference.Reduced => false,
        _ => _uiSettings.AnimationsEnabled,
    };

    private void MainPage_SizeChanged(object sender, SizeChangedEventArgs e)
    {
        var horizontalInset = e.NewSize.Width switch
        {
            >= 1200 => 96d,
            >= 840 => 64d,
            >= 600 => 48d,
            _ => 0d,
        };
        var maximumHeroHeight = Math.Max(360, e.NewSize.Height * 0.72);
        HeroPanel.Height = e.NewSize.Width < 600
            ? Math.Clamp(e.NewSize.Height * 0.72, 360, 620)
            : Math.Clamp(
                (e.NewSize.Width - horizontalInset) / HeroAspectRatio,
                Math.Min(400, maximumHeroHeight),
                maximumHeroHeight);
        ApplyDetailLayout(_catalogDetailLayout, e.NewSize.Width);
        ResizeMediaGrid(SearchResults, e.NewSize.Width);
        ResizeMediaGrid(LibraryResults, e.NewSize.Width);
        ApplySourcePaneLayout(e.NewSize.Width, e.NewSize.Height);
        HeroInfoLabel.Text = e.NewSize.Width < 600
            ? UiText("Details", "Détails")
            : UiText("Info", "Plus d’infos");
    }

    private void ApplySourcePaneLayout(double viewportWidth, double viewportHeight)
    {
        if (viewportWidth < 600)
        {
            SourcePane.Width = double.NaN;
            SourcePane.MaxWidth = 600;
            SourcePane.Height = double.NaN;
            SourcePane.MaxHeight = 680;
            SourcePane.HorizontalAlignment = HorizontalAlignment.Stretch;
            SourcePane.VerticalAlignment = VerticalAlignment.Stretch;
            SourcePane.Margin = new Thickness(12);
            SourcePane.Padding = new Thickness(0);
            return;
        }

        var horizontalMargin = viewportWidth >= 1200 ? 48d : 24d;
        var availableWidth = Math.Max(0, viewportWidth - horizontalMargin * 2);
        var maximumWidth = viewportWidth >= 2000 ? 840d : viewportWidth >= 1200 ? 680d : 440d;
        var availableHeight = Math.Max(0, viewportHeight - 64);
        SourcePane.Width = Math.Min(maximumWidth, Math.Max(440, availableWidth * 0.365));
        SourcePane.MaxWidth = maximumWidth;
        SourcePane.Height = Math.Min(1160, Math.Min(availableHeight, viewportHeight * 0.84));
        SourcePane.MaxHeight = availableHeight;
        SourcePane.HorizontalAlignment = HorizontalAlignment.Right;
        SourcePane.VerticalAlignment = VerticalAlignment.Center;
        SourcePane.Margin = new Thickness(horizontalMargin, 32, horizontalMargin, 32);
        SourcePane.Padding = new Thickness(0);
    }

    private void ServerAddressBox_TextChanged(object sender, TextChangedEventArgs e)
    {
        ServerError.IsOpen = false;
        ServerSupportText.Foreground = (Brush)Application.Current.Resources["RivuneMutedTextBrush"];
        ConnectButton.IsEnabled = !string.IsNullOrWhiteSpace(ServerAddressBox.Text);
        ConnectButtonLabel.Text = "Continue";
    }

    private async void CheckUpdates_Click(object sender, RoutedEventArgs e) =>
        await CheckForUpdatesAsync();

    private Task CheckForUpdatesAsync() => CheckForUpdatesAsync(automatic: false);

    private Task CheckForUpdatesAsync(bool automatic)
    {
        if (!automatic) _manualUpdateCheckRequested = true;
        if (automatic && !AppUpdateChecker.AutomaticCheckIsDue(_devicePreferences.LastSuccessfulUpdateCheckAt, DateTimeOffset.UtcNow))
            return Task.CompletedTask;
        if (_updateOperationTask is { IsCompleted: false } operation) return operation;
        var cancellation = new CancellationTokenSource();
        _updateOperationCancellation = cancellation;
        return _updateOperationTask = CheckForUpdatesCoreAsync(cancellation, automatic);
    }


    internal Task RunAutomaticUpdateCheckAsync() => CheckForUpdatesAsync(automatic: true);

    private async Task CheckForUpdatesCoreAsync(CancellationTokenSource cancellation, bool automatic)
    {
        if (_updateCheckInProgress) return;
        _updateCheckInProgress = true;
        CheckUpdatesButton.IsEnabled = false;
        try
        {
            var result = await AppUpdateChecker.CheckAsync(CurrentAppVersion, cancellation.Token);
            var checkedAt = DateTimeOffset.UtcNow;
            await RecordSuccessfulUpdateCheckAsync(checkedAt, CancellationToken.None);
            if (_closed) return;
            if (!result.IsUpdateAvailable)
            {
                if (automatic && !_manualUpdateCheckRequested) return;
                var comparison = AppUpdateChecker.CompareSemanticVersions(
                    result.CurrentVersion,
                    result.LatestVersion);
                var message = comparison == 0
                    ? $"Rivune {result.CurrentVersion} is the latest public release."
                    : $"This Rivune {result.CurrentVersion} build is newer than the latest public release ({result.LatestVersion}).";
                await ShowUpdateDialogAsync("Rivune is up to date", message);
                _manualUpdateCheckRequested = false;
                return;
            }

            var available = new ContentDialog
            {
                XamlRoot = XamlRoot,
                Title = $"Rivune {result.LatestVersion} is available",
                Content = $"You are using Rivune {result.CurrentVersion}. Download the unsigned portable {result.Package.FileName} from the exact GitHub Release, verify its size, SHA-256, and ProductVersion, then close and restart Rivune to replace this executable? This does not provide an Authenticode publisher guarantee.",
                PrimaryButtonText = "Download update",
                CloseButtonText = "Not now",
                DefaultButton = ContentDialogButton.Primary,
            };
            var decision = await ShowDialogAsync(available);
            _manualUpdateCheckRequested = false;
            if (decision != ContentDialogResult.Primary || _closed) return;

            var downloading = new ContentDialog
            {
                XamlRoot = XamlRoot,
                Title = $"Downloading Rivune {result.LatestVersion}",
                Content = $"Downloading {result.Package.FileName} over HTTPS and verifying its exact size, SHA-256, and ProductVersion before any update is started.",
            };
            var downloadingOpened = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            downloading.Opened += (_, _) => downloadingOpened.TrySetResult();
            var downloadingOperation = ShowDialogAsync(downloading);
            var firstDownloadingEvent = await Task.WhenAny(downloadingOpened.Task, downloadingOperation);
            if (ReferenceEquals(firstDownloadingEvent, downloadingOperation))
            {
                await downloadingOperation;
                if (_closed) return;
                throw new InvalidOperationException("The update progress dialog closed before the download started.");
            }
            string? updatePath = null;
            try
            {
                var processPath = Environment.ProcessPath ??
                    throw new InvalidOperationException("Windows did not report the running Rivune executable path. The current app is unchanged.");
                var targetFileName = Path.GetFileName(processPath);
                var updateDirectory = Path.Combine(
                    Path.GetTempPath(),
                    "Rivune",
                    "updates",
                    Guid.NewGuid().ToString("N"));
                Directory.CreateDirectory(updateDirectory);
                updatePath = Path.Combine(updateDirectory, targetFileName);
                await using (var destination = new FileStream(
                                 updatePath,
                                 FileMode.CreateNew,
                                 FileAccess.Write,
                                 FileShare.None,
                                 81920,
                                 FileOptions.Asynchronous | FileOptions.SequentialScan))
                {
                    await AppUpdateChecker.DownloadPackageAsync(result.Package, destination, cancellation.Token);
                }
                PortableAppUpdate.VerifyProductVersion(updatePath, result.LatestVersion);

                HideDialog(downloading);
                await downloadingOperation;
                if (_closed)
                {
                    await DeleteTemporaryUpdateAsync(updatePath);
                    return;
                }
                PortableAppUpdate.StartHandoff(
                    updatePath,
                    processPath,
                    Environment.ProcessId,
                    result.Package.Size,
                    result.Package.Sha256,
                    result.LatestVersion);
                App.MainWindow.Close();
                return;
            }
            catch (Exception exception)
            {
                HideDialog(downloading);
                await downloadingOperation;
                await DeleteTemporaryUpdateAsync(updatePath);
                if (!_closed)
                {
                    var message = exception switch
                    {
                        HttpRequestException => "GitHub could not be reached while downloading the update. Check the network connection and try again.",
                        OperationCanceledException => "The update download timed out. Check the network connection and try again.",
                        _ => exception.Message,
                    };
                    await ShowUpdateDialogAsync("Could not apply update", message);
                }
            }
        }
        catch (OperationCanceledException) when (_closed)
        {
        }
        catch (Exception exception)
        {
            if (!_closed && (!automatic || _manualUpdateCheckRequested))
            {
                var message = exception is HttpRequestException
                    ? "GitHub could not be reached. Check the network connection and try again."
                    : exception.Message;
                await ShowUpdateDialogAsync("Could not check for updates", message);
                _manualUpdateCheckRequested = false;
            }
        }
        finally
        {
            _updateCheckInProgress = false;
            if (!_closed) CheckUpdatesButton.IsEnabled = true;
            if (ReferenceEquals(_updateOperationCancellation, cancellation))
            {
                _manualUpdateCheckRequested = false;
                _updateOperationCancellation = null;
                cancellation.Dispose();
            }
        }
    }

    private static async Task DeleteTemporaryUpdateAsync(string? updatePath)
    {
        if (updatePath is null) return;
        await PortableAppUpdate.DeleteTemporarySourceAsync(updatePath);
    }

    private async Task RecordSuccessfulUpdateCheckAsync(DateTimeOffset checkedAt, CancellationToken cancellationToken)
    {
        _devicePreferences = _devicePreferences with { LastSuccessfulUpdateCheckAt = checkedAt.ToUniversalTime() };
        if (_devicePreferencesStore is null) return;
        try
        {
            await _devicePreferencesStore.UpdateAsync(
                preferences => preferences with { LastSuccessfulUpdateCheckAt = checkedAt.ToUniversalTime() },
                cancellationToken);
            _devicePreferences = _devicePreferencesStore.Snapshot;
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch (Exception)
        {
            // A local preference write failure must not block a verified update check.
        }
    }

    private async Task<ContentDialogResult> ShowDialogAsync(ContentDialog dialog)
    {
        await _dialogGate.WaitAsync();
        try
        {
            if (_closed) return ContentDialogResult.None;
            _activeDialog = dialog;
            return await dialog.ShowAsync();
        }
        finally
        {
            if (ReferenceEquals(_activeDialog, dialog)) _activeDialog = null;
            _dialogGate.Release();
        }
    }

    private async Task ShowUpdateDialogAsync(string title, string message)
    {
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = title,
            Content = message,
            CloseButtonText = "Close",
        };
        await ShowDialogAsync(dialog);
    }

    private void DismissDialogForShutdown() => HideDialog(_activeDialog);

    private static void HideDialog(ContentDialog? dialog)
    {
        if (dialog is null) return;
        try { dialog.Hide(); }
        catch (InvalidOperationException) { }
    }

    private async void ViewerNav_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not FrameworkElement { Tag: string destination } ||
            !Enum.TryParse<ViewerTab>(destination, ignoreCase: true, out var tab)) return;
        await SelectViewerTabAsync(tab);
    }

    private async Task SelectViewerTabAsync(ViewerTab tab)
    {
        var generation = _state.Transition(AppPhase.Catalogue);
        _selectedViewerTab = tab;
        ShowViewerTab(tab);
        switch (tab)
        {
            case ViewerTab.Home:
                await LoadHomeAsync(generation);
                break;
            case ViewerTab.Search when _searchDescriptors.Count == 0:
                await LoadSearchDescriptorsAsync();
                break;
            case ViewerTab.Library when _libraryPage == 0:
                await LoadLibraryAsync(reset: true);
                break;
            case ViewerTab.Calendar:
                await LoadCalendarAsync();
                break;
        }
    }

    private void ShowViewerTab(ViewerTab tab)
    {
        HomeView.Visibility = tab == ViewerTab.Home ? Visibility.Visible : Visibility.Collapsed;
        SearchView.Visibility = tab == ViewerTab.Search ? Visibility.Visible : Visibility.Collapsed;
        LibraryView.Visibility = tab == ViewerTab.Library ? Visibility.Visible : Visibility.Collapsed;
        CalendarView.Visibility = tab == ViewerTab.Calendar ? Visibility.Visible : Visibility.Collapsed;
        DashboardHeading.Text = ViewerTabLabel(tab);
        HomeNav.IsChecked = BottomHomeNav.IsChecked = tab == ViewerTab.Home;
        SearchNav.IsChecked = BottomSearchNav.IsChecked = tab == ViewerTab.Search;
        LibraryNav.IsChecked = BottomLibraryNav.IsChecked = tab == ViewerTab.Library;
        CalendarNav.IsChecked = BottomCalendarNav.IsChecked = tab == ViewerTab.Calendar;
        if (tab == ViewerTab.Home && _heroTargets.Count > 1 && DeviceAnimationsEnabled && !_heroRotationPaused) _heroTimer.Start();
        else _heroTimer.Stop();
    }

    private async void DashboardRetry_Click(object sender, RoutedEventArgs e)
    {
        var generation = _state.Transition(AppPhase.Catalogue);
        await LoadHomeAsync(generation);
    }

    private async void SearchRetry_Click(object sender, RoutedEventArgs e) => await SearchAsync(reset: _searchPage == 0);

    private async void LibraryRetry_Click(object sender, RoutedEventArgs e) => await LoadLibraryAsync(reset: _libraryPage == 0);

    private async void CalendarRetry_Click(object sender, RoutedEventArgs e) => await LoadCalendarAsync();

    private async void DetailRetry_Click(object sender, RoutedEventArgs e)
    {
        DetailRetryButton.Visibility = Visibility.Collapsed;
        if (_detailRetryAction is null) return;
        try { await _detailRetryAction(); }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            DetailBanner.Severity = InfoBarSeverity.Error;
            DetailBanner.Message = FriendlyError(exception);
            DetailBanner.IsOpen = true;
            DetailRetryButton.Visibility = Visibility.Visible;
            DetailStatus.Visibility = Visibility.Visible;
        }
    }

    private async Task LoadHomeAsync(long generation)
    {
        DashboardBanner.IsOpen = false;
        DashboardRetryButton.Visibility = Visibility.Collapsed;
        DashboardProgress.IsActive = true;
        DashboardLoadingStatus.Visibility = Visibility.Visible;
        var client = _state.Client;
        if (client is null)
        {
            DashboardProgress.IsActive = false;
            DashboardLoadingStatus.Visibility = Visibility.Collapsed;
            return;
        }
        var cancellationToken = _state.Token;
        var collectionsTask = client.GetCollectionsAsync(cancellationToken);
        var continueWatchingTask = GetHomeContinueWatchingAsync(client, cancellationToken);
        var recommendationsTask = GetHomeRecommendationsAsync(client, cancellationToken);
        try
        {
            var collections = (await collectionsTask)
                .Select((collection, index) => (collection, index))
                .OrderByDescending(value => value.collection.PinToTop)
                .ThenBy(value => value.collection.Position)
                .ThenBy(value => value.index)
                .Select(value => value.collection)
                .ToArray();
            if (!HomeRequestCurrent(client, generation)) return;

            var heroTargetsTask = LoadHeroTargetsAsync(client, collections, generation, cancellationToken);

            var continueWatching = await continueWatchingTask;
            if (!HomeRequestCurrent(client, generation)) return;
            IReadOnlyList<MediaTarget> continueTargets = continueWatching.Failed
                ? _continueWatchingTargets
                : continueWatching.Page?.Items.Select(item => item.ToMediaTarget()).ToArray() ?? [];
            var recommendations = await recommendationsTask;
            if (!HomeRequestCurrent(client, generation)) return;
            IReadOnlyList<MediaTarget> recommendationTargets = recommendations.Failed
                ? []
                : recommendations.Page?.Items.Select(item => item.ToMediaTarget()).ToArray() ?? [];


            _viewerCollections = collections;
            _continueWatchingTargets = continueTargets;
            _recommendationTargets = recommendationTargets;
            RebuildHomeSections(collections, continueTargets, recommendationTargets);

            if (continueWatching.Failed || recommendations.Failed)
            {
                DashboardBanner.Severity = InfoBarSeverity.Warning;
                DashboardBanner.Message = (continueWatching.Failed, recommendations.Failed) switch
                {
                    (true, true) => UiText(
                        "Continue watching and recommendations could not be loaded. Your collections are still available.",
                        "Les sections Continuer à regarder et Recommandations n’ont pas pu être chargées. Vos collections restent disponibles."),
                    (true, false) => UiText(
                        "Continue watching could not be loaded. Your collections are still available.",
                        "La section Continuer à regarder n’a pas pu être chargée. Vos collections restent disponibles."),
                    _ => UiText(
                        "Recommendations could not be loaded. Continue watching and your collections are still available.",
                        "Les recommandations n’ont pas pu être chargées. Continuer à regarder et vos collections restent disponibles."),
                };
                DashboardBanner.IsOpen = true;
                DashboardRetryButton.Visibility = Visibility.Visible;
            }

            var heroTargets = await heroTargetsTask;
            if (!HomeRequestCurrent(client, generation)) return;

            await PresentHomeHeroAsync(client, heroTargets, generation);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!HomeRequestCurrent(client, generation)) return;
            DashboardBanner.Severity = InfoBarSeverity.Error;
            DashboardBanner.Message = FriendlyError(exception);
            DashboardBanner.IsOpen = true;
            DashboardRetryButton.Visibility = Visibility.Visible;
        }
        finally
        {
            if (HomeRequestCurrent(client, generation))
            {
                DashboardProgress.IsActive = false;
                DashboardLoadingStatus.Visibility = Visibility.Collapsed;
            }
        }
    }

    private void RebuildHomeSections(
        IReadOnlyList<Collection> collections,
        IReadOnlyList<MediaTarget> continueTargets,
        IReadOnlyList<MediaTarget> recommendationTargets)
    {
        DashboardSections.Children.Clear();
        if (continueTargets.Count > 0)
            DashboardSections.Children.Add(CreateMediaRow(UiText("Continue watching", "Continuer à regarder"), continueTargets, landscape: true));
        if (recommendationTargets.Count > 0)
            DashboardSections.Children.Add(CreateMediaRow(UiText("Recommended for you", "Recommandé pour vous"), recommendationTargets, landscape: false));
        foreach (var collection in collections)
            DashboardSections.Children.Add(CreateCollectionRow(collection));
        DashboardEmpty.Visibility = collections.Count == 0 && continueTargets.Count == 0 && recommendationTargets.Count == 0
            ? Visibility.Visible
            : Visibility.Collapsed;
    }

    private bool HomeRequestCurrent(RivuneApiClient client, long generation) =>
        _state.IsCurrent(generation) && ReferenceEquals(client, _state.Client);

    private async Task<HomeContinueWatchingResult> GetHomeContinueWatchingAsync(RivuneApiClient client, CancellationToken cancellationToken)
    {
        try
        {
            return new HomeContinueWatchingResult(
                await client.GetContinueWatchingAsync(limit: 24, cancellationToken),
                Failed: false);
        }
        catch (OperationCanceledException) { return new HomeContinueWatchingResult(null, Failed: false); }
        catch { return new HomeContinueWatchingResult(null, Failed: true); }
    }

    private async Task<HomeRecommendationResult> GetHomeRecommendationsAsync(RivuneApiClient client, CancellationToken cancellationToken)
    {
        if (!LocalRecommendationsAvailable) return new HomeRecommendationResult(null, Failed: false);
        try
        {
            return new HomeRecommendationResult(
                await client.GetLocalRecommendationsAsync(limit: 24, cancellationToken),
                Failed: false);
        }
        catch (OperationCanceledException) { return new HomeRecommendationResult(null, Failed: false); }
        catch { return new HomeRecommendationResult(null, Failed: true); }
    }

    private async Task PresentHomeHeroAsync(
        RivuneApiClient client,
        IReadOnlyList<MediaTarget> targets,
        long generation)
    {
        if (!HomeRequestCurrent(client, generation)) return;
        _heroTimer.Stop();
        _heroSlideCancellation?.Cancel();
        _heroTargets = targets;
        _heroIndex = 0;
        _heroRotationPaused = false;
        if (targets.Count == 0)
        {
            _heroTarget = null;
            HeroImage.Source = null;
            HeroImage.Opacity = 0;
            HeroLogo.Source = null;
            HeroLogo.Opacity = 0;
            HeroPanel.Visibility = Visibility.Collapsed;
            return;
        }
        await ShowHeroAsync(targets[0], generation);
        if (HomeRequestCurrent(client, generation) && targets.Count > 1 && DeviceAnimationsEnabled && !_heroRotationPaused)
            _heroTimer.Start();
    }

    private async Task<IReadOnlyList<MediaTarget>> LoadHeroTargetsAsync(
        RivuneApiClient client,
        IReadOnlyList<Collection> collections,
        long generation,
        CancellationToken cancellationToken)
    {
        var pending = new List<(Collection Collection, Guid FolderId, FolderArtworkKey Key, Task<ResolvedCollectionFolder?> Task)>();
        var candidateKeys = new HashSet<FolderArtworkKey>();
        foreach (var collection in collections.Where(value => value.HeroEnabled))
        {
            foreach (var folder in collection.Folders)
            {
                if (folder.Id is not Guid folderId) continue;
                var key = new FolderArtworkKey(collection.Id, folderId);
                if (!candidateKeys.Add(key)) continue;
                var task = ResolveHomeFolderAsync(
                    client,
                    collection.Id,
                    folderId,
                    generation,
                    cancellationToken);
                pending.Add((collection, folderId, key, task));
                _homeFolderTasks[key] = new HomeFolderRequest(generation, task);
            }
        }

        ResolvedCollectionFolder?[] results;
        try
        {
            results = await Task.WhenAll(pending.Select(entry => entry.Task));
        }
        finally
        {
            foreach (var candidate in pending)
            {
                if (_homeFolderTasks.TryGetValue(candidate.Key, out var current) &&
                    current.Generation == generation &&
                    ReferenceEquals(current.Task, candidate.Task))
                    _homeFolderTasks.Remove(candidate.Key);
            }
        }
        if (!HomeRequestCurrent(client, generation)) return [];

        for (var index = 0; index < pending.Count; index++)
        {
            var result = results[index];
            if (result is null) continue;
            var artworkUrl = FolderArtworkUrl(result);
            if (!string.IsNullOrWhiteSpace(artworkUrl))
                _folderArtworkCache[pending[index].Key] = artworkUrl;
        }

        var targets = new List<MediaTarget>(12);
        var identities = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        for (var index = 0; index < pending.Count && targets.Count < 12; index++)
        {
            var candidate = pending[index];
            var result = results[index];
            if (result is null) continue;
            foreach (var item in result.Items)
            {
                var target = item.ToMediaTarget() with
                {
                    BackgroundUrl = item.BackgroundUrl
                        ?? result.Folder.HeroBackdropUrl
                        ?? candidate.Collection.BackdropImageUrl
                        ?? item.PosterUrl,
                    LogoUrl = item.LogoUrl ?? result.Folder.TitleLogoUrl,
                };
                if (!identities.Add(target.Identity())) continue;
                targets.Add(target);
                if (targets.Count == 12) break;
            }
        }
        return targets;
    }

    private async Task<ResolvedCollectionFolder?> ResolveHomeFolderAsync(
        RivuneApiClient client,
        Guid collectionId,
        Guid folderId,
        long generation,
        CancellationToken cancellationToken)
    {
        var entered = false;
        try
        {
            await _folderArtworkGate.WaitAsync(cancellationToken);
            entered = true;
            var result = await client.ResolveCollectionFolderAsync(
                collectionId,
                folderId,
                page: 1,
                limit: 12,
                language: MetadataLanguage(),
                region: EffectiveMetadataRegion(),
                cancellationToken: cancellationToken);
            return HomeRequestCurrent(client, generation) ? result : null;
        }
        catch (OperationCanceledException) { throw; }
        catch { return null; }
        finally
        {
            if (entered) _folderArtworkGate.Release();
        }
    }

    private async Task ShowHeroAsync(MediaTarget target, long generation)
    {
        _heroSlideCancellation?.Cancel();
        _heroSlideCancellation?.Dispose();
        _heroSlideCancellation = CancellationTokenSource.CreateLinkedTokenSource(_state.Token);
        var cancellationToken = _heroSlideCancellation.Token;
        _heroTarget = target;
        for (var index = 0; index < _heroTargets.Count; index++)
        {
            if (ReferenceEquals(_heroTargets[index], target) || _heroTargets[index] == target)
            {
                _heroIndex = index;
                break;
            }
        }
        HeroTitle.Text = target.Title;
        HeroTitle.Visibility = Visibility.Visible;
        HeroMetadata.Text = string.Join(" · ", new[] { target.ReleaseInfo, MediaTypeLabel(target.MediaType) }.Where(value => !string.IsNullOrWhiteSpace(value)));
        HeroDescription.Text = target.Description ?? string.Empty;
        HeroPlayButton.Visibility = target.MediaType.Equals("series", StringComparison.OrdinalIgnoreCase) ? Visibility.Collapsed : Visibility.Visible;
        HeroCarouselControls.Visibility = _heroTargets.Count > 1 ? Visibility.Visible : Visibility.Collapsed;
        HeroRotationButton.Visibility = DeviceAnimationsEnabled ? Visibility.Visible : Visibility.Collapsed;
        HeroPlayLabel.Text = UiText("Play", "Lire");
        HeroInfoLabel.Text = ActualWidth < 600 ? UiText("Details", "Détails") : UiText("Info", "Plus d’infos");
        HeroRotationButton.Content = _heroRotationPaused ? UiText("Resume", "Reprendre") : UiText("Pause", "Pause");
        AutomationProperties.SetName(HeroRotationButton, _heroRotationPaused ? UiText("Resume featured title rotation", "Reprendre la rotation des titres à la une") : UiText("Pause featured title rotation", "Mettre en pause la rotation des titres à la une"));
        HeroPosition.Text = _heroTargets.Count > 0 ? HeroPositionLabel(_heroIndex + 1, _heroTargets.Count) : string.Empty;
        AutomationProperties.SetName(HeroPanel, UsesFrenchInterface ? $"Titre à la une {_heroIndex + 1} sur {_heroTargets.Count} : {target.Title}" : $"Featured title {_heroIndex + 1} of {_heroTargets.Count}: {target.Title}");
        AutomationProperties.SetName(HeroLogo, target.Title);
        AutomationProperties.SetName(HeroPlayButton, UsesFrenchInterface ? $"Lire {target.Title}" : $"Play {target.Title}");
        AutomationProperties.SetName(HeroInfoButton, UsesFrenchInterface ? $"Informations sur {target.Title}" : $"Information about {target.Title}");
        HeroPanel.Visibility = Visibility.Visible;
        HeroImage.Source = null;
        HeroImage.Opacity = 0;
        HeroLogo.Source = null;
        HeroLogo.Opacity = 0;
        HeroLogo.Visibility = Visibility.Collapsed;

        var artwork = target.BackgroundUrl ?? target.PosterUrl;
        var artworkTask = string.IsNullOrWhiteSpace(artwork)
            ? Task.FromResult(false)
            : LoadArtworkAsync(HeroImage, artwork, generation, cancellationToken);
        var logoTask = Task.FromResult(false);
        if (!string.IsNullOrWhiteSpace(target.LogoUrl))
        {
            HeroLogo.Visibility = Visibility.Visible;
            logoTask = LoadArtworkAsync(HeroLogo, target.LogoUrl, generation, cancellationToken);
        }
        await artworkTask;
        var logoLoaded = await logoTask;
        if (cancellationToken.IsCancellationRequested || !_state.IsCurrent(generation)) return;
        HeroLogo.Visibility = logoLoaded ? Visibility.Visible : Visibility.Collapsed;
        HeroTitle.Visibility = logoLoaded ? Visibility.Collapsed : Visibility.Visible;
    }

    private async void HeroPrevious_Click(object sender, RoutedEventArgs e)
    {
        SetHeroRotationPaused(true);
        await MoveHeroAsync(-1);
    }

    private async void HeroNext_Click(object sender, RoutedEventArgs e)
    {
        SetHeroRotationPaused(true);
        await MoveHeroAsync(1);
    }

    private void HeroRotation_Click(object sender, RoutedEventArgs e) => SetHeroRotationPaused(!_heroRotationPaused);

    private void HeroPanel_PointerEntered(object sender, PointerRoutedEventArgs e) => SetHeroRotationPaused(true);

    private void HeroPanel_GotFocus(object sender, RoutedEventArgs e) => SetHeroRotationPaused(true);

    private void SetHeroRotationPaused(bool paused)
    {
        _heroRotationPaused = paused;
        _heroTimer.Stop();
        HeroRotationButton.Content = paused ? UiText("Resume", "Reprendre") : UiText("Pause", "Pause");
        AutomationProperties.SetName(HeroRotationButton, paused ? UiText("Resume featured title rotation", "Reprendre la rotation des titres à la une") : UiText("Pause featured title rotation", "Mettre en pause la rotation des titres à la une"));
        if (!paused && _heroTargets.Count > 1 && DashboardView.Visibility == Visibility.Visible && HomeView.Visibility == Visibility.Visible && DeviceAnimationsEnabled)
            _heroTimer.Start();
    }

    private async void HeroTimer_Tick(object? sender, object e)
    {
        _heroTimer.Stop();
        if (!DeviceAnimationsEnabled || _heroRotationPaused) return;
        await MoveHeroAsync(1);
    }

    private async Task MoveHeroAsync(int delta)
    {
        if (_heroTargets.Count < 2 || DashboardView.Visibility != Visibility.Visible || HomeView.Visibility != Visibility.Visible) return;
        _heroTimer.Stop();
        _heroIndex = (_heroIndex + delta + _heroTargets.Count) % _heroTargets.Count;
        await ShowHeroAsync(_heroTargets[_heroIndex], _state.GenerationId);
        if (DashboardView.Visibility == Visibility.Visible && HomeView.Visibility == Visibility.Visible && DeviceAnimationsEnabled && !_heroRotationPaused)
            _heroTimer.Start();
    }


    private FrameworkElement CreateCollectionRow(Collection collection)
    {
        var section = new StackPanel { Spacing = 12 };
        var heading = new Grid { MinHeight = 48, ColumnSpacing = 16 };
        heading.ColumnDefinitions.Add(new ColumnDefinition());
        heading.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        heading.Children.Add(new TextBlock
        {
            Text = collection.Title,
            Style = (Style)Application.Current.Resources["RivuneTitleLargeTextStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        });
        var viewAll = new Button
        {
            Content = UiText("View all  ›", "Tout afficher  ›"),
            Style = (Style)Application.Current.Resources["RivuneTextButton"],
            Tag = collection,
        };
        viewAll.Click += Collection_Click;
        Grid.SetColumn(viewAll, 1);
        heading.Children.Add(viewAll);
        section.Children.Add(heading);
        if (collection.Folders.Count == 0)
        {
            section.Children.Add(CreateEmptyState(
                UiText("This collection has no folders yet.", "Cette collection ne contient encore aucun dossier."),
                UiText("Add folders from the Rivune server to start browsing.", "Ajoutez des dossiers depuis le serveur Rivune pour commencer à naviguer.")));
            return section;
        }
        var row = HorizontalList();
        foreach (var folder in collection.Folders) row.Items.Add(CreateFolderCard(collection, folder));
        section.Children.Add(row);
        return section;
    }

    private ListView HorizontalList()
    {
        var list = new ListView
        {
            SelectionMode = ListViewSelectionMode.None,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            ItemsPanel = (ItemsPanelTemplate)Microsoft.UI.Xaml.Markup.XamlReader.Load(
                "<ItemsPanelTemplate xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'><ItemsStackPanel Orientation='Horizontal'/></ItemsPanelTemplate>"),
        };
        ScrollViewer.SetHorizontalScrollMode(list, ScrollMode.Enabled);
        ScrollViewer.SetHorizontalScrollBarVisibility(list, ScrollBarVisibility.Hidden);
        ScrollViewer.SetVerticalScrollMode(list, ScrollMode.Disabled);
        ScrollViewer.SetVerticalScrollBarVisibility(list, ScrollBarVisibility.Disabled);
        ScrollViewer.SetIsHorizontalRailEnabled(list, true);
        list.AddHandler(UIElement.PointerPressedEvent, new PointerEventHandler(HorizontalList_PointerPressed), handledEventsToo: true);
        list.AddHandler(UIElement.PointerMovedEvent, new PointerEventHandler(HorizontalList_PointerMoved), handledEventsToo: true);
        list.AddHandler(UIElement.PointerReleasedEvent, new PointerEventHandler(HorizontalList_PointerReleased), handledEventsToo: true);
        list.AddHandler(UIElement.PointerCaptureLostEvent, new PointerEventHandler(HorizontalList_PointerCaptureLost), handledEventsToo: true);
        list.AddHandler(UIElement.PointerWheelChangedEvent, new PointerEventHandler(HorizontalList_PointerWheelChanged), handledEventsToo: true);
        list.AddHandler(UIElement.PreviewKeyDownEvent, new KeyEventHandler(HorizontalList_KeyDown), handledEventsToo: true);
        list.ContainerContentChanging += DisableContainerFocus;
        return list;
    }

    private void HorizontalList_PointerPressed(object sender, PointerRoutedEventArgs e)
    {
        if (sender is not ListView list || e.Pointer.PointerDeviceType != global::Microsoft.UI.Input.PointerDeviceType.Mouse) return;
        var point = e.GetCurrentPoint(list);
        if (!point.Properties.IsLeftButtonPressed) return;
        _horizontalDragList = list;
        _horizontalDragScroller = Descendants(list).OfType<ScrollViewer>().FirstOrDefault();
        _horizontalDragPointerId = e.Pointer.PointerId;
        _horizontalDragStartX = point.Position.X;
        _horizontalDragStartOffset = _horizontalDragScroller?.HorizontalOffset ?? 0;
        _horizontalDragActive = false;
    }

    private void HorizontalList_PointerMoved(object sender, PointerRoutedEventArgs e)
    {
        if (sender is not ListView list || !ReferenceEquals(list, _horizontalDragList) ||
            e.Pointer.PointerId != _horizontalDragPointerId || _horizontalDragScroller is null) return;
        var point = e.GetCurrentPoint(list);
        if (!point.Properties.IsLeftButtonPressed)
        {
            ResetHorizontalDrag();
            return;
        }
        var delta = point.Position.X - _horizontalDragStartX;
        if (!_horizontalDragActive && Math.Abs(delta) < 6) return;
        if (!_horizontalDragActive)
        {
            _horizontalDragActive = true;
            list.CapturePointer(e.Pointer);
        }
        _horizontalDragScroller.ChangeView(Math.Max(0, _horizontalDragStartOffset - delta), null, null, disableAnimation: true);
        e.Handled = true;
    }

    private void HorizontalList_PointerReleased(object sender, PointerRoutedEventArgs e)
    {
        if (sender is not ListView list || !ReferenceEquals(list, _horizontalDragList) || e.Pointer.PointerId != _horizontalDragPointerId) return;
        var wasDragging = _horizontalDragActive;
        if (wasDragging) list.ReleasePointerCapture(e.Pointer);
        ResetHorizontalDrag();
        if (wasDragging) e.Handled = true;
    }

    private void HorizontalList_PointerCaptureLost(object sender, PointerRoutedEventArgs e) => ResetHorizontalDrag();

    private void HorizontalList_PointerWheelChanged(object sender, PointerRoutedEventArgs e)
    {
        if (sender is not ListView list) return;
        var scroller = Descendants(list).OfType<ScrollViewer>().FirstOrDefault();
        if (scroller is null || scroller.ScrollableWidth <= 0) return;
        var delta = e.GetCurrentPoint(list).Properties.MouseWheelDelta;
        if (delta == 0) return;
        scroller.ChangeView(Math.Clamp(scroller.HorizontalOffset - delta, 0, scroller.ScrollableWidth), null, null, disableAnimation: false);
        e.Handled = true;
    }

    private void HorizontalList_KeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (sender is not ListView list || e.Key is not (VirtualKey.Left or VirtualKey.Right)) return;
        var focused = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject;
        var items = list.Items.OfType<Control>().ToArray();
        var currentIndex = Array.FindIndex(items, item => ReferenceEquals(item, focused) ||
            focused is not null && Descendants(item).Contains(focused));
        var targetIndex = currentIndex + (e.Key == VirtualKey.Right ? 1 : -1);
        if (targetIndex >= 0 && targetIndex < items.Length && items[targetIndex].Focus(FocusState.Keyboard))
        {
            items[targetIndex].StartBringIntoView();
            e.Handled = true;
            return;
        }
        var scroller = Descendants(list).OfType<ScrollViewer>().FirstOrDefault();
        if (scroller is null) return;
        var direction = e.Key == VirtualKey.Right ? 1 : -1;
        scroller.ChangeView(Math.Clamp(scroller.HorizontalOffset + direction * Math.Max(160, scroller.ViewportWidth * 0.75), 0, scroller.ScrollableWidth), null, null, disableAnimation: false);
        e.Handled = true;
    }

    private void ResetHorizontalDrag()
    {
        _horizontalDragList = null;
        _horizontalDragScroller = null;
        _horizontalDragPointerId = 0;
        _horizontalDragActive = false;
    }

    private Button CreateSeasonCard(SeasonSummary season)
    {
        var width = MediaCardWidth(landscape: false);
        var card = CreateArtworkCard(season.Name, season.PosterUrl ?? season.BackdropUrl, width, width * 3 / 2, hideTitle: false, enabled: true);
        if (card.Content is StackPanel stack)
        {
            var metadata = new List<string> { season.EpisodeCount == 1 ? "1 episode" : $"{season.EpisodeCount} episodes" };
            if (season.VoteAverage > 0) metadata.Add($"★ {season.VoteAverage:0.0}");
            stack.Children.Add(new TextBlock
            {
                Text = string.Join(" · ", metadata),
                Style = (Style)Application.Current.Resources["RivuneLabelSmallTextStyle"],
                Height = 16,
                MaxLines = 1,
                TextTrimming = TextTrimming.CharacterEllipsis,
            });
            stack.Children.Add(new TextBlock
            {
                Text = string.IsNullOrWhiteSpace(season.AirDate) ? string.Empty : ViewerDatePresentation.ReleaseDate(season.AirDate),
                Style = (Style)Application.Current.Resources["RivuneBodySmallTextStyle"],
                Height = 16,
                MaxLines = 1,
                TextTrimming = TextTrimming.CharacterEllipsis,
            });
        }
        card.Tag = season;
        card.Click += Season_Click;
        AutomationProperties.SetName(card, season.Name);
        return card;
    }

    private static GridView CardGrid()
    {
        var grid = new GridView
        {
            SelectionMode = ListViewSelectionMode.None,
            HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        grid.ContainerContentChanging += DisableContainerFocus;
        return grid;
    }

    private double CenteredGridWidth(double viewportWidth)
    {
        if (viewportWidth <= 0) return double.NaN;
        var horizontalPadding = _tvInputMode ? 96 : viewportWidth < 600 ? 32 : 64;
        return Math.Min(_tvInputMode ? 1440 : 1120, Math.Max(0, viewportWidth - horizontalPadding));
    }

    private void ResizeMediaGrid(GridView grid, double viewportWidth)
    {
        grid.Width = CenteredGridWidth(viewportWidth);
        grid.MaxWidth = _tvInputMode ? 1440 : 1120;
        grid.HorizontalAlignment = HorizontalAlignment.Center;
    }
    private static void DisableContainerFocus(ListViewBase sender, ContainerContentChangingEventArgs args) => args.ItemContainer.IsTabStop = false;

    private double MediaCardWidth(bool landscape)
    {
        var viewport = ActualWidth;
        if (_tvInputMode) return landscape ? TvLandscapeCardWidth : 156;
        if (viewport < 600)
        {
            var available = Math.Max(240, viewport - 32);
            return landscape ? Math.Clamp((available - 16) / 2, 140, LandscapeCardWidth) : Math.Clamp((available - 32) / 3.2, 88, 112);
        }
        return landscape ? LandscapeCardWidth : 112;
    }
    private Button CreateFolderCard(Collection collection, CollectionFolder folder)
    {
        var shape = collection.ViewMode == CollectionViewMode.FollowLayout ? folder.TileShape : collection.FolderCoverShape;
        var landscape = shape == CollectionTileShape.Landscape;
        var width = MediaCardWidth(landscape);
        var height = shape switch
        {
            CollectionTileShape.Landscape => width / LandscapeCardAspectRatio,
            CollectionTileShape.Square => width,
            _ => width * 3 / 2,
        };
        var expectsArtwork = !string.IsNullOrWhiteSpace(folder.CoverImageUrl) || folder.Id is not null;
        var hasAssignedArtwork = !string.IsNullOrWhiteSpace(folder.CoverImageUrl);
        Action<Image>? configureArtwork = expectsArtwork
            ? image => ConfigureFolderArtwork(image, collection, folder)
            : null;
        var fallback = !string.IsNullOrWhiteSpace(folder.CoverEmoji)
            ? folder.CoverEmoji
            : string.IsNullOrWhiteSpace(folder.Title) ? "•" : char.ToUpperInvariant(folder.Title.Trim()[0]).ToString();
        var card = CreateArtworkCard(
            folder.Title,
            null,
            width,
            height,
            folder.HideTitle,
            folder.Id is not null,
            configureArtwork,
            showFallback: !hasAssignedArtwork,
            fallbackText: fallback,
            centerTitle: true,
            fixedHeight: folder.HideTitle ? height : height + 48);
        card.Tag = new FolderSelection(collection, folder);
        card.Click += Folder_Click;
        return card;
    }

    private void ConfigureFolderArtwork(Image image, Collection collection, CollectionFolder folder)
    {
        RoutedEventHandler? loaded = null;
        loaded = (_, _) =>
        {
            image.Loaded -= loaded;
            _ = LoadFolderArtworkAsync(image, collection, folder, _state.GenerationId, _state.Token);
        };
        image.Loaded += loaded;
    }

    private async Task LoadFolderArtworkAsync(
        Image image,
        Collection collection,
        CollectionFolder folder,
        long generation,
        CancellationToken cancellationToken)
    {
        try
        {
            if (!string.IsNullOrWhiteSpace(folder.CoverImageUrl))
            {
                if (await LoadArtworkAsync(image, folder.CoverImageUrl, generation, cancellationToken)) RemoveArtworkFallback(image);
                return;
            }
            if (folder.Id is not Guid folderId ||
                !_state.IsCurrent(generation) ||
                cancellationToken.IsCancellationRequested)
                return;

            var key = new FolderArtworkKey(collection.Id, folderId);
            if (!_folderArtworkCache.TryGetValue(key, out var artworkUrl))
            {
                if (_homeFolderTasks.TryGetValue(key, out var homeRequest) && homeRequest.Generation == generation)
                {
                    var resolved = await homeRequest.Task;
                    artworkUrl = resolved is null
                        ? await ResolveFolderArtworkAsync(collection.Id, folderId, generation, cancellationToken)
                        : FolderArtworkUrl(resolved);
                }
                else
                {
                    if (!_folderArtworkTasks.TryGetValue(key, out var request) || request.Generation != generation)
                    {
                        request = new FolderArtworkRequest(
                            generation,
                            ResolveFolderArtworkAsync(collection.Id, folderId, generation, cancellationToken));
                        _folderArtworkTasks[key] = request;
                    }
                    try
                    {
                        artworkUrl = await request.Task;
                    }
                    finally
                    {
                        if (_folderArtworkTasks.TryGetValue(key, out var current) &&
                            current.Generation == generation &&
                            ReferenceEquals(current.Task, request.Task))
                            _folderArtworkTasks.Remove(key);
                    }
                }
                if (string.IsNullOrWhiteSpace(artworkUrl) || !_state.IsCurrent(generation)) return;
                _folderArtworkCache[key] = artworkUrl;
            }

            if (await LoadArtworkAsync(image, artworkUrl, generation, cancellationToken)) RemoveArtworkFallback(image);
        }
        catch (OperationCanceledException) { }
        catch { }
    }
    private static void RemoveArtworkFallback(Image image)
    {
        if (image.Parent is not Grid artwork) return;
        foreach (var fallback in artwork.Children.Where(child => !ReferenceEquals(child, image)).ToArray())
            artwork.Children.Remove(fallback);
    }

    private async Task<string?> ResolveFolderArtworkAsync(
        Guid collectionId,
        Guid folderId,
        long generation,
        CancellationToken cancellationToken)
    {
        var entered = false;
        var client = _state.Client;
        if (client is null) return null;
        try
        {
            await _folderArtworkGate.WaitAsync(cancellationToken);
            entered = true;
            var result = await client.ResolveCollectionFolderAsync(
                collectionId,
                folderId,
                page: 1,
                limit: 1,
                language: MetadataLanguage(),
                region: EffectiveMetadataRegion(),
                cancellationToken: cancellationToken);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return null;
            return FolderArtworkUrl(result);
        }
        finally
        {
            if (entered) _folderArtworkGate.Release();
        }
    }

    private static string? FolderArtworkUrl(ResolvedCollectionFolder result)
    {
        if (result.SourcePosterUrls is { Count: > 0 } sourcePosters)
        {
            foreach (var source in result.Folder.Sources)
            {
                if (source.Id is Guid sourceId &&
                    sourcePosters.TryGetValue(sourceId, out var matching) &&
                    !string.IsNullOrWhiteSpace(matching))
                    return matching;
            }
            var firstSourcePoster = sourcePosters.Values.FirstOrDefault(value => !string.IsNullOrWhiteSpace(value));
            if (firstSourcePoster is not null) return firstSourcePoster;
        }
        if (!string.IsNullOrWhiteSpace(result.Folder.CoverImageUrl)) return result.Folder.CoverImageUrl;
        var firstItem = result.Items.FirstOrDefault();
        if (!string.IsNullOrWhiteSpace(firstItem?.PosterUrl)) return firstItem.PosterUrl;
        return string.IsNullOrWhiteSpace(firstItem?.BackgroundUrl) ? null : firstItem.BackgroundUrl;
    }

    private Button CreateMediaCard(MediaTarget target, bool landscape = false, CollectionTileShape? shape = null)
    {
        var width = MediaCardWidth(landscape);
        var height = shape == CollectionTileShape.Square ? width : landscape ? width / LandscapeCardAspectRatio : width * 3 / 2;
        var artwork = landscape ? target.BackgroundUrl ?? target.PosterUrl : target.PosterUrl ?? target.BackgroundUrl;
        var card = CreateArtworkCard(
            target.Title,
            artwork,
            width,
            height,
            hideTitle: false,
            target.Available,
            fixedHeight: landscape ? height + 72 : null);
        if (card.Content is StackPanel stack)
        {
            if (landscape && stack.Children.OfType<TextBlock>().FirstOrDefault() is { } title)
                title.Height = 40;
            var metadata = new List<string>();
            if (!string.IsNullOrWhiteSpace(target.ReleaseInfo ?? target.Released)) metadata.Add(target.ReleaseInfo ?? target.Released!);
            if (target.RuntimeMinutes is > 0) metadata.Add($"{target.RuntimeMinutes} min");
            if (target.Rating is > 0) metadata.Add($"★ {target.Rating:0.0}");
            if (landscape || metadata.Count > 0)
            {
                stack.Children.Add(new TextBlock
                {
                    Text = metadata.Count == 0 ? MediaTypeLabel(target.MediaType) : string.Join(" • ", metadata),
                    Style = (Style)Application.Current.Resources["RivuneLabelSmallTextStyle"],
                    Height = landscape ? 16 : double.NaN,
                    MaxLines = 1,
                    TextTrimming = TextTrimming.CharacterEllipsis,
                });
            }
            if (target.ResumePositionSeconds > 0 && target.DurationSeconds > 0 &&
                stack.Children.FirstOrDefault() is Grid artworkPanel)
            {
                artworkPanel.Children.Add(new ProgressBar
                {
                    Minimum = 0,
                    Maximum = target.DurationSeconds,
                    Value = Math.Min(target.ResumePositionSeconds, target.DurationSeconds),
                    Height = 3,
                    HorizontalAlignment = HorizontalAlignment.Stretch,
                    VerticalAlignment = VerticalAlignment.Bottom,
                    IsHitTestVisible = false,
                });
            }
        }
        card.Tag = target;
        card.Click += MediaCard_Click;
        AutomationProperties.SetName(card, target.Available ? target.Title : $"{target.Title}, unavailable");
        return card;
    }
    private Button CreateEpisodeCard(MediaTarget target, PlaybackProgress? progress)
    {
        var width = MediaCardWidth(landscape: true);
        var height = width / LandscapeCardAspectRatio;
        var outerHeight = height + 96;
        var button = new Button
        {
            Style = (Style)Application.Current.Resources["RivuneMediaCard"],
            Width = width,
            Height = outerHeight,
            Margin = new Thickness(0, 0, 16, 0),
            Tag = target,
        };
        var stack = new StackPanel { Width = width, Height = outerHeight, Spacing = 8 };
        var artwork = new Grid
        {
            Width = width,
            Height = height,
            Background = (Brush)Application.Current.Resources["RivuneArtworkFallbackBrush"],
            CornerRadius = (CornerRadius)Application.Current.Resources["RivuneRadiusMedium"],
        };
        artwork.Children.Add(ArtworkFallback(target.Title));
        var artworkUrl = target.PosterUrl ?? target.BackgroundUrl;
        if (!string.IsNullOrWhiteSpace(artworkUrl))
        {
            var image = new Image { Opacity = 0, Stretch = Stretch.UniformToFill };
            artwork.Children.Add(image);
            _ = LoadArtworkAsync(image, artworkUrl, _state.GenerationId, _state.Token);
        }
        if (progress is { Completed: true })
        {
            artwork.Children.Add(new Border
            {
                Width = 28,
                Height = 28,
                CornerRadius = new CornerRadius(14),
                Background = (Brush)Application.Current.Resources["RivuneAccentBrush"],
                HorizontalAlignment = HorizontalAlignment.Right,
                VerticalAlignment = VerticalAlignment.Top,
                Margin = new Thickness(8),
                Child = new FontIcon { Glyph = "\uE73E", FontSize = 14 },
            });
        }
        else if (progress is { PositionSeconds: > 0, DurationSeconds: > 0 })
        {
            artwork.Children.Add(new ProgressBar
            {
                Minimum = 0,
                Maximum = progress.DurationSeconds,
                Value = Math.Min(progress.PositionSeconds, progress.DurationSeconds),
                Height = 4,
                Width = width,
                HorizontalAlignment = HorizontalAlignment.Stretch,
                VerticalAlignment = VerticalAlignment.Bottom,
                IsHitTestVisible = false,
            });
        }
        stack.Children.Add(artwork);
        stack.Children.Add(new TextBlock
        {
            Text = $"E{target.EpisodeNumber ?? 0} · {target.Title}",
            Style = (Style)Application.Current.Resources["RivuneTitleSmallTextStyle"],
            Height = 40,
            MaxLines = 2,
            TextTrimming = TextTrimming.CharacterEllipsis,
        });
        var primary = new List<string>();
        if (target.RuntimeMinutes is > 0) primary.Add($"{target.RuntimeMinutes} min");
        if (target.Rating is > 0) primary.Add($"★ {target.Rating:0.0}");
        stack.Children.Add(new TextBlock
        {
            Text = string.Join(" · ", primary),
            Style = (Style)Application.Current.Resources["RivuneLabelSmallTextStyle"],
            Height = 16,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
        });
        var released = target.ReleaseInfo ?? target.Released;
        stack.Children.Add(new TextBlock
        {
            Text = string.IsNullOrWhiteSpace(released) ? string.Empty : ViewerDatePresentation.ReleaseDate(released),
            Style = (Style)Application.Current.Resources["RivuneBodySmallTextStyle"],
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
            Height = 16,
        });
        button.Content = stack;
        AutomationProperties.SetName(button, $"Episode {target.EpisodeNumber ?? 0}, {target.Title}{(progress?.Completed == true ? ", watched" : string.Empty)}");
        return button;
    }

    private Button CreateArtworkCard(
        string title,
        string? artworkUrl,
        double width,
        double height,
        bool hideTitle,
        bool enabled,
        Action<Image>? configureArtwork = null,
        bool showFallback = true,
        string? fallbackText = null,
        double? fixedHeight = null,
        bool centerTitle = false)
    {
        var button = new Button
        {
            Style = (Style)Application.Current.Resources["RivuneMediaCard"],
            IsEnabled = enabled,
            Width = width,
            Height = fixedHeight ?? double.NaN,
            Margin = new Thickness(0, 0, 16, 0),
        };
        var stack = new StackPanel { Width = width, Height = fixedHeight ?? double.NaN, Spacing = 8 };
        var art = new Grid
        {
            Width = width,
            Height = height,
            Background = (Brush)Application.Current.Resources["RivuneArtworkFallbackBrush"],
            CornerRadius = (CornerRadius)Application.Current.Resources["RivuneRadiusMedium"],
        };
        if (showFallback)
        {
            art.Children.Add(fallbackText is null
                ? ArtworkFallback(title)
                : new TextBlock
                {
                    Text = fallbackText,
                    HorizontalAlignment = HorizontalAlignment.Center,
                    VerticalAlignment = VerticalAlignment.Center,
                    FontSize = 28,
                    FontWeight = Microsoft.UI.Text.FontWeights.Bold,
                });
        }
        if (!string.IsNullOrWhiteSpace(artworkUrl) || configureArtwork is not null)
        {
            var image = new Image
            {
                Width = width,
                Height = height,
                Opacity = 0,
                Stretch = Stretch.UniformToFill,
            };
            art.Children.Add(image);
            if (configureArtwork is not null) configureArtwork(image);
            else _ = LoadArtworkAsync(image, artworkUrl!, _state.GenerationId, _state.Token);
        }
        stack.Children.Add(art);
        if (!hideTitle)
        {
            stack.Children.Add(new TextBlock
            {
                Text = title,
                Style = (Style)Application.Current.Resources["RivuneTitleSmallTextStyle"],
                HorizontalAlignment = HorizontalAlignment.Stretch,
                TextAlignment = centerTitle ? TextAlignment.Center : TextAlignment.Left,
                MaxLines = 2,
                TextTrimming = TextTrimming.CharacterEllipsis,
            });
        }
        button.Content = stack;
        return button;
    }

    private FrameworkElement CreateMediaRow(string title, IReadOnlyList<MediaTarget> items, bool landscape)
    {
        var section = new StackPanel { Spacing = 12 };
        section.Children.Add(new TextBlock { Text = title, Style = (Style)Application.Current.Resources["RivuneTitleLargeTextStyle"] });
        var row = HorizontalList();
        foreach (var target in items) row.Items.Add(CreateMediaCard(target, landscape));
        section.Children.Add(row);
        return section;
    }

    private static FrameworkElement CreateEmptyState(string title, string body)
    {
        var panel = new StackPanel { Spacing = 8, Padding = new Thickness(8, 16, 8, 16) };
        var heading = new TextBlock { Text = title, Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"] };
        AutomationProperties.SetHeadingLevel(heading, AutomationHeadingLevel.Level2);
        panel.Children.Add(heading);
        panel.Children.Add(new TextBlock { Text = body, Style = (Style)Application.Current.Resources["RivuneBodyMediumTextStyle"] });
        return panel;
    }


    private async void Collection_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: Collection collection }) await ShowCollectionAsync(collection);
    }

    private async void Folder_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: FolderSelection selection }) await ShowFolderAsync(selection.Collection, selection.Folder);
    }

    private async void MediaCard_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: MediaTarget target } && target.Available)
            await OpenMediaTargetAsync(target);
    }

    private async Task ShowCollectionAsync(Collection collection)
    {
        _state.Transition(AppPhase.Detail);
        _detailBackAction = () => ReturnToViewerAsync();
        PrepareGenericDetail(collection.Title, null, string.Empty, catalogLayout: true);
        DetailSections.Children.Clear();
        var wrap = new StackPanel { Spacing = 16 };
        var grid = CardGrid();
        foreach (var folder in collection.Folders) grid.Items.Add(CreateFolderCard(collection, folder));
        wrap.Children.Add(grid);
        DetailSections.Children.Add(wrap);
        ShowOnly(DetailView);
        await Task.CompletedTask;
    }

    private async Task ShowFolderAsync(Collection collection, CollectionFolder folder)
    {
        _detailRetryAction = () => ShowFolderAsync(collection, folder);
        if (folder.Id is not Guid folderId) return;
        var generation = _state.Transition(AppPhase.Detail);
        _detailBackAction = () => ShowCollectionAsync(collection);
        PrepareGenericDetail(collection.Title, null, "Loading this folder", catalogLayout: true);
        _folderItems.Clear();
        _resolvedFolder = null;
        _folderSourceId = null;
        _folderMediaFilter = null;
        _folderPage = 0;
        _folderHasMore = false;
        ShowOnly(DetailView);
        await LoadFolderPageAsync(collection, folderId, generation);
    }

    private async Task LoadFolderPageAsync(Collection collection, Guid folderId, long generation)
    {
        DetailProgress.IsActive = true;
        DetailStatus.Visibility = Visibility.Visible;
        DetailBanner.IsOpen = false;
        DetailRetryButton.Visibility = Visibility.Collapsed;
        try
        {
            var nextPage = _folderPage + 1;
            var result = await _state.Client!.ResolveCollectionFolderAsync(
                collection.Id,
                folderId,
                nextPage,
                FolderPageSize,
                language: MetadataLanguage(),
                region: EffectiveMetadataRegion(),
                cancellationToken: _state.Token);
            if (!_state.IsCurrent(generation)) return;
            if (_resolvedFolder is null)
            {
                _resolvedFolder = result;
                DetailTitle.Text = collection.Title;
                DetailTagline.Text = string.Empty;
                DetailMetadata.Text = string.Empty;
                var sources = result.Folder.Sources.Where(value => value.Id is not null).ToArray();
                if (sources.Length > 1 && result.Folder.SourceView == CollectionSourceView.Categories)
                    _folderSourceId = sources[0].Id;
            }
            var seenItems = _folderItems.Select(value => $"{value.MediaType}:{value.Id}").ToHashSet(StringComparer.OrdinalIgnoreCase);
            _folderItems.AddRange(result.Items.Where(item => seenItems.Add($"{item.MediaType}:{item.Id}")));
            _folderPage = nextPage;
            _folderHasMore = result.HasMore;
            RenderFolderContents(collection, folderId);
            if (result.Errors.Count > 0)
            {
                DetailBanner.Severity = InfoBarSeverity.Warning;
                DetailBanner.Message = "Some titles could not be loaded. The available results are shown.";
                DetailBanner.IsOpen = true;
            }
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            DetailMetadata.Text = string.Empty;
            DetailBanner.Severity = InfoBarSeverity.Error;
            DetailBanner.Message = FriendlyError(exception);
            DetailBanner.IsOpen = true;
            DetailRetryButton.Visibility = Visibility.Visible;
            DetailStatus.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation))
            {
                DetailProgress.IsActive = false;
                DetailStatus.Visibility = DetailBanner.IsOpen ? Visibility.Visible : Visibility.Collapsed;
            }
        }
    }

    private void RenderFolderContents(Collection collection, Guid folderId)
    {
        DetailSections.Children.Clear();
        if (_resolvedFolder is null) return;
        var folder = _resolvedFolder.Folder;
        var sources = folder.Sources.Where(value => value.Id is not null).ToArray();
        var activeSources = _folderSourceId is Guid sourceId
            ? sources.Where(value => value.Id == sourceId).ToArray()
            : sources;
        if (sources.Length > 1 && folder.SourceView == CollectionSourceView.Categories)
            DetailSections.Children.Add(CreateFolderFilterRow(sources.Select(source => (
                SourceLabel(source),
                SourceGlyph(source),
                source.Id == _folderSourceId,
                (Action)(() => { _folderSourceId = source.Id; _folderMediaFilter = null; RenderFolderContents(collection, folderId); })))));

        var supportsMediaFilter = activeSources.Any(SourceSupportsBothMediaTypes);
        if (supportsMediaFilter)
        {
            DetailSections.Children.Add(CreateFolderFilterRow(new[]
            {
                (Label: "All", Glyph: (string?)"\uE8A9", Selected: _folderMediaFilter is null, Select: (Action)(() => { _folderMediaFilter = null; RenderFolderContents(collection, folderId); })),
                (Label: "Movies", Glyph: (string?)"\uE8B2", Selected: _folderMediaFilter == "movie", Select: (Action)(() => { _folderMediaFilter = "movie"; RenderFolderContents(collection, folderId); })),
                (Label: "Series", Glyph: (string?)"\uE8B7", Selected: _folderMediaFilter == "series", Select: (Action)(() => { _folderMediaFilter = "series"; RenderFolderContents(collection, folderId); })),
            }));
        }

        IEnumerable<CollectionItem> visible = _folderItems;
        if (_folderSourceId is Guid selectedSource)
            visible = visible.Where(item => item.Sources.Any(source => source.Id == selectedSource));
        if (_folderMediaFilter is not null)
            visible = visible.Where(item => item.MediaType.Equals(_folderMediaFilter, StringComparison.OrdinalIgnoreCase));
        var items = visible.ToArray();
        if (items.Length == 0)
        {
            DetailSections.Children.Add(CreateEmptyState("No title is available here.", "Refresh this folder or check its sources on your Rivune server."));
        }
        else
        {
            var grid = CardGrid();
            foreach (var item in items)
            {
                var target = item.ToMediaTarget();
                var shape = target.MediaType.Equals("tv", StringComparison.OrdinalIgnoreCase)
                    ? CollectionTileShape.Landscape
                    : folder.TileShape;
                grid.Items.Add(CreateMediaCard(target, shape == CollectionTileShape.Landscape, shape));
            }
            DetailSections.Children.Add(grid);
        }
        if (_folderHasMore)
        {
            var more = new Button
            {
                Content = "Load more",
                Style = (Style)Application.Current.Resources["RivuneTextButton"],
                HorizontalAlignment = HorizontalAlignment.Center,
            };
            more.Click += async (_, _) =>
            {
                var loadGeneration = _state.Transition(AppPhase.Detail);
                await LoadFolderPageAsync(collection, folderId, loadGeneration);
            };
            DetailSections.Children.Add(more);
        }
    }

    private static FrameworkElement CreateFolderFilterRow(IEnumerable<(string Label, string? Glyph, bool Selected, Action Select)> filters)
    {
        var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4 };
        foreach (var filter in filters)
        {
            var content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
            if (filter.Glyph is not null) content.Children.Add(new FontIcon { Glyph = filter.Glyph, FontSize = 16 });
            content.Children.Add(new TextBlock { Text = filter.Label });
            var button = new ToggleButton
            {
                Content = content,
                IsChecked = filter.Selected,
                Style = (Style)Application.Current.Resources["RivuneCatalogFilterToggle"],
                MinWidth = 72,
            };
            AutomationProperties.SetName(button, filter.Label);
            button.Click += (_, _) => filter.Select();
            row.Children.Add(button);
        }
        return new ScrollViewer
        {
            Content = row,
            HorizontalScrollMode = ScrollMode.Enabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollMode = ScrollMode.Disabled,
        };
    }

    private static bool SourceSupportsBothMediaTypes(CollectionSource source) =>
        SourceMediaType(source).Equals("both", StringComparison.OrdinalIgnoreCase);

    private static string SourceLabel(CollectionSource source)
    {
        var mediaType = SourceMediaType(source);
        var title = source.Title.Trim().ToLowerInvariant();
        if (mediaType == "movie" && new[] { "movie", "movies", "film", "films" }.Contains(title)) return "Movies";
        if ((mediaType is "series" or "tv") && new[] { "series", "tv", "show", "shows", "tv show", "tv shows", "série", "séries" }.Contains(title)) return "Series";
        return source.Title;
    }

    private static string? SourceGlyph(CollectionSource source)
    {
        var mediaType = SourceMediaType(source);
        var title = source.Title.Trim().ToLowerInvariant();
        if (mediaType == "movie" || new[] { "movie", "movies", "film", "films" }.Contains(title)) return "\uE8B2";
        if (mediaType is "series" or "tv" || new[] { "series", "tv", "show", "shows", "tv show", "tv shows", "série", "séries" }.Contains(title)) return "\uE8B7";
        return null;
    }

    private static string SourceMediaType(CollectionSource source)
    {
        if (!string.IsNullOrWhiteSpace(source.AddonCatalog?.Type)) return source.AddonCatalog.Type.Trim().ToLowerInvariant();
        foreach (var value in new[] { source.Tmdb, source.Trakt, source.Mdblist })
        {
            if (value is JsonElement { ValueKind: JsonValueKind.Object } json &&
                json.TryGetProperty("mediaType", out var mediaType) &&
                mediaType.ValueKind == JsonValueKind.String)
                return mediaType.GetString()?.Trim().ToLowerInvariant() ?? string.Empty;
        }
        return string.Empty;
    }

    private void PrepareGenericDetail(string title, string? tagline, string metadata, bool catalogLayout = false)
    {
        ApplyDetailLayout(catalogLayout);
        DetailBackdrop.Opacity = 0;
        DetailTitle.Text = title;
        DetailTitle.Style = (Style)Application.Current.Resources[catalogLayout
            ? "RivuneTitleLargeTextStyle"
            : "RivuneHeadlineLargeTextStyle"];
        DetailTitle.MaxLines = catalogLayout ? 1 : 3;
        DetailTagline.Text = tagline ?? string.Empty;
        DetailMetadata.Text = metadata;
        DetailSecondaryMetadata.Text = string.Empty;
        DetailEpisodeCoordinates.Text = string.Empty;
        DetailEpisodeCoordinates.Visibility = Visibility.Collapsed;
        DetailOverview.Text = string.Empty;
        DetailActions.Items.Clear();
        DetailTagline.Visibility = catalogLayout ? Visibility.Collapsed : Visibility.Visible;
        DetailMetadata.Visibility = catalogLayout ? Visibility.Collapsed : Visibility.Visible;
        DetailSecondaryMetadata.Visibility = catalogLayout ? Visibility.Collapsed : Visibility.Visible;
        DetailActions.Visibility = catalogLayout ? Visibility.Collapsed : Visibility.Visible;
        DetailOverviewScroller.Visibility = catalogLayout ? Visibility.Collapsed : Visibility.Visible;
        DetailSections.Children.Clear();
        DetailRetryButton.Visibility = Visibility.Collapsed;
        DetailBanner.IsOpen = false;
        DetailStatus.Visibility = Visibility.Collapsed;
    }

    private void ApplyDetailLayout(bool catalogLayout, double? viewportWidth = null)
    {
        _catalogDetailLayout = catalogLayout;
        var width = viewportWidth ?? ActualWidth;
        DetailSummary.Spacing = catalogLayout ? 8 : 12;
        DetailSummary.MaxWidth = width >= 840 ? 720 : 560;
        DetailSummary.Margin = catalogLayout
            ? width switch
            {
                < 600 => new Thickness(72, 16, 16, 8),
                < 840 => new Thickness(80, 24, 24, 8),
                < 1200 => new Thickness(88, 32, 32, 8),
                _ => new Thickness(104, 32, 48, 8),
            }
            : width switch
            {
                < 600 => new Thickness(16, 112, 16, 24),
                < 840 => new Thickness(24, 176, 24, 32),
                < 1200 => new Thickness(32, 176, 32, 40),
                _ => new Thickness(48, 176, 48, 40),
            };
    }

    private async void HeroInfo_Click(object sender, RoutedEventArgs e)
    {
        if (_heroTarget is not null) await OpenMediaTargetAsync(_heroTarget);
    }

    private async void HeroPlay_Click(object sender, RoutedEventArgs e)
    {
        if (_heroTarget is not null) await OpenMediaTargetAsync(_heroTarget, playWhenReady: true);
    }

    private async Task LoadSearchDescriptorsAsync()
    {
        var client = _state.Client;
        if (client is null) return;
        var generation = _state.GenerationId;
        SearchLoading.Visibility = Visibility.Visible;
        SearchBanner.IsOpen = false;
        SearchRetryButton.Visibility = Visibility.Collapsed;
        try
        {
            var descriptors = await client.GetAddonCatalogsAsync(_state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return;
            _searchDescriptors = descriptors;
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            SearchBanner.Severity = InfoBarSeverity.Error;
            SearchBanner.Message = FriendlyError(exception);
            SearchBanner.IsOpen = true;
            SearchRetryButton.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation)) SearchLoading.Visibility = Visibility.Collapsed;
        }
    }

    private async void Search_Click(object sender, RoutedEventArgs e) => await SearchAsync(reset: true);
    private async void SearchMore_Click(object sender, RoutedEventArgs e) => await SearchAsync(reset: false);
    private async void SearchBox_KeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter) return;
        e.Handled = true;
        await SearchAsync(reset: true);
    }
    private async Task SearchAsync(bool reset)
    {
        var generation = _state.Transition(AppPhase.Catalogue);
        var query = (reset ? SearchBox.Text : _searchQuery).Trim();
        if (query.Length < 2)
        {
            _searchTargets.Clear();
            _searchQuery = string.Empty;
            _searchPage = 0;
            _searchHasMore = false;
            SearchResults.Items.Clear();
            SearchResultCount.Text = string.Empty;
            SearchMoreButton.Visibility = Visibility.Collapsed;
            SearchResultContent.Visibility = Visibility.Collapsed;
            ((TextBlock)SearchEmpty.Children[0]).Text = "What would you like to watch?";
            ((TextBlock)SearchEmpty.Children[1]).Text = "Enter at least two characters to search movies and series.";
            SearchEmpty.Visibility = Visibility.Visible;
            return;
        }
        if (_searchDescriptors.Count == 0) await LoadSearchDescriptorsAsync();
        if (!_state.IsCurrent(generation)) return;
        if (_searchDescriptors.Count == 0)
        {
            SearchResults.Items.Clear();
            SearchResultCount.Text = string.Empty;
            SearchMoreButton.Visibility = Visibility.Collapsed;
            SearchResultContent.Visibility = Visibility.Collapsed;
            SearchEmpty.Visibility = Visibility.Visible;
            ((TextBlock)SearchEmpty.Children[0]).Text = "Search unavailable";
            ((TextBlock)SearchEmpty.Children[1]).Text = "No searchable source is available for this profile.";
            if (!SearchBanner.IsOpen)
            {
                SearchBanner.Severity = InfoBarSeverity.Warning;
                SearchBanner.Message = "No searchable source is available for this profile.";
                SearchBanner.IsOpen = true;
            }
            return;
        }
        SearchEmpty.Visibility = Visibility.Collapsed;
        if (reset) SearchResultContent.Visibility = Visibility.Collapsed;
        SearchLoading.Visibility = Visibility.Visible;
        SearchBanner.IsOpen = false;
        SearchRetryButton.Visibility = Visibility.Collapsed;
        try
        {
            var page = reset ? 1 : _searchPage + 1;
            var skip = (page - 1) * SearchPageSize;
            var types = _searchDescriptors.Where(value => value.Searchable).Select(value => value.Catalog.Type).Distinct(StringComparer.OrdinalIgnoreCase).ToArray();
            if (types.Length == 0) throw new InvalidOperationException("No searchable source is available for this profile.");
            var results = await Task.WhenAll(types.Select(async type =>
            {
                try { return await _state.Client!.SearchAddonCatalogsAsync(type, query, skip, SearchPageSize, language: MetadataLanguage(), cancellationToken: _state.Token); }
                catch (Exception exception) when (exception is not OperationCanceledException) { return null; }
            }));
            if (!_state.IsCurrent(generation)) return;
            if (results.All(value => value is null)) throw new InvalidOperationException("No search source could be reached.");
            var batches = results.Where(value => value is not null).Cast<AddonResourceBatch>().ToArray();
            var incoming = batches.SelectMany(value => value.ToMediaTargets(_searchDescriptors));
            if (reset) _searchTargets.Clear();
            var seen = _searchTargets.Select(TargetIdentity).ToHashSet(StringComparer.OrdinalIgnoreCase);
            _searchTargets.AddRange(incoming.Where(value => seen.Add(TargetIdentity(value))));
            _searchQuery = query;
            _searchPage = page;
            _searchHasMore = batches.Any(value => value.HasFullPage(SearchPageSize));
            PopulateMediaGrid(SearchResults, _searchTargets);
            SearchResultCount.Text = _searchTargets.Count == 1 ? "1 title" : $"{_searchTargets.Count} titles";
            SearchResultContent.Visibility = _searchTargets.Count == 0 ? Visibility.Collapsed : Visibility.Visible;
            SearchEmpty.Visibility = _searchTargets.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
            if (_searchTargets.Count == 0)
            {
                ((TextBlock)SearchEmpty.Children[0]).Text = "No matching title";
                ((TextBlock)SearchEmpty.Children[1]).Text = "Try another title or a broader search.";
            }
            SearchMoreButton.Visibility = _searchHasMore ? Visibility.Visible : Visibility.Collapsed;
            if (results.Any(value => value is null) || batches.Any(value => value.Errors.Count > 0))
            {
                SearchBanner.Severity = InfoBarSeverity.Warning;
                SearchBanner.Message = "Some sources could not be reached. Available results are shown.";
                SearchBanner.IsOpen = true;
            }
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            SearchBanner.Severity = InfoBarSeverity.Error;
            SearchBanner.Message = FriendlyError(exception);
            SearchBanner.IsOpen = true;
            SearchRetryButton.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation)) SearchLoading.Visibility = Visibility.Collapsed;
        }
    }

    private void PopulateMediaGrid(GridView grid, IEnumerable<MediaTarget> targets)
    {
        ResizeMediaGrid(grid, ActualWidth);
        grid.Items.Clear();
        foreach (var target in targets) grid.Items.Add(CreateMediaCard(target));
    }

    private static string TargetIdentity(MediaTarget target) => target.Identity();

    private async void LibraryFilter_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not ToggleButton button) return;
        _libraryType = (button.Tag as string) switch
        {
            "movie" => TitleMediaType.Movie,
            "series" => TitleMediaType.Series,
            "tv" => TitleMediaType.Tv,
            _ => null,
        };
        if (button.Parent is Panel panel)
            foreach (var sibling in panel.Children.OfType<ToggleButton>()) sibling.IsChecked = sibling == button;
        _state.Transition(AppPhase.Catalogue);
        await LoadLibraryAsync(reset: true);
    }

    private async void LibraryMore_Click(object sender, RoutedEventArgs e)
    {
        _state.Transition(AppPhase.Catalogue);
        await LoadLibraryAsync(reset: false);
    }

    private async Task LoadLibraryAsync(bool reset)
    {
        var generation = _state.GenerationId;
        var client = _state.Client;
        if (client is null) return;
        LibraryLoading.Visibility = Visibility.Visible;
        LibraryBanner.IsOpen = false;
        LibraryRetryButton.Visibility = Visibility.Collapsed;
        try
        {
            var page = reset ? 1 : _libraryPage + 1;
            var response = await client.GetLibraryAsync(_libraryType, page, LibraryPageSize, _state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return;
            if (reset) _libraryItems.Clear();
            var seen = _libraryItems.Select(value => value.TitleId).ToHashSet();
            _libraryItems.AddRange(response.Items.Where(value => seen.Add(value.TitleId)));
            _libraryPage = response.Page;
            _libraryTotalPages = response.TotalPages;
            PopulateMediaGrid(LibraryResults, _libraryItems.Select(value => value.ToMediaTarget()));
            LibraryResultCount.Text = response.TotalResults == 1 ? "1 saved title" : $"{response.TotalResults} saved titles";
            LibraryEmpty.Visibility = _libraryItems.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
            LibraryMoreButton.Visibility = _libraryPage < _libraryTotalPages ? Visibility.Visible : Visibility.Collapsed;
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            LibraryBanner.Severity = InfoBarSeverity.Error;
            LibraryBanner.Message = FriendlyError(exception);
            LibraryBanner.IsOpen = true;
            LibraryRetryButton.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation)) LibraryLoading.Visibility = Visibility.Collapsed;
        }
    }

    private async void Library_ItemClick(object sender, ItemClickEventArgs e)
    {
        if (e.ClickedItem is Button { Tag: MediaTarget target }) await OpenMediaTargetAsync(target);
    }

    private async void CalendarPrevious_Click(object sender, RoutedEventArgs e)
    {
        _state.Transition(AppPhase.Catalogue);
        _calendarMonth = _calendarMonth.AddMonths(-1);
        await LoadCalendarAsync();
    }

    private async void CalendarNext_Click(object sender, RoutedEventArgs e)
    {
        _state.Transition(AppPhase.Catalogue);
        _calendarMonth = _calendarMonth.AddMonths(1);
        await LoadCalendarAsync();
    }

    private async Task LoadCalendarAsync()
    {
        var generation = _state.GenerationId;
        var client = _state.Client;
        if (client is null) return;
        CalendarHeading.Text = _calendarMonth.ToString("MMMM yyyy", CultureInfo.CurrentCulture);
        CalendarLoading.Visibility = Visibility.Visible;
        CalendarBanner.IsOpen = false;
        CalendarRetryButton.Visibility = Visibility.Collapsed;
        try
        {
            var from = _calendarMonth.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
            var to = _calendarMonth.AddMonths(1).AddDays(-1).ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
            var events = await client.GetCalendarAsync(from, to, language: MetadataLanguage(), cancellationToken: _state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return;
            CalendarEvents.Children.Clear();
            foreach (var group in events.OrderBy(value => value.ReleaseDate).GroupBy(value => value.ReleaseDate))
            {
                var section = new StackPanel { Spacing = 8 };
                section.Children.Add(new TextBlock
                {
                    Text = DateTime.TryParse(group.Key, CultureInfo.InvariantCulture, DateTimeStyles.AssumeLocal, out var date)
                        ? date.ToString("D", CultureInfo.CurrentCulture)
                        : group.Key,
                    Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"],
                });
                foreach (var item in group)
                {
                    var target = item.ToMediaTarget();
                    var button = new Button
                    {
                        Style = (Style)Application.Current.Resources["RivuneMediaCard"],
                        HorizontalAlignment = HorizontalAlignment.Stretch,
                        Tag = target,
                    };
                    var grid = new Grid { MinHeight = 72, ColumnSpacing = 12, Padding = new Thickness(8) };
                    grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(64) });
                    grid.ColumnDefinitions.Add(new ColumnDefinition());
                    grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
                    var artwork = new Grid
                    {
                        Width = 64,
                        Height = 64,
                        Background = (Brush)Application.Current.Resources["RivuneArtworkFallbackBrush"],
                        CornerRadius = (CornerRadius)Application.Current.Resources["RivuneRadiusMedium"],
                    };
                    artwork.Children.Add(ArtworkFallback(item.Title));
                    if (!string.IsNullOrWhiteSpace(item.PosterUrl))
                    {
                        var image = new Image { Opacity = 0, Stretch = Stretch.UniformToFill };
                        artwork.Children.Add(image);
                        _ = LoadArtworkAsync(image, item.PosterUrl, generation, _state.Token);
                    }
                    grid.Children.Add(artwork);
                    var copy = new StackPanel { Spacing = 4, VerticalAlignment = VerticalAlignment.Center };
                    copy.Children.Add(new TextBlock { Text = item.SeriesTitle ?? MediaTypeLabel(target.MediaType), Style = (Style)Application.Current.Resources["RivuneLabelSmallTextStyle"] });
                    copy.Children.Add(new TextBlock { Text = item.Title, Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"] });
                    copy.Children.Add(new TextBlock { Text = item.SeasonNumber is int season && item.EpisodeNumber is int episode ? $"S{season} · E{episode}" : MediaTypeLabel(target.MediaType), Style = (Style)Application.Current.Resources["RivuneBodySmallTextStyle"] });
                    Grid.SetColumn(copy, 1);
                    grid.Children.Add(copy);
                    var chevron = new FontIcon { Glyph = "\uE76C", Foreground = (Brush)Application.Current.Resources["RivuneAccentBrush"], VerticalAlignment = VerticalAlignment.Center };
                    Grid.SetColumn(chevron, 2);
                    grid.Children.Add(chevron);
                    button.Content = grid;
                    button.Click += MediaCard_Click;
                    section.Children.Add(button);
                }
                CalendarEvents.Children.Add(section);
            }
            CalendarEmpty.Visibility = events.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            CalendarBanner.Severity = InfoBarSeverity.Error;
            CalendarBanner.Message = FriendlyError(exception);
            CalendarBanner.IsOpen = true;
            CalendarRetryButton.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation)) CalendarLoading.Visibility = Visibility.Collapsed;
        }
    }

    private async Task OpenMediaTargetAsync(MediaTarget target, bool playWhenReady = false)
    {
        if (target.MediaType.Equals("episode", StringComparison.OrdinalIgnoreCase))
        {
            await OpenEpisodeTargetAsync(target, playWhenReady || _devicePreferences.AutomaticallyShowSources);
            return;
        }
        var generation = _state.Transition(AppPhase.Detail);
        _detailRetryAction = () => OpenMediaTargetAsync(target, playWhenReady);
        _detailTarget = target;
        _detailReference = null;
        _detailMovie = null;
        _detailSeries = null;
        _detailSeason = null;
        _detailProgress = null;
        _seriesEpisodes = [];
        _seriesWatchStateReady = false;
        _episodeProgress.Clear();
        _detailInLibrary = false;
        _detailBackAction = ReturnToViewerAsync;
        PrepareGenericDetail(target.Title, null, "Loading title details");
        DetailStatus.Visibility = Visibility.Visible;
        DetailProgress.IsActive = true;
        ShowOnly(DetailView);
        try
        {
            var client = _state.Client ?? throw new NotAuthenticatedException();
            var reference = await ResolveTargetAsync(target);
            if (!_state.IsCurrent(generation)) return;
            _detailReference = reference;
            _state.SelectedTitle = reference;
            _progressTitleId = target.TitleId ?? reference.TitleId;
            try
            {
                _detailProgress = target.MediaType.Equals("tv", StringComparison.OrdinalIgnoreCase)
                    ? null
                    : await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token);
            }
            catch (Exception exception) when (exception is not OperationCanceledException) { }
            _progressVersion = _detailProgress?.Version ?? 0;
            if (reference.MediaType == TitleResolveMediaType.Movie)
            {
                try { _detailMovie = await client.GetMovieAsync(reference.TitleId, language: MetadataLanguage(), cancellationToken: _state.Token); }
                catch (Exception exception) when (exception is not OperationCanceledException) { }
            }
            else if (reference.MediaType == TitleResolveMediaType.Series)
            {
                try { _detailSeries = await LoadSeriesAsync(reference.TitleId); }
                catch (Exception exception) when (exception is not OperationCanceledException) { }
            }
            if (_detailSeries is not null)
            {
                try { await LoadSeriesWatchStateAsync(_detailSeries, generation); }
                catch (Exception exception) when (exception is not OperationCanceledException) { }
            }
            if (!_state.IsCurrent(generation)) return;
            try
            {
                var library = await client.GetLibraryAsync(page: 1, pageSize: LibraryPageSize, cancellationToken: _state.Token);
                _detailInLibrary = library.Items.Any(value => value.TitleId == reference.TitleId);
            }
            catch (Exception exception) when (exception is not OperationCanceledException) { }
            await RenderDetailAsync(generation);
            var showSources = !target.MediaType.Equals("series", StringComparison.OrdinalIgnoreCase) &&
                (playWhenReady || _devicePreferences.AutomaticallyShowSources);
            if (showSources) await OpenSourcesForCurrentDetailAsync(automatic: !playWhenReady);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            DetailBanner.Severity = InfoBarSeverity.Error;
            DetailBanner.Message = FriendlyError(exception);
            DetailBanner.IsOpen = true;
            DetailRetryButton.Visibility = Visibility.Visible;
            DetailStatus.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation)) DetailProgress.IsActive = false;
        }
    }

    private async Task OpenEpisodeTargetAsync(MediaTarget target, bool openSources, bool reuseSeasonContext = false)
    {
        var previousTarget = _detailTarget;
        var previousReference = _detailReference;
        var previousSeries = _detailSeries;
        var previousSeason = _detailSeason;
        var previousProgress = _detailProgress;
        var previousBackAction = _detailBackAction;
        var hadSeasonContext = reuseSeasonContext && previousSeries is not null && previousSeason is not null &&
            target.SeriesId is Guid targetSeriesId && previousSeries.Id == targetSeriesId;
        if (!hadSeasonContext)
        {
            _detailReference = null;
            _detailSeries = null;
            _detailSeason = null;
            _seriesEpisodes = [];
            _seriesWatchStateReady = false;
            _episodeProgress.Clear();
        }
        var generation = _state.Transition(AppPhase.Detail);
        _detailRetryAction = () => OpenEpisodeTargetAsync(target, openSources, reuseSeasonContext);
        _detailTarget = target;
        _detailMovie = null;
        _detailProgress = null;
        _detailInLibrary = false;
        PrepareGenericDetail(target.Title, null, "Loading episode details");
        DetailStatus.Visibility = Visibility.Visible;
        DetailProgress.IsActive = true;
        ShowOnly(DetailView);
        try
        {
            var client = _state.Client ?? throw new NotAuthenticatedException();
            if (!hadSeasonContext && target.SeriesId is Guid seriesId)
            {
                _detailSeries = await LoadSeriesAsync(seriesId);
                var summary = _detailSeries.Seasons.FirstOrDefault(value => value.Id == target.SeasonId)
                    ?? _detailSeries.Seasons.FirstOrDefault(value => value.SeasonNumber == target.SeasonNumber);
                _detailSeason = summary is null
                    ? null
                    : await client.GetSeasonAsync(summary.Id, _detailSeries.MappingProvider, language: MetadataLanguage(), cancellationToken: _state.Token);
            }
            var episode = _detailSeason?.Episodes.FirstOrDefault(value => value.Id == target.TitleId)
                ?? _detailSeason?.Episodes.FirstOrDefault(value => value.EpisodeNumber == target.EpisodeNumber);
            if (episode is not null && _detailSeries is not null)
            {
                _detailTarget = EpisodeTarget(_detailSeries, episode);
            }
            if (!_state.IsCurrent(generation)) return;
            _progressTitleId = _detailTarget.TitleId
                ?? (Guid.TryParse(_detailTarget.Id, out var episodeId) ? episodeId : throw new InvalidOperationException("This episode has no progress identity."));
            _detailProgress = await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token);
            _progressVersion = _detailProgress?.Version ?? 0;
            _detailBackAction = hadSeasonContext
                ? async () =>
                {
                    _detailTarget = previousTarget;
                    _detailReference = previousReference;
                    _detailSeries = previousSeries;
                    _detailSeason = previousSeason;
                    _detailProgress = previousProgress;
                    _detailBackAction = previousBackAction;
                    var backGeneration = _state.Transition(AppPhase.Detail);
                    await RenderDetailAsync(backGeneration);
                }
            : ReturnToViewerAsync;
            await RenderDetailAsync(generation);
            if (openSources) await OpenSourcesForCurrentDetailAsync();
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            DetailBanner.Severity = InfoBarSeverity.Error;
            DetailBanner.Message = FriendlyError(exception);
            DetailBanner.IsOpen = true;
            DetailRetryButton.Visibility = Visibility.Visible;
            DetailStatus.Visibility = Visibility.Visible;
        }
        finally
        {
            if (_state.IsCurrent(generation)) DetailProgress.IsActive = false;
        }
    }

    private async Task<TitleReference> ResolveTargetAsync(MediaTarget target)
    {
        if (target.TitleId is Guid titleId)
        {
            var knownProvider = target.Provider
                ?? new[] { "tmdb", "imdb", "tvdb", "trakt" }.FirstOrDefault(target.ExternalIds.ContainsKey)
                ?? "unknown";
            var knownExternalId = target.ExternalId
                ?? target.ExternalIds.GetValueOrDefault(knownProvider)
                ?? target.Id;
            return new TitleReference
            {
                TitleId = titleId,
                MediaType = target.MediaType switch { "movie" => TitleResolveMediaType.Movie, "tv" => TitleResolveMediaType.Tv, _ => TitleResolveMediaType.Series },
                Provider = knownProvider,
                ExternalId = knownExternalId,
                ResourceId = target.ResourceId,
                Title = target.Title,
                PosterUrl = target.PosterUrl,
                BackgroundUrl = target.BackgroundUrl,
                ReleaseInfo = target.ReleaseInfo,
                SourceAddonId = target.SourceAddonId,
                SourceCatalogId = target.SourceCatalogId,
                SourceName = target.SourceName,
                Country = target.Country,
                Language = target.Language,
                Category = target.Category,
            };
        }
        var preferredProvider = new[] { "tmdb", "imdb", "tvdb", "trakt" }
            .FirstOrDefault(candidate => target.ExternalIds.TryGetValue(candidate, out var value) && !string.IsNullOrWhiteSpace(value));
        var providerName = target.Provider ?? (target.MediaType == "tv" ? "addon" : preferredProvider ?? MediaIdentity.InferProvider(target.Id, target.SourceAddonId is not null));
        var external = target.ExternalId ?? (preferredProvider is not null && target.ExternalIds.TryGetValue(preferredProvider, out var value)
            ? value
            : providerName == "addon" && target.SourceAddonId is Guid addonId
                ? MediaIdentity.AddonExternalId(addonId, target.MediaType, target.ResourceId)
                : MediaIdentity.InferExternalId(target.Id));
        return await _state.Client!.ResolveTitleAsync(new TitleResolveInput
        {
            MediaType = target.MediaType switch { "movie" => TitleResolveMediaType.Movie, "tv" => TitleResolveMediaType.Tv, _ => TitleResolveMediaType.Series },
            Provider = providerName,
            ExternalId = external,
            ResourceId = target.ResourceId,
            Title = target.Title,
            PosterUrl = target.PosterUrl,
            BackgroundUrl = target.BackgroundUrl,
            ReleaseInfo = target.ReleaseInfo,
            Released = target.MediaType == "movie" ? MediaIdentity.NormalizeReleaseDate(target.Released) : null,
            SourceAddonId = target.SourceAddonId,
            SourceCatalogId = target.SourceCatalogId,
            SourceName = target.SourceName,
            Country = target.Country,
            Language = target.Language,
            Category = target.Category,
        }, _state.Token);
    }

    private async Task<Series> LoadSeriesAsync(Guid id)
    {
        try { return await _state.Client!.GetSeriesAsync(id, SeriesMappingProvider.Tmdb, language: MetadataLanguage(), cancellationToken: _state.Token); }
        catch (RivuneServerException) { return await _state.Client!.GetSeriesAsync(id, SeriesMappingProvider.Tvdb, language: MetadataLanguage(), cancellationToken: _state.Token); }
    }

    private async Task LoadSeriesWatchStateAsync(Series series, long generation)
    {
        var seasons = await Task.WhenAll(series.Seasons
            .Where(summary => summary.EpisodeCount > 0)
            .Select(summary => _state.Client!.GetSeasonAsync(
                summary.Id,
                series.MappingProvider,
                language: MetadataLanguage(),
                cancellationToken: _state.Token)));
        if (!_state.IsCurrent(generation)) return;

        var episodes = seasons
            .SelectMany(season => season.Episodes)
            .DistinctBy(episode => episode.Id)
            .ToArray();
        var progress = new Dictionary<Guid, PlaybackProgress?>();
        var batches = await Task.WhenAll(episodes
            .Chunk(100)
            .Select(chunk => _state.Client!.GetPlaybackProgressBatchAsync(
                chunk.Select(episode => episode.Id).ToArray(),
                _state.Token)));
        if (!_state.IsCurrent(generation)) return;
        foreach (var item in batches.SelectMany(batch => batch.Items)) progress[item.TitleId] = item.Progress;

        _seriesEpisodes = episodes;
        _episodeProgress.Clear();
        foreach (var item in progress) _episodeProgress[item.Key] = item.Value;
        _seriesWatchStateReady = true;
    }

    private async Task RenderDetailAsync(long generation)
    {
        var actionVisibility = DetailActionPolicy.For(
            _detailTarget?.MediaType ?? string.Empty,
            _detailSeason is not null,
            _devicePreferences.AutomaticallyShowSources);
        var isEpisode = _detailTarget?.MediaType == "episode";
        var title = isEpisode
            ? _detailTarget?.Title ?? "Episode"
            : _detailMovie?.Title ?? (_detailSeason is not null && _detailSeries is not null ? $"{_detailSeries.Name} · {_detailSeason.Name}" : _detailSeries?.Name) ?? _detailTarget?.Title ?? "Title";
        var overview = isEpisode ? _detailTarget?.Description : _detailMovie?.Overview ?? _detailSeason?.Overview ?? _detailSeries?.Overview ?? _detailTarget?.Description;
        var backdrop = isEpisode ? _detailTarget?.BackgroundUrl ?? _detailTarget?.PosterUrl : _detailMovie?.BackdropUrl ?? _detailSeason?.BackdropUrl ?? _detailSeries?.BackdropUrl ?? _detailTarget?.BackgroundUrl ?? _detailTarget?.PosterUrl;
        DetailTitle.Text = title;
        var tagline = isEpisode ? null : _detailMovie?.Tagline ?? _detailSeries?.Tagline;
        DetailTagline.Text = tagline ?? string.Empty;
        DetailTagline.Visibility = string.IsNullOrWhiteSpace(tagline) ? Visibility.Collapsed : Visibility.Visible;
        if (isEpisode && _detailTarget is { SeasonNumber: int seasonNumber, EpisodeNumber: int episodeNumber })
        {
            DetailEpisodeCoordinates.Text = $"Season {seasonNumber} · Episode {episodeNumber}";
            DetailEpisodeCoordinates.Visibility = Visibility.Visible;
        }
        else
        {
            DetailEpisodeCoordinates.Text = string.Empty;
            DetailEpisodeCoordinates.Visibility = Visibility.Collapsed;
        }
        var rating = isEpisode ? _detailTarget?.Rating : _detailMovie?.VoteAverage ?? _detailSeason?.VoteAverage ?? _detailSeries?.VoteAverage ?? _detailTarget?.Rating;
        var primary = new List<string>();
        var release = isEpisode ? _detailTarget?.ReleaseInfo : _detailMovie?.ReleaseDate ?? _detailSeason?.AirDate ?? _detailSeries?.FirstAirDate ?? _detailTarget?.ReleaseInfo;
        if (!string.IsNullOrWhiteSpace(release)) primary.Add(ViewerDatePresentation.ReleaseDate(release));
        var runtime = _detailMovie?.RuntimeMinutes ?? _detailTarget?.RuntimeMinutes;
        if (runtime is > 0) primary.Add($"{runtime} min");
        if (rating is > 0) primary.Add($"★ {rating:0.0}");
        if (!isEpisode && !string.IsNullOrWhiteSpace(_detailSeries?.Status)) primary.Add(_detailSeries.Status);
        DetailMetadata.Text = string.Join("  ·  ", primary);
        DetailMetadata.Visibility = primary.Count == 0 ? Visibility.Collapsed : Visibility.Visible;
        var secondary = new List<string>();
        if (!isEpisode)
        {
            if (_detailSeason is not null) secondary.Add(_detailSeason.Episodes.Count == 1 ? "1 episode" : $"{_detailSeason.Episodes.Count} episodes");
            else
            {
                if (_detailSeries?.NumberOfSeasons is > 0) secondary.Add($"{_detailSeries.NumberOfSeasons} seasons");
                if (_detailSeries?.NumberOfEpisodes is > 0) secondary.Add($"{_detailSeries.NumberOfEpisodes} episodes");
            }
            var genres = (_detailMovie?.Genres ?? _detailSeries?.Genres ?? []).Select(value => value.Name).ToArray();
            if (genres.Length > 0) secondary.Add(string.Join(" · ", genres));
        }
        DetailSecondaryMetadata.Text = string.Join("  ·  ", secondary);
        DetailSecondaryMetadata.Visibility = secondary.Count == 0 ? Visibility.Collapsed : Visibility.Visible;

        DetailOverview.Text = overview ?? string.Empty;
        DetailOverviewScroller.IsTabStop = false;
        DispatcherQueue.TryEnqueue(() =>
        {
            var scrollable = DetailOverviewScroller.ScrollableHeight > 0;
            DetailOverviewScroller.IsTabStop = scrollable;
            AutomationProperties.SetName(DetailOverviewScroller, scrollable ? "Description, scrollable" : "Description");
        });
        foreach (var button in DetailActions.Items.OfType<ButtonBase>()) ForgetZoomButton(button);
        DetailActions.Items.Clear();
        var seriesWatched = !isEpisode && _detailSeason is null && _detailTarget?.MediaType == "series";
        var watched = seriesWatched
            ? _seriesWatchStateReady && _seriesEpisodes.Count > 0 && _seriesEpisodes.All(value => _episodeProgress.GetValueOrDefault(value.Id)?.Completed == true)
            : _detailSeason is not null && !isEpisode
                ? _detailSeason.Episodes.Count > 0 && _detailSeason.Episodes.All(value => _episodeProgress.GetValueOrDefault(value.Id)?.Completed == true)
                : _detailProgress?.Completed == true;
        if (actionVisibility.Play)
        {
            var label = _detailProgress is { PositionSeconds: > 0, Completed: false } ? "Resume" : "Play";
            var playAction = ActionButton(label, "\uE768", () => OpenSourcesForCurrentDetailAsync());
            if (_detailProgress is { PositionSeconds: > 0, DurationSeconds: > 0, Completed: false } progress)
                AutomationProperties.SetHelpText(playAction, $"Resume at {Math.Clamp(progress.PositionSeconds * 100 / progress.DurationSeconds, 0, 100)} percent");
            DetailActions.Items.Add(playAction);
        }
        Trailer? trailer = null;
        if (_detailReference is not null)
        {
            try
            {
                trailer = (await _state.Client!.GetTrailersAsync(_detailReference.TitleId, language: MetadataLanguage(), seasonNumber: _detailSeason?.SeasonNumber, cancellationToken: _state.Token)).Trailers.FirstOrDefault();
            }
            catch (Exception exception) when (exception is not OperationCanceledException) { }
        }
        if (actionVisibility.Trailer && trailer is not null)
            DetailActions.Items.Add(ActionButton("Trailer", "\uE8B2", async () => await Launcher.LaunchUriAsync(new Uri($"https://www.youtube.com/watch?v={Uri.EscapeDataString(trailer.YoutubeId)}"))));
        if (actionVisibility.Watched && _detailTarget is not null)
        {
            var watchedAction = ActionButton(watched ? "Mark as unwatched" : "Mark as watched", watched ? "\uE890" : "\uE8F5", ToggleWatchedAsync);
            AutomationProperties.SetHelpText(watchedAction, watched ? "Marks this title as not watched" : "Marks this title as watched");
            DetailActions.Items.Add(watchedAction);
        }
        if (actionVisibility.Library)
            DetailActions.Items.Add(ActionToggleButton(_detailInLibrary ? "Remove from library" : "Add to library", _detailInLibrary ? "\uE73E" : "\uE8F1", _detailInLibrary, ToggleLibraryAsync));
        RenderPlaybackCoordinationActions();
        DetailSections.Children.Clear();
        if (_detailSeries is not null && _detailSeason is null)
        {
            DetailSections.Children.Add(new TextBlock { Text = "Seasons", Style = (Style)Application.Current.Resources["RivuneTitleLargeTextStyle"] });
            var row = HorizontalList();
            foreach (var season in _detailSeries.Seasons.Where(value => value.EpisodeCount > 0).OrderBy(value => value.SeasonNumber))
                row.Items.Add(CreateSeasonCard(season));
            DetailSections.Children.Add(row);
        }
        if (_detailSeason is not null && _detailSeries is not null && !isEpisode)
        {
            DetailSections.Children.Add(new TextBlock { Text = "Episodes", Style = (Style)Application.Current.Resources["RivuneTitleLargeTextStyle"] });
            var row = HorizontalList();
            foreach (var episode in _detailSeason.Episodes)
            {
                var target = EpisodeTarget(_detailSeries, episode);
                if (_episodeProgress.GetValueOrDefault(episode.Id) is { } progress)
                    target = target with { ResumePositionSeconds = progress.PositionSeconds, DurationSeconds = progress.DurationSeconds };
                var button = CreateEpisodeCard(target, _episodeProgress.GetValueOrDefault(episode.Id));
                button.Click += EpisodeCard_Click;
                row.Items.Add(button);
            }
            DetailSections.Children.Add(row);
        }
        var cast = _detailMovie?.Cast ?? _detailSeries?.Cast ?? [];
        if (cast.Count > 0)
        {
            DetailSections.Children.Add(new TextBlock { Text = "Cast", Style = (Style)Application.Current.Resources["RivuneTitleLargeTextStyle"] });
            var row = HorizontalList();
            foreach (var member in cast)
            {
                var card = new StackPanel { Width = 120, Spacing = 6, Margin = new Thickness(0, 0, 12, 0) };
                var portrait = new Grid
                {
                    Width = 120,
                    Height = 160,
                    Background = (Brush)Application.Current.Resources["RivuneArtworkFallbackBrush"],
                    CornerRadius = (CornerRadius)Application.Current.Resources["RivuneRadiusMedium"],
                };
                portrait.Children.Add(ArtworkFallback(member.Name));
                if (!string.IsNullOrWhiteSpace(member.ProfileUrl))
                {
                    var image = new Image { Opacity = 0, Stretch = Stretch.UniformToFill };
                    portrait.Children.Add(image);
                    _ = LoadArtworkAsync(image, member.ProfileUrl, generation, _state.Token);
                }
                card.Children.Add(portrait);
                card.Children.Add(new TextBlock { Text = member.Name, Style = (Style)Application.Current.Resources["RivuneLabelLargeTextStyle"], TextWrapping = TextWrapping.Wrap, MaxLines = 2, Height = 40 });
                card.Children.Add(new TextBlock { Text = member.Character, Style = (Style)Application.Current.Resources["RivuneBodySmallTextStyle"], TextWrapping = TextWrapping.Wrap, MaxLines = 2, Height = 36 });
                row.Items.Add(card);
            }
            DetailSections.Children.Add(row);
        }
        DetailStatus.Visibility = Visibility.Collapsed;
        if (!string.IsNullOrWhiteSpace(backdrop)) await LoadArtworkAsync(DetailBackdrop, backdrop, generation, _state.Token);
    }

    private async void EpisodeCard_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: MediaTarget target })
            await OpenEpisodeTargetAsync(target, _devicePreferences.AutomaticallyShowSources, reuseSeasonContext: true);
    }

    private void RenderPlaybackCoordinationActions()
    {
        var deviceFingerprint = string.Join(',', _state.PlaybackDevices
            .OrderBy(device => device.SessionId)
            .Select(device => $"{device.SessionId}:{device.Name}:{string.Join('.', device.Capabilities.Order())}:{device.State.Status}:{device.State.Item?.TitleId}"));
        var room = _state.ActivePlaybackRoom;
        var memberFingerprint = room is null
            ? string.Empty
            : string.Join(',', room.Members.Select(member => member.MemberId).Order(StringComparer.Ordinal));
        var fingerprint = $"{PlaybackCoordinationAvailable}|{_detailTarget?.ResourceId}|{room?.Id}|{room?.Version}|{room?.JoinCode}|{room?.Members.Count}|{memberFingerprint}|{deviceFingerprint}";
        if (fingerprint == _coordinationActionsFingerprint) return;
        _coordinationActionsFingerprint = fingerprint;
        foreach (var button in CoordinationActions.Items.OfType<ButtonBase>()) ForgetZoomButton(button);
        CoordinationActions.Items.Clear();
        if (!PlaybackCoordinationAvailable || _detailTarget is not { MediaType: "movie" or "episode" or "tv" } || _progressTitleId == Guid.Empty)
        {
            CoordinationActionsScroller.Visibility = Visibility.Collapsed;
            return;
        }

        if (room is not null)
        {
            var roomLabel = room.JoinCode is { Length: > 0 } ? $"Room {room.JoinCode}" : "Watch room";
            var status = new Button
            {
                Content = LabeledActionContent($"{roomLabel} · {room.Members.Count} watching", "\uE716"),
                Style = (Style)Application.Current.Resources["RivuneLabeledActionButton"],
                Margin = new Thickness(0, 0, 8, 8),
                IsEnabled = false,
            };
            ApplyLabeledActionPresentation(status, roomLabel);
            CoordinationActions.Items.Add(status);
            CoordinationActions.Items.Add(ActionButton("Leave room", "\uE8BB", LeavePlaybackRoomAsync));
        }
        else
        {
            CoordinationActions.Items.Add(ActionButton("Start watch room", "\uE716", CreatePlaybackRoomAsync));
            CoordinationActions.Items.Add(ActionButton("Join room", "\uE8D4", JoinPlaybackRoomAsync));
        }

        var devices = _state.PlaybackDevices.Where(device => device.Capabilities.Contains("remote-control", StringComparer.Ordinal)).ToArray();
        if (devices.Length > 0)
            CoordinationActions.Items.Add(ActionButton("Send to device", "\uE7F4", ChooseHandoffDeviceAsync));
        foreach (var device in devices.Where(device => device.State.Item is not null))
            CoordinationActions.Items.Add(ActionButton($"Control {device.Name}", "\uE768", () => ShowRemoteControlsAsync(device)));
        CoordinationActionsScroller.Visibility = Visibility.Visible;
    }

    private CoordinatedPlaybackItem CurrentCoordinatedItem()
    {
        var target = _detailTarget ?? throw new InvalidOperationException("No title is selected.");
        return target.CoordinatedItem(_progressTitleId, _detailTitleForPlayback());
    }

    private async Task ChooseHandoffDeviceAsync()
    {
        var devices = _state.PlaybackDevices.Where(device => device.Capabilities.Contains("remote-control", StringComparer.Ordinal)).ToArray();
        var device = await ChooseAsync("Send to device", devices, value => $"{value.Name} · {value.Platform}");
        if (device is null) return;
        await _state.Client!.SendPlaybackCommandAsync(device.SessionId, new PlaybackCommandInput
        {
            Command = "load",
            Item = CurrentCoordinatedItem(),
            PositionMilliseconds = (_detailProgress?.PositionSeconds ?? 0) * 1_000L,
        }, _state.Token);
    }

    private async Task ShowRemoteControlsAsync(PlaybackDevice device)
    {
        var panel = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        var play = new Button { Content = "Play", MinHeight = 48 };
        var pause = new Button { Content = "Pause", MinHeight = 48 };
        var seekBack = new Button { Content = "−10s", MinHeight = 48 };
        var seekForward = new Button { Content = "+10s", MinHeight = 48 };
        var stop = new Button { Content = "Stop", MinHeight = 48 };
        panel.Children.Add(play);
        panel.Children.Add(pause);
        panel.Children.Add(seekBack);
        panel.Children.Add(seekForward);
        panel.Children.Add(stop);
        var dialog = new ContentDialog { XamlRoot = XamlRoot, Title = device.Name, Content = panel, CloseButtonText = "Done" };
        async Task SendAsync(string command, long? position = null)
        {
            try
            {
                await _state.Client!.SendPlaybackCommandAsync(device.SessionId, new PlaybackCommandInput { Command = command, PositionMilliseconds = position }, CancellationToken.None);
                var nextPosition = position ?? device.State.PositionMilliseconds;
                device = device with { State = device.State with { Status = command == "play" ? "playing" : command == "pause" ? "paused" : device.State.Status, PositionMilliseconds = nextPosition } };
            }
            catch (Exception exception)
            {
                dialog.Hide();
                DetailBanner.Severity = InfoBarSeverity.Error;
                DetailBanner.Message = FriendlyError(exception);
                DetailBanner.IsOpen = true;
            }
        }
        play.Click += async (_, _) => await SendAsync("play");
        pause.Click += async (_, _) => await SendAsync("pause");
        seekBack.Click += async (_, _) => await SendAsync("seek", Math.Max(0, device.State.PositionMilliseconds - 10_000));
        seekForward.Click += async (_, _) => await SendAsync("seek", Math.Min(device.State.DurationMilliseconds, device.State.PositionMilliseconds + 10_000));
        stop.Click += async (_, _) => await SendAsync("stop");
        await ShowDialogAsync(dialog);
    }

    private async Task CreatePlaybackRoomAsync()
    {
        var room = await _state.Client!.CreatePlaybackRoomAsync(new PlaybackRoomCreateInput
        {
            Item = CurrentCoordinatedItem(),
            State = "paused",
            PositionMilliseconds = (_detailProgress?.PositionSeconds ?? 0) * 1_000L,
            DurationMilliseconds = (_detailProgress?.DurationSeconds ?? 0) * 1_000L,
        }, _state.Token);
        _state.ActivePlaybackRoom = room;
        RenderPlaybackCoordinationActions();
    }

    private async Task JoinPlaybackRoomAsync()
    {
        var code = new TextBox
        {
            Header = "Room code",
            MaxLength = 10,
            CharacterCasing = CharacterCasing.Upper,
            PlaceholderText = "23456789AB",
            Style = (Style)Application.Current.Resources["RivuneTextField"],
        };
        var dialog = new ContentDialog { XamlRoot = XamlRoot, Title = "Join watch room", Content = code, PrimaryButtonText = "Join", CloseButtonText = "Cancel", DefaultButton = ContentDialogButton.Primary };
        if (await ShowDialogAsync(dialog) != ContentDialogResult.Primary) return;
        var normalized = code.Text.Trim().ToUpperInvariant();
        if (normalized.Length == 0) return;
        var room = await _state.Client!.JoinPlaybackRoomAsync(normalized, _state.Token);
        _state.ActivePlaybackRoom = room;
        RenderPlaybackCoordinationActions();
        await StartCoordinatedPlaybackAsync(room.Item, room.PositionMilliseconds, _state.Client!, _coordinationCancellation?.Token ?? _state.Token);
    }

    private async Task LeavePlaybackRoomAsync()
    {
        var room = _state.ActivePlaybackRoom;
        if (room is null) return;
        _state.ActivePlaybackRoom = null;
        try { await _state.Client!.LeavePlaybackRoomAsync(room.Id, _state.Token); }
        finally { RenderPlaybackCoordinationActions(); }
    }

    private Button ActionButton(string label, string glyph, Func<Task> action)
    {
        var button = new Button
        {
            Content = LabeledActionContent(label, glyph),
            Style = (Style)Application.Current.Resources["RivuneLabeledActionButton"],
            Margin = new Thickness(0, 0, 8, 8),
            Tag = action,
        };
        ApplyLabeledActionPresentation(button, label);
        ConfigureZoomButton(button);
        button.Click += DetailAction_Click;
        return button;
    }

    private ToggleButton ActionToggleButton(string label, string glyph, bool selected, Func<Task> action)
    {
        var button = new ToggleButton
        {
            Content = LabeledActionContent(label, glyph),
            IsChecked = selected,
            Style = (Style)Application.Current.Resources["RivuneLabeledActionToggleButton"],
            Margin = new Thickness(0, 0, 8, 8),
            Tag = action,
        };
        ApplyLabeledActionPresentation(button, label);
        ConfigureZoomButton(button);
        button.Click += DetailAction_Click;
        return button;
    }


    private static StackPanel LabeledActionContent(string label, string glyph)
    {
        var content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, VerticalAlignment = VerticalAlignment.Center };
        content.Children.Add(new FontIcon { Glyph = glyph, FontSize = 20 });
        content.Children.Add(new TextBlock { Text = label, VerticalAlignment = VerticalAlignment.Center });
        return content;
    }

    private void ApplyLabeledActionPresentation(ButtonBase button, string label)
    {
        AutomationProperties.SetName(button, label);
        if (!_tvInputMode) return;
        button.MinHeight = 56;
        button.Padding = new Thickness(16, 0, 16, 0);
        button.CornerRadius = new CornerRadius(28);
        button.FontSize = 16;
    }

    private async void DetailAction_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not ButtonBase { Tag: Func<Task> action } button) return;
        _sourceInvoker = button;
        button.IsEnabled = false;
        DetailBanner.IsOpen = false;
        try { await action(); }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (button is ToggleButton toggle) toggle.IsChecked = !(toggle.IsChecked ?? false);
            DetailBanner.Severity = InfoBarSeverity.Error;
            DetailBanner.Message = FriendlyError(exception);
            DetailBanner.IsOpen = true;
        }
        finally { button.IsEnabled = true; }
    }

    private async void Season_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: SeasonSummary summary } || _detailSeries is null) return;
        var parentBackAction = _detailBackAction;
        var parentRetryAction = _detailRetryAction;
        var generation = _state.Transition(AppPhase.Detail);
        _detailRetryAction = async () =>
        {
            if (_detailSeries is null) return;
            var retryGeneration = _state.Transition(AppPhase.Detail);
            DetailProgress.IsActive = true;
            _detailSeason = await _state.Client!.GetSeasonAsync(summary.Id, _detailSeries.MappingProvider, language: MetadataLanguage(), cancellationToken: _state.Token);
            if (_state.IsCurrent(retryGeneration)) await RenderDetailAsync(retryGeneration);
        };
        DetailProgress.IsActive = true;
        DetailStatus.Visibility = Visibility.Visible;
        try
        {
            _detailSeason = await _state.Client!.GetSeasonAsync(summary.Id, _detailSeries.MappingProvider, language: MetadataLanguage(), cancellationToken: _state.Token);
            if (!_state.IsCurrent(generation)) return;
            _detailBackAction = async () =>
            {
                _detailSeason = null;
                _detailBackAction = parentBackAction;
                _detailRetryAction = parentRetryAction;
                var backGeneration = _state.Transition(AppPhase.Detail);
                await RenderDetailAsync(backGeneration);
            };
            foreach (var chunk in _detailSeason.Episodes.Chunk(100))
            {
                var progressBatch = await _state.Client.GetPlaybackProgressBatchAsync(chunk.Select(value => value.Id).ToArray(), _state.Token);
                foreach (var item in progressBatch.Items) _episodeProgress[item.TitleId] = item.Progress;
            }
            await RenderDetailAsync(generation);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            DetailBanner.Severity = InfoBarSeverity.Error;
            DetailBanner.Message = FriendlyError(exception);
            DetailBanner.IsOpen = true;
            DetailRetryButton.Visibility = Visibility.Visible;
            DetailStatus.Visibility = Visibility.Visible;
        }
        finally { DetailProgress.IsActive = false; }
    }

    private MediaTarget EpisodeTarget(Series series, Episode episode) =>
        episode.ToMediaTarget(series, _detailReference?.ResourceId ?? series.Id.ToString("D"));

    private async Task ToggleLibraryAsync()
    {
        if (_detailReference is null) return;
        if (_detailInLibrary) await _state.Client!.RemoveLibraryTitleAsync(_detailReference.TitleId, _state.Token);
        else await _state.Client!.AddLibraryTitleAsync(_detailReference.TitleId, _state.Token);
        _detailInLibrary = !_detailInLibrary;
        _libraryItems.Clear();
        _libraryPage = 0;
        _libraryTotalPages = 0;
        LibraryResults.Items.Clear();
        await RenderDetailAsync(_state.GenerationId);
    }

    private async Task ToggleWatchedAsync()
    {
        if (_detailTarget is null) return;
        var isEpisode = _detailTarget.MediaType.Equals("episode", StringComparison.OrdinalIgnoreCase);
        if (!isEpisode && _detailSeason is null && _detailTarget.MediaType.Equals("series", StringComparison.OrdinalIgnoreCase))
        {
            if (_detailSeries is null) throw new InvalidOperationException("Series episode data is unavailable.");
            if (!_seriesWatchStateReady) await LoadSeriesWatchStateAsync(_detailSeries, _state.GenerationId);
            await ToggleEpisodesWatchedAsync(_seriesEpisodes);
        }
        else if (_detailSeason is not null && !isEpisode)
        {
            await ToggleEpisodesWatchedAsync(_detailSeason.Episodes);
        }
        else if (_detailProgress?.Completed == true)
        {
            _detailProgress = await _state.Client!.MarkTitleUnwatchedAsync(_progressTitleId, _detailProgress.Version, _state.Token);
        }
        else
        {
            _detailProgress = await _state.Client!.MarkTitleWatchedAsync(_progressTitleId, _detailProgress?.Version ?? 0, _state.Token);
        }
        if (isEpisode && _detailProgress is not null)
            _episodeProgress[_progressTitleId] = _detailProgress;
        await RenderDetailAsync(_state.GenerationId);
    }

    private async Task ToggleEpisodesWatchedAsync(IReadOnlyList<Episode> episodes)
    {
        if (episodes.Count == 0) return;
        var completed = episodes.All(value => _episodeProgress.GetValueOrDefault(value.Id)?.Completed == true);
        foreach (var chunk in episodes.Chunk(100))
        {
            var result = await _state.Client!.SetTitlesWatchedBatchAsync(new SetWatchedBatchRequest
            {
                Items = chunk.Select(episode => new SetWatchedBatchItem
                {
                    TitleId = episode.Id,
                    Completed = !completed,
                    ExpectedVersion = _episodeProgress.GetValueOrDefault(episode.Id)?.Version ?? 0,
                }).ToArray(),
            }, _state.Token);
            foreach (var item in result.Items) _episodeProgress[item.TitleId] = item.Progress;
        }
    }

    private async Task OpenSourcesForCurrentDetailAsync(bool automatic = false)
    {
        if (_detailTarget is null) return;
        _selectedItem = null;
        _playbackTitle = _detailTitleForPlayback();
        var target = _detailTarget;
        _state.CoordinatedItem = target.CoordinatedItem(_progressTitleId, _playbackTitle);
        if (automatic)
        {
            DetailBanner.IsOpen = false;
            DetailRetryButton.Visibility = Visibility.Collapsed;
            DetailStatus.Visibility = Visibility.Collapsed;
        }
        var generation = _state.Transition(AppPhase.Sources);
        SourceTitle.Text = _playbackTitle;
        SourceList.ItemsSource = null;
        SourceList.SelectedItem = null;
        SourceProgress.IsActive = true;
        SourceStatus.Text = "Loading compatible sources…";
        SourceBanner.IsOpen = false;
        PlaySourceButton.IsEnabled = false;
        PlaySourceButton.Visibility = Visibility.Collapsed;
        OpenSourcePicker();
        try
        {
            if (target.MediaType == "episode")
            {
                await LoadSourcesAsync("episode", target.ResourceId, target.TitleId ?? _progressTitleId, generation, target.SourceAddonId);
            }
            else if (_detailReference is not null)
            {
                var mediaType = _detailReference.MediaType == TitleResolveMediaType.Tv ? "tv" : "movie";
                await LoadSourcesAsync(mediaType, _detailReference.ResourceId, _detailReference.TitleId, generation, _detailReference.SourceAddonId, tracksProgress: mediaType != "tv");
            }
            else throw new InvalidOperationException("This title has no playback identity.");
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            SetSourceFailure(exception);
            return;
        }
    }

    private string _detailTitleForPlayback() => _detailTarget is { MediaType: "episode" }
        ? _detailTarget.Title
        : _detailMovie?.Title ?? _detailSeries?.Name ?? _detailTarget?.Title ?? "Playback";

    private async void DetailBack_Click(object sender, RoutedEventArgs e) => await NavigateBackFromDetailAsync();

    private async Task NavigateBackFromDetailAsync()
    {
        if (_detailBackAction is not null) await _detailBackAction();
        else await ReturnToViewerAsync();
    }

    private async Task ReturnToViewerAsync()
    {
        _detailRetryAction = null;
        _state.Transition(AppPhase.Catalogue);
        ShowOnly(DashboardView);
        ShowViewerTab(_selectedViewerTab);
        if (_selectedViewerTab == ViewerTab.Library && _libraryPage == 0) await LoadLibraryAsync(reset: true);
    }

    private void ResetViewerProfileState()
    {
        _viewerProfileId = null;
        _viewerCollections = [];
        _searchDescriptors = [];
        _searchTargets.Clear();
        _searchQuery = string.Empty;
        _searchPage = 0;
        _searchHasMore = false;
        _folderPage = 0;
        _coordinationActionsFingerprint = null;
        _folderHasMore = false;
        _folderItems.Clear();
        _resolvedFolder = null;
        _folderSourceId = null;
        _folderMediaFilter = null;
        _folderArtworkCache.Clear();
        _folderArtworkTasks.Clear();
        _homeFolderTasks.Clear();
        _libraryItems.Clear();
        _libraryType = null;
        _libraryPage = 0;
        _libraryTotalPages = 0;
        _calendarMonth = new DateTime(DateTime.Today.Year, DateTime.Today.Month, 1);
        _heroTarget = null;
        _heroTimer.Stop();
        _heroSlideCancellation?.Cancel();
        _heroTargets = [];
        _continueWatchingTargets = [];
        _heroRotationPaused = false;
        _heroIndex = 0;
        _detailTarget = null;
        _detailReference = null;
        _detailMovie = null;
        _detailSeries = null;
        _detailSeason = null;
        _detailProgress = null;
        _seriesEpisodes = [];
        _seriesWatchStateReady = false;
        _episodeProgress.Clear();
        _effectiveSettings = null;
        _profileSettings = null;
        SearchResults.Items.Clear();
        SearchResultContent.Visibility = Visibility.Collapsed;
        SearchEmpty.Visibility = Visibility.Visible;
        LibraryResults.Items.Clear();
        CalendarEvents.Children.Clear();
        DashboardSections.Children.Clear();
        HeroImage.Source = null;
        HeroImage.Opacity = 0;
        HeroPanel.Visibility = Visibility.Collapsed;
    }

    private void ApplyPersistedProgress(Guid titleId, PlaybackProgress progress)
    {
        if (_detailTarget?.MediaType == "episode" && _detailTarget.TitleId == titleId)
        {
            _detailProgress = progress;
            _episodeProgress[titleId] = progress;
        }
        else if (_detailReference?.TitleId == titleId || _detailTarget?.TitleId == titleId)
        {
            _detailProgress = progress;
        }
    }
    private string MediaTypeLabel(string value) => value.ToLowerInvariant() switch
    {
        "movie" => UiText("Movie", "Film"),
        "series" => UiText("Series", "Série"),
        "season" => UiText("Season", "Saison"),
        "episode" => UiText("Episode", "Épisode"),
        "tv" => UiText("Live TV", "Télévision en direct"),
        _ => value,
    };

    private string? EffectiveMetadataRegion()
    {
        var region = _effectiveSettings?.Settings.MetadataRegion;
        return string.IsNullOrWhiteSpace(region) || region.Equals("auto", StringComparison.OrdinalIgnoreCase)
            ? null
            : region;
    }

    private readonly record struct FolderArtworkKey(Guid CollectionId, Guid FolderId);

    private sealed record FolderArtworkRequest(long Generation, Task<string?> Task);
    private sealed record HomeContinueWatchingResult(ContinueWatchingPage? Page, bool Failed);
    private sealed record HomeRecommendationResult(LocalRecommendationPage? Page, bool Failed);

    private sealed record HomeFolderRequest(long Generation, Task<ResolvedCollectionFolder?> Task);


    private sealed record FolderSelection(Collection Collection, CollectionFolder Folder);
}
