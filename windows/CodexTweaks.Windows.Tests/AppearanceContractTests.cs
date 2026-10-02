using System.Text.Json;
using CodexTweaks.Windows.Generated;
using Xunit;

namespace CodexTweaks.Windows.Tests;

public sealed class AppearanceContractTests
{
    [Fact]
    public void SnapshotKeepsNullableAssetsAndExactRevision()
    {
        const string json = """
            {"saved":{"theme":"native","readingLayout":"native","backgroundMode":"off","solidColor":"#DEF3E5","imageAssetId":null,"overlayOpacity":88},
             "preview":null,"status":"unavailable","statusTextKey":"appearance.status.unavailable","errorTextKey":null,"targetId":null,
             "revision":9223372036854775809,"actions":{"preview":false,"apply":false,"cancelPreview":false,"restoreNative":false,"importImage":true},
             "options":{"themes":[{"value":"native","textKey":"appearance.theme.native","supported":true}],"readingLayouts":[],"backgroundModes":[],"overlayMinimum":50,"overlayMaximum":100}}
            """;
        var snapshot = JsonSerializer.Deserialize<AppearanceSnapshot>(json)!;
        Assert.Null(snapshot.Saved.ImageAssetId);
        Assert.Null(snapshot.Preview);
        Assert.Equal(9223372036854775809UL, snapshot.Revision);
        Assert.False(snapshot.Actions.Apply);
        Assert.Equal("appearance.theme.native", snapshot.Options.Themes.Single().TextKey);
    }

    [Fact]
    public void SettingsEnvelopeMatchesGoWireNamesAndOmitsSourcePath()
    {
        using var payload = JsonDocument.Parse(JsonSerializer.Serialize(new { settings = PresentationDefaults.Appearance }));
        var settings = payload.RootElement.GetProperty("settings");
        Assert.Equal(new[] { "backgroundMode", "imageAssetId", "overlayOpacity", "readingLayout", "solidColor", "theme" },
            settings.EnumerateObject().Select(property => property.Name).Order().ToArray());
        Assert.Equal(JsonValueKind.Null, settings.GetProperty("imageAssetId").ValueKind);
        Assert.Equal(88, settings.GetProperty("overlayOpacity").GetInt32());
        Assert.Equal("#DEF3E5", settings.GetProperty("solidColor").GetString());
    }
}
