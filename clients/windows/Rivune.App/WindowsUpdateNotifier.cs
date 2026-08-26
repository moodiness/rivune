using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;

namespace Rivune.App;

internal sealed class WindowsUpdateNotifier : IDisposable
{
    private const string ActivationArgument = "action=open-update";
    private readonly AppNotificationManager _manager = AppNotificationManager.Default;
    private readonly Action _activated;
    private bool _registered;

    internal WindowsUpdateNotifier(Action activated)
    {
        _activated = activated;
        if (!AppNotificationManager.IsSupported()) return;
        try
        {
            _manager.NotificationInvoked += NotificationInvoked;
            _manager.Register();
            _registered = true;
        }
        catch
        {
            _manager.NotificationInvoked -= NotificationInvoked;
        }
    }

    internal bool Deliver(AppUpdateCheckResult update)
    {
        if (!_registered) return false;
        try
        {
            var notification = new AppNotificationBuilder()
                .AddArgument("action", "open-update")
                .AddText($"Rivune {update.LatestVersion} is available")
                .AddText("Open Rivune to review the verified update. Nothing is downloaded automatically.")
                .BuildNotification();
            notification.Tag = "app-update";
            notification.Group = "stable";
            _manager.Show(notification);
            return true;
        }
        catch
        {
            return false;
        }
    }

    private void NotificationInvoked(AppNotificationManager sender, AppNotificationActivatedEventArgs args)
    {
        if (args.Argument.Contains(ActivationArgument, StringComparison.Ordinal)) _activated();
    }

    public void Dispose()
    {
        if (!_registered) return;
        _manager.NotificationInvoked -= NotificationInvoked;
        try { _manager.Unregister(); }
        catch { }
        _registered = false;
    }
}
