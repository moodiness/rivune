using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class ModalFocusRestoreTests
{
    [Fact]
    public void CloseRestoresOriginalInvokerExactlyOnce()
    {
        var original = new object();
        var replacement = new object();
        var focus = new ModalFocusRestore<object>();

        focus.Open(original);
        focus.Open(replacement);

        Assert.True(focus.IsOpen);
        Assert.Same(original, focus.Close());
        Assert.False(focus.IsOpen);
        Assert.Null(focus.Close());
    }

    [Fact]
    public void CloseWithoutInvokerStillEndsModalSession()
    {
        var focus = new ModalFocusRestore<object>();
        focus.Open(null);

        Assert.True(focus.IsOpen);
        Assert.Null(focus.Close());
        Assert.False(focus.IsOpen);
    }
}
