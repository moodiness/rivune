namespace Rivune.App;

internal sealed class LocalizedPropertyState
{
    private string? _key;
    private string? _rendered;
    private bool _applying;

    internal void Apply(
        Func<string?> read,
        Action<string> write,
        Func<string, bool> containsKey,
        Func<string, string> translate)
    {
        if (_applying) return;

        _applying = true;
        try
        {
            var current = read();
            if (!string.Equals(current, _rendered, StringComparison.Ordinal))
            {
                _key = current is not null && containsKey(current) ? current : null;
                _rendered = null;
            }
            if (_key is null) return;

            var translated = translate(_key);
            _rendered = translated;
            if (!string.Equals(current, translated, StringComparison.Ordinal)) write(translated);
        }
        finally
        {
            _applying = false;
        }
    }
}
