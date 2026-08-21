using Xunit;
using Rivune.App;

namespace Rivune.Windows.Tests;

public sealed class ServerAddressNormalizerTests
{
    [Theory]
    [InlineData("media.example.com", "https://media.example.com")]
    [InlineData(" media.example.com/path ", "https://media.example.com/path")]
    [InlineData("localhost:8080", "http://localhost:8080")]
    [InlineData("127.0.0.1:8080", "http://127.0.0.1:8080")]
    [InlineData("[::1]:8080", "http://[::1]:8080")]
    [InlineData("10.0.0.5:8080", "http://10.0.0.5:8080")]
    [InlineData("172.31.2.3:8080", "http://172.31.2.3:8080")]
    [InlineData("192.168.1.20:8080", "http://192.168.1.20:8080")]
    [InlineData("[fd00::20]:8080", "http://[fd00::20]:8080")]
    [InlineData("100.64.0.1:8080", "https://100.64.0.1:8080")]
    [InlineData("nas.local:8080", "https://nas.local:8080")]
    [InlineData("https://media.example.com", "https://media.example.com")]
    [InlineData("http://localhost:8080", "http://localhost:8080")]
    [InlineData("  ", "")]
    public void NormalizesServerAddress(string input, string expected)
    {
        Assert.Equal(expected, ServerAddressNormalizer.Normalize(input));
    }
}
