using System.Text.Json;
using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class WindowsDevicePreferencesTests : IDisposable
{
    private readonly string _directory = Path.Combine(Path.GetTempPath(), $"rivune-device-preferences-{Guid.NewGuid():N}");
    private string FilePath => Path.Combine(_directory, "device-preferences.v1.json");

    [Fact]
    public async Task MissingFileUsesFreshWindowsDefaults()
    {
        await using var store = new WindowsDevicePreferencesStore(FilePath);

        Assert.Equal(new WindowsDevicePreferences(), store.Snapshot);
        Assert.Equal(ViewerTab.Home, store.Snapshot.StartupTab);
        Assert.False(store.Snapshot.AutomaticallyShowSources);
        Assert.Equal(0, store.Snapshot.VideoAspectIndex);
        Assert.Equal(DeviceMotionPreference.System, store.Snapshot.Motion);
        Assert.Equal(WindowsDevicePreferences.DefaultAccentColor, store.Snapshot.AccentColor);
        Assert.False(store.Snapshot.AutoSkipIntro);
        Assert.False(store.Snapshot.AutoSkipRecap);
        Assert.False(store.Snapshot.AutoSkipOutro);
        Assert.Null(store.Snapshot.LastSuccessfulUpdateCheckAt);
    }

    [Fact]
    public async Task PreferencesRoundTrip()
    {
        var expected = new WindowsDevicePreferences
        {
            StartupTab = ViewerTab.Calendar,
            AutomaticallyShowSources = true,
            VideoAspectIndex = 2,
            Motion = DeviceMotionPreference.Reduced,
            AccentColor = WindowsDevicePreferences.RoseAccentColor,
            AutoSkipIntro = true,
            AutoSkipRecap = true,
            AutoSkipOutro = true,
            LastSuccessfulUpdateCheckAt = new DateTimeOffset(2026, 8, 19, 12, 34, 56, TimeSpan.Zero),
        };

        await using (var writer = new WindowsDevicePreferencesStore(FilePath))
        {
            await writer.SaveAsync(expected, TestContext.Current.CancellationToken);
            Assert.Equal(expected, writer.Snapshot);
        }

        await using var reader = new WindowsDevicePreferencesStore(FilePath);
        Assert.Equal(expected, reader.Snapshot);
    }

    [Theory]
    [InlineData("not json")]
    [InlineData("null")]
    [InlineData("{\"version\":2,\"startupTab\":\"library\"}")]
    [InlineData("{\"version\":1,\"startupTab\":[]}")]
    public async Task MalformedOrUnsupportedContentUsesDefaults(string content)
    {
        Directory.CreateDirectory(_directory);
        File.WriteAllText(FilePath, content);

        await using var store = new WindowsDevicePreferencesStore(FilePath);

        Assert.Equal(new WindowsDevicePreferences(), store.Snapshot);
    }

    [Fact]
    public async Task DeserializedValuesAreClampedAndNormalizedWhileUnknownFieldsAreIgnored()
    {
        Directory.CreateDirectory(_directory);
        File.WriteAllText(FilePath, """
            {
              "version": 1,
              "startupTab": "not-a-tab",
              "automaticallyShowSources": false,
              "videoAspectIndex": 99,
              "motion": "not-a-motion-policy",
              "autoSkipIntro": true,
              "autoSkipRecap": true,
              "autoSkipOutro": false,
              "futureSetting": "ignored"
            }
            """);

        await using var store = new WindowsDevicePreferencesStore(FilePath);

        Assert.Equal(ViewerTab.Home, store.Snapshot.StartupTab);
        Assert.False(store.Snapshot.AutomaticallyShowSources);
        Assert.Equal(2, store.Snapshot.VideoAspectIndex);
        Assert.Equal(DeviceMotionPreference.System, store.Snapshot.Motion);
        Assert.Equal(WindowsDevicePreferences.DefaultAccentColor, store.Snapshot.AccentColor);
        Assert.True(store.Snapshot.AutoSkipIntro);
        Assert.True(store.Snapshot.AutoSkipRecap);
        Assert.False(store.Snapshot.AutoSkipOutro);
    }

    [Fact]
    public async Task UnsupportedPersistedAccentUsesBlueWithoutDiscardingOtherPreferences()
    {
        Directory.CreateDirectory(_directory);
        File.WriteAllText(FilePath, """
            {"version":1,"startupTab":"search","accentColor":"#123456"}
            """);

        await using var store = new WindowsDevicePreferencesStore(FilePath);

        Assert.Equal(ViewerTab.Search, store.Snapshot.StartupTab);
        Assert.Equal(WindowsDevicePreferences.DefaultAccentColor, store.Snapshot.AccentColor);
    }

    [Fact]
    public void PreferenceInstancesConstrainInvalidValues()
    {
        var preferences = new WindowsDevicePreferences
        {
            StartupTab = (ViewerTab)99,
            VideoAspectIndex = -1,
            Motion = (DeviceMotionPreference)99,
            AccentColor = "#123456",
        };

        Assert.Equal(ViewerTab.Home, preferences.StartupTab);
        Assert.Equal(0, preferences.VideoAspectIndex);
        Assert.Equal(DeviceMotionPreference.System, preferences.Motion);
        Assert.Equal(WindowsDevicePreferences.DefaultAccentColor, preferences.AccentColor);
        Assert.Equal(WindowsDevicePreferences.GreenAccentColor, (preferences with { AccentColor = "#71c99a" }).AccentColor);
        Assert.Equal(2, (preferences with { VideoAspectIndex = 3 }).VideoAspectIndex);
    }

    [Fact]
    public async Task SaveOverwritesAtomically()
    {
        await using var store = new WindowsDevicePreferencesStore(FilePath);
        await store.SaveAsync(
            new WindowsDevicePreferences { StartupTab = ViewerTab.Search, VideoAspectIndex = 1 },
            TestContext.Current.CancellationToken);
        var expected = new WindowsDevicePreferences
        {
            StartupTab = ViewerTab.Library,
            AutomaticallyShowSources = false,
            VideoAspectIndex = 2,
            Motion = DeviceMotionPreference.Full,
            AccentColor = WindowsDevicePreferences.VioletAccentColor,
            AutoSkipOutro = true,
        };

        await store.SaveAsync(expected, TestContext.Current.CancellationToken);

        Assert.Equal(expected, store.Snapshot);
        using var document = JsonDocument.Parse(File.ReadAllText(FilePath));
        Assert.Equal(1, document.RootElement.GetProperty("version").GetInt32());
        Assert.Equal("library", document.RootElement.GetProperty("startupTab").GetString());
        Assert.Equal(WindowsDevicePreferences.VioletAccentColor, document.RootElement.GetProperty("accentColor").GetString());
        Assert.Empty(Directory.EnumerateFiles(_directory, "*.tmp", SearchOption.TopDirectoryOnly));
        await using var reloaded = new WindowsDevicePreferencesStore(FilePath);
        Assert.Equal(expected, reloaded.Snapshot);
    }

    [Fact]
    public async Task ConcurrentUpdatesMergeAgainstLatestSnapshot()
    {
        await using var store = new WindowsDevicePreferencesStore(FilePath);
        var checkedAt = new DateTimeOffset(2026, 8, 19, 12, 34, 56, TimeSpan.Zero);

        var updateStartup = store.UpdateAsync(
            preferences => preferences with { StartupTab = ViewerTab.Library },
            TestContext.Current.CancellationToken);
        var updateCheck = store.UpdateAsync(
            preferences => preferences with { LastSuccessfulUpdateCheckAt = checkedAt },
            TestContext.Current.CancellationToken);
        await Task.WhenAll(updateStartup, updateCheck);

        Assert.Equal(ViewerTab.Library, store.Snapshot.StartupTab);
        Assert.Equal(checkedAt, store.Snapshot.LastSuccessfulUpdateCheckAt);
        await using var reloaded = new WindowsDevicePreferencesStore(FilePath);
        Assert.Equal(store.Snapshot, reloaded.Snapshot);
    }

    [Fact]
    public async Task DisposeAsyncQuiescesAcceptedSavesAndIsIdempotent()
    {
        var store = new WindowsDevicePreferencesStore(FilePath);
        var saves = Enumerable.Range(0, 32)
            .Select(index => store.SaveAsync(new WindowsDevicePreferences
            {
                StartupTab = index % 2 == 0 ? ViewerTab.Library : ViewerTab.Search,
                VideoAspectIndex = index % 3,
            }, TestContext.Current.CancellationToken))
            .ToArray();

        var firstDisposal = store.DisposeAsync().AsTask();
        var concurrentDisposal = store.DisposeAsync().AsTask();

        await Task.WhenAll(firstDisposal, concurrentDisposal);
        Assert.All(saves, save => Assert.True(save.IsCompletedSuccessfully));
        await store.DisposeAsync();
        Assert.Throws<ObjectDisposedException>(() => _ = store.Snapshot);
        await Assert.ThrowsAsync<ObjectDisposedException>(() => store.SaveAsync(
            new WindowsDevicePreferences(),
            TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task OversizedContentUsesDefaultsWithoutReadingUnboundedData()
    {
        Directory.CreateDirectory(_directory);
        using (var stream = File.Create(FilePath))
        {
            stream.SetLength(16 * 1024 + 1);
        }

        await using var store = new WindowsDevicePreferencesStore(FilePath);

        Assert.Equal(new WindowsDevicePreferences(), store.Snapshot);
    }

    [Fact]
    public async Task SaveDoesNotSwallowIoFailures()
    {
        Directory.CreateDirectory(_directory);
        var nonDirectory = Path.Combine(_directory, "not-a-directory");
        await File.WriteAllTextAsync(nonDirectory, "occupied", TestContext.Current.CancellationToken);
        await using var store = new WindowsDevicePreferencesStore(Path.Combine(nonDirectory, "preferences.json"));

        await Assert.ThrowsAnyAsync<IOException>(() => store.SaveAsync(
            new WindowsDevicePreferences { StartupTab = ViewerTab.Search },
            TestContext.Current.CancellationToken));
    }

    public void Dispose()
    {
        try { Directory.Delete(_directory, recursive: true); }
        catch (DirectoryNotFoundException) { }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }
}
