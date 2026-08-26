using System.Runtime.InteropServices.WindowsRuntime;
using System.Security.Cryptography;
using System.Globalization;
using System.Threading;
using MicrosoftDispatcherQueue = Microsoft.UI.Dispatching.DispatcherQueue;
using MicrosoftDispatcherQueueTimer = Microsoft.UI.Dispatching.DispatcherQueueTimer;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Markup;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Microsoft.UI.Xaml.Media.Animation;
using Rivune.App.ViewModels;
using Rivune.Windows;
using Windows.Networking.Connectivity;
using Windows.ApplicationModel.DataTransfer;
using Windows.Media.Core;
using Windows.Media.Playback;
using Windows.Media.Streaming.Adaptive;
using Windows.Storage.Streams;
using Windows.System;

namespace Rivune.App;

public sealed partial class MainPage : Page
{
    private static readonly TimeSpan RestoreTimeout = TimeSpan.FromSeconds(30);
    private static readonly TimeSpan ShutdownTimeout = TimeSpan.FromSeconds(3);
    private readonly MainPageViewModel _state = new();
    private readonly DiagnosticsBuffer _diagnostics = new();
    private long _diagnosticClipboardGeneration;
    private string? _diagnosticClipboardReport;
    private readonly ServerAddressStore _serverAddressStore = new();
    private readonly InstallationIdStore _installationIdStore = new();
    private string? _installationId;
    private readonly LanServerDiscovery _lanDiscovery = new();
    private WindowsDevicePreferencesStore? _devicePreferencesStore;
    private WindowsDevicePreferences _devicePreferences = new();
    private readonly WindowsUpdateNotifier _updateNotifier;
    private string? _devicePreferencesFailure;
    private OfflineMediaStore? _offlineMediaStore;
    private string? _offlineScope;
    private IReadOnlyList<OfflineMediaItem> _offlineItems = [];
    private CancellationTokenSource? _offlineDownloadCancellation;
    private Task? _offlineDownloadTask;
    private OfflinePlaybackServer? _offlinePlaybackServer;
    private OfflineMediaItem? _activeOfflineItem;
    private MediaPlayer _mediaPlayer;
    private readonly MicrosoftDispatcherQueueTimer _positionTimer;
    private readonly MicrosoftDispatcherQueueTimer _chromeTimer;
    private DeviceAuthorizationResponse? _deviceAuthorization;
    private CollectionItem? _selectedItem;
    private string? _playbackTitle;
    private Guid _progressTitleId;
    private bool _tracksProgress;
    private long _progressVersion;
    private int _lastQueuedPosition = -10;
    private bool _playbackCompleted;
    private bool _diagnosticPlaybackActive;
    private readonly PlaybackTimeline _timeline = new();
    private AdaptiveMediaSource? _adaptiveMediaSource;
    private MediaSource? _mediaSource;
    private global::Windows.Web.Http.HttpClient? _mediaHttpClient;
    private LoopbackMediaProxy? _directMediaProxy;
    private InMemoryRandomAccessStream? _subtitleStream;
    private readonly object _progressSync = new();
    private readonly object _endingSync = new();
    private readonly object _stopSync = new();
    private readonly object _shutdownSync = new();
    private readonly SemaphoreSlim _dialogGate = new(1, 1);
    private readonly SemaphoreSlim _profileCoordinationGate = new(1, 1);
    private ContentDialog? _activeDialog;
    private ProgressSnapshot? _pendingProgress;
    private Task _progressDrainTask = Task.CompletedTask;
    private Task _serverAddressOperation = Task.CompletedTask;
    private Task? _endingTask;
    private Task? _playerReturnTask;
    private readonly Dictionary<Guid, Task> _sessionStopTasks = [];
    private Task? _restoreTask;
    private CancellationTokenSource? _playerStartupCancellation;
    private Task? _shutdownTask;
    private CancellationTokenSource? _coordinationCancellation;
    private Task? _coordinationTask;
    private Guid? _lastPlaybackOperationId;
    private readonly PlaybackOperationJournal _playbackOperationJournal = new();
    private readonly CoordinationPollingPolicy _coordinationPollingPolicy = new();
    private bool _coordinationOperationExecuting;
    private bool _windowActive = true;
    private string _coordinationStatus = "idle";
    private long _coordinationPositionMilliseconds;
    private long _coordinationDurationMilliseconds;
    private bool PlaybackCoordinationAvailable =>
        _state.Discovery?.Supports(DiscoveryCapability.PlaybackCoordination) == true &&
        _state.Discovery.Supports(DiscoveryCapability.PlaybackCommandResults);
    private bool LocalRecommendationsAvailable => _state.Discovery?.Supports(DiscoveryCapability.LocalRecommendations) == true;
    private SourceRequest? _sourceRequest;
    private Control? _sourceInvoker;
    private readonly ModalFocusRestore<Control> _sourceModalFocus = new();
    private UIElement? _sourceReturnView;
    private UIElement? _playerReturnView;
    private Task<PlaybackCapabilities>? _playbackCapabilitiesTask;
    private bool _timelineFromPlayer;
    private bool _tvInputMode;
    private readonly HashSet<ButtonBase> _zoomPointerButtons = [];
    private readonly HashSet<ButtonBase> _zoomFocusedButtons = [];
    private bool _closed;
    private bool _offlineOnlySession;
    private string? _startupUpdateError;
    private const VirtualKey MediaNextTrackKey = (VirtualKey)0xB0;
    private const VirtualKey MediaPreviousTrackKey = (VirtualKey)0xB1;
    private const VirtualKey MediaStopKey = (VirtualKey)0xB2;
    private const VirtualKey MediaPlayPauseKey = (VirtualKey)0xB3;

    public MainPage()
    {
        InitializeComponent();
        SourceOverlay.CloseRequested += async (_, _) => await CloseSourcesAsync();
        LocalizeVisualTree(this);
        _diagnostics.Record(DiagnosticEventCode.AppStarted);
        Root.AddHandler(UIElement.PointerMovedEvent, new PointerEventHandler(Root_PointerMoved), handledEventsToo: true);
        ConfigureZoomButton(ConnectButton);
        ConfigureZoomButton(CheckUpdatesButton);
        ConfigureZoomButton(CopyCodeButton);
        ConfigureZoomButton(PairingDisconnectButton);
        ConfigureZoomButton(RefreshProfilesButton);
        ConfigureZoomButton(ProfileLogoutButton);
        ConfigureZoomButton(DetailBackButton);
        ConfigureZoomButton(HeroPlayButton);
        ConfigureZoomButton(HeroInfoButton);
        ConfigureZoomButton(CloseSourcesButton);
        ConfigureZoomButton(RefreshSourcesButton);
        ConfigureZoomButton(PlaySourceButton);
        ConfigureZoomButton(ExternalSourceButton);
        ConfigureZoomButton(DownloadSourceButton);
        try
        {
            _devicePreferencesStore = new WindowsDevicePreferencesStore();
            _devicePreferences = _devicePreferencesStore.Snapshot;
            _updateNotifier = new WindowsUpdateNotifier(() =>
                DispatcherQueue.TryEnqueue(async () => await CheckForUpdatesAsync()));
        }
        catch (Exception exception)
        {
            _devicePreferencesFailure = FriendlyError(exception);
            _updateNotifier = new WindowsUpdateNotifier(() =>
                DispatcherQueue.TryEnqueue(async () => await CheckForUpdatesAsync()));
        }
        try
        {
            _offlineMediaStore = new OfflineMediaStore(
                maximumStoredBytes: _devicePreferences.OfflineQuotaBytes,
                expirationDays: _devicePreferences.OfflineExpirationDays);
            _offlineMediaStore.CleanupExpired();
            RefreshOfflineProfiles();
        }
        catch (Exception exception)
        {
            DisableOfflineStorage(exception);
        }
        InitializeAccentPalette();
        InitializeViewerSurface();
        _mediaPlayer = CreateMediaPlayer();
        PlayerElement.SetMediaPlayer(_mediaPlayer);
        _positionTimer = DispatcherQueue.CreateTimer();
        _positionTimer.Interval = TimeSpan.FromSeconds(1);
        _positionTimer.Tick += PositionTimer_Tick;
        _chromeTimer = DispatcherQueue.CreateTimer();
        _chromeTimer.Interval = TimeSpan.FromSeconds(5);
        _chromeTimer.Tick += (_, _) => TryHidePlayerChrome();
        Loaded += MainPage_Loaded;
        _lanDiscovery.ServersChanged += LanDiscovery_ServersChanged;
        NetworkInformation.NetworkStatusChanged += NetworkStatusChanged;
        Unloaded += MainPage_Unloaded;
    }

    private MediaPlayer CreateMediaPlayer()
    {
        var player = new MediaPlayer();
        player.MediaOpened += MediaPlayer_MediaOpened;
        player.MediaEnded += MediaPlayer_MediaEnded;
        player.MediaFailed += MediaPlayer_MediaFailed;
        player.PlaybackSession.PlaybackStateChanged += PlaybackSession_PlaybackStateChanged;
        return player;
    }

    private void ReleaseMediaPlayer(MediaPlayer player)
    {
        player.MediaOpened -= MediaPlayer_MediaOpened;
        player.MediaEnded -= MediaPlayer_MediaEnded;
        player.MediaFailed -= MediaPlayer_MediaFailed;
        player.PlaybackSession.PlaybackStateChanged -= PlaybackSession_PlaybackStateChanged;
        player.Pause();
        player.Source = null;
        player.Dispose();
    }

    private void Root_PointerMoved(object sender, PointerRoutedEventArgs e)
    {
        foreach (var button in _zoomFocusedButtons.ToArray())
        {
            _zoomFocusedButtons.Remove(button);
            AnimateZoomButtonScale(button);
        }
        if (_tvInputMode)
        {
            _tvInputMode = false;
            VisualStateManager.GoToState(this, "PointerMode", useTransitions: true);
        }
        if (PlayerView.Visibility == Visibility.Visible) RevealPlayerChrome(focusTransport: false);
    }
    private void DetailView_SizeChanged(object sender, SizeChangedEventArgs e)
    {
        DetailBackdropHost.Width = e.NewSize.Width;
        DetailBackdropHost.Height = e.NewSize.Height;
        DetailBackdrop.Width = e.NewSize.Width;
        DetailBackdrop.Height = e.NewSize.Height;
    }



    private void ConfigureZoomButton(ButtonBase button)
    {
        button.PointerEntered += ZoomButton_PointerEntered;
        button.PointerExited += ZoomButton_PointerExited;
        button.GotFocus += ZoomButton_GotFocus;
        button.LostFocus += ZoomButton_LostFocus;
    }

    private void ZoomButton_PointerEntered(object sender, PointerRoutedEventArgs e)
    {
        if (sender is ButtonBase button && _zoomPointerButtons.Add(button)) AnimateZoomButtonScale(button);
    }

    private void ZoomButton_PointerExited(object sender, PointerRoutedEventArgs e)
    {
        if (sender is ButtonBase button && _zoomPointerButtons.Remove(button)) AnimateZoomButtonScale(button);
    }

    private void ZoomButton_GotFocus(object sender, RoutedEventArgs e)
    {
        if (sender is ButtonBase button && (_tvInputMode || button.FocusState == FocusState.Keyboard) && _zoomFocusedButtons.Add(button))
            AnimateZoomButtonScale(button);
    }

    private void ZoomButton_LostFocus(object sender, RoutedEventArgs e)
    {
        if (sender is ButtonBase button && _zoomFocusedButtons.Remove(button)) AnimateZoomButtonScale(button);
    }


    private void AnimateZoomButtonScale(ButtonBase button)
    {
        if (button.RenderTransform is not ScaleTransform transform)
        {
            transform = new ScaleTransform();
            button.RenderTransform = transform;
            button.RenderTransformOrigin = new global::Windows.Foundation.Point(0.5, 0.5);
        }
        var target = _zoomPointerButtons.Contains(button) || _zoomFocusedButtons.Contains(button) ? 1.20 : 1.0;
        var duration = new Duration(DeviceAnimationsEnabled ? TimeSpan.FromMilliseconds(140) : TimeSpan.Zero);
        var easing = new CubicEase { EasingMode = EasingMode.EaseOut };
        var storyboard = new Storyboard();
        foreach (var property in new[] { nameof(ScaleTransform.ScaleX), nameof(ScaleTransform.ScaleY) })
        {
            var animation = new DoubleAnimation { To = target, Duration = duration, EasingFunction = easing };
            Storyboard.SetTarget(animation, transform);
            Storyboard.SetTargetProperty(animation, property);
            storyboard.Children.Add(animation);
        }
        storyboard.Begin();
    }

    private void ForgetZoomButton(ButtonBase button)
    {
        _zoomPointerButtons.Remove(button);
        _zoomFocusedButtons.Remove(button);
        button.PointerEntered -= ZoomButton_PointerEntered;
        button.PointerExited -= ZoomButton_PointerExited;
        button.GotFocus -= ZoomButton_GotFocus;
        button.LostFocus -= ZoomButton_LostFocus;
    }

    public static Visibility PinVisibility(bool hasPin) => hasPin ? Visibility.Visible : Visibility.Collapsed;

    public static string SourceDetails(PlaybackMode? mode, string protocol, string? container) =>
        $"{(mode?.ToString() ?? "AUTO").ToUpperInvariant()} · {protocol.ToUpperInvariant()} · {(container ?? "AUTO").ToUpperInvariant()}";

    private async void MainPage_Loaded(object sender, RoutedEventArgs e)
    {
        if (_startupUpdateError is { } error && !_closed)
        {
            _startupUpdateError = null;
            await ShowUpdateDialogAsync("Could not apply update", error);
        }
        if (!_closed)
        {
            await (_restoreTask ??= RestoreAsync());
            if (!_closed) await RunAutomaticUpdateCheckAsync();
        }
    }

    internal void SetStartupUpdateError(string? message) => _startupUpdateError = message;

    private void MainPage_Unloaded(object sender, RoutedEventArgs e)
    {
        NetworkInformation.NetworkStatusChanged -= NetworkStatusChanged;
        if (!_closed) _ = CloseForWindowShutdownAsync();
    }

    private void NetworkStatusChanged(object sender)
    {
        if (!_devicePreferences.DownloadOnMobile && CurrentNetworkClass() == NetworkClass.Mobile)
            _offlineDownloadCancellation?.Cancel();
    }

    private async Task RestoreAsync()
    {
        ShowOnly(BootView);
        BootStatus.Text = "Restoring secure session…";
        var generation = _state.Transition(AppPhase.Restoring);
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(_state.Token);
        deadline.CancelAfter(RestoreTimeout);

        try
        {
            var saved = await _serverAddressStore.LoadAsync(deadline.Token);
            if (!_state.IsCurrent(generation)) return;
            if (string.IsNullOrWhiteSpace(saved))
            {
                ShowServer();
                return;
            }

            _diagnostics.Record(DiagnosticEventCode.ServerConnectionStarted);
            if (!Uri.TryCreate(saved, UriKind.Absolute, out var server))
                throw new InvalidServerUrlException(saved);
            var client = new RivuneApiClient(server);
            ServerAddressBox.Text = server.GetLeftPart(UriPartial.Authority);
            _state.Client = client;
            _state.Discovery = await client.DiscoverAsync(deadline.Token);
            if (!_state.IsCurrent(generation)) return;
            if (_state.Discovery.SetupRequired)
            {
                _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
                var addressCleared = await ClearSavedServerAsync();
                if (!_state.IsCurrent(generation)) return;
                _state.ResetServer();
                ShowServer(addressCleared
                    ? "This Rivune server must finish setup before you can connect."
                    : "This Rivune server must finish setup before you can connect. The saved address could not be removed; fix local file access before restarting Rivune.");
                return;
            }
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionSucceeded);
            var restored = await client.RestoreSessionAsync(deadline.Token);
            if (!_state.IsCurrent(generation)) return;
            if (!restored)
            {
                await StartPairingAsync();
                return;
            }

            BootStatus.Text = "Validating secure session…";
            try
            {
                _state.Account = await client.GetCurrentAccountAsync(deadline.Token);
                if (!_state.IsCurrent(generation)) return;
                if (_state.Account.Session.AuthorizationScope != AuthorizationScope.Category)
                {
                    await DisconnectCoreAsync(clearAddress: false);
                    return;
                }
                if (_state.Account.Session.ActiveProfile is not null)
                {
                    _state.Profile = _state.Account.Profiles.FirstOrDefault(p =>
                        p.Id == _state.Account.Session.ActiveProfile.Id && p.Accessible && p.Enabled);
                    if (_state.Profile is not null)
                    {
                        RestoreOfflineProfile(_state.Profile);
                        await ShowDashboardAsync();
                        return;
                    }
                }
                await ShowProfilesAsync(_state.Account.Profiles);
            }
            catch (Exception exception) when (IsAuthenticationFailure(exception))
            {
                await StartPairingAsync();
            }
        }
        catch (OperationCanceledException) when (_state.IsCurrent(generation) && deadline.IsCancellationRequested)
        {
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
            _state.ResetServer();
            ShowServer("Restoring the saved session timed out. Check the server address and try again.");
        }
        catch (OperationCanceledException) when (_state.IsCurrent(generation))
        {
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
            _state.ResetServer();
            ShowServer("Session restore was cancelled. Try connecting again.");
        }
        catch (OperationCanceledException) { }
        catch (InvalidServerUrlException exception)
        {
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
            if (!_state.IsCurrent(generation)) return;
            var addressCleared = await ClearSavedServerAsync();
            _state.ResetServer();
            ServerAddressBox.Text = string.Empty;
            ShowServer(addressCleared
                ? UiText(FriendlyError(exception))
                : UiFormat("{0} The invalid saved address could not be removed; fix local file access before restarting Rivune.", UiText(FriendlyError(exception))));
        }
        catch (Exception exception)
        {
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
            if (!_state.IsCurrent(generation)) return;
            _state.ResetServer();
            ShowServer(FriendlyError(exception));
        }
    }

    private async void Connect_Click(object sender, RoutedEventArgs e) => await ConnectAsync();
    private void DiscoverServers_Click(object sender, RoutedEventArgs e)
    {
        DiscoverServersButtonLabel.Text = "Refreshing nearby servers…";
        _lanDiscovery.Start();
    }

    private void LanDiscovery_ServersChanged(object? sender, IReadOnlyList<DiscoveredRivuneServer> servers)
    {
        _ = DispatcherQueue.TryEnqueue(() =>
        {
            if (_closed) return;
            DiscoveredServersList.ItemsSource = servers;
            DiscoveredServersPanel.Visibility = servers.Count == 0 ? Visibility.Collapsed : Visibility.Visible;
            DiscoverServersButtonLabel.Text = servers.Count == 0 ? "Find servers on this network" : "Refresh nearby servers";
        });
    }

    private async void DiscoveredServersList_ItemClick(object sender, ItemClickEventArgs e)
    {
        if (e.ClickedItem is not DiscoveredRivuneServer server || _closed) return;
        var transport = server.UsesSecureTransport
            ? "Encrypted HTTPS connection."
            : "Unencrypted HTTP. Continue only on a trusted private network.";
        var dialog = Dialog(
            UiFormat("Connect to {0}?", server.Name),
            UiFormat("{0}\n\n{1}", server.Address.GetLeftPart(UriPartial.Authority), UiText(transport)),
            "Connect");
        if (await ShowDialogAsync(dialog) != ContentDialogResult.Primary || _closed) return;
        ServerAddressBox.Text = server.Address.GetLeftPart(UriPartial.Authority);
        await ConnectAsync();
    }

    private async void ServerAddressBox_KeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter)
        {
            e.Handled = true;
            await ConnectAsync();
        }
    }

    private async Task ConnectAsync()
    {
        ServerError.IsOpen = false;
        var value = ServerAddressNormalizer.Normalize(ServerAddressBox.Text);
        ServerAddressBox.Text = value;
        if (!Uri.TryCreate(value, UriKind.Absolute, out var server) ||
            !string.IsNullOrEmpty(server.UserInfo) ||
            !TrustedLocalTransport.IsAllowedServerUri(server))
        {
            ShowServer("Use HTTPS, or HTTP only with localhost or a literal trusted-private address.");
            ServerAddressBox.Focus(FocusState.Programmatic);
            return;
        }

        _diagnostics.Record(DiagnosticEventCode.ServerConnectionStarted);
        ConnectButton.IsEnabled = false;
        ConnectButtonLabel.Text = "Connecting…";
        try
        {
            _state.ResetServer();
            var generation = _state.GenerationId;
            var client = new RivuneApiClient(server);
            _state.Client = client;
            _state.Discovery = await client.DiscoverAsync(_state.Token);
            if (!_state.IsCurrent(generation)) return;
            if (_state.Discovery.SetupRequired)
            {
                _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
                var addressCleared = await ClearSavedServerAsync();
                if (!_state.IsCurrent(generation)) return;
                _state.ResetServer();
                ShowServer(addressCleared
                    ? "This Rivune server must finish setup before you can connect."
                    : "This Rivune server must finish setup before you can connect. The previously saved address could not be removed.");
                return;
            }
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionSucceeded);
            _serverAddressOperation = _serverAddressStore.SaveAsync(server.GetLeftPart(UriPartial.Authority), _state.Token);
            await _serverAddressOperation;
            await StartPairingAsync();
        }
        catch (OperationCanceledException)
        {
            if (!_closed) _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
        }
        catch (Exception exception)
        {
            _diagnostics.Record(DiagnosticEventCode.ServerConnectionFailed);
            ShowServer(FriendlyError(exception));
        }
        finally
        {
            ConnectButton.IsEnabled = true;
            ConnectButtonLabel.Text = "Continue";
        }
    }


    private async void UseAnotherServer_Click(object sender, RoutedEventArgs e) =>
        await DisconnectCoreAsync(clearAddress: true);

    private async Task<bool> ClearSavedServerAsync()
    {
        try
        {
            _serverAddressOperation = _serverAddressStore.ClearAsync(CancellationToken.None);
            await _serverAddressOperation;
            return true;
        }
        catch (Exception exception)
        {
            ServerError.Message = UiFormat("The saved server address could not be removed. {0}", exception.Message);
            ServerError.IsOpen = true;
            return false;
        }
    }

    private void ShowServer(string? error = null)
    {
        _state.Transition(AppPhase.Server);
        _lanDiscovery.Start();
        DiscoverServersButtonLabel.Text = "Find servers on this network";
        ServerPanel.Visibility = Visibility.Visible;
        PairingPanel.Visibility = Visibility.Collapsed;
        ShowOnly(AuthView);
        ServerError.Message = error ?? string.Empty;
        ServerError.IsOpen = !string.IsNullOrWhiteSpace(error);
        RefreshOfflineProfiles();
        ServerAddressBox.Focus(FocusState.Programmatic);
    }

    private async Task StartPairingAsync(string? preservedFailure = null)
    {
        _lanDiscovery.Stop();
        var client = _state.Client ?? throw new InvalidOperationException("No server connection is active.");
        var generation = _state.Transition(AppPhase.Pairing);
        _deviceAuthorization = null;
        ShowOnly(AuthView);
        ServerPanel.Visibility = Visibility.Collapsed;
        PairingPanel.Visibility = Visibility.Visible;
        PairingError.Message = preservedFailure ?? string.Empty;
        PairingError.IsOpen = !string.IsNullOrWhiteSpace(preservedFailure);
        PairingProgress.IsActive = true;
        NewCodeButton.Visibility = Visibility.Collapsed;
        PairingActions.Visibility = Visibility.Visible;
        CopyCodeButton.IsEnabled = false;
        PairingExpiry.Text = string.Empty;
        PairingExpiry.Visibility = Visibility.Collapsed;
        PairingStatus.Text = "Requesting a one-time code…";
        UserCodeText.Text = string.Empty;
        try
        {
            _deviceAuthorization = await client.BeginDeviceAuthorizationAsync(
                _installationId ??= _installationIdStore.LoadOrCreate(),
                Environment.MachineName,
                "windows",
                _state.Token);
            if (!_state.IsCurrent(generation)) return;
            UserCodeText.Text = _deviceAuthorization.UserCode;
            CopyCodeButton.IsEnabled = true;
            PairingStatus.Text = "Waiting for authorization…";
            PairingExpiry.Text = ExpiryText(_deviceAuthorization.ExpiresAt);
            PairingExpiry.Visibility = Visibility.Visible;
            if (_tvInputMode) CopyCodeButton.Focus(FocusState.Programmatic);
            await PollPairingAsync(generation, _deviceAuthorization);
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            SetPairingFailure(exception);
        }
    }

    private async Task PollPairingAsync(long generation, DeviceAuthorizationResponse authorization)
    {
        var interval = TimeSpan.FromSeconds(Math.Max(1, authorization.IntervalSeconds));
        var expiry = ParseDate(authorization.ExpiresAt);
        while (_state.IsCurrent(generation))
        {
            PairingExpiry.Text = ExpiryText(authorization.ExpiresAt);
            var delay = expiry - DateTimeOffset.UtcNow;
            if (delay <= TimeSpan.Zero)
            {
                SetPairingExpired();
                return;
            }
            await Task.Delay(interval < delay ? interval : delay, _state.Token);
            if (DateTimeOffset.UtcNow >= expiry)
            {
                SetPairingExpired();
                return;
            }
            try
            {
                await _state.Client!.ExchangeDeviceAuthorizationAsync(authorization.DeviceCode, _state.Token);
                if (!_state.IsCurrent(generation)) return;
                PairingStatus.Text = "Authorized. Loading profiles…";
                PairingProgress.IsActive = false;
                _state.Account = await _state.Client.GetCurrentAccountAsync(_state.Token);
                if (!_state.IsCurrent(generation)) return;
                if (_state.Account.Session.AuthorizationScope != AuthorizationScope.Category)
                {
                    await DisconnectCoreAsync(clearAddress: false);
                    return;
                }
                await ShowProfilesAsync(_state.Account.Profiles);
                return;
            }
            catch (RivuneServerException exception) when (exception.Code == "authorization_pending")
            {
                PairingStatus.Text = "Waiting for authorization…";
            }
            catch (RivuneServerException exception) when (exception.Code == "slow_down")
            {
                interval = exception.RetryAfter ?? interval + TimeSpan.FromSeconds(5);
                PairingStatus.Text = "The server asked us to check less often. Still waiting…";
            }
            catch (RivuneServerException exception) when (exception.Code == "expired_device_code")
            {
                SetPairingExpired();
                return;
            }
        }
    }

    private void SetPairingExpired()
    {
        PairingProgress.IsActive = false;
        CopyCodeButton.IsEnabled = false;
        PairingStatus.Text = "Authorization code expired.";
        PairingError.Message = "This one-time code expired. Generate a new code to continue.";
        PairingError.IsOpen = true;
        NewCodeButton.Visibility = Visibility.Visible;
        NewCodeButton.Focus(FocusState.Programmatic);
    }

    private void SetPairingFailure(Exception exception)
    {
        PairingProgress.IsActive = false;
        PairingStatus.Text = "Authorization paused.";
        CopyCodeButton.IsEnabled = false;
        PairingError.Message = FriendlyError(exception);
        PairingError.IsOpen = true;
        NewCodeButton.Visibility = Visibility.Visible;
    }

    private async void NewCode_Click(object sender, RoutedEventArgs e) => await StartPairingAsync();

    private void CopyCode_Click(object sender, RoutedEventArgs e)
    {
        if (string.IsNullOrWhiteSpace(_deviceAuthorization?.UserCode)) return;
        var package = new DataPackage();
        package.SetText(_deviceAuthorization.UserCode);
        Clipboard.SetContent(package);
        PairingStatus.Text = "Code copied. Waiting for authorization…";
    }


    private async void ChangeServer_Click(object sender, RoutedEventArgs e)
    {
        var dialog = Dialog("Change server?", "The pending authorization code will be cancelled.", "Change server");
        if (await ShowDialogAsync(dialog) == ContentDialogResult.Primary)
        {
            await DisconnectCoreAsync(clearAddress: true);
        }
    }

    private async Task ShowProfilesAsync(IReadOnlyList<Profile>? existing = null)
    {
        var roomLeaveFailure = await AbandonPlaybackRoomAsync();
        var generation = _state.Transition(AppPhase.Profiles);
        ShowOnly(ProfileView);
        ProfileBanner.IsOpen = false;
        if (roomLeaveFailure is not null)
        {
            ProfileBanner.Severity = InfoBarSeverity.Warning;
            ProfileBanner.Message = UiFormat("The previous watch room could not be closed: {0}", FriendlyError(roomLeaveFailure));
            ProfileBanner.IsOpen = true;
        }
        ProfileProgress.IsActive = true;
        ProfileLoadingStatus.Visibility = Visibility.Visible;
        try
        {
            var profiles = existing ?? await _state.Client!.GetProfilesAsync(_state.Token);
            if (!_state.IsCurrent(generation)) return;
            PopulateProfiles(profiles, generation);
            ProfileEmpty.Visibility = profiles.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
            if (_state.Account?.Maintenance.Enabled == true)
            {
                ProfileBanner.Severity = InfoBarSeverity.Warning;
                ProfileBanner.Message = _state.Account.Maintenance.Message ?? "The server is in maintenance mode.";
                ProfileBanner.IsOpen = true;
            }
            if (_tvInputMode)
            {
                var target = ProfileGrid.Items.OfType<Button>().FirstOrDefault(button => button.IsEnabled) ?? RefreshProfilesButton;
                DispatcherQueue.TryEnqueue(() => target.Focus(FocusState.Programmatic));
            }
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            ProfileBanner.Severity = InfoBarSeverity.Error;
            ProfileBanner.Message = FriendlyError(exception);
            ProfileBanner.IsOpen = true;
        }
        finally
        {
            if (_state.IsCurrent(generation))
            {
                ProfileProgress.IsActive = false;
                ProfileLoadingStatus.Visibility = Visibility.Collapsed;
            }
        }
    }

    private async void RefreshProfiles_Click(object sender, RoutedEventArgs e) => await ShowProfilesAsync();

    private async void ProfileGrid_ItemClick(object sender, ItemClickEventArgs e)
    {
        if (e.ClickedItem is Profile profile) await ActivateProfileAsync(profile);
        else if (e.ClickedItem is Button { Tag: Profile cardProfile }) await ActivateProfileAsync(cardProfile);
    }

    private async Task PromptForPinAsync(Profile profile)
    {
        var pin = new PasswordBox
        {
            Header = "PIN",
            MaxLength = 8,
            PasswordChar = "●",
            FlowDirection = FlowDirection.LeftToRight,
            InputScope = new InputScope { Names = { new InputScopeName(InputScopeNameValue.Number) } },
        };
        var error = new InfoBar { IsOpen = false, IsClosable = false, Severity = InfoBarSeverity.Error };
        var checking = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Visibility = Visibility.Collapsed };
        checking.Children.Add(new ProgressRing { IsActive = true, Width = 20, Height = 20 });
        checking.Children.Add(new TextBlock { Text = "Checking…", VerticalAlignment = VerticalAlignment.Center });
        var panel = new StackPanel { Spacing = 12 };
        panel.Children.Add(new TextBlock { Text = UiFormat("Unlock {0} to continue.", profile.Name), TextWrapping = TextWrapping.Wrap });
        panel.Children.Add(pin);
        panel.Children.Add(checking);
        panel.Children.Add(error);
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = "Enter profile PIN",
            Content = panel,
            PrimaryButtonText = "Unlock",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
            IsPrimaryButtonEnabled = false,
        };
        var filtering = false;
        var busy = false;
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
            dialog.IsPrimaryButtonEnabled = !busy && digits.Length is >= 4 and <= 8;
            error.IsOpen = false;
        };
        dialog.CloseButtonClick += (_, args) => args.Cancel = busy;
        dialog.PrimaryButtonClick += async (_, args) =>
        {
            var deferral = args.GetDeferral();
            args.Cancel = true;
            var normalized = new string(pin.Password.Where(char.IsDigit).ToArray());
            busy = true;
            pin.IsEnabled = false;
            dialog.IsPrimaryButtonEnabled = false;
            dialog.PrimaryButtonText = "Checking…";
            checking.Visibility = Visibility.Visible;
            error.IsOpen = false;
            try
            {
                if (await SelectProfileAsync(profile, normalized)) args.Cancel = false;
                else if (!error.IsOpen)
                {
                    error.Message = ProfileBanner.IsOpen ? ProfileBanner.Message : "The profile could not be unlocked. Try again.";
                    error.IsOpen = true;
                }
            }
            catch (RivuneServerException exception) when (exception.Code is "invalid_profile_pin" or "profile_pin_rate_limited")
            {
                error.Message = exception.Code == "profile_pin_rate_limited"
                    ? "Too many PIN attempts. Wait a moment before trying again."
                    : "That PIN was not accepted.";
                error.IsOpen = true;
            }
            finally
            {
                normalized = string.Empty;
                filtering = true;
                pin.Password = string.Empty;
                filtering = false;
                busy = false;
                dialog.IsPrimaryButtonEnabled = false;
                pin.IsEnabled = true;
                dialog.PrimaryButtonText = "Unlock";
                checking.Visibility = Visibility.Collapsed;
                deferral.Complete();
            }
        };
        try { await ShowDialogAsync(dialog); }
        finally { pin.Password = string.Empty; }
    }

    private async Task<bool> SelectProfileAsync(Profile profile, string? pin)
    {
        if (!profile.Accessible || !profile.Enabled)
        {
            ProfileBanner.Severity = InfoBarSeverity.Warning;
            ProfileBanner.Message = "This profile is not currently accessible.";
            ProfileBanner.IsOpen = true;
            return false;
        }

        var generation = _state.GenerationId;
        var client = _state.Client ?? throw new InvalidOperationException("No server connection is active.");
        ProfileBanner.IsOpen = false;
        ProfileProgress.IsActive = true;
        try
        {
            var selection = await client.SelectProfileAsync(profile.Id, pin, _state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return false;
            _state.Profile = selection.Profile;
            RegisterOfflineProfile(selection.Profile, pin);
            await ShowDashboardAsync();
            return true;
        }
        catch (RivuneServerException exception) when (exception.Code is "invalid_profile_pin" or "profile_pin_rate_limited")
        {
            throw;
        }
        catch (OperationCanceledException) { return false; }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return false;
            ProfileBanner.Severity = InfoBarSeverity.Error;
            ProfileBanner.Message = FriendlyError(exception);
            ProfileBanner.IsOpen = true;
            return false;
        }
        finally
        {
            if (_state.IsCurrent(generation)) ProfileProgress.IsActive = false;
        }
    }

    private async Task ShowDashboardAsync()
    {
        var profileChanged = _viewerProfileId != _state.Profile?.Id;
        if (profileChanged)
        {
            ResetViewerProfileState();
            _viewerProfileId = _state.Profile?.Id;
            _selectedViewerTab = _devicePreferences.StartupTab;
        }
        var profileInitial = ProfileInitial(_state.Profile?.Name);
        CompactProfileInitial.Text = profileInitial;
        DockProfileInitial.Text = profileInitial;
        SetOnlineNavigationEnabled(true);
        await OfferOfflineUnlockForActiveProfileAsync();
        var profileName = _state.Profile?.Name ?? "profile";
        AutomationProperties.SetName(ProfileMenuButton, UiFormat("Account for {0}", profileName));
        AutomationProperties.SetName(DockAccountButton, UiFormat("Account for {0}", profileName));
        CompactProfileImage.Source = null;
        CompactProfileInitial.Opacity = 1;
        CompactProfileImage.Opacity = 0;
        DockProfileImage.Source = null;
        DockProfileInitial.Opacity = 1;
        DockProfileImage.Opacity = 0;
        ShowOnly(DashboardView);
        if (_effectiveSettings is null) await LoadProfileSettingsAsync();
        ApplyInterfaceLanguage();
        ShowViewerTab(_selectedViewerTab);
        await SelectViewerTabAsync(_selectedViewerTab);
        StartPlaybackCoordination();
        var generation = _state.GenerationId;
        if (_state.Profile is { } profile) _ = LoadShellProfileAvatarAsync(profile, generation);
        if (_tvInputMode)
        {
            var navigationTarget = _selectedViewerTab switch
            {
                ViewerTab.Search => SearchNav,
                ViewerTab.Library => LibraryNav,
                ViewerTab.Calendar => CalendarNav,
                _ => HomeNav,
            };
            navigationTarget.Focus(FocusState.Programmatic);
        }
    }


    private async Task<bool> LoadArtworkAsync(Image image, string value, long generation, CancellationToken cancellationToken)
    {
        var client = _state.Client;
        if (client is null) return false;
        try
        {
            var bytes = await client.DownloadSameOriginResourceAsync(value, cancellationToken);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return false;
            using var stream = new InMemoryRandomAccessStream();
            using (var writer = new DataWriter(stream))
            {
                writer.WriteBytes(bytes);
                await writer.StoreAsync();
                writer.DetachStream();
            }
            stream.Seek(0);

            ImageSource source;
            if (LooksLikeSvgArtwork(bytes))
            {
                var svg = new SvgImageSource();
                if (await svg.SetSourceAsync(stream) != SvgImageSourceLoadStatus.Success) return false;
                source = svg;
            }
            else
            {
                var bitmap = new BitmapImage();
                await bitmap.SetSourceAsync(stream);
                source = bitmap;
            }

            if (!_state.IsCurrent(generation) ||
                !ReferenceEquals(client, _state.Client) ||
                cancellationToken.IsCancellationRequested)
                return false;
            image.Source = source;
            image.Opacity = 1;
            return true;
        }
        catch (OperationCanceledException) { return false; }
        catch { return false; }
    }

    private static bool LooksLikeSvgArtwork(ReadOnlySpan<byte> bytes)
    {
        var index = bytes.Length >= 3 && bytes[0] == 0xef && bytes[1] == 0xbb && bytes[2] == 0xbf ? 3 : 0;
        while (index < bytes.Length && bytes[index] is (byte)' ' or (byte)'\t' or (byte)'\r' or (byte)'\n') index++;
        if (IsSvgStartTag(bytes, index)) return true;
        if (!bytes[index..].StartsWith("<?xml"u8)) return false;

        var limit = Math.Min(bytes.Length, 1024);
        for (; index <= limit - 4; index++)
        {
            if (IsSvgStartTag(bytes[..limit], index)) return true;
        }
        return false;
    }

    private static bool IsSvgStartTag(ReadOnlySpan<byte> bytes, int index) =>
        index <= bytes.Length - 4 &&
        bytes[index] == '<' &&
        (bytes[index + 1] == 's' || bytes[index + 1] == 'S') &&
        (bytes[index + 2] == 'v' || bytes[index + 2] == 'V') &&
        (bytes[index + 3] == 'g' || bytes[index + 3] == 'G') &&
        (index == bytes.Length - 4 || bytes[index + 4] is (byte)' ' or (byte)'\t' or (byte)'\r' or (byte)'\n' or (byte)'>' or (byte)'/');

    private static TextBlock ArtworkFallback(string title) => new()
    {
        Text = string.Concat(title.Split(' ', StringSplitOptions.RemoveEmptyEntries).Take(2).Select(part => char.ToUpperInvariant(part[0]))),
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
        FontSize = 28,
        FontWeight = Microsoft.UI.Text.FontWeights.Bold,
    };


    private async Task<T?> ChooseAsync<T>(string title, IReadOnlyList<T> values, Func<T, string> label) where T : class
    {
        var list = new ListView { SelectionMode = ListViewSelectionMode.Single, MaxHeight = 420 };
        foreach (var value in values) list.Items.Add(new ListViewItem { Content = label(value), Tag = value, MinHeight = 48 });
        var dialog = new ContentDialog { XamlRoot = XamlRoot, Title = title, Content = list, PrimaryButtonText = "Continue", CloseButtonText = "Cancel", DefaultButton = ContentDialogButton.Primary };
        return await ShowDialogAsync(dialog) == ContentDialogResult.Primary && list.SelectedItem is ListViewItem { Tag: T selected } ? selected : null;
    }

    private async Task LoadSourcesAsync(string mediaType, string resourceId, Guid titleId, long generation, Guid? addonId = null, bool tracksProgress = true)
    {
        var request = new SourceRequest(mediaType, resourceId, titleId, addonId, tracksProgress);
        _sourceRequest = request;
        var client = _state.Client ?? throw new InvalidOperationException("No server connection is active.");
        PlaybackDecisionReasons.Text = string.Empty;
        PlaybackDecisionReasons.Visibility = Visibility.Collapsed;
        SourceStatus.Text = "Loading compatible sources…";
        _externalPlayersTask ??= Task.Run(DetectExternalPlayers);
        _playbackCapabilitiesTask ??= DetectPlaybackCapabilitiesAsync(_externalPlayersTask);
        var detectedCapabilities = await _playbackCapabilitiesTask;
        var capabilities = ApplyNetworkQuality(detectedCapabilities);
        var sources = await client.GetPlaybackSourcesAsync(mediaType, resourceId, capabilities, addonId, _state.Token);
        if (!_state.IsCurrent(generation)) return;
        _progressTitleId = titleId;
        _tracksProgress = tracksProgress;
        var progress = tracksProgress ? await client.GetPlaybackProgressAsync(titleId, _state.Token) : null;
        if (!_state.IsCurrent(generation)) return;
        _progressVersion = progress?.Version ?? 0;
        UpdateSourceOptions(sources.Sources);
        if (_autoStartNextEpisode)
        {
            if (sources.Sources.Count == 0) _autoStartNextEpisode = false;
            else
            {
                var automatic = sources.Sources.FirstOrDefault(source => source.AddonId == _preferredSourceAddonId) ?? sources.Sources[0];
                await PrepareSourceAsync(automatic);
                return;
            }
        }
        SourceProgress.IsActive = false;
        SourceStatus.Text = sources.Sources.Count == 0 ? "No streams found." : string.Empty;
        if (sources.ProviderErrors.Count > 0)
        {
            SourceBanner.Severity = InfoBarSeverity.Warning;
            SourceBanner.Message = ProviderFailureMessage(sources.ProviderErrors, sources.Sources.Count == 0);
            SourceBanner.IsOpen = true;
        }
        else if (sources.Sources.Count == 0)
        {
            SourceBanner.Severity = InfoBarSeverity.Informational;
            SourceBanner.Message = UiFormat("No enabled source add-on returned a stream for media ID “{0}”.", resourceId);
            SourceBanner.IsOpen = true;
        }
        else
        {
            SourceBanner.IsOpen = false;
        }
    }

    private string ProviderFailureMessage(IReadOnlyList<PlaybackProviderError> errors, bool noSources)
    {
        var details = string.Join(" · ", errors.Take(3).Select(error => UiFormat("{0}: {1}", error.ManifestId, error.Message)));
        if (errors.Count > 3) details += " · " + UiFormat("{0} more", errors.Count - 3);
        return $"{UiText(noSources ? "Source providers failed" : "Some source providers failed")}: {details}";
    }

    private async void SourceList_ItemClick(object sender, ItemClickEventArgs e)
    {
        if (e.ClickedItem is PlaybackSourceOption source) await PrepareSourceAsync(source);
    }

    private async Task PrepareSourceAsync(PlaybackSourceOption source)
    {
        var client = _state.Client;
        if (client is null) return;
        var generation = _state.Transition(AppPhase.Sources);
        _state.SelectedSource = source;
        _state.Preparation = null;
        PlaySourceButton.IsEnabled = false;
        ExternalSourceButton.IsEnabled = false;
        ExternalSourceButton.Visibility = SupportsExternalPlayer(source) ? Visibility.Visible : Visibility.Collapsed;
        SourceProgress.IsActive = true;
        SourceStatus.Text = UiFormat("Preparing {0}…", source.Name);
        try
        {
            var progress = _tracksProgress ? await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token) : null;
            var startSeconds = progress is null
                ? (int?)null
                : PlaybackProgressPolicy.StartSeconds(progress.PositionSeconds, progress.Completed);
            var preparation = await client.PreparePlaybackAsync(source.SourceRef, startSeconds, _state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(_state.SelectedSource, source)) return;
            _state.Preparation = preparation;
            SourceStatus.Text = string.Join(" · ", new[]
            {
                UiFormat("Ready · {0} · {1} · {2}", preparation.Mode, preparation.Protocol, preparation.Container ?? UiText("Automatic")),
                PlaybackDecisionPresentation.Summary(preparation.Decision),
            }.Where(value => !string.IsNullOrWhiteSpace(value)));
            PlaybackDecisionReasons.Text = PlaybackDecisionPresentation.Summary(preparation.Decision);
            PlaybackDecisionReasons.Visibility = string.IsNullOrEmpty(PlaybackDecisionReasons.Text) ? Visibility.Collapsed : Visibility.Visible;
            PlaySourceButton.IsEnabled = true;
            PlaySourceButton.Visibility = Visibility.Visible;
            ExternalSourceButton.IsEnabled = SupportsExternalPlayer(source);
            ExternalSourceButton.Visibility = SupportsExternalPlayer(source) ? Visibility.Visible : Visibility.Collapsed;
            var downloadable = !preparation.Protocol.Equals("hls", StringComparison.OrdinalIgnoreCase) &&
                !preparation.Protocol.Equals("dash", StringComparison.OrdinalIgnoreCase) &&
                _offlineMediaStore is not null && _offlineScope is not null;
            var downloading = _offlineDownloadTask is { IsCompleted: false };
            DownloadSourceButton.IsEnabled = downloading || downloadable;
            DownloadSourceButton.Visibility = downloading || downloadable ? Visibility.Visible : Visibility.Collapsed;
            if (!downloading) DownloadSourceLabel.Text = "Download";
            if (_autoStartNextEpisode)
            {
                _autoStartNextEpisode = false;
                await ResolveSelectedSourceAsync(generation);
            }
        }
        catch (OperationCanceledException) { }
        catch (RivuneServerException exception) when (exception.Code == "playback_source_expired" && _state.IsCurrent(generation))
        {
            await RefreshExpiredSourcesAsync();
        }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            SourceBanner.Severity = InfoBarSeverity.Error;
            SourceBanner.Message = FriendlyError(exception);
            SourceBanner.IsOpen = true;
            SourceStatus.Text = "Preparation failed. Choose another source or select this one to retry.";
            var downloading = _offlineDownloadTask is { IsCompleted: false };
            DownloadSourceButton.IsEnabled = downloading;
            DownloadSourceButton.Visibility = downloading ? Visibility.Visible : Visibility.Collapsed;
            ExternalSourceButton.IsEnabled = SupportsExternalPlayer(source);
            ExternalSourceButton.Visibility = SupportsExternalPlayer(source) ? Visibility.Visible : Visibility.Collapsed;
            if (SupportsExternalPlayer(source))
                SourceStatus.Text = "Internal preparation failed. Choose the external player option or select another source.";
        }
        finally
        {
            if (_state.IsCurrent(generation)) SourceProgress.IsActive = false;
        }
    }

    private async Task RefreshExpiredSourcesAsync()
    {
        var request = _sourceRequest;
        if (request is null)
        {
            SetSourceFailure(new InvalidOperationException("Playback sources must be loaded again."));
            return;
        }

        var generation = _state.Transition(AppPhase.Sources);
        _state.SelectedSource = null;
        _state.Preparation = null;
        SourceList.SelectedItem = null;
        SourceList.ItemsSource = null;
        PlaySourceButton.IsEnabled = false;
        ExternalSourceButton.IsEnabled = false;
        ExternalSourceButton.Visibility = Visibility.Collapsed;
        SourceBanner.IsOpen = false;
        SourceProgress.IsActive = true;
        var downloading = _offlineDownloadTask is { IsCompleted: false };
        DownloadSourceButton.IsEnabled = downloading;
        DownloadSourceButton.Visibility = downloading ? Visibility.Visible : Visibility.Collapsed;
        SourceStatus.Text = "Refreshing expired sources…";
        try
        {
            await LoadSourcesAsync(request.MediaType, request.ResourceId, request.TitleId, generation, request.AddonId, request.TracksProgress);
            if (!_state.IsCurrent(generation)) return;
            SourceBanner.Severity = InfoBarSeverity.Informational;
            SourceBanner.Message = "The previous source expired. Fresh sources are ready.";
            SourceBanner.IsOpen = true;
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (_state.IsCurrent(generation)) SetSourceFailure(exception);
        }
        finally
        {
            if (_state.IsCurrent(generation)) SourceProgress.IsActive = false;
        }
    }

    private async void PlaySource_Click(object sender, RoutedEventArgs e)
    {
        var generation = _state.Transition(AppPhase.Sources);
        await ResolveSelectedSourceAsync(generation);
    }
    private async void DownloadSource_Click(object sender, RoutedEventArgs e)
    {
        if (_offlineDownloadTask is { IsCompleted: false })
        {
            _offlineDownloadCancellation?.Cancel();
            DownloadSourceButton.IsEnabled = false;
            DownloadSourceLabel.Text = "Cancelling…";
            return;
        }
        if (_offlineMediaStore is null || _offlineScope is null || _state.SelectedSource is null) return;
        if (!_devicePreferences.DownloadOnMobile && CurrentNetworkClass() == NetworkClass.Mobile)
        {
            SourceBanner.Severity = InfoBarSeverity.Warning;
            SourceBanner.Message = "Offline downloads use Wi-Fi by default. Enable Download on mobile in Video settings to continue.";
            SourceBanner.IsOpen = true;
            return;
        }
        var store = _offlineMediaStore;
        var scope = _offlineScope;
        var selected = _state.SelectedSource;
        var titleId = _progressTitleId;
        var title = _playbackTitle ?? _detailTitleForPlayback();
        var posterUrl = _detailTarget?.PosterUrl;
        var client = _state.Client;
        if (client is null) return;
        _offlineDownloadCancellation = CancellationTokenSource.CreateLinkedTokenSource(_state.Token);
        var cancellation = _offlineDownloadCancellation;
        DownloadSourceButton.IsEnabled = true;
        PlaySourceButton.IsEnabled = false;
        ExternalSourceButton.IsEnabled = false;
        DownloadSourceLabel.Text = "Starting… · Cancel";
        AutomationProperties.SetName(DownloadSourceButton, "Cancel offline download");
        SourceBanner.IsOpen = false;
        var progress = new Progress<long>(bytes =>
        {
            if (!_closed && !cancellation.IsCancellationRequested && StringComparer.Ordinal.Equals(_offlineScope, scope))
                DownloadSourceLabel.Text = UiFormat("Downloading {0} · Cancel", FormatBytes(bytes));
        });
        try
        {
            _offlineDownloadTask = DownloadSelectedSourceAsync(store, scope, selected, titleId, title, posterUrl, client, progress, cancellation.Token);
            await _offlineDownloadTask;
            if (_closed || !StringComparer.Ordinal.Equals(_offlineScope, scope)) return;
            DownloadSourceLabel.Text = "Downloaded";
            SourceBanner.Severity = InfoBarSeverity.Success;
            SourceBanner.Message = "The encrypted download is ready for offline playback.";
            SourceBanner.IsOpen = true;
            LoadOfflineItems();
        }
        catch (OperationCanceledException)
        {
            if (!_closed && StringComparer.Ordinal.Equals(_offlineScope, scope))
            {
                DownloadSourceLabel.Text = "Download";
                SourceBanner.Severity = InfoBarSeverity.Informational;
                SourceBanner.Message = "The offline download was cancelled.";
                SourceBanner.IsOpen = true;
            }
        }
        catch (Exception exception)
        {
            if (_closed) return;
            DownloadSourceLabel.Text = "Download";
            SourceBanner.Severity = InfoBarSeverity.Error;
            SourceBanner.Message = FriendlyError(exception);
            SourceBanner.IsOpen = true;
        }
        finally
        {
            if (ReferenceEquals(_offlineDownloadCancellation, cancellation))
            {
                _offlineDownloadCancellation = null;
                cancellation.Dispose();
            }
            _offlineDownloadTask = null;
            AutomationProperties.SetName(DownloadSourceButton, "Download for offline playback");
            if (!_closed)
            {
                PlaySourceButton.IsEnabled = _state.Preparation is not null;
                ExternalSourceButton.IsEnabled = _state.SelectedSource is { } source && SupportsExternalPlayer(source);
                DownloadSourceButton.IsEnabled = _state.Preparation is not null;
            }
        }
    }

    private static async Task DownloadSelectedSourceAsync(
        OfflineMediaStore store,
        string scope,
        PlaybackSourceOption selected,
        Guid titleId,
        string title,
        string? posterUrl,
        RivuneApiClient client,
        IProgress<long> progress,
        CancellationToken cancellationToken)
    {
        PlaybackSession? session = null;
        try
        {
            await client.PreparePlaybackAsync(selected.SourceRef, cancellationToken: cancellationToken, externalPlayer: true);
            session = await client.ResolvePlaybackAsync(
                selected.SourceRef,
                titleId.ToString("D"),
                cancellationToken: cancellationToken,
                externalPlayer: true);
            var source = session.Sources.FirstOrDefault(value => value.Id == session.SelectedSourceId) ?? session.Sources.FirstOrDefault();
            if (source?.Url is null || source.Protocol.Equals("hls", StringComparison.OrdinalIgnoreCase) || source.Protocol.Equals("dash", StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("This stream cannot be downloaded as one offline file.");
            var uri = client.ResolveResponseResourceUrl(source.Url);
            await store.DownloadAsync(
                scope,
                uri,
                client.IsAllowedResponseResourceUrl,
                titleId,
                title,
                source.Container ?? selected.Container,
                posterUrl,
                progress,
                cancellationToken: cancellationToken);
        }
        finally
        {
            if (session is not null)
            {
                try { await client.StopPlaybackAsync(session.Id, CancellationToken.None); }
                catch { }
            }
        }
    }

    private static string FormatBytes(long bytes) => bytes switch
    {
        >= 1024L * 1024L * 1024L => $"{bytes / (1024d * 1024d * 1024d):0.0} GB",
        >= 1024L * 1024L => $"{bytes / (1024d * 1024d):0.0} MB",
        >= 1024L => $"{bytes / 1024d:0.0} KB",
        _ => $"{bytes} B",
    };

    private Task ResolveSelectedSourceAsync(long generation) => ResolveSelectedSourceAtAsync(generation, startOverrideSeconds: null, throwOnFailure: false);

    private async Task ResolveSelectedSourceAtAsync(long generation, int? startOverrideSeconds, bool throwOnFailure = true)
    {
        if (_state.SelectedSource is null || _state.Preparation is null) return;
        _preferredSourceAddonId = _state.SelectedSource.AddonId;
        var client = _state.Client;
        if (client is null) return;
        PlaySourceButton.IsEnabled = false;
        ExternalSourceButton.IsEnabled = false;
        SourceProgress.IsActive = true;
        SourceStatus.Text = "Resolving secure playback…";
        var playbackFailureRecorded = false;
        void RecordPlaybackFailure()
        {
            if (playbackFailureRecorded) return;
            playbackFailureRecorded = true;
            _diagnostics.Record(DiagnosticEventCode.PlaybackFailed);
        }
        try
        {
            var current = _tracksProgress ? await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token) : null;
            _progressVersion = current?.Version ?? 0;
            var startSeconds = startOverrideSeconds ?? (current is null
                ? (int?)null
                : PlaybackProgressPolicy.StartSeconds(current.PositionSeconds, current.Completed));
            await StartPlaybackFailoverAsync(_state.SelectedSource, _state.Token);
            var session = await client.ResolvePlaybackAsync(
                _state.SelectedSource.SourceRef,
                _progressTitleId.ToString("D"),
                startSeconds: startSeconds,
                cancellationToken: _state.Token);
            if (!_state.IsCurrent(generation))
            {
                await client.StopPlaybackAsync(session.Id, CancellationToken.None);
                return;
            }
            var selected = session.Sources.FirstOrDefault(source =>
                source.Id == session.SelectedSourceId && source.Compatible && source.Url is not null);
            if (selected?.Url is null ||
                !(selected.Protocol.Equals("hls", StringComparison.OrdinalIgnoreCase) || selected.Protocol.Equals("http", StringComparison.OrdinalIgnoreCase)))
            {
                await client.StopPlaybackAsync(session.Id, CancellationToken.None);
                throw new InvalidOperationException("The resolved source is not compatible with guarded native HTTP/HLS playback.");
            }
            _state.PlaybackSession = session;
            lock (_endingSync)
            {
                _endingTask = null;
                _playerReturnTask = null;
                _diagnosticPlaybackActive = false;
            }
            _playbackCompleted = false;
            _preferredAudioTrack = session.SelectedAudioTrack;
            _preferredSubtitleId = session.SelectedSubtitleId;
            await ApplyPlaybackAccessibilityAsync(client, session, selected, _state.Token);
            try
            {
                await ShowPlayerAsync(selected, startSeconds ?? 0);
            }
            catch (OperationCanceledException)
            {
                await EndPlaybackAsync(completed: false, returnToDashboard: false);
                _state.PlaybackSession = null;
                throw;
            }
            catch (Exception exception)
            {
                RecordPlaybackFailure();
                await EndPlaybackAsync(completed: false, returnToDashboard: false);
                _state.PlaybackSession = null;
                if (throwOnFailure) throw;
                _state.Transition(AppPhase.Sources);
                OpenSourcePicker();
                SetSourceFailure(exception);
                return;
            }
        }
        catch (OperationCanceledException) { if (throwOnFailure) throw; }
        catch (RivuneServerException exception) when (exception.Code == "playback_source_expired" && _state.IsCurrent(generation) && !throwOnFailure)
        {
            await RefreshExpiredSourcesAsync();
        }
        catch (Exception exception)
        {
            if (_state.IsCurrent(generation)) RecordPlaybackFailure();
            if (throwOnFailure) throw;
            if (_state.IsCurrent(generation)) SetSourceFailure(exception);
        }
        finally
        {
            if (_state.IsCurrent(generation)) SourceProgress.IsActive = false;
            if (_state.IsCurrent(generation) && _state.SelectedSource is { } source)
                ExternalSourceButton.IsEnabled = SupportsExternalPlayer(source);
        }
    }

    private async Task ShowPlayerAsync(PlaybackSource source, int startSeconds, CancellationToken cancellationToken = default, bool preservePlaybackRate = false)
    {
        cancellationToken.ThrowIfCancellationRequested();
        _playerReturnView = _sourceReturnView ?? DashboardView;
        _activePlaybackSource = source;
        var generation = _state.Transition(AppPhase.Player);
        _playbackMarkers = [];
        _activeMarkerIndex = -1;
        RemoveMarkerSkipButton();
        _coordinationEndedSessionId = null;
        _coordinationStatus = "paused";
        _coordinationPositionMilliseconds = Math.Max(startSeconds, 0) * 1_000L;
        _coordinationDurationMilliseconds = 0;
        _nextEpisodeTarget = null;
        PlayerNextButton.Visibility = Visibility.Collapsed;
        _aspectIndex = _devicePreferences.VideoAspectIndex;
        ApplyPlayerAspect();
        AutomationProperties.SetName(PlayPauseButton, "Play");
        SourceOverlay.Visibility = Visibility.Collapsed;
        if (_sourceReturnView is not null) _sourceReturnView.IsHitTestVisible = true;
        _sourceModalFocus.Close();
        DetailBackButton.Visibility = Visibility.Visible;
        ShowOnly(PlayerView);
        SetPlayerControlsLocked(false);
        if (!preservePlaybackRate) SetPlaybackRate(2, applyToPlayer: false);
        UpdatePlayerPresenterActions();
        RevealPlayerChrome(focusTransport: _tvInputMode);
        PlayerTitle.Text = _playbackTitle ?? _selectedItem?.Title ?? _state.SelectedTitle?.Title ?? "Playback";
        SetPlayerStatus("Preparing playback…", busy: true);
        _timeline.Reset(source.MediaTimeline, startSeconds, source.Media?.DurationSeconds);
        _lastQueuedPosition = startSeconds - 10;
        TimelineSlider.Minimum = _timeline.OffsetSeconds;
        var client = _state.Client ?? throw new NotAuthenticatedException();
        var uri = client.ResolveResponseResourceUrl(source.Url!);
        ReplaceMediaPlayerForSource();
        global::Windows.Web.Http.HttpClient? mediaHttpClient = new(new SameOriginHttpFilter(client.IsAllowedResponseResourceUrl));
        try
        {
            if (source.Protocol.Equals("hls", StringComparison.OrdinalIgnoreCase))
            {
                var creation = await AdaptiveMediaSource.CreateFromUriAsync(uri, mediaHttpClient);
                cancellationToken.ThrowIfCancellationRequested();
                if (!_state.IsCurrent(generation)) throw new OperationCanceledException();
                if (creation.Status != AdaptiveMediaSourceCreationStatus.Success || creation.MediaSource is null)
                    throw new InvalidOperationException($"Windows could not open the HLS source ({creation.Status}).");
                _adaptiveMediaSource = creation.MediaSource;
                _mediaHttpClient = mediaHttpClient;
                mediaHttpClient = null;
                _mediaSource = MediaSource.CreateFromAdaptiveMediaSource(_adaptiveMediaSource);
            }
            else
            {
                mediaHttpClient.Dispose();
                mediaHttpClient = null;
                _directMediaProxy = new LoopbackMediaProxy(uri, client.IsAllowedResponseResourceUrl);
                _mediaSource = MediaSource.CreateFromUri(_directMediaProxy.PlaybackUri);
            }
            cancellationToken.ThrowIfCancellationRequested();
            await AttachSelectedSubtitleAsync(_mediaSource, client, cancellationToken);
            _mediaPlayer.Source = _mediaSource;
            cancellationToken.ThrowIfCancellationRequested();
            _mediaPlayer.PlaybackSession.PlaybackRate = PlaybackRates[_playbackRateIndex];
            if (_timeline.OffsetSeconds == 0 && startSeconds > 0)
                _mediaPlayer.PlaybackSession.Position = TimeSpan.FromSeconds(startSeconds);
            lock (_endingSync)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (_endingTask is not null || _playerReturnTask is not null || _closed)
                    throw new OperationCanceledException(cancellationToken);
                _mediaPlayer.Play();
                _positionTimer.Start();
                _diagnosticPlaybackActive = true;
                _diagnostics.Record(DiagnosticEventCode.PlaybackStarted);
            }
            StartPlayerStartupWatchdog(generation);
            _ = LoadPlayerContextAsync(generation);
        }
        finally
        {
            mediaHttpClient?.Dispose();
        }
    }
    private void SetPlayerStatus(string? message, bool busy = false, AutomationLiveSetting liveSetting = AutomationLiveSetting.Polite)
    {
        PlayerStatus.Text = message ?? string.Empty;
        PlayerProgress.IsActive = busy;
        PlayerProgress.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
        PlayerStatusBanner.Visibility = string.IsNullOrWhiteSpace(message) && !busy
            ? Visibility.Collapsed
            : Visibility.Visible;
        PlayerStatus.SetValue(AutomationProperties.LiveSettingProperty, liveSetting);
    }



    private void StartPlayerStartupWatchdog(long generation)
    {
        _playerStartupCancellation?.Cancel();
        _playerStartupCancellation?.Dispose();
        _playerStartupCancellation = CancellationTokenSource.CreateLinkedTokenSource(_state.Token);
        var token = _playerStartupCancellation.Token;
        _ = WatchPlayerStartupAsync(generation, token);
    }

    private async Task WatchPlayerStartupAsync(long generation, CancellationToken cancellationToken)
    {
        try
        {
            await Task.Delay(TimeSpan.FromSeconds(45), cancellationToken);
            if (!_state.IsCurrent(generation) || PlayerView.Visibility != Visibility.Visible ||
                _state.PlaybackSession is null && _activeOfflineItem is null) return;
            var failedPosition = (int)Math.Max(0, AbsolutePlaybackPosition(_mediaPlayer.PlaybackSession.Position));
            await DispatcherQueue.EnqueueAsync(async () =>
            {
                if (!_state.IsCurrent(generation) || _state.PlaybackSession is null && _activeOfflineItem is null) return;
                _diagnostics.Record(DiagnosticEventCode.PlaybackFailed);
                SetPlayerStatus("Playback did not start within 45 seconds. Trying another source…", busy: true, liveSetting: AutomationLiveSetting.Assertive);
                if (_activeOfflineItem is null && await TryAutomaticPlaybackFailoverAsync(PlaybackFailoverError.SourceTimeout, failedPosition)) return;
                await EndPlaybackAsync(completed: false, returnToDashboard: false);
                await ShowPlaybackRecoveryAsync("Playback did not start within 45 seconds.", failedPosition);
            });
        }
        catch (OperationCanceledException) { }
    }
    private void ClearMediaSource()
    {
        _playerStartupCancellation?.Cancel();
        _directMediaProxy?.Dispose();
        _directMediaProxy = null;
        _mediaPlayer.Source = null;
        _mediaSource?.Dispose();
        _mediaSource = null;
        _subtitleStream?.Dispose();
        _subtitleStream = null;
        _adaptiveMediaSource = null;
        _mediaHttpClient?.Dispose();
        _mediaHttpClient = null;
    }

    private void ReplaceMediaPlayerForSource()
    {
        ClearMediaSource();
        var previous = _mediaPlayer;
        _mediaPlayer = CreateMediaPlayer();
        PlayerElement.SetMediaPlayer(_mediaPlayer);
        ReleaseMediaPlayer(previous);
    }

    private async Task AttachSelectedSubtitleAsync(
        MediaSource mediaSource,
        RivuneApiClient client,
        CancellationToken cancellationToken)
    {
        var session = _state.PlaybackSession;
        if (session?.SelectedSubtitleId is not { Length: > 0 } selectedId) return;
        var subtitle = session.Subtitles.FirstOrDefault(value => value.Id == selectedId);
        if (subtitle is not { Delivery: PlaybackSubtitleDelivery.External, Url: { Length: > 0 } url }) return;

        var contents = await client.DownloadSameOriginSubtitleAsync(url, cancellationToken);
        InMemoryRandomAccessStream? stream = new();
        try
        {
            await stream.WriteAsync(contents.AsBuffer()).AsTask(cancellationToken);
            stream.Seek(0);
            mediaSource.ExternalTimedTextSources.Add(TimedTextSource.CreateFromStream(stream));
            _subtitleStream = stream;
            stream = null;
        }
        finally
        {
            CryptographicOperations.ZeroMemory(contents);
            stream?.Dispose();
        }
    }

    private async void CloseSources_Click(object sender, RoutedEventArgs e) => await CloseSourcesAsync();
    private Task CloseSourcesAsync()
    {
        if (!_sourceModalFocus.IsOpen) return Task.CompletedTask;
        _autoStartNextEpisode = false;
        _state.Transition(_sourceReturnView == DetailView ? AppPhase.Detail : AppPhase.Catalogue);
        CloseSourcePicker();
        return Task.CompletedTask;
    }
    private void OpenSourcePicker()
    {
        ExitPlayerPresenterMode();
        var invoker = _sourceInvoker ?? FocusManager.GetFocusedElement(XamlRoot) as Control;
        _sourceModalFocus.Open(invoker);
        if (DetailView.Visibility == Visibility.Visible) _sourceReturnView = DetailView;
        else if (DashboardView.Visibility == Visibility.Visible) _sourceReturnView = DashboardView;
        else _sourceReturnView ??= DashboardView;
        _sourceReturnView.Visibility = Visibility.Visible;
        _sourceReturnView.IsHitTestVisible = false;
        SourceOverlay.Visibility = Visibility.Visible;
        DetailBackButton.Visibility = Visibility.Collapsed;
        PlayerView.Visibility = Visibility.Collapsed;
        if (_sourceInvoker is not null) _sourceInvoker.IsEnabled = true;
        CloseSourcesButton.Focus(FocusState.Programmatic);
    }

    private void CloseSourcePicker()
    {
        if (_offlineDownloadTask is { IsCompleted: false }) _offlineDownloadCancellation?.Cancel();
        var returnView = _sourceReturnView ?? DashboardView;
        var focusTarget = _sourceModalFocus.Close();
        SourceOverlay.Visibility = Visibility.Collapsed;
        returnView.IsHitTestVisible = true;
        DetailBackButton.Visibility = Visibility.Visible;
        ShowOnly(returnView);
        _sourceInvoker = null;
        DispatcherQueue.TryEnqueue(() => (focusTarget ?? (returnView == DetailView ? DetailBackButton : null))?.Focus(FocusState.Programmatic));
    }

    private async void ClosePlayer_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked)
        {
            SetPlayerControlsLocked(false);
            return;
        }
        await CancelPlaybackFailoverAsync();
        await EndPlaybackAsync(completed: false, returnToDashboard: true);
    }
    private async Task EndPlaybackAsync(bool completed, bool returnToDashboard)
    {
        _coordinationStatus = "ended";
        if (completed) _playbackCompleted = true;
        var completionDrain = completed ? QueueProgress(true) : Task.CompletedTask;
        Task ending;
        Task? returning = null;
        CancellationTokenSource? trackRestartCancellation = null;
        lock (_endingSync)
        {
            if (_endingTask is null)
            {
                if (_diagnosticPlaybackActive)
                {
                    _diagnosticPlaybackActive = false;
                    _diagnostics.Record(DiagnosticEventCode.PlaybackStopped);
                }
                _endingTask = EndPlaybackCoreAsync(_state.PlaybackSession, _state.Client, _mediaPlayer);
            }
            ending = _endingTask;
            if (returnToDashboard)
            {
                returning = _playerReturnTask ??= ReturnFromPlayerAsync(ending);
                trackRestartCancellation = _trackRestartCancellation;
            }
        }
        try { trackRestartCancellation?.Cancel(); }
        catch (ObjectDisposedException) { }
        await (returning ?? ending);
        await completionDrain;
    }

    private async Task EndPlaybackCoreAsync(PlaybackSession? session, RivuneApiClient? client, MediaPlayer player)
    {
        var roomEndFailure = await EndActivePlaybackRoomAsync(session, client);
        if (roomEndFailure is not null && ReferenceEquals(player, _mediaPlayer))
            SetPlayerStatus($"Playback stopped, but the watch room could not be ended: {FriendlyError(roomEndFailure)}", liveSetting: AutomationLiveSetting.Assertive);
        _chromeTimer.Stop();
        _playerStartupCancellation?.Cancel();
        _positionTimer.Stop();
        await FlushProgressAsync(_playbackCompleted, CancellationToken.None);
        await StopSessionOnceAsync(session, client);
        if (!ReferenceEquals(player, _mediaPlayer)) return;
        player.Pause();
        ClearMediaSource();
        StopOfflinePlayback(clearItem: false);
        SetPlayerControlsLocked(false);
    }


    private async Task ReturnFromPlayerAsync(Task ending)
    {
        await ending;
        StopOfflinePlayback();
        if (_closed) return;
        ExitPlayerPresenterMode();
        var returnView = _playerReturnView ?? DashboardView;
        var generation = _state.Transition(returnView == DetailView ? AppPhase.Detail : AppPhase.Catalogue);
        _state.ClearPlayback();
        if (returnView == DetailView)
        {
            _state.SelectedTitle = _detailReference;
            await RenderDetailAsync(generation);
        }
        ShowOnly(returnView);
        DispatcherQueue.TryEnqueue(() =>
        {
            var target = returnView == DetailView
                ? DetailActions.Items.OfType<Control>().FirstOrDefault()
                : _sourceInvoker;
            target?.Focus(FocusState.Programmatic);
            _sourceInvoker = null;
        });
    }
    private Task StopSessionOnceAsync() => StopSessionOnceAsync(_state.PlaybackSession, _state.Client);

    private Task StopSessionOnceAsync(PlaybackSession? session, RivuneApiClient? client)
    {
        if (session is null || client is null) return Task.CompletedTask;
        lock (_stopSync)
        {
            if (_sessionStopTasks.TryGetValue(session.Id, out var stopping)) return stopping;
            stopping = StopSessionCoreAsync(client, session.Id);
            _sessionStopTasks.Add(session.Id, stopping);
            return stopping;
        }
    }

    private static async Task StopSessionCoreAsync(RivuneApiClient client, Guid sessionId)
    {
        try { await client.StopPlaybackAsync(sessionId, CancellationToken.None); }
        catch { }
    }

    private void MediaPlayer_MediaOpened(MediaPlayer sender, object args)
    {
        if (!ReferenceEquals(sender, _mediaPlayer)) return;
        DispatcherQueue.TryEnqueue(() =>
        {
            if (!ReferenceEquals(sender, _mediaPlayer)) return;
            _playerStartupCancellation?.Cancel();
            SetPlayerStatus(null);
            var durationSeconds = LogicalDurationSeconds(sender.PlaybackSession.NaturalDuration);
            TimelineSlider.Minimum = _timeline.OffsetSeconds;
            TimelineSlider.Maximum = durationSeconds;
            DurationText.Text = FormatTime(TimeSpan.FromSeconds(durationSeconds));
        });
    }

    private async void MediaPlayer_MediaEnded(MediaPlayer sender, object args)
    {
        if (!ReferenceEquals(sender, _mediaPlayer)) return;
        await DispatcherQueue.EnqueueAsync(async () =>
        {
            if (!ReferenceEquals(sender, _mediaPlayer)) return;
            var position = Math.Max(0, AbsolutePlaybackPosition(sender.PlaybackSession.Position));
            var duration = LogicalDurationSeconds(sender.PlaybackSession.NaturalDuration);
            if (_activeOfflineItem is null && _state.PlaybackSession is not null && _nextEpisodeTarget is null && duration - position > 30)
            {
                SetPlayerStatus("The source ended early. Trying another source…", busy: true, liveSetting: AutomationLiveSetting.Assertive);
                if (await TryAutomaticPlaybackFailoverAsync(PlaybackFailoverError.EndedEarly, position)) return;
                await EndPlaybackAsync(completed: false, returnToDashboard: false);
                await ShowPlaybackRecoveryAsync("The source ended before the title finished.", (int)position);
                return;
            }
            SetPlayerStatus("Playback finished.");
            if (_nextEpisodeTarget is not null && _effectiveSettings?.Settings.AutoplayNextEpisode != false)
            {
                await AdvanceToNextEpisodeAsync();
                return;
            }
            await CancelPlaybackFailoverAsync();
            await EndPlaybackAsync(completed: true, returnToDashboard: false);
            if (!ReferenceEquals(sender, _mediaPlayer)) return;
            SetPlayerStatus(_nextEpisodeTarget is null
                ? "Playback finished. Choose replay or close the player."
                : "Playback finished. Choose replay, next episode, or close the player.");
            PlayPauseIcon.Glyph = "\uE72C";
            AutomationProperties.SetName(PlayPauseButton, "Replay");
            RevealPlayerChrome(focusTransport: _tvInputMode);
        });
    }

    private void MediaPlayer_MediaFailed(MediaPlayer sender, MediaPlayerFailedEventArgs args)
    {
        if (!ReferenceEquals(sender, _mediaPlayer)) return;
        _playerStartupCancellation?.Cancel();
        var failedPosition = (int)Math.Max(0, AbsolutePlaybackPosition(sender.PlaybackSession.Position));
        _ = DispatcherQueue.EnqueueAsync(async () =>
        {
            if (!ReferenceEquals(sender, _mediaPlayer) || PlayerView.Visibility != Visibility.Visible ||
                _state.PlaybackSession is null && _activeOfflineItem is null) return;
            _diagnostics.Record(DiagnosticEventCode.PlaybackFailed);
            SetPlayerStatus("Playback failed. Trying another source…", busy: true, liveSetting: AutomationLiveSetting.Assertive);
            if (_activeOfflineItem is null && await TryAutomaticPlaybackFailoverAsync(PlaybackFailoverError.SourceFailed, failedPosition)) return;
            await EndPlaybackAsync(completed: false, returnToDashboard: false);
            if (!ReferenceEquals(sender, _mediaPlayer)) return;
            await ShowPlaybackRecoveryAsync("Windows could not continue this source.", failedPosition);
        });
    }

    private async Task ShowPlaybackRecoveryAsync(string errorMessage, int failedPosition)
    {
        var offline = _activeOfflineItem is not null;
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = "Playback stopped",
            Content = string.IsNullOrWhiteSpace(errorMessage) ? "Rivune couldn’t continue this source." : errorMessage,
            PrimaryButtonText = "Retry",
            SecondaryButtonText = "Start over",
            CloseButtonText = offline ? "Close player" : "Choose another source",
            DefaultButton = ContentDialogButton.Primary,
        };
        var result = await ShowDialogAsync(dialog);
        if (offline)
        {
            if (result is ContentDialogResult.Primary or ContentDialogResult.Secondary)
                await RestartOfflinePlaybackAsync(result == ContentDialogResult.Secondary ? 0 : failedPosition);
            else await EndPlaybackAsync(completed: false, returnToDashboard: true);
            return;
        }
        if (result is ContentDialogResult.Primary or ContentDialogResult.Secondary)
        {
            await RestartPlaybackWithTracksAsync(result == ContentDialogResult.Secondary ? 0 : failedPosition);
            if (_state.PlaybackSession is not null && _mediaPlayer.Source is not null) return;
        }
        _state.PlaybackSession = null;
        _state.Transition(AppPhase.Sources);
        OpenSourcePicker();
        SourceBanner.Severity = InfoBarSeverity.Error;
        SourceBanner.Message = "Playback stopped. Choose another source or refresh the list.";
        SourceBanner.IsOpen = true;
        SourceStatus.Text = "Choose another source to continue.";
    }

    private void PlaybackSession_PlaybackStateChanged(MediaPlaybackSession sender, object args)
    {
        if (!ReferenceEquals(sender, _mediaPlayer.PlaybackSession)) return;
        DispatcherQueue.TryEnqueue(() =>
        {
            if (!ReferenceEquals(sender, _mediaPlayer.PlaybackSession)) return;
            var playing = sender.PlaybackState == MediaPlaybackState.Playing;
            var replay = !playing && _playbackCompleted;
            PlayPauseIcon.Glyph = playing ? "\uE769" : replay ? "\uE72C" : "\uE768";
            AutomationProperties.SetName(PlayPauseButton, playing ? "Pause" : replay ? "Replay" : "Play");
            if (!_playbackCompleted && _coordinationStatus != "ended") _coordinationStatus = playing ? "playing" : "paused";
            if (playing)
            {
                SetPlayerStatus(null);
                NotePlayerInteraction();
            }
            else
            {
                _chromeTimer.Stop();
                if (!_playerControlsLocked) RevealPlayerChrome(focusTransport: false);
                if (sender.PlaybackState == MediaPlaybackState.Buffering) SetPlayerStatus("Buffering…", busy: true);
            }
        });
    }

    private void PositionTimer_Tick(MicrosoftDispatcherQueueTimer sender, object args)
    {
        var session = _mediaPlayer.PlaybackSession;
        var absolutePosition = AbsolutePlaybackPosition(session.Position);
        var durationSeconds = LogicalDurationSeconds(session.NaturalDuration);
        _timelineFromPlayer = true;
        TimelineSlider.Minimum = _timeline.OffsetSeconds;
        TimelineSlider.Maximum = durationSeconds;
        TimelineSlider.Value = Math.Clamp(absolutePosition, TimelineSlider.Minimum, TimelineSlider.Maximum);
        _timelineFromPlayer = false;
        ElapsedText.Text = FormatTime(TimeSpan.FromSeconds(absolutePosition));
        DurationText.Text = FormatTime(TimeSpan.FromSeconds(durationSeconds));
        _coordinationPositionMilliseconds = (long)Math.Max(absolutePosition, 0) * 1_000L;
        _coordinationDurationMilliseconds = (long)Math.Max(durationSeconds, 0) * 1_000L;
        AutomationProperties.SetHelpText(TimelineSlider, $"{ElapsedText.Text} of {DurationText.Text}");
        UpdateMarkerSkipAction(absolutePosition);
        var wholeSeconds = (int)absolutePosition;
        if (Math.Abs(wholeSeconds - _lastQueuedPosition) >= 10)
        {
            _lastQueuedPosition = wholeSeconds;
            _ = QueueProgress(completed: false);
        }
    }

    private void TimelineSlider_ValueChanged(object sender, RangeBaseValueChangedEventArgs e)
    {
        if (_timelineFromPlayer || PlayerView.Visibility != Visibility.Visible || _playerControlsLocked) return;
        _mediaPlayer.PlaybackSession.Position = MediaPlaybackPosition(e.NewValue);
        ResetAutoSkippedMarkersAfterSeek(e.NewValue);
        _lastQueuedPosition = (int)e.NewValue;
        _ = QueueProgress(false);
        NotePlayerInteraction();
    }

    private async void PlayPause_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        if (_mediaPlayer.Source is null && _activeOfflineItem is not null)
        {
            await RestartOfflinePlaybackAsync(0);
            return;
        }
        if (_mediaPlayer.Source is null && _state.PlaybackSession is not null)
        {
            await RestartPlaybackWithTracksAsync(0);
            return;
        }
        if (_mediaPlayer.PlaybackSession.PlaybackState == MediaPlaybackState.Playing)
        {
            _mediaPlayer.Pause();
            _ = QueueProgress(false);
        }
        else _mediaPlayer.Play();
        NotePlayerInteraction();
    }

    private void SeekBack_Click(object sender, RoutedEventArgs e) => Seek(-10);
    private void SeekForward_Click(object sender, RoutedEventArgs e) => Seek(10);
    private void PlayerFullscreen_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        var entering = App.MainWindow.PlayerPresenterKind != Microsoft.UI.Windowing.AppWindowPresenterKind.FullScreen;
        App.MainWindow.SetPlayerPresenter(entering
            ? Microsoft.UI.Windowing.AppWindowPresenterKind.FullScreen
            : Microsoft.UI.Windowing.AppWindowPresenterKind.Default);
        UpdatePlayerPresenterActions();
        NotePlayerInteraction();
    }

    private void PlayerMiniPlayer_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked) return;
        var entering = App.MainWindow.PlayerPresenterKind != Microsoft.UI.Windowing.AppWindowPresenterKind.CompactOverlay;
        App.MainWindow.SetPlayerPresenter(entering
            ? Microsoft.UI.Windowing.AppWindowPresenterKind.CompactOverlay
            : Microsoft.UI.Windowing.AppWindowPresenterKind.Default);
        UpdatePlayerPresenterActions();
        NotePlayerInteraction();
    }

    private void UpdatePlayerPresenterActions()
    {
        var kind = App.MainWindow.PlayerPresenterKind;
        var fullScreen = kind == Microsoft.UI.Windowing.AppWindowPresenterKind.FullScreen;
        var miniPlayer = kind == Microsoft.UI.Windowing.AppWindowPresenterKind.CompactOverlay;
        PlayerFullscreenIcon.Glyph = fullScreen ? "\uE73F" : "\uE740";
        AutomationProperties.SetName(PlayerFullscreenButton, fullScreen ? "Exit full screen" : "Enter full screen");
        ToolTipService.SetToolTip(PlayerFullscreenButton, fullScreen ? "Exit full screen" : "Full screen");
        PlayerMiniEnterIcon.Visibility = miniPlayer ? Visibility.Collapsed : Visibility.Visible;
        PlayerMiniExitIcon.Visibility = miniPlayer ? Visibility.Visible : Visibility.Collapsed;
        AutomationProperties.SetName(PlayerMiniPlayerButton, miniPlayer ? "Exit mini-player" : "Enter mini-player");
        ToolTipService.SetToolTip(PlayerMiniPlayerButton, miniPlayer ? "Exit mini-player" : "Mini-player");
    }

    private void ExitPlayerPresenterMode()
    {
        if (App.MainWindow.PlayerPresenterKind is Microsoft.UI.Windowing.AppWindowPresenterKind.FullScreen or Microsoft.UI.Windowing.AppWindowPresenterKind.CompactOverlay)
            App.MainWindow.SetPlayerPresenter(Microsoft.UI.Windowing.AppWindowPresenterKind.Default);
        UpdatePlayerPresenterActions();
    }

    private void Seek(double seconds)
    {
        if (_playerControlsLocked) return;
        var duration = LogicalDurationSeconds(_mediaPlayer.PlaybackSession.NaturalDuration);
        var target = Math.Clamp(AbsolutePlaybackPosition(_mediaPlayer.PlaybackSession.Position) + seconds, _timeline.OffsetSeconds, duration);
        _mediaPlayer.PlaybackSession.Position = MediaPlaybackPosition(target);
        ResetAutoSkippedMarkersAfterSeek(target);
        _lastQueuedPosition = (int)target;
        _ = QueueProgress(false);
        NotePlayerInteraction();
    }

    private void PlayerChrome_PointerMoved(object sender, PointerRoutedEventArgs e) => NotePlayerInteraction();

    private void PlayerChrome_GotFocus(object sender, RoutedEventArgs e) => RevealPlayerChrome(focusTransport: false);

    private void NotePlayerInteraction() => RevealPlayerChrome(focusTransport: false);

    private bool TryHidePlayerChrome(bool requirePlaying = true)
    {
        _chromeTimer.Stop();
        var focused = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject;
        var focusInsideChrome = focused is not null &&
            (ReferenceEquals(focused, PlayerChrome) || Descendants(PlayerChrome).Contains(focused));
        if (_playerControlsLocked || focusInsideChrome ||
            requirePlaying && _mediaPlayer.PlaybackSession.PlaybackState != MediaPlaybackState.Playing) return false;
        PlayerChrome.Visibility = Visibility.Collapsed;
        return true;
    }

    private void RevealPlayerChrome(bool focusTransport)
    {
        if (_playerControlsLocked) return;
        PlayerChrome.Visibility = Visibility.Visible;
        PlayerChrome.Opacity = 1;
        _chromeTimer.Stop();
        _chromeTimer.Interval = TimeSpan.FromSeconds(_tvInputMode ? 7 : 5);
        if (_mediaPlayer.PlaybackSession.PlaybackState == MediaPlaybackState.Playing) _chromeTimer.Start();
        if (focusTransport) PlayPauseButton.Focus(FocusState.Programmatic);
    }


    private Task QueueProgress(bool completed)
    {
        if (!_tracksProgress) return Task.CompletedTask;
        var session = _mediaPlayer.PlaybackSession;
        var position = (int)AbsolutePlaybackPosition(session.Position);
        var duration = (int)LogicalDurationSeconds(session.NaturalDuration);
        var snapshot = new ProgressSnapshot(
            position,
            duration,
            PlaybackProgressPolicy.IsCompleted(position, duration, completed || _playbackCompleted));
        TaskCompletionSource? starter = null;
        Task drain;
        lock (_progressSync)
        {
            _pendingProgress = snapshot;
            if (_progressDrainTask.IsCompleted)
            {
                starter = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                _progressDrainTask = starter.Task;
            }
            drain = _progressDrainTask;
        }
        if (starter is not null) _ = DrainProgressAsync(starter, CancellationToken.None);
        return drain;
    }

    private async Task DrainProgressAsync(TaskCompletionSource completion, CancellationToken cancellationToken)
    {
        while (true)
        {
            ProgressSnapshot snapshot;
            lock (_progressSync)
            {
                if (_pendingProgress is null)
                {
                    if (ReferenceEquals(_progressDrainTask, completion.Task))
                        _progressDrainTask = Task.CompletedTask;
                    completion.TrySetResult();
                    return;
                }
                snapshot = _pendingProgress;
                _pendingProgress = null;
            }
            try { await WriteProgressSnapshotAsync(snapshot, cancellationToken); }
            catch { }
        }
    }

    private async Task WriteProgressSnapshotAsync(ProgressSnapshot snapshot, CancellationToken cancellationToken)
    {
        if (_activeOfflineItem is not null)
        {
            await WriteOfflineProgressSnapshotAsync(snapshot);
            return;
        }
        var client = _state.Client ?? throw new NotAuthenticatedException();
        try
        {
            var updated = await client.UpdatePlaybackProgressAsync(_progressTitleId, ProgressRequest(snapshot, _progressVersion), cancellationToken);
            _progressVersion = Math.Max(_progressVersion, updated.Version);
            ApplyPersistedProgress(_progressTitleId, updated);
        }
        catch (RivuneServerException exception) when (exception.StatusCode == 409)
        {
            var current = await client.GetPlaybackProgressAsync(_progressTitleId, cancellationToken);
            if (current is null)
            {
                _progressVersion = 0;
                var inserted = await client.UpdatePlaybackProgressAsync(_progressTitleId, ProgressRequest(snapshot, 0), cancellationToken);
                _progressVersion = Math.Max(_progressVersion, inserted.Version);
                ApplyPersistedProgress(_progressTitleId, inserted);
                return;
            }
            _progressVersion = Math.Max(_progressVersion, current.Version);
            var mergedPolicy = PlaybackProgressPolicy.MergeConflict(
                new PlaybackProgressPolicy.Snapshot(snapshot.Position, snapshot.Duration, snapshot.Completed),
                new PlaybackProgressPolicy.Snapshot(current.PositionSeconds, current.DurationSeconds, current.Completed));
            var merged = new ProgressSnapshot(mergedPolicy.PositionSeconds, mergedPolicy.DurationSeconds, mergedPolicy.Completed);
            var updated = await client.UpdatePlaybackProgressAsync(_progressTitleId, ProgressRequest(merged, _progressVersion), cancellationToken);
            _progressVersion = Math.Max(_progressVersion, updated.Version);
            ApplyPersistedProgress(_progressTitleId, updated);
        }
    }

    private static UpdatePlaybackProgressRequest ProgressRequest(ProgressSnapshot snapshot, long expectedVersion) => new()
    {
        PositionSeconds = snapshot.Position,
        DurationSeconds = snapshot.Duration,
        Completed = snapshot.Completed,
        ExpectedVersion = expectedVersion,
    };

    private async Task FlushProgressAsync(bool completed, CancellationToken cancellationToken)
    {
        _ = QueueProgress(completed);
        while (true)
        {
            Task drain;
            lock (_progressSync) drain = _progressDrainTask;
            await drain.WaitAsync(cancellationToken);
            lock (_progressSync)
            {
                if (_pendingProgress is null && _progressDrainTask.IsCompleted) return;
            }
        }
    }

    private async void ProfileLogout_Click(object sender, RoutedEventArgs e) => await DisconnectCoreAsync(clearAddress: false);

    private async void Disconnect_Click(object sender, RoutedEventArgs e) => await DisconnectCoreAsync(clearAddress: false);
    private async void RefreshDashboard_Click(object sender, RoutedEventArgs e) => await ShowDashboardAsync();
    private async Task DisconnectCoreAsync(bool clearAddress)
    {
        StopPlaybackCoordinationPolling();
        if (_state.PlaybackSession is not null) await EndPlaybackAsync(false, false);
        else await EndActivePlaybackRoomAsync(null, _state.Client);
        LockOfflineAccess();
        AbandonPlaybackCoordination();
        var client = _state.Client;
        var discovery = _state.Discovery;
        _state.Transition(clearAddress ? AppPhase.Server : AppPhase.Pairing);
        CredentialStoreException? credentialFailure = null;
        Exception? remoteFailure = null;
        try
        {
            if (client is not null) await client.LogoutAsync(CancellationToken.None);
        }
        catch (CredentialStoreException exception)
        {
            credentialFailure = exception;
        }
        catch (Exception exception)
        {
            remoteFailure = exception;
        }

        _deviceAuthorization = null;
        _selectedItem = null;
        _playbackTitle = null;
        _sourceRequest = null;
        _state.Account = null;
        _state.Profile = null;
        ResetViewerProfileState();
        ProfileGrid.ItemsSource = null;
        ProfileGrid.Items.Clear();
        DashboardSections.Children.Clear();
        SourceList.ItemsSource = null;

        var failure = credentialFailure is not null
            ? "Encrypted local credentials could not be removed. Fix local file access before pairing this device again."
            : remoteFailure is not null
                ? "Local credentials were removed, but the server session could not be closed. It will expire automatically."
                : null;
        if (clearAddress)
        {
            var addressCleared = await ClearSavedServerAsync();
            _state.ResetServer();
            if (addressCleared) ServerAddressBox.Text = string.Empty;
            if (!addressCleared)
                failure = $"{failure}{(failure is null ? string.Empty : " ")}The saved server address could not be removed. Fix local file access, then try again.";
            ShowServer(failure);
            return;
        }

        if (client is null || discovery is null)
        {
            _state.ResetServer();
            ShowServer(failure);
            return;
        }

        _state.Client = client;
        _state.Discovery = discovery;
        await StartPairingAsync(failure);
    }

    private void SetSourceFailure(Exception exception)
    {
        SourceProgress.IsActive = false;
        PlaySourceButton.IsEnabled = false;
        var externalAvailable = _state.SelectedSource is { } source && SupportsExternalPlayer(source);
        ExternalSourceButton.IsEnabled = externalAvailable;
        ExternalSourceButton.Visibility = externalAvailable ? Visibility.Visible : Visibility.Collapsed;
        SourceBanner.Severity = InfoBarSeverity.Error;
        SourceBanner.Message = FriendlyError(exception);
        SourceBanner.IsOpen = true;
        SourceStatus.Text = "Choose another source or close this panel and try again.";
    }

    private async void Page_KeyDown(object sender, KeyRoutedEventArgs e)
    {
        var gamepadNavigation = e.Key is VirtualKey.GamepadA or VirtualKey.GamepadB or VirtualKey.GamepadDPadUp or VirtualKey.GamepadDPadDown or
            VirtualKey.GamepadDPadLeft or VirtualKey.GamepadDPadRight or VirtualKey.GamepadLeftThumbstickUp or
            VirtualKey.GamepadLeftThumbstickDown or VirtualKey.GamepadLeftThumbstickLeft or VirtualKey.GamepadLeftThumbstickRight;
        var playerPlaybackKey = e.Key is VirtualKey.Space or MediaPlayPauseKey or VirtualKey.Left or VirtualKey.GamepadLeftShoulder or
            MediaPreviousTrackKey or VirtualKey.Right or VirtualKey.GamepadRightShoulder or MediaNextTrackKey or MediaStopKey or VirtualKey.M;
        var playerNavigationKey = gamepadNavigation || e.Key is VirtualKey.Up or VirtualKey.Down or VirtualKey.Tab or VirtualKey.Enter;
        if (gamepadNavigation)
        {
            if (!_tvInputMode)
            {
                _tvInputMode = true;
                VisualStateManager.GoToState(this, "TvMode", useTransitions: true);
            }
            if (PlayerView.Visibility == Visibility.Visible && _playerControlsLocked && e.Key != VirtualKey.GamepadB)
            {
                PlayerUnlockButton.Focus(FocusState.Programmatic);
                e.Handled = true;
                return;
            }
        }
        if (PlayerView.Visibility == Visibility.Visible && e.Key is not (VirtualKey.GamepadB or VirtualKey.Escape))
        {
            if (_playerControlsLocked)
            {
                PlayerUnlockButton.Focus(FocusState.Programmatic);
                e.Handled = true;
                return;
            }
            if (PlayerChrome.Visibility != Visibility.Visible)
            {
                RevealPlayerChrome(focusTransport: !playerPlaybackKey);
                if (playerNavigationKey && !playerPlaybackKey)
                {
                    e.Handled = true;
                    return;
                }
            }
        }
        if ((e.Key is VirtualKey.GamepadB or VirtualKey.Escape) && SourceOverlay.Visibility == Visibility.Visible)
        {
            e.Handled = true;
            await CloseSourcesAsync();
        }
        else if ((e.Key is VirtualKey.GamepadB or VirtualKey.Escape) && PlayerView.Visibility == Visibility.Visible)
        {
            e.Handled = true;
            if (_playerControlsLocked) SetPlayerControlsLocked(false, focusTransport: true);
            else if (App.MainWindow.PlayerPresenterKind is Microsoft.UI.Windowing.AppWindowPresenterKind.FullScreen or Microsoft.UI.Windowing.AppWindowPresenterKind.CompactOverlay)
                ExitPlayerPresenterMode();
            else if (PlayerChrome.Visibility != Visibility.Visible || !TryHidePlayerChrome(requirePlaying: false))
                await EndPlaybackAsync(false, true);
        }
        else if ((e.Key is VirtualKey.GamepadB or VirtualKey.Escape) && DetailView.Visibility == Visibility.Visible)
        {
            e.Handled = true;
            if (_detailBackAction is not null) await _detailBackAction();
            else await ReturnToViewerAsync();
        }
        else if ((e.Key is VirtualKey.GamepadB or VirtualKey.Escape) && SettingsView.Visibility == Visibility.Visible)
        {
            e.Handled = true;
            if (_activeSettingsCategory is not null) ShowSettingsCategories();
            else await ReturnFromSettingsAsync();
        }
        else if (PlayerView.Visibility == Visibility.Visible)
        {
            if (_playerControlsLocked)
            {
                e.Handled = true;
                return;
            }
            if (e.Key is VirtualKey.Space or MediaPlayPauseKey) { PlayPause_Click(this, new RoutedEventArgs()); e.Handled = true; }
            else if (e.Key is VirtualKey.Left or VirtualKey.GamepadLeftShoulder or MediaPreviousTrackKey) { Seek(-10); e.Handled = true; }
            else if (e.Key is VirtualKey.Right or VirtualKey.GamepadRightShoulder or MediaNextTrackKey) { Seek(10); e.Handled = true; }
            else if (e.Key == MediaStopKey) { e.Handled = true; await EndPlaybackAsync(false, true); }
            else if (e.Key == VirtualKey.M && !_playerControlsLocked) { _mediaPlayer.IsMuted = !_mediaPlayer.IsMuted; e.Handled = true; }
        }
    }

    public async Task HandleWindowActivationAsync(bool active)
    {
        _windowActive = active;
        if (!active) StopPlaybackCoordinationPolling();
        else if (!_closed && PlaybackCoordinationAvailable && _state.Client is not null) StartPlaybackCoordination();
        if (!active && _offlineScope is not null && _offlineMediaStore?.Profile(_offlineScope)?.RequiresPin == true)
        {
            var offlineOnly = _offlineOnlySession;
            if (_activeOfflineItem is not null && PlayerView.Visibility == Visibility.Visible)
                await EndPlaybackAsync(completed: false, returnToDashboard: true);
            LockOfflineAccess();
            if (_closed) return;
            if (offlineOnly) ShowServer("Downloaded media was locked when Rivune moved to the background.");
            else RebuildHomeSections(_viewerCollections, _continueWatchingTargets, _recommendationTargets);
            return;
        }
        if (!active && PlayerView.Visibility == Visibility.Visible && _mediaPlayer.Source is not null)
            await QueueProgress(false);
    }

    private void StartPlaybackCoordination()
    {
        StopPlaybackCoordinationPolling();
        if (!CoordinationPollingPolicy.ShouldRun(_windowActive, _closed, PlaybackCoordinationAvailable, _state.Profile is not null) || _state.Client is null) return;
        _coordinationCancellation = new CancellationTokenSource();
        _coordinationTask = RunPlaybackCoordinationAsync(_state.Client, _coordinationCancellation.Token);
    }

    private void StopPlaybackCoordinationPolling()
    {
        _coordinationCancellation?.Cancel();
        _coordinationCancellation?.Dispose();
        _coordinationCancellation = null;
        _coordinationTask = null;
    }

    private void AbandonPlaybackCoordination(bool preserveActiveRoom = false)
    {
        var room = preserveActiveRoom ? _state.ActivePlaybackRoom : null;
        StopPlaybackCoordinationPolling();
        _lastPlaybackOperationId = null;
        _coordinationStatus = "idle";
        _coordinationPositionMilliseconds = 0;
        _coordinationDurationMilliseconds = 0;
        _coordinationEndedSessionId = null;
        _state.ClearCoordination();
        if (room is not null) _state.ActivePlaybackRoom = room;
    }

    private async Task<Exception?> AbandonPlaybackRoomAsync()
    {
        var room = _state.ActivePlaybackRoom;
        var client = _state.Client;
        AbandonPlaybackCoordination(preserveActiveRoom: room is not null);
        if (room is null || client is null) return null;
        try
        {
            await client.LeavePlaybackRoomAsync(room.Id, CancellationToken.None);
            if (_state.ActivePlaybackRoom?.Id == room.Id) _state.ActivePlaybackRoom = null;
            return null;
        }
        catch (RivuneServerException exception) when (exception.StatusCode is 403 or 404)
        {
            if (_state.ActivePlaybackRoom?.Id == room.Id) _state.ActivePlaybackRoom = null;
            return null;
        }
        catch (Exception exception)
        {
            if (ReferenceEquals(client, _state.Client)) StartPlaybackCoordination();
            return exception;
        }
    }

    private async Task RunPlaybackCoordinationAsync(RivuneApiClient client, CancellationToken cancellationToken)
    {
        var nextPresence = DateTimeOffset.MinValue;
        while (!cancellationToken.IsCancellationRequested && ReferenceEquals(client, _state.Client))
        {
            try
            {
                if (DateTimeOffset.UtcNow >= nextPresence)
                {
                    var item = _state.PlaybackSession is null ? null : _state.CoordinatedItem;
                    var status = item is null ? "idle" : _coordinationStatus;
                    await client.UpdatePlaybackDeviceAsync(new PlaybackDeviceHeartbeatInput
                    {
                        Capabilities = ["remote-control", "watch-room"],
                        State = new PlaybackDeviceState
                        {
                            Status = status,
                            Item = item,
                            PositionMilliseconds = item is null ? 0 : _coordinationPositionMilliseconds,
                            DurationMilliseconds = item is null ? 0 : _coordinationDurationMilliseconds,
                        },
                    }, cancellationToken);
                    var devices = await client.GetPlaybackDevicesAsync(cancellationToken);
                    _state.PlaybackDevices = devices.Devices.Where(device => !device.Current).ToArray();
                    nextPresence = DateTimeOffset.UtcNow + CoordinationPollingPolicy.PresenceInterval;
                }

                var commands = await client.GetPlaybackCommandsAsync(_lastPlaybackOperationId, cancellationToken);
                if (commands.Commands.Count > 0) _coordinationPollingPolicy.MarkCommandActivity();
                foreach (var command in commands.Commands)
                {
                    var persisted = _playbackOperationJournal.Find(command.OperationId);
                    PlaybackOperationStatus status;
                    PlaybackOperationCode code;
                    if (persisted is not null)
                    {
                        status = persisted.Status;
                        code = persisted.Code;
                    }
                    else
                    {
                        _coordinationOperationExecuting = true;
                        try
                        {
                            (status, code) = await ApplyPlaybackCommandAsync(command, client, cancellationToken);
                            _playbackOperationJournal.Record(command.OperationId, status, code);
                        }
                        finally { _coordinationOperationExecuting = false; }
                    }
                    await client.PutPlaybackCommandResultAsync(command.OperationId, new PlaybackOperationResultInput
                    {
                        Status = status,
                        Code = code,
                    }, cancellationToken);
                    _lastPlaybackOperationId = command.OperationId;
                }
                var currentItem = _state.PlaybackSession is null ? null : _state.CoordinatedItem;
                await RefreshPlaybackRoomAsync(client, currentItem, cancellationToken);
                RefreshCoordinationActions();
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { return; }
            catch { }

            var interval = _coordinationPollingPolicy.CommandInterval(
                _state.PlaybackSession is not null,
                _state.ActivePlaybackRoom is not null,
                _coordinationOperationExecuting);
            try { await Task.Delay(interval, cancellationToken); }
            catch (OperationCanceledException) { return; }
        }
    }

    private async Task<(PlaybackOperationStatus Status, PlaybackOperationCode Code)> ApplyPlaybackCommandAsync(
        PlaybackCommand command,
        RivuneApiClient client,
        CancellationToken cancellationToken)
    {
        if (command.Command == PlaybackCommandKind.Load)
        {
            if (command.Item is null || command.Mode is null)
                return (PlaybackOperationStatus.Failed, PlaybackOperationCode.InvalidState);
            try
            {
                await StartCoordinatedPlaybackAsync(command.Item, command.PositionMilliseconds ?? 0, client, cancellationToken, endCurrentPlayback: true);
                if (_state.PlaybackSession is null)
                    throw new InvalidOperationException("Remote playback could not find a compatible source.");
                return (PlaybackOperationStatus.Applied, PlaybackOperationCode.Applied);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
            catch (Exception exception) when (PlaybackCoordinationPolicy.IsTerminalRemoteLoadFailure(exception))
            {
                await DispatcherQueue.EnqueueAsync(() =>
                {
                    ShowPlaybackCoordinationError(exception);
                    return Task.CompletedTask;
                });
                return (PlaybackOperationStatus.Failed, PlaybackOperationCode.ExecutionFailed);
            }
        }

        if (_state.PlaybackSession is null)
            return (PlaybackOperationStatus.Failed, PlaybackOperationCode.InvalidState);
        try
        {
            if (command.PositionMilliseconds is long position)
                _mediaPlayer.PlaybackSession.Position = MediaPlaybackPosition(position / 1_000d);
            switch (command.Command)
            {
                case PlaybackCommandKind.Play when _playbackCompleted || _mediaPlayer.Source is null:
                {
                    var restartFailure = await RestartPlaybackWithTracksAsync(0, showRecovery: false);
                    if (restartFailure is not null) throw restartFailure;
                    break;
                }
                case PlaybackCommandKind.Play: _mediaPlayer.Play(); break;
                case PlaybackCommandKind.Pause: _mediaPlayer.Pause(); break;
                case PlaybackCommandKind.Stop: await EndPlaybackAsync(completed: false, returnToDashboard: true); break;
                case PlaybackCommandKind.Seek: break;
                default: return (PlaybackOperationStatus.Failed, PlaybackOperationCode.Unsupported);
            }
            return (PlaybackOperationStatus.Applied, PlaybackOperationCode.Applied);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
        catch (Exception exception)
        {
            ShowPlaybackCoordinationError(exception);
            return (PlaybackOperationStatus.Failed, PlaybackOperationCode.ExecutionFailed);
        }
    }

    private void ShowPlaybackCoordinationError(Exception exception)
    {
        if (_closed) return;
        var message = $"Remote playback command failed: {FriendlyError(exception)}";
        DashboardBanner.Severity = InfoBarSeverity.Error;
        DashboardBanner.Message = message;
        DashboardBanner.IsOpen = true;
        DetailBanner.Severity = InfoBarSeverity.Error;
        DetailBanner.Message = message;
        DetailBanner.IsOpen = true;
        DetailStatus.Visibility = Visibility.Visible;
        SourceBanner.Severity = InfoBarSeverity.Error;
        SourceBanner.Message = message;
        SourceBanner.IsOpen = true;
        if (PlayerView.Visibility == Visibility.Visible)
            SetPlayerStatus(message, liveSetting: AutomationLiveSetting.Assertive);
    }

    private async Task<Exception?> LeaveParticipantPlaybackRoomAsync(RivuneApiClient? client)
    {
        var room = _state.ActivePlaybackRoom;
        if (room is null || room.CurrentMemberIsHost || client is null) return null;
        try
        {
            await client.LeavePlaybackRoomAsync(room.Id, CancellationToken.None);
            if (_state.ActivePlaybackRoom?.Id == room.Id) _state.ActivePlaybackRoom = null;
            return null;
        }
        catch (RivuneServerException exception) when (exception.StatusCode is 403 or 404)
        {
            if (_state.ActivePlaybackRoom?.Id == room.Id) _state.ActivePlaybackRoom = null;
            return null;
        }
        catch (Exception exception) { return exception; }
    }

    private async Task<Exception?> EndActivePlaybackRoomAsync(PlaybackSession? session, RivuneApiClient? client)
    {
        var participantLeaveFailure = await LeaveParticipantPlaybackRoomAsync(client);
        if (participantLeaveFailure is not null) return participantLeaveFailure;
        var room = _state.ActivePlaybackRoom;
        if (room is null || client is null || !room.CurrentMemberIsHost ||
            session is not null && _coordinationEndedSessionId == session.Id) return null;
        if (room.State == "ended")
        {
            if (session is not null) _coordinationEndedSessionId = session.Id;
            return null;
        }

        const int maximumAttempts = 3;
        for (var attempt = 0; attempt < maximumAttempts; attempt++)
        {
            try
            {
                var ended = await client.UpdatePlaybackRoomAsync(room.Id, new PlaybackRoomUpdateInput
                {
                    State = "ended",
                    PositionMilliseconds = _coordinationPositionMilliseconds,
                    DurationMilliseconds = _coordinationDurationMilliseconds,
                    ExpectedVersion = room.Version,
                }, CancellationToken.None);
                _state.ActivePlaybackRoom = ended.PreservingJoinCodeFrom(room);
                if (session is not null) _coordinationEndedSessionId = session.Id;
                return null;
            }
            catch (RivuneServerException exception) when (exception.StatusCode == 409 && attempt + 1 < maximumAttempts)
            {
                try
                {
                    var refreshed = (await client.GetPlaybackRoomAsync(room.Id, CancellationToken.None)).PreservingJoinCodeFrom(room);
                    _state.ActivePlaybackRoom = refreshed;
                    if (refreshed.State == "ended")
                    {
                        if (session is not null) _coordinationEndedSessionId = session.Id;
                        return null;
                    }
                    if (!refreshed.CurrentMemberIsHost) return null;
                    room = refreshed;
                }
                catch (Exception refreshException)
                {
                    return refreshException;
                }
            }
            catch (RivuneServerException exception) when (exception.StatusCode is 403 or 404)
            {
                _state.ActivePlaybackRoom = null;
                return null;
            }
            catch (Exception exception)
            {
                return exception;
            }
        }
        return null;
    }

    private async Task RefreshPlaybackRoomAsync(RivuneApiClient client, CoordinatedPlaybackItem? item, CancellationToken cancellationToken)
    {
        var room = _state.ActivePlaybackRoom;
        if (room is null) return;
        try
        {
            PlaybackRoom refreshed;
            if (room.CurrentMemberIsHost && room.State != "ended" && item?.TitleId == room.Item.TitleId)
            {
                try
                {
                    refreshed = await client.UpdatePlaybackRoomAsync(room.Id, new PlaybackRoomUpdateInput
                    {
                        State = _coordinationStatus switch
                        {
                            "playing" => "playing",
                            "ended" => "ended",
                            _ => "paused",
                        },
                        PositionMilliseconds = _coordinationPositionMilliseconds,
                        DurationMilliseconds = _coordinationDurationMilliseconds,
                        ExpectedVersion = room.Version,
                    }, cancellationToken);
                }
                catch { refreshed = await client.GetPlaybackRoomAsync(room.Id, cancellationToken); }
            }
            else refreshed = await client.GetPlaybackRoomAsync(room.Id, cancellationToken);
            _state.ActivePlaybackRoom = refreshed.PreservingJoinCodeFrom(room);
            ApplyPlaybackRoomState();
        }
        catch (RivuneServerException exception) when (exception.StatusCode is 403 or 404)
        {
            _state.ActivePlaybackRoom = null;
        }
    }

    private void ApplyPlaybackRoomState()
    {
        var room = _state.ActivePlaybackRoom;
        if (room is null || room.CurrentMemberIsHost || _state.PlaybackSession is null) return;
        var target = room.PositionMilliseconds / 1_000d;
        var current = AbsolutePlaybackPosition(_mediaPlayer.PlaybackSession.Position);
        if (Math.Abs(current - target) > 1.5) _mediaPlayer.PlaybackSession.Position = MediaPlaybackPosition(target);
        switch (room.State)
        {
            case "playing": _mediaPlayer.Play(); break;
            case "paused": _mediaPlayer.Pause(); break;
            case "ended":
                _ = LeaveEndedParticipantRoomAsync(room);
                break;
        }
    }

    private async Task LeaveEndedParticipantRoomAsync(PlaybackRoom room)
    {
        var client = _state.Client;
        if (client is null || room.CurrentMemberIsHost || _state.ActivePlaybackRoom?.Id != room.Id) return;
        Exception? failure = null;
        try { await EndPlaybackAsync(completed: false, returnToDashboard: true); }
        catch (Exception exception) { failure = exception; }
        var leaveFailure = await LeaveParticipantPlaybackRoomAsync(client);
        failure ??= leaveFailure;
        if (failure is not null) ShowPlaybackCoordinationError(failure);
        RefreshCoordinationActions();
    }

    private async Task StartCoordinatedPlaybackAsync(
        CoordinatedPlaybackItem item,
        long positionMilliseconds,
        RivuneApiClient client,
        CancellationToken cancellationToken,
        bool endCurrentPlayback)
    {
        cancellationToken.ThrowIfCancellationRequested();
        if (endCurrentPlayback && _state.PlaybackSession is not null) await EndPlaybackAsync(completed: false, returnToDashboard: true);
        var generation = _state.Transition(AppPhase.Sources);
        _sourceReturnView = DashboardView;
        _playerReturnView = DashboardView;
        _state.CoordinatedItem = item;
        _playbackTitle = item.Title;
        await LoadSourcesAsync(item.MediaType, item.ResourceId, item.TitleId, generation, item.SourceAddonId, tracksProgress: item.MediaType != "tv");
        if (!_state.IsCurrent(generation)) return;
        var source = _sourceOptions.FirstOrDefault()
            ?? throw new InvalidOperationException("No compatible playback source was returned.");
        _state.SelectedSource = source;
        var startSeconds = (int)Math.Clamp(positionMilliseconds / 1_000, 0, int.MaxValue);
        _state.Preparation = await client.PreparePlaybackAsync(source.SourceRef, startSeconds, _state.Token);
        _state.CoordinatedItem = item;
        await ResolveSelectedSourceAtAsync(generation, startSeconds);
        cancellationToken.ThrowIfCancellationRequested();
    }

    private void RefreshCoordinationActions()
    {
        if (DetailView.Visibility == Visibility.Visible) RenderPlaybackCoordinationActions();
    }
    public Task CloseForWindowShutdownAsync()
    {
        lock (_shutdownSync)
        {
            if (_shutdownTask is not null) return _shutdownTask;
            _closed = true;
            _state.Transition(AppPhase.Closing);
            _positionTimer.Stop();
            _chromeTimer.Stop();
            DisposeAccentPalette();
            StopPlaybackCoordinationPolling();
            _heroTimer.Stop();
            _updateOperationCancellation?.Cancel();
            _updateNotifier.Dispose();
            _offlineDownloadCancellation?.Cancel();
            DismissDialogForShutdown();
            _heroSlideCancellation?.Cancel();
            return _shutdownTask = ShutdownAsync();
        }
    }

    private Task ShutdownAsync() => ShutdownDeadline.RunAsync(
        async cancellationToken =>
        {
            var diagnosticReport = _diagnosticClipboardReport;
            if (diagnosticReport is not null)
                await ClearDiagnosticClipboardAsync(
                    diagnosticReport,
                    Volatile.Read(ref _diagnosticClipboardGeneration),
                    waitForExpiry: false).WaitAsync(cancellationToken);
            if (_restoreTask is not null) await _restoreTask.WaitAsync(cancellationToken);
            if (_updateOperationTask is { } updateOperation)
                await updateOperation.WaitAsync(cancellationToken);
            if (_devicePreferencesStore is { } devicePreferencesStore)
                await devicePreferencesStore.DisposeAsync().AsTask().WaitAsync(cancellationToken);
            if (_offlineDownloadTask is { } offlineDownloadTask)
                await offlineDownloadTask.WaitAsync(cancellationToken);
            await _lanDiscovery.DisposeAsync().AsTask().WaitAsync(cancellationToken);
            await _serverAddressOperation.WaitAsync(cancellationToken);

            Task? ending;
            lock (_endingSync) ending = _endingTask;
            if (ending is not null) await ending.WaitAsync(cancellationToken);
            await EndActivePlaybackRoomAsync(_state.PlaybackSession, _state.Client).WaitAsync(cancellationToken);
            if (ending is null && (_state.PlaybackSession is not null || _activeOfflineItem is not null))
            {
                await FlushProgressAsync(false, cancellationToken);
                if (_state.PlaybackSession is not null)
                    await StopSessionOnceAsync().WaitAsync(cancellationToken);
            }
        },
        ShutdownTimeout,
        () =>
        {
            ClearMediaSource();
            ReleaseMediaPlayer(_mediaPlayer);
            _state.Dispose();
            StopOfflinePlayback();
            _offlineMediaStore?.Dispose();
            _serverAddressStore.Dispose();
        });

    private void ShowOnly(UIElement view)
    {
        BootView.Visibility = view == BootView ? Visibility.Visible : Visibility.Collapsed;
        AuthView.Visibility = view == AuthView ? Visibility.Visible : Visibility.Collapsed;
        ProfileView.Visibility = view == ProfileView ? Visibility.Visible : Visibility.Collapsed;
        DashboardView.Visibility = view == DashboardView ? Visibility.Visible : Visibility.Collapsed;
        PlayerView.Visibility = view == PlayerView ? Visibility.Visible : Visibility.Collapsed;
        DetailView.Visibility = view == DetailView ? Visibility.Visible : Visibility.Collapsed;
        SettingsView.Visibility = view == SettingsView ? Visibility.Visible : Visibility.Collapsed;
        LocalizeVisualTree(view);
    }

    private ContentDialog Dialog(string title, string body, string primary) => new() { XamlRoot = XamlRoot, Title = UiText(title), Content = UiText(body), PrimaryButtonText = UiText(primary), CloseButtonText = UiText("Cancel"), DefaultButton = ContentDialogButton.Close };
    private static bool IsAuthenticationFailure(Exception exception) => exception is NotAuthenticatedException || exception is RivuneServerException { StatusCode: 401 };
    private static DateTimeOffset ParseDate(string value) => DateTimeOffset.TryParse(value, CultureInfo.InvariantCulture, DateTimeStyles.AssumeUniversal, out var result) ? result : DateTimeOffset.UtcNow;
    private string ExpiryText(string value) { var remaining = ParseDate(value) - DateTimeOffset.UtcNow; return UiFormat("Expires in {0} minutes", Math.Max(0, (int)Math.Ceiling(remaining.TotalMinutes))); }
    private string RetryText(TimeSpan? retry) => retry is { } value ? UiFormat("in {0} seconds", Math.Max(1, (int)Math.Ceiling(value.TotalSeconds))) : UiText("later");
    private static string FormatTime(TimeSpan value) => value.TotalHours >= 1 ? value.ToString(@"h\:mm\:ss", CultureInfo.InvariantCulture) : value.ToString(@"m\:ss", CultureInfo.InvariantCulture);
    private double AbsolutePlaybackPosition(TimeSpan mediaPosition) => _timeline.ToAbsolutePosition(mediaPosition);

    private TimeSpan MediaPlaybackPosition(double absolutePosition) => _timeline.ToMediaPosition(absolutePosition);

    private double LogicalDurationSeconds(TimeSpan naturalDuration) => _timeline.UpdateDuration(naturalDuration);
    private static async Task<PlaybackCapabilities> DetectPlaybackCapabilitiesAsync(Task<IReadOnlyList<ExternalPlayerApp>> externalPlayersTask)
    {
        var query = new CodecQuery();
        var videoCandidates = new[]
        {
            (Name: "h264", Subtype: CodecSubtypes.VideoFormatH264),
            (Name: "hevc", Subtype: CodecSubtypes.VideoFormatHevc),
        };

        var audioCandidates = new[]
        {
            (Name: "aac", Subtype: CodecSubtypes.AudioFormatAac),
            (Name: "mp3", Subtype: CodecSubtypes.AudioFormatMP3),
            (Name: "ac3", Subtype: CodecSubtypes.AudioFormatDolbyAC3),
            (Name: "eac3", Subtype: CodecSubtypes.AudioFormatDolbyDDPlus),
        };

        IReadOnlyList<string> videoCodecs;
        IReadOnlyList<string> audioCodecs;
        try
        {
            var detectedVideo = new List<string>();
            foreach (var candidate in videoCandidates)
            {
                if ((await query.FindAllAsync(CodecKind.Video, CodecCategory.Decoder, candidate.Subtype)).Count > 0)
                    detectedVideo.Add(candidate.Name);
            }
            var detectedAudio = new List<string>();
            foreach (var candidate in audioCandidates)
            {
                if ((await query.FindAllAsync(CodecKind.Audio, CodecCategory.Decoder, candidate.Subtype)).Count > 0)
                    detectedAudio.Add(candidate.Name);
            }
            videoCodecs = detectedVideo;
            audioCodecs = detectedAudio;
        }
        catch
        {
            videoCodecs = ["h264"];
            audioCodecs = ["aac", "mp3"];
        }

        var containers = new[] { "mp4", "m4v", "mpegts" };
        var profiles = (from container in containers
                        from video in videoCodecs
                        from audio in audioCodecs
                        select new PlaybackMediaProfile
                        {
                            Container = container,
                            VideoCodec = video,
                            AudioCodec = audio,
                            MaximumVideoBitDepth = 8,
                        }).ToArray();
        return new PlaybackCapabilities
        {
            StreamingProtocols = ["hls", "http"],
            Containers = containers,
            VideoCodecs = videoCodecs,
            AudioCodecs = audioCodecs,
            HdrFormats = ["sdr"],
            ExternalPlayers = (await externalPlayersTask).Count > 0 ? ["windows_process"] : null,
            ProcessingModes = [PlaybackProcessingMode.Remux, PlaybackProcessingMode.TranscodeAudio, PlaybackProcessingMode.Transcode],
            MaximumHeight = 2160,
            MaximumAudioChannels = 2,
            SubtitleModes = [PlaybackSubtitleDelivery.External, PlaybackSubtitleDelivery.Burn],
            MediaProfiles = profiles,
        };
    }

    private PlaybackCapabilities ApplyNetworkQuality(PlaybackCapabilities detected)
    {
        var networkClass = CurrentNetworkClass();
        var preset = networkClass switch
        {
            NetworkClass.Local => _devicePreferences.LocalQuality,
            NetworkClass.RemoteWifi => _devicePreferences.RemoteWifiQuality,
            _ => _devicePreferences.MobileQuality,
        };
        var limit = PlaybackQualityPolicy.Limit(preset, networkClass);
        return detected with
        {
            MaximumHeight = PlaybackQualityPolicy.Cap(detected.MaximumHeight, limit.MaximumHeight),
            MaximumVideoBitrateKbps = PlaybackQualityPolicy.Cap(detected.MaximumVideoBitrateKbps, limit.MaximumVideoBitrateKbps),
        };
    }

    private NetworkClass CurrentNetworkClass()
    {
        if (_state.Client is { } client && TrustedLocalTransport.IsTrustedLocalHost(client.ServerOrigin.Host))
            return NetworkClass.Local;
        try
        {
            var profile = NetworkInformation.GetInternetConnectionProfile();
            var cost = profile?.GetConnectionCost();
            if (profile is null || cost?.NetworkCostType is NetworkCostType.Variable or NetworkCostType.Fixed || cost?.Roaming == true)
                return NetworkClass.Mobile;
            return profile.IsWlanConnectionProfile ? NetworkClass.RemoteWifi : NetworkClass.Local;
        }
        catch { return NetworkClass.Mobile; }
    }

    private static string FriendlyError(Exception exception) => exception switch
    {
        InvalidServerUrlException => "That server address is not allowed. Use HTTPS or trusted-local HTTP with localhost or a literal private address.",
        IncompatibleProtocolException => "This server uses an incompatible Rivune protocol version.",
        NotAuthenticatedException => "Your session is no longer valid. Authorize this device again.",
        RivuneServerException server => server.Message,
        HttpRequestException => "The server could not be reached. Check the address and network connection.",
        _ => exception.Message,
    };
    private sealed record SourceRequest(string MediaType, string ResourceId, Guid TitleId, Guid? AddonId, bool TracksProgress);
    private sealed record EpisodeSelection(Series Series, Episode Episode);
    private sealed record ProgressSnapshot(int Position, int Duration, bool Completed);
}

internal static class DispatcherQueueExtensions
{
    public static Task EnqueueAsync(this MicrosoftDispatcherQueue dispatcher, Func<Task> action)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        if (!dispatcher.TryEnqueue(async () =>
        {
            try { await action(); completion.SetResult(); }
            catch (Exception exception) { completion.SetException(exception); }
        })) completion.SetResult();
        return completion.Task;
    }
}
