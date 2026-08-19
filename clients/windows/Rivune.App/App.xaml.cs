using Microsoft.UI.Xaml;

namespace Rivune.App;

public partial class App : Application
{
    public static MainWindow MainWindow { get; private set; } = null!;

    public App()
    {
        InitializeComponent();
        RequestedTheme = ApplicationTheme.Dark;
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        var processPath = Environment.ProcessPath ?? throw new InvalidOperationException("Windows did not report the Rivune executable path.");
        PortableUpdateStartupCommand? command;
        try
        {
            command = PortableAppUpdate.ParseStartupCommand(Environment.GetCommandLineArgs()[1..], processPath);
        }
        catch (Exception exception)
        {
            LaunchMainWindow(exception.Message);
            return;
        }

        if (command is PortableUpdateStartupCommand.Apply apply)
        {
            _ = RunUpdateApplyAsync(apply.Request);
            return;
        }

        LaunchMainWindow(command is PortableUpdateStartupCommand.ReportError error ? error.Message : null);
        if (command is PortableUpdateStartupCommand.Cleanup cleanup)
            _ = PortableAppUpdate.DeleteTemporarySourceAsync(cleanup.SourcePath);
    }

    private static void LaunchMainWindow(string? updateError)
    {
        MainWindow = new MainWindow(updateError);
        MainWindow.Activate();
    }

    private static async Task RunUpdateApplyAsync(PortableUpdateApplyRequest request)
    {
        var exitWhenComplete = true;
        try
        {
            await PortableAppUpdate.ApplyAsync(request);
        }
        catch (Exception exception)
        {
            try
            {
                var startInfo = new System.Diagnostics.ProcessStartInfo(request.TargetPath) { UseShellExecute = false };
                startInfo.ArgumentList.Add(PortableAppUpdate.ErrorSwitch);
                startInfo.ArgumentList.Add($"The verified update could not be applied. {exception.Message}");
                if (System.Diagnostics.Process.Start(startInfo) is null)
                    throw new InvalidOperationException("Windows did not restart Rivune.");
            }
            catch
            {
                exitWhenComplete = false;
                LaunchMainWindow($"The verified update could not be applied. {exception.Message}");
            }
        }
        finally
        {
            if (exitWhenComplete) Current.Exit();
        }
    }
}
