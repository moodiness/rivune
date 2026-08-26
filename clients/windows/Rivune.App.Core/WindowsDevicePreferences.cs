using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.App;

internal enum DeviceMotionPreference
{
    System,
    Full,
    Reduced,
}

internal sealed record WindowsDevicePreferences
{
    public const string DefaultAccentColor = "#77A7FF";
    public const string CoralAccentColor = "#FF8F70";
    public const string GreenAccentColor = "#71C99A";
    public const string VioletAccentColor = "#C29AFF";
    public const string RoseAccentColor = "#FF7D8C";

    private ViewerTab _startupTab = ViewerTab.Home;
    private int _videoAspectIndex;
    private DeviceMotionPreference _motion = DeviceMotionPreference.System;
    private string _accentColor = DefaultAccentColor;
    private PlaybackQualityPreset _localQuality = PlaybackQualityPreset.Automatic;
    private PlaybackQualityPreset _remoteWifiQuality = PlaybackQualityPreset.Automatic;
    private PlaybackQualityPreset _mobileQuality = PlaybackQualityPreset.Automatic;
    private int _offlineExpirationDays = 30;
    private long _offlineQuotaBytes = 20L * 1024 * 1024 * 1024;

    public ViewerTab StartupTab
    {
        get => _startupTab;
        init => _startupTab = Enum.IsDefined(value) ? value : ViewerTab.Home;
    }

    public bool AutomaticallyShowSources { get; init; }

    public int VideoAspectIndex
    {
        get => _videoAspectIndex;
        init => _videoAspectIndex = Math.Clamp(value, 0, 2);
    }

    public DeviceMotionPreference Motion
    {
        get => _motion;
        init => _motion = Enum.IsDefined(value) ? value : DeviceMotionPreference.System;
    }

    public string AccentColor
    {
        get => _accentColor;
        init => _accentColor = NormalizeAccentColor(value);
    }

    public bool AutoSkipIntro { get; init; }
    public bool AutoSkipRecap { get; init; }
    public bool AutoSkipOutro { get; init; }
    public DateTimeOffset? LastSuccessfulUpdateCheckAt { get; init; }
    public string? LastPresentedUpdateVersion { get; init; }
    public PlaybackQualityPreset LocalQuality
    {
        get => _localQuality;
        init => _localQuality = Enum.IsDefined(value) ? value : PlaybackQualityPreset.Automatic;
    }

    public PlaybackQualityPreset RemoteWifiQuality
    {
        get => _remoteWifiQuality;
        init => _remoteWifiQuality = Enum.IsDefined(value) ? value : PlaybackQualityPreset.Automatic;
    }

    public PlaybackQualityPreset MobileQuality
    {
        get => _mobileQuality;
        init => _mobileQuality = Enum.IsDefined(value) ? value : PlaybackQualityPreset.Automatic;
    }

    public long OfflineQuotaBytes
    {
        get => _offlineQuotaBytes;
        init => _offlineQuotaBytes = Math.Clamp(value, 1L * 1024 * 1024, 2L * 1024 * 1024 * 1024 * 1024);
    }

    public int OfflineExpirationDays
    {
        get => _offlineExpirationDays;
        init => _offlineExpirationDays = Math.Clamp(value, 0, 3_650);
    }

    public bool DownloadOnMobile { get; init; }

    private static string NormalizeAccentColor(string? value)
    {
        if (string.Equals(value, CoralAccentColor, StringComparison.OrdinalIgnoreCase)) return CoralAccentColor;
        if (string.Equals(value, GreenAccentColor, StringComparison.OrdinalIgnoreCase)) return GreenAccentColor;
        if (string.Equals(value, VioletAccentColor, StringComparison.OrdinalIgnoreCase)) return VioletAccentColor;
        if (string.Equals(value, RoseAccentColor, StringComparison.OrdinalIgnoreCase)) return RoseAccentColor;
        return DefaultAccentColor;
    }
}

internal sealed class WindowsDevicePreferencesStore : IAsyncDisposable
{
    private const int CurrentVersion = 1;
    private const int MaximumFileBytes = 16 * 1024;
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private readonly string _filePath;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private readonly object _lifecycleSync = new();
    private WindowsDevicePreferences _snapshot;
    private Task? _disposeTask;
    private TaskCompletionSource? _savesQuiesced;
    private int _activeSaves;

    public WindowsDevicePreferencesStore(string? filePath = null)
    {
        _filePath = Path.GetFullPath(filePath ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Rivune",
            "device-preferences.v1.json"));
        _snapshot = ReadSnapshot();
    }

    public WindowsDevicePreferences Snapshot
    {
        get
        {
            lock (_lifecycleSync) ThrowIfDisposing();
            return Volatile.Read(ref _snapshot);
        }
    }

    public Task<WindowsDevicePreferences> UpdateAsync(
        Func<WindowsDevicePreferences, WindowsDevicePreferences> update,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(update);
        return WriteAsync(update, cancellationToken);
    }

    public async Task SaveAsync(
        WindowsDevicePreferences preferences,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(preferences);
        await WriteAsync(_ => preferences, cancellationToken).ConfigureAwait(false);
    }

    private async Task<WindowsDevicePreferences> WriteAsync(
        Func<WindowsDevicePreferences, WindowsDevicePreferences> update,
        CancellationToken cancellationToken)
    {
        BeginSave();
        try
        {
            await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                var normalized = update(Volatile.Read(ref _snapshot)) with { };
                var document = PersistedPreferences.From(normalized);
                var bytes = JsonSerializer.SerializeToUtf8Bytes(document, JsonOptions);
                if (bytes.Length > MaximumFileBytes)
                    throw new InvalidDataException("The device preferences are too large.");
                var directory = Path.GetDirectoryName(_filePath)!;
                Directory.CreateDirectory(directory);
                var temporaryPath = Path.Combine(directory, $".{Path.GetFileName(_filePath)}.{Guid.NewGuid():N}.tmp");
                try
                {
                    await using (var stream = new FileStream(
                                     temporaryPath,
                                     FileMode.CreateNew,
                                     FileAccess.Write,
                                     FileShare.None,
                                     bufferSize: 4096,
                                     FileOptions.Asynchronous | FileOptions.WriteThrough))
                    {
                        await stream.WriteAsync(bytes, cancellationToken).ConfigureAwait(false);
                        await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
                    }

                    cancellationToken.ThrowIfCancellationRequested();
                    if (File.Exists(_filePath)) File.Replace(temporaryPath, _filePath, null);
                    else File.Move(temporaryPath, _filePath);
                    Volatile.Write(ref _snapshot, normalized);
                    return normalized;
                }
                finally
                {
                    try { File.Delete(temporaryPath); }
                    catch (IOException) { }
                    catch (UnauthorizedAccessException) { }
                }
            }
            finally
            {
                _gate.Release();
            }
        }
        finally
        {
            EndSave();
        }
    }

    public ValueTask DisposeAsync()
    {
        lock (_lifecycleSync)
        {
            if (_disposeTask is not null) return new ValueTask(_disposeTask);

            _savesQuiesced = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            if (_activeSaves == 0) _savesQuiesced.SetResult();
            return new ValueTask(_disposeTask = DisposeCoreAsync(_savesQuiesced.Task));
        }
    }

    private async Task DisposeCoreAsync(Task savesQuiesced)
    {
        await savesQuiesced.ConfigureAwait(false);
        _gate.Dispose();
    }

    private void BeginSave()
    {
        lock (_lifecycleSync)
        {
            ThrowIfDisposing();
            _activeSaves++;
        }
    }

    private void EndSave()
    {
        TaskCompletionSource? savesQuiesced = null;
        lock (_lifecycleSync)
        {
            _activeSaves--;
            if (_activeSaves == 0) savesQuiesced = _savesQuiesced;
        }
        savesQuiesced?.TrySetResult();
    }

    private WindowsDevicePreferences ReadSnapshot()
    {
        FileStream stream;
        try
        {
            stream = new FileStream(
                _filePath,
                FileMode.Open,
                FileAccess.Read,
                FileShare.Read,
                bufferSize: 4096,
                FileOptions.SequentialScan);
        }
        catch (FileNotFoundException)
        {
            return new WindowsDevicePreferences();
        }
        catch (DirectoryNotFoundException)
        {
            return new WindowsDevicePreferences();
        }

        using (stream)
        {
            if (stream.Length > MaximumFileBytes) return new WindowsDevicePreferences();
            try
            {
                var document = JsonSerializer.Deserialize<PersistedPreferences>(stream, JsonOptions);
                return document is { Version: CurrentVersion }
                    ? document.ToPreferences()
                    : new WindowsDevicePreferences();
            }
            catch (JsonException)
            {
                return new WindowsDevicePreferences();
            }
            catch (NotSupportedException)
            {
                return new WindowsDevicePreferences();
            }
        }
    }

    private void ThrowIfDisposing() => ObjectDisposedException.ThrowIf(_disposeTask is not null, this);

    private sealed record PersistedPreferences
    {
        public int Version { get; init; }
        public string? StartupTab { get; init; } = "home";
        public bool AutomaticallyShowSources { get; init; }
        public int VideoAspectIndex { get; init; }
        public string? Motion { get; init; } = "system";
        public string? AccentColor { get; init; } = WindowsDevicePreferences.DefaultAccentColor;

        public bool AutoSkipIntro { get; init; }
        public bool AutoSkipRecap { get; init; }
        public bool AutoSkipOutro { get; init; }
        public string? LastSuccessfulUpdateCheckAt { get; init; }
        public string? LastPresentedUpdateVersion { get; init; }
        public string? LocalQuality { get; init; } = "automatic";
        public string? RemoteWifiQuality { get; init; } = "automatic";
        public string? MobileQuality { get; init; } = "automatic";
        public long OfflineQuotaBytes { get; init; } = 20L * 1024 * 1024 * 1024;
        public int OfflineExpirationDays { get; init; } = 30;
        public bool DownloadOnMobile { get; init; }

        public WindowsDevicePreferences ToPreferences() => new()
        {
            StartupTab = ParseStartupTab(StartupTab),
            AutomaticallyShowSources = AutomaticallyShowSources,
            VideoAspectIndex = VideoAspectIndex,
            Motion = ParseMotion(Motion),
            AccentColor = AccentColor ?? WindowsDevicePreferences.DefaultAccentColor,

            AutoSkipIntro = AutoSkipIntro,
            AutoSkipRecap = AutoSkipRecap,
            AutoSkipOutro = AutoSkipOutro,
            LastSuccessfulUpdateCheckAt = ParseUpdateCheckTimestamp(LastSuccessfulUpdateCheckAt),
            LastPresentedUpdateVersion = ParseUpdateVersion(LastPresentedUpdateVersion),
            LocalQuality = ParseQuality(LocalQuality),
            RemoteWifiQuality = ParseQuality(RemoteWifiQuality),
            MobileQuality = ParseQuality(MobileQuality),
            OfflineQuotaBytes = OfflineQuotaBytes,
            OfflineExpirationDays = OfflineExpirationDays,
            DownloadOnMobile = DownloadOnMobile,
        };

        public static PersistedPreferences From(WindowsDevicePreferences preferences) => new()
        {
            Version = CurrentVersion,
            StartupTab = preferences.StartupTab.ToString().ToLowerInvariant(),
            AutomaticallyShowSources = preferences.AutomaticallyShowSources,
            VideoAspectIndex = preferences.VideoAspectIndex,
            Motion = preferences.Motion.ToString().ToLowerInvariant(),
            AccentColor = preferences.AccentColor,
            AutoSkipIntro = preferences.AutoSkipIntro,
            AutoSkipRecap = preferences.AutoSkipRecap,
            AutoSkipOutro = preferences.AutoSkipOutro,
            LastSuccessfulUpdateCheckAt = preferences.LastSuccessfulUpdateCheckAt?.ToUniversalTime().ToString("O", System.Globalization.CultureInfo.InvariantCulture),
            LastPresentedUpdateVersion = preferences.LastPresentedUpdateVersion,
            LocalQuality = preferences.LocalQuality.ToString().ToLowerInvariant(),
            RemoteWifiQuality = preferences.RemoteWifiQuality.ToString().ToLowerInvariant(),
            MobileQuality = preferences.MobileQuality.ToString().ToLowerInvariant(),
            OfflineQuotaBytes = preferences.OfflineQuotaBytes,
            OfflineExpirationDays = preferences.OfflineExpirationDays,
            DownloadOnMobile = preferences.DownloadOnMobile,
        };

        private static ViewerTab ParseStartupTab(string? value) =>
            Enum.TryParse<ViewerTab>(value, ignoreCase: true, out var parsed) && Enum.IsDefined(parsed)
                ? parsed
                : ViewerTab.Home;

        private static DeviceMotionPreference ParseMotion(string? value) =>
            Enum.TryParse<DeviceMotionPreference>(value, ignoreCase: true, out var parsed) && Enum.IsDefined(parsed)
                ? parsed
                : DeviceMotionPreference.System;

        private static PlaybackQualityPreset ParseQuality(string? value) =>
            Enum.TryParse<PlaybackQualityPreset>(value, ignoreCase: true, out var parsed) && Enum.IsDefined(parsed)
                ? parsed
                : PlaybackQualityPreset.Automatic;

        private static string? ParseUpdateVersion(string? value)
        {
            if (value is null) return null;
            try
            {
                _ = AppUpdateChecker.CompareSemanticVersions(value, value);
                return value;
            }
            catch (InvalidOperationException)
            {
                return null;
            }
        }

        private static DateTimeOffset? ParseUpdateCheckTimestamp(string? value) =>
            DateTimeOffset.TryParseExact(
                value,
                "O",
                System.Globalization.CultureInfo.InvariantCulture,
                System.Globalization.DateTimeStyles.AssumeUniversal | System.Globalization.DateTimeStyles.AdjustToUniversal,
                out var parsed)
                ? parsed
                : null;
    }
}
