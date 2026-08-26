#if os(iOS)
import SwiftUI
import RivuneAPI
import UIKit

@MainActor
public struct RivuneIOSRootView: View {
    @StateObject private var model: RivuneAppModel
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.openURL) private var openURL
    @State private var offlinePIN = ""

    public init(model: RivuneAppModel) {
        _model = StateObject(wrappedValue: model)
    }

    public init() {
        _model = StateObject(wrappedValue: RivuneAppModel())
    }

    public var body: some View {
        ZStack(alignment: .bottomTrailing) {
            RivuneIOSCanvas()
            Group {
                switch model.destination {
                case .server:
                    RivuneIOSServerView(model: model)
                case .pairing:
                    RivuneIOSPairingView(model: model)
                case .profiles:
                    RivuneIOSProfilesView(model: model)
                case .library:
                    RivuneIOSLibraryView(model: model)
                }
            }
            .id(model.destination)
            .rivuneTransition(.opacity)

            if let presentation = model.minimizedPlaybackPresentation {
                RivuneMiniPlayerView(presentation: presentation, model: model)
                    .frame(width: 300)
                    .aspectRatio(16 / 9, contentMode: .fit)
                    .padding(.trailing, 16)
                    .padding(.bottom, 92)
                    .rivuneTransition(.move(edge: .trailing).combined(with: .opacity))
                    .zIndex(20)
            }
        }
        .tint(RivuneIOSTheme.ember)
        .accentColor(RivuneIOSTheme.ember)
        .preferredColorScheme(.dark)
        .environment(\.rivuneAnimationPreference, model.animationPreference)
        .task {
            if scenePhase == .active { model.handleSceneActive() } else { model.handleSceneBackground() }
            model.start()
        }
        .onChange(of: scenePhase) { phase in
            if phase == .active { model.handleSceneActive() } else { model.handleSceneBackground() }
        }
        .sheet(item: Binding(
            get: { model.pendingOfflineProfile },
            set: { if $0 == nil { offlinePIN = ""; model.dismissOfflineUnlock() } }
        )) { profile in
            RivuneIOSOfflineUnlockView(profile: profile, pin: $offlinePIN, model: model)
        }
        .alert(
            "Rivune \(model.updateNotice?.latestVersion ?? "") is available",
            isPresented: Binding(
                get: { model.updateNotice != nil },
                set: { if !$0 { model.dismissUpdateNotice() } }
            ),
            presenting: model.updateNotice
        ) { update in
            Button("Open release") {
                model.dismissUpdateNotice()
                openURL(update.releaseURL)
            }
            Button("Later", role: .cancel, action: model.dismissUpdateNotice)
        } message: { _ in
            Text("Rivune does not install Apple updates automatically. Open the verified GitHub release to download the unsigned package and follow its installation instructions.")
        }
        .rivuneAnimation(.easeInOut(duration: 0.22), value: model.destination)
        .mediaPlayerPresentation(item: Binding(
            get: { model.mediaDetail == nil ? model.playbackPresentation : nil },
            set: { _ in }
        )) { presentation in
            RivuneInternalPlayerView(presentation: presentation, model: model)
        }
    }
}

private struct RivuneIOSServerView: View {
    @ObservedObject var model: RivuneAppModel
    @StateObject private var browser = RivuneLANBrowser()
    @State private var address = ""
    @State private var selectedServer: DiscoveredRivuneServer?
    @State private var searching = true
    @State private var discoveryGeneration = 0

    var body: some View {
        RivuneIOSAccessPage {
            VStack(alignment: .leading, spacing: 24) {
                RivuneIOSHeading(
                    eyebrow: "Self-hosted",
                    title: "Your media starts here",
                    message: "Connect to the Rivune server you control. The app ships with no account, catalog, or hosted service."
                )

                nearbySection
                manualSection

                if !model.offlineProfiles.isEmpty {
                    offlineSection
                }
            }
        }
        .onAppear {
            address = model.serverAddress
            refreshDiscovery()
        }
        .onDisappear {
            discoveryGeneration += 1
            browser.stop()
        }
        .onChange(of: address) { _ in model.clearFailure() }
        .onChange(of: browser.servers) { servers in
            if !servers.isEmpty { searching = false }
        }
        .task(id: discoveryGeneration) {
            guard discoveryGeneration > 0, searching else { return }
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            guard !Task.isCancelled else { return }
            searching = false
        }
        .confirmationDialog(
            selectedServer.map { rivuneLocalizedFormat("Connect to %@?", $0.name) } ?? rivuneLocalized("Connect to this server?"),
            isPresented: Binding(
                get: { selectedServer != nil },
                set: { if !$0 { selectedServer = nil } }
            ),
            titleVisibility: .visible,
            presenting: selectedServer
        ) { server in
            Button("Connect") {
                selectedServer = nil
                address = server.address.absoluteString
                model.connect(to: server.address.absoluteString)
            }
            Button("Cancel", role: .cancel) { selectedServer = nil }
        } message: { server in
            Text(server.address.absoluteString + "\n\n" + rivuneLocalized(server.usesSecureTransport ? "Encrypted HTTPS connection." : "Unencrypted HTTP. Continue only on a trusted private network."))
        }
    }

    private var nearbySection: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text("Nearby servers")
                        .font(.headline)
                        .foregroundStyle(RivuneIOSTheme.primaryText)
                    Text("Found on this local network")
                        .font(.caption)
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                }
                Spacer()
                if searching {
                    ProgressView().tint(RivuneIOSTheme.ember)
                        .frame(width: 48, height: 48)
                } else {
                    Button(action: refreshDiscovery) {
                        Image(systemName: "arrow.clockwise")
                    }
                    .rivuneIOSIconButton()
                    .accessibilityLabel(rivuneLocalized("Refresh servers"))
                }
            }

            if browser.servers.isEmpty {
                HStack(spacing: 12) {
                    Image(systemName: searching ? "dot.radiowaves.left.and.right" : "network.slash")
                        .font(.title3)
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                    Text(searching ? "Looking for Rivune servers…" : "No server was found automatically.")
                        .font(.callout)
                        .foregroundStyle(RivuneIOSTheme.secondaryText)
                }
                .frame(maxWidth: .infinity, minHeight: 60, alignment: .leading)
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(browser.servers.enumerated()), id: \.element.id) { index, server in
                        Button { selectedServer = server } label: {
                            HStack(spacing: 14) {
                                Image(systemName: server.usesSecureTransport ? "lock.fill" : "network")
                                    .font(.headline)
                                    .foregroundStyle(server.usesSecureTransport ? RivuneIOSTheme.success : RivuneIOSTheme.ember)
                                    .frame(width: 36, height: 36)
                                    .background(RivuneIOSTheme.raised, in: Circle())
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(server.name)
                                        .font(.headline)
                                        .foregroundStyle(RivuneIOSTheme.primaryText)
                                    Text(server.address.absoluteString)
                                        .font(.caption.monospaced())
                                        .foregroundStyle(RivuneIOSTheme.mutedText)
                                        .lineLimit(1)
                                }
                                Spacer()
                                Image(systemName: "chevron.right")
                                    .font(.caption.bold())
                                    .foregroundStyle(RivuneIOSTheme.mutedText)
                            }
                            .padding(.vertical, 14)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        if index < browser.servers.count - 1 {
                            Divider().overlay(RivuneIOSTheme.hairline)
                        }
                    }
                }
            }
        }
        .rivuneIOSCard()
    }

    private var manualSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Server address")
                .font(.headline)
                .foregroundStyle(RivuneIOSTheme.primaryText)
            HStack(spacing: 8) {
                TextField("https://rivune.example.com", text: $address)
                    .font(.body.monospaced())
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                    .textInputAutocapitalization(.never)
                    .keyboardType(.URL)
                    .autocorrectionDisabled()
                    .submitLabel(.go)
                    .onSubmit(submit)
                    .padding(.leading, 16)
                    .frame(maxWidth: .infinity, minHeight: 54)
                    .accessibilityIdentifier("server-address")

                Button(action: submit) {
                    Group {
                        if model.isBusy {
                            ProgressView().tint(.black)
                        } else {
                            Image(systemName: model.failure == nil ? "arrow.right" : "arrow.clockwise")
                                .font(.body.weight(.bold))
                        }
                    }
                    .foregroundStyle(Color.black.opacity(0.88))
                    .frame(width: 44, height: 44)
                    .background(RivuneIOSTheme.ember, in: Circle())
                }
                .buttonStyle(.plain)
                .disabled(model.isBusy || address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityLabel(rivuneLocalized(model.isBusy ? "Connecting…" : model.failure == nil ? "Connect" : "Try again"))
                .accessibilityIdentifier("server-connect")
                .padding(.trailing, 5)
            }
            .background(RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .stroke(model.failure == nil ? RivuneIOSTheme.outline : RivuneIOSTheme.danger, lineWidth: 1)
            }

            RivuneIOSErrorMessage(failure: model.failure)

            Text("Private-network addresses use HTTP when no scheme is supplied. Public addresses default to HTTPS.")
                .font(.footnote)
                .foregroundStyle(RivuneIOSTheme.mutedText)
                .fixedSize(horizontal: false, vertical: true)

        }
    }

    private var offlineSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            RivuneIOSSectionHeader(title: "Downloads", subtitle: "Available without the server")
            ForEach(model.offlineProfiles) { profile in
                Button { model.requestOfflineUnlock(profile) } label: {
                    HStack(spacing: 12) {
                        Image(systemName: profile.requiresPIN ? "lock.fill" : "arrow.down.circle.fill")
                            .foregroundStyle(RivuneIOSTheme.ember)
                        Text(profile.name)
                        Spacer()
                        Image(systemName: "chevron.right")
                            .font(.caption.bold())
                            .foregroundStyle(RivuneIOSTheme.mutedText)
                    }
                    .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
            RivuneIOSErrorMessage(failure: model.offlineUnlockFailure)
        }
        .padding(.top, 8)
    }

    private func refreshDiscovery() {
        discoveryGeneration += 1
        searching = true
        browser.start()
    }

    private func submit() {
        let value = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return }
        model.connect(to: value)
    }
}

private struct RivuneIOSPairingView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var copied = false
    @State private var confirmDisconnect = false

    var body: some View {
        RivuneIOSPage(maximumWidth: 620, verticalAlignment: .top) {
            VStack(alignment: .leading, spacing: 24) {
                RivuneIOSHeading(
                    eyebrow: model.serverName,
                    title: model.pairingAccepted ? "Device paired" : "Pair this device",
                    message: model.pairingAccepted
                        ? "Approval received. Your profiles are loading now."
                        : "This device is waiting for approval from a Rivune administrator."
                )

                pairingCard
                    .padding(.top, 16)

                if model.failure != nil {
                    RivuneIOSErrorMessage(failure: model.failure)
                }
            }
            .padding(.top, 44)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            GeometryReader { proxy in
                HStack(spacing: 16) {
                    RivuneIOSBrand()
                    Spacer()
                    Button(role: .destructive) { confirmDisconnect = true } label: {
                        Image(systemName: "rectangle.portrait.and.arrow.right")
                            .font(.body.weight(.semibold))
                    }
                    .rivuneIOSIconButton(destructive: true)
                    .accessibilityLabel(rivuneLocalized("Disconnect"))
                }
                .padding(.horizontal, RivuneIOSTheme.pageInset(for: proxy.size.width))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .frame(height: 64)
        }
        .onChange(of: model.pairingCode) { _ in copied = false }
        .confirmationDialog("Disconnect from \(model.serverName)?", isPresented: $confirmDisconnect) {
            Button("Disconnect", role: .destructive, action: model.disconnect)
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The locally stored session for this server will be removed.")
        }
    }

    private var pairingCard: some View {
        VStack(spacing: 20) {
            ZStack {
                Circle()
                    .fill(model.pairingAccepted ? RivuneIOSTheme.success.opacity(0.14) : RivuneIOSTheme.ember.opacity(0.14))
                    .frame(width: 64, height: 64)
                Image(systemName: model.pairingAccepted ? "checkmark" : "link")
                    .font(.system(size: 26, weight: .bold))
                    .foregroundStyle(model.pairingAccepted ? RivuneIOSTheme.success : RivuneIOSTheme.ember)
            }

            if model.pairingAccepted {
                Text("Approved")
                    .font(.title2.bold())
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                ProgressView().tint(RivuneIOSTheme.ember)
            } else if let code = model.pairingCode {
                Text("One-time code")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(RivuneIOSTheme.mutedText)
                Button {
                    UIPasteboard.general.string = code
                    copied = true
                } label: {
                    VStack(spacing: 10) {
                        Text(code)
                            .font(.system(size: 38, weight: .black, design: .monospaced))
                            .tracking(3)
                            .foregroundStyle(RivuneIOSTheme.primaryText)
                            .minimumScaleFactor(0.7)
                            .lineLimit(1)
                        Label(copied ? "Copied" : "Tap to copy", systemImage: copied ? "checkmark" : "doc.on.doc")
                            .font(.footnote.weight(.semibold))
                            .foregroundStyle(RivuneIOSTheme.ember)
                    }
                    .frame(maxWidth: .infinity)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("pairing-code")

                Text("The code expires automatically. Keep this screen open while approving the device.")
                    .font(.footnote)
                    .foregroundStyle(RivuneIOSTheme.mutedText)
                    .multilineTextAlignment(.center)
            } else if let seconds = model.pairingRetrySeconds {
                ProgressView().tint(RivuneIOSTheme.ember)
                Label(
                    rivuneLocalizedFormat("Retrying automatically in %d s", seconds),
                    systemImage: "clock.arrow.circlepath"
                )
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(RivuneIOSTheme.secondaryText)
                .multilineTextAlignment(.center)
            } else {
                ProgressView().tint(RivuneIOSTheme.ember)
                Text("Requesting a code")
                    .foregroundStyle(RivuneIOSTheme.secondaryText)
            }
        }
        .frame(maxWidth: .infinity)
        .rivuneIOSCard(inset: 24)
    }
}

private struct RivuneIOSProfilesView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var pendingProfile: Profile?
    @State private var pin = ""
    @State private var confirmDisconnect = false

    var body: some View {
        Group {
            if let profile = pendingProfile {
                RivuneIOSPINView(
                    profile: profile,
                    imageData: model.profileAvatarData[profile.id],
                    pin: $pin,
                    failure: model.failure,
                    busy: model.isBusy,
                    clearFailure: model.clearFailure,
                    cancel: cancelPIN,
                    submit: submitPIN
                )
            } else {
                profilePicker
            }
        }
        .onChange(of: model.destination) { destination in
            if destination != .profiles { cancelPIN() }
        }
        .confirmationDialog("Disconnect from \(model.serverName)?", isPresented: $confirmDisconnect) {
            Button("Disconnect", role: .destructive, action: model.disconnect)
            Button("Cancel", role: .cancel) {}
        }
    }

    private var profilePicker: some View {
        GeometryReader { proxy in
            let compact = proxy.size.width < 700
            let minimum: CGFloat = compact ? 96 : 112
            let maximum: CGFloat = compact ? 116 : 140
            let spacing: CGFloat = compact ? 16 : 24
            ZStack {
                RivuneIOSCanvas()
                RivuneIOSPage(maximumWidth: 1000) {
                    VStack(alignment: .leading, spacing: 28) {
                        HStack(spacing: 12) {
                            RivuneIOSBrand(compact: true)
                            Spacer()
                            Button { confirmDisconnect = true } label: {
                                Image(systemName: "rectangle.portrait.and.arrow.right")
                            }
                            .rivuneIOSIconButton(destructive: true)
                            .accessibilityLabel(rivuneLocalized("Disconnect"))
                        }

                        RivuneIOSHeading(
                            eyebrow: model.serverName,
                            title: "Who’s watching?",
                            message: "Choose a profile. Access and PIN rules are managed by your server."
                        )
                        RivuneIOSErrorMessage(failure: model.failure)

                        LazyVGrid(
                            columns: [GridItem(.adaptive(minimum: minimum, maximum: maximum), spacing: spacing)],
                            alignment: .center,
                            spacing: 24
                        ) {
                            ForEach(model.profiles) { profile in
                                profileButton(profile)
                            }
                        }
                        .padding(.top, 12)
                    }
                }
            }
        }
    }

    private func profileButton(_ profile: Profile) -> some View {
        let status = profileStatus(profile)

        return Button { select(profile) } label: {
            VStack(spacing: 10) {
                ProfileAvatarImage(profile: profile, imageData: model.profileAvatarData[profile.id])
                    .frame(width: 88, height: 88)
                    .clipShape(Circle())
                    .overlay { Circle().stroke(RivuneIOSTheme.outline, lineWidth: 1) }
                    .saturation(profile.accessible ? 1 : 0)
                    .opacity(profile.accessible ? 1 : 0.52)
                    .overlay(alignment: .bottomTrailing) {
                        if profile.hasPin {
                            Image(systemName: "lock.fill")
                                .font(.caption2.weight(.bold))
                                .foregroundStyle(RivuneIOSTheme.primaryText)
                                .frame(width: 26, height: 26)
                                .background(RivuneIOSTheme.raised, in: Circle())
                                .overlay { Circle().stroke(RivuneIOSTheme.outline, lineWidth: 1) }
                        }
                    }

                VStack(spacing: 3) {
                    Text(profile.name)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(profile.accessible ? RivuneIOSTheme.primaryText : RivuneIOSTheme.mutedText)
                        .lineLimit(1)

                    if let status {
                        Text(rivuneLocalized(status))
                            .font(.caption2.weight(.medium))
                            .foregroundStyle(RivuneIOSTheme.mutedText)
                            .multilineTextAlignment(.center)
                            .lineLimit(2)
                    }
                }
                .frame(minHeight: 36, alignment: .top)
            }
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(model.isBusy || !profile.accessible)
        .accessibilityLabel(profileAccessibilityLabel(profile))
    }

    private func profileStatus(_ profile: Profile) -> String? {
        guard !profile.accessible else { return nil }
        return profile.enabled ? "Outside access hours" : "Disabled"
    }

    private func profileAccessibilityLabel(_ profile: Profile) -> String {
        let name = profile.hasPin ? rivuneLocalizedFormat("%@, PIN required", profile.name) : profile.name
        guard let status = profileStatus(profile) else { return name }
        return rivuneLocalizedFormat("%@. %@", name, rivuneLocalized(status))
    }

    private func select(_ profile: Profile) {
        model.clearFailure()
        if profile.hasPin {
            pin = ""
            pendingProfile = profile
        } else {
            model.selectProfile(profile)
        }
    }

    private func cancelPIN() {
        pin = ""
        pendingProfile = nil
        model.clearFailure()
    }

    private func submitPIN() {
        guard let pendingProfile else { return }
        let normalized = String(pin.filter(\.isNumber).prefix(8))
        guard normalized.count >= 4 else { return }
        pin = normalized
        model.selectProfile(pendingProfile, pin: normalized)
    }
}

private struct RivuneIOSPINView: View {
    let profile: Profile
    let imageData: Data?
    @Binding var pin: String
    let failure: RivuneAppFailure?
    let busy: Bool
    let clearFailure: () -> Void
    let cancel: () -> Void
    let submit: () -> Void
    @FocusState private var focused: Bool

    private var canSubmit: Bool { (4...8).contains(pin.count) && !busy }

    var body: some View {
        ZStack(alignment: .topLeading) {
            RivuneIOSCanvas()

            RivuneIOSPage(maximumWidth: 520, verticalAlignment: .center) {
                VStack(spacing: 20) {
                    ProfileAvatarImage(profile: profile, imageData: imageData)
                        .frame(width: 80, height: 80)
                        .clipShape(Circle())
                        .overlay { Circle().stroke(RivuneIOSTheme.outline, lineWidth: 1) }

                    RivuneIOSHeading(
                        title: "Enter PIN",
                        message: rivuneLocalizedFormat("Unlock %@ to continue.", profile.name),
                        centered: true
                    )

                    ZStack {
                        HStack(spacing: 12) {
                            ForEach(0..<8, id: \.self) { index in
                                Circle()
                                    .fill(index < pin.count ? RivuneIOSTheme.ember : Color.white.opacity(0.08))
                                    .frame(width: 12, height: 12)
                                    .overlay {
                                        Circle()
                                            .stroke(index < pin.count ? RivuneIOSTheme.ember.opacity(0.8) : Color.white.opacity(0.18), lineWidth: 1)
                                    }
                            }
                        }
                        SecureField("PIN", text: $pin)
                            .focused($focused)
                            .keyboardType(.numberPad)
                            .textContentType(.oneTimeCode)
                            .foregroundStyle(.clear)
                            .tint(.clear)
                            .opacity(0.02)
                            .onChange(of: pin, perform: normalize)
                    }
                    .frame(maxWidth: 360, minHeight: 64)
                    .rivuneIOSGlassSurface(hasFailure: failure != nil)
                    .contentShape(Capsule())
                    .onTapGesture { focused = true }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("PIN")
                    .accessibilityValue(rivuneLocalizedFormat("%d digits entered", pin.count))

                    Group {
                        if let failure {
                            RivuneIOSInlineError(
                                message: pinFailureMessage(failure),
                                centered: true
                            )
                        } else {
                            Text(pin.isEmpty ? "Enter at least 4 digits" : rivuneLocalizedFormat("%d of 8 digits", pin.count))
                                .font(.caption)
                                .foregroundStyle(RivuneIOSTheme.mutedText)
                        }
                    }
                    .frame(maxWidth: 360, minHeight: 18)

                    unlockButton
                }
                .padding(.bottom, 72)
                .frame(maxWidth: .infinity)
            }

            Button(action: cancel) {
                Image(systemName: "chevron.left")
                    .font(.body.weight(.semibold))
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                    .frame(width: 30, height: 30)
            }
            .rivuneIOSGlassButton()
            .disabled(busy)
            .accessibilityLabel(rivuneLocalized("Back"))
            .padding(.leading, 20)
            .padding(.top, 20)
        }
        .onAppear { DispatchQueue.main.async { focused = true } }
    }

    @ViewBuilder
    private var unlockButton: some View {
        if #available(iOS 26.0, *) {
            GlassEffectContainer(spacing: 0) {
                unlockAction
            }
        } else {
            unlockAction
        }
    }

    private var unlockAction: some View {
        Button(action: submit) {
            HStack(spacing: 8) {
                if busy { ProgressView().tint(RivuneIOSTheme.ember) }
                Text(busy ? "Unlocking…" : "Unlock")
                if !busy { Image(systemName: "lock.open.fill") }
            }
            .foregroundStyle(RivuneIOSTheme.ember)
            .padding(.horizontal, 8)
            .frame(minHeight: 50)
        }
        .fixedSize(horizontal: true, vertical: false)
        .rivuneIOSGlassButton()
        .disabled(!canSubmit)
    }

    private func pinFailureMessage(_ failure: RivuneAppFailure) -> String {
        failure == .invalidPin
            ? rivuneLocalized("Incorrect PIN. Try again.")
            : rivuneLocalized(failure.localizedDescription)
    }

    private func normalize(_ value: String) {
        let normalized = String(value.filter(\.isNumber).prefix(8))
        if failure != nil { clearFailure() }
        if normalized != value { pin = normalized }
        if normalized.count == 8 && !busy { submit() }
    }
}

private struct RivuneIOSOfflineUnlockView: View {
    let profile: RivuneOfflineProfileAccess
    @Binding var pin: String
    @ObservedObject var model: RivuneAppModel
    @FocusState private var focused: Bool

    var body: some View {
        ZStack {
            RivuneIOSCanvas()
            VStack(spacing: 20) {
                Capsule()
                    .fill(RivuneIOSTheme.outline)
                    .frame(width: 42, height: 5)
                    .padding(.top, 10)
                Image(systemName: "arrow.down.circle.fill")
                    .font(.system(size: 42))
                    .foregroundStyle(RivuneIOSTheme.ember)
                RivuneIOSHeading(
                    title: "Unlock \(profile.name)",
                    message: "Downloads stay on this device and remain protected by the profile PIN.",
                    centered: true
                )
                SecureField("Profile PIN", text: $pin)
                    .focused($focused)
                    .keyboardType(.numberPad)
                    .onChange(of: pin) { value in
                        let normalized = String(value.filter(\.isNumber).prefix(8))
                        if normalized != value { pin = normalized }
                    }
                    .padding(.horizontal, 16)
                    .frame(minHeight: 54)
                    .background(RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                    .overlay { RoundedRectangle(cornerRadius: 14).stroke(RivuneIOSTheme.outline, lineWidth: 1) }
                RivuneIOSErrorMessage(failure: model.offlineUnlockFailure)
                HStack(spacing: 12) {
                    Button("Cancel") {
                        pin = ""
                        model.dismissOfflineUnlock()
                    }
                    .rivuneIOSSecondaryButton()
                    Button("Unlock") {
                        let normalized = String(pin.filter(\.isNumber).prefix(8))
                        model.unlockOfflineProfile(profile, pin: normalized)
                        if model.offlineAccessUnlocked { pin = "" }
                    }
                    .rivuneIOSPrimaryButton()
                    .disabled(pin.filter(\.isNumber).count < 4)
                }
            }
            .padding(24)
            .frame(maxWidth: 520)
        }
        .onAppear { focused = true }
    }
}
#endif
