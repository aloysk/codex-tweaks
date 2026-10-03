using CodexTweaks.Windows.Generated;
using CodexTweaks.Windows.Models;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace CodexTweaks.Windows.Pages;

internal sealed class SignalsView : StackPanel
{
    private MainWindow? _host;
    private BackendAppSnapshot? _snapshot;
    private bool _rendering;
    private readonly TextBlock _title = new() { FontSize = 20, FontWeight = Microsoft.UI.Text.FontWeights.SemiBold };
    private readonly TextBlock _task = new() { TextWrapping = TextWrapping.Wrap };
    private readonly TextBlock _rate = new() { TextWrapping = TextWrapping.Wrap };
    private readonly TextBlock _quota = new() { TextWrapping = TextWrapping.Wrap };
    private readonly TextBlock _details = new() { TextWrapping = TextWrapping.Wrap, IsTextSelectionEnabled = true };
    private readonly Expander _sources = new() { HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly ToggleSwitch _capsule = new();
    private readonly Button _collapse = new();
    private readonly Button _reset = new();

    internal SignalsView()
    {
        Spacing = 10;
        Children.Add(_title);
        Children.Add(_task);
        Children.Add(_rate);
        Children.Add(_quota);
        _sources.Content = _details;
        Children.Add(_sources);
        Children.Add(_capsule);
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        actions.Children.Add(_collapse);
        actions.Children.Add(_reset);
        Children.Add(actions);
        _capsule.Toggled += async (_, _) =>
        {
            if (_rendering || _host is null || _snapshot is null) return;
            await _host.RunBackendAsync("setCapsule", new { enabled = _capsule.IsOn, collapsed = _snapshot.Signals.Capsule.Collapsed });
        };
        _collapse.Click += async (_, _) =>
        {
            if (_host is null || _snapshot is null) return;
            await _host.RunBackendAsync("setCapsule", new { enabled = _snapshot.Signals.Capsule.Enabled, collapsed = !_snapshot.Signals.Capsule.Collapsed });
        };
        _reset.Click += (_, _) => _host?.ResetCapsulePosition();
    }

    internal void Render(MainWindow host, BackendAppSnapshot snapshot)
    {
        _host = host;
        _snapshot = snapshot;
        _rendering = true;
        try
        {
            _title.Text = host.Text(PresentationTextKey.SignalsTitle);
            _task.Text = snapshot.Signals.Task.Label + ": " + snapshot.Signals.Task.Value;
            _rate.Text = snapshot.Signals.Rate.Label + ": " + snapshot.Signals.Rate.Value;
            _quota.Text = snapshot.Signals.Quota.Label + ": " + snapshot.Signals.Quota.Value;
            _sources.Header = host.Text(PresentationTextKey.SignalsSources);
            var quota = snapshot.Signals.Quota;
            _details.Text = string.Join("\n", new[] {
                snapshot.Signals.Task.Detail, snapshot.Signals.Rate.Detail, quota.Detail, quota.Source,
                host.Text(PresentationTextKey.SignalsObservedAt) + ": " + quota.ObservedAt,
                host.Text(PresentationTextKey.SignalsCacheUpdatedAt) + ": " + quota.CacheUpdatedAt,
                host.Text(PresentationTextKey.SignalsSourceUpdatedAt) + ": " + quota.SourceUpdatedAt,
            }.Concat(snapshot.Signals.Windows.Select(window => window.Title + " · " + window.Remaining + " · " + window.ResetAt)));
            _capsule.Header = host.Text(PresentationTextKey.CapsuleShow);
            _capsule.IsOn = snapshot.Signals.Capsule.Enabled;
            _collapse.Content = host.Text(snapshot.Signals.Capsule.Collapsed ? PresentationTextKey.CapsuleExpand : PresentationTextKey.CapsuleCollapse);
            _reset.Content = host.Text(PresentationTextKey.CapsuleResetPosition);
            _collapse.IsEnabled = snapshot.Signals.Capsule.Enabled;
            _reset.IsEnabled = snapshot.Signals.Capsule.Enabled;
        }
        finally { _rendering = false; }
    }

    internal void Clear(MainWindow host)
    {
        _snapshot = null;
        _task.Text = host.Text(PresentationTextKey.SignalsTask) + ": " + host.Text(PresentationTextKey.SignalsUnavailable);
        _rate.Text = host.Text(PresentationTextKey.SignalsRate) + ": " + host.Text(PresentationTextKey.SignalsUnavailable);
        _quota.Text = host.Text(PresentationTextKey.SignalsQuota) + ": " + host.Text(PresentationTextKey.SignalsUnavailable);
        _details.Text = host.Text(PresentationTextKey.AppBackendNotRunning);
        _capsule.IsEnabled = false;
        _collapse.IsEnabled = false;
        _reset.IsEnabled = false;
    }
}
