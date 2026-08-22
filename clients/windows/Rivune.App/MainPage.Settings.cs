using System.Globalization;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Rivune.Windows;
using Rivune.App.ViewModels;
using Windows.ApplicationModel.DataTransfer;
using Windows.System;

namespace Rivune.App;

public sealed partial class MainPage
{
    private readonly string[] _settingsCategoryNames = ["General", "Video", "IntroDB", "Audio & subtitles", "Metadata", "About"];
    private EffectiveSettings? _effectiveSettings;
    private SettingsLayer? _profileSettings;
    private string? _activeSettingsCategory;
    private readonly List<Button> _settingsCategoryButtons = [];
    private string? _returningSettingsCategory;
    private string? _settingsLoadFailure;
    private bool _settingsUpdateInProgress;

    private bool UsesFrenchInterface =>
        _effectiveSettings?.Settings.InterfaceLanguage?.StartsWith("fr", StringComparison.OrdinalIgnoreCase) == true;

    private string UiText(string english, string french) => UsesFrenchInterface ? french : english;

    private string ViewerTabLabel(ViewerTab tab) => tab switch
    {
        ViewerTab.Home => UiText("Home", "Accueil"),
        ViewerTab.Search => UiText("Search", "Recherche"),
        ViewerTab.Library => UiText("Library", "Bibliothèque"),
        ViewerTab.Calendar => UiText("Calendar", "Calendrier"),
        _ => tab.ToString(),
    };

    private string HeroPositionLabel(int position, int total) =>
        UsesFrenchInterface ? $"{position} sur {total}" : $"{position} of {total}";

    private void ApplyInterfaceLanguage()
    {
        DashboardHeading.Text = ViewerTabLabel(_selectedViewerTab);
        DashboardRetryButton.Content = UiText("Retry", "Réessayer");
        DashboardLoadingStatus.Text = UiText("Loading your home", "Chargement de votre accueil");
        HeroPlayLabel.Text = UiText("Play", "Lire");
        HeroInfoLabel.Text = UiText("Info", "Plus d’infos");
        HeroRotationButton.Content = _heroRotationPaused ? UiText("Resume", "Reprendre") : UiText("Pause", "Pause");
        AutomationProperties.SetName(HeroPlayButton, UiText("Play", "Lire"));
        AutomationProperties.SetName(HeroInfoButton, UiText("More information", "Plus d’informations"));
        AutomationProperties.SetName(HomeNav, ViewerTabLabel(ViewerTab.Home));
        AutomationProperties.SetName(SearchNav, ViewerTabLabel(ViewerTab.Search));
        AutomationProperties.SetName(LibraryNav, ViewerTabLabel(ViewerTab.Library));
        AutomationProperties.SetName(CalendarNav, ViewerTabLabel(ViewerTab.Calendar));
        if (DashboardSections.Children.Count > 0)
            RebuildHomeSections(_viewerCollections, _continueWatchingTargets, _recommendationTargets);
    }

    private void BuildSettingsCategories()
    {
        if (SettingsCategories.Children.Count > 2) return;
        foreach (var category in _settingsCategoryNames)
        {
            var button = new Button
            {
                Tag = category,
                Style = (Style)Application.Current.Resources["RivuneMediaCard"],
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Stretch,
                MinHeight = 80,
            };
            button.Click += SettingsCategory_Click;

            var row = new Grid { MinHeight = 80, Padding = new Thickness(16, 10, 16, 10), ColumnSpacing = 16 };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            row.ColumnDefinitions.Add(new ColumnDefinition());
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });

            var iconField = new Border
            {
                Width = 48,
                Height = 48,
                CornerRadius = new CornerRadius(24),
                Background = (Brush)Application.Current.Resources["RivuneAccentContainerBrush"],
                VerticalAlignment = VerticalAlignment.Center,
                Child = new FontIcon
                {
                    Glyph = CategoryIcon(category),
                    FontSize = 22,
                    Foreground = (Brush)Application.Current.Resources["RivuneAccentBrush"],
                },
            };
            row.Children.Add(iconField);

            var copy = new StackPanel { Spacing = 4, VerticalAlignment = VerticalAlignment.Center };
            copy.Children.Add(new TextBlock
            {
                Text = category,
                Foreground = (Brush)Application.Current.Resources["RivunePrimaryTextBrush"],
                Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"],
            });
            copy.Children.Add(new TextBlock
            {
                Text = CategoryDescription(category),
                Foreground = (Brush)Application.Current.Resources["RivuneSecondaryTextBrush"],
                Style = (Style)Application.Current.Resources["RivuneBodySmallTextStyle"],
                TextWrapping = TextWrapping.Wrap,
                MaxLines = 2,
                TextTrimming = TextTrimming.CharacterEllipsis,
            });
            Grid.SetColumn(copy, 1);
            row.Children.Add(copy);

            var chevron = new FontIcon
            {
                Glyph = "\uE76C",
                Foreground = (Brush)Application.Current.Resources["RivuneMutedTextBrush"],
                VerticalAlignment = VerticalAlignment.Center,
            };
            Grid.SetColumn(chevron, 2);
            row.Children.Add(chevron);
            button.Content = row;
            AutomationProperties.SetName(button, category);
            _settingsCategoryButtons.Add(button);
            SettingsCategories.Children.Add(button);
        }
    }

    private static string CategoryIcon(string category) => category switch
    {
        "General" => "\uE775",
        "Video" => "\uE714",
        "IntroDB" => "\uE893",
        "Audio & subtitles" => "\uE7F6",
        "Metadata" => "\uE8B2",
        _ => "\uE946",
    };

    private static string CategoryDescription(string category) => category switch
    {
        "General" => "Startup, playback, motion, language, and color",
        "Video" => "Resolution, display matching, framing, and network quality",
        "IntroDB" => "Detected intro, recap, and credits actions, including automatic skipping on this device",
        "Audio & subtitles" => "Preferred audio and subtitle tracks",
        "Metadata" => "Language used for titles, descriptions, calendars, and discovery",
        _ => "Server, build, support, and diagnostics",
    };

    private async void ProfileMenu_Click(object sender, RoutedEventArgs e) => await ShowAccountDialogAsync();

    private async Task ShowAccountDialogAsync()
    {
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot,
        };
        var stack = new StackPanel();
        var closeButton = new Button
        {
            Style = (Style)Application.Current.Resources["RivuneIconButton"],
            Content = new FontIcon { Glyph = "\uE8BB", FontSize = 18 },
            Foreground = (Brush)Application.Current.Resources["RivuneSecondaryTextBrush"],
            HorizontalAlignment = HorizontalAlignment.Right,
        };
        AutomationProperties.SetName(closeButton, "Close");
        closeButton.Click += (_, _) => dialog.Hide();
        stack.Children.Add(closeButton);
        var settingsButton = AccountAction("Settings", "\uE713", OpenSettingsAsync, dialog);
        stack.Children.Add(settingsButton);
        stack.Children.Add(AccountDivider());
        stack.Children.Add(AccountAction("Change profile", "\uE77B", ChangeProfileAsync, dialog));
        stack.Children.Add(AccountDivider());
        stack.Children.Add(AccountAction("Refresh", "\uE72C", async () => await ShowDashboardAsync(), dialog));
        stack.Children.Add(AccountDivider(separated: true));
        stack.Children.Add(AccountAction("Disconnect", "\uE8AC", async () => await DisconnectCoreAsync(clearAddress: false), dialog, destructive: true));
        dialog.Content = new ScrollViewer
        {
            Content = stack,
            MaxHeight = 480,
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            VerticalScrollMode = ScrollMode.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        if (_tvInputMode) dialog.Opened += (_, _) => settingsButton.Focus(FocusState.Programmatic);
        await ShowDialogAsync(dialog);
    }
    private async Task ChangeProfileAsync()
    {
        var client = _state.Client;
        if (client is null) return;
        var generation = _state.GenerationId;
        try
        {
            await client.ClearProfileSelectionAsync(_state.Token);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client)) return;
            _state.Profile = null;
            ResetViewerProfileState();
            await ShowProfilesAsync();
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation)) return;
            var failure = Dialog("Could not change profile", FriendlyError(exception), "Close");
            await ShowDialogAsync(failure);
        }
    }


    private Button AccountAction(string label, string glyph, Func<Task> action, ContentDialog dialog, bool destructive = false)
    {
        var foreground = (Brush)Application.Current.Resources[destructive ? "RivuneDangerBrush" : "RivunePrimaryTextBrush"];
        var row = new Grid { Padding = new Thickness(16, 10, 16, 10), ColumnSpacing = 16 };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition());
        row.Children.Add(new FontIcon
        {
            Glyph = glyph,
            FontSize = 20,
            Foreground = destructive ? foreground : (Brush)Application.Current.Resources["RivuneSecondaryTextBrush"],
            VerticalAlignment = VerticalAlignment.Center,
        });
        var text = new TextBlock
        {
            Text = label,
            Foreground = foreground,
            Style = (Style)Application.Current.Resources["RivuneBodyLargeTextStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        };
        Grid.SetColumn(text, 1);
        row.Children.Add(text);
        var button = new Button
        {
            Content = row,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Style = (Style)Application.Current.Resources["RivuneLabeledActionButton"],
            Padding = new Thickness(0),
            MinHeight = 52,
            Foreground = foreground,
        };
        AutomationProperties.SetName(button, label);
        button.Click += async (_, _) =>
        {
            dialog.Hide();
            await action();
        };
        return button;
    }

    private static Border AccountDivider(bool separated = false) => new()
    {
        Height = 1,
        Margin = separated ? new Thickness(8, 20, 8, 4) : new Thickness(16, 0, 16, 0),
        Background = (Brush)Application.Current.Resources["RivuneHairlineBrush"],
    };

    private Task OpenSettingsAsync()
    {
        _state.Transition(AppPhase.Settings);
        _activeSettingsCategory = null;
        _returningSettingsCategory = null;
        SettingsHeading.Text = "Settings";
        SettingsCategories.Visibility = Visibility.Visible;
        SettingsPanelHost.Visibility = Visibility.Collapsed;
        ShowOnly(SettingsView);
        FocusSettingsCategories();
        return Task.CompletedTask;
    }

    private async Task LoadProfileSettingsAsync()
    {
        if (_state.Profile is not { Id: var profileId }) return;
        var client = _state.Client;
        if (client is null) return;
        var generation = _state.GenerationId;
        _settingsLoadFailure = null;
        try
        {
            var layerTask = client.GetProfileSettingsAsync(profileId, _state.Token);
            var effectiveTask = client.GetEffectiveProfileSettingsAsync(profileId, _state.Token);
            await Task.WhenAll(layerTask, effectiveTask);
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client) || _state.Profile?.Id != profileId) return;
            _profileSettings = await layerTask;
            _effectiveSettings = await effectiveTask;
            ApplyInterfaceLanguage();
        }
        catch (OperationCanceledException) { }
        catch (Exception exception)
        {
            if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client) || _state.Profile?.Id != profileId) return;
            _settingsLoadFailure = FriendlyError(exception);
        }
    }

    private string? MetadataLanguage()
    {
        var configured = _effectiveSettings?.Settings.MetadataLanguage?.Trim();
        if (!string.IsNullOrEmpty(configured) && !configured.Equals("auto", StringComparison.OrdinalIgnoreCase)) return configured;
        var language = CultureInfo.CurrentUICulture.IetfLanguageTag.Trim();
        if (!string.IsNullOrEmpty(language) && !language.Equals("und", StringComparison.OrdinalIgnoreCase)) return language;
        language = CultureInfo.CurrentUICulture.TwoLetterISOLanguageName.Trim();
        return string.IsNullOrEmpty(language) || language.Equals("iv", StringComparison.OrdinalIgnoreCase) ? null : language;
    }

    private async void SettingsCategory_Click(object sender, RoutedEventArgs e)
    {
        if (sender is not Button { Tag: string category }) return;
        _activeSettingsCategory = category;
        _returningSettingsCategory = category;
        SettingsHeading.Text = category;
        SettingsCategories.Visibility = Visibility.Collapsed;
        SettingsPanelHost.Visibility = Visibility.Visible;
        SettingsBackButton.Focus(FocusState.Programmatic);
        if (category != "About" && _effectiveSettings is null)
        {
            SettingsPanelHost.Children.Clear();
            var loading = new StackPanel { Spacing = 12, HorizontalAlignment = HorizontalAlignment.Center };
            loading.Children.Add(new ProgressRing { IsActive = true, Width = 28, Height = 28 });
            loading.Children.Add(new TextBlock { Text = "Loading profile settings…", Style = (Style)Application.Current.Resources["RivuneBodyMediumTextStyle"] });
            SettingsPanelHost.Children.Add(loading);
            await LoadProfileSettingsAsync();
        }
        if (!StringComparer.Ordinal.Equals(_activeSettingsCategory, category)) return;
        RenderSettingsCategory(category);
        FocusSettingsPanel();
    }

    private void RenderSettingsCategory(string category)
    {
        SettingsPanelHost.Children.Clear();
        if (category == "About")
        {
            RenderAboutSettings();
            return;
        }
        var settings = _effectiveSettings?.Settings;
        var sources = _effectiveSettings?.Sources;
        if (settings is null || sources is null)
        {
            SettingsPanelHost.Children.Add(CreateSettingsError(_settingsLoadFailure ?? "Profile settings are not available for this session."));
            return;
        }
        SettingsPanelHost.Children.Add(new TextBlock { Text = category, Style = (Style)Application.Current.Resources["RivuneHeadlineMediumTextStyle"] });
        if (_devicePreferencesFailure is not null)
            SettingsPanelHost.Children.Add(CreateSettingsError($"Device preferences are unavailable: {_devicePreferencesFailure}"));
        if (_state.Profile?.CanManage != true)
        {
            SettingsPanelHost.Children.Add(new InfoBar
            {
                Severity = InfoBarSeverity.Informational,
                Message = "This profile's effective settings are read-only for the current account.",
                IsOpen = true,
                IsClosable = false,
            });
        }
        switch (category)
        {
            case "General":
                AddChoice("Interface language", "Language used by compatible Rivune clients.", settings.InterfaceLanguage, ["en", "fr", "de", "es", "it", "pt"], sources.InterfaceLanguage, value => SaveSettingsAsync(new SettingsPatch { InterfaceLanguage = StringPatch(value) }));
                AddChoice("Theme", "Appearance inherited by compatible clients.", settings.Theme, ["system", "dark", "light"], sources.Theme, value => SaveSettingsAsync(new SettingsPatch { Theme = StringPatch(value) }));
                AddChoice("Card density", "Controls how much artwork appears on each row.", settings.CardDensity, ["comfortable", "compact"], sources.CardDensity, value => SaveSettingsAsync(new SettingsPatch { CardDensity = StringPatch(value) }));
                AddBooleanPreference("Animations", "Animate navigation and artwork where supported.", settings.AnimationsEnabled, sources.AnimationsEnabled, value => SaveSettingsAsync(new SettingsPatch { AnimationsEnabled = BooleanPatch(value) }));
                AddDeviceChoice("Accent color", "Color used for highlighted controls on this device.", AccentName(_devicePreferences.AccentColor), ["Blue", "Coral", "Green", "Violet", "Rose"], (preferences, value) => preferences with { AccentColor = AccentColorFromName(value) });
                AddDeviceChoice("Startup page", "Page opened after profile selection on this device.", _devicePreferences.StartupTab.ToString(), ["Home", "Search", "Library", "Calendar"], (preferences, value) => preferences with { StartupTab = Enum.Parse<ViewerTab>(value) });
                AddDeviceChoice("Motion", "Animation policy used by this device.", _devicePreferences.Motion.ToString(), ["System", "Full", "Reduced"], (preferences, value) => preferences with { Motion = Enum.Parse<DeviceMotionPreference>(value) });
                AddDeviceBoolean("Automatically show sources", "Open the source picker after selecting a playable title on this device.", _devicePreferences.AutomaticallyShowSources, (preferences, value) => preferences with { AutomaticallyShowSources = value });
                break;
            case "Video":
                AddChoice("Maximum resolution", "Limits automatic source selection.", settings.MaximumResolution, ["auto", "2160p", "1080p", "720p", "480p"], sources.MaximumResolution, value => SaveSettingsAsync(new SettingsPatch { MaximumResolution = StringPatch(value) }));
                AddBooleanPreference("Prefer direct play", "Use the original source when this device supports it.", settings.PreferDirectPlay, sources.PreferDirectPlay, value => SaveSettingsAsync(new SettingsPatch { PreferDirectPlay = BooleanPatch(value) }));
                AddReadOnly("Transcoding", SourceDescription(settings.AllowTranscoding == true ? "Available on this server" : "Unavailable on this server", sources.AllowTranscoding, settings.AllowTranscoding == true ? "Available" : "Unavailable"));
                AddBooleanPreference("Autoplay next episode", "Continue into the next episode automatically.", settings.AutoplayNextEpisode, sources.AutoplayNextEpisode, value => SaveSettingsAsync(new SettingsPatch { AutoplayNextEpisode = BooleanPatch(value) }));
                AddDeviceChoice("Default video aspect", "Initial fit mode for the native player on this device.", new[] { "Fit", "Fill", "Zoom" }[_devicePreferences.VideoAspectIndex], ["Fit", "Fill", "Zoom"], (preferences, value) => preferences with { VideoAspectIndex = value switch { "Fill" => 1, "Zoom" => 2, _ => 0 } });
                break;
            case "IntroDB":
                AddBooleanPreference("Detect intros", "Allow compatible players to offer intro skipping.", settings.SkipIntroEnabled, sources.SkipIntroEnabled, value => SaveSettingsAsync(new SettingsPatch { SkipIntroEnabled = BooleanPatch(value) }));
                AddBooleanPreference("Detect recaps", "Allow compatible players to offer recap skipping.", settings.SkipRecapEnabled, sources.SkipRecapEnabled, value => SaveSettingsAsync(new SettingsPatch { SkipRecapEnabled = BooleanPatch(value) }));
                AddBooleanPreference("Detect outros", "Allow compatible players to offer outro skipping.", settings.SkipOutroEnabled, sources.SkipOutroEnabled, value => SaveSettingsAsync(new SettingsPatch { SkipOutroEnabled = BooleanPatch(value) }));
                AddDeviceBoolean("Automatically skip intros", "Jump to the end of a detected intro on this device.", _devicePreferences.AutoSkipIntro, (preferences, value) => preferences with { AutoSkipIntro = value });
                AddDeviceBoolean("Automatically skip recaps", "Jump to the end of a detected recap on this device.", _devicePreferences.AutoSkipRecap, (preferences, value) => preferences with { AutoSkipRecap = value });
                AddDeviceBoolean("Automatically skip outros", "Jump to the end of detected credits or an outro on this device.", _devicePreferences.AutoSkipOutro, (preferences, value) => preferences with { AutoSkipOutro = value });
                break;
            case "Audio & subtitles":
                AddChoice("Audio language", "Preferred spoken language.", settings.AudioLanguage, ["auto", "en", "fr", "de", "es", "it", "ja"], sources.AudioLanguage, value => SaveSettingsAsync(new SettingsPatch { AudioLanguage = StringPatch(value) }));
                AddChoice("Subtitle language", "Preferred subtitle language.", settings.SubtitleLanguage, ["auto", "none", "en", "fr", "de", "es", "it"], sources.SubtitleLanguage, value => SaveSettingsAsync(new SettingsPatch { SubtitleLanguage = StringPatch(value) }));
                AddChoice("Forced subtitle language", "Language for forced dialogue subtitles.", settings.ForcedSubtitleLanguage, ["auto", "none", "off", "en", "fr", "de", "es", "it"], sources.ForcedSubtitleLanguage, value => SaveSettingsAsync(new SettingsPatch { ForcedSubtitleLanguage = StringPatch(value) }));
                AddChoice("Subtitle size", "Text size as a percentage of the player default.", settings.SubtitleSizePercent.ToString(), ["75", "100", "125", "150", "175", "200"], sources.SubtitleSizePercent, value => SaveSettingsAsync(new SettingsPatch { SubtitleSizePercent = value is null ? PatchField<int>.Null : PatchField<int>.FromValue(int.Parse(value)) }));
                AddChoice("Subtitle color", "Text color used by compatible players.", settings.SubtitleTextColor, ["white", "yellow", "cyan", "green"], sources.SubtitleTextColor, value => SaveSettingsAsync(new SettingsPatch { SubtitleTextColor = StringPatch(value) }));
                AddChoice("Subtitle background", "Background opacity percentage.", settings.SubtitleBackgroundOpacityPercent.ToString(), ["0", "25", "50", "75", "100"], sources.SubtitleBackgroundOpacityPercent, value => SaveSettingsAsync(new SettingsPatch { SubtitleBackgroundOpacityPercent = value is null ? PatchField<int>.Null : PatchField<int>.FromValue(int.Parse(value)) }));
                break;
            case "Metadata":
                AddChoice("Metadata language", "Preferred title and description language.", settings.MetadataLanguage, ["auto", "en", "fr", "de", "es", "it", "ja"], sources.MetadataLanguage, value => SaveSettingsAsync(new SettingsPatch { MetadataLanguage = StringPatch(value) }));
                AddChoice("Metadata region", "Region used for release and provider data.", settings.MetadataRegion, ["US", "FR", "GB", "DE", "CA", "JP"], sources.MetadataRegion, value => SaveSettingsAsync(new SettingsPatch { MetadataRegion = StringPatch(value) }));
                AddChoice("Series mapping", "Provider used to map seasons and episodes.", settings.SeriesMappingProvider, ["tmdb", "tvdb"], sources.SeriesMappingProvider, value => SaveSettingsAsync(new SettingsPatch { SeriesMappingProvider = StringPatch(value) }));
                AddBooleanPreference("Hide unreleased titles", "Exclude titles that are not yet available.", settings.HideUnreleased, sources.HideUnreleased, value => SaveSettingsAsync(new SettingsPatch { HideUnreleased = BooleanPatch(value) }));
                AddChoice("Maximum cast members", "Number of cast cards to display.", (settings.MaximumCastMembers ?? 20).ToString(), ["10", "20", "30", "50"], sources.MaximumCastMembers, value => SaveSettingsAsync(new SettingsPatch { MaximumCastMembers = value is null ? PatchField<int>.Null : PatchField<int>.FromValue(int.Parse(value)) }));
                break;
        }
    }

    private void RenderAboutSettings()
    {
        var discovery = _state.Discovery;

        var application = AboutSection("Rivune for Windows", "\uE7F4");
        application.Children.Add(DiagnosticValue("App build", $"Version {CurrentAppVersion}"));
        application.Children.Add(new TextBlock
        {
            Text = "Checks automatically at most once every 24 hours and whenever you ask.",
            Style = (Style)Application.Current.Resources["RivuneBodyMediumTextStyle"],
            Foreground = (Brush)Application.Current.Resources["RivuneSecondaryTextBrush"],
        });
        application.Children.Add(AboutSecondaryAction("Check now", "\uE895", CheckForUpdatesAsync));
        SettingsPanelHost.Children.Add(AboutSurface(application));

        var server = AboutSection("Connected server", "\uE774");
        server.Children.Add(DiagnosticValue("Name", discovery?.Name ?? "Unavailable"));
        server.Children.Add(DiagnosticValue("Address", DisplayServerAddress(discovery)));
        server.Children.Add(DiagnosticValue("Version", discovery?.ServerVersion ?? "Unavailable"));
        server.Children.Add(DiagnosticValue("Protocol", discovery?.ProtocolVersion.ToString() ?? "Unavailable"));
        server.Children.Add(AboutSecondaryAction("Change server", "\uE72B", ConfirmChangeServerAsync));
        SettingsPanelHost.Children.Add(AboutSurface(server));
        var links = new StackPanel { HorizontalAlignment = HorizontalAlignment.Stretch };
        links.Children.Add(AboutActionRow("Latest release", "\uE895", new Uri("https://github.com/moodiness/rivune/releases/latest")));
        links.Children.Add(AboutActionRow("Source code", "\uE943", new Uri("https://github.com/moodiness/rivune")));
        links.Children.Add(AboutActionRow("Report a problem", "\uE783", new Uri("https://github.com/moodiness/rivune/issues")));
        links.Children.Add(AboutActionRow("License", "\uE946", new Uri("https://github.com/moodiness/rivune/blob/main/LICENSE")));
        links.Children.Add(AboutActionRow("Notice", "\uE946", new Uri("https://github.com/moodiness/rivune/blob/main/NOTICE")));
        SettingsPanelHost.Children.Add(AboutSurface(links, new Thickness(0, 8, 0, 8)));

        var diagnostics = AboutSection("Diagnostics", "\uE8C8");
        diagnostics.Children.Add(new TextBlock
        {
            Text = "Copy a private, token-free summary for support.",
            Style = (Style)Application.Current.Resources["RivuneBodyMediumTextStyle"],
            Foreground = (Brush)Application.Current.Resources["RivuneSecondaryTextBrush"],
        });
        diagnostics.Children.Add(AboutSecondaryAction("Copy diagnostics", "\uE8C8", CopyDiagnosticsAsync));
        SettingsPanelHost.Children.Add(AboutSurface(diagnostics));
    }

    private async Task CopyDiagnosticsAsync()
    {
        var discovery = _state.Discovery;
        var package = new DataPackage();
        package.SetText($"Rivune Windows {CurrentAppVersion}\nServer: {discovery?.Name ?? "unknown"}\nAddress: {DisplayServerAddress(discovery)}\nServer version: {discovery?.ServerVersion ?? "unknown"}\nProtocol: {discovery?.ProtocolVersion.ToString() ?? "unknown"}");
        Clipboard.SetContent(package);
        await ShowUpdateDialogAsync("Diagnostics copied", "A token-free system summary was copied to the clipboard.");
    }

    private async Task OpenAboutLinkAsync(Uri uri)
    {
        if (!await Launcher.LaunchUriAsync(uri))
            await ShowUpdateDialogAsync("Could not open link", "Windows could not open this Rivune link.");
    }
    private async Task ConfirmChangeServerAsync()
    {
        var dialog = Dialog("Change server?", "This device will be signed out and the saved server address will be removed.", "Change server");
        if (await ShowDialogAsync(dialog) == ContentDialogResult.Primary)
        {
            await DisconnectCoreAsync(clearAddress: true);
        }
    }


    private Button AboutSecondaryAction(string label, string glyph, Func<Task> action)
    {
        var content = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            HorizontalAlignment = HorizontalAlignment.Center,
            Spacing = 10,
        };
        content.Children.Add(new FontIcon { Glyph = glyph, FontSize = 18 });
        content.Children.Add(new TextBlock { Text = label, VerticalAlignment = VerticalAlignment.Center });
        var button = new Button
        {
            Content = content,
            Style = (Style)Application.Current.Resources["RivuneSecondaryButton"],
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Center,
        };
        AutomationProperties.SetName(button, label);
        button.Click += async (_, _) => await action();
        return button;
    }

    private Button AboutActionRow(string label, string glyph, Uri uri)
    {
        var row = new Grid
        {
            MinHeight = 48,
            Padding = new Thickness(16, 8, 16, 8),
            ColumnSpacing = 16,
        };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition());
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.Children.Add(new FontIcon
        {
            Glyph = glyph,
            FontSize = 20,
            Foreground = (Brush)Application.Current.Resources["RivuneAccentBrush"],
            VerticalAlignment = VerticalAlignment.Center,
        });
        var text = new TextBlock
        {
            Text = label,
            Style = (Style)Application.Current.Resources["RivuneBodyLargeTextStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        };
        Grid.SetColumn(text, 1);
        row.Children.Add(text);
        var external = new FontIcon
        {
            Glyph = "\uE8A7",
            FontSize = 16,
            Foreground = (Brush)Application.Current.Resources["RivuneMutedTextBrush"],
            VerticalAlignment = VerticalAlignment.Center,
        };
        Grid.SetColumn(external, 2);
        row.Children.Add(external);
        var button = new Button
        {
            Content = row,
            Style = (Style)Application.Current.Resources["RivuneMediaCard"],
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
        };
        AutomationProperties.SetName(button, label);
        button.Click += async (_, _) => await OpenAboutLinkAsync(uri);
        return button;
    }

    private static StackPanel AboutSection(string title, string glyph)
    {
        var section = new StackPanel
        {
            HorizontalAlignment = HorizontalAlignment.Stretch,
            Spacing = 16,
        };
        var heading = new Grid { ColumnSpacing = 16 };
        heading.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        heading.ColumnDefinitions.Add(new ColumnDefinition());
        heading.Children.Add(new Border
        {
            Width = 48,
            Height = 48,
            CornerRadius = new CornerRadius(24),
            Background = (Brush)Application.Current.Resources["RivuneAccentContainerBrush"],
            Child = new FontIcon
            {
                Glyph = glyph,
                FontSize = 22,
                Foreground = (Brush)Application.Current.Resources["RivuneAccentBrush"],
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
            },
        });
        var text = new TextBlock
        {
            Text = title,
            Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        };
        AutomationProperties.SetHeadingLevel(text, AutomationHeadingLevel.Level2);
        Grid.SetColumn(text, 1);
        heading.Children.Add(text);
        section.Children.Add(heading);
        return section;
    }

    private static Border AboutSurface(StackPanel content, Thickness? padding = null) => new()
    {
        HorizontalAlignment = HorizontalAlignment.Stretch,
        Padding = padding ?? new Thickness(16),
        Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
        BorderBrush = (Brush)Application.Current.Resources["RivuneHairlineBrush"],
        BorderThickness = new Thickness(1),
        CornerRadius = (CornerRadius)Application.Current.Resources["RivuneRadiusLarge"],
        Child = content,
    };

    private static StackPanel DiagnosticValue(string label, string value)
    {
        var field = new StackPanel { Spacing = 2 };
        field.Children.Add(new TextBlock
        {
            Text = label,
            Style = (Style)Application.Current.Resources["RivuneLabelLargeTextStyle"],
            Foreground = (Brush)Application.Current.Resources["RivuneSecondaryTextBrush"],
        });
        field.Children.Add(new TextBlock
        {
            Text = value,
            Style = (Style)Application.Current.Resources["RivuneBodyMediumTextStyle"],
            Foreground = (Brush)Application.Current.Resources["RivunePrimaryTextBrush"],
            TextWrapping = TextWrapping.Wrap,
        });
        return field;
    }

    private void AddChoice(string title, string description, string effectiveValue, IReadOnlyList<string> choices, SettingSource source, Func<string?, Task> save)
    {
        const string inherit = "Use server value";
        var options = new List<string> { inherit };
        options.AddRange(choices);
        if (source == SettingSource.Profile && !options.Contains(effectiveValue)) options.Add(effectiveValue);
        var selected = source == SettingSource.Profile ? effectiveValue : inherit;
        var panel = PreferencePanel(title, SourceDescription(description, source, effectiveValue));
        var combo = new ComboBox { Header = title, MinWidth = 200, ItemsSource = options, SelectedItem = selected, IsEnabled = _state.Profile?.CanManage == true };
        AutomationProperties.SetName(combo, $"{title}. Effective value: {effectiveValue}. Source: {SourceLabel(source)}");
        combo.SelectionChanged += async (_, _) =>
        {
            if (combo.SelectedItem is string value && value != selected)
                await RunSettingsUpdateAsync(() => save(value == inherit ? null : value));
        };
        panel.Children.Add(combo);
        SettingsPanelHost.Children.Add(panel);
    }

    private void AddBooleanPreference(string title, string description, bool effectiveValue, SettingSource source, Func<bool?, Task> save)
    {
        AddChoice(title, description, effectiveValue ? "On" : "Off", ["On", "Off"], source, value => save(value switch
        {
            null => null,
            "On" => true,
            _ => false,
        }));
    }

    private void AddDeviceChoice(string title, string description, string selected, IReadOnlyList<string> choices, Func<WindowsDevicePreferences, string, WindowsDevicePreferences> update)
    {
        var panel = PreferencePanel(title, $"{description}\nStored on this device.");
        var combo = new ComboBox { Header = title, MinWidth = 200, ItemsSource = choices, SelectedItem = selected, IsEnabled = _devicePreferencesStore is not null };
        var committedValue = selected;
        var saveInProgress = false;
        AutomationProperties.SetName(combo, $"{title}. Device value: {committedValue}");
        combo.SelectionChanged += async (_, _) =>
        {
            if (_closed || saveInProgress || combo.SelectedItem is not string value || value == committedValue) return;
            saveInProgress = true;
            var saved = await RunSettingsUpdateAsync(() => SaveDevicePreferencesAsync(preferences => update(preferences, value)), "Saving device setting…");
            if (_closed) return;
            if (saved)
            {
                committedValue = value;
                AutomationProperties.SetName(combo, $"{title}. Device value: {committedValue}");
            }
            else
            {
                combo.SelectedItem = committedValue;
            }
            saveInProgress = false;
        };
        panel.Children.Add(combo);
        SettingsPanelHost.Children.Add(panel);
    }

    private void AddDeviceBoolean(string title, string description, bool selected, Func<WindowsDevicePreferences, bool, WindowsDevicePreferences> update) =>
        AddDeviceChoice(title, description, selected ? "On" : "Off", ["On", "Off"], (preferences, value) => update(preferences, value == "On"));

    private static string AccentName(string color) => color switch
    {
        WindowsDevicePreferences.CoralAccentColor => "Coral",
        WindowsDevicePreferences.GreenAccentColor => "Green",
        WindowsDevicePreferences.VioletAccentColor => "Violet",
        WindowsDevicePreferences.RoseAccentColor => "Rose",
        _ => "Blue",
    };

    private static string AccentColorFromName(string name) => name switch
    {
        "Coral" => WindowsDevicePreferences.CoralAccentColor,
        "Green" => WindowsDevicePreferences.GreenAccentColor,
        "Violet" => WindowsDevicePreferences.VioletAccentColor,
        "Rose" => WindowsDevicePreferences.RoseAccentColor,
        _ => WindowsDevicePreferences.DefaultAccentColor,
    };
    private async Task SaveDevicePreferencesAsync(Func<WindowsDevicePreferences, WindowsDevicePreferences> update)
    {
        var store = _devicePreferencesStore ?? throw new InvalidOperationException("Device preferences are unavailable.");
        await store.UpdateAsync(update);
        if (_closed) return;
        _devicePreferences = store.Snapshot;
        ApplyAccentPalette();
    }

    private void AddReadOnly(string title, string value)
    {
        var panel = PreferencePanel(title, value);
        SettingsPanelHost.Children.Add(panel);
    }
    private static PatchField<string> StringPatch(string? value) => value is null ? PatchField<string>.Null : PatchField<string>.FromValue(value);
    private static PatchField<bool> BooleanPatch(bool? value) => value is null ? PatchField<bool>.Null : PatchField<bool>.FromValue(value.Value);

    private static string SourceDescription(string description, SettingSource? source, string effectiveValue) =>
        $"{description}\nEffective: {effectiveValue}. Source: {SourceLabel(source)}.";

    private static string SourceLabel(SettingSource? source) => source switch
    {
        SettingSource.Profile => "Profile override",
        SettingSource.Instance => "Server setting",
        SettingSource.Device => "Device setting",
        SettingSource.Default => "Server default",
        _ => "Server",
    };

    private static StackPanel PreferencePanel(string title, string description)
    {
        var panel = new StackPanel
        {
            Spacing = 8,
            Padding = new Thickness(16),
            Background = (Microsoft.UI.Xaml.Media.Brush)Application.Current.Resources["RivuneSurfaceBrush"],
        };
        panel.Children.Add(new TextBlock { Text = title, Style = (Style)Application.Current.Resources["RivuneTitleMediumTextStyle"] });
        panel.Children.Add(new TextBlock { Text = description, Style = (Style)Application.Current.Resources["RivuneBodySmallTextStyle"], TextWrapping = TextWrapping.Wrap });
        return panel;
    }

    private static FrameworkElement CreateSettingsError(string message) => new InfoBar
    {
        Severity = InfoBarSeverity.Error,
        Message = message,
        IsOpen = true,
        IsClosable = false,
    };

    private async Task<bool> RunSettingsUpdateAsync(Func<Task> update, string status = "Saving profile setting…")
    {
        if (_closed || _settingsUpdateInProgress) return false;
        _settingsUpdateInProgress = true;
        var saving = new InfoBar
        {
            Severity = InfoBarSeverity.Informational,
            Message = status,
            IsOpen = true,
            IsClosable = false,
        };
        SettingsPanelHost.Children.Insert(0, saving);
        SettingsContent.IsHitTestVisible = false;
        SettingsContent.Opacity = 0.72;
        try
        {
            await update();
            return !_closed;
        }
        catch (OperationCanceledException)
        {
            return false;
        }
        catch (Exception exception)
        {
            if (!_closed)
            {
                if (SettingsPanelHost.Children.Contains(saving)) SettingsPanelHost.Children.Remove(saving);
                SettingsPanelHost.Children.Insert(0, CreateSettingsError(FriendlyError(exception)));
            }
            return false;
        }
        finally
        {
            if (!_closed)
            {
                if (SettingsPanelHost.Children.Contains(saving)) SettingsPanelHost.Children.Remove(saving);
                SettingsContent.IsHitTestVisible = true;
                SettingsContent.Opacity = 1;
            }
            _settingsUpdateInProgress = false;
        }
    }

    private async Task SaveSettingsAsync(SettingsPatch patch)
    {
        _settingsLoadFailure = null;
        if (_state.Profile is not { Id: var profileId, CanManage: true }) return;
        var client = _state.Client;
        if (client is null) return;
        var generation = _state.GenerationId;
        var layer = await client.UpdateProfileSettingsAsync(profileId, patch, _state.Token);
        var effective = await client.GetEffectiveProfileSettingsAsync(profileId, _state.Token);
        if (!_state.IsCurrent(generation) || !ReferenceEquals(client, _state.Client) || _state.Profile?.Id != profileId) return;
        _profileSettings = layer;
        _effectiveSettings = effective;
        ApplyInterfaceLanguage();
        if (_activeSettingsCategory is { } category) RenderSettingsCategory(category);
    }

    private static string DisplayServerAddress(Discovery? discovery)
    {
        if (discovery is null) return "Not connected";
        return Uri.TryCreate(discovery.ApiBaseUrl, UriKind.Absolute, out var uri)
            ? uri.GetLeftPart(UriPartial.Authority)
            : discovery.ApiBaseUrl;
    }

    private void ShowSettingsCategories()
    {
        _activeSettingsCategory = null;
        SettingsHeading.Text = "Settings";
        SettingsPanelHost.Visibility = Visibility.Collapsed;
        SettingsCategories.Visibility = Visibility.Visible;
        FocusSettingsCategories();
    }

    private void FocusSettingsCategories()
    {
        DispatcherQueue.TryEnqueue(() =>
        {
            var target = _settingsCategoryButtons.FirstOrDefault(button => button.Tag is string category && StringComparer.Ordinal.Equals(category, _returningSettingsCategory))
                ?? _settingsCategoryButtons.FirstOrDefault();
            target?.Focus(FocusState.Programmatic);
        });
    }

    private void FocusSettingsPanel()
    {
        DispatcherQueue.TryEnqueue(() =>
        {
            var target = Descendants(SettingsPanelHost)
                .OfType<Control>()
                .FirstOrDefault(control => control.Visibility == Visibility.Visible && control.IsEnabled && control.IsTabStop);
            (target ?? SettingsBackButton).Focus(FocusState.Programmatic);
        });
    }

    private async Task ReturnFromSettingsAsync()
    {
        ShowOnly(DashboardView);
        ShowViewerTab(_selectedViewerTab);
        FocusActiveViewerNavigation();
        switch (_selectedViewerTab)
        {
            case ViewerTab.Home:
                {
                    var generation = _state.Transition(AppPhase.Catalogue);
                    await LoadHomeAsync(generation);
                    break;
                }
            case ViewerTab.Search:
                if (SearchBox.Text.Trim().Length >= 2) await SearchAsync(reset: true);
                else
                {
                    _state.Transition(AppPhase.Catalogue);
                    await LoadSearchDescriptorsAsync();
                }
                break;
            case ViewerTab.Library:
                _state.Transition(AppPhase.Catalogue);
                await LoadLibraryAsync(reset: true);
                break;
            case ViewerTab.Calendar:
                _state.Transition(AppPhase.Catalogue);
                await LoadCalendarAsync();
                break;
        }
    }

    private void FocusActiveViewerNavigation()
    {
        var target = _selectedViewerTab switch
        {
            ViewerTab.Search => TabletDock.Visibility == Visibility.Visible ? SearchNav : BottomSearchNav,
            ViewerTab.Library => TabletDock.Visibility == Visibility.Visible ? LibraryNav : BottomLibraryNav,
            ViewerTab.Calendar => TabletDock.Visibility == Visibility.Visible ? CalendarNav : BottomCalendarNav,
            _ => TabletDock.Visibility == Visibility.Visible ? HomeNav : BottomHomeNav,
        };
        DispatcherQueue.TryEnqueue(() => target.Focus(FocusState.Programmatic));
    }

    private async void SettingsBack_Click(object sender, RoutedEventArgs e)
    {
        if (_activeSettingsCategory is not null)
        {
            ShowSettingsCategories();
            return;
        }
        await ReturnFromSettingsAsync();
    }
}
