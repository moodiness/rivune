using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Automation.Provider;
using Microsoft.UI.Xaml.Controls;

namespace Rivune.App;

public sealed class ModalDialog : ContentControl
{
    public event EventHandler? CloseRequested;

    protected override AutomationPeer OnCreateAutomationPeer() => new ModalDialogAutomationPeer(this);

    internal void RequestAutomationClose() =>
        DispatcherQueue.TryEnqueue(() => CloseRequested?.Invoke(this, EventArgs.Empty));
}

internal sealed class ModalDialogAutomationPeer(ModalDialog owner) : FrameworkElementAutomationPeer(owner), IWindowProvider
{
    private ModalDialog Dialog => (ModalDialog)Owner;

    public WindowInteractionState InteractionState => WindowInteractionState.ReadyForUserInteraction;
    public bool IsModal => true;
    public bool IsTopmost => true;
    public bool Maximizable => false;
    public bool Minimizable => false;
    public WindowVisualState VisualState => WindowVisualState.Normal;

    protected override string GetClassNameCore() => nameof(ModalDialog);
    protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Window;
    protected override object? GetPatternCore(PatternInterface patternInterface) =>
        patternInterface == PatternInterface.Window ? this : base.GetPatternCore(patternInterface);

    public void Close() => Dialog.RequestAutomationClose();
    public void SetVisualState(WindowVisualState state) { }
    public bool WaitForInputIdle(int milliseconds) => true;
}
