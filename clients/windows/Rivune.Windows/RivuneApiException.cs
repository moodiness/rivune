namespace Rivune.Windows;

public abstract class RivuneApiException : Exception
{
    protected RivuneApiException(string message, Exception? innerException = null)
        : base(message, innerException)
    {
    }
}

public sealed class InvalidServerUrlException : RivuneApiException
{
    public InvalidServerUrlException(string value)
        : base($"Invalid Rivune server URL: {value}")
    {
        Value = value;
    }

    public string Value { get; }
}

public sealed class IncompatibleProtocolException : RivuneApiException
{
    public IncompatibleProtocolException(int expected, int actual)
        : base($"Rivune protocol {actual} is incompatible; this client requires {expected}.")
    {
        Expected = expected;
        Actual = actual;
    }

    public int Expected { get; }
    public int Actual { get; }
}

public sealed class InvalidResponseException : RivuneApiException
{
    public InvalidResponseException(Exception? innerException = null)
        : base("The Rivune server returned an invalid response.", innerException)
    {
    }
}

public sealed class ResponseTooLargeException : RivuneApiException
{
    public ResponseTooLargeException(long maximumBytes)
        : base($"The Rivune server response exceeded the {maximumBytes}-byte limit.")
    {
        MaximumBytes = maximumBytes;
    }

    public long MaximumBytes { get; }
}

public sealed class NotAuthenticatedException : RivuneApiException
{
    public NotAuthenticatedException()
        : base("Authentication is required.")
    {
    }
}

public sealed class RivuneServerException : RivuneApiException
{
    public RivuneServerException(int statusCode, string code, string message)
        : base(message)
    {
        StatusCode = statusCode;
        Code = code;
    }

    public int StatusCode { get; }
    public string Code { get; }
}
