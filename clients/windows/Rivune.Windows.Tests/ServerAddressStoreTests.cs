using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ServerAddressStoreTests : IDisposable
{
    private readonly string _directory = Path.Combine(Path.GetTempPath(), $"rivune-server-store-{Guid.NewGuid():N}");
    private string FilePath => Path.Combine(_directory, "server-address.txt");

    [Fact]
    public async Task MissingAddressReturnsNull()
    {
        using var store = new ServerAddressStore(FilePath);

        Assert.Null(await store.LoadAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task AddressRoundTripsAndOverwritesAtomically()
    {
        using var store = new ServerAddressStore(FilePath);
        await store.SaveAsync("http://localhost:8080", TestContext.Current.CancellationToken);
        await store.SaveAsync("https://media.example.com", TestContext.Current.CancellationToken);

        Assert.Equal("https://media.example.com", await store.LoadAsync(TestContext.Current.CancellationToken));
        Assert.Empty(Directory.EnumerateFiles(_directory, "*.tmp", SearchOption.TopDirectoryOnly));
    }

    [Fact]
    public async Task EmptyAddressReturnsNull()
    {
        Directory.CreateDirectory(_directory);
        await File.WriteAllTextAsync(FilePath, " \r\n ", TestContext.Current.CancellationToken);
        using var store = new ServerAddressStore(FilePath);

        Assert.Null(await store.LoadAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task OversizedAddressIsRejectedWithoutAllocation()
    {
        Directory.CreateDirectory(_directory);
        await using (var stream = File.Create(FilePath))
        {
            stream.SetLength(2049);
        }
        using var store = new ServerAddressStore(FilePath);

        await Assert.ThrowsAsync<InvalidDataException>(() => store.LoadAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task ClearRemovesSavedAddress()
    {
        using var store = new ServerAddressStore(FilePath);
        await store.SaveAsync("http://localhost:8080", TestContext.Current.CancellationToken);

        await store.ClearAsync(TestContext.Current.CancellationToken);

        Assert.Null(await store.LoadAsync(TestContext.Current.CancellationToken));
    }

    public void Dispose()
    {
        try { Directory.Delete(_directory, recursive: true); }
        catch (DirectoryNotFoundException) { }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }
}
