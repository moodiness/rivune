using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Rivune.Windows;

public interface ICredentialStore
{
    ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default);
    ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default);
    ValueTask ClearAsync(CancellationToken cancellationToken = default);
}

public sealed record StoredCredentials
{
    public required string Issuer { get; init; }
    public required TokenPair Credentials { get; init; }
    public string? ProfileContext { get; init; }
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
    private static readonly byte[] OptionalEntropy = Encoding.UTF8.GetBytes("Rivune.Windows.Protocol18.Credentials");
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    private readonly string _issuer;
    private readonly string _filePath;
    private readonly string? _legacyFilePath;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private bool _disposed;

    public DpapiCredentialStore(Uri serverUrl, string? filePath = null)
    {
        ArgumentNullException.ThrowIfNull(serverUrl);
        if (!serverUrl.IsAbsoluteUri)
        {
            throw new ArgumentException("The credential issuer must be an absolute URL.", nameof(serverUrl));
        }

        _issuer = CredentialIssuer.Canonicalize(serverUrl);
        if (filePath is not null)
        {
            _filePath = Path.GetFullPath(filePath);
            return;
        }

        var applicationDirectory = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Rivune");
        _legacyFilePath = Path.Combine(applicationDirectory, "credentials.v16.dat");
        var issuerHash = Convert.ToHexStringLower(SHA256.HashData(Encoding.UTF8.GetBytes(_issuer)));
        _filePath = Path.Combine(
            applicationDirectory,
            "credentials",
            $"credentials.v18.{issuerHash}.dat");
    }

    public async ValueTask<StoredCredentials?> LoadAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        EnsureWindows();
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            DeleteLegacyCredentials();
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
                var credentials = JsonSerializer.Deserialize<StoredCredentials>(plaintext, JsonOptions)
                    ?? throw new CredentialStoreException("Stored Rivune credentials are empty.");
                if (!StringComparer.Ordinal.Equals(credentials.Issuer, _issuer))
                {
                    DeleteCredentialFile();
                    return null;
                }

                return credentials;
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

    public async ValueTask SaveAsync(StoredCredentials credentials, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(credentials);
        if (!StringComparer.Ordinal.Equals(credentials.Issuer, _issuer))
        {
            throw new CredentialStoreException("Credential issuer does not match this credential store.");
        }
        ThrowIfDisposed();
        EnsureWindows();
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            DeleteLegacyCredentials();
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
                DeleteLegacyCredentials();
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

    private void DeleteCredentialFile()
    {
        try
        {
            File.Delete(_filePath);
        }
        catch (IOException)
        {
        }
        catch (UnauthorizedAccessException)
        {
        }
    }

    private void DeleteLegacyCredentials()
    {
        if (_legacyFilePath is null)
        {
            return;
        }

        try
        {
            File.Delete(_legacyFilePath);
        }
        catch (IOException)
        {
        }
        catch (UnauthorizedAccessException)
        {
        }
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

internal static class CredentialIssuer
{
    public static string Canonicalize(Uri serverUrl)
    {
        var origin = serverUrl.GetComponents(UriComponents.SchemeAndServer, UriFormat.UriEscaped);
        return new Uri(origin, UriKind.Absolute).AbsoluteUri;
    }
}
