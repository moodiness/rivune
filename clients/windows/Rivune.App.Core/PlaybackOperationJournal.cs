using System.Text.Json;
using Rivune.Windows;

namespace Rivune.App;

internal sealed record PlaybackOperationJournalEntry
{
    public required Guid OperationId { get; init; }
    public required PlaybackOperationStatus Status { get; init; }
    public required PlaybackOperationCode Code { get; init; }
    public required DateTimeOffset RecordedAt { get; init; }
}

internal sealed class PlaybackOperationJournal
{
    private const int MaximumEntries = 256;
    private const int MaximumFileBytes = 128 * 1024;
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);
    private readonly string _path;
    private readonly object _sync = new();

    public PlaybackOperationJournal(string? path = null)
    {
        _path = Path.GetFullPath(path ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Rivune",
            "playback-operations.v22.json"));
    }

    public PlaybackOperationJournalEntry? Find(Guid operationId)
    {
        lock (_sync) return Read().FirstOrDefault(entry => entry.OperationId == operationId);
    }

    public void Record(Guid operationId, PlaybackOperationStatus status, PlaybackOperationCode code)
    {
        if (operationId == Guid.Empty) throw new ArgumentException("Operation ID cannot be empty.", nameof(operationId));
        lock (_sync)
        {
            var entries = Read().ToList();
            var existing = entries.FirstOrDefault(entry => entry.OperationId == operationId);
            if (existing is not null)
            {
                if (existing.Status != status || existing.Code != code)
                    throw new InvalidOperationException("A playback operation already has a different terminal result.");
                return;
            }
            entries.Add(new PlaybackOperationJournalEntry
            {
                OperationId = operationId,
                Status = status,
                Code = code,
                RecordedAt = DateTimeOffset.UtcNow,
            });
            Write(entries.OrderByDescending(entry => entry.RecordedAt).Take(MaximumEntries).ToArray());
        }
    }

    private IReadOnlyList<PlaybackOperationJournalEntry> Read()
    {
        try
        {
            using var stream = new FileStream(_path, FileMode.Open, FileAccess.Read, FileShare.Read, 4096, FileOptions.SequentialScan);
            if (stream.Length > MaximumFileBytes) return [];
            var entries = JsonSerializer.Deserialize<IReadOnlyList<PlaybackOperationJournalEntry>>(stream, JsonOptions);
            if (entries is null || entries.Count > MaximumEntries || entries.Any(entry => entry.OperationId == Guid.Empty) ||
                entries.Select(entry => entry.OperationId).Distinct().Count() != entries.Count) return [];
            return entries;
        }
        catch (Exception exception) when (exception is FileNotFoundException or DirectoryNotFoundException or JsonException or NotSupportedException)
        {
            return [];
        }
    }

    private void Write(IReadOnlyList<PlaybackOperationJournalEntry> entries)
    {
        var bytes = JsonSerializer.SerializeToUtf8Bytes(entries, JsonOptions);
        if (bytes.Length > MaximumFileBytes) throw new InvalidDataException("Playback operation journal is too large.");
        var directory = Path.GetDirectoryName(_path)!;
        Directory.CreateDirectory(directory);
        var temporary = Path.Combine(directory, $".{Path.GetFileName(_path)}.{Guid.NewGuid():N}.tmp");
        try
        {
            using (var stream = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None, 4096, FileOptions.WriteThrough))
            {
                stream.Write(bytes);
                stream.Flush(flushToDisk: true);
            }
            if (File.Exists(_path)) File.Replace(temporary, _path, null);
            else File.Move(temporary, _path);
        }
        finally
        {
            try { File.Delete(temporary); } catch (IOException) { } catch (UnauthorizedAccessException) { }
        }
    }
}
