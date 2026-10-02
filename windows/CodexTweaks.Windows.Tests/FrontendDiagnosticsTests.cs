using System.Text;
using CodexTweaks.Windows.Services;
using Xunit;

namespace CodexTweaks.Windows.Tests;

public sealed class FrontendDiagnosticsTests : IDisposable
{
    private readonly string _root = Directory.CreateTempSubdirectory("codex-companion-diagnostics-").FullName;

    private string LogPath => Path.Combine(_root, "Logs", "windows-frontend.log");

    [Fact]
    public void EntryIsSingleLineBoundedUtf8WithoutBom()
    {
        var diagnostics = new FrontendDiagnostics(LogPath);
        diagnostics.Log("fixture\nforged\r\t\u001b[2J\u202e " + string.Concat(Enumerable.Repeat("你好🙂", 1000)));

        var data = File.ReadAllBytes(LogPath);
        Assert.Null(diagnostics.LastError);
        Assert.InRange(data.Length, 1, FrontendDiagnostics.MaxEntryBytes);
        Assert.False(data.AsSpan().StartsWith(new byte[] { 0xEF, 0xBB, 0xBF }));
        var text = new UTF8Encoding(false, true).GetString(data);
        Assert.Equal(1, text.Count(value => value == '\n'));
        Assert.Contains(@"fixture\nforged\r\t\u001b[2J\u202e", text);
        Assert.Contains("[truncated]", text);
    }

    [Fact]
    public void RotationRetainsOneBoundedBackupAndLeavesUnrelatedFiles()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(LogPath)!);
        var unrelated = LogPath + ".unrelated";
        File.WriteAllText(unrelated, "leave unchanged");
        var diagnostics = new FrontendDiagnostics(LogPath);
        for (var cycle = 0; cycle < 4; cycle++)
        {
            var line = $"cycle-{cycle}\n";
            File.WriteAllText(LogPath, string.Concat(Enumerable.Repeat(line, FrontendDiagnostics.MaxFileBytes / line.Length)), new UTF8Encoding(false));
            diagnostics.Log($"Synthetic event {cycle}");
        }

        Assert.Null(diagnostics.LastError);
        Assert.InRange(new FileInfo(LogPath).Length, 1, FrontendDiagnostics.MaxFileBytes);
        Assert.InRange(new FileInfo(LogPath + ".1").Length, 1, FrontendDiagnostics.MaxFileBytes);
        Assert.Contains("Synthetic event 3", File.ReadAllText(LogPath));
        Assert.Contains("cycle-3", File.ReadAllText(LogPath + ".1"));
        Assert.False(File.Exists(LogPath + ".2"));
        Assert.Equal("leave unchanged", File.ReadAllText(unrelated));
    }

    [Fact]
    public void OversizedPreviousFilesKeepTheirRecentTailWithinBounds()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(LogPath)!);
        var oldLog = string.Concat(Enumerable.Repeat("old entry\n", FrontendDiagnostics.MaxFileBytes / 10 + 1000)) + "recent entry\n";
        File.WriteAllText(LogPath, oldLog);
        File.WriteAllText(LogPath + ".1", oldLog);
        var diagnostics = new FrontendDiagnostics(LogPath);
        diagnostics.Log("New event");

        Assert.Null(diagnostics.LastError);
        Assert.InRange(new FileInfo(LogPath).Length, 1, FrontendDiagnostics.MaxFileBytes);
        Assert.InRange(new FileInfo(LogPath + ".1").Length, 1, FrontendDiagnostics.MaxFileBytes);
        Assert.Contains("recent entry", File.ReadAllText(LogPath + ".1"));
        Assert.Contains("New event", File.ReadAllText(LogPath));
    }

    [Fact]
    public async Task ConcurrentWritersPreserveCompleteEntries()
    {
        var diagnostics = new FrontendDiagnostics(LogPath);
        await Task.WhenAll(Enumerable.Range(0, 8).Select(worker => Task.Run(() =>
        {
            for (var item = 0; item < 50; item++)
            {
                diagnostics.Log($"Synthetic worker {worker} item {item}");
            }
        })));

        Assert.Null(diagnostics.LastError);
        var lines = File.ReadAllLines(LogPath);
        Assert.Equal(400, lines.Length);
        Assert.Equal(400, lines.Select(line => line[(line.IndexOf("Synthetic", StringComparison.Ordinal))..]).Distinct().Count());
        Assert.All(lines, line => Assert.Contains("Synthetic worker", line));
    }

    [Fact]
    public void ExceptionLoggingNeverReadsItsMessageStackOrToString()
    {
        var diagnostics = new FrontendDiagnostics(LogPath);
        diagnostics.LogException("Synthetic operation failed", new PrivateException());

        Assert.Null(diagnostics.LastError);
        var text = File.ReadAllText(LogPath);
        Assert.Contains(nameof(PrivateException), text);
        Assert.Contains("hresult=0x81234567", text);
        Assert.DoesNotContain("private", text.ToLowerInvariant().Replace(nameof(PrivateException).ToLowerInvariant(), ""));
    }

    [Fact]
    public void PersistenceFailureIsObservableWithoutItsSensitivePath()
    {
        var blocked = Path.Combine(_root, "private-user-path");
        File.WriteAllText(blocked, "keep unchanged");
        var diagnostics = new FrontendDiagnostics(Path.Combine(blocked, "log.txt"));

        diagnostics.Log("Synthetic startup entered");

        Assert.NotNull(diagnostics.LastError);
        Assert.Contains("type=", diagnostics.LastError);
        Assert.Contains("hresult=0x", diagnostics.LastError);
        Assert.DoesNotContain("private-user-path", diagnostics.LastError);
        Assert.Equal("keep unchanged", File.ReadAllText(blocked));
    }

    [Fact]
    public void NonFileBackupIsRejectedWithoutRemovingItsContents()
    {
        Directory.CreateDirectory(LogPath + ".1");
        var unrelated = Path.Combine(LogPath + ".1", "keep.txt");
        File.WriteAllText(unrelated, "keep unchanged");
        var diagnostics = new FrontendDiagnostics(LogPath);

        diagnostics.Log("Synthetic startup entered");

        Assert.NotNull(diagnostics.LastError);
        Assert.Equal("keep unchanged", File.ReadAllText(unrelated));
        Assert.False(File.Exists(LogPath));
    }

    [Fact]
    public void LinkedLogIsRejectedWithoutWritingItsTarget()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(LogPath)!);
        var target = Path.Combine(_root, "unrelated.txt");
        File.WriteAllText(target, "keep unchanged");
        File.CreateSymbolicLink(LogPath, target);
        var diagnostics = new FrontendDiagnostics(LogPath);

        diagnostics.Log("Synthetic startup entered");

        Assert.NotNull(diagnostics.LastError);
        Assert.Equal("keep unchanged", File.ReadAllText(target));
    }

    [Fact]
    public void LinkedDirectoryIsRejectedWithoutCreatingLogsInItsTarget()
    {
        var target = Path.Combine(_root, "other-directory");
        Directory.CreateDirectory(target);
        Directory.CreateSymbolicLink(Path.GetDirectoryName(LogPath)!, target);
        var diagnostics = new FrontendDiagnostics(LogPath);

        diagnostics.Log("Synthetic startup entered");

        Assert.NotNull(diagnostics.LastError);
        Assert.Empty(Directory.EnumerateFileSystemEntries(target));
    }

    public void Dispose() => Directory.Delete(_root, recursive: true);

    private sealed class PrivateException : Exception
    {
        internal PrivateException() : base("private-message") => HResult = unchecked((int)0x81234567);
        public override string Message => throw new InvalidOperationException("Message must not be read");
        public override string? StackTrace => throw new InvalidOperationException("StackTrace must not be read");
        public override string ToString() => throw new InvalidOperationException("ToString must not be read");
    }
}
