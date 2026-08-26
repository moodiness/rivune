using Rivune.App;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class PlaybackOperationJournalTests : IDisposable
{
    private readonly string _directory = Path.Combine(Path.GetTempPath(), $"rivune-operation-journal-{Guid.NewGuid():N}");
    private string PathName => Path.Combine(_directory, "journal.json");

    [Fact]
    public void TerminalResultSurvivesRestartAndRetryDoesNotDuplicateAction()
    {
        var operationId = Guid.NewGuid();
        var first = new PlaybackOperationJournal(PathName);
        first.Record(operationId, PlaybackOperationStatus.Applied, PlaybackOperationCode.Applied);
        first.Record(operationId, PlaybackOperationStatus.Applied, PlaybackOperationCode.Applied);

        var restored = new PlaybackOperationJournal(PathName).Find(operationId);

        Assert.NotNull(restored);
        Assert.Equal(PlaybackOperationStatus.Applied, restored.Status);
        Assert.Equal(PlaybackOperationCode.Applied, restored.Code);
    }

    [Fact]
    public void ConflictingTerminalResultFailsClosed()
    {
        var operationId = Guid.NewGuid();
        var journal = new PlaybackOperationJournal(PathName);
        journal.Record(operationId, PlaybackOperationStatus.Applied, PlaybackOperationCode.Applied);

        Assert.Throws<InvalidOperationException>(() =>
            journal.Record(operationId, PlaybackOperationStatus.Failed, PlaybackOperationCode.ExecutionFailed));
    }

    [Fact]
    public void JournalIsBoundedToRecentOperations()
    {
        var journal = new PlaybackOperationJournal(PathName);
        var operations = Enumerable.Range(0, 300).Select(_ => Guid.NewGuid()).ToArray();
        foreach (var operation in operations)
            journal.Record(operation, PlaybackOperationStatus.Applied, PlaybackOperationCode.Applied);

        Assert.Null(new PlaybackOperationJournal(PathName).Find(operations[0]));
        Assert.NotNull(new PlaybackOperationJournal(PathName).Find(operations[^1]));
    }

    public void Dispose()
    {
        try { Directory.Delete(_directory, recursive: true); }
        catch (DirectoryNotFoundException) { }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }
}
