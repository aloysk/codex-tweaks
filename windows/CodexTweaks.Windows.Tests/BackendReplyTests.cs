using System.Text.Json;
using CodexTweaks.Windows.Generated;
using Xunit;

namespace CodexTweaks.Windows.Tests;

public sealed class BackendReplyTests
{
    [Theory]
    [InlineData("{}")]
    [InlineData("{\"accepted\":true}")]
    [InlineData("{\"shutdown\":false}")]
    public void MissingCleanupConfirmationIsNotAccepted(string json)
    {
        Assert.False(JsonSerializer.Deserialize<ShutdownResult>(json)!.Shutdown);
    }

    [Fact]
    public void ConfirmedShutdownIsAccepted()
    {
        Assert.True(JsonSerializer.Deserialize<ShutdownResult>("{\"shutdown\":true}")!.Shutdown);
    }

    [Fact]
    public void MalformedConfirmationIsRejected()
    {
        Assert.Throws<JsonException>(() => JsonSerializer.Deserialize<ShutdownResult>("{\"shutdown\":\"true\"}"));
    }
}
