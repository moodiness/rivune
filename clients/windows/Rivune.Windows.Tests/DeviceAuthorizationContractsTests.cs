using System.Net;
using System.Text;
using System.Text.Json;
using Rivune.Windows;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class DeviceAuthorizationContractsTests
{
    [Fact]
    public async Task BeginAuthorizationSendsInstallationIdentity()
    {
        var handler = new DeviceAuthorizationHandler();
        using var client = new RivuneApiClient("https://rivune.test", handler);

        var authorization = await client.BeginDeviceAuthorizationAsync(
            "installation-1",
            "Living room",
            "windows",
            TestContext.Current.CancellationToken);

        Assert.Equal("ABCD-EFGH", authorization.UserCode);
        var request = Assert.Single(handler.Requests);
        using var body = JsonDocument.Parse(request);
        Assert.Equal("installation-1", body.RootElement.GetProperty("installationId").GetString());
        Assert.Equal("Living room", body.RootElement.GetProperty("deviceName").GetString());
        Assert.Equal("windows", body.RootElement.GetProperty("platform").GetString());
    }

    private sealed class DeviceAuthorizationHandler : HttpMessageHandler
    {
        public List<string> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            if (request.RequestUri!.AbsolutePath == "/.well-known/rivune")
            {
                return JsonResponse(
                    """{"name":"Rivune","serverVersion":"test","protocolVersion":22,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""");
            }

            Assert.Equal("/api/v1/auth/device-code", request.RequestUri.AbsolutePath);
            Assert.Null(request.Headers.Authorization);
            Requests.Add(await request.Content!.ReadAsStringAsync(cancellationToken));
            return JsonResponse(
                """{"deviceCode":"secret","userCode":"ABCD-EFGH","verificationUri":"https://rivune.test/pair","verificationUriComplete":"https://rivune.test/pair?code=ABCD-EFGH","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":5}""",
                HttpStatusCode.Created);
        }

        private static HttpResponseMessage JsonResponse(string json, HttpStatusCode status = HttpStatusCode.OK) => new(status)
        {
            Content = new StringContent(json, Encoding.UTF8, "application/json"),
        };
    }
}
