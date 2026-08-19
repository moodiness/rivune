using System.Runtime.InteropServices.WindowsRuntime;
using Windows.Foundation;
using Windows.Web.Http;
using Windows.Web.Http.Filters;
using WinHttpRequestMessage = Windows.Web.Http.HttpRequestMessage;
using WinHttpResponseMessage = Windows.Web.Http.HttpResponseMessage;

namespace Rivune.App;

internal sealed class SameOriginHttpFilter : IHttpFilter
{
    private readonly HttpBaseProtocolFilter _inner = new() { AllowAutoRedirect = false };
    private readonly Func<Uri, bool> _isAllowed;
    private bool _disposed;

    public SameOriginHttpFilter(Func<Uri, bool> isAllowed)
    {
        _isAllowed = isAllowed ?? throw new ArgumentNullException(nameof(isAllowed));
    }

    public IAsyncOperationWithProgress<WinHttpResponseMessage, HttpProgress> SendRequestAsync(WinHttpRequestMessage request)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(request);

        return AsyncInfo.Run<WinHttpResponseMessage, HttpProgress>(async (cancellationToken, progress) =>
        {
            var requestUri = request.RequestUri;
            if (requestUri is null || !_isAllowed(requestUri))
                throw new UnauthorizedAccessException("Adaptive media requests must remain on the configured Rivune origin.");

            var response = await _inner.SendRequestAsync(request).AsTask(cancellationToken, progress);
            var responseUri = response.RequestMessage?.RequestUri ?? requestUri;
            var statusCode = (int)response.StatusCode;
            if (!_isAllowed(responseUri) || statusCode is >= 300 and <= 399)
            {
                response.Dispose();
                throw new UnauthorizedAccessException("Adaptive media redirects are not allowed.");
            }
            return response;
        });
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _inner.Dispose();
        GC.SuppressFinalize(this);
    }
}
