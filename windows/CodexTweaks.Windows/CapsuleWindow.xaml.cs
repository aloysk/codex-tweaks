using System.Runtime.InteropServices;
using CodexTweaks.Windows.Generated;
using CodexTweaks.Windows.Models;
using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Input;
using Windows.Graphics;
using WinRT.Interop;

namespace CodexTweaks.Windows;

// This is a view of MainWindow's backend. It never observes Codex, interprets
// account data or creates another sidecar. Passive rendering cannot activate it.
public sealed partial class CapsuleWindow : Window
{
    private readonly MainWindow _host;
    private readonly AppWindow _window;
    private readonly nint _handle;
    private readonly SubclassCallback _callback;
    private bool _visible;
    private bool _closed;
    private bool _positioned;
    private bool _clamping;

    internal CapsuleWindow(MainWindow host)
    {
        InitializeComponent();
        _host = host;
        _handle = WindowNative.GetWindowHandle(this);
        _window = AppWindow.GetFromWindowId(Win32Interop.GetWindowIdFromWindow(_handle));
        if (_window.Presenter is OverlappedPresenter presenter)
        {
            presenter.SetBorderAndTitleBar(false, false);
            presenter.IsAlwaysOnTop = false;
            presenter.IsResizable = false;
            presenter.IsMaximizable = false;
            presenter.IsMinimizable = false;
        }
        _callback = WindowMessage;
        var style = GetWindowLongPtr(_handle, -20).ToInt64();
        SetWindowLongPtr(_handle, -20, (nint)(style | 0x08000000 | 0x00000080));
        if (!SetWindowSubclass(_handle, _callback, 1, 0))
        {
            Close();
            throw new InvalidOperationException("Cannot establish capsule focus protection.");
        }
        _window.Changed += WindowChanged;
        Closed += (_, _) =>
        {
            _closed = true;
            _window.Changed -= WindowChanged;
            RemoveWindowSubclass(_handle, _callback, 1);
        };
    }

    internal void Render(BackendAppSnapshot snapshot)
    {
        if (_closed) return;
        Title = _host.Text(PresentationTextKey.CapsuleTitle);
        Summary.Text = snapshot.Signals.Quota.Value;
        Source.Text = snapshot.Signals.Quota.Detail;
        Source.Visibility = snapshot.Signals.Capsule.Collapsed ? Visibility.Collapsed : Visibility.Visible;
        OpenPanel.Content = _host.Text(PresentationTextKey.CapsuleOpenPanel);
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(DragArea,
            snapshot.Signals.Quota.Label + ": " + Summary.Text + ". " + Source.Text);
        var dpi = Math.Max(1, GetDpiForWindow(_handle) / 96.0);
        var width = snapshot.Signals.Capsule.Collapsed
            ? snapshot.Presentation.Tokens.CapsuleCollapsedWidth
            : snapshot.Presentation.Tokens.CapsuleWidth;
        _window.Resize(new SizeInt32((int)Math.Ceiling(width * dpi),
            (int)Math.Ceiling(snapshot.Presentation.Tokens.CapsuleHeight * dpi)));
        ClampToWorkArea(reset: !_positioned);
        if (snapshot.Signals.Capsule.Enabled && !_visible)
        {
            _window.Show(activateWindow: false);
            _visible = true;
        }
        else if (!snapshot.Signals.Capsule.Enabled && _visible) Hide();
    }

    internal void Hide() { if (_closed) return; _window.Hide(); _visible = false; }
    internal void ResetPosition() => ClampToWorkArea(reset: true);

    private void ClampToWorkArea(bool reset = false)
    {
        if (_closed || _clamping) return;
        _clamping = true;
        try
        {
            var area = DisplayArea.GetFromWindowId(_window.Id, DisplayAreaFallback.Primary).WorkArea;
            var size = _window.Size;
            var position = _window.Position;
            var x = reset ? area.X + area.Width - size.Width - 16 : position.X;
            var y = reset ? area.Y + area.Height - size.Height - 16 : position.Y;
            _window.Move(new PointInt32(
                Math.Clamp(x, area.X, Math.Max(area.X, area.X + area.Width - size.Width)),
                Math.Clamp(y, area.Y, Math.Max(area.Y, area.Y + area.Height - size.Height))));
            _positioned = true;
        }
        finally { _clamping = false; }
    }

    private void WindowChanged(AppWindow sender, AppWindowChangedEventArgs args)
    {
        if (args.DidPositionChange || args.DidSizeChange) ClampToWorkArea();
    }

    private void DragArea_PointerPressed(object sender, PointerRoutedEventArgs args)
    {
        if (!args.GetCurrentPoint(DragArea).Properties.IsLeftButtonPressed) return;
        ReleaseCapture();
        SendMessage(_handle, 0x00A1, 2, 0);
        ClampToWorkArea();
        args.Handled = true;
    }

    private void OpenPanel_Click(object sender, RoutedEventArgs args) => _host.ShowFromTray();

    private nint WindowMessage(nint handle, uint message, nuint wParam, nint lParam, nuint id, nuint data)
    {
        if (message == 0x0021) return 3; // MA_NOACTIVATE; still deliver the click.
        if (message is 0x02E0 or 0x007E)
            DispatcherQueue.TryEnqueue(() => { if (!_closed) { if (_host.CurrentSnapshot is { } snapshot) Render(snapshot); ClampToWorkArea(); } });
        return DefSubclassProc(handle, message, wParam, lParam);
    }

    private delegate nint SubclassCallback(nint handle, uint message, nuint wParam, nint lParam, nuint id, nuint data);
    [DllImport("user32.dll", EntryPoint = "GetWindowLongPtrW")] private static extern nint GetWindowLongPtr(nint handle, int index);
    [DllImport("user32.dll", EntryPoint = "SetWindowLongPtrW")] private static extern nint SetWindowLongPtr(nint handle, int index, nint value);
    [DllImport("user32.dll")] private static extern uint GetDpiForWindow(nint handle);
    [DllImport("user32.dll")] private static extern bool ReleaseCapture();
    [DllImport("user32.dll", EntryPoint = "SendMessageW")] private static extern nint SendMessage(nint handle, uint message, nuint wParam, nint lParam);
    [DllImport("comctl32.dll")] private static extern bool SetWindowSubclass(nint handle, SubclassCallback callback, nuint id, nuint data);
    [DllImport("comctl32.dll")] private static extern bool RemoveWindowSubclass(nint handle, SubclassCallback callback, nuint id);
    [DllImport("comctl32.dll")] private static extern nint DefSubclassProc(nint handle, uint message, nuint wParam, nint lParam);
}
