using System.Text;

namespace Rivune.App;

internal sealed class InstallationIdStore
{
    private const int MaximumIdBytes = 128;
    private readonly string _filePath;
    private readonly object _sync = new();
    private string? _installationId;

    public InstallationIdStore(string? filePath = null)
    {
        _filePath = Path.GetFullPath(filePath ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Rivune",
            "installation-id.v1.txt"));
    }

    public string LoadOrCreate()
    {
        lock (_sync)
        {
            if (_installationId is not null) return _installationId;
            var stored = Read();
            if (stored is not null) return _installationId = stored;

            var generated = Guid.NewGuid().ToString("D");
            var directory = Path.GetDirectoryName(_filePath)!;
            Directory.CreateDirectory(directory);
            var temporaryPath = Path.Combine(directory, $".{Path.GetFileName(_filePath)}.{Guid.NewGuid():N}.tmp");
            try
            {
                File.WriteAllText(temporaryPath, generated, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
                if (File.Exists(_filePath)) File.Replace(temporaryPath, _filePath, null);
                else File.Move(temporaryPath, _filePath);
            }
            finally
            {
                try { File.Delete(temporaryPath); }
                catch (IOException) { }
                catch (UnauthorizedAccessException) { }
            }
            return _installationId = generated;
        }
    }

    private string? Read()
    {
        try
        {
            var file = new FileInfo(_filePath);
            if (!file.Exists || file.Length is 0 or > MaximumIdBytes) return null;
            var value = File.ReadAllText(_filePath, Encoding.UTF8).Trim();
            return Guid.TryParseExact(value, "D", out var parsed) ? parsed.ToString("D") : null;
        }
        catch (FileNotFoundException) { return null; }
        catch (DirectoryNotFoundException) { return null; }
        catch (DecoderFallbackException) { return null; }
    }
}
