using Rivune.Windows;

namespace Rivune.App;

internal static class ServerAddressNormalizer
{
    public static string Normalize(string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        var trimmed = value.Trim();
        if (trimmed.Length == 0 || trimmed.Contains("://", StringComparison.Ordinal)) return trimmed;
        var trustedLocal = Uri.TryCreate($"http://{trimmed}", UriKind.Absolute, out var probe) &&
            TrustedLocalTransport.IsAllowedServerUri(probe);
        return $"{(trustedLocal ? Uri.UriSchemeHttp : Uri.UriSchemeHttps)}://{trimmed}";
    }
}
