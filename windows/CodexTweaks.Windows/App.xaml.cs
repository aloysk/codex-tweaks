using CodexTweaks.Windows.Generated;
using CodexTweaks.Windows.Services;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;

namespace CodexTweaks.Windows;

public partial class App : Microsoft.UI.Xaml.Application
{
    private static DispatcherQueue? _mainDispatcher;
    private static int _pendingActivation;

    private MainWindow? _window;
    private TrayIconService? _trayIcon;
    private bool _quitting;
    private static readonly string FrontendLogPath = Path.Combine(
        Environment.GetEnvironmentVariable(ApplicationIdentity.EnvironmentPrefix + "APPLICATION_SUPPORT")
            ?? Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        ApplicationIdentity.Name,
        "Logs",
        "windows-frontend.log");
    private static readonly FrontendDiagnostics Diagnostics = new(FrontendLogPath);

    internal static string? LastLogError => Diagnostics.LastError;

    public App()
    {
        UnhandledException += (_, args) =>
        {
            LogException("Unhandled exception", args.Exception);
        };
        try
        {
            Log("Application startup entered.");
            InitializeComponent();
            Log("Application XAML initialized.");
        }
        catch (Exception exception)
        {
            LogException("Application constructor failed", exception);
            throw;
        }
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        try
        {
            Log("Window launch entered.");
            _window = new MainWindow();
            Log("Window constructed.");
            try
            {
                _trayIcon = new TrayIconService(_window, QuitAsync);
                if (!_window.EnableTrayMode())
                {
                    throw new InvalidOperationException(
                        "The main window cannot intercept close requests.");
                }
                Log("Windows tray icon initialized.");
            }
            catch (Exception exception)
            {
                _trayIcon?.Dispose();
                _trayIcon = null;
                LogException("Tray initialization failed; close will exit normally", exception);
            }
            _window.Activate();
            Log("Window activated.");

            Volatile.Write(ref _mainDispatcher, DispatcherQueue.GetForCurrentThread());
            if (Interlocked.Exchange(ref _pendingActivation, 0) != 0)
            {
                _window.ShowFromTray();
            }
        }
        catch (Exception exception)
        {
            LogException("Window launch failed", exception);
            throw;
        }
    }

    internal static void RequestMainWindowActivation()
    {
        var dispatcher = Volatile.Read(ref _mainDispatcher);
        if (dispatcher is null
            || !dispatcher.TryEnqueue(() =>
                (Microsoft.UI.Xaml.Application.Current as App)?._window?.ShowFromTray()))
        {
            Interlocked.Exchange(ref _pendingActivation, 1);
        }
    }

    private async Task QuitAsync()
    {
        if (_quitting)
        {
            return;
        }

        _quitting = true;
        Log("Application quit requested from the tray.");
        Volatile.Write(ref _mainDispatcher, null);
        _trayIcon?.Dispose();
        _trayIcon = null;

        if (_window is not null)
        {
            await _window.ShutdownAsync();
            _window.CloseForExit();
            _window = null;
        }
        Exit();
    }

    internal static void Log(string message)
    {
        Diagnostics.Log(message);
    }

    internal static void LogException(string eventSummary, Exception exception) =>
        Diagnostics.LogException(eventSummary, exception);
}
