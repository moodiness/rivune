#if os(iOS)
import SwiftUI
import RivuneAPI
import UIKit

// iOS owns its visual language. Other Apple clients deliberately do not use these types.
enum RivuneIOSTheme {
    static let canvas = Color(red: 5 / 255, green: 5 / 255, blue: 5 / 255)
    static let surface = Color(red: 13 / 255, green: 13 / 255, blue: 13 / 255)
    static let raised = Color(red: 20 / 255, green: 20 / 255, blue: 20 / 255)
    static let hairline = Color(red: 36 / 255, green: 36 / 255, blue: 36 / 255)
    static let outline = Color(red: 57 / 255, green: 57 / 255, blue: 57 / 255)
    static let primaryText = Color(red: 244 / 255, green: 241 / 255, blue: 236 / 255)
    static let secondaryText = Color(red: 200 / 255, green: 196 / 255, blue: 190 / 255)
    static let mutedText = Color(red: 140 / 255, green: 146 / 255, blue: 154 / 255)
    static let ember = Color(red: 1, green: 143 / 255, blue: 112 / 255)
    static let emberPressed = Color(red: 231 / 255, green: 122 / 255, blue: 95 / 255)
    static let danger = Color(red: 1, green: 141 / 255, blue: 146 / 255)
    static let success = Color(red: 121 / 255, green: 213 / 255, blue: 174 / 255)
    static let warning = Color(red: 232 / 255, green: 184 / 255, blue: 112 / 255)

    static func pageInset(for width: CGFloat) -> CGFloat { width < 700 ? 20 : 32 }
    static func pageTopInset(for width: CGFloat) -> CGFloat { width < 700 ? 20 : 28 }
    static func gridMinimum(for width: CGFloat) -> CGFloat { width < 700 ? 142 : 170 }
}


struct RivuneIOSCanvas: View {
    var body: some View { RivuneIOSTheme.canvas.ignoresSafeArea() }
}

struct RivuneIOSBrand: View {
    var compact = false

    var body: some View {
        HStack(spacing: compact ? 9 : 12) {
            mark
                .frame(width: compact ? 30 : 40, height: compact ? 30 : 40)
            Text("Rivune")
                .font(.system(size: compact ? 22 : 29, weight: .bold, design: .rounded))
                .foregroundStyle(RivuneIOSTheme.primaryText)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Rivune")
    }

    @ViewBuilder
    private var mark: some View {
        if let path = Bundle.module.path(forResource: "RivuneMark", ofType: "png"),
           let image = UIImage(contentsOfFile: path) {
            Image(uiImage: image).resizable().scaledToFit()
        } else {
            Image(systemName: "play.square.stack.fill")
                .resizable()
                .scaledToFit()
                .foregroundStyle(RivuneIOSTheme.primaryText)
        }
    }
}

struct RivuneIOSPage<Content: View>: View {
    let maximumWidth: CGFloat
    let verticalAlignment: Alignment
    private let content: Content

    init(
        maximumWidth: CGFloat = 1180,
        verticalAlignment: Alignment = .top,
        @ViewBuilder content: () -> Content
    ) {
        self.maximumWidth = maximumWidth
        self.verticalAlignment = verticalAlignment
        self.content = content()
    }

    var body: some View {
        GeometryReader { proxy in
            ScrollView {
                content
                    .frame(maxWidth: maximumWidth, alignment: .leading)
                    .padding(.horizontal, RivuneIOSTheme.pageInset(for: proxy.size.width))
                    .padding(.top, RivuneIOSTheme.pageTopInset(for: proxy.size.width))
                    .padding(.bottom, 40)
                    .frame(maxWidth: .infinity, minHeight: proxy.size.height, alignment: verticalAlignment)
            }
            .modifier(RivuneIOSKeyboardDismissModifier())
        }
    }
}
private struct RivuneIOSKeyboardDismissModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
        if #available(iOS 16.0, *) {
            content.scrollDismissesKeyboard(.interactively)
        } else {
            content
        }
    }
}


struct RivuneIOSAccessPage<Content: View>: View {
    private let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        RivuneIOSPage(maximumWidth: 620, verticalAlignment: .top) {
            content
                .padding(.top, 44)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            GeometryReader { proxy in
                HStack {
                    RivuneIOSBrand()
                    Spacer()
                }
                .padding(.horizontal, RivuneIOSTheme.pageInset(for: proxy.size.width))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .frame(height: 64)
        }
    }
}


struct RivuneIOSHeading: View {
    let eyebrow: String?
    let title: String
    let message: String?
    var centered = false

    init(eyebrow: String? = nil, title: String, message: String? = nil, centered: Bool = false) {
        self.eyebrow = eyebrow
        self.title = title
        self.message = message
        self.centered = centered
    }

    var body: some View {
        VStack(alignment: centered ? .center : .leading, spacing: 10) {
            if let eyebrow, !eyebrow.isEmpty {
                Text(rivuneLocalized(eyebrow).uppercased())
                    .font(.caption.weight(.bold))
                    .tracking(1.6)
                    .foregroundStyle(RivuneIOSTheme.ember)
            }
            Text(rivuneLocalized(title))
                .font(.largeTitle.bold())
                .foregroundStyle(RivuneIOSTheme.primaryText)
                .multilineTextAlignment(centered ? .center : .leading)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityAddTraits(.isHeader)
            if let message, !message.isEmpty {
                Text(rivuneLocalized(message))
                    .font(.body)
                    .foregroundStyle(RivuneIOSTheme.secondaryText)
                    .multilineTextAlignment(centered ? .center : .leading)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: centered ? .center : .leading)
    }
}

struct RivuneIOSSectionHeader: View {
    let title: String
    var subtitle: String?
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(rivuneLocalized(title))
                    .font(.title2.bold())
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                if let subtitle {
                    Text(rivuneLocalized(subtitle))
                        .font(.subheadline)
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                }
            }
            Spacer(minLength: 8)
            if let actionTitle, let action {
                Button(action: action) {
                    HStack(spacing: 5) {
                        Text(rivuneLocalized(actionTitle))
                        Image(systemName: "chevron.right")
                            .font(.caption.bold())
                    }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(RivuneIOSTheme.ember)
                    .frame(minHeight: 48)
                }
                .buttonStyle(.plain)
            }
        }
        .accessibilityElement(children: .contain)
    }
}

private struct RivuneIOSButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled
    let prominent: Bool
    let destructive: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(foreground)
            .padding(.horizontal, 18)
            .frame(minHeight: 52)
            .background(background(configuration.isPressed), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .stroke(border, lineWidth: 1)
            }
            .contentShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            .opacity(isEnabled ? 1 : 0.42)
            .scaleEffect(configuration.isPressed ? 0.985 : 1)
            .rivuneAnimation(.easeOut(duration: 0.14), value: configuration.isPressed)
    }

    private var foreground: Color {
        if prominent { return Color.black.opacity(0.88) }
        if destructive { return RivuneIOSTheme.danger }
        return RivuneIOSTheme.primaryText
    }

    private func background(_ pressed: Bool) -> Color {
        if prominent { return pressed ? RivuneIOSTheme.emberPressed : RivuneIOSTheme.ember }
        if destructive { return pressed ? RivuneIOSTheme.danger.opacity(0.18) : RivuneIOSTheme.danger.opacity(0.10) }
        return pressed ? RivuneIOSTheme.outline.opacity(0.72) : RivuneIOSTheme.raised
    }

    private var border: Color {
        if prominent { return Color.clear }
        if destructive { return RivuneIOSTheme.danger.opacity(0.36) }
        return RivuneIOSTheme.outline
    }
}

private struct RivuneIOSIconButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled
    var destructive = false

    func makeBody(configuration: Configuration) -> some View {
        let color = destructive ? RivuneIOSTheme.danger : RivuneIOSTheme.primaryText
        let background = destructive
            ? RivuneIOSTheme.danger.opacity(configuration.isPressed ? 0.18 : 0.10)
            : (configuration.isPressed ? RivuneIOSTheme.outline.opacity(0.8) : RivuneIOSTheme.raised)
        let border = destructive ? RivuneIOSTheme.danger.opacity(0.36) : RivuneIOSTheme.outline

        configuration.label
            .foregroundStyle(color)
            .frame(width: 48, height: 48)
            .background(background, in: Circle())
            .overlay { Circle().stroke(border, lineWidth: 1) }
            .opacity(isEnabled ? 1 : 0.42)
            .scaleEffect(configuration.isPressed ? 0.96 : 1)
    }
}

private struct RivuneIOSGlassButtonFallbackStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(RivuneIOSTheme.primaryText)
            .background(.ultraThinMaterial, in: Capsule())
            .overlay {
                Capsule()
                    .stroke(Color.white.opacity(configuration.isPressed ? 0.26 : 0.14), lineWidth: 1)
            }
            .opacity(isEnabled ? 1 : 0.42)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .rivuneAnimation(.easeOut(duration: 0.14), value: configuration.isPressed)
    }
}

private struct RivuneIOSGlassButtonModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
        if #available(iOS 26.0, *) {
            content
                .buttonStyle(.glass)
                .buttonBorderShape(.capsule)
                .tint(.clear)
        } else {
            content.buttonStyle(RivuneIOSGlassButtonFallbackStyle())
        }
    }
}

private struct RivuneIOSGlassSurfaceModifier: ViewModifier {
    let hasFailure: Bool

    @ViewBuilder
    func body(content: Content) -> some View {
        if #available(iOS 26.0, *) {
            content
                .glassEffect(.clear.interactive(), in: Capsule())
                .overlay { border }
        } else {
            content
                .background(.ultraThinMaterial, in: Capsule())
                .overlay { border }
        }
    }

    private var border: some View {
        Capsule()
            .stroke(hasFailure ? RivuneIOSTheme.danger : Color.white.opacity(0.14), lineWidth: 1)
    }
}

private struct RivuneIOSDynamicTitleModifier: ViewModifier {
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let standardLimit: Int

    func body(content: Content) -> some View {
        content
            .lineLimit(
                RivuneDynamicTitlePolicy.lineLimit(
                    isAccessibilitySize: dynamicTypeSize.isAccessibilitySize,
                    standardLimit: standardLimit
                )
            )
            .fixedSize(horizontal: false, vertical: true)
    }
}

private struct RivuneIOSCardModifier: ViewModifier {
    let inset: CGFloat
    func body(content: Content) -> some View {
        content
            .padding(inset)
            .background(RivuneIOSTheme.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 20, style: .continuous)
                    .stroke(RivuneIOSTheme.hairline, lineWidth: 1)
            }
    }
}

extension View {
    func rivuneIOSDynamicTitle(standardLimit: Int) -> some View {
        modifier(RivuneIOSDynamicTitleModifier(standardLimit: standardLimit))
    }


    func rivuneIOSPrimaryButton() -> some View {
        buttonStyle(RivuneIOSButtonStyle(prominent: true, destructive: false))
    }

    func rivuneIOSSecondaryButton() -> some View {
        buttonStyle(RivuneIOSButtonStyle(prominent: false, destructive: false))
    }

    func rivuneIOSDangerButton() -> some View {
        buttonStyle(RivuneIOSButtonStyle(prominent: false, destructive: true))
    }

    func rivuneIOSIconButton(destructive: Bool = false) -> some View {
        buttonStyle(RivuneIOSIconButtonStyle(destructive: destructive))
    }

    func rivuneIOSGlassButton() -> some View {
        modifier(RivuneIOSGlassButtonModifier())
    }

    func rivuneIOSGlassSurface(hasFailure: Bool = false) -> some View {
        modifier(RivuneIOSGlassSurfaceModifier(hasFailure: hasFailure))
    }

    func rivuneIOSCard(inset: CGFloat = 18) -> some View {
        modifier(RivuneIOSCardModifier(inset: inset))
    }
}

struct RivuneIOSInlineError: View {
    let message: String
    var centered = false

    var body: some View {
        Label {
            Text(message)
                .fixedSize(horizontal: false, vertical: true)
        } icon: {
            Image(systemName: "exclamationmark.circle.fill")
        }
        .font(.caption.weight(.medium))
        .foregroundStyle(RivuneIOSTheme.danger)
        .multilineTextAlignment(centered ? .center : .leading)
        .frame(maxWidth: .infinity, alignment: centered ? .center : .leading)
        .accessibilityIdentifier("failure-message")
    }
}

struct RivuneIOSErrorMessage: View {
    let failure: RivuneAppFailure?

    var body: some View {
        if let failure {
            RivuneIOSInlineError(message: rivuneLocalized(failure.localizedDescription))
        }
    }
}

struct RivuneIOSStatusView: View {
    enum State {
        case loading(String)
        case empty(icon: String, title: String, message: String?)
        case failure(RivuneAppFailure)
    }

    let state: State

    var body: some View {
        VStack(spacing: 12) {
            switch state {
            case .loading(let title):
                ProgressView().tint(RivuneIOSTheme.ember)
                Text(rivuneLocalized(title))
                    .foregroundStyle(RivuneIOSTheme.secondaryText)
            case .empty(let icon, let title, let message):
                Image(systemName: icon)
                    .font(.system(size: 34, weight: .medium))
                    .foregroundStyle(RivuneIOSTheme.mutedText)
                Text(rivuneLocalized(title))
                    .font(.headline)
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                if let message {
                    Text(rivuneLocalized(message))
                        .font(.callout)
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                        .multilineTextAlignment(.center)
                }
            case .failure(let failure):
                RivuneIOSInlineError(
                    message: rivuneLocalized(failure.localizedDescription),
                    centered: true
                )
            }
        }
        .frame(maxWidth: .infinity, minHeight: 160)
        .padding(20)
        .accessibilityElement(children: .combine)
    }
}

struct RivuneIOSChip: View {
    let title: String
    var icon: String?
    var selected = false

    var body: some View {
        HStack(spacing: 6) {
            if let icon { Image(systemName: icon) }
            Text(rivuneLocalized(title)).lineLimit(1)
        }
        .font(.subheadline.weight(.semibold))
        .foregroundStyle(selected ? Color.black.opacity(0.84) : RivuneIOSTheme.secondaryText)
        .padding(.horizontal, 14)
        .frame(minHeight: 44)
        .background(selected ? RivuneIOSTheme.ember : RivuneIOSTheme.raised, in: Capsule())
        .overlay { Capsule().stroke(selected ? Color.clear : RivuneIOSTheme.outline, lineWidth: 1) }
    }
}
struct RivuneIOSTileTitle: View {
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let title: String
    var width: CGFloat?
    var centered = false

    @ViewBuilder
    var body: some View {
        let title = Text(title)
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(RivuneIOSTheme.primaryText)
            .lineLimit(
                RivuneDynamicTitlePolicy.lineLimit(
                    isAccessibilitySize: dynamicTypeSize.isAccessibilitySize
                )
            )
            .fixedSize(horizontal: false, vertical: true)
            .multilineTextAlignment(centered ? .center : .leading)
            .frame(
                minHeight: dynamicTypeSize.isAccessibilitySize ? nil : 38,
                alignment: centered ? .top : .topLeading
            )

        if let width {
            title.frame(width: width, alignment: centered ? .top : .topLeading)
        } else {
            title
        }
    }
}

struct RivuneIOSArtwork: View {
    let url: URL?
    let aspectRatio: CGFloat
    let fallbackSystemImage: String
    var cornerRadius: CGFloat = 14

    var body: some View {
        ZStack {
            RivuneIOSTheme.raised
            AsyncImage(url: url) { phase in
                if let image = phase.image {
                    image.resizable().scaledToFill()
                } else {
                    Image(systemName: fallbackSystemImage)
                        .font(.system(size: 30, weight: .medium))
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                }
            }
        }
        .aspectRatio(aspectRatio, contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                .stroke(RivuneIOSTheme.hairline, lineWidth: 1)
        }
        .clipped()
    }
}

struct RivuneIOSAccountButton: View {
    let profile: RivuneAPI.Profile?
    let imageData: Data?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Group {
                if let profile {
                    ProfileAvatarImage(profile: profile, imageData: imageData)
                } else {
                    Image(systemName: "person.crop.circle.fill")
                        .resizable()
                        .foregroundStyle(RivuneIOSTheme.mutedText)
                }
            }
            .frame(width: 38, height: 38)
            .clipShape(Circle())
            .overlay { Circle().stroke(RivuneIOSTheme.outline, lineWidth: 1) }
            .frame(width: 48, height: 48)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(profile?.name ?? rivuneLocalized("Account"))
    }
}
#endif
