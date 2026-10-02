using System.Globalization;
using CodexTweaks.Windows.Generated;
using CodexTweaks.Windows.Models;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace CodexTweaks.Windows.Pages;

public sealed partial class AppearancePage : Page
{
    private MainWindow? _host;
    private BackendAppSnapshot? _snapshot;
    private AppearanceSettings _draft = PresentationDefaults.Appearance;
    private AppearanceImageResult? _image;
    private bool _dirty;
    private bool _rendering;
    private int _pendingCommands;
    private int _commandVersion;
    private bool Busy => _pendingCommands > 0;
    private string? _requestError;

    public AppearancePage() => InitializeComponent();

    internal void Render(MainWindow host, BackendAppSnapshot snapshot)
    {
        _host = host;
        _snapshot = snapshot;
        var appearance = snapshot.Appearance;
        if (!_dirty && !Busy)
        {
            _draft = appearance.Preview ?? appearance.Saved;
        }
        _rendering = true;
        try
        {
            PageTitle.Text = host.Text(PresentationTextKey.AppearanceTitle);
            PageSubtitle.Text = host.Text(PresentationTextKey.AppearanceSubtitle);
            StatusTitle.Text = host.Text(appearance.StatusTextKey);
            TargetText.Text = appearance.TargetId is { Length: > 0 } target
                ? host.Text(PresentationTextKey.AppearanceTarget, ("target", target))
                : host.Text(PresentationTextKey.AppearanceTargetUnconfirmed);
            StatusDetail.Text = appearance.ErrorTextKey is { } errorKey
                ? host.Text(errorKey)
                : snapshot.Presentation.Status.Detail;
            PopulatePicker(ThemePicker, appearance.Options.Themes, _draft.Theme, PresentationTextKey.AppearanceTheme);
            PopulatePicker(LayoutPicker, appearance.Options.ReadingLayouts, _draft.ReadingLayout, PresentationTextKey.AppearanceLayout);
            PopulatePicker(BackgroundPicker, appearance.Options.BackgroundModes, _draft.BackgroundMode, PresentationTextKey.AppearanceBackground);
            LayoutHint.Text = host.Text(PresentationTextKey.AppearanceLayoutUnavailable);
            LayoutHint.Visibility = appearance.Options.ReadingLayouts.Any(option => !option.Supported)
                ? Visibility.Visible : Visibility.Collapsed;
            ColorInput.Header = host.Text(PresentationTextKey.AppearanceSolidColor);
            ColorInput.Text = _draft.SolidColor;
            ImageTitle.Text = host.Text(PresentationTextKey.AppearanceImage);
            ChooseImageButton.Content = host.Text(PresentationTextKey.AppearanceChooseImage);
            ImageHint.Text = host.Text(PresentationTextKey.AppearanceImageLimits);
            ImageInfo.Text = _draft.ImageAssetId is null
                ? host.Text(PresentationTextKey.AppearanceImageNone)
                : _image is { } image
                    ? host.Text(PresentationTextKey.AppearanceImageImported,
                        ("width", image.Width.ToString(CultureInfo.InvariantCulture)),
                        ("height", image.Height.ToString(CultureInfo.InvariantCulture)),
                        ("format", image.Format))
                    : host.Text(PresentationTextKey.AppearanceImageSelected);
            OpacityTitle.Text = host.Text(PresentationTextKey.AppearanceOverlayOpacity);
            AutomationProperties.SetName(OpacitySlider, OpacityTitle.Text);
            OpacitySlider.Minimum = appearance.Options.OverlayMinimum;
            OpacitySlider.Maximum = appearance.Options.OverlayMaximum;
            OpacitySlider.Value = _draft.OverlayOpacity;
            OpacityValue.Text = host.Text(PresentationTextKey.AppearanceOpacityValue,
                ("value", _draft.OverlayOpacity.ToString(CultureInfo.InvariantCulture)));
            DraftHint.Text = host.Text(_dirty ? PresentationTextKey.AppearanceLocalDraft
                : appearance.Preview is not null ? PresentationTextKey.AppearancePreviewOnly
                : PresentationTextKey.AppearanceSavedSettings);
            RequestError.Text = _requestError is null ? string.Empty : host.Text(_requestError);
            RequestError.Visibility = _requestError is null ? Visibility.Collapsed : Visibility.Visible;
            PreviewButton.Content = host.Text(PresentationTextKey.AppearancePreview);
            ApplyButton.Content = host.Text(PresentationTextKey.AppearanceApply);
            CancelButton.Content = host.Text(PresentationTextKey.AppearanceCancelPreview);
            RestoreButton.Content = host.Text(PresentationTextKey.AppearanceRestoreNative);
            PreviewButton.IsEnabled = appearance.Actions.Preview && !Busy;
            ApplyButton.IsEnabled = appearance.Actions.Apply && !Busy;
            CancelButton.IsEnabled = appearance.Actions.CancelPreview;
            RestoreButton.IsEnabled = appearance.Actions.RestoreNative && !Busy;
            ChooseImageButton.IsEnabled = appearance.Actions.ImportImage && !Busy;
            ThemePicker.IsEnabled = LayoutPicker.IsEnabled = BackgroundPicker.IsEnabled = ColorInput.IsEnabled = OpacitySlider.IsEnabled = !Busy;
        }
        finally
        {
            _rendering = false;
        }
    }

    private void PopulatePicker(ComboBox picker, List<AppearanceOption> options, string selected, string labelKey)
    {
        picker.Header = Host.Text(labelKey);
        AutomationProperties.SetName(picker, Host.Text(labelKey));
        picker.Items.Clear();
        foreach (var option in options)
        {
            var item = new ComboBoxItem
            {
                Content = Host.Text(option.TextKey),
                Tag = option.Value,
                IsEnabled = option.Supported,
            };
            picker.Items.Add(item);
            if (option.Value == selected)
            {
                picker.SelectedItem = item;
            }
        }
    }

    private void UpdateDraft()
    {
        if (_rendering || _host is null || _snapshot is null)
        {
            return;
        }
        _draft = new AppearanceSettings
        {
            Theme = (ThemePicker.SelectedItem as ComboBoxItem)?.Tag as string ?? _draft.Theme,
            ReadingLayout = (LayoutPicker.SelectedItem as ComboBoxItem)?.Tag as string ?? _draft.ReadingLayout,
            BackgroundMode = (BackgroundPicker.SelectedItem as ComboBoxItem)?.Tag as string ?? _draft.BackgroundMode,
            SolidColor = ColorInput.Text,
            ImageAssetId = _draft.ImageAssetId,
            OverlayOpacity = (int)OpacitySlider.Value,
        };
        _dirty = true;
        _requestError = null;
        Render(Host, _snapshot);
    }

    private void Option_SelectionChanged(object sender, SelectionChangedEventArgs e) => UpdateDraft();
    private void ColorInput_TextChanged(object sender, TextChangedEventArgs e) => UpdateDraft();
    private void OpacitySlider_ValueChanged(object sender, RangeBaseValueChangedEventArgs e) => UpdateDraft();

    private async void ChooseImage_Click(object sender, RoutedEventArgs e)
    {
        if (Busy || _snapshot is null)
        {
            return;
        }
        _pendingCommands++;
        Render(Host, _snapshot);
        try
        {
            if (await Host.PickAppearanceImageAsync() is not { } image)
            {
                return;
            }
            _image = image;
            _draft = new AppearanceSettings
            {
                Theme = _draft.Theme, ReadingLayout = _draft.ReadingLayout,
                BackgroundMode = _draft.BackgroundMode, SolidColor = _draft.SolidColor,
                ImageAssetId = image.AssetId, OverlayOpacity = _draft.OverlayOpacity,
            };
            _dirty = true;
            _requestError = null;
        }
        catch (Exception exception)
        {
            App.LogException("Appearance image import failed", exception);
            _requestError = Host.CurrentSnapshot?.Appearance.ErrorTextKey ?? PresentationTextKey.AppearanceRequestFailed;
        }
        finally
        {
            _pendingCommands--;
            Render(Host, _snapshot);
        }
    }

    private async Task RunAsync(string method, bool includeSettings, bool allowQueuedCancel = false)
    {
        if ((Busy && !allowQueuedCancel) || _snapshot is null)
        {
            return;
        }
        var commandVersion = ++_commandVersion;
        _pendingCommands++;
        Render(Host, _snapshot);
        try
        {
            var result = await Host.RunAppearanceAsync(method, includeSettings ? new { settings = _draft } : null);
            if (commandVersion != _commandVersion)
            {
                return;
            }
            _draft = result.Preview ?? result.Saved;
            _dirty = false;
            _requestError = null;
            if (_image?.AssetId != _draft.ImageAssetId)
            {
                _image = null;
            }
        }
        catch (Exception exception)
        {
            App.LogException("Appearance command failed", exception);
            if (commandVersion == _commandVersion)
            {
                _requestError = Host.CurrentSnapshot?.Appearance.ErrorTextKey ?? PresentationTextKey.AppearanceRequestFailed;
            }
        }
        finally
        {
            _pendingCommands--;
            Render(Host, Host.Snapshot);
        }
    }

    private async void Preview_Click(object sender, RoutedEventArgs e) => await RunAsync("appearance.preview", true);
    private async void Apply_Click(object sender, RoutedEventArgs e) => await RunAsync("appearance.apply", true);
    private async void Cancel_Click(object sender, RoutedEventArgs e) => await RunAsync("appearance.cancelPreview", false, true);
    private async void Restore_Click(object sender, RoutedEventArgs e) => await RunAsync("appearance.restoreNative", false);
    private MainWindow Host => _host ?? throw new InvalidOperationException("Appearance page is not attached.");
}
