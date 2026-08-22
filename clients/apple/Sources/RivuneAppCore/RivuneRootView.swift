import SwiftUI
import RivuneAPI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

@MainActor
public struct RivuneRootView: View {
    @StateObject private var model: RivuneAppModel
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.openURL) private var openURL
    @State private var offlinePIN = ""
#if os(tvOS)
    @State private var televisionUpdate: RivuneAppleUpdate?
#endif

    public init(model: RivuneAppModel) {
        _model = StateObject(wrappedValue: model)
    }

    public init() {
        _model = StateObject(wrappedValue: RivuneAppModel())
    }

    public var body: some View {
        ZStack(alignment: .bottomTrailing) {
            RivuneBackground()
            Group {
                switch model.destination {
                case .server: ServerView(model: model)
                case .pairing: PairingView(model: model)
                case .profiles: ProfilesView(model: model)
                case .library: LibraryView(model: model)
                }
            }
            .transition(.opacity)
            if let presentation = model.minimizedPlaybackPresentation {
                RivuneMiniPlayerView(presentation: presentation, model: model)
                    .frame(maxWidth: 340)
                    .aspectRatio(16 / 9, contentMode: .fit)
                    .padding(.horizontal, 16)
                    .padding(.bottom, 92)
                    .transition(.move(edge: .trailing).combined(with: .opacity))
                    .zIndex(20)
            }
        }
        .tint(RivunePalette.color(for: model.accent))
        .accentColor(RivunePalette.color(for: model.accent))
        .preferredColorScheme(.dark)
        .task { model.start() }
        .onChange(of: scenePhase) { phase in
            if phase != .active { model.handleSceneBackground() }
        }
        .alert(
            "Rivune \(model.updateNotice?.latestVersion ?? "") is available",
            isPresented: Binding(
                get: { model.updateNotice != nil },
                set: { if !$0 { model.dismissUpdateNotice() } }
            ),
            presenting: model.updateNotice
        ) { update in
#if os(tvOS)
            Button("View release QR code") {
                model.dismissUpdateNotice()
                televisionUpdate = update
            }
#else
            Button("Open release") {
                model.dismissUpdateNotice()
                openURL(update.releaseURL)
            }
#endif
            Button("Later", role: .cancel, action: model.dismissUpdateNotice)
        } message: { _ in
#if os(tvOS)
            Text("Rivune does not install Apple updates automatically. Scan the release QR code on another device to download and prepare the unsigned package.")
#else
            Text("Rivune does not install Apple updates automatically. Open the verified GitHub release to download the unsigned package and follow its installation instructions.")
#endif
        }
        .animation(.easeInOut(duration: 0.22), value: model.destination)
        .mediaPlayerPresentation(item: Binding(
            get: { model.mediaDetail == nil ? model.playbackPresentation : nil },
            set: { _ in }
        )) { presentation in
            RivuneInternalPlayerView(presentation: presentation, model: model)
        }
        .sheet(item: Binding(
            get: { model.pendingOfflineProfile },
            set: { if $0 == nil { offlinePIN = ""; model.dismissOfflineUnlock() } }
        )) { profile in
            OfflineUnlockView(profile: profile, pin: $offlinePIN, model: model)
        }
#if os(tvOS)
        .sheet(item: $televisionUpdate) { update in
            RivuneTelevisionUpdateView(update: update)
        }
#endif
    }
}

private enum RivunePalette {
    static let canvas = Color.black
    static let softCanvas = Color.black
    static let surface = Color(red: 0.075, green: 0.075, blue: 0.075)
    static let raised = Color(red: 0.10, green: 0.10, blue: 0.10)
    static let accent = Color.accentColor

    static func color(for accent: RivuneAccent) -> Color {
        switch accent {
        case .blue: return Color(red: 0.47, green: 0.65, blue: 1.0)
        case .coral: return Color(red: 1.0, green: 0.56, blue: 0.44)
        case .green: return Color(red: 0.44, green: 0.79, blue: 0.60)
        case .violet: return Color(red: 0.76, green: 0.60, blue: 1.0)
        case .rose: return Color(red: 1.0, green: 0.49, blue: 0.55)
        }
    }
    static let secondary = Color.white.opacity(0.68)
}
private struct RivuneGlassButtonModifier: ViewModifier {
    let prominent: Bool

    @ViewBuilder
    func body(content: Content) -> some View {
#if os(visionOS)
        if prominent {
            content.buttonStyle(.borderedProminent)
        } else {
            content.buttonStyle(.bordered)
        }
#else
        if #available(iOS 26.0, tvOS 26.0, macOS 26.0, *) {
            if prominent {
                content.buttonStyle(.glassProminent)
            } else {
                content.buttonStyle(.glass)
            }
        } else if prominent {
            content.buttonStyle(.borderedProminent)
        } else {
            content.buttonStyle(.bordered)
        }
#endif
    }
}

private extension View {
    func rivuneGlassButton(prominent: Bool = false) -> some View {
        modifier(RivuneGlassButtonModifier(prominent: prominent))
    }

    func rivuneDestructiveButton() -> some View {
        buttonStyle(.plain)
            .foregroundStyle(.red)
    }
}


private struct RivuneBackground: View {
    var body: some View {
        Color.black
            .ignoresSafeArea()
    }
}

private struct Brand: View {
    var compact = false

    var body: some View {
        HStack(spacing: compact ? 10 : 14) {
            rivuneMark
                .resizable()
                .scaledToFit()
                .frame(width: compact ? 34 : 44, height: compact ? 34 : 44)
            Text("Rivune")
                .font(.system(size: compact ? 24 : 32, weight: .bold, design: .rounded))
                .fixedSize(horizontal: true, vertical: false)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Rivune")
    }
    private var rivuneMark: Image {
#if canImport(UIKit)
        let path = Bundle.module.path(forResource: "RivuneMark", ofType: "png")!
        return Image(uiImage: UIImage(contentsOfFile: path)!)
#elseif canImport(AppKit)
        let path = Bundle.module.path(forResource: "RivuneMark", ofType: "png")!
        return Image(nsImage: NSImage(contentsOfFile: path)!)
#endif
    }
}


private struct ScreenHeading: View {
    let eyebrow: String
    let title: String
    let bodyText: String?
    var centered = false

    var body: some View {
        VStack(alignment: centered ? .center : .leading, spacing: 10) {
            Text(eyebrow.uppercased())
                .font(.caption.weight(.bold))
                .tracking(1.8)
                .foregroundStyle(RivunePalette.accent)
            Text(title)
                .font(.largeTitle.bold())
                .multilineTextAlignment(centered ? .center : .leading)
            if let bodyText {
                Text(bodyText)
                    .font(.body)
                    .foregroundStyle(RivunePalette.secondary)
                    .multilineTextAlignment(centered ? .center : .leading)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: centered ? .center : .leading)
    }
}

private struct AuthFrame<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        GeometryReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 30) {
                    Brand()
                    content
                }
                .frame(maxWidth: 560, alignment: .leading)
                .padding(.horizontal, proxy.size.width > 900 ? 72 : 24)
                .padding(.vertical, 32)
                .frame(minHeight: proxy.size.height, alignment: .center)
                .frame(maxWidth: .infinity)
            }
        }
    }
}

private struct FailureText: View {
    let failure: RivuneAppFailure?

    var body: some View {
        if let failure {
            Label(failure.localizedDescription, systemImage: "exclamationmark.triangle.fill")
                .font(.callout)
                .foregroundStyle(Color.red.opacity(0.92))
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("failure-message")
        }
    }
}

private struct OfflineUnlockView: View {
    let profile: RivuneOfflineProfileAccess
    @Binding var pin: String
    @ObservedObject var model: RivuneAppModel

    var body: some View {
        NavigationView {
            VStack(spacing: 18) {
                Image(systemName: "lock.fill").font(.largeTitle)
                Text("Unlock \(profile.name)").font(.title2.bold())
                SecureField("Profile PIN", text: $pin)
#if os(tvOS)
                    .textFieldStyle(.plain).padding(.horizontal, 16).frame(minHeight: 54)
                    .background(RivunePalette.raised, in: RoundedRectangle(cornerRadius: 13, style: .continuous))
#else
                    .textFieldStyle(.roundedBorder)
#endif
#if os(iOS) || os(visionOS)
                    .keyboardType(.numberPad)
#endif
                FailureText(failure: model.offlineUnlockFailure)
                HStack {
                    Button("Cancel") { pin = ""; model.dismissOfflineUnlock() }
                    Button("Unlock") {
                        let normalized = String(pin.filter(\.isNumber).prefix(8))
                        model.unlockOfflineProfile(profile, pin: normalized)
                        if model.offlineAccessUnlocked { pin = "" }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(pin.filter(\.isNumber).count < 4)
                }
            }
            .padding(28).frame(maxWidth: 480)
        }
    }
}

private struct PrimaryButton: View {
    let title: String
    let busy: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                if busy { ProgressView().tint(.black) }
                Text(title).fontWeight(.bold)
                Spacer(minLength: 0)
                if !busy { Image(systemName: "arrow.right") }
            }
            .padding(.horizontal, 18)
            .frame(minHeight: 52)
        }
        .rivuneGlassButton(prominent: true)
        .tint(RivunePalette.accent)
        .disabled(busy)
    }
}

private struct ServerView: View {
    @ObservedObject var model: RivuneAppModel
    @StateObject private var browser = RivuneLANBrowser()
    @State private var address = ""
    @State private var selectedServer: DiscoveredRivuneServer?
    @State private var discoveryGeneration = 0
    @State private var isSearching = true

    var body: some View {
        AuthFrame {
            ScreenHeading(
                eyebrow: "Your server",
                title: "Connect to Rivune",
                bodyText: "Choose a Rivune server found on this network, or enter its address manually. This app has no public catalog or hosted account."
            )
            serverSections
            offlineProfilesSection
            UpdateStatusCard(model: model)
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
            if !servers.isEmpty { isSearching = false }
        }
        .task(id: discoveryGeneration) {
            guard discoveryGeneration > 0, isSearching else { return }
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            guard !Task.isCancelled else { return }
            isSearching = false
        }
        .confirmationDialog(
            selectedServer.map { "Connect to \($0.name)?" } ?? "Connect to this server?",
            isPresented: Binding(
                get: { selectedServer != nil },
                set: { if !$0 { selectedServer = nil } }
            ),
            titleVisibility: .visible,
            presenting: selectedServer
        ) { server in
            Button("Connect") {
                selectedServer = nil
                model.connect(to: server.address.absoluteString)
            }
            Button("Cancel", role: .cancel) { selectedServer = nil }
        } message: { server in
            Text("\(server.address.absoluteString)\n\n\(server.usesSecureTransport ? "Encrypted HTTPS connection." : "Unencrypted HTTP. Continue only on a trusted private network.")")
        }
    }

    private var serverSections: some View {
        VStack(alignment: .leading, spacing: sectionSpacing) {
            LANDiscoveryCard(
                servers: browser.servers,
                isSearching: isSearching,
                refresh: refreshDiscovery,
                select: { selectedServer = $0 }
            )
            serverAddressSection
        }
    }

    private var serverAddressSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("SERVER ADDRESS")
                .font(.caption.weight(.semibold))
                .foregroundStyle(RivunePalette.secondary)
            TextField("https://rivune.example.com", text: $address)
                .textFieldStyle(.plain)
                .font(.body.monospaced())
                .padding(.horizontal, 16)
                .frame(minHeight: addressFieldHeight)
                .background(RivunePalette.raised, in: RoundedRectangle(cornerRadius: 13, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: 13, style: .continuous)
                        .stroke(addressStrokeColor, lineWidth: 1)
                }
#if os(iOS) || os(visionOS)
                .textInputAutocapitalization(.never)
                .keyboardType(.URL)
                .autocorrectionDisabled()
#endif
                .onSubmit(submit)
                .accessibilityIdentifier("server-address")
            FailureText(failure: model.failure)
            Text("Localhost and private-network addresses use HTTP when no scheme is supplied. Public addresses default to HTTPS. Use HTTP only on a trusted local network.")
                .font(.footnote)
                .foregroundStyle(RivunePalette.secondary)
            PrimaryButton(title: connectButtonTitle, busy: model.isBusy, action: submit)
                .disabled(address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("server-connect")
        }
    }

    @ViewBuilder
    private var offlineProfilesSection: some View {
        if !model.offlineProfiles.isEmpty {
            VStack(alignment: .leading, spacing: 14) {
                Text("OFFLINE PROFILES")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(RivunePalette.secondary)
                ForEach(model.offlineProfiles) { profile in
                    offlineProfileButton(profile)
                }
                if model.offlineAccessUnlocked {
                    Button("Lock offline access") { model.lockOffline() }.buttonStyle(.bordered)
                }
                FailureText(failure: model.offlineUnlockFailure)
            }
            if !model.offlineItems.isEmpty {
                OfflineMediaSection(items: model.offlineItems, model: model, availableWidth: 620)
                    .padding(.top, 8)
            }
        }
    }

    private func offlineProfileButton(_ profile: RivuneOfflineProfileAccess) -> some View {
        let icon = profile.requiresPIN ? "lock.fill" : "arrow.down.circle.fill"
        return Button { model.requestOfflineUnlock(profile) } label: {
            Label(profile.name, systemImage: icon)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .rivuneGlassButton(prominent: false)
    }

    private var addressStrokeColor: Color {
        model.failure == nil ? Color.white.opacity(0.10) : Color.red.opacity(0.75)
    }

    private var connectButtonTitle: String {
        if model.isBusy { return "Connecting…" }
        return model.failure == nil ? "Connect" : "Try again"
    }

    private var sectionSpacing: CGFloat {
#if os(tvOS)
        30
#else
        24
#endif
    }

    private var addressFieldHeight: CGFloat {
#if os(tvOS)
        64
#else
        54
#endif
    }

    private func refreshDiscovery() {
        discoveryGeneration += 1
        isSearching = true
        browser.start()
    }

    private func submit() {
        let value = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return }
        model.connect(to: value)
    }
}

private struct UpdateStatusCard: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.openURL) private var openURL
#if os(tvOS)
    @State private var televisionUpdate: RivuneAppleUpdate?
#endif

    var body: some View {
        Group {
            switch model.updateState {
            case .available(let update):
                VStack(alignment: .leading, spacing: 12) {
                    Label("Rivune \(update.latestVersion) is available", systemImage: "arrow.down.circle.fill")
                        .font(.headline)
#if os(tvOS)
                    Text("Scan the release QR code on another device to download and prepare the unsigned Apple package.")
#else
                    Text("Open the verified GitHub release to download the unsigned Apple package and follow its installation instructions.")
#endif
                        .font(.footnote)
                        .foregroundStyle(RivunePalette.secondary)
#if os(tvOS)
                    Button("View release QR code") { televisionUpdate = update }
                        .buttonStyle(.borderedProminent)
#else
                    Button("Open release") { openURL(update.releaseURL) }
                        .buttonStyle(.borderedProminent)
#endif
                }
                .padding(18)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(RivunePalette.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            case .failed:
                HStack(spacing: 12) {
                    Label("The update check failed.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.yellow)
                    Spacer()
                    Button("Try again") { model.checkForUpdates() }.buttonStyle(.bordered)
                }
                .padding(18)
                .background(RivunePalette.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            default:
                EmptyView()
            }
        }
#if os(tvOS)
        .sheet(item: $televisionUpdate) { update in
            RivuneTelevisionUpdateView(update: update)
        }
#endif
    }
}


private struct LANDiscoveryCard: View {
    let servers: [DiscoveredRivuneServer]
    let isSearching: Bool
    let refresh: () -> Void
    let select: (DiscoveredRivuneServer) -> Void

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 14) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Nearby servers")
                        .font(.headline)
                    Text("Rivune on your local network")
                        .font(.caption)
                        .foregroundStyle(RivunePalette.secondary)
                }
                Spacer(minLength: 10)
                discoveryStatus
                Button(action: refresh) {
                    Group {
                        if isSearching {
                            ProgressView()
                                .tint(RivunePalette.secondary)
                        } else {
                            Image(systemName: "arrow.clockwise")
                                .font(.body.weight(.semibold))
                        }
                    }
                    .frame(width: refreshButtonSize, height: refreshButtonSize)
                    .background(RivunePalette.raised, in: Circle())
                }
                .buttonStyle(.plain)
                .disabled(isSearching)
                .accessibilityLabel(isSearching ? "Searching for nearby servers" : "Refresh nearby servers")
                .accessibilityIdentifier("server-discover")
            }
            .padding(cardPadding)

            Divider()

            if servers.isEmpty {
                LANDiscoveryEmptyState(isSearching: isSearching)
                    .padding(.horizontal, cardPadding)
                    .padding(.vertical, emptyStatePadding)
            } else {
                ForEach(servers) { server in
                    Button {
                        select(server)
                    } label: {
                        DiscoveredServerRow(server: server)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("discovered-server-\(server.id)")

                    if server.id != servers.last?.id {
                        Divider().padding(.leading, rowDividerInset)
                    }
                }
            }
        }
        .background(RivunePalette.surface)
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .stroke(Color.white.opacity(0.08), lineWidth: 1)
        }
    }

    @ViewBuilder
    private var discoveryStatus: some View {
        if servers.isEmpty && isSearching {
            Text("Searching")
                .font(.caption.weight(.semibold))
                .foregroundStyle(RivunePalette.secondary)
        } else if servers.isEmpty {
            Text("Listening")
                .font(.caption.weight(.semibold))
                .foregroundStyle(RivunePalette.secondary)
        } else {
            Text("\(servers.count) \(servers.count == 1 ? "server" : "servers")")
                .font(.caption.weight(.semibold))
                .foregroundStyle(RivunePalette.secondary)
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(RivunePalette.raised, in: Capsule())
        }
    }

    private var cardPadding: CGFloat {
#if os(tvOS)
        24
#else
        18
#endif
    }

    private var emptyStatePadding: CGFloat {
#if os(tvOS)
        40
#else
        28
#endif
    }

    private var refreshButtonSize: CGFloat {
#if os(tvOS)
        52
#else
        40
#endif
    }

    private var rowDividerInset: CGFloat {
#if os(tvOS)
        86
#else
        66
#endif
    }
}

private struct LANDiscoveryEmptyState: View {
    let isSearching: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            Group {
                if isSearching {
                    ProgressView()
                        .tint(RivunePalette.accent)
                } else {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(RivunePalette.secondary)
                }
            }
            .frame(width: 28, height: 28)

            VStack(alignment: .leading, spacing: 5) {
                Text(isSearching ? "Looking on your local network" : "No servers yet")
                    .font(.subheadline.weight(.semibold))
                Text(isSearching
                     ? "Nearby Rivune servers will appear here. This can take a few seconds."
                     : "Discovery is still active. Make sure Rivune is running and this device is on the same network.")
                    .font(.caption)
                    .foregroundStyle(RivunePalette.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .combine)
    }
}

private struct DiscoveredServerRow: View {
    @Environment(\.isFocused) private var isFocused
    let server: DiscoveredRivuneServer

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "network")
                .font(.body.weight(.semibold))
                .foregroundStyle(RivunePalette.accent)
                .frame(width: serverIconSize, height: serverIconSize)
                .background(RivunePalette.raised, in: RoundedRectangle(cornerRadius: 13, style: .continuous))
            VStack(alignment: .leading, spacing: 5) {
                Text(server.name)
                    .font(.body.weight(.semibold))
                    .lineLimit(1)
                Text(server.address.absoluteString)
                    .font(.caption.monospaced())
                    .foregroundStyle(RivunePalette.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            ServerTransportBadge(secure: server.usesSecureTransport)
            Image(systemName: "chevron.right")
                .font(.caption.weight(.bold))
                .foregroundStyle(RivunePalette.secondary)
        }
        .padding(.horizontal, rowHorizontalPadding)
        .padding(.vertical, rowVerticalPadding)
        .contentShape(Rectangle())
        .background(isFocused ? RivunePalette.raised : Color.clear)
        .foregroundStyle(.primary)
    }

    private var serverIconSize: CGFloat {
#if os(tvOS)
        48
#else
        38
#endif
    }

    private var rowHorizontalPadding: CGFloat {
#if os(tvOS)
        24
#else
        14
#endif
    }

    private var rowVerticalPadding: CGFloat {
#if os(tvOS)
        20
#else
        14
#endif
    }
}

private struct ServerTransportBadge: View {
    let secure: Bool

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: secure ? "lock.fill" : "wifi")
            Text(secure ? "HTTPS" : "LAN")
        }
        .font(.caption2.weight(.bold))
        .foregroundStyle(secure ? RivunePalette.accent : RivunePalette.secondary)
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(RivunePalette.raised, in: Capsule())
        .accessibilityLabel(secure ? "Secure HTTPS" : "Local network HTTP")
    }
}

private struct PairingView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var confirmDisconnect = false
    @State private var codeCopied = false

    var body: some View {
        AuthFrame {
            ScreenHeading(
                eyebrow: model.serverName,
                title: model.pairingAccepted ? "Device paired" : "Pair this device",
                bodyText: "Open your Rivune web interface, go to Devices, and enter this one-time code."
            )
            Group {
                if model.pairingAccepted {
                    PairingCard(icon: "checkmark", title: "Approved", subtitle: "Loading your profiles…", code: nil, codeCopied: false, copyCode: nil)
                } else if let code = model.pairingCode {
#if os(tvOS)
                    PairingCard(icon: "link", title: "One-time code", subtitle: "This code expires automatically.", code: code, codeCopied: false, copyCode: nil)
#else
                    PairingCard(icon: "link", title: "One-time code", subtitle: "This code expires automatically.", code: code, codeCopied: codeCopied) {
                        copyPairingCode(code)
                        codeCopied = true
                    }
#endif
                } else if model.isBusy {
                    PairingCard(icon: "ellipsis", title: "Requesting a code", subtitle: "Contacting your server…", code: nil, codeCopied: false, copyCode: nil)
                }
            }
            FailureText(failure: model.failure)
            if model.failure != nil && !model.isBusy {
                PrimaryButton(title: "Request a new code", busy: false, action: model.restartPairing)
                    .accessibilityIdentifier("pairing-retry")
            }
            Button(role: .destructive) { confirmDisconnect = true } label: {
                Label("Disconnect", systemImage: "rectangle.portrait.and.arrow.right")
                    .frame(maxWidth: .infinity, minHeight: 48)
            }
            .rivuneDestructiveButton()
        }
        .confirmationDialog("Disconnect from \(model.serverName)?", isPresented: $confirmDisconnect) {
            Button("Disconnect", role: .destructive, action: model.disconnect)
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The locally stored session for this server will be removed.")
        }
        .onChange(of: model.pairingCode) { _ in codeCopied = false }
    }
    private func copyPairingCode(_ code: String) {
#if os(iOS) || os(visionOS)
        UIPasteboard.general.string = code
#elseif os(macOS)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(code, forType: .string)
#endif
    }
}


private struct PairingCard: View {
    let icon: String
    let title: String
    let subtitle: String
    let code: String?
    let codeCopied: Bool
    let copyCode: (() -> Void)?

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: icon)
                .font(.system(size: 26, weight: .bold))
                .foregroundStyle(RivunePalette.accent)
            Text(title).font(.headline)
            if let code {
                if let copyCode {
                    Button(action: copyCode) {
                        VStack(spacing: 10) {
                            Text(code)
                                .font(.system(size: 38, weight: .black, design: .monospaced))
                                .tracking(5)
                            Label(codeCopied ? "Copied" : "Tap to copy", systemImage: codeCopied ? "checkmark" : "doc.on.doc")
                                .font(.footnote.weight(.semibold))
                                .foregroundStyle(RivunePalette.accent)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(codeCopied ? "Code \(code), copied" : "Code \(code), tap to copy")
                    .accessibilityIdentifier("pairing-code")
                } else {
                    Text(code)
                        .font(.system(size: 38, weight: .black, design: .monospaced))
                        .tracking(5)
                        .accessibilityIdentifier("pairing-code")
                }
            }
            Text(subtitle)
                .font(.callout)
                .foregroundStyle(RivunePalette.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(28)
        .background(RivunePalette.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .stroke(Color.white.opacity(0.08), lineWidth: 1)
        }
    }
}

private struct ProfilesView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var pendingProfile: Profile?
    @State private var pin = ""
    @State private var confirmDisconnect = false

    private let columns = [GridItem(.adaptive(minimum: 150, maximum: 220), spacing: 20)]

    var body: some View {
        NavigationView {
            ScrollView {
                VStack(alignment: .leading, spacing: 30) {
                    HStack {
                        Brand(compact: true)
                        Spacer()
                        Button(role: .destructive) { confirmDisconnect = true } label: {
                            Label("Disconnect", systemImage: "rectangle.portrait.and.arrow.right")
                        }
                        .rivuneDestructiveButton()
                    }
                    ScreenHeading(
                        eyebrow: model.serverName,
                        title: "Who’s watching?",
                        bodyText: "Choose a profile to continue. Availability and PIN rules come from your server."
                    )
                    FailureText(failure: model.failure)
                    LazyVGrid(columns: columns, alignment: .leading, spacing: 20) {
                        ForEach(model.profiles) { profile in
                            ProfileButton(profile: profile, imageData: model.profileAvatarData[profile.id]) {
                                select(profile)
                            }
                            .disabled(model.isBusy || !profile.accessible)
                        }
                    }
                }
                .padding(28)
                .frame(maxWidth: 1120)
                .frame(maxWidth: .infinity)
            }
        }
        .sheet(item: $pendingProfile) { profile in
            PinView(
                profile: profile,
                imageData: model.profileAvatarData[profile.id],
                pin: $pin,
                failure: model.failure,
                busy: model.isBusy,
                cancel: {
                    pin = ""
                    pendingProfile = nil
                    model.clearFailure()
                },
                submit: {
                    let normalized = String(pin.filter(\.isNumber).prefix(8))
                    guard normalized.count >= 4 else { return }
                    pin = normalized
                    model.selectProfile(profile, pin: normalized)
                }
            )
        }
        .onChange(of: model.destination) { destination in
            if destination != .profiles {
                pin = ""
                pendingProfile = nil
            }
        }
        .confirmationDialog("Disconnect from \(model.serverName)?", isPresented: $confirmDisconnect) {
            Button("Disconnect", role: .destructive, action: model.disconnect)
            Button("Cancel", role: .cancel) {}
        }
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
}

private struct ProfileButton: View {
    let profile: Profile
    let imageData: Data?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 12) {
                ProfileAvatarImage(profile: profile, imageData: imageData)
                    .frame(width: 124, height: 124)
                    .clipShape(Circle())
                    .overlay(alignment: .bottomTrailing) {
                        if profile.hasPin {
                            Image(systemName: "lock.fill")
                                .font(.caption.weight(.bold))
                                .padding(8)
                                .background(.black, in: Circle())
                        }
                    }
                Text(profile.name)
                    .font(.headline)
                    .lineLimit(1)
                if !profile.accessible {
                    Text("Unavailable")
                        .font(.caption)
                        .foregroundStyle(RivunePalette.secondary)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 18)
            .foregroundStyle(.white)
            .background(RivunePalette.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        }
        .buttonStyle(.plain)
#if os(tvOS)
        .buttonStyle(.card)
#endif
        .opacity(profile.accessible ? 1 : 0.48)
        .accessibilityLabel("\(profile.name)\(profile.hasPin ? ", PIN required" : "")")
    }
}

private struct ProfileAvatarImage: View {
    let profile: Profile
    let imageData: Data?

    @ViewBuilder var body: some View {
        if profile.avatar.kind == "custom", let image = platformImage(from: imageData) {
            image.resizable().scaledToFill()
        } else if let presetID = profile.avatar.presetId,
                  let artwork = ProfileAvatarArtwork.presets[presetID] {
            ProfilePresetAvatar(artwork: artwork)
        } else {
            ZStack {
                LinearGradient(
                    colors: [RivunePalette.accent.opacity(0.85), Color.purple.opacity(0.75)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
                Text(profile.name.prefix(1).uppercased())
                    .font(.system(size: 44, weight: .bold, design: .rounded))
            }
        }
    }

    private func platformImage(from data: Data?) -> Image? {
        guard let data else { return nil }
#if canImport(UIKit)
        guard let image = UIImage(data: data) else { return nil }
        return Image(uiImage: image)
#elseif canImport(AppKit)
        guard let image = NSImage(data: data) else { return nil }
        return Image(nsImage: image)
#endif
    }
}

private struct ProfilePresetAvatar: View {
    let artwork: ProfileAvatarArtwork

    var body: some View {
        GeometryReader { proxy in
            let scale = proxy.size.width / 512
            ZStack(alignment: .topLeading) {
                LinearGradient(
                    colors: [Color(hexRGB: artwork.start), Color(hexRGB: artwork.end)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
                Circle()
                    .fill(Color.white.opacity(0.18))
                    .frame(width: 380 * scale, height: 380 * scale)
                    .offset(x: -22 * scale, y: -48 * scale)
                artwork.path
                    .applying(CGAffineTransform(scaleX: scale, y: scale))
                    .fill(Color(hexRGB: artwork.accent).opacity(0.88))
                Circle()
                    .fill(Color.white.opacity(0.3))
                    .frame(width: 56 * scale, height: 56 * scale)
                    .offset(x: 186 * scale, y: 182 * scale)
            }
        }
    }
}

private struct ProfileAvatarArtwork {
    let start: UInt32
    let end: UInt32
    let accent: UInt32
    let path: Path

    init(_ start: UInt32, _ end: UInt32, _ accent: UInt32, _ pathData: String) {
        self.start = start
        self.end = end
        self.accent = accent
        self.path = SVGAvatarPath(data: pathData).path
    }

    static let presets: [String: ProfileAvatarArtwork] = [
        "aurora": .init(0x432371, 0x00D4A8, 0xF7E8FF, "M94 330C159 214 230 371 418 158C355 352 225 438 94 330Z"),
        "ember": .init(0x53131E, 0xFF6B35, 0xFFE8B6, "M256 74C344 174 381 244 336 337C300 412 188 415 151 338C113 257 174 189 256 74Z"),
        "tide": .init(0x062A4D, 0x168AAD, 0xD9F7FF, "M60 298C126 198 203 221 260 276C319 332 391 347 452 252C431 400 324 446 225 394C150 355 112 292 60 298Z"),
        "grove": .init(0x16351A, 0x70A33A, 0xEDFFD1, "M256 65C278 172 379 172 414 239C449 308 393 403 306 421C227 437 142 390 114 309C87 231 152 149 256 65Z"),
        "violet": .init(0x24124D, 0x9B5DE5, 0xF4DCFF, "M256 68L310 202L450 211L341 301L376 438L256 363L136 438L171 301L62 211L202 202Z"),
        "solar": .init(0x7A2E00, 0xFFB703, 0xFFF6C2, "M256 91L292 194L401 167L340 259L432 319L323 318L310 427L256 332L202 427L189 318L80 319L172 259L111 167L220 194Z"),
        "glacier": .init(0x12355B, 0x5DD9C1, 0xE9FFFD, "M256 62L409 185L357 420H155L103 185Z"),
        "rose": .init(0x4A1942, 0xE56B8A, 0xFFE4EC, "M256 421C215 367 99 303 104 207C108 132 201 107 256 181C311 107 404 132 408 207C413 303 297 367 256 421Z"),
        "luna": .init(0x10143D, 0x6D5DFB, 0xF5E8A8, "M340 91C227 120 182 258 249 351C291 410 374 426 429 376C359 384 296 347 272 287C241 210 270 132 340 91Z"),
        "coral": .init(0x5A153B, 0xFF7A7A, 0xFFE6C7, "M256 241C181 103 83 158 145 260C58 306 129 405 243 312C275 442 398 399 341 286C460 236 402 124 274 222C328 88 195 54 256 241Z"),
        "nebula": .init(0x0B1026, 0xD946EF, 0xBFFBFF, "M83 301C122 237 207 185 296 166C385 147 447 166 459 207C470 249 421 299 340 336C257 375 155 390 91 365C46 348 42 322 83 301ZM151 300C210 323 303 298 370 252C307 274 218 276 151 300Z"),
        "meadow": .init(0x0F3D2E, 0x82C91E, 0xFFF3A3, "M251 249C202 136 100 126 108 225C112 281 178 291 236 270C198 333 185 412 256 421C327 412 314 333 276 270C334 291 400 281 404 225C412 126 310 136 261 249Z"),
        "cobalt": .init(0x061B3A, 0x2563EB, 0xD8F3FF, "M256 61L407 174L372 370L256 448L140 370L105 174Z"),
        "peach": .init(0x7C2D52, 0xFDBA8C, 0xFFF1E8, "M122 352C70 352 51 286 88 252C110 232 139 227 165 238C174 174 230 132 292 151C335 164 366 198 374 241C430 232 467 274 455 322C446 360 411 382 372 382H133C126 382 122 369 122 352Z"),
        "volt": .init(0x18203F, 0x00C2FF, 0xF7FF72, "M286 56L111 285H224L188 456L401 206H281Z"),
        "summit": .init(0x28203D, 0xFF7A45, 0xFFF0D1, "M55 405L197 152L262 263L324 105L457 405Z"),
    ]
}

private struct SVGAvatarPath {
    let path: Path

    init(data: String) {
        var result = Path()
        let scanner = Scanner(string: data)
        scanner.charactersToBeSkipped = CharacterSet(charactersIn: " ,\n\t")
        var command: Character?
        while !scanner.isAtEnd {
            if let next = scanner.string[scanner.currentIndex...].first, next.isLetter {
                command = next
                scanner.currentIndex = scanner.string.index(after: scanner.currentIndex)
            }
            switch command {
            case "M":
                guard let point = scanner.scanPoint() else { scanner.currentIndex = scanner.string.endIndex; continue }
                result.move(to: point)
                command = "L"
            case "L":
                guard let point = scanner.scanPoint() else { scanner.currentIndex = scanner.string.endIndex; continue }
                result.addLine(to: point)
            case "H":
                guard let x = scanner.scanDouble(representation: .decimal) else { scanner.currentIndex = scanner.string.endIndex; continue }
                result.addLine(to: CGPoint(x: x, y: result.currentPoint?.y ?? 0))
            case "C":
                guard let control1 = scanner.scanPoint(), let control2 = scanner.scanPoint(), let point = scanner.scanPoint() else {
                    scanner.currentIndex = scanner.string.endIndex
                    continue
                }
                result.addCurve(to: point, control1: control1, control2: control2)
            case "Z":
                result.closeSubpath()
                command = nil
            default:
                scanner.currentIndex = scanner.string.endIndex
            }
        }
        path = result
    }
}

private extension Scanner {
    func scanPoint() -> CGPoint? {
        guard let x = scanDouble(representation: .decimal), let y = scanDouble(representation: .decimal) else { return nil }
        return CGPoint(x: x, y: y)
    }
}

private extension Color {
    init(hexRGB: UInt32) {
        self.init(
            red: Double((hexRGB >> 16) & 0xff) / 255,
            green: Double((hexRGB >> 8) & 0xff) / 255,
            blue: Double(hexRGB & 0xff) / 255
        )
    }
}

private struct PinView: View {
    let profile: Profile
    let imageData: Data?
    @Binding var pin: String
    let failure: RivuneAppFailure?
    let busy: Bool
    let cancel: () -> Void
    let submit: () -> Void
    @FocusState private var pinFocused: Bool

    private var canSubmit: Bool { pin.count >= 4 && pin.count <= 8 && !busy }

    var body: some View {
        ZStack {
            LinearGradient(
                colors: [RivunePalette.raised, Color.black],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            .ignoresSafeArea()

            GeometryReader { proxy in
                ScrollView {
                VStack(spacing: 20) {
                    ProfileAvatarImage(profile: profile, imageData: imageData)
                        .frame(width: 72, height: 72)
                        .clipShape(Circle())
                        .overlay { Circle().stroke(Color.white.opacity(0.12), lineWidth: 1) }

                    VStack(spacing: 7) {
                        Text("Enter PIN")
                            .font(.title2.bold())
                        Text("Unlock \(profile.name) with the 4–8 digit PIN.")
                            .font(.callout)
                            .foregroundStyle(RivunePalette.secondary)
                            .multilineTextAlignment(.center)
                    }

                    ZStack {
                        HStack(spacing: 10) {
                            ForEach(0..<8, id: \.self) { index in
                                Circle()
                                    .fill(index < pin.count ? RivunePalette.accent : Color.white.opacity(0.14))
                                    .frame(width: index < 4 ? 15 : 11, height: index < 4 ? 15 : 11)
                                    .overlay { Circle().stroke(Color.white.opacity(index < pin.count ? 0 : 0.20), lineWidth: 1) }
                            }
                        }
                        SecureField("PIN", text: $pin)
                            .focused($pinFocused)
                            .textFieldStyle(.plain)
                            .foregroundStyle(.clear)
                            .tint(.clear)
                            .opacity(0.02)
#if os(iOS) || os(visionOS)
                            .keyboardType(.numberPad)
                            .textContentType(.oneTimeCode)
#endif
                            .onChange(of: pin) { value in
                                let normalized = String(value.filter(\.isNumber).prefix(8))
                                if normalized != value { pin = normalized }
                                if normalized.count == 8 && !busy { submit() }
                            }
                            .onSubmit { if canSubmit { submit() } }
                    }
                    .frame(maxWidth: 360, minHeight: 64)
                    .padding(.horizontal, 20)
                    .background(RivunePalette.surface.opacity(0.94), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                    .overlay {
                        RoundedRectangle(cornerRadius: 16, style: .continuous)
                            .stroke(failure == nil ? Color.white.opacity(0.12) : Color.red.opacity(0.85), lineWidth: 1)
                    }
                    .contentShape(Rectangle())
#if !os(tvOS)
                    .onTapGesture { pinFocused = true }
#endif
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("PIN")
                    .accessibilityValue("\(pin.count) digits entered")

                    if let failure {
                        Label(failure.localizedDescription, systemImage: "exclamationmark.triangle.fill")
                            .font(.callout)
                            .foregroundStyle(Color.red.opacity(0.94))
                            .multilineTextAlignment(.center)
                    } else {
                        Text(pin.isEmpty ? "Enter at least 4 digits" : "\(pin.count) of 8 digits")
                            .font(.caption)
                            .foregroundStyle(RivunePalette.secondary)
                    }

                    HStack(spacing: 12) {
                        Button("Cancel", role: .cancel, action: cancel)
                            .rivuneGlassButton()
                            .disabled(busy)
                        Button(action: submit) {
                            HStack(spacing: 8) {
                                if busy { ProgressView() }
                                Text(busy ? "Unlocking…" : "Unlock").fontWeight(.semibold)
                                if !busy { Image(systemName: "lock.open.fill") }
                            }
                            .frame(minWidth: 112)
                        }
                        .rivuneGlassButton(prominent: true)
                        .disabled(!canSubmit)
                    }
                }
                .padding(.horizontal, 24)
                .padding(.vertical, 26)
                .frame(maxWidth: 520)
                .frame(maxWidth: .infinity)
                    .frame(minHeight: proxy.size.height, alignment: .center)
            }
            }
        }
        .preferredColorScheme(.dark)
        .onAppear {
            DispatchQueue.main.async { pinFocused = true }
        }
    }
}

private struct LibraryView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var confirmDisconnect = false
    @State private var showAppearanceSettings = false

    var body: some View {
        tabContent
            .tint(RivunePalette.accent)
        .confirmationDialog("Disconnect from \(model.serverName)?", isPresented: $confirmDisconnect) {
            Button("Disconnect", role: .destructive, action: model.disconnect)
            Button("Cancel", role: .cancel) {}
        }
        .folderPresentation(
            item: Binding(
                get: { model.openedFolder },
                set: { if $0 == nil { model.closeFolder() } }
            ),
            onDismiss: model.closeFolder
        ) { _ in
            FolderView(model: model)
        }
        .sheet(isPresented: $showAppearanceSettings) {
            AppearanceSettingsView(model: model)
        }
        .sheet(isPresented: Binding(
            get: { model.mediaLoading || model.mediaDetail != nil || model.mediaFailure != nil },
            set: { if !$0 { model.closeMedia() } }
        )) {
            RivuneMediaDetailView(model: model)
        }
    }

    private var tabContent: some View {
        TabView(
            selection: Binding(
                get: { model.selectedTab },
                set: model.selectTab
            )
        ) {
            homeContent
                .tag(RivuneViewerTab.home)
                .tabItem { EqualNativeTabLabel(title: "Home", systemImage: "house.fill") }
            SearchTabView(
                model: model,
                settings: { showAppearanceSettings = true },
                disconnect: { confirmDisconnect = true }
            )
            .tag(RivuneViewerTab.search)
            .tabItem { EqualNativeTabLabel(title: "Search", systemImage: "magnifyingglass") }
            PersonalLibraryTabView(
                model: model,
                settings: { showAppearanceSettings = true },
                disconnect: { confirmDisconnect = true }
            )
            .tag(RivuneViewerTab.library)
            .tabItem { EqualNativeTabLabel(title: "Library", systemImage: "rectangle.stack.fill") }
            CalendarTabView(
                model: model,
                settings: { showAppearanceSettings = true },
                disconnect: { confirmDisconnect = true }
            )
            .tag(RivuneViewerTab.calendar)
            .tabItem { EqualNativeTabLabel(title: "Calendar", systemImage: "calendar") }
        }
    }

    private var homeContent: some View {
        GeometryReader { proxy in
            NavigationView {
                ScrollView {
                    VStack(alignment: .leading, spacing: 32) {
                        LibraryHeader(
                            compact: proxy.size.width < 620,
                            switchProfile: model.chooseAnotherProfile,
                            settings: { showAppearanceSettings = true },
                            disconnect: { confirmDisconnect = true }
                        )
                        ScreenHeading(
                            eyebrow: model.serverName,
                            title: "Home",
                            bodyText: model.collections.isEmpty && !model.isBusy ? "Your server has no visible collections for this profile." : nil
                        )
                        if !model.heroItems.isEmpty {
                            HomeHeroCarousel(
                                items: model.heroItems,
                                model: model,
                                height: min(max(proxy.size.width * 9 / 21, 220), 420)
                            )
                        }
                        if !model.continueWatchingItems.isEmpty {
                            ContinueWatchingSection(
                                items: model.continueWatchingItems,
                                model: model,
                                availableWidth: proxy.size.width - (proxy.size.width < 620 ? 40 : 56)
                            )
                        }
                        if !model.recommendationItems.isEmpty {
                            RecommendationSection(items: model.recommendationItems, model: model, availableWidth: proxy.size.width - (proxy.size.width < 620 ? 40 : 56))
                        }
                        if !model.offlineItems.isEmpty {
                            OfflineMediaSection(items: model.offlineItems, model: model, availableWidth: proxy.size.width - (proxy.size.width < 620 ? 40 : 56))
                        }
                        if model.isBusy {
                            HStack(spacing: 12) {
                                ProgressView()
                                Text("Loading your home…")
                            }
                            .foregroundStyle(RivunePalette.secondary)
                        }
                        FailureText(failure: model.failure)
                        if model.failure != nil {
                            Button("Try again", action: model.retryLibrary)
                                .rivuneGlassButton(prominent: true)
                        }
                        ForEach(model.collections) { collection in
                            CollectionSection(
                                collection: collection,
                                model: model,
                                availableWidth: proxy.size.width - (proxy.size.width < 620 ? 40 : 56)
                            )
                        }
                    }
                    .padding(proxy.size.width < 620 ? 20 : 28)
                    .frame(maxWidth: 1320)
                    .frame(maxWidth: .infinity)
                }
            }
        }
    }
}
private struct EqualNativeTabLabel: View {
    let title: String
    let systemImage: String
    var body: some View {
        VStack(spacing: 2) {
            Image(systemName: systemImage)
            Text(title)
        }
        .frame(width: 72)
        .accessibilityLabel(title)
    }
}



private struct SearchTabView: View {
    @ObservedObject var model: RivuneAppModel
    let settings: () -> Void
    let disconnect: () -> Void

    private let columns = [GridItem(.adaptive(minimum: 140, maximum: 190), spacing: 18)]

    var body: some View {
        NavigationView {
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    LibraryHeader(compact: true, switchProfile: model.chooseAnotherProfile, settings: settings, disconnect: disconnect)
                    ScreenHeading(eyebrow: model.serverName, title: "Search", bodyText: "Search every compatible catalog connected to your server.")
                    HStack(spacing: 12) {
                        TextField("Movies, series, anime…", text: $model.searchQuery)
                            .textFieldStyle(.plain)
                            .padding(.horizontal, 16)
                            .frame(minHeight: 50)
                            .background(RivunePalette.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                            .onSubmit(model.search)
                        Button(action: model.search) {
                            Label("Search", systemImage: "magnifyingglass")
                        }
                        .rivuneGlassButton(prominent: true)
                        .disabled(model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                    TabStatus(model: model, empty: model.searchItems.isEmpty && !model.searchQuery.isEmpty ? "No matching titles." : nil)
                    LazyVGrid(columns: columns, alignment: .leading, spacing: 24) {
                        ForEach(model.searchItems) { item in
                            Button { model.openMedia(item) } label: {
                                MediaArtworkTile(
                                    title: item.title,
                                    subtitle: item.releaseInfo,
                                    mediaType: item.mediaType,
                                    landscape: item.mediaType == "tv",
                                    imageURL: (item.mediaType == "tv" ? item.backgroundUrl ?? item.posterUrl : item.posterUrl ?? item.backgroundUrl).flatMap(model.resolvedResourceURL)
                                )
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                .padding(28)
                .frame(maxWidth: 1200)
                .frame(maxWidth: .infinity)
            }
        }
    }
}

private struct PersonalLibraryTabView: View {
    @ObservedObject var model: RivuneAppModel
    let settings: () -> Void
    let disconnect: () -> Void

    private let columns = [GridItem(.adaptive(minimum: 140, maximum: 190), spacing: 18)]

    var body: some View {
        NavigationView {
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    LibraryHeader(compact: true, switchProfile: model.chooseAnotherProfile, settings: settings, disconnect: disconnect)
                    ScreenHeading(eyebrow: model.serverName, title: "Library", bodyText: "Titles saved to this profile.")
                    TabStatus(model: model, empty: model.libraryItems.isEmpty ? "Your library is empty." : nil)
                    LazyVGrid(columns: columns, alignment: .leading, spacing: 24) {
                        ForEach(model.libraryItems) { item in
                            Button { model.openMedia(item) } label: {
                                MediaArtworkTile(
                                    title: item.title ?? "Untitled",
                                    subtitle: item.releaseInfo,
                                    mediaType: item.mediaType.rawValue,
                                    landscape: item.mediaType == .tv,
                                    imageURL: (item.mediaType == .tv ? item.backgroundUrl ?? item.posterUrl : item.posterUrl ?? item.backgroundUrl).flatMap(model.resolvedResourceURL)
                                )
                            }
                            .buttonStyle(.plain)
                            .disabled(!item.available)
                            .opacity(item.available ? 1 : 0.5)
                        }
                    }
                }
                .padding(28)
                .frame(maxWidth: 1200)
                .frame(maxWidth: .infinity)
            }
        }
    }
}

private struct CalendarTabView: View {
    @ObservedObject var model: RivuneAppModel
    let settings: () -> Void
    let disconnect: () -> Void

    var body: some View {
        NavigationView {
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    LibraryHeader(compact: true, switchProfile: model.chooseAnotherProfile, settings: settings, disconnect: disconnect)
                    ScreenHeading(eyebrow: model.serverName, title: "Calendar", bodyText: "Upcoming movies and episodes from your library.")
                    TabStatus(model: model, empty: model.calendarEvents.isEmpty ? "No releases in this date range." : nil)
                    LazyVStack(spacing: 12) {
                        ForEach(model.calendarEvents) { event in
                            Button { model.openMedia(event) } label: {
                                HStack(spacing: 16) {
                                    AsyncImage(url: event.posterUrl.flatMap(model.resolvedResourceURL)) { phase in
                                        if let image = phase.image {
                                            image.resizable().scaledToFill()
                                        } else {
                                            ZStack {
                                                RivunePalette.surface
                                                Image(systemName: event.mediaType == "episode" ? "tv.fill" : "film.fill")
                                                    .foregroundStyle(RivunePalette.secondary)
                                            }
                                        }
                                    }
                                    .frame(width: 72, height: 96)
                                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                    VStack(alignment: .leading, spacing: 5) {
                                        Text(event.title).font(.headline)
                                        if let seriesTitle = event.seriesTitle { Text(seriesTitle).foregroundStyle(RivunePalette.secondary) }
                                        Text(event.releaseDate).font(.caption.weight(.semibold)).foregroundStyle(RivunePalette.accent)
                                    }
                                    Spacer()
                                    Image(systemName: "chevron.right")
                                }
                                .padding(14)
                                .background(RivunePalette.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                .padding(28)
                .frame(maxWidth: 920)
                .frame(maxWidth: .infinity)
            }
        }
    }
}

private struct TabStatus: View {
    @ObservedObject var model: RivuneAppModel
    let empty: String?

    var body: some View {
        if model.tabLoading {
            HStack(spacing: 12) {
                ProgressView()
                Text("Loading…")
            }
            .foregroundStyle(RivunePalette.secondary)
        } else if let failure = model.tabFailure {
            FailureText(failure: failure)
        } else if let empty {
            Text(empty).foregroundStyle(RivunePalette.secondary)
        }
    }
}

private struct MediaArtworkTile: View {
    let title: String
    let subtitle: String?
    let mediaType: String
    let landscape: Bool
    let imageURL: URL?

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            AsyncImage(url: imageURL) { phase in
                if let image = phase.image {
                    image.resizable().scaledToFill()
                } else {
                    ZStack {
                        RivunePalette.surface
                        Image(systemName: mediaType == "series" || mediaType == "tv" ? "tv" : "film")
                            .font(.system(size: 30))
                            .foregroundStyle(RivunePalette.secondary)
                    }
                }
            }
            .frame(maxWidth: .infinity)
            .aspectRatio(landscape ? 16.0 / 9.0 : 2.0 / 3.0, contentMode: .fit)
            .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            Text(title)
                .font(.subheadline.weight(.semibold))
                .lineLimit(2)
            if let subtitle, !subtitle.isEmpty {
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(RivunePalette.secondary)
                    .lineLimit(1)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

private struct LibraryHeader: View {
    let compact: Bool
    let switchProfile: () -> Void
    let settings: () -> Void
    let disconnect: () -> Void

    var body: some View {
        HStack(spacing: compact ? 10 : 16) {
            Brand(compact: true)
            Spacer(minLength: compact ? 4 : 16)
            if compact {
#if os(tvOS)
                Button(action: settings) {
                    Label("Settings", systemImage: "gearshape.fill")
                }
                .rivuneGlassButton()
                Button(action: switchProfile) {
                    Label("Switch profile", systemImage: "person.2.fill")
                }
                .rivuneGlassButton()
#else
                Menu {
                    Button(action: settings) {
                        Label("Settings", systemImage: "gearshape.fill")
                    }
                    Button(action: switchProfile) {
                        Label("Switch profile", systemImage: "person.2.fill")
                    }
                    Button(role: .destructive, action: disconnect) {
                        Label("Disconnect", systemImage: "rectangle.portrait.and.arrow.right")
                    }
                } label: {
                    Image(systemName: "ellipsis.circle")
                        .font(.title2)
                        .frame(width: 44, height: 44)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Account actions")
#endif
            } else {
                HStack(spacing: 14) {
                    Button(action: settings) {
                        Label("Settings", systemImage: "gearshape.fill")
                    }
                    .buttonStyle(.bordered)
                    Button(action: switchProfile) {
                        Label("Switch profile", systemImage: "person.2.fill")
                    }
                    .buttonStyle(.bordered)
                    Button(role: .destructive, action: disconnect) {
                        Label("Disconnect", systemImage: "rectangle.portrait.and.arrow.right")
                    }
                    .rivuneDestructiveButton()
                }
            }
        }
    }
}


private struct AppearanceSettingsView: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var diagnosticStatus: String?
    @State private var diagnosticPreview = ""
#if os(tvOS)
    @State private var televisionUpdate: RivuneAppleUpdate?
#endif
    @State private var diagnosticPreviewPresented = false
#if !os(tvOS)
    @State private var diagnosticDocument: RivuneDiagnosticTextDocument?
    @State private var diagnosticExporterPresented = false
#endif

    private let columns = [GridItem(.adaptive(minimum: 140, maximum: 220), spacing: 14)]

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    HStack {
                        Spacer()
                        Button("Done") { dismiss() }.buttonStyle(.bordered)
                    }
                    ScreenHeading(
                        eyebrow: "Device",
                        title: "Settings",
                        bodyText: "Playback and appearance preferences are stored on this device. Profile playback policy comes from your server."
                    )

                    if model.settingsLoading && model.profileSettings == nil {
                        HStack(spacing: 10) {
                            ProgressView()
                            Text("Loading profile settings…")
                        }
                        .foregroundStyle(RivunePalette.secondary)
                        .accessibilityElement(children: .combine)
                    }
                    if let failure = model.settingsFailure {
                        VStack(alignment: .leading, spacing: 12) {
                            Label(failure.localizedDescription, systemImage: "exclamationmark.triangle.fill")
                                .foregroundStyle(.yellow)
                            Button("Try again", action: model.loadProfileSettings)
                                .buttonStyle(.borderedProminent)
                                .disabled(model.settingsLoading)
                        }
                        .padding(16)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.yellow.opacity(0.10), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                    }

                    settingsSection("APPEARANCE") {
                        LazyVGrid(columns: columns, alignment: .leading, spacing: 14) {
                            ForEach(RivuneAccent.allCases) { accent in
                                Button { model.setAccent(accent) } label: {
                                    HStack(spacing: 12) {
                                        Circle().fill(RivunePalette.color(for: accent)).frame(width: 24, height: 24)
                                        Text(accent.displayName).fontWeight(.semibold)
                                        Spacer(minLength: 8)
                                        if model.accent == accent {
                                            Image(systemName: "checkmark.circle.fill").foregroundStyle(RivunePalette.color(for: accent))
                                        }
                                    }
                                    .foregroundStyle(.white).padding(.horizontal, 16).frame(maxWidth: .infinity, minHeight: 56)
                                    .background(RivunePalette.raised, in: RoundedRectangle(cornerRadius: 13, style: .continuous))
                                }
                                .buttonStyle(.plain)
                                .accessibilityAddTraits(model.accent == accent ? .isSelected : [])
                            }
                        }
                        settingsPicker("Animations", selection: Binding(get: { model.animationPreference }, set: model.setAnimationPreference), options: RivuneAnimationPreference.allCases)
                    }

                    settingsSection("NAVIGATION") {
                        settingsPicker("Startup tab", selection: Binding(get: { model.startupTab }, set: model.setStartupTab), options: RivuneViewerTab.allCases)
                    }

                    settingsSection("PLAYER") {
                        settingsPicker("Preferred player", selection: Binding(get: { model.preferredPlayer }, set: model.setPreferredPlayer), options: RivunePlayerPreference.allCases)
                        settingsPicker("Embedded engine", selection: Binding(get: { model.embeddedPlayerPreference }, set: model.setEmbeddedPlayerPreference), options: RivuneEmbeddedPlayerPreference.allCases)
                        settingsPicker("Frame-rate matching", selection: Binding(get: { model.frameRateMatching }, set: model.setFrameRateMatching), options: RivuneFrameRatePreference.allCases)
                        settingsPicker("Video aspect", selection: Binding(get: { model.videoAspect }, set: model.setVideoAspect), options: RivuneVideoAspect.allCases)
                        settingsPicker("Wi-Fi quality", selection: Binding(get: { model.wifiQuality }, set: model.setWifiQuality), options: RivuneNetworkQuality.allCases)
                        settingsPicker("Mobile quality", selection: Binding(get: { model.mobileQuality }, set: model.setMobileQuality), options: RivuneNetworkQuality.allCases)
                        settingsToggle("Show streams automatically", value: Binding(get: { model.automaticallyShowStreams }, set: model.setAutomaticallyShowStreams))
                    }

                    settingsSection("VIDEO") {
                        serverStringPicker(
                            "Maximum resolution",
                            value: model.profileSettings?.maximumResolution,
                            source: model.profileSettingsSources?.maximumResolution,
                            options: [("auto", "Automatic"), ("2160p", "4K"), ("1080p", "1080p"), ("720p", "720p"), ("480p", "480p")]
                        ) { model.updateProfileSettings(ProfileSettingsPatch(maximumResolution: $0.map(SettingsPatchField.value) ?? .null)) }
                        serverBoolPicker("Prefer direct play", value: model.profileSettings?.preferDirectPlay, source: model.profileSettingsSources?.preferDirectPlay) {
                            model.updateProfileSettings(ProfileSettingsPatch(preferDirectPlay: $0.map(SettingsPatchField.value) ?? .null))
                        }
                        serverStringPicker(
                            "Transcoding policy",
                            value: model.profileSettings?.transcoding,
                            source: model.profileSettingsSources?.transcoding,
                            options: [("inherit", "Inherit"), ("enabled", "Enabled"), ("disabled", "Disabled")]
                        ) { model.updateProfileSettings(ProfileSettingsPatch(transcoding: $0.map(SettingsPatchField.value) ?? .null)) }
                        settingsValue("Transcoding available", value: model.profileSettings.map { $0.allowTranscoding == true ? "Yes" : "No" } ?? "Unavailable")
                        serverBoolPicker("Autoplay next episode", value: model.profileSettings?.autoplayNextEpisode, source: model.profileSettingsSources?.autoplayNextEpisode) {
                            model.updateProfileSettings(ProfileSettingsPatch(autoplayNextEpisode: $0.map(SettingsPatchField.value) ?? .null))
                        }
                    }

                    settingsSection("SKIP MARKERS") {
                        serverBoolPicker("Enable intro markers", value: model.profileSettings?.skipIntroEnabled, source: model.profileSettingsSources?.skipIntroEnabled) {
                            model.updateProfileSettings(ProfileSettingsPatch(skipIntroEnabled: $0.map(SettingsPatchField.value) ?? .null))
                        }
                        serverBoolPicker("Enable recap markers", value: model.profileSettings?.skipRecapEnabled, source: model.profileSettingsSources?.skipRecapEnabled) {
                            model.updateProfileSettings(ProfileSettingsPatch(skipRecapEnabled: $0.map(SettingsPatchField.value) ?? .null))
                        }
                        serverBoolPicker("Enable outro markers", value: model.profileSettings?.skipOutroEnabled, source: model.profileSettingsSources?.skipOutroEnabled) {
                            model.updateProfileSettings(ProfileSettingsPatch(skipOutroEnabled: $0.map(SettingsPatchField.value) ?? .null))
                        }
                        settingsToggle("Skip intros automatically", value: Binding(get: { model.autoSkipIntro }, set: model.setAutoSkipIntro))
                        settingsToggle("Skip recaps automatically", value: Binding(get: { model.autoSkipRecap }, set: model.setAutoSkipRecap))
                        settingsToggle("Skip outros automatically", value: Binding(get: { model.autoSkipOutro }, set: model.setAutoSkipOutro))
                    }

                    settingsSection("AUDIO & SUBTITLES") {
                        serverStringPicker("Audio language", value: model.profileSettings?.audioLanguage, source: model.profileSettingsSources?.audioLanguage, options: languageOptions) {
                            model.updateProfileSettings(ProfileSettingsPatch(audioLanguage: $0.map(SettingsPatchField.value) ?? .null))
                        }
                        serverStringPicker("Subtitle language", value: model.profileSettings?.subtitleLanguage, source: model.profileSettingsSources?.subtitleLanguage, options: languageOptions) {
                            model.updateProfileSettings(ProfileSettingsPatch(subtitleLanguage: $0.map(SettingsPatchField.value) ?? .null))
                        }
                        serverStringPicker("Forced subtitle language", value: model.profileSettings?.forcedSubtitleLanguage, source: model.profileSettingsSources?.forcedSubtitleLanguage, options: [("off", "Off")] + languageOptions.filter { $0.0 != "auto" }) {
                            model.updateProfileSettings(ProfileSettingsPatch(forcedSubtitleLanguage: $0.map(SettingsPatchField.value) ?? .null))
                        }
                    }

                    settingsSection("METADATA") {
                        serverStringPicker("Metadata language", value: model.profileSettings?.metadataLanguage, source: model.profileSettingsSources?.metadataLanguage, options: metadataLanguageOptions) {
                            model.updateProfileSettings(ProfileSettingsPatch(metadataLanguage: $0.map(SettingsPatchField.value) ?? .null))
                        }
                    }

                    settingsSection("CONNECTION") {
                        settingsValue("Server", value: model.serverName)
                        settingsValue("Address", value: RivuneDiagnosticsReport.sanitizeServerOrigin(model.serverAddress) ?? "Unavailable")
                        settingsValue("Server version", value: model.serverVersion ?? "Unavailable")
                        settingsValue("Protocol", value: model.serverProtocolVersion.map(String.init) ?? "Unavailable")
                        settingsValue("Profile", value: model.activeProfile?.name ?? "None")
                    }

                    settingsSection("APPLICATION UPDATE") {
                        settingsValue("Installed version", value: model.applicationVersion)
                        updateStatus(model.updateState)
                        Button {
                            model.checkForUpdates()
                        } label: {
                            if case .checking = model.updateState {
                                Label("Checking…", systemImage: "arrow.triangle.2.circlepath")
                            } else {
                                Label("Check now", systemImage: "arrow.triangle.2.circlepath")
                            }
                        }
                        .buttonStyle(.bordered)
                        .disabled(model.updateState == .checking)
                    }

                    settingsSection("DIAGNOSTICS") {
                        Text("The report is generated only from allowlisted system fields and recent in-memory event codes. It contains no token, media title, profile identifier, request payload, or error detail, and Rivune never uploads it.")
                            .foregroundStyle(RivunePalette.secondary)
#if os(tvOS)
                        Button("View or scan diagnostics") {
                            diagnosticPreview = model.diagnosticReport()
                            diagnosticPreviewPresented = true
                            model.recordDiagnosticExport(succeeded: true)
                        }
                        .buttonStyle(.borderedProminent)
#else
#if os(iOS) || os(visionOS)
                        Button("Copy diagnostics") {
                            let copied = copyRivuneDiagnosticReport(model.diagnosticReport())
                            model.recordDiagnosticExport(succeeded: copied)
                            diagnosticStatus = copied
                                ? "Diagnostics copied locally for 60 seconds. Universal Clipboard is disabled."
                                : "Diagnostics could not be copied."
                        }
                        .buttonStyle(.bordered)
#endif
                        Button("Export logs") {
                            diagnosticDocument = RivuneDiagnosticTextDocument(report: model.diagnosticReport())
                            diagnosticExporterPresented = true
                        }
                        .buttonStyle(.borderedProminent)
#endif
                        if let diagnosticStatus {
                            Text(diagnosticStatus)
                                .font(.caption)
                                .foregroundStyle(RivunePalette.secondary)
                        }
                    }
                }
                .padding(28).frame(maxWidth: 820).frame(maxWidth: .infinity)
            }
        }
        .preferredColorScheme(.dark)
        .onAppear(perform: model.loadProfileSettings)
#if os(tvOS)
        .sheet(isPresented: $diagnosticPreviewPresented) {
            RivuneTelevisionDiagnosticView(report: diagnosticPreview)
        }
        .sheet(item: $televisionUpdate) { update in
            RivuneTelevisionUpdateView(update: update)
        }
#else
        .fileExporter(
            isPresented: $diagnosticExporterPresented,
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
#endif
    }

    @ViewBuilder private func settingsSection<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(title).font(.caption.weight(.semibold)).foregroundStyle(RivunePalette.secondary)
            content()
        }
        .padding(18)
        .background(RivunePalette.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    @ViewBuilder private func updateStatus(_ state: RivuneAppleUpdateState) -> some View {
        switch state {
        case .idle:
            Text("Rivune checks GitHub automatically when the app launches, then waits 24 hours after a successful check.")
                .foregroundStyle(RivunePalette.secondary)
        case .checking:
            HStack(spacing: 10) {
                ProgressView()
                Text("Checking the verified Rivune release manifest…")
            }
            .foregroundStyle(RivunePalette.secondary)
        case .upToDate(_, let latestVersion):
            Label("Up to date · \(latestVersion)", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
        case .available(let update):
            VStack(alignment: .leading, spacing: 10) {
                Label("Version \(update.latestVersion) is available", systemImage: "arrow.down.circle.fill")
                    .foregroundStyle(RivunePalette.accent)
                Text("Automatic installation is unavailable. The public Apple package is unsigned and must be downloaded from the verified GitHub release, then signed or approved as required by the platform.")
                    .font(.footnote)
                    .foregroundStyle(RivunePalette.secondary)
#if os(tvOS)
                Button("View release QR code") { televisionUpdate = update }
                    .buttonStyle(.borderedProminent)
#else
                Button("Open release") { openURL(update.releaseURL) }
                    .buttonStyle(.borderedProminent)
#endif
            }
        case .failed:
            Label("The update check failed. No package was downloaded.", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.yellow)
        }
    }

    private func settingsPicker<Value>(_ title: String, selection: Binding<Value>, options: [Value]) -> some View where Value: Hashable, Value: RawRepresentable, Value.RawValue == String {
        HStack {
            Text(title).font(.headline)
            Spacer()
            Picker(title, selection: selection) {
                ForEach(options, id: \.self) { option in
                    Text(displayName(option)).tag(option)
                }
            }.labelsHidden()
        }
    }

    private func displayName<Value>(_ value: Value) -> String where Value: RawRepresentable, Value.RawValue == String {
        if let value = value as? RivunePlayerPreference { return value.displayName }
        if let value = value as? RivuneEmbeddedPlayerPreference { return value.displayName }
        if let value = value as? RivuneAnimationPreference { return value.displayName }
        if let value = value as? RivuneFrameRatePreference { return value.displayName }
        if let value = value as? RivuneVideoAspect { return value.displayName }
        if let value = value as? RivuneNetworkQuality { return value.displayName }
        return value.rawValue.capitalized
    }

    private var languageOptions: [(String, String)] {
        [("auto", "Automatic"), ("en", "English"), ("fr", "French"), ("de", "German"), ("es", "Spanish"), ("it", "Italian"), ("pt", "Portuguese"), ("ja", "Japanese")]
    }

    private var metadataLanguageOptions: [(String, String)] {
        [("auto", "Automatic"), ("en-US", "English"), ("fr-FR", "French"), ("de-DE", "German"), ("es-ES", "Spanish"), ("it-IT", "Italian"), ("pt-BR", "Portuguese"), ("ja-JP", "Japanese")]
    }

    private func serverStringPicker(_ title: String, value: String?, source: String?, options: [(String, String)], update: @escaping (String?) -> Void) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(title).font(.headline)
                Spacer()
                Picker(title, selection: Binding(
                    get: { source == "profile" ? value ?? "__server__" : "__server__" },
                    set: { update($0 == "__server__" ? nil : $0) }
                )) {
                    Text("Use server value").tag("__server__")
                    ForEach(options, id: \.0) { Text($0.1).tag($0.0) }
                }.labelsHidden().disabled(model.activeProfile?.canManage != true || model.settingsLoading || model.profileSettings == nil)
            }
            if model.profileSettings == nil {
                Text("Effective value unavailable").font(.caption).foregroundStyle(RivunePalette.secondary)
            } else {
                Text("Effective: \(value ?? "server default") · source: \(source ?? "server")").font(.caption).foregroundStyle(RivunePalette.secondary)
            }
        }
    }

    private func serverBoolPicker(_ title: String, value: Bool?, source: String?, update: @escaping (Bool?) -> Void) -> some View {
        serverStringPicker(title, value: value.map(String.init), source: source, options: [("true", "On"), ("false", "Off")]) {
            update($0.flatMap(Bool.init))
        }
    }

    private func settingsToggle(_ title: String, value: Binding<Bool>) -> some View {
        Toggle(title, isOn: value).font(.headline)
    }

    private func settingsValue(_ title: String, value: String) -> some View {
        HStack { Text(title).font(.headline); Spacer(); Text(value).foregroundStyle(RivunePalette.secondary).lineLimit(1) }
    }
}
private struct HomeHeroCarousel: View {
    let items: [RivuneHeroItem]
    @ObservedObject var model: RivuneAppModel
    let height: CGFloat
    @State private var selection = 0

    var body: some View {
        TabView(selection: $selection) {
            ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                Button { model.openMedia(item.target) } label: {
                    ZStack(alignment: .bottomLeading) {
                        AsyncImage(url: item.backgroundUrl.flatMap(model.resolvedResourceURL)) { phase in
                            if let image = phase.image { image.resizable().scaledToFill() }
                            else { RivunePalette.surface }
                        }
                        .frame(height: height).clipped()
                        LinearGradient(colors: [.clear, .black.opacity(0.94)], startPoint: .center, endPoint: .bottom)
                        VStack(alignment: .leading, spacing: 12) {
                            if let logo = item.logoUrl {
                                AsyncImage(url: model.resolvedResourceURL(logo)) { phase in
                                    if let image = phase.image { image.resizable().scaledToFit() } else { Color.clear }
                                }.frame(maxWidth: 300, maxHeight: 100, alignment: .leading)
                            } else {
                                Text(item.title).font(.largeTitle.bold()).lineLimit(2)
                            }
                            if let releaseInfo = item.releaseInfo { Text(releaseInfo).foregroundStyle(RivunePalette.secondary) }
                            Label("View details", systemImage: "info.circle.fill").font(.headline)
                        }.padding(30)
                    }
                }
                .buttonStyle(.plain)
                .tag(index)
            }
        }
        .heroTabStyle(showIndicators: items.count > 1)
        .frame(height: height)
        .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
    }
}

private struct ContinueWatchingSection: View {
    let items: [ContinueWatchingItem]
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Continue watching")
                .font(.title2.bold())
            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: 16) {
                    ForEach(items) { item in
                        let width = responsiveTileWidth(for: .landscape, availableWidth: availableWidth)
                        Button { model.openMedia(item) } label: {
                            VStack(alignment: .leading, spacing: 8) {
                                AsyncImage(url: (item.episodeStillUrl ?? item.backgroundUrl ?? item.posterUrl).flatMap(model.resolvedResourceURL)) { phase in
                                    if let image = phase.image {
                                        image.resizable().scaledToFill()
                                    } else {
                                        ZStack {
                                            RivunePalette.surface
                                            Image(systemName: "play.rectangle.fill").foregroundStyle(RivunePalette.secondary)
                                        }
                                    }
                                }
                                .frame(width: width, height: width * 9 / 16)
                                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                                .overlay(alignment: .bottom) {
                                    GeometryReader { proxy in
                                        let progress = item.durationSeconds > 0 ? min(max(CGFloat(item.positionSeconds) / CGFloat(item.durationSeconds), 0), 1) : 0
                                        HStack(spacing: 0) {
                                            RivunePalette.accent.frame(width: proxy.size.width * progress)
                                            Color.white.opacity(0.24)
                                        }
                                    }
                                    .frame(height: 3)
                                }
                                Text(item.title ?? item.episodeTitle ?? "Continue watching")
                                    .font(.subheadline.weight(.semibold)).lineLimit(1).frame(width: width, alignment: .leading)
                                if let episodeTitle = item.episodeTitle {
                                    Text(episodeTitle).font(.caption).foregroundStyle(RivunePalette.secondary).lineLimit(1).frame(width: width, alignment: .leading)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
    }
}

private struct RecommendationSection: View {
    let items: [RivuneRecommendationItem]
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Recommended for you").font(.title2.bold())
            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: 16) {
                    ForEach(items) { item in
                        let width = responsiveTileWidth(for: .poster, availableWidth: availableWidth)
                        Button { model.openMedia(item.target) } label: {
                            VStack(alignment: .leading, spacing: 8) {
                                AsyncImage(url: item.target.posterUrl.flatMap(model.resolvedResourceURL)) { phase in
                                    if let image = phase.image { image.resizable().scaledToFill() }
                                    else { ZStack { RivunePalette.surface; Image(systemName: "sparkles.tv") } }
                                }
                                .frame(width: width, height: width * 1.5).clipShape(RoundedRectangle(cornerRadius: 14))
                                Text(item.target.title).font(.subheadline.weight(.semibold)).lineLimit(1).frame(width: width, alignment: .leading)
                                Text(item.reason).font(.caption).foregroundStyle(RivunePalette.secondary).lineLimit(2).frame(width: width, alignment: .leading)
                            }
                        }.buttonStyle(.plain)
                    }
                }
            }
        }
    }
}

private struct OfflineMediaSection: View {
    let items: [RivuneOfflineMediaItem]
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Downloads").font(.title2.bold())
            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: 16) {
                    ForEach(items) { item in
                        let width = responsiveTileWidth(for: .landscape, availableWidth: availableWidth)
                        VStack(alignment: .leading, spacing: 8) {
                            Button { model.playOffline(item) } label: {
                                ZStack { RivunePalette.surface; Image(systemName: "play.circle.fill").font(.largeTitle) }
                                    .frame(width: width, height: width * 9 / 16).clipShape(RoundedRectangle(cornerRadius: 14))
                            }.buttonStyle(.plain)
                            Text(item.title).font(.subheadline.weight(.semibold)).lineLimit(1).frame(width: width, alignment: .leading)
                            HStack { Text(ByteCountFormatter.string(fromByteCount: item.sizeBytes, countStyle: .file)); Spacer(); Button(role: .destructive) { model.removeOffline(item) } label: { Image(systemName: "trash") } }
                                .font(.caption).foregroundStyle(RivunePalette.secondary).frame(width: width)
                        }
                    }
                }
            }
        }
    }
}

private struct CollectionSection: View {
    let collection: Collection
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Text(collection.title)
                    .font(.title2.bold())
                Spacer()
                NavigationLink {
                    CollectionOverviewView(collection: collection, model: model)
                } label: {
                    Label("View all", systemImage: "chevron.right")
                        .labelStyle(.titleAndIcon)
                }
                .buttonStyle(.plain)
                .foregroundStyle(RivunePalette.accent)
            }
            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: 16) {
                    ForEach(collection.folders) { folder in
                        let tileShape = effectiveTileShape(collection: collection, folder: folder)
                        let width = responsiveTileWidth(for: tileShape, availableWidth: availableWidth)
#if os(tvOS)
                        Button {
                            model.openFolder(in: collection, folder: folder)
                        } label: {
                            FolderTile(folder: folder, tileShape: tileShape, width: width, imageURL: model.folderArtworkURL(for: folder))
                        }
                        .buttonStyle(.card)
                        .disabled(folder.id == nil)
#else
                        FolderTile(folder: folder, tileShape: tileShape, width: width, imageURL: model.folderArtworkURL(for: folder))
                            .frame(width: width, alignment: .leading)
                            .contentShape(Rectangle())
                            .onTapGesture { model.openFolder(in: collection, folder: folder) }
                            .allowsHitTesting(folder.id != nil)
                            .opacity(folder.id == nil ? 0.48 : 1)
                            .accessibilityAddTraits(.isButton)
                            .accessibilityAction { model.openFolder(in: collection, folder: folder) }
#endif
                    }
                }
                .padding(.vertical, 4)
            }
        }
    }
}

private struct CollectionOverviewView: View {
    let collection: Collection
    @ObservedObject var model: RivuneAppModel

    private let posterColumns = [GridItem(.adaptive(minimum: 150, maximum: 190), spacing: 18)]
    private let landscapeColumns = [GridItem(.adaptive(minimum: 240, maximum: 300), spacing: 18)]

    var body: some View {
        ZStack {
            RivuneBackground()
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    ScreenHeading(eyebrow: model.serverName, title: collection.title, bodyText: nil)
                    LazyVGrid(
                        columns: collectionUsesLandscapeTiles(collection) ? landscapeColumns : posterColumns,
                        alignment: .leading,
                        spacing: 24
                    ) {
                        ForEach(collection.folders) { folder in
                            let tileShape = effectiveTileShape(collection: collection, folder: folder)
                            Button {
                                model.openFolder(in: collection, folder: folder)
                            } label: {
                                FolderTile(
                                    folder: folder,
                                    tileShape: tileShape,
                                    width: tileWidth(for: tileShape),
                                    imageURL: model.folderArtworkURL(for: folder)
                                )
                                .frame(maxWidth: .infinity)
                            }
                            .buttonStyle(.plain)
#if os(tvOS)
                            .buttonStyle(.card)
#endif
                            .disabled(folder.id == nil)
                        }
                    }
                }
                .padding(28)
                .frame(maxWidth: 1200)
                .frame(maxWidth: .infinity)
            }
        }
    }
}

private func effectiveTileShape(collection: Collection, folder: CollectionFolder) -> CollectionTileShape {
    collection.viewMode == .followLayout ? folder.tileShape : collection.folderCoverShape
}

private func collectionUsesLandscapeTiles(_ collection: Collection) -> Bool {
    collection.folders.contains { effectiveTileShape(collection: collection, folder: $0) == .landscape }
}

private func tileWidth(for shape: CollectionTileShape) -> CGFloat {
    switch shape {
    case .poster: return 128
    case .landscape: return 220
    case .square: return 146
    }
}

private func responsiveTileWidth(for shape: CollectionTileShape, availableWidth: CGFloat) -> CGFloat {
#if os(tvOS)
    switch shape {
    case .poster: return min(max(availableWidth / 6.2, 170), 220)
    case .landscape: return min(max(availableWidth / 4.4, 240), 320)
    case .square: return min(max(availableWidth / 5.4, 190), 250)
    }
#else
    switch shape {
    case .poster: return min(max(availableWidth / 3.2, 92), 128)
    case .landscape: return min(max((availableWidth - 16) / 2.15, 148), 220)
    case .square: return min(max(availableWidth / 2.75, 112), 146)
    }
#endif
}

private struct FolderTile: View {
    let folder: CollectionFolder
    let tileShape: CollectionTileShape
    let width: CGFloat
    let imageURL: URL?

    private var tileSize: CGSize {
        switch tileShape {
        case .poster: return CGSize(width: width, height: width * 1.5)
        case .landscape: return CGSize(width: width, height: width * 9 / 16)
        case .square: return CGSize(width: width, height: width)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            AsyncImage(url: imageURL) { phase in
                if let image = phase.image {
                    image.resizable().scaledToFill()
                } else {
                    ZStack {
                        LinearGradient(
                            colors: [RivunePalette.raised, RivunePalette.surface],
                            startPoint: .topLeading,
                            endPoint: .bottomTrailing
                        )
                        if let emoji = folder.coverEmoji, !emoji.isEmpty {
                            Text(emoji).font(.system(size: 42))
                        } else {
                            Image(systemName: "rectangle.stack.fill")
                                .font(.system(size: 32))
                                .foregroundStyle(RivunePalette.secondary)
                        }
                    }
                }
            }
            .frame(width: tileSize.width, height: tileSize.height)
            .clipShape(RoundedRectangle(cornerRadius: 15, style: .continuous))
            if !folder.hideTitle {
                Text(folder.title)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                    .frame(width: tileSize.width, alignment: .center)
                    .multilineTextAlignment(.center)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

private struct FolderView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var mediaFilter: String?

    private let posterColumns = [GridItem(.adaptive(minimum: 140, maximum: 190), spacing: 18)]
    private let landscapeColumns = [GridItem(.adaptive(minimum: 240, maximum: 300), spacing: 18)]

    var body: some View {
        ZStack {
            RivuneBackground()
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    HStack {
                        Button(action: model.closeFolder) {
                            Label("Library", systemImage: "chevron.left")
                        }
                        .rivuneGlassButton()
                        Spacer()
                    }
                    if let opened = model.openedFolder {
                        ScreenHeading(
                            eyebrow: model.serverName,
                            title: opened.folder.title,
                            bodyText: opened.items?.isEmpty == true ? "This folder contains no visible titles." : nil
                        )
                        if model.isBusy && opened.items == nil {
                            HStack(spacing: 12) {
                                ProgressView()
                                Text("Loading titles…")
                            }
                            .foregroundStyle(RivunePalette.secondary)
                        }
                        FailureText(failure: model.failure)
                        if let items = opened.items {
                            let supportsMediaFilter = opened.folder.sources.contains {
                                $0.tmdb?.mediaType == .both
                            }
                            if supportsMediaFilter {
                                HStack(spacing: 10) {
                                    MediaFilterButton(title: "All", systemImage: "rectangle.stack.fill", selected: mediaFilter == nil) {
                                        mediaFilter = nil
                                    }
                                    MediaFilterButton(title: "Movies", systemImage: "film.fill", selected: mediaFilter == "movie") {
                                        mediaFilter = "movie"
                                    }
                                    MediaFilterButton(title: "Series", systemImage: "tv.fill", selected: mediaFilter == "series") {
                                        mediaFilter = "series"
                                    }
                                }
                            }
                            let visibleItems = mediaFilter.map { filter in items.filter { $0.mediaType == filter } } ?? items
                            let folderLandscape = opened.folder.tileShape == .landscape
                            LazyVGrid(columns: folderLandscape ? landscapeColumns : posterColumns, alignment: .leading, spacing: 24) {
                                ForEach(visibleItems) { item in
                                    let landscape = folderLandscape || item.mediaType == "tv"
                                    let artwork = landscape ? item.backgroundUrl ?? item.posterUrl : item.posterUrl ?? item.backgroundUrl
                                    Button { model.openMedia(item) } label: {
                                        CollectionItemTile(
                                            item: item,
                                            imageURL: artwork.flatMap(model.resolvedResourceURL),
                                            landscape: landscape
                                        )
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                            if opened.hasMore {
                                Button(action: model.loadMoreFolderItems) {
                                    if model.isBusy { ProgressView() }
                                    else { Label("Load more", systemImage: "arrow.down.circle") }
                                }
                                .rivuneGlassButton(prominent: true)
                                .disabled(model.isBusy)
                                .frame(maxWidth: .infinity)
                            }
                        }
                        if !opened.errors.isEmpty {
                            Label("Some collection sources were unavailable.", systemImage: "exclamationmark.triangle")
                                .font(.footnote)
                                .foregroundStyle(RivunePalette.secondary)
                        }
                    }
                }
                .padding(28)
                .frame(maxWidth: 1200)
                .frame(maxWidth: .infinity)
            }
        }
        .onChange(of: model.openedFolder?.id) { _ in mediaFilter = nil }
        .sheet(isPresented: Binding(
            get: { model.mediaLoading || model.mediaDetail != nil || model.mediaFailure != nil },
            set: { if !$0 { model.closeMedia() } }
        )) {
            RivuneMediaDetailView(model: model)
        }
    }
}

private struct MediaFilterButton: View {
    let title: String
    let systemImage: String
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label(title, systemImage: systemImage)
        }
        .rivuneGlassButton(prominent: selected)
        .tint(selected ? RivunePalette.accent : .clear)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

private struct CollectionItemTile: View {
    let item: CollectionItem
    let imageURL: URL?
    let landscape: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            AsyncImage(url: imageURL) { phase in
                if let image = phase.image {
                    image.resizable().scaledToFill()
                } else {
                    ZStack {
                        RivunePalette.surface
                        Image(systemName: item.mediaType == "series" ? "tv" : "film")
                            .font(.system(size: 30))
                            .foregroundStyle(RivunePalette.secondary)
                    }
                }
            }
            .frame(maxWidth: .infinity)
            .aspectRatio(landscape ? 16.0 / 9.0 : 2.0 / 3.0, contentMode: .fit)
            .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            Text(item.title)
                .font(.subheadline.weight(.semibold))
                .lineLimit(2)
            if let releaseInfo = item.releaseInfo {
                Text(releaseInfo)
                    .font(.caption)
                    .foregroundStyle(RivunePalette.secondary)
                    .lineLimit(1)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

private extension View {
    @ViewBuilder
    func folderPresentation<Content: View>(
        item: Binding<OpenedCollectionFolder?>,
        onDismiss: @escaping () -> Void,
        @ViewBuilder content: @escaping (OpenedCollectionFolder) -> Content
    ) -> some View {
#if os(tvOS)
        fullScreenCover(item: item, onDismiss: onDismiss, content: content)
#else
        sheet(item: item, onDismiss: onDismiss, content: content)
#endif
    }


    @ViewBuilder
    func heroTabStyle(showIndicators: Bool) -> some View {
#if os(macOS)
        tabViewStyle(.automatic)
#else
        tabViewStyle(.page(indexDisplayMode: showIndicators ? .automatic : .never))
#endif
    }
}
