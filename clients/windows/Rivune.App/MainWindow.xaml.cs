using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;

namespace Rivune.App;

public sealed partial class MainWindow : Window
{
    private MainPage? _page;
    private bool _allowClose;
    private OverlappedPresenterState? _presenterStateBeforePlayerMode;

    public MainWindow(string? updateError = null)
    {
        InitializeComponent();
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(AppTitleBar);
        ConfigureTitleBar();
        AppWindow.SetIcon(Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.ico"));
        AppWindow.Closing += AppWindow_Closing;
        Activated += MainWindow_Activated;
        RootFrame.Navigate(typeof(MainPage));
        _page = RootFrame.Content as MainPage;
        _page?.SetStartupUpdateError(updateError);
    }

    private void ConfigureTitleBar()
    {
        var titleBar = AppWindow.TitleBar;
        titleBar.BackgroundColor = Colors.Black;
        titleBar.InactiveBackgroundColor = Colors.Black;
        titleBar.ButtonBackgroundColor = Colors.Black;
        titleBar.ButtonInactiveBackgroundColor = Colors.Black;
        titleBar.ButtonHoverBackgroundColor = ColorHelper.FromArgb(255, 27, 27, 27);
        titleBar.ButtonPressedBackgroundColor = ColorHelper.FromArgb(255, 57, 57, 57);
        titleBar.ButtonForegroundColor = Colors.White;
        titleBar.ButtonInactiveForegroundColor = ColorHelper.FromArgb(255, 140, 146, 154);
    }
    internal AppWindowPresenterKind PlayerPresenterKind => AppWindow.Presenter.Kind;

    internal void SetPlayerPresenter(AppWindowPresenterKind kind)
    {
        if (kind is AppWindowPresenterKind.Default or AppWindowPresenterKind.Overlapped)
        {
            AppWindow.SetPresenter(AppWindowPresenterKind.Default);
            SetCustomTitleBarVisible(true);
            if (_presenterStateBeforePlayerMode == OverlappedPresenterState.Maximized &&
                AppWindow.Presenter is OverlappedPresenter overlapped)
                overlapped.Maximize();
            _presenterStateBeforePlayerMode = null;
            return;
        }

        if (_presenterStateBeforePlayerMode is null && AppWindow.Presenter is OverlappedPresenter current)
            _presenterStateBeforePlayerMode = current.State;
        SetCustomTitleBarVisible(kind != AppWindowPresenterKind.FullScreen);
        AppWindow.SetPresenter(kind);
    }

    private void SetCustomTitleBarVisible(bool visible)
    {
        AppTitleBar.Visibility = visible ? Visibility.Visible : Visibility.Collapsed;
        TitleBarRow.Height = new GridLength(visible ? 32 : 0);
    }


    private async void MainWindow_Activated(object sender, WindowActivatedEventArgs args)
    {
        if (_page is not null)
            await _page.HandleWindowActivationAsync(args.WindowActivationState != WindowActivationState.Deactivated);
    }

    private async void AppWindow_Closing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (_allowClose) return;
        args.Cancel = true;
        if (_page is not null) await _page.CloseForWindowShutdownAsync();
        _allowClose = true;
        Close();
    }
}
