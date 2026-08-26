using System.Buffers.Binary;
using System.Net;
using System.Net.Sockets;
using System.Text;
using Rivune.Windows;

namespace Rivune.App;

internal sealed record DiscoveredRivuneServer(string ServiceName, string Name, Uri Address, string? Version)
{
    public bool UsesSecureTransport => Address.Scheme == Uri.UriSchemeHttps;
    public string SecurityLabel => UsesSecureTransport ? "Secure HTTPS" : "Trusted-LAN HTTP";
}

internal static class RivuneLanService
{
    public const string ServiceType = "_rivune._tcp.local";

    public static DiscoveredRivuneServer? Parse(string serviceName, IReadOnlyDictionary<string, string> attributes)
    {
        if (!attributes.TryGetValue("protocol", out var protocol) || protocol != RivuneProtocol.Version.ToString() ||
            !attributes.TryGetValue("url", out var rawAddress) || rawAddress.Length is 0 or > 255 ||
            !Uri.TryCreate(rawAddress.Trim(), UriKind.Absolute, out var supplied) ||
            !string.IsNullOrEmpty(supplied.UserInfo) ||
            supplied.AbsolutePath is not ("" or "/") ||
            !string.IsNullOrEmpty(supplied.Query) ||
            !string.IsNullOrEmpty(supplied.Fragment) ||
            !TrustedLocalTransport.IsAllowedServerUri(supplied))
        {
            return null;
        }

        var address = new Uri(supplied.GetLeftPart(UriPartial.Authority));
        var normalizedServiceName = serviceName.Trim();
        if (normalizedServiceName.Length == 0) normalizedServiceName = "Rivune";
        normalizedServiceName = normalizedServiceName[..Math.Min(120, normalizedServiceName.Length)];
        var name = attributes.TryGetValue("name", out var advertisedName) ? advertisedName.Trim() : string.Empty;
        if (name.Length == 0) name = normalizedServiceName;
        name = name[..Math.Min(120, name.Length)];
        var version = attributes.TryGetValue("version", out var advertisedVersion)
            ? advertisedVersion.Trim()
            : null;
        if (version?.Length == 0) version = null;
        if (version?.Length > 64) version = version[..64];
        return new DiscoveredRivuneServer(normalizedServiceName, name, address, version);
    }
}

internal sealed class LanServerDiscovery : IAsyncDisposable
{
    private static readonly IPEndPoint MulticastEndpoint = new(IPAddress.Parse("224.0.0.251"), 5353);
    private static readonly TimeSpan QueryInterval = TimeSpan.FromSeconds(15);
    private static readonly TimeSpan RecordLifetime = TimeSpan.FromSeconds(75);
    private readonly object sync = new();
    private readonly Dictionary<string, ServiceState> services = new(StringComparer.OrdinalIgnoreCase);
    private CancellationTokenSource? cancellation;
    private UdpClient? client;
    private Task? worker;

    public event EventHandler<IReadOnlyList<DiscoveredRivuneServer>>? ServersChanged;

    public void Start()
    {
        lock (sync)
        {
            var previous = worker;
            StopLocked();
            Observe(previous);
            cancellation = new CancellationTokenSource();
            try
            {
                client = new UdpClient(AddressFamily.InterNetwork);
                client.Client.SetSocketOption(SocketOptionLevel.Socket, SocketOptionName.ReuseAddress, true);
                client.Client.Bind(new IPEndPoint(IPAddress.Any, 5353));
                client.JoinMulticastGroup(MulticastEndpoint.Address);
                worker = RunAsync(client, cancellation.Token);
            }
            catch
            {
                StopLocked();
            }
        }
    }

    public void Stop()
    {
        lock (sync)
        {
            var previous = worker;
            StopLocked();
            Observe(previous);
        }
    }

    public async ValueTask DisposeAsync()
    {
        Task? pending;
        lock (sync)
        {
            pending = worker;
            StopLocked();
        }
        if (pending is null) return;
        try { await pending.ConfigureAwait(false); }
        catch (OperationCanceledException) { }
        catch (ObjectDisposedException) { }
        catch (SocketException) { }
    }

    private void StopLocked()
    {
        cancellation?.Cancel();
        client?.Dispose();
        cancellation?.Dispose();
        cancellation = null;
        client = null;
        worker = null;
        services.Clear();
    }

    private static void Observe(Task? task)
    {
        if (task is null) return;
        _ = task.ContinueWith(
            completed => _ = completed.Exception,
            CancellationToken.None,
            TaskContinuationOptions.OnlyOnFaulted | TaskContinuationOptions.ExecuteSynchronously,
            TaskScheduler.Default);
    }

    private async Task RunAsync(UdpClient udp, CancellationToken cancellationToken)
    {
        using var timer = new PeriodicTimer(QueryInterval);
        await SendQueryAsync(udp, cancellationToken).ConfigureAwait(false);
        var receive = udp.ReceiveAsync(cancellationToken).AsTask();
        var tick = timer.WaitForNextTickAsync(cancellationToken).AsTask();
        while (!cancellationToken.IsCancellationRequested)
        {
            var completed = await Task.WhenAny(receive, tick).ConfigureAwait(false);
            if (completed == receive)
            {
                var packet = await receive.ConfigureAwait(false);
                HandlePacket(packet.Buffer);
                receive = udp.ReceiveAsync(cancellationToken).AsTask();
                continue;
            }
            if (!await tick.ConfigureAwait(false)) return;
            ExpireRecords();
            await SendQueryAsync(udp, cancellationToken).ConfigureAwait(false);
            tick = timer.WaitForNextTickAsync(cancellationToken).AsTask();
        }
    }

    private static async Task SendQueryAsync(UdpClient udp, CancellationToken cancellationToken)
    {
        var query = DnsPacket.BuildPtrQuery(RivuneLanService.ServiceType);
        _ = await udp.SendAsync(query, MulticastEndpoint, cancellationToken).ConfigureAwait(false);
    }

    private void HandlePacket(byte[] packet)
    {
        IReadOnlyList<DnsRecord> records;
        try { records = DnsPacket.Parse(packet); }
        catch (InvalidDataException) { return; }

        var now = DateTimeOffset.UtcNow;
        lock (sync)
        {
            foreach (var record in records)
            {
                if (record.Type == DnsRecordType.Ptr &&
                    record.Name.Equals(RivuneLanService.ServiceType, StringComparison.OrdinalIgnoreCase) &&
                    record.Target is not null)
                {
                    if (record.Ttl == 0) services.Remove(record.Target);
                    else Get(record.Target).ExpiresAt = now.AddSeconds(Math.Clamp(record.Ttl, 1u, (uint)RecordLifetime.TotalSeconds));
                    continue;
                }
                if (record.Type != DnsRecordType.Txt || record.Attributes is null ||
                    !record.Name.EndsWith("." + RivuneLanService.ServiceType, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                if (record.Ttl == 0)
                {
                    services.Remove(record.Name);
                    continue;
                }
                var state = Get(record.Name);
                state.Attributes = record.Attributes;
                state.ExpiresAt = now.AddSeconds(Math.Clamp(record.Ttl, 1u, (uint)RecordLifetime.TotalSeconds));
            }
            PublishLocked();
        }
    }

    private ServiceState Get(string key)
    {
        if (!services.TryGetValue(key, out var state))
        {
            state = new ServiceState { ServiceName = DnsPacket.InstanceName(key) };
            services[key] = state;
        }
        return state;
    }

    private void ExpireRecords()
    {
        lock (sync)
        {
            var now = DateTimeOffset.UtcNow;
            foreach (var key in services.Where(pair => pair.Value.ExpiresAt <= now).Select(pair => pair.Key).ToArray())
                services.Remove(key);
            PublishLocked();
        }
    }

    private void PublishLocked()
    {
        var found = services.Values
            .Select(state => state.Attributes is null ? null : RivuneLanService.Parse(state.ServiceName, state.Attributes))
            .OfType<DiscoveredRivuneServer>()
            .DistinctBy(server => server.Address.AbsoluteUri, StringComparer.OrdinalIgnoreCase)
            .OrderBy(server => server.Name, StringComparer.CurrentCultureIgnoreCase)
            .ThenBy(server => server.Address.AbsoluteUri, StringComparer.Ordinal)
            .ToArray();
        ServersChanged?.Invoke(this, found);
    }

    private sealed class ServiceState
    {
        public required string ServiceName { get; init; }
        public IReadOnlyDictionary<string, string>? Attributes { get; set; }
        public DateTimeOffset ExpiresAt { get; set; } = DateTimeOffset.UtcNow.Add(RecordLifetime);
    }
}

internal enum DnsRecordType : ushort { Ptr = 12, Txt = 16 }

internal sealed record DnsRecord(
    string Name,
    DnsRecordType Type,
    uint Ttl,
    string? Target,
    IReadOnlyDictionary<string, string>? Attributes);

internal static class DnsPacket
{
    private const int HeaderLength = 12;

    public static byte[] BuildPtrQuery(string serviceType)
    {
        using var stream = new MemoryStream();
        stream.Write(new byte[] { 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0 });
        WriteName(stream, serviceType);
        stream.Write(new byte[] { 0, 12, 0, 1 });
        return stream.ToArray();
    }

    public static IReadOnlyList<DnsRecord> Parse(byte[] packet)
    {
        if (packet.Length < HeaderLength) throw new InvalidDataException("DNS packet is truncated");
        var questions = ReadUInt16(packet, 4);
        var recordCount = ReadUInt16(packet, 6) + ReadUInt16(packet, 8) + ReadUInt16(packet, 10);
        if (questions > packet.Length / 5 || recordCount > packet.Length / 11)
            throw new InvalidDataException("DNS record counts exceed the packet size");
        var offset = HeaderLength;
        for (var i = 0; i < questions; i++)
        {
            _ = ReadName(packet, ref offset);
            Require(packet, offset, 4);
            offset += 4;
        }
        var records = new List<DnsRecord>(recordCount);
        for (var i = 0; i < recordCount; i++)
        {
            var name = ReadName(packet, ref offset);
            Require(packet, offset, 10);
            var type = ReadUInt16(packet, offset);
            var ttl = ReadUInt32(packet, offset + 4);
            var length = ReadUInt16(packet, offset + 8);
            offset += 10;
            Require(packet, offset, length);
            var dataOffset = offset;
            offset += length;
            if (type == (ushort)DnsRecordType.Ptr)
            {
                var targetOffset = dataOffset;
                records.Add(new DnsRecord(name, DnsRecordType.Ptr, ttl, ReadName(packet, ref targetOffset), null));
            }
            else if (type == (ushort)DnsRecordType.Txt)
            {
                records.Add(new DnsRecord(name, DnsRecordType.Txt, ttl, null, ReadTxt(packet, dataOffset, length)));
            }
        }
        return records;
    }

    public static string InstanceName(string qualifiedName)
    {
        var suffix = "." + RivuneLanService.ServiceType;
        return qualifiedName.EndsWith(suffix, StringComparison.OrdinalIgnoreCase)
            ? qualifiedName[..^suffix.Length].Replace("\\.", ".", StringComparison.Ordinal)
            : qualifiedName;
    }

    private static IReadOnlyDictionary<string, string> ReadTxt(byte[] packet, int offset, int length)
    {
        var end = offset + length;
        var attributes = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        while (offset < end)
        {
            var itemLength = packet[offset++];
            if (itemLength == 0) continue;
            if (offset + itemLength > end) throw new InvalidDataException("DNS TXT record is truncated");
            string item;
            try { item = new UTF8Encoding(false, true).GetString(packet, offset, itemLength); }
            catch (DecoderFallbackException exception) { throw new InvalidDataException("DNS TXT record is not UTF-8", exception); }
            offset += itemLength;
            var separator = item.IndexOf('=');
            if (separator > 0) attributes[item[..separator].ToLowerInvariant()] = item[(separator + 1)..];
        }
        return attributes;
    }

    private static string ReadName(byte[] packet, ref int offset)
    {
        var labels = new List<string>();
        var cursor = offset;
        var resume = -1;
        var jumps = 0;
        while (true)
        {
            Require(packet, cursor, 1);
            var length = packet[cursor++];
            if (length == 0) break;
            if ((length & 0xc0) == 0xc0)
            {
                Require(packet, cursor, 1);
                var pointer = ((length & 0x3f) << 8) | packet[cursor++];
                if (pointer >= packet.Length || ++jumps > 32) throw new InvalidDataException("DNS name pointer is invalid");
                if (resume < 0) resume = cursor;
                cursor = pointer;
                continue;
            }
            if ((length & 0xc0) != 0 || length > 63) throw new InvalidDataException("DNS label is invalid");
            Require(packet, cursor, length);
            labels.Add(Encoding.UTF8.GetString(packet, cursor, length).Replace(".", "\\.", StringComparison.Ordinal));
            cursor += length;
        }
        offset = resume >= 0 ? resume : cursor;
        return string.Join('.', labels);
    }

    private static void WriteName(Stream stream, string name)
    {
        foreach (var label in name.TrimEnd('.').Split('.'))
        {
            var bytes = Encoding.UTF8.GetBytes(label);
            if (bytes.Length is 0 or > 63) throw new ArgumentException("DNS label length is invalid", nameof(name));
            stream.WriteByte((byte)bytes.Length);
            stream.Write(bytes);
        }
        stream.WriteByte(0);
    }

    private static ushort ReadUInt16(byte[] packet, int offset)
    {
        Require(packet, offset, 2);
        return BinaryPrimitives.ReadUInt16BigEndian(packet.AsSpan(offset, 2));
    }

    private static uint ReadUInt32(byte[] packet, int offset)
    {
        Require(packet, offset, 4);
        return BinaryPrimitives.ReadUInt32BigEndian(packet.AsSpan(offset, 4));
    }

    private static void Require(byte[] packet, int offset, int length)
    {
        if (offset < 0 || length < 0 || offset > packet.Length - length)
            throw new InvalidDataException("DNS packet is truncated");
    }
}
