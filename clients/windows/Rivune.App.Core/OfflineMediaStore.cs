using System.Buffers;
using System.Buffers.Binary;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Rivune.Windows;

namespace Rivune.App;

internal sealed record OfflineMediaItem
{
    public required Guid Id { get; init; }
    public required Guid TitleId { get; init; }
    public required string Title { get; init; }
    public required string FileName { get; init; }
    public required string Container { get; init; }
    public required long SizeBytes { get; init; }
    public required DateTimeOffset CreatedAt { get; init; }
    public string? PosterUrl { get; init; }
    public long PositionMilliseconds { get; init; }
    public long DurationMilliseconds { get; init; }
    public bool Completed { get; init; }
}

internal sealed record OfflineProfileGate
{
    public required string Name { get; init; }
    public required string Scope { get; init; }
    public required bool RequiresPin { get; init; }
}

internal interface IOfflineKeyProtector
{
    byte[] Protect(ReadOnlySpan<byte> plaintext);
    byte[] Unprotect(ReadOnlySpan<byte> ciphertext);
}

internal sealed class DpapiOfflineKeyProtector : IOfflineKeyProtector
{
    private static readonly byte[] OptionalEntropy = Encoding.UTF8.GetBytes("Rivune.Windows.OfflineMedia.v1");

    public byte[] Protect(ReadOnlySpan<byte> plaintext)
    {
        EnsureWindows();
        var copy = plaintext.ToArray();
        try { return ProtectedData.Protect(copy, OptionalEntropy, DataProtectionScope.CurrentUser); }
        finally { CryptographicOperations.ZeroMemory(copy); }
    }

    public byte[] Unprotect(ReadOnlySpan<byte> ciphertext)
    {
        EnsureWindows();
        var copy = ciphertext.ToArray();
        try { return ProtectedData.Unprotect(copy, OptionalEntropy, DataProtectionScope.CurrentUser); }
        finally { CryptographicOperations.ZeroMemory(copy); }
    }

    private static void EnsureWindows()
    {
        if (!OperatingSystem.IsWindows())
            throw new PlatformNotSupportedException("Offline media keys require Windows DPAPI.");
    }
}

internal sealed class OfflineMediaStore : IDisposable
{
    private const long MaximumStoredBytes = 20L * 1024 * 1024 * 1024;
    private const int MaximumManifestBytes = 4 * 1024 * 1024;
    private const int MaximumGateBytes = 256 * 1024;
    private const int MaximumKeyBytes = 16 * 1024;
    private const int PinIterations = 120_000;
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private readonly string _root;
    private readonly string _gatesPath;
    private readonly IOfflineKeyProtector _keyProtector;
    private readonly long _maximumStoredBytes;
    private readonly object _sync = new();
    private readonly SemaphoreSlim _downloadSlot = new(1, 1);
    private readonly HashSet<string> _activePartialPaths = new(StringComparer.OrdinalIgnoreCase);
    private string? _authorizedScope;
    private bool _disposed;
    public OfflineMediaStore(
        string? root = null,
        IOfflineKeyProtector? keyProtector = null,
        long maximumStoredBytes = MaximumStoredBytes)
    {
        if (maximumStoredBytes <= EncryptedMediaFormat.HeaderBytes + EncryptedMediaFormat.TagBytes)
            throw new ArgumentOutOfRangeException(nameof(maximumStoredBytes));
        _root = Path.GetFullPath(root ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Rivune",
            "offline-media"));
        _gatesPath = Path.Combine(_root, "profiles.v1.json");
        _keyProtector = keyProtector ?? new DpapiOfflineKeyProtector();
        _maximumStoredBytes = maximumStoredBytes;
        Directory.CreateDirectory(_root);
    }

    public static string ScopeFor(Uri serverOrigin, Guid profileId)
    {
        ArgumentNullException.ThrowIfNull(serverOrigin);
        if (!serverOrigin.IsAbsoluteUri) throw new ArgumentException("The server origin must be absolute.", nameof(serverOrigin));
        var issuer = new Uri(serverOrigin.GetComponents(UriComponents.SchemeAndServer, UriFormat.UriEscaped), UriKind.Absolute).AbsoluteUri;
        var material = Encoding.UTF8.GetBytes($"{issuer}\0{profileId:D}");
        return Convert.ToHexStringLower(SHA256.HashData(material));
    }

    public IReadOnlyList<OfflineProfileGate> Profiles()
    {
        lock (_sync)
        {
            ThrowIfDisposed();
            return ReadGates()
                .Where(gate => ReconcileManifest(gate.Scope).Count > 0)
                .Select(gate => new OfflineProfileGate { Name = gate.Name, Scope = gate.Scope, RequiresPin = gate.RequiresPin })
                .ToArray();
        }
    }

    public OfflineProfileGate? Profile(string scope)
    {
        lock (_sync)
        {
            ThrowIfDisposed();
            ValidateScope(scope);
            var gate = ReadGates().FirstOrDefault(value => StringComparer.Ordinal.Equals(value.Scope, scope));
            return gate is null ? null : new OfflineProfileGate { Name = gate.Name, Scope = gate.Scope, RequiresPin = gate.RequiresPin };
        }
    }

    public string RegisterProfile(Uri serverOrigin, Profile profile, string? pin)
    {
        ArgumentNullException.ThrowIfNull(profile);
        lock (_sync)
        {
            ThrowIfDisposed();
            var scope = ScopeFor(serverOrigin, profile.Id);
            var gates = ReadGates().ToList();
            byte[]? salt = null;
            byte[]? verifier = null;
            var requiresPin = profile.HasPin;
            if (requiresPin)
            {
                if (!ValidPin(pin)) throw new InvalidOperationException("Offline PIN must contain 4 to 8 digits.");
                salt = RandomNumberGenerator.GetBytes(16);
                verifier = DerivePin(pin!, salt);
            }
            gates.RemoveAll(value => StringComparer.Ordinal.Equals(value.Scope, scope));
            gates.Add(new StoredGate
            {
                Name = profile.Name.Length <= 120 ? profile.Name : profile.Name[..120],
                Scope = scope,
                RequiresPin = requiresPin,
                PinSalt = salt,
                PinVerifier = verifier,
            });
            WriteJsonAtomic(_gatesPath, gates, MaximumGateBytes);
            _authorizedScope = scope;
            Directory.CreateDirectory(ScopeDirectory(scope));
            return scope;
        }
    }

    public string? OpenRestoredProfile(Uri serverOrigin, Profile profile)
    {
        ArgumentNullException.ThrowIfNull(profile);
        lock (_sync)
        {
            ThrowIfDisposed();
            var scope = ScopeFor(serverOrigin, profile.Id);
            var gates = ReadGates().ToList();
            var existing = gates.FirstOrDefault(value => StringComparer.Ordinal.Equals(value.Scope, scope));
            gates.RemoveAll(value => StringComparer.Ordinal.Equals(value.Scope, scope));
            if (profile.HasPin)
            {
                gates.Add(new StoredGate
                {
                    Name = profile.Name.Length <= 120 ? profile.Name : profile.Name[..120],
                    Scope = scope,
                    RequiresPin = true,
                    PinSalt = existing?.RequiresPin == true ? existing.PinSalt : null,
                    PinVerifier = existing?.RequiresPin == true ? existing.PinVerifier : null,
                });
                WriteJsonAtomic(_gatesPath, gates, MaximumGateBytes);
                _authorizedScope = null;
                return null;
            }
            gates.Add(new StoredGate
            {
                Name = profile.Name.Length <= 120 ? profile.Name : profile.Name[..120],
                Scope = scope,
                RequiresPin = false,
            });
            WriteJsonAtomic(_gatesPath, gates, MaximumGateBytes);
            _authorizedScope = scope;
            Directory.CreateDirectory(ScopeDirectory(scope));
            return scope;
        }
    }

    public bool Unlock(string scope, string? pin)
    {
        lock (_sync)
        {
            ThrowIfDisposed();
            ValidateScope(scope);
            var gate = ReadGates().FirstOrDefault(value => StringComparer.Ordinal.Equals(value.Scope, scope));
            if (gate is null) return false;
            if (gate.RequiresPin)
            {
                if (!ValidPin(pin) || gate.PinSalt is not { Length: 16 } salt || gate.PinVerifier is not { Length: 32 } verifier)
                    return false;
                var candidate = DerivePin(pin!, salt);
                try
                {
                    if (!CryptographicOperations.FixedTimeEquals(candidate, verifier)) return false;
                }
                finally
                {
                    CryptographicOperations.ZeroMemory(candidate);
                }
            }
            _authorizedScope = scope;
            return true;
        }
    }

    public void Lock()
    {
        lock (_sync) _authorizedScope = null;
    }

    public IReadOnlyList<OfflineMediaItem> Items(string scope)
    {
        lock (_sync)
        {
            ThrowIfDisposed();
            RequireOpen(scope);
            return ReconcileManifest(scope).OrderByDescending(item => item.CreatedAt).ToArray();
        }
    }

    public async Task<OfflineMediaItem> DownloadAsync(
        string scope,
        Uri source,
        Func<Uri, bool> isAllowed,
        Guid titleId,
        string title,
        string? container,
        string? posterUrl,
        IProgress<long>? progress = null,
        HttpMessageHandler? handler = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(source);
        ArgumentNullException.ThrowIfNull(isAllowed);
        await _downloadSlot.WaitAsync(cancellationToken).ConfigureAwait(false);
        string? activePartial = null;
        try
        {
            string directory;
            long maximumDownloadBytes;
            byte[] key;
            lock (_sync)
            {
                ThrowIfDisposed();
                RequireOpen(scope);
                if (!isAllowed(source)) throw new InvalidOperationException("The offline source is outside the Rivune origin.");
                directory = ScopeDirectory(scope);
                _ = ReconcileManifest(scope);
                maximumDownloadBytes = MaximumPlaintextForStoredBudget(_maximumStoredBytes - StoredArchiveBytes(directory));
                if (maximumDownloadBytes <= 0) throw new InvalidOperationException("Offline storage quota reached.");
                key = OfflineKey(scope);
            }
            var id = Guid.NewGuid();
            var partial = Path.Combine(directory, $".{id:N}.partial");
            var destination = Path.Combine(directory, $"{id:N}.rvn");
            activePartial = partial;
            lock (_sync) _activePartialPaths.Add(partial);
            await using var writer = new EncryptedMediaWriter(partial, key, maximumDownloadBytes);
            CryptographicOperations.ZeroMemory(key);
            try
            {
                using var http = new HttpClient(handler ?? new SocketsHttpHandler
                {
                    AllowAutoRedirect = false,
                    AutomaticDecompression = DecompressionMethods.None,
                    UseCookies = false,
                }, disposeHandler: true) { Timeout = Timeout.InfiniteTimeSpan };
                using var request = new HttpRequestMessage(HttpMethod.Get, source);
                using var response = await http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken).ConfigureAwait(false);
                var finalUri = response.RequestMessage?.RequestUri ?? source;
                if (!isAllowed(finalUri) || (int)response.StatusCode is >= 300 and <= 399 || !response.IsSuccessStatusCode)
                    throw new InvalidOperationException("The offline source could not be downloaded without leaving the Rivune origin.");
                if (response.Content.Headers.ContentLength is long announced && announced > maximumDownloadBytes)
                    throw new InvalidOperationException("Offline storage quota reached.");
                await using var input = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
                var buffer = ArrayPool<byte>.Shared.Rent(256 * 1024);
                try
                {
                    long total = 0;
                    while (true)
                    {
                        var count = await input.ReadAsync(buffer.AsMemory(0, buffer.Length), cancellationToken).ConfigureAwait(false);
                        if (count == 0) break;
                        total = checked(total + count);
                        if (total > maximumDownloadBytes) throw new InvalidOperationException("Offline storage quota reached.");
                        await writer.AppendAsync(buffer.AsMemory(0, count), cancellationToken).ConfigureAwait(false);
                        progress?.Report(total);
                    }
                }
                finally
                {
                    ArrayPool<byte>.Shared.Return(buffer, clearArray: true);
                }

                var size = await writer.FinishAsync(cancellationToken).ConfigureAwait(false);
                var item = new OfflineMediaItem
                {
                    Id = id,
                    TitleId = titleId,
                    Title = title.Length <= 240 ? title : title[..240],
                    FileName = Path.GetFileName(destination),
                    Container = NormalizeContainer(container),
                    SizeBytes = size,
                    CreatedAt = DateTimeOffset.UtcNow,
                    PosterUrl = posterUrl,
                };
                lock (_sync)
                {
                    RequireOpen(scope);
                    var items = ReadManifest(scope).Where(value => value.Id != item.Id).Append(item).ToArray();
                    File.Move(partial, destination);
                    try { WriteJsonAtomic(ManifestPath(scope), items, MaximumManifestBytes); }
                    catch { TryDelete(destination); throw; }
                }
                return item;
            }
            catch
            {
                await writer.CancelAsync().ConfigureAwait(false);
                TryDelete(partial);
                TryDelete(destination);
                throw;
            }
        }
        finally
        {
            if (activePartial is not null)
            {
                TryDelete(activePartial);
                lock (_sync) _activePartialPaths.Remove(activePartial);
            }
            _downloadSlot.Release();
        }
    }

    public void Remove(string scope, OfflineMediaItem item)
    {
        ArgumentNullException.ThrowIfNull(item);
        lock (_sync)
        {
            ThrowIfDisposed();
            RequireOpen(scope);
            var items = ReadManifest(scope);
            var stored = items.FirstOrDefault(value => value.Id == item.Id && StringComparer.Ordinal.Equals(value.FileName, item.FileName))
                ?? throw new InvalidOperationException("Offline media belongs to another profile.");
            var path = ArchivePath(scope, stored);
            File.Delete(path);
            WriteJsonAtomic(ManifestPath(scope), items.Where(value => value.Id != item.Id).ToArray(), MaximumManifestBytes);
        }
    }

    public OfflineMediaItem UpdateProgress(string scope, Guid id, long positionMilliseconds, long durationMilliseconds, bool completed)
    {
        lock (_sync)
        {
            ThrowIfDisposed();
            RequireOpen(scope);
            var items = ReadManifest(scope);
            var current = items.FirstOrDefault(value => value.Id == id)
                ?? throw new InvalidOperationException("Offline media does not exist in this profile.");
            var updated = current with
            {
                PositionMilliseconds = Math.Max(0, positionMilliseconds),
                DurationMilliseconds = Math.Max(0, durationMilliseconds),
                Completed = completed,
            };
            WriteJsonAtomic(ManifestPath(scope), items.Select(value => value.Id == id ? updated : value).ToArray(), MaximumManifestBytes);
            return updated;
        }
    }

    public OfflinePlaybackServer StartPlayback(string scope, OfflineMediaItem item)
    {
        lock (_sync)
        {
            ThrowIfDisposed();
            RequireOpen(scope);
            var stored = ReadManifest(scope).FirstOrDefault(value => value.Id == item.Id && StringComparer.Ordinal.Equals(value.FileName, item.FileName))
                ?? throw new InvalidOperationException("Offline media belongs to another profile.");
            var key = OfflineKey(scope);
            try { return new OfflinePlaybackServer(ArchivePath(scope, stored), key, stored.Container); }
            finally { CryptographicOperations.ZeroMemory(key); }
        }
    }

    private IReadOnlyList<OfflineMediaItem> ReconcileManifest(string scope)
    {
        ValidateScope(scope);
        var directory = ScopeDirectory(scope);
        Directory.CreateDirectory(directory);
        var manifest = ReadManifest(scope);
        if (manifest.Any(item => !ValidArchiveMetadata(item)) ||
            manifest.Select(item => item.Id).Distinct().Count() != manifest.Count ||
            manifest.Select(item => item.FileName).Distinct(StringComparer.OrdinalIgnoreCase).Count() != manifest.Count)
            throw new InvalidDataException("Offline manifest contains invalid or duplicate entries.");
        var reconciled = manifest.Where(item => ArchiveExists(item, directory)).ToArray();
        if (!manifest.SequenceEqual(reconciled)) WriteJsonAtomic(ManifestPath(scope), reconciled, MaximumManifestBytes);
        var referenced = reconciled.Select(item => item.FileName).ToHashSet(StringComparer.OrdinalIgnoreCase);
        foreach (var path in Directory.EnumerateFiles(directory))
        {
            if (path.EndsWith(".rvn", StringComparison.OrdinalIgnoreCase) && !referenced.Contains(Path.GetFileName(path))) TryDelete(path);
            else if (path.EndsWith(".partial", StringComparison.OrdinalIgnoreCase) && !_activePartialPaths.Contains(path)) TryDelete(path);
        }
        return reconciled;
    }

    private static bool ValidArchiveMetadata(OfflineMediaItem item) =>
        item.Id != Guid.Empty && item.TitleId != Guid.Empty && !string.IsNullOrWhiteSpace(item.Title) && item.Title.Length <= 240 &&
        item.SizeBytes > 0 && item.PositionMilliseconds >= 0 && item.DurationMilliseconds >= 0 && SafeFileName(item.FileName) &&
        StringComparer.OrdinalIgnoreCase.Equals(item.FileName, $"{item.Id:N}.rvn");

    private static bool ArchiveExists(OfflineMediaItem item, string directory)
    {
        var path = Path.Combine(directory, item.FileName);
        if (!File.Exists(path)) return false;
        if (new FileInfo(path).Length <= EncryptedMediaFormat.HeaderBytes)
            throw new InvalidDataException("Encrypted offline media is incomplete.");
        return true;
    }

    private IReadOnlyList<OfflineMediaItem> ReadManifest(string scope)
    {
        var directory = ScopeDirectory(scope);
        var allowMissing = !Directory.EnumerateFiles(directory, "*.rvn").Any();
        return ReadJsonRequired<IReadOnlyList<OfflineMediaItem>>(
            ManifestPath(scope), MaximumManifestBytes, [], allowMissing);
    }

    private IReadOnlyList<StoredGate> ReadGates()
    {
        var gates = ReadJsonRequired<IReadOnlyList<StoredGate>>(
            _gatesPath, MaximumGateBytes, [], allowMissing: !Directory.EnumerateDirectories(_root).Any());
        if (gates.Any(gate => !ValidScope(gate.Scope) || string.IsNullOrWhiteSpace(gate.Name) || gate.Name.Length > 120 ||
                gate.RequiresPin && !((gate.PinSalt is null && gate.PinVerifier is null) ||
                    gate.PinSalt is { Length: 16 } && gate.PinVerifier is { Length: 32 })) ||
            gates.Select(gate => gate.Scope).Distinct(StringComparer.Ordinal).Count() != gates.Count)
            throw new InvalidDataException("Offline profile metadata is invalid or duplicated.");
        return gates;
    }

    private T ReadJsonRequired<T>(string path, int maximumBytes, T missingValue, bool allowMissing)
    {
        try
        {
            using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 4096, FileOptions.SequentialScan);
            if (stream.Length > maximumBytes) throw new InvalidDataException("Offline metadata is too large.");
            return JsonSerializer.Deserialize<T>(stream, JsonOptions)
                ?? throw new InvalidDataException("Offline metadata is invalid.");
        }
        catch (Exception exception) when (allowMissing && exception is FileNotFoundException or DirectoryNotFoundException)
        {
            return missingValue;
        }
        catch (Exception exception) when (exception is FileNotFoundException or DirectoryNotFoundException)
        {
            throw new InvalidDataException("Offline metadata is missing.", exception);
        }
        catch (Exception exception) when (exception is JsonException or NotSupportedException)
        {
            throw new InvalidDataException("Offline metadata is invalid.", exception);
        }
    }

    private static void WriteJsonAtomic<T>(string path, T value, int maximumBytes)
    {
        var bytes = JsonSerializer.SerializeToUtf8Bytes(value, JsonOptions);
        if (bytes.Length > maximumBytes) throw new InvalidDataException("Offline metadata is too large.");
        var directory = Path.GetDirectoryName(path)!;
        Directory.CreateDirectory(directory);
        var temporary = Path.Combine(directory, $".{Path.GetFileName(path)}.{Guid.NewGuid():N}.tmp");
        try
        {
            using (var stream = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None, 4096, FileOptions.WriteThrough))
            {
                stream.Write(bytes);
                stream.Flush(flushToDisk: true);
            }
            if (File.Exists(path)) File.Replace(temporary, path, null);
            else File.Move(temporary, path);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(bytes);
            TryDelete(temporary);
        }
    }

    private byte[] OfflineKey(string scope)
    {
        RequireOpen(scope);
        var path = Path.Combine(ScopeDirectory(scope), "key.v1.dpapi");
        if (File.Exists(path))
        {
            var protectedKey = File.ReadAllBytes(path);
            if (protectedKey.Length == 0 || protectedKey.Length > MaximumKeyBytes) throw new InvalidDataException("Offline media key is invalid.");
            try
            {
                var key = _keyProtector.Unprotect(protectedKey);
                if (key.Length != 32)
                {
                    CryptographicOperations.ZeroMemory(key);
                    throw new CryptographicException("Offline media key has an invalid length.");
                }
                return key;
            }
            finally
            {
                CryptographicOperations.ZeroMemory(protectedKey);
            }
        }

        var generated = RandomNumberGenerator.GetBytes(32);
        var encrypted = _keyProtector.Protect(generated);
        try { WriteBytesAtomic(path, encrypted); }
        catch { CryptographicOperations.ZeroMemory(generated); throw; }
        finally { CryptographicOperations.ZeroMemory(encrypted); }
        return generated;
    }

    private static void WriteBytesAtomic(string path, byte[] bytes)
    {
        var directory = Path.GetDirectoryName(path)!;
        Directory.CreateDirectory(directory);
        var temporary = Path.Combine(directory, $".{Path.GetFileName(path)}.{Guid.NewGuid():N}.tmp");
        try
        {
            using (var stream = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None, 4096, FileOptions.WriteThrough))
            {
                stream.Write(bytes);
                stream.Flush(flushToDisk: true);
            }
            File.Move(temporary, path);
        }
        finally { TryDelete(temporary); }
    }

    private string ArchivePath(string scope, OfflineMediaItem item)
    {
        if (!SafeFileName(item.FileName)) throw new InvalidDataException("Offline media path is invalid.");
        return Path.Combine(ScopeDirectory(scope), item.FileName);
    }

    private string ScopeDirectory(string scope)
    {
        ValidateScope(scope);
        return Path.Combine(_root, scope);
    }

    private string ManifestPath(string scope) => Path.Combine(ScopeDirectory(scope), "manifest.v1.json");

    private void RequireOpen(string scope)
    {
        ValidateScope(scope);
        if (!StringComparer.Ordinal.Equals(_authorizedScope, scope))
            throw new InvalidOperationException("Offline profile access is locked.");
    }

    private static void ValidateScope(string scope)
    {
        if (!ValidScope(scope)) throw new ArgumentException("Invalid offline profile scope.", nameof(scope));
    }

    private static bool ValidScope(string? scope) =>
        scope is { Length: 64 } && scope.All(character => character is >= '0' and <= '9' or >= 'a' and <= 'f');

    private static bool SafeFileName(string name) =>
        !string.IsNullOrWhiteSpace(name) && StringComparer.Ordinal.Equals(name, Path.GetFileName(name));

    private static bool ValidPin(string? pin) => pin is { Length: >= 4 and <= 8 } && pin.All(char.IsDigit);

    private static byte[] DerivePin(string pin, byte[] salt) =>
        Rfc2898DeriveBytes.Pbkdf2(pin, salt, PinIterations, HashAlgorithmName.SHA256, 32);

    private static string NormalizeContainer(string? value)
    {
        var normalized = value?.Trim().ToLowerInvariant();
        return normalized is "mp4" or "m4v" or "mpegts" or "ts" ? normalized : "mp4";
    }

    private static long StoredArchiveBytes(string directory)
    {
        long total = 0;
        foreach (var path in Directory.EnumerateFiles(directory))
        {
            if (path.EndsWith(".rvn", StringComparison.OrdinalIgnoreCase) ||
                path.EndsWith(".partial", StringComparison.OrdinalIgnoreCase))
                total = checked(total + new FileInfo(path).Length);
        }
        return total;
    }

    private static long MaximumPlaintextForStoredBudget(long budget)
    {
        if (budget <= EncryptedMediaFormat.HeaderBytes + EncryptedMediaFormat.TagBytes) return 0;
        var plaintext = budget - EncryptedMediaFormat.HeaderBytes;
        while (plaintext > 0)
        {
            var chunks = (plaintext + EncryptedMediaFormat.ChunkBytes - 1) / EncryptedMediaFormat.ChunkBytes;
            var adjusted = budget - EncryptedMediaFormat.HeaderBytes - chunks * EncryptedMediaFormat.TagBytes;
            if (adjusted >= plaintext) return plaintext;
            plaintext = Math.Max(0, adjusted);
        }
        return 0;
    }

    private static void TryDelete(string path)
    {
        try { File.Delete(path); }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    private void ThrowIfDisposed() => ObjectDisposedException.ThrowIf(_disposed, this);

    public void Dispose()
    {
        lock (_sync)
        {
            if (_disposed) return;
            _disposed = true;
            _authorizedScope = null;
        }
    }

    private sealed record StoredGate
    {
        public required string Name { get; init; }
        public required string Scope { get; init; }
        public required bool RequiresPin { get; init; }
        public byte[]? PinSalt { get; init; }
        public byte[]? PinVerifier { get; init; }
    }
}

internal static class EncryptedMediaFormat
{
    public static ReadOnlySpan<byte> Magic => "RVN2"u8;
    public const int HeaderBytes = 48;
    public const int ChunkBytes = 1024 * 1024;
    public const int TagBytes = 16;

    public static void Nonce(ReadOnlySpan<byte> prefix, uint index, Span<byte> destination)
    {
        prefix.CopyTo(destination);
        BinaryPrimitives.WriteUInt32BigEndian(destination[8..], index);
    }

    public static void ChunkAad(uint index, Span<byte> destination)
    {
        Magic.CopyTo(destination);
        BinaryPrimitives.WriteUInt32BigEndian(destination[4..], index);
    }
}

internal sealed class EncryptedMediaWriter : IAsyncDisposable
{
    private readonly FileStream _stream;
    private readonly byte[] _key;
    private readonly byte[] _noncePrefix = RandomNumberGenerator.GetBytes(8);
    private readonly byte[] _buffer = new byte[EncryptedMediaFormat.ChunkBytes];
    private readonly long _maximumBytes;
    private int _buffered;
    private uint _chunkIndex;
    private long _plaintextBytes;
    private bool _closed;

    public EncryptedMediaWriter(string path, ReadOnlySpan<byte> key, long maximumBytes)
    {
        if (key.Length != 32 || maximumBytes <= 0) throw new ArgumentException("Invalid encrypted media writer configuration.");
        _key = key.ToArray();
        _maximumBytes = maximumBytes;
        _stream = new FileStream(path, FileMode.CreateNew, FileAccess.ReadWrite, FileShare.None, 256 * 1024, FileOptions.Asynchronous | FileOptions.SequentialScan);
        _stream.Write(new byte[EncryptedMediaFormat.HeaderBytes]);
    }

    public async ValueTask AppendAsync(ReadOnlyMemory<byte> bytes, CancellationToken cancellationToken)
    {
        if (_closed) throw new ObjectDisposedException(nameof(EncryptedMediaWriter));
        if (bytes.Length > _maximumBytes - _plaintextBytes) throw new InvalidOperationException("Offline storage quota reached.");
        var offset = 0;
        while (offset < bytes.Length)
        {
            var copied = Math.Min(bytes.Length - offset, _buffer.Length - _buffered);
            bytes.Slice(offset, copied).CopyTo(_buffer.AsMemory(_buffered));
            _buffered += copied;
            offset += copied;
            _plaintextBytes += copied;
            if (_buffered == _buffer.Length) await FlushChunkAsync(cancellationToken).ConfigureAwait(false);
        }
    }

    public async Task<long> FinishAsync(CancellationToken cancellationToken)
    {
        if (_closed || _plaintextBytes <= 0) throw new InvalidDataException("Offline media is empty.");
        if (_buffered > 0) await FlushChunkAsync(cancellationToken).ConfigureAwait(false);
        var header = new byte[EncryptedMediaFormat.HeaderBytes];
        EncryptedMediaFormat.Magic.CopyTo(header);
        header[4] = 1;
        BinaryPrimitives.WriteInt32BigEndian(header.AsSpan(8), EncryptedMediaFormat.ChunkBytes);
        BinaryPrimitives.WriteInt64BigEndian(header.AsSpan(12), _plaintextBytes);
        _noncePrefix.CopyTo(header, 20);
        Span<byte> nonce = stackalloc byte[12];
        EncryptedMediaFormat.Nonce(_noncePrefix, uint.MaxValue, nonce);
        using (var aes = new AesGcm(_key, EncryptedMediaFormat.TagBytes))
            aes.Encrypt(nonce, ReadOnlySpan<byte>.Empty, Span<byte>.Empty, header.AsSpan(28, EncryptedMediaFormat.TagBytes), header.AsSpan(0, 28));
        _stream.Position = 0;
        await _stream.WriteAsync(header, cancellationToken).ConfigureAwait(false);
        await _stream.FlushAsync(cancellationToken).ConfigureAwait(false);
        _stream.Flush(flushToDisk: true);
        await CloseAsync().ConfigureAwait(false);
        return _plaintextBytes;
    }

    public async ValueTask CancelAsync() => await CloseAsync().ConfigureAwait(false);

    private async Task FlushChunkAsync(CancellationToken cancellationToken)
    {
        var encrypted = ArrayPool<byte>.Shared.Rent(_buffered);
        try
        {
            var nonce = new byte[12];
            var aad = new byte[8];
            var tag = new byte[EncryptedMediaFormat.TagBytes];
            EncryptedMediaFormat.Nonce(_noncePrefix, _chunkIndex, nonce);
            EncryptedMediaFormat.ChunkAad(_chunkIndex, aad);
            using (var aes = new AesGcm(_key, EncryptedMediaFormat.TagBytes))
                aes.Encrypt(nonce, _buffer.AsSpan(0, _buffered), encrypted.AsSpan(0, _buffered), tag, aad);
            await _stream.WriteAsync(encrypted.AsMemory(0, _buffered), cancellationToken).ConfigureAwait(false);
            await _stream.WriteAsync(tag, cancellationToken).ConfigureAwait(false);
            CryptographicOperations.ZeroMemory(_buffer.AsSpan(0, _buffered));
            _buffered = 0;
            _chunkIndex = checked(_chunkIndex + 1);
        }
        finally { ArrayPool<byte>.Shared.Return(encrypted, clearArray: true); }
    }

    private async ValueTask CloseAsync()
    {
        if (_closed) return;
        _closed = true;
        CryptographicOperations.ZeroMemory(_key);
        CryptographicOperations.ZeroMemory(_buffer);
        await _stream.DisposeAsync().ConfigureAwait(false);
    }

    public ValueTask DisposeAsync() => CloseAsync();
}

internal sealed class EncryptedMediaReader : IDisposable
{
    private readonly FileStream _stream;
    private readonly byte[] _key;
    private readonly byte[] _noncePrefix;
    private readonly object _sync = new();
    private byte[]? _cachedChunk;
    private uint _cachedIndex = uint.MaxValue;
    private bool _disposed;

    public EncryptedMediaReader(string path, ReadOnlySpan<byte> key)
    {
        if (key.Length != 32) throw new ArgumentException("Invalid offline media key.", nameof(key));
        _key = key.ToArray();
        _stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 256 * 1024, FileOptions.RandomAccess);
        try
        {
            var header = new byte[EncryptedMediaFormat.HeaderBytes];
            _stream.ReadExactly(header);
            if (!header.AsSpan(0, 4).SequenceEqual(EncryptedMediaFormat.Magic) || header[4] != 1 ||
                BinaryPrimitives.ReadInt32BigEndian(header.AsSpan(8)) != EncryptedMediaFormat.ChunkBytes)
                throw new InvalidDataException("The encrypted offline file is invalid.");
            PlaintextLength = BinaryPrimitives.ReadInt64BigEndian(header.AsSpan(12));
            if (PlaintextLength <= 0) throw new InvalidDataException("The encrypted offline file is empty.");
            _noncePrefix = header.AsSpan(20, 8).ToArray();
            Span<byte> nonce = stackalloc byte[12];
            EncryptedMediaFormat.Nonce(_noncePrefix, uint.MaxValue, nonce);
            using (var aes = new AesGcm(_key, EncryptedMediaFormat.TagBytes))
                aes.Decrypt(nonce, ReadOnlySpan<byte>.Empty, header.AsSpan(28, EncryptedMediaFormat.TagBytes), Span<byte>.Empty, header.AsSpan(0, 28));
            var chunks = (PlaintextLength + EncryptedMediaFormat.ChunkBytes - 1) / EncryptedMediaFormat.ChunkBytes;
            var expected = checked(EncryptedMediaFormat.HeaderBytes + PlaintextLength + chunks * EncryptedMediaFormat.TagBytes);
            if (_stream.Length != expected) throw new InvalidDataException("The encrypted offline file is incomplete.");
        }
        catch
        {
            _stream.Dispose();
            CryptographicOperations.ZeroMemory(_key);
            throw;
        }
    }

    public long PlaintextLength { get; }

    public int Read(long position, Span<byte> destination)
    {
        lock (_sync)
        {
            ObjectDisposedException.ThrowIf(_disposed, this);
            if (position < 0) throw new ArgumentOutOfRangeException(nameof(position));
            if (position >= PlaintextLength || destination.IsEmpty) return 0;
            var wanted = (int)Math.Min(destination.Length, PlaintextLength - position);
            var written = 0;
            while (written < wanted)
            {
                var index = checked((uint)(position / EncryptedMediaFormat.ChunkBytes));
                var chunk = Chunk(index);
                var inChunk = (int)(position % EncryptedMediaFormat.ChunkBytes);
                var count = Math.Min(wanted - written, chunk.Length - inChunk);
                chunk.AsSpan(inChunk, count).CopyTo(destination[written..]);
                written += count;
                position += count;
            }
            return written;
        }
    }

    private byte[] Chunk(uint index)
    {
        if (_cachedIndex == index && _cachedChunk is not null) return _cachedChunk;
        if (_cachedChunk is not null) CryptographicOperations.ZeroMemory(_cachedChunk);
        var chunkStart = checked((long)index * EncryptedMediaFormat.ChunkBytes);
        var plainLength = (int)Math.Min(EncryptedMediaFormat.ChunkBytes, PlaintextLength - chunkStart);
        var encrypted = ArrayPool<byte>.Shared.Rent(plainLength);
        var tag = new byte[EncryptedMediaFormat.TagBytes];
        try
        {
            var storedOffset = checked(EncryptedMediaFormat.HeaderBytes + (long)index * (EncryptedMediaFormat.ChunkBytes + EncryptedMediaFormat.TagBytes));
            _stream.Position = storedOffset;
            _stream.ReadExactly(encrypted.AsSpan(0, plainLength));
            _stream.ReadExactly(tag);
            var plaintext = new byte[plainLength];
            Span<byte> nonce = stackalloc byte[12];
            Span<byte> aad = stackalloc byte[8];
            EncryptedMediaFormat.Nonce(_noncePrefix, index, nonce);
            EncryptedMediaFormat.ChunkAad(index, aad);
            using (var aes = new AesGcm(_key, EncryptedMediaFormat.TagBytes))
                aes.Decrypt(nonce, encrypted.AsSpan(0, plainLength), tag, plaintext, aad);
            _cachedChunk = plaintext;
            _cachedIndex = index;
            return plaintext;
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(encrypted, clearArray: true);
            CryptographicOperations.ZeroMemory(tag);
        }
    }
    public void Dispose()
    {
        lock (_sync)
        {
            if (_disposed) return;
            _disposed = true;
            _stream.Dispose();
            CryptographicOperations.ZeroMemory(_key);
            if (_cachedChunk is not null) CryptographicOperations.ZeroMemory(_cachedChunk);
        }
    }
}

internal sealed class OfflinePlaybackServer : IDisposable
{
    private const int MaximumRequestHeaderBytes = 16 * 1024;
    private readonly EncryptedMediaReader _reader;
    private readonly System.Net.Sockets.TcpListener _listener;
    private readonly CancellationTokenSource _stopping = new();
    private readonly SemaphoreSlim _connectionSlots = new(4, 4);
    private readonly Task _accepting;
    private readonly string _path;
    private readonly string _expectedHost;
    private readonly string _contentType;
    private readonly object _lifetimeSync = new();
    private readonly HashSet<Task> _connections = [];
    private readonly HashSet<System.Net.Sockets.TcpClient> _clients = [];
    private bool _disposed;

    public OfflinePlaybackServer(string archivePath, ReadOnlySpan<byte> key, string container)
    {
        _reader = new EncryptedMediaReader(archivePath, key);
        _path = $"/{Guid.NewGuid():N}/media";
        _listener = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        _listener.Start(4);
        var endpoint = (IPEndPoint)_listener.LocalEndpoint;
        _expectedHost = $"127.0.0.1:{endpoint.Port}";
        PlaybackUri = new Uri($"http://{_expectedHost}{_path}");
        _contentType = container is "mpegts" or "ts" ? "video/mp2t" : "video/mp4";
        _accepting = AcceptLoopAsync();
    }

    public Uri PlaybackUri { get; }

    private async Task AcceptLoopAsync()
    {
        while (!_stopping.IsCancellationRequested)
        {
            try { await _connectionSlots.WaitAsync(_stopping.Token).ConfigureAwait(false); }
            catch (OperationCanceledException) { return; }

            System.Net.Sockets.TcpClient client;
            try { client = await _listener.AcceptTcpClientAsync(_stopping.Token).ConfigureAwait(false); }
            catch (OperationCanceledException) { _connectionSlots.Release(); return; }
            catch (ObjectDisposedException) { _connectionSlots.Release(); return; }
            catch (System.Net.Sockets.SocketException) when (_stopping.IsCancellationRequested)
            {
                _connectionSlots.Release();
                return;
            }

            var handling = HandleAcceptedAsync(client);
            lock (_lifetimeSync)
            {
                _connections.Add(handling);
                _clients.Add(client);
            }
            _ = handling.ContinueWith(
                completed =>
                {
                    lock (_lifetimeSync)
                    {
                        _connections.Remove(completed);
                        _clients.Remove(client);
                    }
                },
                CancellationToken.None,
                TaskContinuationOptions.ExecuteSynchronously,
                TaskScheduler.Default);
        }
    }

    private async Task HandleAcceptedAsync(System.Net.Sockets.TcpClient client)
    {
        try { await HandleAsync(client, _stopping.Token).ConfigureAwait(false); }
        catch (Exception) { }
        finally
        {
            client.Dispose();
            _connectionSlots.Release();
        }
    }

    private async Task HandleAsync(System.Net.Sockets.TcpClient client, CancellationToken cancellationToken)
    {
        using var stream = client.GetStream();
        PlaybackRequest? request;
        using (var headerDeadline = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
        {
            headerDeadline.CancelAfter(TimeSpan.FromSeconds(10));
            request = await ReadRequestAsync(stream, headerDeadline.Token).ConfigureAwait(false);
        }
        if (request is null || request.Target != _path ||
            !StringComparer.OrdinalIgnoreCase.Equals(request.Host, _expectedHost) ||
            request.Method is not ("GET" or "HEAD"))
        {
            await WriteErrorAsync(stream, HttpStatusCode.NotFound, null, cancellationToken).ConfigureAwait(false);
            return;
        }

        var start = 0L;
        var end = _reader.PlaintextLength - 1;
        var partial = false;
        if (request.Range is not null)
        {
            if (!TryParseSingleRange(request.Range, _reader.PlaintextLength, out start, out end))
            {
                await WriteErrorAsync(stream, HttpStatusCode.RequestedRangeNotSatisfiable,
                    $"Content-Range: bytes */{_reader.PlaintextLength}\r\n", cancellationToken).ConfigureAwait(false);
                return;
            }
            partial = true;
        }

        var length = end - start + 1;
        var headers = new StringBuilder(256)
            .Append("HTTP/1.1 ").Append(partial ? "206 Partial Content" : "200 OK").Append("\r\n")
            .Append("Connection: close\r\n")
            .Append("Cache-Control: no-store\r\n")
            .Append("Accept-Ranges: bytes\r\n")
            .Append("Content-Type: ").Append(_contentType).Append("\r\n")
            .Append("Content-Length: ").Append(length).Append("\r\n");
        if (partial) headers.Append("Content-Range: bytes ").Append(start).Append('-').Append(end).Append('/').Append(_reader.PlaintextLength).Append("\r\n");
        headers.Append("\r\n");
        await stream.WriteAsync(Encoding.ASCII.GetBytes(headers.ToString()), cancellationToken).ConfigureAwait(false);
        if (request.Method == "HEAD") return;

        var buffer = ArrayPool<byte>.Shared.Rent(256 * 1024);
        try
        {
            var position = start;
            while (position <= end)
            {
                var count = _reader.Read(position, buffer.AsSpan(0, (int)Math.Min(buffer.Length, end - position + 1)));
                if (count == 0) break;
                await stream.WriteAsync(buffer.AsMemory(0, count), cancellationToken).ConfigureAwait(false);
                position += count;
            }
        }
        finally { ArrayPool<byte>.Shared.Return(buffer, clearArray: true); }
    }

    private static async Task<PlaybackRequest?> ReadRequestAsync(System.Net.Sockets.NetworkStream stream, CancellationToken cancellationToken)
    {
        var buffer = ArrayPool<byte>.Shared.Rent(MaximumRequestHeaderBytes);
        try
        {
            var total = 0;
            while (total < MaximumRequestHeaderBytes)
            {
                var read = await stream.ReadAsync(buffer.AsMemory(total, MaximumRequestHeaderBytes - total), cancellationToken).ConfigureAwait(false);
                if (read == 0) return null;
                total += read;
                var end = HeaderEnd(buffer.AsSpan(0, total));
                if (end < 0) continue;
                string[] lines;
                try { lines = new UTF8Encoding(false, true).GetString(buffer, 0, end).Split("\r\n", StringSplitOptions.None); }
                catch (DecoderFallbackException) { return null; }
                var start = lines[0].Split(' ', StringSplitOptions.RemoveEmptyEntries);
                if (start.Length != 3 || start[0] is not ("GET" or "HEAD") ||
                    !start[2].StartsWith("HTTP/1.", StringComparison.Ordinal) || start[1].Length > 512) return null;
                string? host = null;
                string? range = null;
                for (var index = 1; index < lines.Length; index++)
                {
                    var separator = lines[index].IndexOf(':');
                    if (separator <= 0) return null;
                    var name = lines[index][..separator].Trim();
                    var value = lines[index][(separator + 1)..].Trim();
                    if (name.Equals("Host", StringComparison.OrdinalIgnoreCase))
                    {
                        if (host is not null) return null;
                        host = value;
                    }
                    else if (name.Equals("Range", StringComparison.OrdinalIgnoreCase))
                    {
                        if (range is not null) return null;
                        range = value;
                    }
                }
                return new PlaybackRequest(start[0], start[1], host, range);
            }
            return null;
        }
        finally { ArrayPool<byte>.Shared.Return(buffer, clearArray: true); }
    }

    private static int HeaderEnd(ReadOnlySpan<byte> bytes)
    {
        for (var index = 3; index < bytes.Length; index++)
            if (bytes[index - 3] == '\r' && bytes[index - 2] == '\n' && bytes[index - 1] == '\r' && bytes[index] == '\n') return index - 3;
        return -1;
    }

    private static async Task WriteErrorAsync(System.Net.Sockets.NetworkStream stream, HttpStatusCode status, string? extraHeaders, CancellationToken cancellationToken)
    {
        var body = Encoding.ASCII.GetBytes($"{(int)status} {status}\n");
        var headers = Encoding.ASCII.GetBytes(
            $"HTTP/1.1 {(int)status} {status}\r\nConnection: close\r\nCache-Control: no-store\r\n{extraHeaders}Content-Type: text/plain\r\nContent-Length: {body.Length}\r\n\r\n");
        await stream.WriteAsync(headers, cancellationToken).ConfigureAwait(false);
        await stream.WriteAsync(body, cancellationToken).ConfigureAwait(false);
    }

    internal static bool TryParseSingleRange(string value, long length, out long start, out long end)
    {
        start = 0;
        end = length - 1;
        if (length <= 0 || !value.StartsWith("bytes=", StringComparison.OrdinalIgnoreCase)) return false;
        var specification = value[6..].Trim();
        if (specification.Contains(',')) return false;
        var separator = specification.IndexOf('-');
        if (separator < 0) return false;
        var first = specification[..separator].Trim();
        var last = specification[(separator + 1)..].Trim();
        if (first.Length == 0)
        {
            if (!long.TryParse(last, out var suffix) || suffix <= 0) return false;
            start = Math.Max(0, length - suffix);
            end = length - 1;
            return true;
        }
        if (!long.TryParse(first, out start) || start < 0 || start >= length) return false;
        if (last.Length == 0) { end = length - 1; return true; }
        if (!long.TryParse(last, out end) || end < start) return false;
        end = Math.Min(end, length - 1);
        return true;
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _stopping.Cancel();
        _listener.Stop();
        try { _accepting.GetAwaiter().GetResult(); }
        catch (OperationCanceledException) { }
        catch (System.Net.Sockets.SocketException) { }
        Task[] connections;
        System.Net.Sockets.TcpClient[] clients;
        lock (_lifetimeSync)
        {
            connections = _connections.ToArray();
            clients = _clients.ToArray();
        }
        foreach (var client in clients) client.Dispose();
        try { Task.WaitAll(connections); }
        catch (AggregateException) { }
        _reader.Dispose();
        _connectionSlots.Dispose();
        _stopping.Dispose();
    }

    private sealed record PlaybackRequest(string Method, string Target, string? Host, string? Range);
}
