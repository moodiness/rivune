#if os(tvOS)
import SwiftUI
import UIKit

@MainActor
struct RivuneTelevisionServerView: View {
    @ObservedObject var model: RivuneAppModel
    @StateObject private var browser = RivuneLANBrowser()
    @State private var selectedServer: DiscoveredRivuneServer?
    @State private var address = ""
    @State private var showsManualAddress = false
    @State private var discoveryGeneration = 0
    @State private var isSearching = true
    @FocusState private var focusedServerID: String?

    var body: some View {
        ZStack {
            televisionBackground
            VStack(alignment: .leading, spacing: 22) {
                header
                controls
            }
            .frame(maxWidth: 1_120, maxHeight: .infinity, alignment: .center)
            .padding(.horizontal, 80)
            .padding(.vertical, 56)
        }
        .onAppear {
            address = model.serverAddress
            refreshDiscovery()
        }
        .onDisappear {
            discoveryGeneration += 1
            browser.stop()
        }
        .onChange(of: browser.servers) { servers in
            if !servers.isEmpty { isSearching = false }
            if focusedServerID == nil { focusedServerID = servers.first?.id }
        }
        .task(id: discoveryGeneration) {
            guard discoveryGeneration > 0, isSearching else { return }
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            guard !Task.isCancelled else { return }
            isSearching = false
        }
        .confirmationDialog(
            selectedServer.map { rivuneLocalizedFormat("Connect to %@?", $0.name) }
                ?? rivuneLocalized("Connect to this server?"),
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
            Text(
                server.address.absoluteString + "\n\n"
                    + rivuneLocalized(
                        server.usesSecureTransport
                            ? "Encrypted HTTPS connection."
                            : "Unencrypted HTTP. Continue only on a trusted private network."
                    )
            )
        }
        .sheet(isPresented: $showsManualAddress) {
            manualAddressView
        }
    }

    private var televisionBackground: some View {
        ZStack {
            Color.black
            RadialGradient(
                colors: [Color(red: 0.12, green: 0.20, blue: 0.38).opacity(0.42), .clear],
                center: .topTrailing,
                startRadius: 40,
                endRadius: 900
            )
            LinearGradient(
                colors: [Color.white.opacity(0.035), .clear, Color.black.opacity(0.55)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        }
        .ignoresSafeArea()
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 14) {
                televisionMark
                    .frame(width: 42, height: 42)
                Text("Rivune")
                    .font(.system(size: 30, weight: .bold, design: .rounded))
                Text(rivuneLocalized("YOUR SERVER"))
                    .font(.system(size: 17, weight: .bold))
                    .tracking(2)
                    .foregroundStyle(televisionAccent)
                    .padding(.leading, 12)
            }
            Text(rivuneLocalized("Connect to Rivune"))
                .font(.system(size: 42, weight: .bold, design: .rounded))
            Text(rivuneLocalized("Choose a nearby server with the remote, or enter its address manually."))
                .font(.system(size: 21, weight: .regular))
                .foregroundStyle(Color.white.opacity(0.68))
                .lineLimit(2)
        }
    }

    private var controls: some View {
        VStack(alignment: .leading, spacing: 18) {
            nearbyServersPanel
            HStack(spacing: 18) {
                Button {
                    address = model.serverAddress
                    showsManualAddress = true
                } label: {
                    televisionActionLabel("Enter server address", systemImage: "keyboard")
                }
                .buttonStyle(.card)
                .frame(maxWidth: .infinity)

                Button(action: refreshDiscovery) {
                    Group {
                        if isSearching {
                            HStack(spacing: 12) {
                                ProgressView()
                                Text("Searching…")
                            }
                        } else {
                            Label("Search again", systemImage: "arrow.clockwise")
                        }
                    }
                    .font(.system(size: 20, weight: .semibold))
                    .foregroundStyle(.white)
                    .frame(maxWidth: .infinity, minHeight: 60)
                    .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                }
                .buttonStyle(.card)
                .frame(maxWidth: .infinity)
                .disabled(isSearching)
            }

            if let failure = model.failure {
                Label(rivuneLocalized(failure.localizedDescription), systemImage: "exclamationmark.triangle.fill")
                    .font(.headline)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 22)
                    .padding(.vertical, 18)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.red.opacity(0.12), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    .accessibilityIdentifier("failure-message")
            }

            offlineProfiles
        }
    }

    private var nearbyServersPanel: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 18) {
                VStack(alignment: .leading, spacing: 5) {
                    Text("Nearby servers")
                        .font(.system(size: 30, weight: .bold))
                    Text("Rivune on your local network")
                        .font(.system(size: 18))
                        .foregroundStyle(Color.white.opacity(0.62))
                }
                Spacer()
                discoveryStatus
            }
            .padding(.horizontal, 22)
            .padding(.vertical, 18)

            Divider().overlay(Color.white.opacity(0.12))

            if browser.servers.isEmpty {
                HStack(spacing: 20) {
                    if isSearching {
                        ProgressView()
                    } else {
                        Image(systemName: "wifi.slash")
                            .font(.title2)
                            .foregroundStyle(Color.white.opacity(0.58))
                    }
                    VStack(alignment: .leading, spacing: 7) {
                        Text(rivuneLocalized(isSearching ? "Looking on your local network" : "No servers found"))
                            .font(.headline)
                        Text(rivuneLocalized(isSearching
                            ? "Nearby Rivune servers will appear automatically."
                            : "Check that the server is running and connected to the same network."))
                            .font(.callout)
                            .foregroundStyle(Color.white.opacity(0.62))
                    }
                }
                .frame(maxWidth: .infinity, minHeight: 170, alignment: .leading)
                .padding(.horizontal, 32)
            } else {
                if browser.servers.count <= 3 {
                    serverList.padding(28)
                } else {
                    ScrollView {
                        serverList.padding(28)
                    }
                    .frame(maxHeight: 430)
                }
            }
        }
        .background(televisionSurface, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 22, style: .continuous)
                .stroke(Color.white.opacity(0.10), lineWidth: 1)
        }
    }

    private var serverList: some View {
        LazyVStack(spacing: 12) {
            ForEach(browser.servers) { server in
                Button {
                    model.clearFailure()
                    selectedServer = server
                } label: {
                    serverRow(server)
                }
                .buttonStyle(.card)
                .focused($focusedServerID, equals: server.id)
                .disabled(model.isBusy)
                .accessibilityIdentifier("discovered-server-\(server.id)")
            }
        }
    }

    @ViewBuilder
    private var televisionMark: some View {
        if let path = Bundle.module.path(forResource: "RivuneMark", ofType: "png"),
           let image = UIImage(contentsOfFile: path) {
            Image(uiImage: image)
                .resizable()
                .scaledToFit()
        } else {
            Image(systemName: "play.rectangle.fill")
                .resizable()
                .scaledToFit()
        }
    }

    private func televisionActionLabel(_ title: String, systemImage: String) -> some View {
        Label(rivuneLocalized(title), systemImage: systemImage)
            .font(.system(size: 20, weight: .semibold))
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity, minHeight: 60)
            .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    private func serverRow(_ server: DiscoveredRivuneServer) -> some View {
        HStack(spacing: 18) {
            Image(systemName: server.usesSecureTransport ? "lock.shield.fill" : "network")
                .font(.system(size: 24, weight: .semibold))
                .foregroundStyle(server.usesSecureTransport ? .green : televisionAccent)
                .frame(width: 46, height: 46)
                .background(Color.white.opacity(0.08), in: Circle())
            VStack(alignment: .leading, spacing: 5) {
                Text(server.name)
                    .font(.system(size: 26, weight: .bold))
                    .lineLimit(1)
                Text(server.address.absoluteString)
                    .font(.system(size: 17, design: .monospaced))
                    .foregroundStyle(Color.white.opacity(0.64))
                    .lineLimit(1)
            }
            Spacer(minLength: 20)
            if model.isBusy {
                ProgressView()
            } else {
                Text(server.usesSecureTransport ? "HTTPS" : "LAN")
                    .font(.caption.weight(.bold))
                    .padding(.horizontal, 12)
                    .padding(.vertical, 7)
                    .background(Color.white.opacity(0.10), in: Capsule())
                Image(systemName: "chevron.right")
                    .font(.headline.weight(.bold))
                    .foregroundStyle(Color.white.opacity(0.62))
            }
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 16)
        .frame(maxWidth: .infinity, minHeight: 84, alignment: .leading)
        .background(Color.white.opacity(0.055), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
    }

    @ViewBuilder
    private var offlineProfiles: some View {
        if !model.offlineProfiles.isEmpty {
            VStack(alignment: .leading, spacing: 16) {
                Text("Downloaded profiles")
                    .font(.headline)
                    .foregroundStyle(Color.white.opacity(0.72))
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 18) {
                        ForEach(model.offlineProfiles) { profile in
                            Button { model.requestOfflineUnlock(profile) } label: {
                                Label(profile.name, systemImage: profile.requiresPIN ? "lock.fill" : "arrow.down.circle.fill")
                                    .frame(minWidth: 210, minHeight: 70)
                            }
                            .buttonStyle(.card)
                        }
                    }
                }
            }
        }
    }

    private var discoveryStatus: some View {
        Group {
            if isSearching {
                HStack(spacing: 10) {
                    ProgressView()
                    Text("Searching")
                }
            } else {
                Text(rivuneLocalizedFormat(
                    browser.servers.count == 1 ? "%d server" : "%d servers",
                    browser.servers.count
                ))
            }
        }
        .font(.system(size: 17, weight: .semibold))
        .foregroundStyle(Color.white.opacity(0.72))
        .padding(.horizontal, 13)
        .padding(.vertical, 8)
        .background(Color.white.opacity(0.08), in: Capsule())
    }

    private var manualAddressView: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 28) {
                VStack(alignment: .leading, spacing: 10) {
                    Text("Server address")
                        .font(.largeTitle.bold())
                    Text("Use this only when the server does not appear on the local network.")
                        .font(.headline)
                        .foregroundStyle(Color.white.opacity(0.68))
                }
                TextField("https://rivune.example.com", text: $address)
                    .font(.title3.monospaced())
                    .frame(minHeight: 78)
                    .accessibilityIdentifier("server-address")
                HStack(spacing: 18) {
                    Button("Cancel", role: .cancel) { showsManualAddress = false }
                        .buttonStyle(.bordered)
                    Button {
                        submitManualAddress()
                    } label: {
                        if model.isBusy {
                            ProgressView()
                        } else {
                            Label("Connect", systemImage: "arrow.right")
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.isBusy)
                    .accessibilityIdentifier("server-connect")
                }
                .controlSize(.large)
            }
            .frame(maxWidth: 880)
            .padding(64)
        }
    }

    private var televisionAccent: Color {
        Color(red: 0.47, green: 0.65, blue: 1.0)
    }

    private var televisionSurface: Color {
        Color(red: 0.075, green: 0.075, blue: 0.085).opacity(0.96)
    }

    private func refreshDiscovery() {
        discoveryGeneration += 1
        isSearching = true
        browser.start()
    }

    private func submitManualAddress() {
        let value = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return }
        showsManualAddress = false
        model.connect(to: value)
    }
}
#endif
