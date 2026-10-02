// Compile the transport client without loading WinUI. These two platform
// adapters are unused by the lifecycle tests; the client itself is production.
namespace CodexTweaks.Windows
{
    internal static class App
    {
        internal static void Log(string _) { }
        internal static void LogException(string _, Exception exception) { }
    }
}

namespace Windows.System.UserProfile
{
    internal static class GlobalizationPreferences
    {
        internal static IReadOnlyList<string> Languages => ["en"];
    }
}
