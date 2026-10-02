using System.Globalization;
using System.Text;

namespace CodexTweaks.Windows.Services;

// Small startup fallback, independent of WinUI and the Go sidecar. Callers supply
// fixed event summaries; arbitrary external text is not safe diagnostic input.
internal sealed class FrontendDiagnostics(string path)
{
    internal const int MaxFileBytes = 256 * 1024;
    internal const int MaxEntryBytes = 1024;

    private static readonly UTF8Encoding Utf8 = new(encoderShouldEmitUTF8Identifier: false);
    private readonly object _gate = new();
    private string? _lastError;

    // Retain the most recent persistence failure even if a later write succeeds.
    // Exception messages and paths are deliberately excluded from this status.
    internal string? LastError
    {
        get { lock (_gate) { return _lastError; } }
    }

    internal void Log(string eventSummary)
    {
        lock (_gate)
        {
            try
            {
                var prefix = $"{DateTimeOffset.UtcNow:O} ";
                var summary = BoundSummary(eventSummary, MaxEntryBytes - Utf8.GetByteCount(prefix) - 1);
                var entry = Utf8.GetBytes(prefix + summary + "\n");
                var logPath = Path.GetFullPath(path);
                var backupPath = logPath + ".1";
                var directory = Path.GetDirectoryName(logPath)!;
                EnsureUnlinkedDirectory(directory);
                NormalizeFile(logPath);
                NormalizeFile(backupPath);

                if (RegularFileLength(logPath) is { } length && length + entry.Length > MaxFileBytes)
                {
                    File.Move(logPath, backupPath, overwrite: true);
                }
                using var file = new FileStream(logPath, FileMode.Append, FileAccess.Write, FileShare.Read);
                file.Write(entry);
                file.Flush();
            }
            catch (Exception exception)
            {
                // Startup diagnostics cannot prevent launch, but failure remains
                // observable without recursively writing to the failed sink.
                _lastError = ExceptionIdentity(exception);
            }
        }
    }

    internal void LogException(string eventSummary, Exception exception)
    {
        Log(eventSummary + "; " + ExceptionIdentity(exception));
    }

    private static string ExceptionIdentity(Exception exception) =>
        $"type={exception.GetType().FullName} hresult=0x{exception.HResult:X8}";

    private static void EnsureUnlinkedDirectory(string directory)
    {
        for (var current = new DirectoryInfo(directory); current is not null; current = current.Parent)
        {
            var attributes = AttributesOrMissing(current.FullName);
            if (attributes is { } value && (value & FileAttributes.ReparsePoint) != 0)
            {
                throw new IOException("Linked diagnostics directories are not supported.");
            }
        }
        Directory.CreateDirectory(directory);
    }

    private static FileAttributes? AttributesOrMissing(string filePath)
    {
        try { return File.GetAttributes(filePath); }
        catch (FileNotFoundException) { return null; }
        catch (DirectoryNotFoundException) { return null; }
    }

    private static long? RegularFileLength(string filePath)
    {
        if (AttributesOrMissing(filePath) is not { } attributes)
        {
            return null;
        }
        if ((attributes & (FileAttributes.Directory | FileAttributes.ReparsePoint)) != 0)
        {
            throw new IOException("Diagnostics paths must be regular files.");
        }
        return new FileInfo(filePath).Length;
    }

    private static void NormalizeFile(string filePath)
    {
        if (RegularFileLength(filePath) is not { } length || length <= MaxFileBytes)
        {
            return;
        }

        // Previous versions had no cap. Retain a bounded recent tail before the
        // file becomes a backup; only these two exact log names are touched.
        using var file = new FileStream(filePath, FileMode.Open, FileAccess.ReadWrite, FileShare.Read);
        file.Seek(-MaxFileBytes, SeekOrigin.End);
        var tail = new byte[MaxFileBytes];
        file.ReadExactly(tail);
        var start = Array.IndexOf(tail, (byte)'\n') + 1;
        if (start == 0)
        {
            while (start < tail.Length && (tail[start] & 0xC0) == 0x80)
            {
                start++;
            }
        }
        file.Position = 0;
        file.Write(tail.AsSpan(start));
        file.SetLength(tail.Length - start);
        file.Flush();
    }

    private static string BoundSummary(string summary, int maxBytes)
    {
        var result = new StringBuilder();
        var byteCount = 0;
        foreach (var rune in summary.EnumerateRunes())
        {
            var category = Rune.GetUnicodeCategory(rune);
            var value = rune.Value switch
            {
                '\n' => "\\n",
                '\r' => "\\r",
                '\t' => "\\t",
                _ when category is UnicodeCategory.Control or UnicodeCategory.Format
                    or UnicodeCategory.LineSeparator or UnicodeCategory.ParagraphSeparator => $"\\u{rune.Value:x4}",
                _ => rune.ToString(),
            };
            var nextBytes = Utf8.GetByteCount(value);
            if (byteCount + nextBytes > maxBytes)
            {
                const string marker = " [truncated]";
                var encoded = Utf8.GetBytes(result.ToString());
                var end = Math.Min(encoded.Length, maxBytes - marker.Length);
                while (end > 0 && end < encoded.Length && (encoded[end] & 0xC0) == 0x80)
                {
                    end--;
                }
                return Utf8.GetString(encoded, 0, end) + marker;
            }
            result.Append(value);
            byteCount += nextBytes;
        }
        return result.ToString();
    }
}
