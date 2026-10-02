using System.Collections.Concurrent;
using System.Diagnostics;
using System.Reflection;
using System.Text;
using System.Text.Json;
using CodexTweaks.Windows.Generated;
using CodexTweaks.Windows.Models;
using Windows.System.UserProfile;

namespace CodexTweaks.Windows.Services;

internal sealed class BackendClient : IAsyncDisposable
{
    internal const int ProtocolVersion = BackendProtocolContract.ProtocolVersion;
    private static readonly Encoding Utf8NoBom = new UTF8Encoding(encoderShouldEmitUTF8Identifier: false);

    private readonly ConcurrentDictionary<long, TaskCompletionSource<JsonElement>> _pending = new();
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly JsonSerializerOptions _json = new()
    {
        PropertyNameCaseInsensitive = true,
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
    };
    private Process? _process;
    private long _nextId;
    private Task? _stdoutTask;
    private Task? _stderrTask;
    private volatile bool _stopping;

    internal event Action<BackendAppSnapshot>? SnapshotChanged;
    internal event Action<string>? BackendFailed;

    internal async Task<BackendAppSnapshot> StartAsync()
    {
        if (_process is not null)
        {
            return await RequestAsync<BackendAppSnapshot>("getState", null);
        }

        var executable = Environment.GetEnvironmentVariable(ApplicationIdentity.EnvironmentPrefix + "BACKEND_PATH");
        if (string.IsNullOrWhiteSpace(executable))
        {
            executable = Path.Combine(AppContext.BaseDirectory, "codex-tweaks-backend.exe");
        }
        if (!File.Exists(executable))
        {
            throw new FileNotFoundException(
                PresentationFallback.Text(PresentationTextKey.AppBackendMissing),
                executable);
        }

        var start = new ProcessStartInfo(executable)
        {
            WorkingDirectory = AppContext.BaseDirectory,
            UseShellExecute = false,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            CreateNoWindow = true,
            WindowStyle = ProcessWindowStyle.Hidden,
            StandardInputEncoding = Utf8NoBom,
            StandardOutputEncoding = Utf8NoBom,
            StandardErrorEncoding = Utf8NoBom,
        };
        _process = new Process { StartInfo = start, EnableRaisingEvents = true };
        _process.Exited += (_, _) => HandleExit();
        if (!_process.Start())
        {
            _process.Dispose();
            _process = null;
            throw new InvalidOperationException(
                PresentationFallback.Text(PresentationTextKey.AppBackendNotRunning));
        }

        _stdoutTask = ReadStdoutAsync(_process);
        _stderrTask = ReadStderrAsync(_process);

        try
        {
            return await InitializeBackendAsync();
        }
        catch
        {
            try
            {
                await DisposeAsync();
            }
            catch (Exception exception)
            {
                App.LogException("Failed startup cleanup was not confirmed", exception);
            }
            throw;
        }
    }

    private async Task<BackendAppSnapshot> InitializeBackendAsync()
    {
        var ping = await RequestAsync<BackendPing>("ping", null);
        if (ping.ProtocolVersion != ProtocolVersion || ping.Backend != "go")
        {
            throw new InvalidOperationException(
                PresentationFallback.Text(PresentationTextKey.AppProtocolMismatch));
        }

        var local = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        var applicationSupport = Environment.GetEnvironmentVariable(ApplicationIdentity.EnvironmentPrefix + "APPLICATION_SUPPORT") ?? local;
        var cache = Environment.GetEnvironmentVariable(ApplicationIdentity.EnvironmentPrefix + "CACHE_DIRECTORY") ?? local;
        var informational = Assembly.GetExecutingAssembly()
            .GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion;
        var version = informational?.Split('+')[0] ?? "0.0.0-dev";
        var buildNumber = Assembly.GetExecutingAssembly()
            .GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(attribute => attribute.Key == "CodexTweaksBuildNumber")?.Value ?? "1";
        return await RequestAsync<BackendAppSnapshot>(
            "initialize",
            new
            {
                applicationSupportDirectory = applicationSupport,
                cacheDirectory = cache,
                bundledPackagesDirectory = Path.Combine(AppContext.BaseDirectory, "Tweaks", "packages"),
                skillPath = Path.Combine(AppContext.BaseDirectory, "Skills", "develop-codex-tweaks-package", "SKILL.md"),
                preferredLanguages = GlobalizationPreferences.Languages,
                currentVersion = version,
                buildNumber,
            });
    }

    internal async Task SendAsync(string method, object? parameters = null)
    {
        _ = await RequestAsync<JsonElement>(method, parameters);
    }

    internal async Task<BackendAppSnapshot> CheckAppUpdateAsync(bool startCheck)
    {
        if (startCheck)
        {
            await SendAsync("checkAppUpdate", new { prompt = false });
        }

        var startedAt = Stopwatch.GetTimestamp();
        while (true)
        {
            var snapshot = await RequestAsync<BackendAppSnapshot>("getState", null);
            if (!snapshot.Update.Checking)
            {
                return snapshot;
            }
            if (Stopwatch.GetElapsedTime(startedAt) >= TimeSpan.FromSeconds(45))
            {
                throw new TimeoutException(
                    PresentationFallback.Text(PresentationTextKey.AppBackendRequestFailed));
            }
            await Task.Delay(250);
        }
    }

    internal async Task<T> RequestAsync<T>(string method, object? parameters, TimeSpan? timeout = null)
    {
        var process = _process ?? throw new InvalidOperationException(
            PresentationFallback.Text(PresentationTextKey.AppBackendNotRunning));
        var id = Interlocked.Increment(ref _nextId);
        var completion = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        if (!_pending.TryAdd(id, completion))
        {
            throw new InvalidOperationException(
                PresentationFallback.Text(PresentationTextKey.AppBackendRequestCreateFailed));
        }

        try
        {
            using var deadline = new CancellationTokenSource(timeout ?? TimeSpan.FromMinutes(3));
            var payload = JsonSerializer.Serialize(new { id, method, @params = parameters }, _json);
            await _writeLock.WaitAsync(deadline.Token);
            try
            {
                await process.StandardInput.WriteLineAsync(payload.AsMemory(), deadline.Token);
                await process.StandardInput.FlushAsync(deadline.Token);
            }
            finally
            {
                _writeLock.Release();
            }

            var result = await completion.Task.WaitAsync(deadline.Token);
            if (typeof(T) == typeof(JsonElement))
            {
                return (T)(object)result;
            }
            return result.Deserialize<T>(_json)
                ?? throw new InvalidOperationException(
                    PresentationFallback.Text(PresentationTextKey.AppBackendMalformed));
        }
        finally
        {
            _pending.TryRemove(id, out _);
        }
    }

    internal async Task<string> ReadAuthoringPromptAsync()
    {
        return await RequestAsync<string>("readAuthoringPrompt", null);
    }

    private async Task ReadStdoutAsync(Process process)
    {
        try
        {
            while (await process.StandardOutput.ReadLineAsync() is { } line)
            {
                using var document = JsonDocument.Parse(line);
                var root = document.RootElement;
                if (root.TryGetProperty("event", out var eventName)
                    && eventName.GetString() == "state"
                    && root.TryGetProperty("data", out var data))
                {
                    var snapshot = data.Deserialize<BackendAppSnapshot>(_json);
                    if (snapshot is not null)
                    {
                        SnapshotChanged?.Invoke(snapshot);
                    }
                    continue;
                }
                if (!root.TryGetProperty("id", out var idElement))
                {
                    continue;
                }
                var id = idElement.GetInt64();
                if (!_pending.TryGetValue(id, out var completion))
                {
                    continue;
                }
                if (root.TryGetProperty("error", out var error))
                {
                    var message = error.TryGetProperty("message", out var value)
                        ? value.GetString()
                        : PresentationFallback.Text(PresentationTextKey.AppBackendRequestFailed);
                    completion.TrySetException(new InvalidOperationException(message));
                }
                else if (root.TryGetProperty("result", out var result))
                {
                    completion.TrySetResult(result.Clone());
                }
                else
                {
                    completion.TrySetException(new InvalidOperationException(
                        PresentationFallback.Text(PresentationTextKey.AppBackendMalformed)));
                }
            }
        }
        catch (Exception exception)
        {
            App.LogException("Backend stdout failed", exception);
            FailPending(exception);
        }
    }

    private static async Task ReadStderrAsync(Process process)
    {
        // Drain untrusted diagnostics in fixed chunks. A newline-free stream
        // must not accumulate in memory or expose external output in our log.
        var buffer = new char[4096];
        long characters = 0;
        try
        {
            int count;
            while ((count = await process.StandardError.ReadAsync(buffer.AsMemory())) != 0)
            {
                characters += count;
            }
        }
        catch (Exception exception)
        {
            App.LogException("Backend stderr drain failed", exception);
        }
        if (characters != 0)
        {
            App.Log($"Backend diagnostics received: {characters} characters.");
        }
    }

    private void HandleExit()
    {
        if (_stopping)
        {
            return;
        }
        var code = _process?.ExitCode ?? -1;
        var exception = new InvalidOperationException(PresentationFallback.Text(
            PresentationTextKey.AppBackendTerminated,
            ("status", code.ToString())));
        FailPending(exception);
        BackendFailed?.Invoke(exception.Message);
    }

    private void FailPending(Exception exception)
    {
        foreach (var completion in _pending.Values)
        {
            completion.TrySetException(exception);
        }
    }

    public async ValueTask DisposeAsync()
    {
        var process = _process;
        if (process is null)
        {
            return;
        }
        _stopping = true;
        var startedAt = Stopwatch.GetTimestamp();
        var grace = TimeSpan.FromSeconds(BackendProtocolContract.ShutdownGraceSeconds);
        var cleanupConfirmed = false;
        var forced = false;
        try
        {
            if (!process.HasExited)
            {
                var result = await RequestAsync<ShutdownResult>("shutdown", null, grace);
                cleanupConfirmed = result.Shutdown;
            }
        }
        catch (Exception exception)
        {
            App.LogException("Backend cleanup was not confirmed", exception);
        }
        try
        {
            process.StandardInput.Close();
            var remaining = grace - Stopwatch.GetElapsedTime(startedAt);
            if (!process.HasExited && remaining > TimeSpan.Zero)
            {
                using var deadline = new CancellationTokenSource(remaining);
                await process.WaitForExitAsync(deadline.Token);
            }
        }
        catch (Exception exception)
        {
            App.LogException("Backend did not exit within its shutdown budget", exception);
        }
        _process = null;
        FailPending(new InvalidOperationException(PresentationFallback.Text(PresentationTextKey.AppBackendNotRunning)));
        if (!process.HasExited)
        {
            forced = true;
            // The official application may have been launched by this sidecar;
            // terminating a descendant tree would close user-owned work.
            try
            {
                process.Kill(entireProcessTree: false);
                App.Log("Private backend terminated after its shutdown budget.");
            }
            catch (Exception exception)
            {
                App.LogException("Private backend termination failed", exception);
            }
        }
        try
        {
            await Task.WhenAll(_stdoutTask ?? Task.CompletedTask, _stderrTask ?? Task.CompletedTask)
                .WaitAsync(TimeSpan.FromSeconds(1)).ConfigureAwait(false);
        }
        catch (Exception exception)
        {
            App.LogException("Backend output drain was not confirmed", exception);
        }
        var exitedCleanly = process.HasExited && process.ExitCode == 0;
        process.Dispose();
        _writeLock.Dispose();
        if (!cleanupConfirmed || !exitedCleanly || forced)
        {
            throw new InvalidOperationException(PresentationFallback.Text(PresentationTextKey.AppBackendShutdownIncomplete));
        }
    }
}
