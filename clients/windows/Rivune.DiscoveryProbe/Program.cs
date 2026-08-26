using System.Net;
using System.Net.Sockets;
using Rivune.App;

if (args.Length != 3 ||
    !int.TryParse(args[1], out var expectedProtocol) || expectedProtocol <= 0 ||
    !TimeSpan.TryParse(args[2], out var timeout) || timeout <= TimeSpan.Zero)
{
    Console.Error.WriteLine("Usage: Rivune.DiscoveryProbe EXPECTED_ORIGIN EXPECTED_PROTOCOL TIMEOUT");
    return 64;
}
if (!Uri.TryCreate(args[0], UriKind.Absolute, out var expectedOrigin))
{
    Console.Error.WriteLine("EXPECTED_ORIGIN must be an absolute URL.");
    return 64;
}

var expected = new Uri(expectedOrigin.GetLeftPart(UriPartial.Authority));
var expectedProtocolText = expectedProtocol.ToString(System.Globalization.CultureInfo.InvariantCulture);
using var cancellation = new CancellationTokenSource(timeout);
using var udp = new UdpClient(AddressFamily.InterNetwork);
udp.Client.SetSocketOption(SocketOptionLevel.Socket, SocketOptionName.ReuseAddress, true);
udp.Client.Bind(new IPEndPoint(IPAddress.Any, 5353));
udp.JoinMulticastGroup(IPAddress.Parse("224.0.0.251"));
var multicast = new IPEndPoint(IPAddress.Parse("224.0.0.251"), 5353);
var query = DnsPacket.BuildPtrQuery(RivuneLanService.ServiceType);

var sender = Task.Run(async () =>
{
    while (true)
    {
        _ = await udp.SendAsync(query, multicast, cancellation.Token);
        await Task.Delay(TimeSpan.FromSeconds(1), cancellation.Token);
    }
}, cancellation.Token);

try
{
    while (true)
    {
        IReadOnlyList<DnsRecord> records;
        try { records = DnsPacket.Parse((await udp.ReceiveAsync(cancellation.Token)).Buffer); }
        catch (InvalidDataException) { continue; }
        foreach (var record in records)
        {
            if (record.Type != DnsRecordType.Txt || record.Attributes is null ||
                !record.Attributes.TryGetValue("protocol", out var protocol) || protocol != expectedProtocolText ||
                !record.Attributes.TryGetValue("url", out var rawAddress) ||
                !Uri.TryCreate(rawAddress, UriKind.Absolute, out var supplied) ||
                new Uri(supplied.GetLeftPart(UriPartial.Authority)) != expected)
            {
                continue;
            }
            Console.WriteLine($"Discovered {DnsPacket.InstanceName(record.Name)} at {expected.GetLeftPart(UriPartial.Authority)} using protocol {expectedProtocolText}.");
            return 0;
        }
    }
}
catch (OperationCanceledException) when (cancellation.IsCancellationRequested)
{
    Console.Error.WriteLine($"No Rivune DNS-SD announcement for {expected.GetLeftPart(UriPartial.Authority)} arrived within {timeout}.");
    return 1;
}
finally
{
    cancellation.Cancel();
    try { await sender; }
    catch (OperationCanceledException) { }
}
