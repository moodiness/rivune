namespace Rivune.App;

internal enum NetworkClass
{
    Local,
    RemoteWifi,
    Mobile,
}

internal enum PlaybackQualityPreset
{
    Automatic,
    Economy,
    Balanced,
    Maximum,
}

internal readonly record struct PlaybackQualityLimit(int? MaximumHeight, int? MaximumVideoBitrateKbps);

internal static class PlaybackQualityPolicy
{
    public static PlaybackQualityLimit Limit(PlaybackQualityPreset preset, NetworkClass networkClass) => preset switch
    {
        PlaybackQualityPreset.Economy => new(480, 2_000),
        PlaybackQualityPreset.Balanced => new(1080, 8_000),
        PlaybackQualityPreset.Automatic when networkClass == NetworkClass.Mobile => new(720, 5_000),
        PlaybackQualityPreset.Automatic => new(null, null),
        PlaybackQualityPreset.Maximum => new(null, null),
        _ => throw new ArgumentOutOfRangeException(nameof(preset)),
    };

    public static int? Cap(int? deviceCapacity, int? configuredLimit)
    {
        if (deviceCapacity is <= 0) deviceCapacity = null;
        if (configuredLimit is <= 0) configuredLimit = null;
        return (deviceCapacity, configuredLimit) switch
        {
            (int capacity, int limit) => Math.Min(capacity, limit),
            (int capacity, null) => capacity,
            (null, int limit) => limit,
            _ => null,
        };
    }
}
