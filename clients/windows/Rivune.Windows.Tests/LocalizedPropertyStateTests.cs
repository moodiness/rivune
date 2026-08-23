using Rivune.App;
using Xunit;

namespace Rivune.Windows.Tests;

public sealed class LocalizedPropertyStateTests
{
    [Fact]
    public void SynchronousPropertyResetCannotReenterLocalizationWrite()
    {
        var state = new LocalizedPropertyState();
        var writes = 0;

        void Apply() => state.Apply(
            read: () => "Home",
            write: _ =>
            {
                writes++;
                if (writes < 5) Apply();
            },
            containsKey: value => value == "Home",
            translate: _ => "Accueil");

        Apply();

        Assert.Equal(1, writes);
    }

    [Fact]
    public void DynamicEnglishValueCanBeTranslatedAfterInitialRender()
    {
        var state = new LocalizedPropertyState();
        var value = "Home";

        state.Apply(
            read: () => value,
            write: translated => value = translated,
            containsKey: key => key is "Home" or "Search",
            translate: key => key == "Home" ? "Accueil" : "Recherche");
        Assert.Equal("Accueil", value);

        value = "Search";
        state.Apply(
            read: () => value,
            write: translated => value = translated,
            containsKey: key => key is "Home" or "Search",
            translate: key => key == "Home" ? "Accueil" : "Recherche");

        Assert.Equal("Recherche", value);
    }
}
