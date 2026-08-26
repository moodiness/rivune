using System.Security.Cryptography;
using Microsoft.Windows.Storage.Pickers;
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
    private const int MaximumProfileArchiveBytes = 16 * 1024 * 1024;

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
        var archivesAvailable = _state.Discovery?.SupportsProfileArchivesV2 == true &&
            _state.Account?.Session.AuthorizationScope == AuthorizationScope.GlobalAdministrator;
        ExportProfileArchiveButton.Visibility = archivesAvailable ? Visibility.Visible : Visibility.Collapsed;
        ImportProfileArchiveButton.Visibility = archivesAvailable ? Visibility.Visible : Visibility.Collapsed;
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

    private async void ExportProfileArchive_Click(object sender, RoutedEventArgs e)
    {
        var client = _state.Client;
        if (client is null || _state.Discovery?.SupportsProfileArchivesV2 != true) return;
        var profiles = await client.GetProfilesAsync(_state.Token);
        var profile = await ChooseAsync("Export profile", profiles, value => value.Name);
        if (profile is null) return;
        var warning = Dialog("Export private profile archive?", "This archive can contain secret add-on URLs, including embedded tokens. Store and share it like a credential file.", "Export");
        if (await ShowDialogAsync(warning) != ContentDialogResult.Primary) return;
        byte[]? bytes = null;
        try
        {
            var document = await client.ExportProfileArchiveAsync(profile.Id, _state.Token);
            bytes = RivuneApiClient.SerializeProfileArchive(document);
            var picker = new FileSavePicker(App.MainWindow.AppWindow.Id)
            {
                SuggestedStartLocation = PickerLocationId.DocumentsLibrary,
                SuggestedFileName = $"rivune-{SafeArchiveFileStem(profile.Name)}-profile-v2",
                DefaultFileExtension = ".json",
                CommitButtonText = "Export",
                ShowOverwritePrompt = true,
            };
            picker.FileTypeChoices.Add("Rivune profile archive", new List<string> { ".json" });
            var file = await picker.PickSaveFileAsync();
            if (file is null) return;
            await File.WriteAllBytesAsync(file.Path, bytes, _state.Token);
            await ShowUpdateDialogAsync("Profile exported", "The private archive was saved. Keep it protected because add-on URLs may contain secrets.");
        }
        catch (Exception exception) { await ShowUpdateDialogAsync("Profile export failed", FriendlyError(exception)); }
        finally { if (bytes is not null) CryptographicOperations.ZeroMemory(bytes); }
    }

    private async void ImportProfileArchive_Click(object sender, RoutedEventArgs e)
    {
        var client = _state.Client;
        if (client is null || _state.Discovery?.SupportsProfileArchivesV2 != true) return;
        try
        {
            var picker = new FileOpenPicker(App.MainWindow.AppWindow.Id)
            {
                SuggestedStartLocation = PickerLocationId.DocumentsLibrary,
                CommitButtonText = "Open private archive",
            };
            picker.FileTypeFilter.Add(".json");
            var file = await picker.PickSingleFileAsync();
            if (file is null) return;
            var info = new FileInfo(file.Path);
            if (!info.Exists || info.Length is <= 0 or > MaximumProfileArchiveBytes)
                throw new InvalidDataException("The profile archive must be between 1 byte and 16 MiB.");
            var bytes = await File.ReadAllBytesAsync(file.Path, _state.Token);
            ProfileArchiveDocument archive;
            try { archive = RivuneApiClient.ParseProfileArchive(bytes); }
            finally { CryptographicOperations.ZeroMemory(bytes); }
            var warning = Dialog("Import private profile archive?", "This file may contain secret add-on URLs. Rivune sends it only to the connected server and never places its contents in settings or diagnostics.", "Continue");
            if (await ShowDialogAsync(warning) != ContentDialogResult.Primary) return;
            var mode = await ChooseAsync("Import mode", new[] { "Merge into a profile", "Create a profile" }, value => value);
            if (mode is null) return;
            ProfileArchiveImportReport report;
            if (mode == "Merge into a profile")
            {
                var target = await ChooseAsync("Merge into profile", await client.GetProfilesAsync(_state.Token), value => value.Name);
                if (target is null) return;
                report = await client.MergeProfileArchiveAsync(target.Id, archive, _state.Token);
            }
            else
            {
                var category = await ChooseAsync("Create in category", await client.GetCategoriesAsync(_state.Token), value => value.Name);
                if (category is null) return;
                report = await client.CreateProfileFromArchiveAsync(category.Id, archive, _state.Token);
            }
            await ShowUpdateDialogAsync("Profile archive imported", ArchiveReport(report));
            await ShowProfilesAsync();
        }
        catch (RivuneServerException exception) when (exception.StatusCode == 403) { await ShowUpdateDialogAsync("Profile import forbidden", "Global administrator access is required for portable profile archives."); }
        catch (RivuneServerException exception) when (exception.StatusCode == 409) { await ShowUpdateDialogAsync("Profile import conflict", "The archive conflicts with current profile data. Refresh profiles and retry with the intended target."); }
        catch (Exception exception) { await ShowUpdateDialogAsync("Profile import failed", FriendlyError(exception)); }
    }

    private static string SafeArchiveFileStem(string value)
    {
        var stem = new string(value.Where(character => char.IsLetterOrDigit(character) || character is '-' or '_').Take(48).ToArray());
        return stem.Length == 0 ? "profile" : stem;
    }

    private static string ArchiveReport(ProfileArchiveImportReport report)
    {
        var sections = report.Sections.Take(32).Select(section => $"{section.Section}: {section.Created} created, {section.Updated} updated, {section.Unchanged} unchanged");
        return string.Join(Environment.NewLine, sections.Append($"Tracking accounts updated: {report.TrackingAccountsUpdated}"));
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
