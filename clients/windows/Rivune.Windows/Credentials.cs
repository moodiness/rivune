using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.Windows;

public interface ICredentialStore
{
    ValueTask<TokenPair?> LoadAsync(CancellationToken cancellationToken = default);
    ValueTask SaveAsync(TokenPair credentials, CancellationToken cancellationToken = default);
    ValueTask ClearAsync(CancellationToken cancellationToken = default);
}

public sealed class CredentialStoreException : Exception
{
    public CredentialStoreException(string message, Exception? innerException = null)
        : base(message, innerException)
    {
    }
}

public sealed class DpapiCredentialStore : ICredentialStore, IDisposable
{
    private static readonly byte[] OptionalEntropy = Encoding.UTF8.GetBytes("Rivune.Windows.Protocol16.Credentials");
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private readonly string _filePath;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private bool _disposed;

    public DpapiCredentialStore(string? filePath = null)
    {
        _filePath = Path.GetFullPath(filePath ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Rivune",
            "credentials.v16.dat"));
    }

    public async ValueTask<TokenPair?> LoadAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        EnsureWindows();
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (!File.Exists(_filePath))
            {
                return null;
            }

            byte[] encrypted;
            try
            {
                encrypted = await File.ReadAllBytesAsync(_filePath, cancellationToken).ConfigureAwait(false);
            }
            catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
            {
                throw new CredentialStoreException("Unable to read Rivune credentials.", exception);
            }

            byte[] plaintext;
            try
            {
                plaintext = ProtectedData.Unprotect(encrypted, OptionalEntropy, DataProtectionScope.CurrentUser);
            }
            catch (Exception exception) when (exception is CryptographicException or PlatformNotSupportedException)
            {
                throw new CredentialStoreException("Unable to decrypt Rivune credentials for the current Windows user.", exception);
            }
            finally
            {
                CryptographicOperations.ZeroMemory(encrypted);
            }

            try
            {
                return JsonSerializer.Deserialize<TokenPair>(plaintext, JsonOptions)
                    ?? throw new CredentialStoreException("Stored Rivune credentials are empty.");
            }
            catch (JsonException exception)
            {
                throw new CredentialStoreException("Stored Rivune credentials are invalid.", exception);
            }
            finally
            {
                CryptographicOperations.ZeroMemory(plaintext);
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    public async ValueTask SaveAsync(TokenPair credentials, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(credentials);
        ThrowIfDisposed();
        EnsureWindows();
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            var directory = Path.GetDirectoryName(_filePath)!;
            Directory.CreateDirectory(directory);

            var plaintext = JsonSerializer.SerializeToUtf8Bytes(credentials, JsonOptions);
            byte[] encrypted;
            try
            {
                encrypted = ProtectedData.Protect(plaintext, OptionalEntropy, DataProtectionScope.CurrentUser);
            }
            catch (Exception exception) when (exception is CryptographicException or PlatformNotSupportedException)
            {
                throw new CredentialStoreException("Unable to encrypt Rivune credentials for the current Windows user.", exception);
            }
            finally
            {
                CryptographicOperations.ZeroMemory(plaintext);
            }

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
                    await stream.WriteAsync(encrypted, cancellationToken).ConfigureAwait(false);
                    await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
                }

                cancellationToken.ThrowIfCancellationRequested();
                if (File.Exists(_filePath))
                {
                    File.Replace(temporaryPath, _filePath, destinationBackupFileName: null);
                }
                else
                {
                    File.Move(temporaryPath, _filePath);
                }
            }
            catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
            {
                throw new CredentialStoreException("Unable to persist encrypted Rivune credentials.", exception);
            }
            finally
            {
                CryptographicOperations.ZeroMemory(encrypted);
                try
                {
                    File.Delete(temporaryPath);
                }
                catch (IOException)
                {
                }
                catch (UnauthorizedAccessException)
                {
                }
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    public async ValueTask ClearAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            try
            {
                File.Delete(_filePath);
            }
            catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
            {
                throw new CredentialStoreException("Unable to clear Rivune credentials.", exception);
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _gate.Dispose();
    }

    private static void EnsureWindows()
    {
        if (!OperatingSystem.IsWindows())
        {
            throw new PlatformNotSupportedException("DPAPI credential storage requires Windows.");
        }
    }

    private void ThrowIfDisposed() => ObjectDisposedException.ThrowIf(_disposed, this);
}
