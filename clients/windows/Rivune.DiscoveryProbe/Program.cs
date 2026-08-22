using System.Net;
using System.Net.Sockets;
using Rivune.App;

if (args.Length != 2 || !TimeSpan.TryParse(args[1], out var timeout) || timeout <= TimeSpan.Zero)
{
    Console.Error.WriteLine("Usage: Rivune.DiscoveryProbe EXPECTED_ORIGIN TIMEOUT");
    return 64;
}
if (!Uri.TryCreate(args[0], UriKind.Absolute, out var expectedOrigin))
{
    Console.Error.WriteLine("EXPECTED_ORIGIN must be an absolute URL.");
    return 64;
}

var expected = new Uri(expectedOrigin.GetLeftPart(UriPartial.Authority));
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
            if (record.Type != DnsRecordType.Txt || record.Attributes is null) continue;
            var service = RivuneLanService.Parse(DnsPacket.InstanceName(record.Name), record.Attributes);
            if (service?.Address != expected) continue;
            Console.WriteLine($"Discovered {service.Name} at {service.Address.GetLeftPart(UriPartial.Authority)}.");
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
