using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Rivune.Windows;
using Windows.Storage.Streams;

namespace Rivune.App;

public sealed partial class MainPage
{
    private readonly SemaphoreSlim _profileAvatarGate = new(4, 4);

    private void PopulateProfiles(IReadOnlyList<Profile> profiles, long generation)
    {
        ProfileGrid.IsItemClickEnabled = false;
        ProfileGrid.ItemsSource = null;
        ProfileGrid.Items.Clear();
        foreach (var profile in profiles)
        {
            var available = profile.Accessible && profile.Enabled;
            var button = new Button
            {
                Tag = profile,
                Style = (Style)Application.Current.Resources["RivuneProfileCard"],
                IsEnabled = available,
                Margin = new Thickness(8),
            };
            button.Click += ProfileCard_Click;

            var stack = new StackPanel { Width = 148, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Center };
            var avatar = new Grid
            {
                Width = 136,
                Height = 136,
                Background = (Brush)Application.Current.Resources["RivuneArtworkFallbackBrush"],
                CornerRadius = (CornerRadius)Application.Current.Resources["RivuneRadiusLarge"],
                Opacity = available ? 1 : 0.55,
            };
            var initial = new TextBlock
            {
                Text = ProfileInitial(profile.Name),
                Style = (Style)Application.Current.Resources["RivuneHeadlineLargeTextStyle"],
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
            };
            avatar.Children.Add(initial);
            var image = new Image { Stretch = Stretch.UniformToFill, Opacity = 0 };
            avatar.Children.Add(image);
            stack.Children.Add(avatar);
            stack.Children.Add(new TextBlock
            {
                Text = profile.Name,
                Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"],
                TextAlignment = TextAlignment.Center,
                MaxLines = 2,
                TextTrimming = TextTrimming.CharacterEllipsis,
            });

            var statuses = new List<string>();
            var statusPanel = new StackPanel
            {
                Height = 34,
                Spacing = 2,
                HorizontalAlignment = HorizontalAlignment.Center,
            };
            if (profile.HasPin)
            {
                const string pinRequired = "PIN required";
                statusPanel.Children.Add(CreateProfileStatus("\uE72E", pinRequired, "RivuneAccentBrush"));
                statuses.Add(UiText(pinRequired));
            }
            if (!profile.Enabled)
            {
                const string disabled = "Disabled";
                statusPanel.Children.Add(CreateProfileStatus("\uE711", disabled, "RivuneDangerBrush"));
                statuses.Add(UiText(disabled));
            }
            else if (!profile.Accessible)
            {
                const string unavailable = "Unavailable";
                statusPanel.Children.Add(CreateProfileStatus("\uE823", unavailable, "RivuneWarningBrush"));
                statuses.Add(UiText(unavailable));
            }
            stack.Children.Add(statusPanel);

            button.Content = stack;
            AutomationProperties.SetName(button, statuses.Count == 0 ? profile.Name : $"{profile.Name}, {string.Join(", ", statuses)}");
            ProfileGrid.Items.Add(button);
            _ = LoadProfileAvatarAsync(image, initial, profile, generation);
        }
    }

    private StackPanel CreateProfileStatus(string glyph, string text, string brushKey)
    {
        var brush = (Brush)Application.Current.Resources[brushKey];
        var row = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 5,
            HorizontalAlignment = HorizontalAlignment.Center,
        };
        row.Children.Add(new FontIcon
        {
            Glyph = glyph,
            FontSize = 12,
            Foreground = brush,
            VerticalAlignment = VerticalAlignment.Center,
        });
        row.Children.Add(new TextBlock
        {
            Text = UiText(text),
            Foreground = brush,
            Style = (Style)Application.Current.Resources["RivuneLabelSmallTextStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        });
        return row;
    }

    private async Task LoadProfileAvatarAsync(Image image, TextBlock initial, Profile profile, long generation)
    {
        var source = await LoadProfileAvatarSourceAsync(profile, generation);
        if (source is null) return;
        image.Source = source;
        initial.Opacity = 0;
        image.Opacity = 1;
    }

    private async Task LoadShellProfileAvatarAsync(Profile profile, long generation)
    {
        var source = await LoadProfileAvatarSourceAsync(profile, generation);
        if (source is null) return;
        CompactProfileImage.Source = source;
        CompactProfileInitial.Opacity = 0;
        CompactProfileImage.Opacity = 1;
        DockProfileImage.Source = source;
        DockProfileImage.Opacity = 1;
        DockProfileInitial.Opacity = 0;
    }

    private async Task<ImageSource?> LoadProfileAvatarSourceAsync(Profile profile, long generation)
    {
        if (profile.Avatar.Kind is not ("custom" or "preset")) return null;
        var client = _state.Client;
        if (client is null) return null;

        var entered = false;
        try
        {
            await _profileAvatarGate.WaitAsync(_state.Token);
            entered = true;
            var bytes = profile.Avatar.Kind == "custom"
                ? await client.GetProfileAvatarAsync(profile.Id, _state.Token)
                : await client.DownloadSameOriginResourceAsync(profile.Avatar.Url, _state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return null;
            using var stream = new InMemoryRandomAccessStream();
            using (var writer = new DataWriter(stream))
            {
                writer.WriteBytes(bytes);
                await writer.StoreAsync();
                writer.DetachStream();
            }
            stream.Seek(0);
            ImageSource source;
            if (profile.Avatar.Kind == "preset" && LooksLikeSvgArtwork(bytes))
            {
                var svg = new SvgImageSource();
                if (await svg.SetSourceAsync(stream) != SvgImageSourceLoadStatus.Success) return null;
                source = svg;
            }
            else
            {
                var bitmap = new BitmapImage();
                await bitmap.SetSourceAsync(stream);
                source = bitmap;
            }
            return _state.IsCurrent(generation) && ReferenceEquals(client, _state.Client) ? source : null;
        }
        catch (OperationCanceledException) { return null; }
        catch (Exception) { return null; }
        finally
        {
            if (entered) _profileAvatarGate.Release();
        }
    }

    private async void ProfileCard_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: Profile profile }) await ActivateProfileAsync(profile);
    }

    private async Task ActivateProfileAsync(Profile profile)
    {
        if (!profile.Accessible || !profile.Enabled)
        {
            ProfileBanner.Severity = InfoBarSeverity.Warning;
            ProfileBanner.Message = UiText("This profile is not currently accessible.");
            ProfileBanner.IsOpen = true;
            return;
        }
        if (profile.HasPin) await PromptForPinAsync(profile);
        else await SelectProfileAsync(profile, null);
    }

    private static string ProfileInitial(string? name)
    {
        var trimmed = name?.Trim();
        return string.IsNullOrEmpty(trimmed) ? "?" : char.ToUpperInvariant(trimmed[0]).ToString();
    }
}
