namespace Rivune.App;

internal static class ServerAddressNormalizer
{
    public static string Normalize(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        var trimmed = value.Trim();
        if (trimmed.Length == 0 || trimmed.Contains("://", StringComparison.Ordinal)) return trimmed;
        var loopback = Uri.TryCreate($"http://{trimmed}", UriKind.Absolute, out var probe) && probe.IsLoopback;
        return $"{(loopback ? Uri.UriSchemeHttp : Uri.UriSchemeHttps)}://{trimmed}";
    }
}
