using System.Buffers;
using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Text;

namespace Rivune.App;

internal sealed class LoopbackMediaProxy : IDisposable
{
    private const int MaximumRequestHeaderBytes = 16 * 1024;
    private readonly Uri _target;
    private readonly Func<Uri, bool> _isAllowed;
    private readonly TcpListener _listener;
    private readonly HttpClient _upstream;
    private readonly CancellationTokenSource _stopping = new();
    private readonly SemaphoreSlim _connectionSlots = new(8, 8);
    private readonly string _path;
    private readonly string _expectedHost;
    private readonly Task _accepting;
    private bool _disposed;

    public LoopbackMediaProxy(Uri target, Func<Uri, bool> isAllowed, HttpMessageHandler? handler = null)
    {
        ArgumentNullException.ThrowIfNull(target);
        _isAllowed = isAllowed ?? throw new ArgumentNullException(nameof(isAllowed));
        if (!_isAllowed(target)) throw new ArgumentException("The media target is outside the Rivune origin.", nameof(target));

        _target = target;
        _path = $"/{Guid.NewGuid():N}/media";
        _listener = new TcpListener(IPAddress.Loopback, 0);
        _listener.Start();
        var endpoint = (IPEndPoint)_listener.LocalEndpoint;
        _expectedHost = $"127.0.0.1:{endpoint.Port}";
        PlaybackUri = new Uri($"http://{_expectedHost}{_path}", UriKind.Absolute);
        _upstream = new HttpClient(handler ?? new SocketsHttpHandler
        {
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.None,
            UseCookies = false,
        }, disposeHandler: true)
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
        _accepting = AcceptLoopAsync();
    }

    public Uri PlaybackUri { get; }

    private async Task AcceptLoopAsync()
    {
        while (!_stopping.IsCancellationRequested)
        {
            TcpClient client;
            try
            {
                client = await _listener.AcceptTcpClientAsync(_stopping.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch (ObjectDisposedException)
            {
                return;
            }
            catch (SocketException) when (_stopping.IsCancellationRequested)
            {
                return;
            }
            _ = HandleLimitedAsync(client);
        }
    }

    private async Task HandleLimitedAsync(TcpClient client)
    {
        try
        {
            await _connectionSlots.WaitAsync(_stopping.Token).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            client.Dispose();
            return;
        }

        try
        {
            await HandleClientAsync(client, _stopping.Token).ConfigureAwait(false);
        }
        catch
        {
        }
        finally
        {
            client.Dispose();
            _connectionSlots.Release();
        }
    }

    private async Task HandleClientAsync(TcpClient client, CancellationToken cancellationToken)
    {
        using var stream = client.GetStream();
        var request = await ReadRequestAsync(stream, cancellationToken).ConfigureAwait(false);
        if (request is null ||
            request.Target != _path ||
            !StringComparer.OrdinalIgnoreCase.Equals(request.Host, _expectedHost) ||
            request.Method is not ("GET" or "HEAD"))
        {
            await WriteErrorAsync(stream, HttpStatusCode.NotFound, cancellationToken).ConfigureAwait(false);
            return;
        }

        using var upstreamRequest = new HttpRequestMessage(
            request.Method == "HEAD" ? HttpMethod.Head : HttpMethod.Get,
            _target);
        if (request.Range is not null && RangeHeaderValue.TryParse(request.Range, out var range))
            upstreamRequest.Headers.Range = range;

        using var response = await _upstream.SendAsync(
            upstreamRequest,
            HttpCompletionOption.ResponseHeadersRead,
            cancellationToken).ConfigureAwait(false);
        var finalUri = response.RequestMessage?.RequestUri ?? _target;
        var statusCode = (int)response.StatusCode;
        if (!_isAllowed(finalUri) || statusCode is >= 300 and <= 399)
        {
            await WriteErrorAsync(stream, HttpStatusCode.BadGateway, cancellationToken).ConfigureAwait(false);
            return;
        }

        var headers = new StringBuilder(256)
            .Append("HTTP/1.1 ").Append(statusCode).Append(' ').Append(SafeReason(response.ReasonPhrase)).Append("\r\n")
            .Append("Connection: close\r\n")
            .Append("Cache-Control: no-store\r\n");
        if (response.Content.Headers.ContentLength is long length)
            headers.Append("Content-Length: ").Append(length).Append("\r\n");
        if (response.Content.Headers.ContentType is { } contentType)
            headers.Append("Content-Type: ").Append(contentType).Append("\r\n");
        if (response.Content.Headers.ContentRange is { } contentRange)
            headers.Append("Content-Range: ").Append(contentRange).Append("\r\n");
        if (response.Headers.AcceptRanges.Count > 0)
            headers.Append("Accept-Ranges: ").Append(string.Join(", ", response.Headers.AcceptRanges)).Append("\r\n");
        headers.Append("\r\n");
        await stream.WriteAsync(Encoding.ASCII.GetBytes(headers.ToString()), cancellationToken).ConfigureAwait(false);

        if (request.Method == "GET")
        {
            await using var body = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
            await body.CopyToAsync(stream, 64 * 1024, cancellationToken).ConfigureAwait(false);
        }
    }

    private static async Task<ProxyRequest?> ReadRequestAsync(NetworkStream stream, CancellationToken cancellationToken)
    {
        var buffer = ArrayPool<byte>.Shared.Rent(MaximumRequestHeaderBytes);
        try
        {
            var total = 0;
            while (total < MaximumRequestHeaderBytes)
            {
                var read = await stream.ReadAsync(
                    buffer.AsMemory(total, MaximumRequestHeaderBytes - total),
                    cancellationToken).ConfigureAwait(false);
                if (read == 0) return null;
                total += read;
                var end = HeaderEnd(buffer.AsSpan(0, total));
                if (end < 0) continue;

                var lines = Encoding.ASCII.GetString(buffer, 0, end).Split("\r\n", StringSplitOptions.None);
                var start = lines[0].Split(' ', StringSplitOptions.RemoveEmptyEntries);
                if (start.Length != 3 || !start[2].StartsWith("HTTP/1.", StringComparison.Ordinal)) return null;
                string? host = null;
                string? range = null;
                for (var index = 1; index < lines.Length; index++)
                {
                    var separator = lines[index].IndexOf(':');
                    if (separator <= 0) continue;
                    var name = lines[index][..separator].Trim();
                    var value = lines[index][(separator + 1)..].Trim();
                    if (name.Equals("Host", StringComparison.OrdinalIgnoreCase)) host = value;
                    else if (name.Equals("Range", StringComparison.OrdinalIgnoreCase)) range = value;
                }
                return new ProxyRequest(start[0].ToUpperInvariant(), start[1], host, range);
            }
            return null;
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer, clearArray: true);
        }
    }

    private static int HeaderEnd(ReadOnlySpan<byte> bytes)
    {
        for (var index = 3; index < bytes.Length; index++)
        {
            if (bytes[index - 3] == '\r' && bytes[index - 2] == '\n' && bytes[index - 1] == '\r' && bytes[index] == '\n')
                return index - 3;
        }
        return -1;
    }

    private static async Task WriteErrorAsync(NetworkStream stream, HttpStatusCode status, CancellationToken cancellationToken)
    {
        var body = Encoding.ASCII.GetBytes($"{(int)status} {status}\n");
        var headers = Encoding.ASCII.GetBytes(
            $"HTTP/1.1 {(int)status} {status}\r\nConnection: close\r\nCache-Control: no-store\r\nContent-Type: text/plain\r\nContent-Length: {body.Length}\r\n\r\n");
        await stream.WriteAsync(headers, cancellationToken).ConfigureAwait(false);
        await stream.WriteAsync(body, cancellationToken).ConfigureAwait(false);
    }

    private static string SafeReason(string? value) =>
        string.IsNullOrWhiteSpace(value) ? "Response" : value.Replace("\r", string.Empty).Replace("\n", string.Empty);

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _stopping.Cancel();
        _listener.Stop();
        _upstream.Dispose();
        GC.SuppressFinalize(this);
    }

    private sealed record ProxyRequest(string Method, string Target, string? Host, string? Range);
}
