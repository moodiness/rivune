using System.Net;
using System.Security.Cryptography;
using System.Text;
using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class OfflineMediaStoreTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"rivune-offline-{Guid.NewGuid():N}");
    private static readonly Uri Server = new("https://rivune.test");
    private static readonly Guid ProfileId = Guid.Parse("11111111-1111-4111-8111-111111111111");
    private static readonly Guid TitleId = Guid.Parse("22222222-2222-4222-8222-222222222222");

    [Fact]
    public async Task DownloadEncryptsMediaAndReaderSupportsRandomAccess()
    {
        var plaintext = Encoding.UTF8.GetBytes(string.Concat(Enumerable.Repeat("private-offline-video-payload-", 100_000)));
        var protector = new TestKeyProtector();
        using var store = new OfflineMediaStore(_root, protector);
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);

        var item = await store.DownloadAsync(
            scope,
            new Uri(Server, "/media/video.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "Movie",
            "mp4",
            "/poster.jpg",
            handler: new FixedBodyHandler(plaintext),
            cancellationToken: TestContext.Current.CancellationToken);

        var archivePath = Path.Combine(_root, scope, item.FileName);
        var archive = await File.ReadAllBytesAsync(archivePath, TestContext.Current.CancellationToken);
        Assert.DoesNotContain(Convert.ToHexString(plaintext.AsSpan(0, 64)), Convert.ToHexString(archive));
        Assert.Equal(plaintext.Length, item.SizeBytes);
        Assert.Equal(item, Assert.Single(store.Items(scope)));
        var transition = Assert.Single(store.DownloadTransitions());
        Assert.Equal(item.Id, transition.Id);
        Assert.Equal(OfflineMediaState.Ready, transition.State);
        Assert.Equal(0, transition.ReservedBytes);

        var protectedKey = await File.ReadAllBytesAsync(Path.Combine(_root, scope, "key.v1.dpapi"), TestContext.Current.CancellationToken);
        var key = protector.Unprotect(protectedKey);
        try
        {
            using var reader = new EncryptedMediaReader(archivePath, key);
            var sample = new byte[80_000];
            var position = 1_040_000L;
            Assert.Equal(sample.Length, reader.Read(position, sample));
            Assert.Equal(plaintext.AsSpan((int)position, sample.Length).ToArray(), sample);
        }
        finally { CryptographicOperations.ZeroMemory(key); }
    }

    [Fact]
    public async Task ProfilePinAndScopePreventCrossProfileAccess()
    {
        using var store = new OfflineMediaStore(_root, new TestKeyProtector());
        var firstScope = store.RegisterProfile(Server, Profile(hasPin: true), "2468");
        await store.DownloadAsync(
            firstScope,
            new Uri(Server, "/media/video.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "Movie",
            "mp4",
            null,
            handler: new FixedBodyHandler(new byte[1024]),
            cancellationToken: TestContext.Current.CancellationToken);
        var secondScope = store.RegisterProfile(Server, Profile(Guid.Parse("33333333-3333-4333-8333-333333333333"), hasPin: false), null);

        Assert.Throws<InvalidOperationException>(() => store.Items(firstScope));
        store.Lock();
        Assert.False(store.Unlock(firstScope, "0000"));
        Assert.True(store.Unlock(firstScope, "2468"));
        Assert.Single(store.Items(firstScope));
        Assert.Throws<InvalidOperationException>(() => store.Items(secondScope));

        var gate = Assert.Single(store.Profiles());
        Assert.Equal(firstScope, gate.Scope);
        Assert.True(gate.RequiresPin);
    }

    [Fact]
    public async Task RestoringProfileWithoutPinRemovesStaleOfflinePin()
    {
        using var store = new OfflineMediaStore(_root, new TestKeyProtector());
        var scope = store.RegisterProfile(Server, Profile(hasPin: true), "2468");
        await store.DownloadAsync(
            scope,
            new Uri(Server, "/media/video.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "Movie",
            "mp4",
            null,
            handler: new FixedBodyHandler(new byte[1024]),
            cancellationToken: TestContext.Current.CancellationToken);
        Assert.Equal(scope, store.OpenRestoredProfile(Server, Profile(hasPin: false)));
        store.Lock();

        var gate = Assert.Single(store.Profiles());
        Assert.False(gate.RequiresPin);
        Assert.True(store.Unlock(scope, null));
        Assert.Single(store.Items(scope));
    }


    [Fact]
    public async Task RejectedRedirectLeavesNoPartialOrManifestEntry()
    {
        using var store = new OfflineMediaStore(_root, new TestKeyProtector());
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);

        await Assert.ThrowsAsync<InvalidOperationException>(() => store.DownloadAsync(
            scope,
            new Uri(Server, "/media/video.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "Movie",
            "mp4",
            null,
            handler: new RedirectHandler(),
            cancellationToken: TestContext.Current.CancellationToken));

        Assert.Empty(store.Items(scope));
        Assert.DoesNotContain(Directory.EnumerateFiles(Path.Combine(_root, scope)), path =>
            path.EndsWith(".partial", StringComparison.OrdinalIgnoreCase) || path.EndsWith(".rvn", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task InvalidOrMissingManifestNeverDeletesEncryptedArchives()
    {
        using var store = new OfflineMediaStore(_root, new TestKeyProtector());
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);
        var item = await store.DownloadAsync(
            scope,
            new Uri(Server, "/media/video.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "Movie",
            "mp4",
            null,
            handler: new FixedBodyHandler(new byte[1024]),
            cancellationToken: TestContext.Current.CancellationToken);
        var directory = Path.Combine(_root, scope);
        var archive = Path.Combine(directory, item.FileName);
        var manifest = Path.Combine(directory, "manifest.v1.json");

        await File.WriteAllTextAsync(manifest, "{", TestContext.Current.CancellationToken);
        Assert.Throws<InvalidDataException>(() => store.Items(scope));
        Assert.True(File.Exists(archive));

        File.Delete(manifest);
        Assert.Throws<InvalidDataException>(() => store.Items(scope));
        Assert.True(File.Exists(archive));
    }

    [Fact]
    public async Task QuotaUsesArchiveBytesInsteadOfMutableManifestSize()
    {
        using var store = new OfflineMediaStore(_root, new TestKeyProtector(), maximumStoredBytes: 2200);
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);
        await store.DownloadAsync(
            scope,
            new Uri(Server, "/media/first.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "First",
            "mp4",
            null,
            handler: new FixedBodyHandler(new byte[1500]),
            cancellationToken: TestContext.Current.CancellationToken);
        var manifest = Path.Combine(_root, scope, "manifest.v1.json");
        var json = await File.ReadAllTextAsync(manifest, TestContext.Current.CancellationToken);
        Assert.Contains("\"sizeBytes\":1500", json);
        await File.WriteAllTextAsync(manifest, json.Replace("\"sizeBytes\":1500", "\"sizeBytes\":1", StringComparison.Ordinal), TestContext.Current.CancellationToken);

        await Assert.ThrowsAsync<InvalidOperationException>(() => store.DownloadAsync(
            scope,
            new Uri(Server, "/media/second.mp4"),
            uri => uri.Host == Server.Host,
            Guid.NewGuid(),
            "Second",
            "mp4",
            null,
            handler: new FixedBodyHandler(new byte[700]),
            cancellationToken: TestContext.Current.CancellationToken));
        Assert.Single(store.Items(scope));
    }

    [Fact]
    public async Task QuotaIsGlobalAcrossProfileScopesAndCountsRealArchives()
    {
        using var store = new OfflineMediaStore(_root, new TestKeyProtector(), maximumStoredBytes: 2_500);
        var firstScope = store.RegisterProfile(Server, Profile(hasPin: false), null);
        await store.DownloadAsync(firstScope, new Uri(Server, "/media/first.mp4"), uri => uri.Host == Server.Host,
            TitleId, "First", "mp4", null, handler: new FixedBodyHandler(new byte[1_500]), cancellationToken: TestContext.Current.CancellationToken);
        var secondScope = store.RegisterProfile(Server, Profile(Guid.NewGuid(), hasPin: false), null);

        await Assert.ThrowsAsync<InvalidOperationException>(() => store.DownloadAsync(secondScope,
            new Uri(Server, "/media/second.mp4"), uri => uri.Host == Server.Host, Guid.NewGuid(), "Second", "mp4", null,
            handler: new FixedBodyHandler(new byte[1_000]), cancellationToken: TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task ExpirationIsPersistedAndCleanupRemovesOnlyExpiredReferencedArchive()
    {
        var clock = new TestTimeProvider(new DateTimeOffset(2026, 8, 1, 0, 0, 0, TimeSpan.Zero));
        using var store = new OfflineMediaStore(_root, new TestKeyProtector(), expirationDays: 30, timeProvider: clock);
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);
        var item = await store.DownloadAsync(scope, new Uri(Server, "/media/video.mp4"), uri => uri.Host == Server.Host,
            TitleId, "Movie", "mp4", null, handler: new FixedBodyHandler(new byte[1_024]), cancellationToken: TestContext.Current.CancellationToken);
        var orphan = Path.Combine(_root, scope, $"{Guid.NewGuid():N}.rvn");
        await File.WriteAllBytesAsync(orphan, new byte[128], TestContext.Current.CancellationToken);
        Assert.Equal(clock.GetUtcNow().AddDays(30), item.ExpiresAt);

        clock.Advance(TimeSpan.FromDays(31));
        Assert.Equal(1, store.CleanupExpired());
        Assert.Empty(store.Items(scope));
        Assert.True(File.Exists(orphan));
    }

    [Fact]
    public async Task ZeroExpirationNeverExpires()
    {
        var clock = new TestTimeProvider(DateTimeOffset.UtcNow);
        using var store = new OfflineMediaStore(_root, new TestKeyProtector(), expirationDays: 0, timeProvider: clock);
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);
        var item = await store.DownloadAsync(scope, new Uri(Server, "/media/video.mp4"), uri => uri.Host == Server.Host,
            TitleId, "Movie", "mp4", null, handler: new FixedBodyHandler(new byte[1_024]), cancellationToken: TestContext.Current.CancellationToken);
        clock.Advance(TimeSpan.FromDays(365));

        Assert.Null(item.ExpiresAt);
        Assert.Equal(0, store.CleanupExpired());
        Assert.Single(store.Items(scope));
    }

    [Fact]
    public void SecondStoreCannotOpenTheSameOfflineRoot()
    {
        using var firstStore = new OfflineMediaStore(_root, new TestKeyProtector(), maximumStoredBytes: 3000);
        firstStore.RegisterProfile(Server, Profile(hasPin: false), null);

        Assert.Throws<IOException>(() =>
        {
            using var _ = new OfflineMediaStore(_root, new TestKeyProtector(), maximumStoredBytes: 3000);
        });
    }


    [Fact]
    public async Task PlaybackServerServesAuthenticatedByteRanges()
    {
        var plaintext = RandomNumberGenerator.GetBytes(EncryptedMediaFormat.ChunkBytes + 120_000);
        using var store = new OfflineMediaStore(_root, new TestKeyProtector());
        var scope = store.RegisterProfile(Server, Profile(hasPin: false), null);
        var item = await store.DownloadAsync(
            scope,
            new Uri(Server, "/media/video.mp4"),
            uri => uri.Host == Server.Host,
            TitleId,
            "Movie",
            "mp4",
            null,
            handler: new FixedBodyHandler(plaintext),
            cancellationToken: TestContext.Current.CancellationToken);

        using var playback = store.StartPlayback(scope, item);
        using var http = new HttpClient(new SocketsHttpHandler { AllowAutoRedirect = false, UseProxy = false });
        const int start = EncryptedMediaFormat.ChunkBytes - 100;
        const int end = EncryptedMediaFormat.ChunkBytes + 100;
        using var request = new HttpRequestMessage(HttpMethod.Get, playback.PlaybackUri);
        request.Headers.Range = new System.Net.Http.Headers.RangeHeaderValue(start, end);
        using var response = await http.SendAsync(request, TestContext.Current.CancellationToken);

        Assert.Equal(HttpStatusCode.PartialContent, response.StatusCode);
        Assert.Equal($"bytes {start}-{end}/{plaintext.Length}", response.Content.Headers.ContentRange?.ToString());
        Assert.Equal(plaintext[start..(end + 1)], await response.Content.ReadAsByteArrayAsync(TestContext.Current.CancellationToken));

        using var head = new HttpRequestMessage(HttpMethod.Head, playback.PlaybackUri);
        using var headResponse = await http.SendAsync(head, TestContext.Current.CancellationToken);
        Assert.Equal(HttpStatusCode.OK, headResponse.StatusCode);
        Assert.Equal(plaintext.Length, headResponse.Content.Headers.ContentLength);
        Assert.Empty(await headResponse.Content.ReadAsByteArrayAsync(TestContext.Current.CancellationToken));
    }


    [Theory]
    [InlineData("bytes=0-99", 1000, 0, 99)]
    [InlineData("bytes=900-", 1000, 900, 999)]
    [InlineData("bytes=-100", 1000, 900, 999)]
    [InlineData("bytes=900-2000", 1000, 900, 999)]
    public void SingleRangesAreBounded(string value, long length, long expectedStart, long expectedEnd)
    {
        Assert.True(OfflinePlaybackServer.TryParseSingleRange(value, length, out var start, out var end));
        Assert.Equal(expectedStart, start);
        Assert.Equal(expectedEnd, end);
    }

    [Theory]
    [InlineData("items=0-1")]
    [InlineData("bytes=0-1,3-4")]
    [InlineData("bytes=1000-")]
    [InlineData("bytes=9-4")]
    public void InvalidRangesFailClosed(string value)
    {
        Assert.False(OfflinePlaybackServer.TryParseSingleRange(value, 1000, out _, out _));
    }

    private static Profile Profile(bool hasPin) => Profile(ProfileId, hasPin);

    private static Profile Profile(Guid id, bool hasPin) => new()
    {
        Id = id,
        Name = "Viewer",
        Description = null,
        CategoryId = Guid.Parse("44444444-4444-4444-8444-444444444444"),
        Category = new CategoryRef { Id = Guid.Parse("44444444-4444-4444-8444-444444444444"), Name = "Default", Color = null, Icon = null },
        IsChild = false,
        HasPin = hasPin,
        CanManage = false,
        Enabled = true,
        AvailableFrom = null,
        AvailableUntil = null,
        AccessStartTime = null,
        AccessEndTime = null,
        AccessTimezone = "UTC",
        Accessible = true,
        Avatar = new ProfileAvatar { Kind = "preset", PresetId = "blue", Url = "/avatar" },
    };

    public void Dispose()
    {
        try { Directory.Delete(_root, recursive: true); }
        catch (DirectoryNotFoundException) { }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    private sealed class TestKeyProtector : IOfflineKeyProtector
    {
        public byte[] Protect(ReadOnlySpan<byte> plaintext) => plaintext.ToArray().Select(value => (byte)(value ^ 0xa5)).ToArray();
        public byte[] Unprotect(ReadOnlySpan<byte> ciphertext) => Protect(ciphertext);
    }


    private sealed class TestTimeProvider(DateTimeOffset now) : TimeProvider
    {
        private DateTimeOffset _now = now;
        public override DateTimeOffset GetUtcNow() => _now;
        public void Advance(TimeSpan duration) => _now += duration;
    }
    private sealed class FixedBodyHandler(byte[] body) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                RequestMessage = request,
                Content = new ByteArrayContent(body),
            });
    }

    private sealed class RedirectHandler : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(new HttpResponseMessage(HttpStatusCode.Redirect)
            {
                RequestMessage = request,
                Headers = { Location = new Uri("https://outside.example/media.mp4") },
            });
    }
}
