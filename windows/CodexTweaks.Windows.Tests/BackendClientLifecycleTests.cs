using System.Diagnostics;
using System.Reflection;
using CodexTweaks.Windows.Services;
using Xunit;

namespace CodexTweaks.Windows.Tests;

public sealed class BackendClientLifecycleTests
{
    [Fact]
    public async Task RepeatedDisposeRetainsUnconfirmedCleanupAfterProcessIsReleased()
    {
        using var process = new Process
        {
            StartInfo = new ProcessStartInfo("cmd.exe", "/c exit /b 1")
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardInput = true,
            },
        };
        Assert.True(process.Start());
        await process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(5));
        var client = new BackendClient();
        typeof(BackendClient).GetField("_process", BindingFlags.Instance | BindingFlags.NonPublic)!
            .SetValue(client, process);
        var first = client.DisposeAsync().AsTask();
        await Assert.ThrowsAsync<InvalidOperationException>(() => first);
        var repeated = client.DisposeAsync().AsTask();
        await Assert.ThrowsAsync<InvalidOperationException>(() => repeated);
        Assert.Same(first, repeated);
    }

    [Fact]
    public async Task NeverStartedClientCanDisposeRepeatedlyButCannotRestart()
    {
        var client = new BackendClient();
        await client.DisposeAsync();
        await client.DisposeAsync();
        await Assert.ThrowsAsync<ObjectDisposedException>(() => client.StartAsync());
    }
}
