namespace Rivune.App;

internal sealed class ModalFocusRestore<T> where T : class
{
    private T? _target;

    public bool IsOpen { get; private set; }

    public void Open(T? target)
    {
        if (!IsOpen) _target = target;
        IsOpen = true;
    }

    public T? Close()
    {
        if (!IsOpen) return null;
        IsOpen = false;
        return TakeTarget();
    }

    private T? TakeTarget()
    {
        var target = _target;
        _target = null;
        return target;
    }
}
