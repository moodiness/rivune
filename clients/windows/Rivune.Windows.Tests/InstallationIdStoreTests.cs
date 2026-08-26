using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class InstallationIdStoreTests : IDisposable
{
    private readonly string _directory = Path.Combine(Path.GetTempPath(), $"rivune-installation-store-{Guid.NewGuid():N}");
    private string FilePath => Path.Combine(_directory, "installation-id.txt");

    [Fact]
    public void GeneratedIdentityPersistsAcrossStoreInstances()
    {
        var first = new InstallationIdStore(FilePath).LoadOrCreate();
        var second = new InstallationIdStore(FilePath).LoadOrCreate();

        Assert.Equal(first, second);
        Assert.True(Guid.TryParseExact(first, "D", out _));
        Assert.Empty(Directory.EnumerateFiles(_directory, "*.tmp", SearchOption.TopDirectoryOnly));
    }

    [Fact]
    public void InvalidPersistedIdentityIsReplaced()
    {
        Directory.CreateDirectory(_directory);
        File.WriteAllText(FilePath, "not-an-installation-id");

        var identity = new InstallationIdStore(FilePath).LoadOrCreate();

        Assert.True(Guid.TryParseExact(identity, "D", out _));
        Assert.Equal(identity, File.ReadAllText(FilePath));
    }

    public void Dispose()
    {
        try { Directory.Delete(_directory, recursive: true); }
        catch (DirectoryNotFoundException) { }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }
}
