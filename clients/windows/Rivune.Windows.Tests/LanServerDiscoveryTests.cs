using System.Buffers.Binary;
using System.Text;
using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class LanServerDiscoveryTests
{
    [Fact]
    public void ParsesSecureAndTrustedLanAnnouncements()
    {
        var secure = RivuneLanService.Parse("Living room", new Dictionary<string, string>
        {
            ["protocol"] = "20",
            ["url"] = "https://media.example.com",
            ["version"] = "1.10.0",
        });
        Assert.NotNull(secure);
        Assert.Equal("https://media.example.com/", secure.Address.AbsoluteUri);
        Assert.Equal("1.10.0", secure.Version);
        Assert.True(secure.UsesSecureTransport);

        var local = RivuneLanService.Parse("Bedroom", new Dictionary<string, string>
        {
            ["protocol"] = "20",
            ["url"] = "http://192.168.1.20:8080/",
        });
        Assert.NotNull(local);
        Assert.Equal("http://192.168.1.20:8080/", local.Address.AbsoluteUri);
        Assert.False(local.UsesSecureTransport);
    }

    [Theory]
    [InlineData("19", "https://media.example.com")]
    [InlineData("20", "http://media.example.com")]
    [InlineData("20", "http://198.51.100.20:8080")]
    [InlineData("20", "https://user:secret@media.example.com")]
    [InlineData("20", "https://media.example.com/path")]
    [InlineData("20", "https://media.example.com?token=secret")]
    [InlineData("20", "ftp://media.example.com")]
    public void RejectsIncompatibleOrUnsafeAnnouncements(string protocol, string address)
    {
        Assert.Null(RivuneLanService.Parse("Hostile", new Dictionary<string, string>
        {
            ["protocol"] = protocol,
            ["url"] = address,
        }));
    }

    [Fact]
    public void QueryTargetsTheRivunePtrService()
    {
        var query = DnsPacket.BuildPtrQuery(RivuneLanService.ServiceType);

        Assert.Equal(0, query[0]);
        Assert.Equal(1, BinaryPrimitives.ReadUInt16BigEndian(query.AsSpan(4, 2)));
        Assert.True(query.AsSpan().EndsWith(new byte[] { 0, 12, 0, 1 }));
        Assert.True(query.AsSpan().IndexOf(Encoding.ASCII.GetBytes("rivune")) >= 0);
    }

    [Fact]
    public void ParsesCompressedPtrAndTxtResponse()
    {
        var packet = BuildResponse();

        var records = DnsPacket.Parse(packet);

        var pointer = Assert.Single(records, record => record.Type == DnsRecordType.Ptr);
        Assert.Equal(RivuneLanService.ServiceType, pointer.Name);
        Assert.Equal("Living room._rivune._tcp.local", pointer.Target);
        var text = Assert.Single(records, record => record.Type == DnsRecordType.Txt);
        Assert.Equal("https://media.example.com", text.Attributes?["url"]);
        Assert.Equal("20", text.Attributes?["protocol"]);
        Assert.Equal("Living room", DnsPacket.InstanceName(text.Name));
    }

    [Fact]
    public void RejectsTruncatedAndRecursivePackets()
    {
        Assert.Throws<InvalidDataException>(() => DnsPacket.Parse([0, 1, 2]));
        var recursive = new byte[24];
        recursive[6] = 0;
        recursive[7] = 1;
        recursive[12] = 0xc0;
        recursive[13] = 12;
        Assert.Throws<InvalidDataException>(() => DnsPacket.Parse(recursive));
    }

    private static byte[] BuildResponse()
    {
        using var stream = new MemoryStream();
        WriteUInt16(stream, 0);
        WriteUInt16(stream, 0x8400);
        WriteUInt16(stream, 0);
        WriteUInt16(stream, 2);
        WriteUInt16(stream, 0);
        WriteUInt16(stream, 0);
        var serviceOffset = checked((ushort)stream.Position);
        WriteName(stream, RivuneLanService.ServiceType);
        WriteUInt16(stream, 12);
        WriteUInt16(stream, 1);
        WriteUInt32(stream, 120);
        using (var data = new MemoryStream())
        {
            WriteLabel(data, "Living room");
            WritePointer(data, serviceOffset);
            WriteUInt16(stream, checked((ushort)data.Length));
            data.Position = 0;
            data.CopyTo(stream);
        }
        WriteLabel(stream, "Living room");
        WritePointer(stream, serviceOffset);
        WriteUInt16(stream, 16);
        WriteUInt16(stream, 1);
        WriteUInt32(stream, 120);
        var values = new[] { "protocol=20", "url=https://media.example.com", "version=1.10.0" };
        var payloadLength = values.Sum(value => Encoding.UTF8.GetByteCount(value) + 1);
        WriteUInt16(stream, checked((ushort)payloadLength));
        foreach (var value in values)
        {
            var bytes = Encoding.UTF8.GetBytes(value);
            stream.WriteByte(checked((byte)bytes.Length));
            stream.Write(bytes);
        }
        return stream.ToArray();
    }

    private static void WriteName(Stream stream, string name)
    {
        foreach (var label in name.Split('.')) WriteLabel(stream, label);
        stream.WriteByte(0);
    }

    private static void WriteLabel(Stream stream, string label)
    {
        var bytes = Encoding.UTF8.GetBytes(label);
        stream.WriteByte(checked((byte)bytes.Length));
        stream.Write(bytes);
    }

    private static void WritePointer(Stream stream, ushort offset) => WriteUInt16(stream, (ushort)(0xc000 | offset));

    private static void WriteUInt16(Stream stream, ushort value)
    {
        Span<byte> bytes = stackalloc byte[2];
        BinaryPrimitives.WriteUInt16BigEndian(bytes, value);
        stream.Write(bytes);
    }

    private static void WriteUInt32(Stream stream, uint value)
    {
        Span<byte> bytes = stackalloc byte[4];
        BinaryPrimitives.WriteUInt32BigEndian(bytes, value);
        stream.Write(bytes);
    }
}
