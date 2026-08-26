using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class CoordinationPollingPolicyTests
{
    [Fact]
    public void IdleAndActiveIntervalsFollowLifecycleState()
    {
        var clock = new TestTimeProvider(DateTimeOffset.UtcNow);
        var policy = new CoordinationPollingPolicy(clock);

        Assert.Equal(TimeSpan.FromSeconds(15), policy.CommandInterval(false, false, false));
        Assert.Equal(TimeSpan.FromSeconds(2), policy.CommandInterval(true, false, false));
        Assert.Equal(TimeSpan.FromSeconds(2), policy.CommandInterval(false, true, false));
        Assert.Equal(TimeSpan.FromSeconds(2), policy.CommandInterval(false, false, true));
    }

    [Fact]
    public void RecentCommandsRemainFastThenReturnIdle()
    {
        var clock = new TestTimeProvider(DateTimeOffset.UtcNow);
        var policy = new CoordinationPollingPolicy(clock);
        policy.MarkCommandActivity();

        Assert.Equal(TimeSpan.FromSeconds(2), policy.CommandInterval(false, false, false));
        clock.Advance(TimeSpan.FromSeconds(31));
        Assert.Equal(TimeSpan.FromSeconds(15), policy.CommandInterval(false, false, false));
    }

    [Theory]
    [InlineData(true, false, true, true, true)]
    [InlineData(false, false, true, true, false)]
    [InlineData(true, true, true, true, false)]
    [InlineData(true, false, false, true, false)]
    [InlineData(true, false, true, false, false)]
    public void LifecycleSuspendsOutsideActiveForegroundProfile(
        bool foreground,
        bool closed,
        bool capability,
        bool profile,
        bool expected) =>
        Assert.Equal(expected, CoordinationPollingPolicy.ShouldRun(foreground, closed, capability, profile));

    private sealed class TestTimeProvider(DateTimeOffset now) : TimeProvider
    {
        private DateTimeOffset _now = now;
        public override DateTimeOffset GetUtcNow() => _now;
        internal void Advance(TimeSpan duration) => _now += duration;
    }
}
