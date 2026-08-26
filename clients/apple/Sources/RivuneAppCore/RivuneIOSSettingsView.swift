#if os(iOS)
import SwiftUI
import RivuneAPI
import UniformTypeIdentifiers

struct RivuneIOSSettingsView: View {
    @ObservedObject var model: RivuneAppModel
    let dismiss: () -> Void
    @Environment(\.openURL) private var openURL
    @State private var diagnosticStatus: String?
    @State private var diagnosticDocument: RivuneDiagnosticTextDocument?
    @State private var exportingDiagnostics = false
    @State private var archiveStatus: String?
    @State private var archiveDocument: RivuneProfileArchiveFileDocument?
    @State private var exportingArchive = false
    @State private var importingArchive = false
    @State private var archiveImportMode: ProfileArchiveImportMode = .merge

    private var accessibilityStatusAnnouncement: String? {
        if model.accessibilityPreferences != nil { return nil }
        if model.isProfileExperienceLoading(.accessibility) {
            return "Loading accessibility preferences"
        }
        if let failure = model.profileExperienceFailure(for: .accessibility) {
            return failure.localizedDescription
        }
        return "Accessibility preferences unavailable"
    }
    var body: some View {

        ZStack {
            RivuneIOSCanvas()
            RivuneIOSPage(maximumWidth: 860) {
                VStack(alignment: .leading, spacing: 24) {
                    header
                    if model.settingsLoading && model.profileSettings == nil {
                        RivuneIOSStatusView(state: .loading("Loading profile settings…"))
                    }
                    if let failure = model.settingsFailure {
                        VStack(spacing: 12) {
                            RivuneIOSStatusView(state: .failure(failure))
                            Button("Try again", action: model.loadProfileSettings)
                                .rivuneIOSPrimaryButton()
                                .disabled(model.settingsLoading)
                        }
                    }

                    deviceSection
                    playbackSection
                    videoSection
                    skipSection
                    accessibilitySection
                    languageSection
                    downloadsSection
                    if model.profileArchiveAvailable { profileArchiveSection }
                    connectionSection
                    updateSection
                    diagnosticsSection
                }
            }
        }
        .preferredColorScheme(.dark)
        .onAppear(perform: model.loadProfileSettings)
        .fileExporter(
            isPresented: $exportingDiagnostics,
            document: diagnosticDocument,
            contentType: .utf8PlainText,
            defaultFilename: "rivune-diagnostics"
        ) { result in
            let succeeded: Bool
            switch result {
            case .success:
                succeeded = true
                diagnosticStatus = "Diagnostic log exported."
            case .failure:
                succeeded = false
                diagnosticStatus = "Diagnostic log could not be exported."
            }
            diagnosticDocument = nil
            model.recordDiagnosticExport(succeeded: succeeded)
        }
        .fileExporter(
            isPresented: $exportingArchive,
            document: archiveDocument,
            contentType: .rivuneProfileArchive,
            defaultFilename: "rivune-profile"
        ) { result in
            archiveDocument = nil
            switch result {
            case .success:
                archiveStatus = "Profile archive exported. Keep it private: it can contain secret add-on URLs."
            case .failure:
                archiveStatus = "Profile archive could not be exported."
            }
        }
        .fileImporter(
            isPresented: $importingArchive,
            allowedContentTypes: [.rivuneProfileArchive, .json],
            allowsMultipleSelection: false
        ) { result in
            importArchive(result)
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 16) {
            RivuneIOSHeading(
                eyebrow: "This device",
                title: "Settings",
                message: "Local playback preferences stay on this device. Profile policy comes from your server."
            )
            Button(action: dismiss) { Image(systemName: "xmark") }
                .rivuneIOSIconButton()
                .accessibilityLabel(rivuneLocalized("Done"))
        }
    }

    private var deviceSection: some View {
        settingsSection("DEVICE", icon: "iphone") {
            settingsPicker(
                "Startup tab",
                value: rivuneIOSViewerTabName(model.startupTab),
                options: RivuneViewerTab.allCases.map { ($0, rivuneIOSViewerTabName($0)) },
                set: model.setStartupTab
            )
            divider
            settingsPicker(
                "Animations",
                value: model.animationPreference.displayName,
                options: RivuneAnimationPreference.allCases.map { ($0, $0.displayName) },
                set: model.setAnimationPreference
            )
            divider
            settingsPicker(
                "Recommendation cards",
                value: model.recommendationLayout.displayName,
                options: RivuneRecommendationLayout.allCases.map { ($0, $0.displayName) },
                set: model.setRecommendationLayout
            )
        }
    }

    private var playbackSection: some View {
        settingsSection("PLAYER", icon: "play.rectangle.fill") {
            settingsPicker(
                "Preferred player",
                value: model.preferredPlayer.displayName,
                options: RivunePlayerPreference.allCases.map { ($0, $0.displayName) },
                set: model.setPreferredPlayer
            )
            divider
            settingsPicker(
                "Embedded engine",
                value: model.embeddedPlayerPreference.displayName,
                options: RivuneEmbeddedPlayerPreference.allCases.map { ($0, $0.displayName) },
                set: model.setEmbeddedPlayerPreference
            )
            divider
            settingsPicker(
                "Frame-rate matching",
                value: model.frameRateMatching.displayName,
                options: RivuneFrameRatePreference.allCases.map { ($0, $0.displayName) },
                set: model.setFrameRateMatching
            )
            divider
            settingsPicker(
                "Video aspect",
                value: model.videoAspect.displayName,
                options: RivuneVideoAspect.allCases.map { ($0, $0.displayName) },
                set: model.setVideoAspect
            )
            divider
            settingsPicker(
                "Local quality",
                value: model.localQuality.displayName,
                options: RivuneNetworkQuality.allCases.map { ($0, $0.displayName) },
                set: model.setLocalQuality
            )
            divider
            settingsPicker(
                "Remote Wi-Fi quality",
                value: model.remoteWifiQuality.displayName,
                options: RivuneNetworkQuality.allCases.map { ($0, $0.displayName) },
                set: model.setRemoteWifiQuality
            )
            divider
            settingsPicker(
                "Mobile quality",
                value: model.mobileQuality.displayName,
                options: RivuneNetworkQuality.allCases.map { ($0, $0.displayName) },
                set: model.setMobileQuality
            )
            divider
            settingsToggle(
                "Show streams automatically",
                value: Binding(get: { model.automaticallyShowStreams }, set: model.setAutomaticallyShowStreams)
            )
        }
    }

    private var videoSection: some View {
        settingsSection("PROFILE VIDEO", icon: "film.fill") {
            profileStringPicker(
                "Maximum resolution",
                value: model.profileSettings?.maximumResolution,
                source: model.profileSettingsSources?.maximumResolution,
                options: [("auto", "Automatic"), ("2160p", "4K"), ("1080p", "1080p"), ("720p", "720p"), ("480p", "480p")]
            ) { model.updateProfileSettings(ProfileSettingsPatch(maximumResolution: $0.map(SettingsPatchField.value) ?? .null)) }
            divider
            profileBoolPicker("Prefer direct play", value: model.profileSettings?.preferDirectPlay, source: model.profileSettingsSources?.preferDirectPlay) {
                model.updateProfileSettings(ProfileSettingsPatch(preferDirectPlay: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            profileStringPicker(
                "Transcoding policy",
                value: model.profileSettings?.transcoding,
                source: model.profileSettingsSources?.transcoding,
                options: [("inherit", "Inherit"), ("enabled", "Enabled"), ("disabled", "Disabled")]
            ) { model.updateProfileSettings(ProfileSettingsPatch(transcoding: $0.map(SettingsPatchField.value) ?? .null)) }
            divider
            valueRow("Transcoding available", value: model.profileSettings.map { $0.allowTranscoding == true ? "Yes" : "No" } ?? "Unavailable")
            divider
            profileBoolPicker("Autoplay next episode", value: model.profileSettings?.autoplayNextEpisode, source: model.profileSettingsSources?.autoplayNextEpisode) {
                model.updateProfileSettings(ProfileSettingsPatch(autoplayNextEpisode: $0.map(SettingsPatchField.value) ?? .null))
            }
        }
    }

    private var accessibilitySection: some View {
        settingsSection("PROFILE ACCESSIBILITY", icon: "accessibility") {
            if let preferences = model.accessibilityPreferences {
                settingsPicker(
                    "Reduced motion", value: preferences.reducedMotion.rawValue,
                    options: AccessibilityReducedMotion.allCases.map { ($0, $0.rawValue) }
                ) { value in
                    model.updateAccessibilityPreferences(AccessibilityPreferencesDocument(
                        revision: preferences.revision, reducedMotion: value,
                        highContrast: preferences.highContrast, textScale: preferences.textScale,
                        captions: preferences.captions, audioDescription: preferences.audioDescription,
                        focusIndicators: preferences.focusIndicators))
                }
                divider
                settingsPicker(
                    "Contrast", value: preferences.highContrast.rawValue,
                    options: AccessibilityContrast.allCases.map { ($0, $0.rawValue) }
                ) { value in
                    model.updateAccessibilityPreferences(AccessibilityPreferencesDocument(
                        revision: preferences.revision, reducedMotion: preferences.reducedMotion,
                        highContrast: value, textScale: preferences.textScale,
                        captions: preferences.captions, audioDescription: preferences.audioDescription,
                        focusIndicators: preferences.focusIndicators))
                }
                divider
                settingsPicker(
                    "Text scale", value: "\(preferences.textScale)%",
                    options: [100, 115, 130].map { ($0, "\($0)%") }
                ) { value in
                    model.updateAccessibilityPreferences(AccessibilityPreferencesDocument(
                        revision: preferences.revision, reducedMotion: preferences.reducedMotion,
                        highContrast: preferences.highContrast, textScale: value,
                        captions: preferences.captions, audioDescription: preferences.audioDescription,
                        focusIndicators: preferences.focusIndicators))
                }
                divider
                settingsPicker(
                    "Captions", value: preferences.captions.rawValue,
                    options: AccessibilityCaptions.allCases.map { ($0, $0.rawValue) }
                ) { value in
                    model.updateAccessibilityPreferences(AccessibilityPreferencesDocument(
                        revision: preferences.revision, reducedMotion: preferences.reducedMotion,
                        highContrast: preferences.highContrast, textScale: preferences.textScale,
                        captions: value, audioDescription: preferences.audioDescription,
                        focusIndicators: preferences.focusIndicators))
                }
                divider
                settingsToggle("Audio description", value: Binding(
                    get: { preferences.audioDescription },
                    set: { value in
                        model.updateAccessibilityPreferences(AccessibilityPreferencesDocument(
                            revision: preferences.revision, reducedMotion: preferences.reducedMotion,
                            highContrast: preferences.highContrast, textScale: preferences.textScale,
                            captions: preferences.captions, audioDescription: value,
                            focusIndicators: preferences.focusIndicators))
                    }))
                divider
                settingsPicker(
                    "Focus indicators", value: preferences.focusIndicators.rawValue,
                    options: AccessibilityFocusIndicators.allCases.map { ($0, $0.rawValue) }
                ) { value in
                    model.updateAccessibilityPreferences(AccessibilityPreferencesDocument(
                        revision: preferences.revision, reducedMotion: preferences.reducedMotion,
                        highContrast: preferences.highContrast, textScale: preferences.textScale,
                        captions: preferences.captions, audioDescription: preferences.audioDescription,
                        focusIndicators: value))
                }
            } else if model.isProfileExperienceLoading(.accessibility) {
                RivuneIOSStatusView(state: .loading("Loading accessibility preferences…"))
                    .accessibilityLabel("Loading accessibility preferences")
            } else if let failure = model.profileExperienceFailure(for: .accessibility) {
                VStack(spacing: 10) {
                    RivuneIOSStatusView(state: .failure(failure))
                    Button("Retry", action: model.loadProfileExperiences).rivuneIOSPrimaryButton()
                        .accessibilityLabel("Retry loading accessibility preferences")
                }
            } else {
                RivuneIOSStatusView(state: .empty(
                    icon: "accessibility", title: "Accessibility preferences unavailable",
                    message: "Connect to this profile to load its preferences."))
                    .accessibilityLabel("Accessibility preferences unavailable")
            }
            if let conflict = model.profileConflictMessage {
                Label(conflict, systemImage: "arrow.triangle.2.circlepath")
                    .foregroundStyle(RivuneIOSTheme.warning)
                    .rivuneStatusAnnouncement(conflict)
            }
        }
        .rivuneStatusAnnouncement(accessibilityStatusAnnouncement)
    }

    private var skipSection: some View {
        settingsSection("SKIP MARKERS", icon: "forward.end.fill") {
            profileBoolPicker("Enable intro markers", value: model.profileSettings?.skipIntroEnabled, source: model.profileSettingsSources?.skipIntroEnabled) {
                model.updateProfileSettings(ProfileSettingsPatch(skipIntroEnabled: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            profileBoolPicker("Enable recap markers", value: model.profileSettings?.skipRecapEnabled, source: model.profileSettingsSources?.skipRecapEnabled) {
                model.updateProfileSettings(ProfileSettingsPatch(skipRecapEnabled: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            profileBoolPicker("Enable outro markers", value: model.profileSettings?.skipOutroEnabled, source: model.profileSettingsSources?.skipOutroEnabled) {
                model.updateProfileSettings(ProfileSettingsPatch(skipOutroEnabled: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            settingsToggle("Skip intros automatically", value: Binding(get: { model.autoSkipIntro }, set: model.setAutoSkipIntro))
            divider
            settingsToggle("Skip recaps automatically", value: Binding(get: { model.autoSkipRecap }, set: model.setAutoSkipRecap))
            divider
            settingsToggle("Skip outros automatically", value: Binding(get: { model.autoSkipOutro }, set: model.setAutoSkipOutro))
        }
    }

    private var languageSection: some View {
        settingsSection("LANGUAGES", icon: "captions.bubble.fill") {
            profileStringPicker("Audio language", value: model.profileSettings?.audioLanguage, source: model.profileSettingsSources?.audioLanguage, options: languageOptions) {
                model.updateProfileSettings(ProfileSettingsPatch(audioLanguage: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            profileStringPicker("Subtitle language", value: model.profileSettings?.subtitleLanguage, source: model.profileSettingsSources?.subtitleLanguage, options: languageOptions) {
                model.updateProfileSettings(ProfileSettingsPatch(subtitleLanguage: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            profileStringPicker("Forced subtitle language", value: model.profileSettings?.forcedSubtitleLanguage, source: model.profileSettingsSources?.forcedSubtitleLanguage, options: [("off", "Off")] + languageOptions.filter { $0.0 != "auto" }) {
                model.updateProfileSettings(ProfileSettingsPatch(forcedSubtitleLanguage: $0.map(SettingsPatchField.value) ?? .null))
            }
            divider
            profileStringPicker("Metadata language", value: model.profileSettings?.metadataLanguage, source: model.profileSettingsSources?.metadataLanguage, options: metadataLanguageOptions) {
                model.updateProfileSettings(ProfileSettingsPatch(metadataLanguage: $0.map(SettingsPatchField.value) ?? .null))
            }
        }
    }

    private var downloadsSection: some View {
        settingsSection("DOWNLOADS", icon: "arrow.down.circle.fill") {
            valueRow("Stored titles", value: String(model.offlineItems.count))
            divider
            settingsPicker(
                "Offline expiration",
                value: model.offlineExpirationDays == 0 ? "Never" : "\(model.offlineExpirationDays) days",
                options: [(30, "30 days"), (90, "90 days"), (0, "Never")],
                set: model.setOfflineExpirationDays
            )
            divider
            valueRow("Offline profiles", value: String(model.offlineProfiles.count))
            if model.offlineAccessUnlocked {
                divider
                Button { model.lockOffline() } label: {
                    Label("Lock offline access", systemImage: "lock.fill")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
            if model.offlineItems.isEmpty {
                Text("Choose Download from a compatible playback source to keep a title on this device.")
                    .font(.footnote)
                    .foregroundStyle(RivuneIOSTheme.mutedText)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                ForEach(model.offlineItems) { item in
                    divider
                    HStack(spacing: 12) {
                        VStack(alignment: .leading, spacing: 3) {
                            Text(item.title)
                                .font(.subheadline.weight(.semibold))
                                .foregroundStyle(RivuneIOSTheme.primaryText)
                                .lineLimit(2)
                            Text(ByteCountFormatter.string(fromByteCount: item.sizeBytes, countStyle: .file))
                                .font(.caption)
                                .foregroundStyle(RivuneIOSTheme.mutedText)
                        }
                        Spacer()
                        Button(role: .destructive) { model.removeOffline(item) } label: {
                            Image(systemName: "trash")
                                .frame(width: 44, height: 44)
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(RivuneIOSTheme.danger)
                        .accessibilityLabel(rivuneLocalized("Delete download"))
                    }
                }
            }
        }
    }
    private var profileArchiveSection: some View {
        settingsSection("PROFILE ARCHIVE", icon: "archivebox.fill") {
            Text("Archives can contain secret add-on URLs. Store and share them only with people you trust.")
                .font(.footnote)
                .foregroundStyle(RivuneIOSTheme.warning)
                .fixedSize(horizontal: false, vertical: true)
            Button("Export profile") {
                Task {
                    do {
                        archiveDocument = RivuneProfileArchiveFileDocument(
                            archive: try await model.exportActiveProfileArchive())
                        exportingArchive = true
                    } catch { archiveStatus = archiveErrorMessage(error) }
                }
            }
            .rivuneIOSSecondaryButton()
            Button("Merge archive into this profile") {
                archiveImportMode = .merge
                importingArchive = true
            }
            .rivuneIOSSecondaryButton()
            Button("Create profile from archive") {
                archiveImportMode = .create
                importingArchive = true
            }
            .rivuneIOSPrimaryButton()
            if let report = model.profileArchiveReport {
                Text(archiveReport(report)).font(.caption).foregroundStyle(RivuneIOSTheme.mutedText)
            } else if let archiveStatus {
                Text(archiveStatus).font(.caption).foregroundStyle(RivuneIOSTheme.mutedText)
            }
        }
    }

    private func importArchive(_ result: Result<[URL], Error>) {
        Task {
            do {
                let urls = try result.get()
                guard let url = urls.first else { throw ProfileArchiveError.invalidDocument }
                let accessed = url.startAccessingSecurityScopedResource()
                defer { if accessed { url.stopAccessingSecurityScopedResource() } }
                let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
                guard size <= ProfileArchiveDocument.maximumBytes else { throw ProfileArchiveError.tooLarge }
                let archive = try ProfileArchiveDocument(data: Data(contentsOf: url, options: .mappedIfSafe))
                let report = archiveImportMode == .merge
                    ? try await model.mergeActiveProfileArchive(archive)
                    : try await model.createProfileFromArchive(archive)
                archiveStatus = archiveReport(report)
            } catch { archiveStatus = archiveErrorMessage(error) }
        }
    }

    private func archiveReport(_ report: ProfileArchiveImportReport) -> String {
        let details = report.sections.map {
            "\($0.section): \($0.created) created, \($0.updated) updated, \($0.unchanged) unchanged"
        }.joined(separator: " · ")
        return "\(report.mode == .merge ? "Merge" : "Creation") complete. \(details)"
    }

    private func archiveErrorMessage(_ error: Error) -> String {
        if let api = error as? RivuneAPIError,
           case .server(let status, let code, _, _) = api {
            switch status {
            case 403: return "Global administrator access is required."
            case 409: return "The archive conflicts with existing profile data (\(code))."
            case 413: return "The profile archive exceeds the 16 MiB limit."
            default: break
            }
        }
        return error.localizedDescription
    }


    private var connectionSection: some View {
        settingsSection("CONNECTION", icon: "network") {
            valueRow("Server", value: model.serverName)
            divider
            valueRow("Address", value: RivuneDiagnosticsReport.sanitizeServerOrigin(model.serverAddress) ?? "Unavailable")
            divider
            valueRow("Server version", value: model.serverVersion ?? "Unavailable")
            divider
            valueRow("Protocol", value: model.serverProtocolVersion.map(String.init) ?? "Unavailable")
            divider
            valueRow("Profile", value: model.activeProfile?.name ?? "None")
        }
    }

    private var updateSection: some View {
        settingsSection("APPLICATION UPDATE", icon: "arrow.triangle.2.circlepath") {
            valueRow("Installed version", value: model.applicationVersion)
            divider
            updateStatus(model.updateState)
            divider
            Button { model.checkForUpdates() } label: {
                HStack(spacing: 8) {
                    if model.updateState == .checking { ProgressView() }
                    Label(model.updateState == .checking ? "Checking…" : "Check now", systemImage: "arrow.triangle.2.circlepath")
                }
                .frame(maxWidth: .infinity)
            }
            .rivuneIOSSecondaryButton()
            .disabled(model.updateState == .checking)
            Button { model.enableUpdateNotifications() } label: {
                Label("Enable update alerts", systemImage: "bell.badge")
                    .frame(maxWidth: .infinity)
            }
            .rivuneIOSSecondaryButton()
        }
    }

    private var diagnosticsSection: some View {
        settingsSection("DIAGNOSTICS", icon: "stethoscope") {
            Text("The report contains allowlisted system fields and recent in-memory event codes only. Rivune never uploads it.")
                .font(.footnote)
                .foregroundStyle(RivuneIOSTheme.mutedText)
                .fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 10) {
                Button("Copy") {
                    let copied = copyRivuneDiagnosticReport(model.diagnosticReport())
                    model.recordDiagnosticExport(succeeded: copied)
                    diagnosticStatus = copied
                        ? "Diagnostics copied locally for 60 seconds. Universal Clipboard is disabled."
                        : "Diagnostics could not be copied."
                }
                .rivuneIOSSecondaryButton()
                Button("Export") {
                    diagnosticDocument = RivuneDiagnosticTextDocument(report: model.diagnosticReport())
                    exportingDiagnostics = true
                }
                .rivuneIOSPrimaryButton()
            }
            if let diagnosticStatus {
                Text(rivuneLocalized(diagnosticStatus))
                    .font(.caption)
                    .foregroundStyle(RivuneIOSTheme.mutedText)
            }
        }
    }

    private func settingsSection<Content: View>(
        _ title: String,
        icon: String,
        @ViewBuilder content: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Label(rivuneLocalized(title), systemImage: icon)
                .font(.caption.weight(.bold))
                .foregroundStyle(RivuneIOSTheme.ember)
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .rivuneIOSCard()
    }

    private var divider: some View {
        Divider().overlay(RivuneIOSTheme.hairline)
    }

    private func settingsToggle(_ title: String, value: Binding<Bool>) -> some View {
        Toggle(isOn: value) {
            Text(rivuneLocalized(title))
                .font(.body.weight(.medium))
                .foregroundStyle(RivuneIOSTheme.primaryText)
        }
        .tint(RivuneIOSTheme.ember)
        .frame(minHeight: 44)
    }

    private func settingsPicker<Value: Hashable>(
        _ title: String,
        value: String,
        options: [(Value, String)],
        set: @escaping (Value) -> Void
    ) -> some View {
        HStack(spacing: 14) {
            Text(rivuneLocalized(title))
                .font(.body.weight(.medium))
                .foregroundStyle(RivuneIOSTheme.primaryText)
            Spacer()
            Menu {
                ForEach(options, id: \.0) { option, name in
                    Button(rivuneLocalized(name)) { set(option) }
                }
            } label: {
                HStack(spacing: 6) {
                    Text(rivuneLocalized(value))
                        .foregroundStyle(RivuneIOSTheme.secondaryText)
                        .lineLimit(1)
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.caption2.bold())
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                }
                .frame(minHeight: 44)
            }
        }
    }

    private func profileStringPicker(
        _ title: String,
        value: String?,
        source: String?,
        options: [(String, String)],
        update: @escaping (String?) -> Void
    ) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 14) {
                Text(rivuneLocalized(title))
                    .font(.body.weight(.medium))
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                Spacer()
                Menu {
                    Button("Use server value") { update(nil) }
                    ForEach(options, id: \.0) { option in
                        Button(rivuneLocalized(option.1)) { update(option.0) }
                    }
                } label: {
                    HStack(spacing: 6) {
                        Text(rivuneLocalized(profileDisplayValue(value, source: source, options: options)))
                            .foregroundStyle(RivuneIOSTheme.secondaryText)
                            .lineLimit(1)
                        Image(systemName: "chevron.up.chevron.down")
                            .font(.caption2.bold())
                            .foregroundStyle(RivuneIOSTheme.mutedText)
                    }
                    .frame(minHeight: 44)
                }
                .disabled(model.activeProfile?.canManage != true || model.settingsLoading || model.profileSettings == nil)
            }
            Text(model.profileSettings == nil
                ? rivuneLocalized("Effective value unavailable")
                : rivuneLocalizedFormat("Source: %@", rivuneLocalized(source ?? "server")))
                .font(.caption)
                .foregroundStyle(RivuneIOSTheme.mutedText)
        }
    }

    private func profileBoolPicker(
        _ title: String,
        value: Bool?,
        source: String?,
        update: @escaping (Bool?) -> Void
    ) -> some View {
        profileStringPicker(
            title,
            value: value.map(String.init),
            source: source,
            options: [("true", "On"), ("false", "Off")]
        ) { update($0.flatMap(Bool.init)) }
    }

    private func profileDisplayValue(_ value: String?, source: String?, options: [(String, String)]) -> String {
        guard source == "profile", let value else { return "Server value" }
        return options.first { $0.0 == value }?.1 ?? value
    }

    private func valueRow(_ title: String, value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 16) {
            Text(rivuneLocalized(title))
                .font(.body.weight(.medium))
                .foregroundStyle(RivuneIOSTheme.primaryText)
            Spacer()
            Text(rivuneLocalized(value))
                .foregroundStyle(RivuneIOSTheme.mutedText)
                .multilineTextAlignment(.trailing)
                .lineLimit(2)
        }
        .frame(minHeight: 44)
    }

    @ViewBuilder
    private func updateStatus(_ state: RivuneAppleUpdateState) -> some View {
        switch state {
        case .idle:
            Text("Rivune checks the verified GitHub release automatically once per day.")
                .font(.footnote)
                .foregroundStyle(RivuneIOSTheme.mutedText)
        case .checking:
            Label("Checking the verified release manifest…", systemImage: "arrow.triangle.2.circlepath")
                .foregroundStyle(RivuneIOSTheme.secondaryText)
        case .upToDate(_, let latest):
            Label("Up to date · \(latest)", systemImage: "checkmark.circle.fill")
                .foregroundStyle(RivuneIOSTheme.success)
        case .available(let update):
            VStack(alignment: .leading, spacing: 10) {
                Label("Version \(update.latestVersion) is available", systemImage: "arrow.down.circle.fill")
                    .foregroundStyle(RivuneIOSTheme.ember)
                Text("Automatic installation is unavailable. Download the unsigned Apple package from the verified release.")
                    .font(.footnote)
                    .foregroundStyle(RivuneIOSTheme.mutedText)
                Button("Open release") { openURL(update.releaseURL) }
                    .rivuneIOSPrimaryButton()
            }
        case .failed:
            Label("The update check failed. No package was downloaded.", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(RivuneIOSTheme.warning)
        }
    }

    private var languageOptions: [(String, String)] {
        [("auto", "Automatic"), ("en", "English"), ("fr", "French"), ("de", "German"), ("es", "Spanish"), ("it", "Italian"), ("pt", "Portuguese"), ("ja", "Japanese")]
    }

    private var metadataLanguageOptions: [(String, String)] {
        [("auto", "Automatic"), ("en-US", "English"), ("fr-FR", "French"), ("de-DE", "German"), ("es-ES", "Spanish"), ("it-IT", "Italian"), ("pt-BR", "Portuguese"), ("ja-JP", "Japanese")]
    }
}

private func rivuneIOSViewerTabName(_ tab: RivuneViewerTab) -> String {
    switch tab {
    case .home: return "Home"
    case .search: return "Search"
    case .library: return "Library"
    case .calendar: return "Calendar"
    }
}
#endif
