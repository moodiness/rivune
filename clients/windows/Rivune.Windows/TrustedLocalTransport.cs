using System.Net;
using System.Net.Sockets;

namespace Rivune.Windows;

/// <summary>Classifies cleartext server destinations allowed on trusted local networks.</summary>
public static class TrustedLocalTransport
{
    /// <summary>Returns whether an absolute URI is HTTPS or trusted-local HTTP.</summary>
    public static bool IsAllowedServerUri(Uri value)
    {
        ArgumentNullException.ThrowIfNull(value);
        return value.Scheme.Equals(Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase) ||
            value.Scheme.Equals(Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) && IsTrustedLocalHost(value.Host);
    }

    /// <summary>Returns whether a host is the localhost name or an allowed literal local address.</summary>
    public static bool IsTrustedLocalHost(string host)
    {
        ArgumentNullException.ThrowIfNull(host);
        if (host.Equals("localhost", StringComparison.OrdinalIgnoreCase)) return true;
        var literal = host.AsSpan();
        if (literal.Length >= 2 && literal[0] == '[' && literal[^1] == ']') literal = literal[1..^1];
        if (!IPAddress.TryParse(literal, out var address) || address.IsIPv4MappedToIPv6) return false;

        Span<byte> bytes = stackalloc byte[16];
        if (!address.TryWriteBytes(bytes, out var length)) return false;
        if (address.AddressFamily == AddressFamily.InterNetwork && length == 4)
        {
            return bytes[0] == 127 ||
                bytes[0] == 10 ||
                bytes[0] == 172 && bytes[1] is >= 16 and <= 31 ||
                bytes[0] == 192 && bytes[1] == 168;
        }

        if (address.AddressFamily != AddressFamily.InterNetworkV6 || length != 16 || address.ScopeId != 0) return false;
        if (address.Equals(IPAddress.IPv6Loopback)) return true;
        return (bytes[0] & 0xfe) == 0xfc;
    }
}
