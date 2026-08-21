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
    private readonly ServerAddressStore _serverAddressStore = new();
    private WindowsDevicePreferencesStore? _devicePreferencesStore;
    private WindowsDevicePreferences _devicePreferences = new();
    private string? _devicePreferencesFailure;
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
    private SourceRequest? _sourceRequest;
    private Control? _sourceInvoker;
    private UIElement? _sourceReturnView;
    private UIElement? _playerReturnView;
    private Task<PlaybackCapabilities>? _playbackCapabilitiesTask;
    private bool _timelineFromPlayer;
    private bool _tvInputMode;
    private readonly HashSet<ButtonBase> _zoomPointerButtons = [];
    private readonly HashSet<ButtonBase> _zoomFocusedButtons = [];
    private bool _closed;
    private string? _startupUpdateError;
    private const VirtualKey MediaNextTrackKey = (VirtualKey)0xB0;
    private const VirtualKey MediaPreviousTrackKey = (VirtualKey)0xB1;
    private const VirtualKey MediaStopKey = (VirtualKey)0xB2;
    private const VirtualKey MediaPlayPauseKey = (VirtualKey)0xB3;

    public MainPage()
    {
        InitializeComponent();
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
        try
        {
            _devicePreferencesStore = new WindowsDevicePreferencesStore();
            _devicePreferences = _devicePreferencesStore.Snapshot;
        }
        catch (Exception exception)
        {
            _devicePreferencesFailure = FriendlyError(exception);
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
        if (!_closed) _ = CloseForWindowShutdownAsync();
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

            if (!Uri.TryCreate(saved, UriKind.Absolute, out var server))
                throw new InvalidServerUrlException(saved);
            var client = new RivuneApiClient(server);
            ServerAddressBox.Text = server.GetLeftPart(UriPartial.Authority);
            _state.Client = client;
            _state.Discovery = await client.DiscoverAsync(deadline.Token);
            if (!_state.IsCurrent(generation)) return;
            if (_state.Discovery.SetupRequired)
            {
                var addressCleared = await ClearSavedServerAsync();
                if (!_state.IsCurrent(generation)) return;
                _state.ResetServer();
                ShowServer(addressCleared
                    ? "This Rivune server must finish setup before you can connect."
                    : "This Rivune server must finish setup before you can connect. The saved address could not be removed; fix local file access before restarting Rivune.");
                return;
            }
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
            _state.ResetServer();
            ShowServer("Restoring the saved session timed out. Check the server address and try again.");
        }
        catch (OperationCanceledException) when (_state.IsCurrent(generation))
        {
            _state.ResetServer();
            ShowServer("Session restore was cancelled. Try connecting again.");
        }
        catch (OperationCanceledException) { }
        catch (InvalidServerUrlException exception)
        {
            if (!_state.IsCurrent(generation)) return;
            var addressCleared = await ClearSavedServerAsync();
            _state.ResetServer();
            ServerAddressBox.Text = string.Empty;
            ShowServer(addressCleared
                ? FriendlyError(exception)
                : $"{FriendlyError(exception)} The invalid saved address could not be removed; fix local file access before restarting Rivune.");
        }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            _state.ResetServer();
            ShowServer(FriendlyError(exception));
        }
    }

    private async void Connect_Click(object sender, RoutedEventArgs e) => await ConnectAsync();

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
                var addressCleared = await ClearSavedServerAsync();
                if (!_state.IsCurrent(generation)) return;
                _state.ResetServer();
                ShowServer(addressCleared
                    ? "This Rivune server must finish setup before you can connect."
                    : "This Rivune server must finish setup before you can connect. The previously saved address could not be removed.");
                return;
            }
            _serverAddressOperation = _serverAddressStore.SaveAsync(server.GetLeftPart(UriPartial.Authority), _state.Token);
            await _serverAddressOperation;
            await StartPairingAsync();
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
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
            ServerError.Message = $"The saved server address could not be removed. {exception.Message}";
            ServerError.IsOpen = true;
            return false;
        }
    }

    private void ShowServer(string? error = null)
    {
        _state.Transition(AppPhase.Server);
        ServerPanel.Visibility = Visibility.Visible;
        PairingPanel.Visibility = Visibility.Collapsed;
        ShowOnly(AuthView);
        ServerError.Message = error ?? string.Empty;
        ServerError.IsOpen = !string.IsNullOrWhiteSpace(error);
        ServerAddressBox.Focus(FocusState.Programmatic);
    }

    private async Task StartPairingAsync(string? preservedFailure = null)
    {
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
            _deviceAuthorization = await client.BeginDeviceAuthorizationAsync(Environment.MachineName, "windows", _state.Token);
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
                interval += exception.RetryAfter ?? TimeSpan.FromSeconds(5);
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
        var generation = _state.Transition(AppPhase.Profiles);
        ShowOnly(ProfileView);
        ProfileBanner.IsOpen = false;
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
        panel.Children.Add(new TextBlock { Text = $"Unlock {profile.Name} to continue.", TextWrapping = TextWrapping.Wrap });
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
        var profileName = _state.Profile?.Name ?? "profile";
        AutomationProperties.SetName(ProfileMenuButton, $"Account for {profileName}");
        AutomationProperties.SetName(DockAccountButton, $"Account for {profileName}");
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
        SourceStatus.Text = "Loading compatible sources…";
        _playbackCapabilitiesTask ??= DetectPlaybackCapabilitiesAsync();
        var capabilities = await _playbackCapabilitiesTask;
        if (!_state.IsCurrent(generation)) return;
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
            SourceBanner.Message = $"No enabled source add-on returned a stream for media ID “{resourceId}”.";
            SourceBanner.IsOpen = true;
        }
        else
        {
            SourceBanner.IsOpen = false;
        }
    }

    private static string ProviderFailureMessage(IReadOnlyList<PlaybackProviderError> errors, bool noSources)
    {
        var details = string.Join(" · ", errors.Take(3).Select(error => $"{error.ManifestId}: {error.Message}"));
        if (errors.Count > 3) details += $" · {errors.Count - 3} more";
        return $"{(noSources ? "Source providers failed" : "Some source providers failed")}: {details}";
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
        SourceProgress.IsActive = true;
        SourceStatus.Text = $"Preparing {source.Name}…";
        try
        {
            var progress = _tracksProgress ? await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token) : null;
            var startSeconds = progress is null
                ? (int?)null
                : PlaybackProgressPolicy.StartSeconds(progress.PositionSeconds, progress.Completed);
            var preparation = await client.PreparePlaybackAsync(source.SourceRef, startSeconds, _state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(_state.SelectedSource, source)) return;
            _state.Preparation = preparation;
            SourceStatus.Text = $"Ready · {preparation.Mode} · {preparation.Protocol} · {preparation.Container ?? "automatic"}";
            PlaySourceButton.IsEnabled = true;
            PlaySourceButton.Visibility = Visibility.Visible;
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
        SourceBanner.IsOpen = false;
        SourceProgress.IsActive = true;
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

    private async Task ResolveSelectedSourceAsync(long generation)
    {
        if (_state.SelectedSource is null || _state.Preparation is null) return;
        _preferredSourceAddonId = _state.SelectedSource.AddonId;
        var client = _state.Client;
        if (client is null) return;
        PlaySourceButton.IsEnabled = false;
        SourceProgress.IsActive = true;
        SourceStatus.Text = "Resolving secure playback…";
        try
        {
            var current = _tracksProgress ? await client.GetPlaybackProgressAsync(_progressTitleId, _state.Token) : null;
            var startSeconds = current is null
                ? (int?)null
                : PlaybackProgressPolicy.StartSeconds(current.PositionSeconds, current.Completed);
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
            }
            _playbackCompleted = false;
            _preferredAudioTrack = session.SelectedAudioTrack;
            _preferredSubtitleId = session.SelectedSubtitleId;
            try
            {
                await ShowPlayerAsync(selected, startSeconds ?? 0);
            }
            catch (OperationCanceledException)
            {
                await StopSessionOnceAsync();
                _state.PlaybackSession = null;
                ClearMediaSource();
                throw;
            }
            catch (Exception exception)
            {
                await StopSessionOnceAsync();
                _state.PlaybackSession = null;
                ClearMediaSource();
                _state.Transition(AppPhase.Sources);
                OpenSourcePicker();
                SetSourceFailure(exception);
                return;
            }
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
            if (_state.IsCurrent(generation)) SourceProgress.IsActive = false;
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
        _nextEpisodeTarget = null;
        PlayerNextButton.Visibility = Visibility.Collapsed;
        _aspectIndex = _devicePreferences.VideoAspectIndex;
        ApplyPlayerAspect();
        AutomationProperties.SetName(PlayPauseButton, "Play");
        SourceOverlay.Visibility = Visibility.Collapsed;
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
            if (!_state.IsCurrent(generation) || PlayerView.Visibility != Visibility.Visible || _state.PlaybackSession is null) return;
            var failedPosition = (int)Math.Max(0, AbsolutePlaybackPosition(_mediaPlayer.PlaybackSession.Position));
            await DispatcherQueue.EnqueueAsync(async () =>
            {
                if (!_state.IsCurrent(generation) || _state.PlaybackSession is null) return;
                SetPlayerStatus("Playback did not start within 45 seconds.", liveSetting: AutomationLiveSetting.Assertive);
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

    private async void CloseSources_Click(object sender, RoutedEventArgs e)
    {
        var returnFromDetail = _sourceReturnView == DetailView;
        await CloseSourcesAsync();
        if (returnFromDetail) await NavigateBackFromDetailAsync();
    }
    private Task CloseSourcesAsync()
    {
        _autoStartNextEpisode = false;
        _state.Transition(_sourceReturnView == DetailView ? AppPhase.Detail : AppPhase.Catalogue);
        CloseSourcePicker();
        return Task.CompletedTask;
    }
    private void OpenSourcePicker()
    {
        ExitPlayerPresenterMode();
        if (DetailView.Visibility == Visibility.Visible) _sourceReturnView = DetailView;
        else if (DashboardView.Visibility == Visibility.Visible) _sourceReturnView = DashboardView;
        else _sourceReturnView ??= DashboardView;
        _sourceReturnView.Visibility = Visibility.Visible;
        SourceOverlay.Visibility = Visibility.Visible;
        DetailBackButton.Visibility = Visibility.Collapsed;
        PlayerView.Visibility = Visibility.Collapsed;
        RefreshSourcesButton.Focus(FocusState.Programmatic);
    }

    private void CloseSourcePicker()
    {
        SourceOverlay.Visibility = Visibility.Collapsed;
        DetailBackButton.Visibility = Visibility.Visible;
        ShowOnly(_sourceReturnView ?? DashboardView);
        _sourceInvoker?.Focus(FocusState.Programmatic);
        _sourceInvoker = null;
    }

    private async void ClosePlayer_Click(object sender, RoutedEventArgs e)
    {
        if (_playerControlsLocked)
        {
            SetPlayerControlsLocked(false);
            return;
        }
        await EndPlaybackAsync(completed: false, returnToDashboard: true);
    }
    private async Task EndPlaybackAsync(bool completed, bool returnToDashboard)
    {
        if (completed) _playbackCompleted = true;
        var completionDrain = completed ? QueueProgress(true) : Task.CompletedTask;
        Task ending;
        Task? returning = null;
        CancellationTokenSource? trackRestartCancellation = null;
        lock (_endingSync)
        {
            ending = _endingTask ??= EndPlaybackCoreAsync(_state.PlaybackSession, _state.Client, _mediaPlayer);
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
        _chromeTimer.Stop();
        _playerStartupCancellation?.Cancel();
        _positionTimer.Stop();
        await FlushProgressAsync(_playbackCompleted, CancellationToken.None);
        await StopSessionOnceAsync(session, client);
        if (!ReferenceEquals(player, _mediaPlayer)) return;
        player.Pause();
        ClearMediaSource();
        SetPlayerControlsLocked(false);
    }


    private async Task ReturnFromPlayerAsync(Task ending)
    {
        await ending;
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
            SetPlayerStatus("Playback finished.");
            if (_nextEpisodeTarget is not null && _effectiveSettings?.Settings.AutoplayNextEpisode != false)
            {
                await AdvanceToNextEpisodeAsync();
                return;
            }
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
            if (!ReferenceEquals(sender, _mediaPlayer) || PlayerView.Visibility != Visibility.Visible || _state.PlaybackSession is null) return;
            SetPlayerStatus($"Playback failed: {args.ErrorMessage}", liveSetting: AutomationLiveSetting.Assertive);
            await EndPlaybackAsync(completed: false, returnToDashboard: false);
            if (!ReferenceEquals(sender, _mediaPlayer)) return;
            await ShowPlaybackRecoveryAsync(args.ErrorMessage, failedPosition);
        });
    }

    private async Task ShowPlaybackRecoveryAsync(string errorMessage, int failedPosition)
    {
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = "Playback stopped",
            Content = string.IsNullOrWhiteSpace(errorMessage) ? "Rivune couldn’t continue this source." : errorMessage,
            PrimaryButtonText = "Retry",
            SecondaryButtonText = "Start over",
            CloseButtonText = "Choose another source",
            DefaultButton = ContentDialogButton.Primary,
        };
        var result = await ShowDialogAsync(dialog);
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
        if (_state.PlaybackSession is not null) await EndPlaybackAsync(false, false);
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

    public Task HandleWindowActivationAsync(bool active)
    {
        if (active || PlayerView.Visibility != Visibility.Visible || _mediaPlayer.Source is null)
            return Task.CompletedTask;
        return QueueProgress(false);
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
            _heroTimer.Stop();
            _updateOperationCancellation?.Cancel();
            DismissDialogForShutdown();
            _heroSlideCancellation?.Cancel();
            return _shutdownTask = ShutdownAsync();
        }
    }

    private Task ShutdownAsync() => ShutdownDeadline.RunAsync(
        async cancellationToken =>
        {
            if (_restoreTask is not null) await _restoreTask.WaitAsync(cancellationToken);
            if (_updateOperationTask is { } updateOperation)
                await updateOperation.WaitAsync(cancellationToken);
            if (_devicePreferencesStore is { } devicePreferencesStore)
                await devicePreferencesStore.DisposeAsync().AsTask().WaitAsync(cancellationToken);
            await _serverAddressOperation.WaitAsync(cancellationToken);

            Task? ending;
            lock (_endingSync) ending = _endingTask;
            if (ending is not null)
            {
                await ending.WaitAsync(cancellationToken);
            }
            else if (_state.PlaybackSession is not null)
            {
                await FlushProgressAsync(false, cancellationToken);
                await StopSessionOnceAsync().WaitAsync(cancellationToken);
            }
        },
        ShutdownTimeout,
        () =>
        {
            ClearMediaSource();
            ReleaseMediaPlayer(_mediaPlayer);
            _state.Dispose();
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
    }

    private ContentDialog Dialog(string title, string body, string primary) => new() { XamlRoot = XamlRoot, Title = title, Content = body, PrimaryButtonText = primary, CloseButtonText = "Cancel", DefaultButton = ContentDialogButton.Close };
    private static bool IsAuthenticationFailure(Exception exception) => exception is NotAuthenticatedException || exception is RivuneServerException { StatusCode: 401 };
    private static DateTimeOffset ParseDate(string value) => DateTimeOffset.TryParse(value, CultureInfo.InvariantCulture, DateTimeStyles.AssumeUniversal, out var result) ? result : DateTimeOffset.UtcNow;
    private static string ExpiryText(string value) { var remaining = ParseDate(value) - DateTimeOffset.UtcNow; return $"Expires in {Math.Max(0, (int)Math.Ceiling(remaining.TotalMinutes))} minutes"; }
    private static string RetryText(TimeSpan? retry) => retry is { } value ? $"in {Math.Max(1, (int)Math.Ceiling(value.TotalSeconds))} seconds" : "later";
    private static string FormatTime(TimeSpan value) => value.TotalHours >= 1 ? value.ToString(@"h\:mm\:ss", CultureInfo.InvariantCulture) : value.ToString(@"m\:ss", CultureInfo.InvariantCulture);
    private double AbsolutePlaybackPosition(TimeSpan mediaPosition) => _timeline.ToAbsolutePosition(mediaPosition);

    private TimeSpan MediaPlaybackPosition(double absolutePosition) => _timeline.ToMediaPosition(absolutePosition);

    private double LogicalDurationSeconds(TimeSpan naturalDuration) => _timeline.UpdateDuration(naturalDuration);
    private static async Task<PlaybackCapabilities> DetectPlaybackCapabilitiesAsync()
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
            ProcessingModes = [PlaybackProcessingMode.Remux, PlaybackProcessingMode.TranscodeAudio, PlaybackProcessingMode.Transcode],
            MaximumHeight = 2160,
            MaximumAudioChannels = 2,
            SubtitleModes = [PlaybackSubtitleDelivery.External, PlaybackSubtitleDelivery.Burn],
            MediaProfiles = profiles,
        };

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
