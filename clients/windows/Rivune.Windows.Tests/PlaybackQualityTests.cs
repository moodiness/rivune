using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class PlaybackQualityTests
{
    [Theory]
    [InlineData(1, 0, 480, 2000)]
    [InlineData(1, 2, 480, 2000)]
    [InlineData(2, 1, 1080, 8000)]
    [InlineData(0, 2, 720, 5000)]
    public void SharedPresetTableReturnsExpectedCaps(int preset, int network, int height, int bitrate)
    {
        var limit = PlaybackQualityPolicy.Limit((Rivune.App.PlaybackQualityPreset)preset, (NetworkClass)network);
        Assert.Equal(height, limit.MaximumHeight);
        Assert.Equal(bitrate, limit.MaximumVideoBitrateKbps);
    }
    [Theory]
    [InlineData(0, 0)]
    [InlineData(0, 1)]
    [InlineData(3, 2)]
    public void UnlimitedPresetsHaveNoArtificialCaps(int preset, int network)
    {
        var limit = PlaybackQualityPolicy.Limit((Rivune.App.PlaybackQualityPreset)preset, (NetworkClass)network);
        Assert.Null(limit.MaximumHeight);
        Assert.Null(limit.MaximumVideoBitrateKbps);
    }

    [Theory]
    [InlineData(12000, 8000, 8000)]
    [InlineData(5000, 8000, 5000)]
    [InlineData(5000, null, 5000)]
    [InlineData(null, 8000, 8000)]
    public void EffectiveCapIsMinimumOfCapacityAndNetworkLimit(int? capacity, int? limit, int expected) =>
        Assert.Equal(expected, PlaybackQualityPolicy.Cap(capacity, limit));
}
