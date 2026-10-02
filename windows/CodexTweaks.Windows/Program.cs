using CodexTweaks.Windows.Generated;
using Microsoft.Windows.AppLifecycle;
using Velopack;

namespace CodexTweaks.Windows;

internal static class Program
{
    private static AppInstance? _mainInstance;

    [STAThread]
    private static void Main(string[] args)
    {
        VelopackApp.Build().Run();
        if (!RegisterMainInstance())
        {
            return;
        }
        XamlGeneratedProgram.XamlGeneratedMain();
    }

    private static bool RegisterMainInstance()
    {
        try
        {
            var currentInstance = AppInstance.GetCurrent();
            var mainInstance = AppInstance.FindOrRegisterForKey(ApplicationIdentity.BundleIdentifier + ".main");
            if (!mainInstance.IsCurrent)
            {
                var activationArguments = currentInstance.GetActivatedEventArgs();
                Task.Run(async () =>
                        await mainInstance.RedirectActivationToAsync(activationArguments))
                    .GetAwaiter()
                    .GetResult();
                return false;
            }

            _mainInstance = mainInstance;
            _mainInstance.Activated += (_, _) => App.RequestMainWindowActivation();
            return true;
        }
        catch (Exception exception)
        {
            App.LogException("Single-instance registration failed; continuing normally", exception);
            return true;
        }
    }
}
