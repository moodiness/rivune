using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class PlaybackProgressPolicyTests
{
    [Theory]
    [InlineData(89, false)]
    [InlineData(90, true)]
    [InlineData(91, true)]
    public void CompletionUsesNinetyPercentThreshold(int positionSeconds, bool expected)
    {
        Assert.Equal(
            expected,
            PlaybackProgressPolicy.IsCompleted(positionSeconds, 100, explicitlyCompleted: false));
    }

    [Fact]
    public void ZeroDurationDoesNotImplyCompletion()
    {
        Assert.False(PlaybackProgressPolicy.IsCompleted(100, 0, explicitlyCompleted: false));
    }

    [Fact]
    public void ExplicitCompletionDoesNotRequireDuration()
    {
        Assert.True(PlaybackProgressPolicy.IsCompleted(0, 0, explicitlyCompleted: true));
    }

    [Theory]
    [InlineData(480, true, 0)]
    [InlineData(480, false, 480)]
    [InlineData(-1, false, 0)]
    public void StartSecondsRestartsCompletedProgressAndResumesIncompleteProgress(
        int positionSeconds,
        bool completed,
        int expected)
    {
        Assert.Equal(expected, PlaybackProgressPolicy.StartSeconds(positionSeconds, completed));
    }

    [Fact]
    public void ConflictKeepsLatestLocalPositionEvenWhenRewound()
    {
        var merged = PlaybackProgressPolicy.MergeConflict(
            new PlaybackProgressPolicy.Snapshot(120, 1_000, false),
            new PlaybackProgressPolicy.Snapshot(600, 1_000, false));

        Assert.Equal(120, merged.PositionSeconds);
    }

    [Fact]
    public void ConflictUsesLatestValidLocalDuration()
    {
        var merged = PlaybackProgressPolicy.MergeConflict(
            new PlaybackProgressPolicy.Snapshot(120, 900, false),
            new PlaybackProgressPolicy.Snapshot(600, 1_000, false));

        Assert.Equal(900, merged.DurationSeconds);
    }

    [Fact]
    public void ConflictFallsBackToRemoteDurationWhenLocalDurationIsInvalid()
    {
        var merged = PlaybackProgressPolicy.MergeConflict(
            new PlaybackProgressPolicy.Snapshot(120, 0, false),
            new PlaybackProgressPolicy.Snapshot(600, 1_000, false));

        Assert.Equal(1_000, merged.DurationSeconds);
    }

    [Theory]
    [InlineData(true, false)]
    [InlineData(false, true)]
    public void ConflictPreservesCompletionFromEitherSide(bool localCompleted, bool remoteCompleted)
    {
        var merged = PlaybackProgressPolicy.MergeConflict(
            new PlaybackProgressPolicy.Snapshot(120, 1_000, localCompleted),
            new PlaybackProgressPolicy.Snapshot(600, 1_000, remoteCompleted));

        Assert.True(merged.Completed);
    }
}
