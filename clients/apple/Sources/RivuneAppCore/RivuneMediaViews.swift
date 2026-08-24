import AVFoundation
import AVKit
import Combine
import SwiftUI
#if canImport(AppKit)
import AppKit
import QuartzCore
import UniformTypeIdentifiers
#endif
import RivuneAPI

struct RivuneMediaDetailView: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var showJoinRoom = false
    @State private var roomCode = ""
#if os(macOS)
    @StateObject private var seasonRailDrag = RivuneHorizontalRailDragController()
    @StateObject private var episodeRailDrag = RivuneHorizontalRailDragController()
#endif

    var body: some View {
        Group {
#if os(macOS)
            if model.showPlaybackSources {
                macOSDetailAndSources
            } else {
                presentationContent
            }
#else
            if model.showPlaybackSources {
                RivunePlaybackSourcesView(model: model)
            } else {
                presentationContent
            }
#endif
        }
#if !os(macOS)
        .mediaPlayerPresentation(item: Binding(
            get: { model.playbackPresentation },
            set: { _ in }
        )) { presentation in
            RivuneInternalPlayerView(presentation: presentation, model: model)
        }
#endif
        .preferredColorScheme(.dark)
#if os(iOS) || os(visionOS)
        .sheet(isPresented: Binding(
            get: { model.externalPlaybackURL != nil },
            set: { if !$0 { model.clearExternalPlaybackURL() } }
        )) {
            if let url = model.externalPlaybackURL { RivuneExternalPlaybackSheet(url: url) }
        }
#elseif os(macOS)
        .sheet(isPresented: Binding(
            get: { model.externalPlaybackURL != nil },
            set: { if !$0 { model.clearExternalPlaybackURL() } }
        )) {
            if let url = model.externalPlaybackURL {
                RivuneExternalApplicationPicker(
                    url: url,
                    cancel: model.clearExternalPlaybackURL,
                    opened: model.clearExternalPlaybackURL
                )
            }
        }
#endif
    }

#if os(macOS)
    private var macOSDetailAndSources: some View {
        GeometryReader { proxy in
            let compact = proxy.size.width < 900
            let margin: CGFloat = compact ? 12 : 24
            let regularWidth = min(max(proxy.size.width * 0.365, 360), 440)
            let panelWidth = compact ? min(360, max(proxy.size.width - margin * 2, 0)) : regularWidth
            let panelHeight = min(max(proxy.size.height - margin * 2, 0), 720)

            ZStack(alignment: .trailing) {
                presentationContent
                    .frame(width: proxy.size.width, height: proxy.size.height)
                    .clipped()

                RivunePlaybackSourcesView(model: model, panelMode: true)
                    .frame(width: panelWidth, height: panelHeight)
                    .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                    .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
                    .overlay {
                        RoundedRectangle(cornerRadius: 20, style: .continuous)
                            .stroke(Color.white.opacity(0.14), lineWidth: 1)
                    }
                    .shadow(color: .black.opacity(0.45), radius: 24, x: -8, y: 8)
                    .padding(.trailing, margin)
            }
            .frame(width: proxy.size.width, height: proxy.size.height)
            .background(Color.black)
        }
    }
#endif

    private var presentationContent: some View {
        RivunePlatformNavigation {
            detailStateContent
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button(action: closePresentation) {
#if os(macOS)
                            Text(rivuneLocalized("Back"))
                                .foregroundStyle(.primary)
#else
                            Text(rivuneLocalized(model.selectedSeason != nil || model.canNavigateBackFromMedia ? "Back" : "Close"))
                                .foregroundStyle(.primary)
#endif
                        }
                        .rivuneGlassButton()
                    }
                }
        }
    }

    private var detailStateContent: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let season = model.selectedSeason {
                seasonContent(season)
            } else if let detail = model.mediaDetail {
                detailContent(detail)
            } else if model.mediaLoading {
                ProgressView("Loading details…")
            } else {
                failureContent
            }
        }
    }

    private func closePresentation() {
        if model.selectedSeason != nil {
            model.closeSeason()
        } else if model.canNavigateBackFromMedia {
            model.closeMedia()
        } else {
            model.closeMedia()
#if !os(macOS)
            dismiss()
#endif
        }
    }

    private func detailContent(_ detail: RivuneMediaDetail) -> some View {
        compactDetailContent(detail)
    }

    private func compactDetailContent(_ detail: RivuneMediaDetail) -> some View {
        ZStack {
            cinematicBackdrop(url: detailBackdropURL(detail))
            ScrollView {
                VStack(alignment: .leading, spacing: 22) {
                    detailHeader(detail)
                    actionRow(detail)
                    if model.playbackCoordinationAvailable { coordinationControls }
                    if let overview = overview(detail)?.nilIfEmpty {
                        Text(overview)
                            .font(.body)
                            .foregroundStyle(Color.white.opacity(0.88))
                            .fixedSize(horizontal: false, vertical: true)
                            .frame(maxWidth: 780, alignment: .leading)
                    }
                    if let series = detail.series { seasons(series) }
                    cast(detail.movie?.cast ?? detail.series?.cast ?? detail.parentSeries?.cast ?? [])
                    if let failure = model.mediaFailure {
                        Label(rivuneLocalized(failure.localizedDescription), systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }
#if os(macOS)
                .padding(.horizontal, 48)
                .padding(.top, 112)
                .padding(.bottom, 48)
                .frame(maxWidth: 1080, alignment: .leading)
                .frame(maxWidth: .infinity, alignment: .leading)
#else
                .padding(.horizontal, 20)
                .padding(.vertical, 56)
                .frame(maxWidth: 920, alignment: .leading)
                .frame(maxWidth: .infinity, alignment: .leading)
#endif
            }
        }
    }

    @ViewBuilder private var coordinationControls: some View {
        VStack(alignment: .leading, spacing: 10) {
            if !model.playbackDevices.isEmpty {
#if os(tvOS)
                ForEach(model.playbackDevices) { device in
                    HStack {
                        Button("Play on \(device.name)") { model.handoffPlayback(to: device) }
                        Button("Play") { model.controlPlayback(on: device, command: "play") }
                        Button("Pause") { model.controlPlayback(on: device, command: "pause") }
                        Button("Match position") { model.controlPlayback(on: device, command: "seek") }
                        Button("Stop", role: .destructive) { model.controlPlayback(on: device, command: "stop") }
                    }
                }
#else
                Menu {
                    ForEach(model.playbackDevices) { device in
                        Button("Play on \(device.name)") { model.handoffPlayback(to: device) }
                        Button("Play \(device.name)") { model.controlPlayback(on: device, command: "play") }
                        Button("Pause \(device.name)") { model.controlPlayback(on: device, command: "pause") }
                        Button("Match position on \(device.name)") { model.controlPlayback(on: device, command: "seek") }
                        Button("Stop \(device.name)", role: .destructive) { model.controlPlayback(on: device, command: "stop") }
                    }
                } label: { Label(rivuneLocalized("Play on another device"), systemImage: "airplayvideo") }
                .rivuneGlassButton()
#endif
            }
            HStack {
                if let room = model.activePlaybackRoom {
#if os(tvOS)
                    Text(room.joinCode.map { rivuneLocalizedFormat("Room %@", $0) } ?? rivuneLocalized("Watch room"))
                        .font(.subheadline.monospaced())
#else
                    Text(room.joinCode.map { rivuneLocalizedFormat("Room %@", $0) } ?? rivuneLocalized("Watch room"))
                        .font(.subheadline.monospaced()).textSelection(.enabled)
#endif
                    Text(rivuneLocalizedFormat("%d watching", room.members.count)).foregroundStyle(.secondary)
                    Button("Leave", action: model.leavePlaybackRoom).rivuneGlassButton()
                } else {
                    Button { model.createPlaybackRoom() } label: { Label("Start watch room", systemImage: "person.2.wave.2") }.rivuneGlassButton()
                    Button("Join room") { showJoinRoom = true }.rivuneGlassButton()
                }
            }
        }
        .alert("Join watch room", isPresented: $showJoinRoom) {
            TextField("Room code", text: $roomCode)
            Button("Join") { model.joinPlaybackRoom(code: roomCode); roomCode = "" }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func detailHeader(_ detail: RivuneMediaDetail) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(displayTitle(detail))
#if os(macOS)
                .font(.system(size: 42, weight: .bold))
#else
                .font(.largeTitle.bold())
#endif
                .lineLimit(3)
            if let tagline = tagline(detail)?.nilIfEmpty {
                Text(tagline)
                    .font(.title3)
                    .foregroundStyle(Color.white.opacity(0.78))
                    .lineLimit(2)
            }
            if let subtitle = releaseLine(detail) {
                Text(subtitle)
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(Color.white.opacity(0.80))
                    .lineLimit(3)
            }
            let genreNames = detail.movie?.genres.map(\.name) ?? detail.series?.genres.map(\.name) ?? []
            if !genreNames.isEmpty {
                Text(genreNames.joined(separator: " · "))
                    .foregroundStyle(Color.white.opacity(0.76))
                    .lineLimit(2)
            }
        }
        .frame(maxWidth: 820, alignment: .leading)
    }

    private func detailBackdropURL(_ detail: RivuneMediaDetail) -> URL? {
        let value = detail.movie?.backdropUrl
            ?? detail.series?.backdropUrl
            ?? detail.episode?.backdropUrl
            ?? detail.episode?.stillUrl
            ?? detail.parentSeries?.backdropUrl
            ?? detail.target.backgroundUrl
            ?? detail.target.posterUrl
        return value.flatMap(model.resolvedResourceURL)
    }

    private func seasonBackdropURL(_ season: Season) -> URL? {
        let value = season.backdropUrl
            ?? model.mediaDetail?.series?.backdropUrl
            ?? model.mediaDetail?.parentSeries?.backdropUrl
            ?? model.mediaDetail?.target.backgroundUrl
            ?? season.posterUrl
        return value.flatMap(model.resolvedResourceURL)
    }

    private func cinematicBackdrop(url: URL?) -> some View {
        ZStack {
            Color.black
            AsyncImage(url: url) { phase in
                if let image = phase.image {
                    image
                        .resizable()
                        .scaledToFill()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .clipped()
                } else {
                    Color.black
                }
            }
#if os(macOS)
            LinearGradient(
                stops: [
                    .init(color: .black.opacity(0.68), location: 0),
                    .init(color: .black.opacity(0.58), location: 0.72),
                    .init(color: .black.opacity(0.30), location: 1),
                ],
                startPoint: .leading,
                endPoint: .trailing
            )
            LinearGradient(
                colors: [.clear, .black.opacity(0.42)],
                startPoint: .center,
                endPoint: .bottom
            )
#else
            Color.black.opacity(0.58)
            LinearGradient(
                colors: [.black.opacity(0.90), .black.opacity(0.52), .clear],
                startPoint: .leading,
                endPoint: .trailing
            )
            LinearGradient(
                colors: [.clear, .black.opacity(0.78)],
                startPoint: .center,
                endPoint: .bottom
            )
#endif
        }
        .ignoresSafeArea()
    }

    private func actionRow(_ detail: RivuneMediaDetail) -> some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 150, maximum: 240), spacing: 10)], alignment: .leading, spacing: 10) {
            if detail.target.mediaType != "series", !model.automaticallyShowStreams {
                Button(action: model.loadPlaybackSources) {
                    Label(rivuneLocalized(detail.progress?.positionSeconds ?? 0 > 0 ? "Resume" : "Play"), systemImage: "play.fill")
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.rivuneGlassButton(prominent: true)
            }
            if detail.target.mediaType != "episode" {
                Button(action: model.toggleLibrary) {
                    Label(rivuneLocalized(detail.inLibrary ? "In library" : "Add to library"), systemImage: detail.inLibrary ? "checkmark" : "plus")
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.rivuneGlassButton()
            }
            Button(action: model.toggleWatched) {
                let watched = detail.target.mediaType == "series"
                    ? model.seriesEpisodesWatched == true
                    : detail.progress?.completed == true
                Label(rivuneLocalized(watched ? "Mark as unwatched" : "Mark as watched"), systemImage: watched ? "eye.slash" : "checkmark.circle")
                    .foregroundStyle(.primary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.75)
                    .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
            }.rivuneGlassButton()
            if let trailer = detail.trailers.first, let url = trailerURL(trailer) {
                Button { openURL(url) } label: {
                    Label("Trailer", systemImage: "play.rectangle")
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.rivuneGlassButton()
            }
            if model.mediaActionLoading || model.mediaLoading { ProgressView() }
        }
        .disabled(model.mediaActionLoading)
    }


    @ViewBuilder private func seasons(_ series: Series) -> some View {
        if !series.seasons.isEmpty {
            VStack(alignment: .leading, spacing: 14) {
                Text("Seasons").font(.title2.bold())
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(spacing: 14) {
                        ForEach(series.seasons) { season in
                            Button {
#if os(macOS)
                                guard !seasonRailDrag.suppressesClicks else { return }
#endif
                                model.openSeason(season)
                            } label: {
                                VStack(alignment: .leading, spacing: 8) {
                                    AsyncImage(url: season.posterUrl.flatMap(model.resolvedResourceURL)) { phase in
                                        if let image = phase.image { image.resizable().scaledToFill() }
                                        else { Color.white.opacity(0.08) }
                                    }
                                    .frame(width: 126, height: 189)
                                    .clipShape(RoundedRectangle(cornerRadius: 13, style: .continuous))
                                    Text(season.name).font(.headline).lineLimit(1).frame(width: 126, alignment: .leading)
                                    Text(rivuneLocalizedFormat("%d episodes", season.episodeCount)).font(.caption).foregroundStyle(Color.white.opacity(0.64))
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    }
#if os(macOS)
                    .rivuneHorizontalRailContent(seasonRailDrag)
#endif
                }
#if os(macOS)
                .rivuneHorizontalRailDrag(seasonRailDrag)
#endif
            }
        }
    }

    @ViewBuilder private func cast(_ members: [CastMember]) -> some View {
        if !members.isEmpty {
            VStack(alignment: .leading, spacing: 14) {
                Text("Cast").font(.title2.bold())
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(spacing: 16) {
                        ForEach(members) { member in
                            VStack(spacing: 8) {
                                AsyncImage(url: member.profileUrl.flatMap(model.resolvedResourceURL)) { phase in
                                    if let image = phase.image { image.resizable().scaledToFill() }
                                    else { Image(systemName: "person.fill").frame(maxWidth: .infinity, maxHeight: .infinity).background(Color.white.opacity(0.08)) }
                                }
                                .frame(width: 84, height: 84).clipShape(Circle())
                                Text(member.name).font(.caption).lineLimit(1)
                                if let character = member.character { Text(character).font(.caption2).foregroundStyle(Color.white.opacity(0.62)).lineLimit(1) }
                            }.frame(width: 108)
                        }
                    }
                }
            }
        }
    }


    private func seasonContent(_ season: Season) -> some View {
        ZStack {
            cinematicBackdrop(url: seasonBackdropURL(season))
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    VStack(alignment: .leading, spacing: 8) {
                        if let seriesName = model.mediaDetail?.series?.name ?? model.mediaDetail?.parentSeries?.name {
                            Text(seriesName)
                                .font(.headline)
                                .foregroundStyle(Color.white.opacity(0.72))
                        }
                        Text(season.name)
#if os(macOS)
                            .font(.system(size: 42, weight: .bold))
#else
                            .font(.largeTitle.bold())
#endif
                        let metadata = [
                            season.airDate,
                            rivuneLocalizedFormat("%d episodes", season.episodes.count),
                            season.voteAverage > 0 ? String(format: "★ %.1f", season.voteAverage) : nil,
                        ].compactMap { $0 }
                        if !metadata.isEmpty {
                            Text(metadata.joined(separator: " · "))
                                .foregroundStyle(Color.white.opacity(0.76))
                        }
                    }
                    LazyVGrid(
                        columns: [GridItem(.adaptive(minimum: 140, maximum: 240), spacing: 10)],
                        alignment: .leading,
                        spacing: 10
                    ) {
                        if let detail = model.mediaDetail, detail.target.mediaType != "episode" {
                            Button(action: model.toggleLibrary) {
                                Label(rivuneLocalized(detail.inLibrary ? "In library" : "Add to library"), systemImage: detail.inLibrary ? "checkmark" : "plus")
                                    .foregroundStyle(.primary)
                                    .lineLimit(1)
                                    .minimumScaleFactor(0.8)
                                    .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                            }
                            .rivuneGlassButton()
                        }
                        Button(action: model.toggleWatched) {
                            let watched = !season.episodes.isEmpty && season.episodes.allSatisfy { model.episodeProgress[$0.id]?.completed == true }
                            Label(rivuneLocalized(watched ? "Mark as unwatched" : "Mark as watched"), systemImage: watched ? "eye.slash" : "checkmark.circle")
                                .foregroundStyle(.primary)
                                .lineLimit(1)
                                .minimumScaleFactor(0.8)
                                .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                        }
                        .rivuneGlassButton(prominent: true)
                        if let trailer = model.seasonTrailers.first, let url = trailerURL(trailer) {
                            Button { openURL(url) } label: {
                                Label("Trailer", systemImage: "play.rectangle")
                                    .foregroundStyle(.primary)
                                    .lineLimit(1)
                                    .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                            }
                            .rivuneGlassButton()
                        }
                        if model.mediaActionLoading { ProgressView() }
                    }
                    .disabled(model.mediaActionLoading)
                    if !season.overview.isEmpty {
                        Text(season.overview)
                            .foregroundStyle(Color.white.opacity(0.86))
                            .frame(maxWidth: 780, alignment: .leading)
                    }
                    Text("Episodes").font(.title2.bold())
                        ScrollView(.horizontal, showsIndicators: false) {
                            LazyHStack(alignment: .top, spacing: 16) {
                                ForEach(season.episodes) { episode in
                                    Button {
#if os(macOS)
                                        guard !episodeRailDrag.suppressesClicks else { return }
#endif
                                        model.openEpisode(episode)
                                    } label: {
                                        VStack(alignment: .leading, spacing: 0) {
                                            ZStack(alignment: .bottom) {
                                                AsyncImage(url: (episode.stillUrl ?? episode.backdropUrl).flatMap(model.resolvedResourceURL)) { phase in
                                                    if let image = phase.image { image.resizable().scaledToFill() }
                                                    else { Color.white.opacity(0.08) }
                                                }
#if os(macOS)
                                                .frame(width: 380, height: 213.75)
#else
                                                .frame(width: 300, height: 168.75)
#endif
                                                .clipped()
                                                if let progress = model.episodeProgress[episode.id], progress.durationSeconds > 0, !progress.completed {
                                                    ProgressView(value: Double(progress.positionSeconds), total: Double(progress.durationSeconds))
                                                        .tint(.accentColor)
                                                        .frame(maxWidth: .infinity)
                                                }
                                            }
                                            VStack(alignment: .leading, spacing: 8) {
                                                HStack(alignment: .firstTextBaseline, spacing: 8) {
                                                    Text(rivuneLocalizedFormat("%d. %@", episode.episodeNumber, episode.name))
                                                        .font(.headline)
                                                        .lineLimit(2)
                                                    Spacer(minLength: 4)
                                                    if model.episodeProgress[episode.id]?.completed == true {
                                                        Image(systemName: "checkmark.circle.fill")
                                                            .foregroundStyle(.secondary)
                                                    }
                                                }
                                                let metadata = [
                                                    episode.runtimeMinutes.map { rivuneLocalizedFormat("%d min", $0) },
                                                    episode.airDate,
                                                    episode.voteAverage > 0 ? String(format: "★ %.1f", episode.voteAverage) : nil,
                                                ].compactMap { $0 }
                                                if !metadata.isEmpty {
                                                    Text(metadata.joined(separator: " · "))
                                                        .font(.caption)
                                                        .foregroundStyle(Color.white.opacity(0.64))
                                                        .lineLimit(1)
                                                }
                                                if !episode.overview.isEmpty {
                                                    Text(episode.overview)
                                                        .font(.caption)
                                                        .foregroundStyle(Color.white.opacity(0.72))
                                                        .lineLimit(3)
                                                }
                                            }
                                            .padding(14)
                                        }
#if os(macOS)
                                        .frame(width: 380, alignment: .leading)
#else
                                        .frame(width: 300, alignment: .leading)
#endif
                                        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                                        .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
#if os(macOS)
                            .rivuneHorizontalRailContent(episodeRailDrag)
#endif
                        }
#if os(macOS)
                        .rivuneHorizontalRailDrag(episodeRailDrag)
#endif
                    if let failure = model.mediaFailure {
                        Text(rivuneLocalized(failure.localizedDescription)).foregroundStyle(.red)
                    }
                }
#if os(macOS)
                .padding(.horizontal, 48)
                .padding(.top, 112)
                .padding(.bottom, 48)
                .frame(maxWidth: .infinity, alignment: .leading)
#else
                .padding(20)
                .frame(maxWidth: 1000, alignment: .leading)
                .frame(maxWidth: .infinity)
#endif
            }
        }
    }

    private var failureContent: some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill").font(.largeTitle).foregroundStyle(.red)
            Text(model.mediaFailure.map { rivuneLocalized($0.localizedDescription) } ?? rivuneLocalized("This title could not be loaded.")).multilineTextAlignment(.center)
        }.padding(30)
    }

    private func displayTitle(_ detail: RivuneMediaDetail) -> String { detail.movie?.title ?? detail.series?.name ?? detail.episode?.name ?? detail.target.title }
    private func overview(_ detail: RivuneMediaDetail) -> String? { detail.movie?.overview ?? detail.series?.overview ?? detail.episode?.overview ?? detail.target.overview }
    private func tagline(_ detail: RivuneMediaDetail) -> String? { detail.movie?.tagline ?? detail.series?.tagline }
    private func releaseLine(_ detail: RivuneMediaDetail) -> String? {
        let date = detail.movie?.releaseDate ?? detail.series?.firstAirDate ?? detail.episode?.airDate ?? detail.target.releaseInfo
        let runtime = detail.movie?.runtimeMinutes ?? detail.episode?.runtimeMinutes ?? detail.target.runtimeMinutes
        let rating = detail.movie?.voteAverage ?? detail.series?.voteAverage ?? detail.episode?.voteAverage
        let type = rivuneLocalized(detail.target.mediaType == "series" ? "Series" : detail.target.mediaType.capitalized)
        let counts = detail.series.flatMap { series in
            [series.numberOfSeasons.map { rivuneLocalizedFormat("%d seasons", $0) }, series.numberOfEpisodes.map { rivuneLocalizedFormat("%d episodes", $0) }].compactMap { $0 }.joined(separator: " · ").nilIfEmpty
        }
        return [type, date, runtime.map { rivuneLocalizedFormat("%d min", $0) }, rating.flatMap { $0 > 0 ? String(format: "★ %.1f", $0) : nil }, counts, detail.series?.status].compactMap { $0 }.joined(separator: " · ").nilIfEmpty
    }
    private func trailerURL(_ trailer: Trailer) -> URL? {
        var components = URLComponents(string: "https://www.youtube.com/watch")
        components?.queryItems = [URLQueryItem(name: "v", value: trailer.youtubeId)]
        return components?.url
    }
}

#if os(macOS)
final class RivuneHorizontalRailDragController: ObservableObject {
    private enum Axis: Equatable {
        case horizontal
        case vertical
    }

    private(set) var suppressesClicks = false

    private weak var scrollView: NSScrollView?
    private var axis: Axis?
    private var dragStartX: CGFloat?
    private var clearClickSuppression: DispatchWorkItem?

    func attach(to scrollView: NSScrollView) {
        self.scrollView = scrollView
    }

    func dragChanged(_ value: DragGesture.Value) {
        registerDrag(translation: value.translation)

        guard axis == .horizontal, let scrollView else { return }
        if dragStartX == nil {
            dragStartX = scrollView.contentView.bounds.origin.x
        }
        guard let dragStartX else { return }
        scroll(to: dragStartX - value.translation.width, in: scrollView)
    }

    func registerDrag(translation: CGSize) {
        guard axis == nil else { return }
        axis = abs(translation.width) > abs(translation.height) ? .horizontal : .vertical
        suppressesClicks = true
        clearClickSuppression?.cancel()
    }

    func dragEnded(_ value: DragGesture.Value) {
        endDrag(predictedEndTranslation: value.predictedEndTranslation)
    }

    func endDrag(predictedEndTranslation: CGSize) {
        if axis == .horizontal, let scrollView, let dragStartX {
            let targetX = constrainedX(
                dragStartX - predictedEndTranslation.width,
                in: scrollView
            )
            let targetOrigin = CGPoint(x: targetX, y: scrollView.contentView.bounds.origin.y)
            NSAnimationContext.runAnimationGroup({ context in
                context.duration = 0.24
                context.timingFunction = CAMediaTimingFunction(name: .easeOut)
                scrollView.contentView.animator().setBoundsOrigin(targetOrigin)
            }, completionHandler: { [weak scrollView] in
                guard let scrollView else { return }
                scrollView.reflectScrolledClipView(scrollView.contentView)
            })
        }

        axis = nil
        dragStartX = nil
        let workItem = DispatchWorkItem { [weak self] in
            self?.suppressesClicks = false
        }
        clearClickSuppression = workItem
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.12, execute: workItem)
    }

    private func scroll(to proposedX: CGFloat, in scrollView: NSScrollView) {
        let clipView = scrollView.contentView
        clipView.scroll(to: CGPoint(x: constrainedX(proposedX, in: scrollView), y: clipView.bounds.origin.y))
        scrollView.reflectScrolledClipView(clipView)
    }

    private func constrainedX(_ proposedX: CGFloat, in scrollView: NSScrollView) -> CGFloat {
        let clipView = scrollView.contentView
        var proposedBounds = clipView.bounds
        proposedBounds.origin.x = proposedX
        return clipView.constrainBoundsRect(proposedBounds).origin.x
    }
}

private struct RivuneHorizontalScrollViewResolver: NSViewRepresentable {
    let controller: RivuneHorizontalRailDragController

    func makeNSView(context: Context) -> RivuneHorizontalScrollViewResolverView {
        let view = RivuneHorizontalScrollViewResolverView()
        view.controller = controller
        DispatchQueue.main.async { [weak view] in view?.resolveScrollView() }
        return view
    }

    func updateNSView(_ nsView: RivuneHorizontalScrollViewResolverView, context: Context) {
        nsView.controller = controller
        DispatchQueue.main.async { [weak nsView] in nsView?.resolveScrollView() }
    }
}

private final class RivuneHorizontalScrollViewResolverView: NSView {
    weak var controller: RivuneHorizontalRailDragController?

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        resolveScrollView()
    }

    func resolveScrollView() {
        guard let scrollView = enclosingScrollView else { return }
        controller?.attach(to: scrollView)
    }
}

private struct RivuneHorizontalRailDragModifier: ViewModifier {
    let controller: RivuneHorizontalRailDragController

    func body(content: Content) -> some View {
        content.simultaneousGesture(
            DragGesture(minimumDistance: 8)
                .onChanged { controller.dragChanged($0) }
                .onEnded { controller.dragEnded($0) }
        )
    }
}

private extension View {
    func rivuneHorizontalRailContent(_ controller: RivuneHorizontalRailDragController) -> some View {
        background(RivuneHorizontalScrollViewResolver(controller: controller).allowsHitTesting(false))
    }

    func rivuneHorizontalRailDrag(_ controller: RivuneHorizontalRailDragController) -> some View {
        modifier(RivuneHorizontalRailDragModifier(controller: controller))
    }
}
#endif

struct RivunePlaybackSourcesView: View {
    @ObservedObject var model: RivuneAppModel
    let panelMode: Bool
    @State private var selectedAddonID: UUID? = nil

    init(model: RivuneAppModel, panelMode: Bool = false) {
        self.model = model
        self.panelMode = panelMode
    }

    private var filteredSources: [PlaybackSourceOption] {
        guard let selectedAddonID else { return model.playbackSources }
        return model.playbackSources.filter { $0.addonId == selectedAddonID }
    }

    private var addonFilters: [RivunePlaybackSourceFilter] {
        var seen = Set<UUID>()
        return model.playbackSources.compactMap { source in
            guard seen.insert(source.addonId).inserted else { return nil }
            return RivunePlaybackSourceFilter(
                id: source.addonId,
                label: source.addonName?.nilIfEmpty ?? source.manifestId
            )
        }
    }

    var body: some View {
        ZStack {
            if !panelMode { Color.black.ignoresSafeArea() }
            VStack(alignment: .leading, spacing: panelMode ? 14 : 20) {
                header
                if !model.playbackSources.isEmpty { filters }
                ScrollView {
                    sourceList
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding(panelMode ? 18 : 28)
            .frame(maxWidth: panelMode ? .infinity : 920, alignment: .leading)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .background(panelMode ? Color.black.opacity(0.46) : Color.clear)
        }
        .preferredColorScheme(.dark)
        .onChange(of: model.playbackSources) { sources in
            if let selectedAddonID, !sources.contains(where: { $0.addonId == selectedAddonID }) {
                self.selectedAddonID = nil
            }
        }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            Button(action: model.closePlaybackSources) {
                Image(systemName: "chevron.left")
                    .foregroundStyle(.primary)
                    .font(.headline.weight(.semibold))
                    .frame(width: 34, height: 34)
            }
            .rivuneCircularButton()
            .rivuneGlassButton()
            VStack(alignment: .leading, spacing: panelMode ? 2 : 6) {
                if !panelMode {
                    Text("CHOOSE A STREAM")
                        .font(.caption.weight(.bold))
                        .tracking(1.6)
                        .foregroundStyle(Color.white.opacity(0.58))
                }
                Text("Playback sources")
                    .font(panelMode ? .title2.bold() : .largeTitle.bold())
                if !panelMode {
                    Text("Choose the quality and playback route returned by your server.")
                        .foregroundStyle(Color.white.opacity(0.68))
                }
            }
            Spacer(minLength: 0)
        }
    }

    private var filters: some View {
        HStack(spacing: 8) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    filterButton(label: rivuneLocalized("All"), addonID: nil)
                    ForEach(addonFilters) { filter in
                        filterButton(label: filter.label, addonID: filter.id)
                    }
                }
            }
            Button(action: model.loadPlaybackSources) {
                Image(systemName: "arrow.clockwise")
                    .frame(width: 28, height: 28)
            }
            .rivuneCircularButton()
            .rivuneGlassButton()
            .disabled(model.mediaLoading)
            .accessibilityLabel(rivuneLocalized("Refresh sources"))
        }
    }

    private func filterButton(label: String, addonID: UUID?) -> some View {
        Button {
            selectedAddonID = addonID
        } label: {
            HStack(spacing: 6) {
                if selectedAddonID == addonID { Image(systemName: "checkmark") }
                Text(label).lineLimit(1)
            }
        }
        .rivuneGlassButton()
        .accessibilityAddTraits(selectedAddonID == addonID ? .isSelected : [])
    }

    @ViewBuilder private var sourceList: some View {
        if model.mediaLoading && model.playbackSources.isEmpty {
            HStack(spacing: 12) {
                ProgressView()
                Text("Finding streams…")
            }
            .frame(maxWidth: .infinity, minHeight: 180)
            .foregroundStyle(Color.white.opacity(0.72))
        } else if model.playbackSources.isEmpty {
            VStack(spacing: 12) {
                Image(systemName: "play.slash").font(.largeTitle)
                Text("No compatible stream").font(.title2.bold())
                Text("Your server did not return a source that this device can play.")
                    .foregroundStyle(Color.white.opacity(0.68))
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity, minHeight: 240)
        } else if filteredSources.isEmpty {
            Text("No sources are available from this add-on.")
                .foregroundStyle(Color.white.opacity(0.68))
                .frame(maxWidth: .infinity, minHeight: 160)
        } else {
            LazyVStack(spacing: 12) {
                ForEach(filteredSources) { source in sourceCard(source) }
            }
        }
    }

    private func sourceCard(_ source: PlaybackSourceOption) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                Text(source.name)
                    .font(panelMode ? .headline : .title3.weight(.semibold))
                    .foregroundStyle(.white)
                Spacer(minLength: 12)
                Text(source.protocol.uppercased())
                    .font(.caption2.weight(.bold))
                    .padding(.horizontal, 9)
                    .padding(.vertical, 5)
                    .background(Color.white.opacity(0.10), in: Capsule())
            }
            if let description = source.description {
                Text(description)
                    .font(.callout)
                    .foregroundStyle(Color.white.opacity(0.72))
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text([
                source.addonName?.nilIfEmpty ?? source.manifestId,
                source.container?.uppercased(),
                source.mode?.rawValue.replacingOccurrences(of: "_", with: " ").capitalized,
            ].compactMap { $0 }.joined(separator: " · "))
                .font(.caption)
                .foregroundStyle(Color.white.opacity(0.54))

            LazyVGrid(
                columns: [GridItem(.adaptive(minimum: panelMode ? 124 : 150), spacing: 8)],
                alignment: .leading,
                spacing: 8
            ) {
                if model.preferredPlayer != .external {
                    Button { model.play(source, externally: false) } label: {
                        Label("Play in Rivune", systemImage: "play.fill")
                            .foregroundStyle(.white)
                            .frame(maxWidth: .infinity)
                    }
                    .rivuneGlassButton(prominent: true)
                }
#if !os(tvOS)
                if model.preferredPlayer != .rivune {
                    Button { model.play(source, externally: true) } label: {
                        Label("Open in app", systemImage: "play.rectangle.on.rectangle")
                            .foregroundStyle(.white)
                            .frame(maxWidth: .infinity)
                    }
                    .rivuneGlassButton()
                }
#endif
                if !["hls", "dash"].contains(source.protocol.lowercased()) {
                    Button { model.download(source) } label: {
                        Label(
                            rivuneLocalized(model.isDownloading(source) ? "Downloading…" : "Download"),
                            systemImage: "arrow.down.circle"
                        )
                        .foregroundStyle(.white)
                        .frame(maxWidth: .infinity)
                    }
                    .disabled(model.offlineDownloadActive)
                    .rivuneGlassButton()
                }
            }
        }
        .padding(panelMode ? 14 : 18)
        .background(Color.white.opacity(0.055), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .stroke(Color.white.opacity(0.10), lineWidth: 1)
        }
    }
}

private struct RivunePlaybackSourceFilter: Identifiable {
    let id: UUID
    let label: String
}

struct RivuneInternalPlayerView: View {
    let presentation: RivunePlaybackPresentation
    @ObservedObject var model: RivuneAppModel

    @ViewBuilder var body: some View {
        let active = model.playbackPresentation.flatMap { $0.id == presentation.id ? $0 : nil } ?? presentation
        if active.engine == .mpv {
            RivuneMPVInternalPlayerView(presentation: active, model: model)
        } else {
            RivuneNativeInternalPlayerView(presentation: active, model: model)
        }
    }
}

private struct RivunePlayerChrome<Options: View>: View {
    let title: String
    let badge: String?
    let playing: Bool
    @Binding private var position: Double
    let duration: Double
    let close: () -> Void
    let minimize: () -> Void
    let seekBackward: () -> Void
    let togglePlayback: () -> Void
    let seekForward: () -> Void
    let nextEpisode: (() -> Void)?
    let scrubbingChanged: (Bool) -> Void
    let options: Options

    init(
        title: String,
        badge: String? = nil,
        playing: Bool,
        position: Binding<Double>,
        duration: Double,
        close: @escaping () -> Void,
        minimize: @escaping () -> Void,
        seekBackward: @escaping () -> Void,
        togglePlayback: @escaping () -> Void,
        seekForward: @escaping () -> Void,
        nextEpisode: (() -> Void)? = nil,
        scrubbingChanged: @escaping (Bool) -> Void,
        @ViewBuilder options: () -> Options
    ) {
        self.title = title
        self.badge = badge
        self.playing = playing
        _position = position
        self.duration = duration
        self.close = close
        self.minimize = minimize
        self.seekBackward = seekBackward
        self.togglePlayback = togglePlayback
        self.seekForward = seekForward
        self.nextEpisode = nextEpisode
        self.scrubbingChanged = scrubbingChanged
        self.options = options()
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                playerButton(systemImage: "xmark", label: "Close player", action: close)
                Text(title)
                    .font(.headline)
                    .lineLimit(1)
                if let badge {
                    Text(badge)
                        .font(.caption2.weight(.bold))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(.thinMaterial, in: Capsule())
                }
                Spacer(minLength: 12)
                playerButton(systemImage: "pip.enter", label: "Mini player", action: minimize)
            }
            .padding(.horizontal, 22)
            .padding(.top, 18)

            Spacer()

            HStack(spacing: 20) {
                transportButton(systemImage: "gobackward.10", label: "Back 10 seconds", action: seekBackward)
                Button(action: togglePlayback) {
                    Image(systemName: playing ? "pause.fill" : "play.fill")
                        .font(.system(size: 29, weight: .bold))
                        .frame(width: 66, height: 66)
                }
                .rivuneCircularButton()
                .rivuneGlassButton()
                .accessibilityLabel(rivuneLocalized(playing ? "Pause" : "Play"))
                transportButton(systemImage: "goforward.10", label: "Forward 10 seconds", action: seekForward)
                if let nextEpisode {
                    transportButton(systemImage: "forward.end.fill", label: "Next episode", action: nextEpisode)
                }
            }

            Spacer()

            VStack(spacing: 10) {
#if os(tvOS)
                GeometryReader { proxy in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.white.opacity(0.24))
                        Capsule()
                            .fill(Color.white.opacity(0.88))
                            .frame(width: proxy.size.width * min(max(position / max(duration, 1), 0), 1))
                    }
                }
                .frame(height: 8)
#else
                Slider(value: $position, in: 0...max(duration, 1), onEditingChanged: scrubbingChanged)
                    .tint(.white)
#endif
                HStack {
                    Text(formatTime(position))
                    Spacer()
                    Text("−\(formatTime(max(duration - position, 0)))")
                }
                .font(.caption.monospacedDigit())
                .foregroundStyle(Color.white.opacity(0.82))

                HStack(spacing: 10) {
                    options
                }
                .font(.subheadline.weight(.semibold))
                .rivunePlayerOptionsContainer()
                .frame(maxWidth: .infinity, alignment: .trailing)
            }
            .padding(.horizontal, 24)
            .padding(.bottom, 20)
        }
    }

    private func playerButton(systemImage: String, label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.headline)
                .frame(width: 34, height: 34)
        }
        .rivuneCircularButton()
        .rivuneGlassButton()
        .accessibilityLabel(rivuneLocalized(label))
    }

    private func transportButton(systemImage: String, label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.system(size: 24, weight: .semibold))
                .frame(width: 50, height: 50)
        }
        .rivuneCircularButton()
        .rivuneGlassButton()
        .accessibilityLabel(rivuneLocalized(label))
    }

    private func formatTime(_ value: Double) -> String {
        let seconds = max(Int(value.isFinite ? value : 0), 0)
        let hours = seconds / 3600
        return hours > 0
            ? String(format: "%d:%02d:%02d", hours, seconds / 60 % 60, seconds % 60)
            : String(format: "%02d:%02d", seconds / 60, seconds % 60)
    }
}

private struct RivunePlayerOptionControlModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
#if os(macOS)
        content
            .buttonStyle(.plain)
            .frame(width: 36, height: 30)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .stroke(Color.white.opacity(0.14), lineWidth: 1)
            }
#else
        content
#endif
    }
}

private struct RivuneMiniPlayerControlModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
#if os(macOS)
        content.rivuneGlassButton()
#else
        content.buttonStyle(.plain)
#endif
    }
}

private struct RivunePlayerActionButtonModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
#if os(macOS)
        content.rivuneGlassButton()
#else
        content.rivuneGlassButton(prominent: true)
#endif
    }
}

private struct RivunePlayerOptionsContainerModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
#if os(macOS)
        content
#else
        content.rivuneGlassButton()
#endif
    }
}

private extension View {
    func rivunePlayerOptionControl() -> some View {
        modifier(RivunePlayerOptionControlModifier())
    }

    func rivunePlayerOptionsContainer() -> some View {
        modifier(RivunePlayerOptionsContainerModifier())
    }

    func rivuneMiniPlayerControl() -> some View {
        modifier(RivuneMiniPlayerControlModifier())
    }

    func rivunePlayerActionButton() -> some View {
        modifier(RivunePlayerActionButtonModifier())
    }
}

private struct RivuneNativeInternalPlayerView: View {
    let presentation: RivunePlaybackPresentation
    @ObservedObject var model: RivuneAppModel
    @State private var player = AVPlayer()
    @State private var finished = false
    @State private var activeMarker: PlaybackMarker?
    @State private var consumedMarkers = Set<String>()
    @State private var controlsVisible = true
    @State private var playing = false
    @State private var position = 0.0
    @State private var duration = 0.0
    @State private var scrubbing = false
    @State private var controlsTask: Task<Void, Never>?
    @State private var sessionAspect: RivuneVideoAspect
    @State private var playbackSpeed = 1.0
    @State private var audioGroup: AVMediaSelectionGroup?
    @State private var subtitleGroup: AVMediaSelectionGroup?
    @State private var audioOptions: [AVMediaSelectionOption] = []
    @State private var subtitleOptions: [AVMediaSelectionOption] = []
    @State private var failureMessage: String?
    @State private var handoffToMPV = false
    @State private var loadAttempt = 0

    init(presentation: RivunePlaybackPresentation, model: RivuneAppModel) {
        self.presentation = presentation
        self.model = model
        _sessionAspect = State(initialValue: model.videoAspect)
    }
    private let playerTimer = Timer.publish(every: 0.25, on: .main, in: .common).autoconnect()

    private var activePresentation: RivunePlaybackPresentation {
        guard let current = model.playbackPresentation, current.id == presentation.id else { return presentation }
        return current
    }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            RivuneNativePlayer(player: player, aspect: sessionAspect, frameRateMatching: model.frameRateMatching)
                .scaleEffect(sessionAspect == .zoom ? 1.12 : 1)
#if os(tvOS)
            Color.clear
#else
            Color.clear.contentShape(Rectangle()).onTapGesture { revealControls() }
#endif
            if controlsVisible {
                LinearGradient(
                    colors: [.black.opacity(0.68), .clear, .black.opacity(0.78)],
                    startPoint: .top,
                    endPoint: .bottom
                )
                .ignoresSafeArea()
                RivunePlayerChrome(
                    title: activePresentation.title,
                    playing: playing,
                    position: Binding(get: { position }, set: { position = $0 }),
                    duration: duration,
                    close: { finish(completed: false) },
                    minimize: minimize,
                    seekBackward: { seek(by: -10) },
                    togglePlayback: togglePlayback,
                    seekForward: { seek(by: 10) },
                    nextEpisode: activePresentation.nextEpisode == nil ? nil : playNextEpisode,
                    scrubbingChanged: { editing in
                        scrubbing = editing
                        if !editing {
                            player.seek(
                                to: CMTime(seconds: position, preferredTimescale: 600),
                                toleranceBefore: .zero,
                                toleranceAfter: .zero
                            )
                            scheduleControlsHide()
                        }
                    }
                ) {
                    Group {
#if os(tvOS)
                        Button { cycleAudio() } label: { Label("Audio", systemImage: "speaker.wave.2") }
                        Button { cycleSubtitle() } label: { Label("Subtitles", systemImage: "captions.bubble") }
                        Button { cycleAspect() } label: { Label(rivuneLocalized(sessionAspect.displayName), systemImage: "aspectratio") }
                        Button { cycleSpeed() } label: { Label(playbackSpeed == 1 ? "1×" : "\(playbackSpeed.formatted())×", systemImage: "speedometer") }
#else
                        Menu {
                            if activePresentation.audioTracks.isEmpty && audioOptions.isEmpty {
                                Text("No alternate audio tracks")
                            }
                            ForEach(activePresentation.audioTracks, id: \.index) { track in
                                Button {
                                    changeServerOptions(audioTrack: track.index, subtitleId: activePresentation.selectedSubtitleId)
                                } label: {
                                    if activePresentation.selectedAudioTrack == track.index {
                                        Label(trackLabel(track), systemImage: "checkmark")
                                    } else {
                                        Text(trackLabel(track))
                                    }
                                }
                            }
                            ForEach(Array(audioOptions.enumerated()), id: \.offset) { _, option in
                                Button(option.displayName) { select(option, in: audioGroup) }
                            }
                        } label: {
                            Image(systemName: "speaker.wave.2")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(rivuneLocalized("Audio"))
                        }
                        .rivunePlayerOptionControl()

                        Menu {
                            Button {
                                changeServerOptions(audioTrack: activePresentation.selectedAudioTrack, subtitleId: "none")
                                select(nil, in: subtitleGroup)
                            } label: {
                                if activePresentation.selectedSubtitleId == nil || activePresentation.selectedSubtitleId == "none" {
                                    Label("Off", systemImage: "checkmark")
                                } else {
                                    Text("Off")
                                }
                            }
                            ForEach(activePresentation.subtitles) { subtitle in
                                Button {
                                    changeServerOptions(audioTrack: activePresentation.selectedAudioTrack, subtitleId: subtitle.id)
                                } label: {
                                    if activePresentation.selectedSubtitleId == subtitle.id {
                                        Label(subtitleLabel(subtitle), systemImage: "checkmark")
                                    } else {
                                        Text(subtitleLabel(subtitle))
                                    }
                                }
                            }
                            ForEach(Array(subtitleOptions.enumerated()), id: \.offset) { _, option in
                                Button(option.displayName) { select(option, in: subtitleGroup) }
                            }
                        } label: {
                            Image(systemName: "captions.bubble")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(rivuneLocalized("Subtitles"))
                        }
                        .rivunePlayerOptionControl()

                        Menu {
                            ForEach(RivuneVideoAspect.allCases) { aspect in
                                Button {
                                    sessionAspect = aspect
                                    scheduleControlsHide()
                                } label: {
                                    if sessionAspect == aspect {
                                        Label(rivuneLocalized(aspect.displayName), systemImage: "checkmark")
                                    } else {
                                        Text(rivuneLocalized(aspect.displayName))
                                    }
                                }
                            }
                        } label: {
                            Image(systemName: "aspectratio")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(rivuneLocalized(sessionAspect.displayName))
                        }
                        .rivunePlayerOptionControl()

                        Menu {
                            ForEach([0.5, 0.75, 1.0, 1.25, 1.5, 2.0], id: \.self) { speed in
                                Button(speed == 1 ? rivuneLocalized("Normal") : "\(speed.formatted())×") {
                                    setSpeed(speed)
                                }
                            }
                        } label: {
                            Image(systemName: "speedometer")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(playbackSpeed == 1 ? "1×" : "\(playbackSpeed.formatted())×")
                        }
                        .rivunePlayerOptionControl()
#endif
                    }
                    .disabled(model.playbackOptionLoading)
                }
                .transition(.opacity)
            }
            if let marker = activeMarker {
                Button { skip(marker) } label: {
                    Label(skipTitle(marker.type), systemImage: "forward.fill")
                        .font(.headline)
                        .padding(.horizontal, 8)
                }
                .rivunePlayerActionButton()
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
            }
            if let failureMessage {
                RivunePlaybackFailureView(
                    message: failureMessage,
                    retry: { self.failureMessage = nil; loadAttempt += 1 },
                    close: { finish(completed: false) }
                )
            }
        }
        .animation(.easeOut(duration: 0.18), value: controlsVisible)
        .task(id: "\(activePresentation.url.absoluteString)|\(loadAttempt)") {
            let current = activePresentation
            failureMessage = nil
            activatePlaybackAudioSession()
            let wasPlaying = player.timeControlStatus == .playing || player.currentItem == nil
            let item = AVPlayerItem(url: current.url)
            audioGroup = nil
            subtitleGroup = nil
            audioOptions = []
            subtitleOptions = []
            item.preferredPeakBitRate = preferredPeakBitRate
            player.replaceCurrentItem(with: item)
            if current.startSeconds > 0 {
                await player.seek(to: CMTime(seconds: Double(current.startSeconds), preferredTimescale: 600))
            }
            position = Double(current.startSeconds)
            duration = Double(current.durationSeconds ?? 0)
            if wasPlaying { player.playImmediately(atRate: Float(playbackSpeed)) }
            playing = wasPlaying
            applyRoomState()
            scheduleControlsHide()
            let loadedAudioGroup = try? await item.asset.loadMediaSelectionGroup(for: .audible)
            let loadedSubtitleGroup = try? await item.asset.loadMediaSelectionGroup(for: .legible)
            guard player.currentItem === item else { return }
            audioGroup = loadedAudioGroup
            audioOptions = loadedAudioGroup?.options ?? []
            subtitleGroup = loadedSubtitleGroup
            subtitleOptions = loadedSubtitleGroup?.options ?? []
        }
        .onReceive(playerTimer) { _ in updatePlaybackState() }
        .onChange(of: playing) { _ in model.updateCoordinationPlayback(position: position, duration: duration, playing: playing) }
        .onChange(of: model.pendingPlaybackCommands.first?.id) { _ in applyRemoteCommand() }
        .onChange(of: model.activePlaybackRoom?.version) { _ in applyRoomState() }
        .onDisappear { controlsTask?.cancel(); if !finished && !handoffToMPV { finish(completed: false) } }
        .onReceive(NotificationCenter.default.publisher(for: .AVPlayerItemDidPlayToEndTime)) { notification in
            guard notification.object as? AVPlayerItem === player.currentItem else { return }
            if activePresentation.nextEpisode != nil && model.autoplayNextEpisode { playNextEpisode() }
            else { finish(completed: true) }
        }
    }

    private var preferredPeakBitRate: Double {
        switch model.playbackQuality {
        case .automatic: return 0
        case .economy: return 2_000_000
        case .balanced: return 8_000_000
        case .maximum: return 30_000_000
        }
    }

    private func updatePlaybackState() {
        if let item = player.currentItem, item.status == .failed {
            handlePlaybackFailure(item.error ?? player.error)
            return
        }
        let seconds = player.currentTime().seconds
        if seconds.isFinite, !scrubbing { position = max(seconds, 0) }
        let observedDuration = player.currentItem?.duration.seconds ?? .nan
        let itemDuration = observedDuration.isFinite && observedDuration > 0 ? observedDuration : Double(activePresentation.durationSeconds ?? 0)
        if itemDuration > 0 { duration = itemDuration }
        playing = player.timeControlStatus == .playing
        model.updateCoordinationPlayback(position: position, duration: duration, playing: playing)
        updateMarker(at: seconds)
    }
    private func handlePlaybackFailure(_ error: Error?) {
        guard failureMessage == nil, !handoffToMPV, !finished else { return }
        model.recordPlaybackFailure()
        let detail = error?.localizedDescription
            ?? player.currentItem?.errorLog()?.events.last?.errorComment
            ?? "The media format or server response was rejected"
        if activePresentation.fallbackAllowed {
            handoffToMPV = true
            player.pause()
            player.replaceCurrentItem(with: nil)
            model.fallbackPlaybackToMPV(position: Int(position), duration: max(Int(duration), Int(position)))
        } else {
            failureMessage = rivuneLocalizedFormat("Apple player could not play this stream: %@. Choose MPV under Settings > Player for broader format support.", detail)
            player.pause()
            playing = false
            controlsVisible = true
        }
    }


    private func select(_ option: AVMediaSelectionOption?, in group: AVMediaSelectionGroup?) {
        guard let group else { return }
        player.currentItem?.select(option, in: group)
        scheduleControlsHide()
    }

    private func changeServerOptions(audioTrack: Int?, subtitleId: String?) {
        model.selectPlaybackOptions(audioTrack: audioTrack, subtitleId: subtitleId, position: Int(position))
        scheduleControlsHide()
    }

    private func trackLabel(_ track: PlaybackMediaTrack) -> String {
        [track.title, track.language?.uppercased(), track.channels.map { rivuneLocalizedFormat("%d ch", $0) }].compactMap { $0 }.joined(separator: " · ").nilIfEmpty ?? rivuneLocalizedFormat("Track %d", track.index)
    }

    private func subtitleLabel(_ subtitle: PlaybackSubtitle) -> String {
        [subtitle.language?.uppercased(), subtitle.forced == true ? rivuneLocalized("Forced") : nil].compactMap { $0 }.joined(separator: " · ").nilIfEmpty ?? rivuneLocalized("Subtitle")
    }

    private func minimize() {
        guard !finished else { return }
        finished = true
        controlsTask?.cancel()
        let rawDuration = player.currentItem?.duration.seconds ?? Double(activePresentation.durationSeconds ?? 0)
        let finalDuration = Int(rawDuration.isFinite ? rawDuration : Double(activePresentation.durationSeconds ?? 0))
        let finalPosition = Int(player.currentTime().seconds.isFinite ? player.currentTime().seconds : position)
        player.pause()
        model.minimizePlayback(position: finalPosition, duration: max(finalDuration, finalPosition))
    }

    private func cycleAspect() {
        let values = RivuneVideoAspect.allCases
        let index = values.firstIndex(of: sessionAspect) ?? 0
        sessionAspect = values[(index + 1) % values.count]
        scheduleControlsHide()
    }
    private func cycleAudio() {
        if !activePresentation.audioTracks.isEmpty {
            let selected = activePresentation.selectedAudioTrack
            let index = selected.flatMap { value in activePresentation.audioTracks.firstIndex { $0.index == value } } ?? -1
            let next = activePresentation.audioTracks[(index + 1) % activePresentation.audioTracks.count]
            changeServerOptions(audioTrack: next.index, subtitleId: activePresentation.selectedSubtitleId)
            return
        }
        guard let group = audioGroup, !audioOptions.isEmpty else { return }
        let selected = player.currentItem?.currentMediaSelection.selectedMediaOption(in: group)
        let index = selected.flatMap { current in audioOptions.firstIndex(where: { $0 === current }) } ?? -1
        select(audioOptions[(index + 1) % audioOptions.count], in: group)
    }

    private func cycleSubtitle() {
        if !activePresentation.subtitles.isEmpty {
            guard let selected = activePresentation.selectedSubtitleId,
                  selected != "none",
                  let index = activePresentation.subtitles.firstIndex(where: { $0.id == selected }),
                  index + 1 < activePresentation.subtitles.count else {
                let next = activePresentation.selectedSubtitleId == nil || activePresentation.selectedSubtitleId == "none" ? activePresentation.subtitles[0].id : "none"
                changeServerOptions(audioTrack: activePresentation.selectedAudioTrack, subtitleId: next)
                return
            }
            changeServerOptions(audioTrack: activePresentation.selectedAudioTrack, subtitleId: activePresentation.subtitles[index + 1].id)
            return
        }
        guard let group = subtitleGroup, !subtitleOptions.isEmpty else { return }
        let selected = player.currentItem?.currentMediaSelection.selectedMediaOption(in: group)
        guard let selected else { select(subtitleOptions[0], in: group); return }
        let index = subtitleOptions.firstIndex(where: { $0 === selected }) ?? -1
        if index + 1 >= subtitleOptions.count { select(nil, in: group) }
        else { select(subtitleOptions[index + 1], in: group) }
    }

    private func setSpeed(_ value: Double) {
        playbackSpeed = value
        if playing { player.playImmediately(atRate: Float(value)) }
        scheduleControlsHide()
    }

    private func cycleSpeed() {
        let values = [0.5, 0.75, 1.0, 1.25, 1.5, 2.0]
        let index = values.firstIndex(of: playbackSpeed) ?? 1
        setSpeed(values[(index + 1) % values.count])
    }

    private func updateMarker(at seconds: Double) {
        guard seconds.isFinite else { return }
        guard let marker = activePresentation.markers.first(where: {
            seconds >= $0.startSeconds && seconds < $0.endSeconds && !consumedMarkers.contains(markerKey($0))
        }) else { activeMarker = nil; return }
        if shouldAutoSkip(marker.type) { skip(marker) } else { activeMarker = marker }
    }

    private func togglePlayback() {
        if playing { player.pause() } else { player.playImmediately(atRate: Float(playbackSpeed)) }
        playing.toggle()
        scheduleControlsHide()
    }

    private func applyRemoteCommand() {
        guard let command = model.pendingPlaybackCommands.first else { return }
        switch command.command {
        case "play": player.playImmediately(atRate: Float(playbackSpeed))
        case "pause": player.pause()
        case "seek": if let milliseconds = command.positionMilliseconds { player.seek(to: CMTime(seconds: Double(milliseconds) / 1_000, preferredTimescale: 600)) }
        case "stop": finish(completed: false)
        default: break
        }
        model.consumePlaybackCommand()
    }

    private func applyRoomState() {
        guard let room = model.activePlaybackRoom,
              !room.currentMemberIsHost else { return }
        let target = Double(room.positionMilliseconds) / 1_000
        if abs(position - target) > 1.5 { player.seek(to: CMTime(seconds: target, preferredTimescale: 600)) }
        switch room.state {
        case "playing": player.playImmediately(atRate: Float(playbackSpeed)); playing = true
        case "paused": player.pause(); playing = false
        case "ended": finish(completed: true)
        default: break
        }
    }

    private func seek(by offset: Double) {
        let destination = min(max(position + offset, 0), max(duration, 0))
        position = destination
        player.seek(to: CMTime(seconds: destination, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
        scheduleControlsHide()
    }

    private func revealControls() {
        controlsVisible.toggle()
        if controlsVisible { scheduleControlsHide() }
    }

    private func scheduleControlsHide() {
#if !os(tvOS)
        controlsTask?.cancel()
        guard playing else { return }
        controlsTask = Task {
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            guard !Task.isCancelled, !scrubbing else { return }
            controlsVisible = false
        }
#endif
    }

    private func skip(_ marker: PlaybackMarker) {
        consumedMarkers.insert(markerKey(marker))
        activeMarker = nil
        player.seek(to: CMTime(seconds: marker.endSeconds, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    private func shouldAutoSkip(_ type: PlaybackMarkerType) -> Bool {
        switch type { case .intro: return model.autoSkipIntro; case .recap: return model.autoSkipRecap; case .outro: return model.autoSkipOutro }
    }

    private func skipTitle(_ type: PlaybackMarkerType) -> String {
        switch type { case .intro: return rivuneLocalized("Skip intro"); case .recap: return rivuneLocalized("Skip recap"); case .outro: return rivuneLocalized("Skip outro") }
    }

    private func markerKey(_ marker: PlaybackMarker) -> String { "\(marker.type.rawValue):\(marker.startSeconds):\(marker.endSeconds)" }
    private func formatTime(_ value: Double) -> String {
        let seconds = max(Int(value.isFinite ? value : 0), 0)
        let hours = seconds / 3600
        return hours > 0 ? String(format: "%d:%02d:%02d", hours, seconds / 60 % 60, seconds % 60) : String(format: "%02d:%02d", seconds / 60, seconds % 60)
    }

    private func playNextEpisode() {
        guard !finished, activePresentation.nextEpisode != nil else { return }
        finished = true
        controlsTask?.cancel()
        let rawDuration = player.currentItem?.duration.seconds ?? Double(activePresentation.durationSeconds ?? 0)
        let finalDuration = Int(rawDuration.isFinite ? rawDuration : Double(activePresentation.durationSeconds ?? 0))
        let finalPosition = Int(player.currentTime().seconds.isFinite ? player.currentTime().seconds : position)
        player.pause()
        model.playNextEpisode(position: finalPosition, duration: max(finalDuration, finalPosition))
    }

    private func finish(completed: Bool) {
        guard !finished else { return }
        finished = true
        controlsTask?.cancel()
        let rawDuration = player.currentItem?.duration.seconds ?? Double(activePresentation.durationSeconds ?? 0)
        let finalDuration = Int(rawDuration.isFinite ? rawDuration : Double(activePresentation.durationSeconds ?? 0))
        let finalPosition = Int(player.currentTime().seconds.isFinite ? player.currentTime().seconds : position)
        player.pause()
        model.playbackFinished(position: finalPosition, duration: max(finalDuration, finalPosition), completed: completed)
    }
}

private struct RivuneMPVInternalPlayerView: View {
    let presentation: RivunePlaybackPresentation
    @ObservedObject var model: RivuneAppModel
    @StateObject private var player = RivuneMPVPlaybackController()
    @State private var finished = false
    @State private var activeMarker: PlaybackMarker?
    @State private var consumedMarkers = Set<String>()
    @State private var controlsVisible = true
    @State private var scrubbing = false
    @State private var controlsTask: Task<Void, Never>?
    @State private var sessionAspect: RivuneVideoAspect
    @State private var playbackSpeed = 1.0
    @State private var selectedAudioTrack: Int?
    @State private var selectedSubtitleId: String?
    @State private var failureMessage: String?
    @State private var loadAttempt = 0
    @State private var handoff = false

    init(presentation: RivunePlaybackPresentation, model: RivuneAppModel) {
        self.presentation = presentation
        self.model = model
        _sessionAspect = State(initialValue: model.videoAspect)
        _selectedAudioTrack = State(initialValue: presentation.selectedAudioTrack)
        _selectedSubtitleId = State(initialValue: presentation.selectedSubtitleId)
    }

    private var activePresentation: RivunePlaybackPresentation {
        model.playbackPresentation.flatMap { $0.id == presentation.id ? $0 : nil } ?? presentation
    }

    private var loadIdentifier: String {
        "\(activePresentation.url.absoluteString)|\(activePresentation.startSeconds)|\(loadAttempt)"
    }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            RivuneMPVPlayerSurface(controller: player, aspect: sessionAspect)
                .scaleEffect(sessionAspect == .zoom ? 1.12 : 1)
#if os(tvOS)
            Color.clear
#else
            Color.clear.contentShape(Rectangle()).onTapGesture { revealControls() }
#endif
            if controlsVisible {
                LinearGradient(
                    colors: [.black.opacity(0.68), .clear, .black.opacity(0.78)],
                    startPoint: .top,
                    endPoint: .bottom
                )
                .ignoresSafeArea()
                RivunePlayerChrome(
                    title: activePresentation.title,
                    badge: "MPV",
                    playing: player.playing,
                    position: Binding(
                        get: { player.position },
                        set: { if !$0.isNaN { player.seek(to: $0) } }
                    ),
                    duration: player.duration,
                    close: { finish(completed: false) },
                    minimize: minimize,
                    seekBackward: { seek(by: -10) },
                    togglePlayback: togglePlayback,
                    seekForward: { seek(by: 10) },
                    nextEpisode: activePresentation.nextEpisode == nil ? nil : playNextEpisode,
                    scrubbingChanged: { editing in
                        scrubbing = editing
                        if !editing { scheduleControlsHide() }
                    }
                ) {
                    Group {
#if os(tvOS)
                        Button { cycleAudio() } label: { Label("Audio", systemImage: "speaker.wave.2") }
                        Button { cycleSubtitle() } label: { Label("Subtitles", systemImage: "captions.bubble") }
                        Button { cycleAspect() } label: { Label(rivuneLocalized(sessionAspect.displayName), systemImage: "aspectratio") }
                        Button { cycleSpeed() } label: { Label(playbackSpeed == 1 ? "1×" : "\(playbackSpeed.formatted())×", systemImage: "speedometer") }
#else
                        Menu {
                            if activePresentation.audioTracks.isEmpty { Text("No alternate audio tracks") }
                            ForEach(activePresentation.audioTracks, id: \.index) { track in
                                Button {
                                    selectedAudioTrack = track.index
                                    player.selectAudio(streamIndex: track.index)
                                    scheduleControlsHide()
                                } label: {
                                    if selectedAudioTrack == track.index {
                                        Label(trackLabel(track), systemImage: "checkmark")
                                    } else {
                                        Text(trackLabel(track))
                                    }
                                }
                            }
                        } label: {
                            Image(systemName: "speaker.wave.2")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(rivuneLocalized("Audio"))
                        }
                        .rivunePlayerOptionControl()

                        Menu {
                            Button { selectSubtitle(nil) } label: {
                                if selectedSubtitleId == nil || selectedSubtitleId == "none" {
                                    Label("Off", systemImage: "checkmark")
                                } else {
                                    Text("Off")
                                }
                            }
                            ForEach(activePresentation.subtitles) { subtitle in
                                Button { selectSubtitle(subtitle) } label: {
                                    if selectedSubtitleId == subtitle.id {
                                        Label(subtitleLabel(subtitle), systemImage: "checkmark")
                                    } else {
                                        Text(subtitleLabel(subtitle))
                                    }
                                }
                            }
                        } label: {
                            Image(systemName: "captions.bubble")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(rivuneLocalized("Subtitles"))
                        }
                        .rivunePlayerOptionControl()

                        Menu {
                            ForEach(RivuneVideoAspect.allCases) { aspect in
                                Button {
                                    sessionAspect = aspect
                                    scheduleControlsHide()
                                } label: {
                                    if sessionAspect == aspect {
                                        Label(rivuneLocalized(aspect.displayName), systemImage: "checkmark")
                                    } else {
                                        Text(rivuneLocalized(aspect.displayName))
                                    }
                                }
                            }
                        } label: {
                            Image(systemName: "aspectratio")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(rivuneLocalized(sessionAspect.displayName))
                        }
                        .rivunePlayerOptionControl()

                        Menu {
                            ForEach([0.5, 0.75, 1.0, 1.25, 1.5, 2.0], id: \.self) { speed in
                                Button(speed == 1 ? rivuneLocalized("Normal") : "\(speed.formatted())×") {
                                    setSpeed(speed)
                                }
                            }
                        } label: {
                            Image(systemName: "speedometer")
                                .foregroundStyle(.white)
                                .frame(width: 28, height: 28)
                                .accessibilityLabel(playbackSpeed == 1 ? "1×" : "\(playbackSpeed.formatted())×")
                        }
                        .rivunePlayerOptionControl()
#endif
                    }
                }
                .transition(.opacity)
            }
            if player.buffering && failureMessage == nil {
                ProgressView("Buffering…").padding(18).background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 14))
            }
            if let marker = activeMarker {
                Button { skip(marker) } label: {
                    Label(skipTitle(marker.type), systemImage: "forward.fill")
                        .font(.headline)
                        .padding(.horizontal, 8)
                }
                .rivunePlayerActionButton()
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
            }
            if let failureMessage {
                RivunePlaybackFailureView(
                    message: failureMessage,
                    retry: { self.failureMessage = nil; loadAttempt += 1 },
                    close: { finish(completed: false) }
                )
            }
        }
        .animation(.easeOut(duration: 0.18), value: controlsVisible)
        .task(id: loadIdentifier) {
            activatePlaybackAudioSession()
            failureMessage = nil
            selectedAudioTrack = activePresentation.selectedAudioTrack
            selectedSubtitleId = activePresentation.selectedSubtitleId
            player.load(
                url: activePresentation.url,
                startSeconds: activePresentation.startSeconds,
                selectedAudioTrack: selectedAudioTrack,
                selectedSubtitleURL: selectedSubtitle().flatMap(subtitleURL)
            )
            player.setSpeed(playbackSpeed)
            player.setAspect(sessionAspect)
            applyRoomState()
            scheduleControlsHide()
        }
        .onReceive(player.$position) { value in
            updateMarker(at: value)
            model.updateCoordinationPlayback(position: value, duration: player.duration, playing: player.playing)
        }
        .onReceive(player.$playing) { active in model.updateCoordinationPlayback(position: player.position, duration: player.duration, playing: active) }
        .onReceive(player.$failureMessage.compactMap { $0 }) { failureMessage = $0; controlsVisible = true; model.recordPlaybackFailure() }
        .onChange(of: model.pendingPlaybackCommands.first?.id) { _ in applyRemoteCommand() }
        .onChange(of: model.activePlaybackRoom?.version) { _ in applyRoomState() }
        .onReceive(player.$ended.filter { $0 }) { _ in
            if activePresentation.nextEpisode != nil && model.autoplayNextEpisode { playNextEpisode() }
            else { finish(completed: true) }
        }
        .onDisappear {
            controlsTask?.cancel()
            player.shutdown()
            if !finished && !handoff { finish(completed: false) }
        }
    }

    private func selectedSubtitle() -> PlaybackSubtitle? {
        activePresentation.subtitles.first { $0.id == selectedSubtitleId }
    }

    private func subtitleURL(_ subtitle: PlaybackSubtitle) -> URL? {
        subtitle.url.flatMap(model.resolvedResourceURL)
    }

    private func selectSubtitle(_ subtitle: PlaybackSubtitle?) {
        selectedSubtitleId = subtitle?.id ?? "none"
        player.selectSubtitle(url: subtitle.flatMap(subtitleURL), title: subtitle.map(subtitleLabel), language: subtitle?.language)
        scheduleControlsHide()
    }

    private func cycleAudio() {
        guard !activePresentation.audioTracks.isEmpty else { return }
        let index = selectedAudioTrack.flatMap { selected in activePresentation.audioTracks.firstIndex { $0.index == selected } } ?? -1
        let next = activePresentation.audioTracks[(index + 1) % activePresentation.audioTracks.count]
        selectedAudioTrack = next.index
        player.selectAudio(streamIndex: next.index)
        scheduleControlsHide()
    }

    private func cycleSubtitle() {
        guard !activePresentation.subtitles.isEmpty else { return }
        guard let selectedSubtitleId,
              selectedSubtitleId != "none",
              let index = activePresentation.subtitles.firstIndex(where: { $0.id == selectedSubtitleId }),
              index + 1 < activePresentation.subtitles.count else {
            let next = self.selectedSubtitleId == nil || self.selectedSubtitleId == "none" ? activePresentation.subtitles[0] : nil
            selectSubtitle(next)
            return
        }
        selectSubtitle(activePresentation.subtitles[index + 1])
    }

    private func togglePlayback() {
        if player.playing { player.pause() } else { player.play() }
        scheduleControlsHide()
    }

    private func seek(by offset: Double) {
        player.seek(to: min(max(player.position + offset, 0), max(player.duration, 0)))
        scheduleControlsHide()
    }

    private func setSpeed(_ value: Double) {
        playbackSpeed = value
        player.setSpeed(value)
        scheduleControlsHide()
    }

    private func cycleSpeed() {
        let values = [0.5, 0.75, 1.0, 1.25, 1.5, 2.0]
        let index = values.firstIndex(of: playbackSpeed) ?? 1
        setSpeed(values[(index + 1) % values.count])
    }

    private func applyRemoteCommand() {
        guard let command = model.pendingPlaybackCommands.first else { return }
        switch command.command {
        case "play": player.play()
        case "pause": player.pause()
        case "seek": if let milliseconds = command.positionMilliseconds { player.seek(to: Double(milliseconds) / 1_000) }
        case "stop": finish(completed: false)
        default: break
        }
        model.consumePlaybackCommand()
    }

    private func applyRoomState() {
        guard let room = model.activePlaybackRoom,
              !room.currentMemberIsHost else { return }
        let target = Double(room.positionMilliseconds) / 1_000
        if abs(player.position - target) > 1.5 { player.seek(to: target) }
        switch room.state {
        case "playing": player.play()
        case "paused": player.pause()
        case "ended": finish(completed: true)
        default: break
        }
    }

    private func cycleAspect() {
        let values = RivuneVideoAspect.allCases
        let index = values.firstIndex(of: sessionAspect) ?? 0
        sessionAspect = values[(index + 1) % values.count]
        player.setAspect(sessionAspect)
        scheduleControlsHide()
    }

    private func revealControls() {
        controlsVisible.toggle()
        if controlsVisible { scheduleControlsHide() }
    }

    private func scheduleControlsHide() {
#if !os(tvOS)
        controlsTask?.cancel()
        guard player.playing else { return }
        controlsTask = Task {
            try? await Task.sleep(nanoseconds: 5_000_000_000)
            guard !Task.isCancelled, !scrubbing else { return }
            controlsVisible = false
        }
#endif
    }

    private func updateMarker(at seconds: Double) {
        guard seconds.isFinite else { return }
        guard let marker = activePresentation.markers.first(where: {
            seconds >= $0.startSeconds && seconds < $0.endSeconds && !consumedMarkers.contains(markerKey($0))
        }) else { activeMarker = nil; return }
        if shouldAutoSkip(marker.type) { skip(marker) } else { activeMarker = marker }
    }

    private func skip(_ marker: PlaybackMarker) {
        consumedMarkers.insert(markerKey(marker))
        activeMarker = nil
        player.seek(to: marker.endSeconds)
    }

    private func minimize() {
        guard !finished else { return }
        handoff = true
        finished = true
        player.pause()
        model.minimizePlayback(position: Int(player.position), duration: max(Int(player.duration), Int(player.position)))
    }

    private func playNextEpisode() {
        guard !finished, activePresentation.nextEpisode != nil else { return }
        finished = true
        controlsTask?.cancel()
        player.pause()
        model.playNextEpisode(
            position: Int(player.position),
            duration: max(Int(player.duration), Int(player.position))
        )
    }

    private func finish(completed: Bool) {
        guard !finished else { return }
        finished = true
        controlsTask?.cancel()
        player.pause()
        model.playbackFinished(position: Int(player.position), duration: max(Int(player.duration), Int(player.position)), completed: completed)
    }

    private func trackLabel(_ track: PlaybackMediaTrack) -> String {
        [track.title, track.language?.uppercased(), track.channels.map { rivuneLocalizedFormat("%d ch", $0) }].compactMap { $0 }.joined(separator: " · ").nilIfEmpty ?? rivuneLocalizedFormat("Track %d", track.index)
    }

    private func subtitleLabel(_ subtitle: PlaybackSubtitle) -> String {
        [subtitle.language?.uppercased(), subtitle.forced == true ? rivuneLocalized("Forced") : nil].compactMap { $0 }.joined(separator: " · ").nilIfEmpty ?? rivuneLocalized("Subtitle")
    }

    private func shouldAutoSkip(_ type: PlaybackMarkerType) -> Bool {
        switch type { case .intro: return model.autoSkipIntro; case .recap: return model.autoSkipRecap; case .outro: return model.autoSkipOutro }
    }

    private func skipTitle(_ type: PlaybackMarkerType) -> String {
        switch type { case .intro: return rivuneLocalized("Skip intro"); case .recap: return rivuneLocalized("Skip recap"); case .outro: return rivuneLocalized("Skip outro") }
    }

    private func markerKey(_ marker: PlaybackMarker) -> String { "\(marker.type.rawValue):\(marker.startSeconds):\(marker.endSeconds)" }

    private func formatTime(_ value: Double) -> String {
        let seconds = max(Int(value.isFinite ? value : 0), 0)
        let hours = seconds / 3600
        return hours > 0 ? String(format: "%d:%02d:%02d", hours, seconds / 60 % 60, seconds % 60) : String(format: "%02d:%02d", seconds / 60, seconds % 60)
    }
}

private struct RivunePlaybackFailureView: View {
    let message: String
    let retry: () -> Void
    let close: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill").font(.largeTitle).foregroundStyle(.yellow)
            Text("Playback failed").font(.title2.bold())
            Text(rivuneLocalized(message))
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
#if !os(tvOS)
                .textSelection(.enabled)
#endif
            HStack(spacing: 12) {
                Button("Close", action: close).rivuneGlassButton()
                Button("Try again", action: retry).rivunePlayerActionButton()
            }
        }
        .padding(26).frame(maxWidth: 520)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .padding(24)
    }
}

struct RivuneMiniPlayerView: View {
    let presentation: RivunePlaybackPresentation
    @ObservedObject var model: RivuneAppModel

    @ViewBuilder var body: some View {
        let active = model.minimizedPlaybackPresentation.flatMap { $0.id == presentation.id ? $0 : nil } ?? presentation
        if active.engine == .mpv {
            RivuneMPVMiniPlayerView(presentation: active, model: model)
        } else {
            RivuneNativeMiniPlayerView(presentation: active, model: model)
        }
    }
}

private struct RivuneNativeMiniPlayerView: View {
    let presentation: RivunePlaybackPresentation
    @ObservedObject var model: RivuneAppModel
    @State private var player = AVPlayer()
    @State private var position = 0.0
    @State private var duration = 0.0
    @State private var playing = false
    @State private var handoff = false
    @State private var finished = false
    @State private var failureMessage: String?
    @State private var loadAttempt = 0
    private let timer = Timer.publish(every: 0.5, on: .main, in: .common).autoconnect()

    var body: some View {
        ZStack {
            Color.black
            RivuneNativePlayer(player: player, aspect: model.videoAspect, frameRateMatching: model.frameRateMatching)
            LinearGradient(colors: [.black.opacity(0.58), .clear, .black.opacity(0.78)], startPoint: .top, endPoint: .bottom)
            VStack {
                HStack {
                    Text(presentation.title).font(.caption.weight(.semibold)).lineLimit(1)
                    Spacer()
                    if presentation.nextEpisode != nil {
                        Button { playNextEpisode() } label: { Image(systemName: "forward.end.fill").font(.title3) }
                            .rivuneMiniPlayerControl()
                            .accessibilityLabel(rivuneLocalized("Next episode"))
                    }
                    Button { finish(completed: false) } label: { Image(systemName: "xmark") }
                        .rivuneMiniPlayerControl()
                        .accessibilityLabel("Close player")
                }
                Spacer()
                HStack(spacing: 18) {
                    Button { togglePlayback() } label: { Image(systemName: playing ? "pause.fill" : "play.fill").font(.title2) }
                        .rivuneMiniPlayerControl()
                        .accessibilityLabel(rivuneLocalized(playing ? "Pause" : "Play"))
                    Spacer()
                    Button { restore() } label: { Image(systemName: "pip.exit").font(.title3) }
                        .rivuneMiniPlayerControl()
                        .accessibilityLabel(rivuneLocalized("Return to full player"))
                }
            }
            .padding(12)
            if let failureMessage {
                VStack(spacing: 8) {
                    Text("Playback failed").font(.caption.bold())
                    Text(rivuneLocalized(failureMessage)).font(.caption2).lineLimit(3).multilineTextAlignment(.center)
                    Button("Try again") { self.failureMessage = nil; loadAttempt += 1 }.rivunePlayerActionButton()
                }
                .padding(12).background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)).padding(8)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay { RoundedRectangle(cornerRadius: 16, style: .continuous).stroke(Color.white.opacity(0.18), lineWidth: 1) }
        .shadow(color: .black.opacity(0.55), radius: 18, y: 8)
        .task(id: loadAttempt) {
            activatePlaybackAudioSession()
            failureMessage = nil
            let item = AVPlayerItem(url: presentation.url)
            player.replaceCurrentItem(with: item)
            if presentation.startSeconds > 0 {
                await player.seek(to: CMTime(seconds: Double(presentation.startSeconds), preferredTimescale: 600))
            }
            position = Double(presentation.startSeconds)
            duration = Double(presentation.durationSeconds ?? 0)
            player.play()
            playing = true
            model.updateCoordinationPlayback(position: position, duration: duration, playing: playing)
            applyRoomState()
            applyRemoteCommand()
        }
        .onReceive(timer) { _ in
            if let item = player.currentItem, item.status == .failed {
                handlePlaybackFailure(item.error ?? player.error)
                return
            }
            let seconds = player.currentTime().seconds
            if seconds.isFinite { position = max(seconds, 0) }
            let observed = player.currentItem?.duration.seconds ?? .nan
            if observed.isFinite && observed > 0 { duration = observed }
            playing = player.timeControlStatus == .playing
            model.updateCoordinationPlayback(position: position, duration: duration, playing: playing)
        }
        .onChange(of: model.pendingPlaybackCommands.first?.id) { _ in applyRemoteCommand() }
        .onChange(of: model.activePlaybackRoom?.version) { _ in applyRoomState() }
        .onReceive(NotificationCenter.default.publisher(for: .AVPlayerItemDidPlayToEndTime)) { notification in
            guard notification.object as? AVPlayerItem === player.currentItem else { return }
            if presentation.nextEpisode != nil && model.autoplayNextEpisode { playNextEpisode() }
            else { finish(completed: true) }
        }
        .onDisappear {
            if !handoff && !finished { finish(completed: false) }
        }
    }

    private func handlePlaybackFailure(_ error: Error?) {
        guard failureMessage == nil, !handoff, !finished else { return }
        model.recordPlaybackFailure()
        let detail = error?.localizedDescription ?? "The media format or server response was rejected"
        if presentation.fallbackAllowed {
            handoff = true
            player.pause()
            player.replaceCurrentItem(with: nil)
            model.fallbackMinimizedPlaybackToMPV(position: Int(position), duration: max(Int(duration), Int(position)))
        } else {
            failureMessage = rivuneLocalizedFormat("Apple player: %@", detail)
            player.pause()
            playing = false
        }
    }

    private func togglePlayback() {
        if playing { player.pause() } else { player.play() }
        playing.toggle()
    }

    private func applyRemoteCommand() {
        guard let command = model.pendingPlaybackCommands.first else { return }
        switch command.command {
        case "play": player.play(); playing = true
        case "pause": player.pause(); playing = false
        case "seek":
            if let milliseconds = command.positionMilliseconds {
                let target = Double(milliseconds) / 1_000
                player.seek(to: CMTime(seconds: target, preferredTimescale: 600))
                position = target
            }
        case "stop": finish(completed: false)
        default: return
        }
        model.updateCoordinationPlayback(position: position, duration: duration, playing: playing)
        model.consumePlaybackCommand()
    }

    private func applyRoomState() {
        guard let room = model.activePlaybackRoom, !room.currentMemberIsHost else { return }
        let target = Double(room.positionMilliseconds) / 1_000
        if abs(position - target) > 1.5 {
            player.seek(to: CMTime(seconds: target, preferredTimescale: 600))
            position = target
        }
        switch room.state {
        case "playing": player.play(); playing = true
        case "paused": player.pause(); playing = false
        case "ended": finish(completed: true)
        default: break
        }
        model.updateCoordinationPlayback(position: position, duration: duration, playing: playing)
    }
    private func playNextEpisode() {
        guard !finished, presentation.nextEpisode != nil else { return }
        finished = true
        player.pause()
        model.playNextMinimizedEpisode(position: Int(position), duration: max(Int(duration), Int(position)))
    }


    private func restore() {
        guard !finished else { return }
        handoff = true
        player.pause()
        model.resumeMinimizedPlayback(position: Int(position), duration: max(Int(duration), Int(position)))
    }

    private func finish(completed: Bool) {
        guard !finished else { return }
        finished = true
        player.pause()
        model.minimizedPlaybackFinished(position: Int(position), duration: max(Int(duration), Int(position)), completed: completed)
    }
}
private struct RivuneMPVMiniPlayerView: View {
    let presentation: RivunePlaybackPresentation
    @ObservedObject var model: RivuneAppModel
    @StateObject private var player = RivuneMPVPlaybackController()
    @State private var handoff = false
    @State private var finished = false
    @State private var failureMessage: String?
    @State private var loadAttempt = 0

    var body: some View {
        ZStack {
            Color.black
            RivuneMPVPlayerSurface(controller: player, aspect: model.videoAspect)
            LinearGradient(colors: [.black.opacity(0.58), .clear, .black.opacity(0.78)], startPoint: .top, endPoint: .bottom)
            VStack {
                HStack {
                    Text(presentation.title).font(.caption.weight(.semibold)).lineLimit(1)
                    Text("MPV").font(.system(size: 8, weight: .bold)).padding(.horizontal, 5).padding(.vertical, 2).background(.ultraThinMaterial, in: Capsule())
                    Spacer()
                    if presentation.nextEpisode != nil {
                        Button { playNextEpisode() } label: { Image(systemName: "forward.end.fill").font(.title3) }
                            .rivuneMiniPlayerControl()
                            .accessibilityLabel(rivuneLocalized("Next episode"))
                    }
                    Button { finish(completed: false) } label: { Image(systemName: "xmark") }
                        .rivuneMiniPlayerControl()
                        .accessibilityLabel("Close player")
                }
                Spacer()
                HStack(spacing: 18) {
                    Button { player.playing ? player.pause() : player.play() } label: { Image(systemName: player.playing ? "pause.fill" : "play.fill").font(.title2) }
                        .rivuneMiniPlayerControl()
                        .accessibilityLabel(rivuneLocalized(player.playing ? "Pause" : "Play"))
                    Spacer()
                    Button { restore() } label: { Image(systemName: "pip.exit").font(.title3) }
                        .rivuneMiniPlayerControl()
                        .accessibilityLabel(rivuneLocalized("Return to full player"))
                }
            }
            .padding(12)
            if let failureMessage {
                VStack(spacing: 8) {
                    Text("Playback failed").font(.caption.bold())
                    Text(rivuneLocalized(failureMessage)).font(.caption2).lineLimit(3).multilineTextAlignment(.center)
                    Button("Try again") { self.failureMessage = nil; loadAttempt += 1 }.rivunePlayerActionButton()
                }
                .padding(12).background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)).padding(8)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay { RoundedRectangle(cornerRadius: 16, style: .continuous).stroke(Color.white.opacity(0.18), lineWidth: 1) }
        .shadow(color: .black.opacity(0.55), radius: 18, y: 8)
        .task(id: loadAttempt) {
            activatePlaybackAudioSession()
            failureMessage = nil
            let selectedSubtitle = presentation.subtitles.first { $0.id == presentation.selectedSubtitleId }
            player.load(
                url: presentation.url,
                startSeconds: presentation.startSeconds,
                selectedAudioTrack: presentation.selectedAudioTrack,
                selectedSubtitleURL: selectedSubtitle?.url.flatMap(model.resolvedResourceURL)
            )
            player.setAspect(model.videoAspect)
            applyRoomState()
            applyRemoteCommand()
        }
        .onReceive(player.$position) { value in
            model.updateCoordinationPlayback(position: value, duration: player.duration, playing: player.playing)
        }
        .onReceive(player.$playing) { active in
            model.updateCoordinationPlayback(position: player.position, duration: player.duration, playing: active)
        }
        .onChange(of: model.pendingPlaybackCommands.first?.id) { _ in applyRemoteCommand() }
        .onChange(of: model.activePlaybackRoom?.version) { _ in applyRoomState() }
        .onReceive(player.$failureMessage.compactMap { $0 }) { failureMessage = $0; model.recordPlaybackFailure() }
        .onReceive(player.$ended.filter { $0 }) { _ in
            if presentation.nextEpisode != nil && model.autoplayNextEpisode { playNextEpisode() }
            else { finish(completed: true) }
        }
        .onDisappear {
            player.shutdown()
            if !handoff && !finished { finish(completed: false) }
        }
    }

    private func applyRemoteCommand() {
        guard let command = model.pendingPlaybackCommands.first else { return }
        switch command.command {
        case "play": player.play()
        case "pause": player.pause()
        case "seek": if let milliseconds = command.positionMilliseconds { player.seek(to: Double(milliseconds) / 1_000) }
        case "stop": finish(completed: false)
        default: return
        }
        model.updateCoordinationPlayback(position: player.position, duration: player.duration, playing: player.playing)
        model.consumePlaybackCommand()
    }

    private func applyRoomState() {
        guard let room = model.activePlaybackRoom, !room.currentMemberIsHost else { return }
        let target = Double(room.positionMilliseconds) / 1_000
        if abs(player.position - target) > 1.5 { player.seek(to: target) }
        switch room.state {
        case "playing": player.play()
        case "paused": player.pause()
        case "ended": finish(completed: true)
        default: break
        }
        model.updateCoordinationPlayback(position: target, duration: player.duration, playing: room.state == "playing")
    }
    private func playNextEpisode() {
        guard !finished, presentation.nextEpisode != nil else { return }
        finished = true
        player.pause()
        model.playNextMinimizedEpisode(
            position: Int(player.position),
            duration: max(Int(player.duration), Int(player.position))
        )
    }


    private func restore() {
        guard !finished else { return }
        handoff = true
        player.pause()
        model.resumeMinimizedPlayback(position: Int(player.position), duration: max(Int(player.duration), Int(player.position)))
    }

    private func finish(completed: Bool) {
        guard !finished else { return }
        finished = true
        player.pause()
        model.minimizedPlaybackFinished(position: Int(player.position), duration: max(Int(player.duration), Int(player.position)), completed: completed)
    }
}

private func activatePlaybackAudioSession() {
#if canImport(UIKit) && !os(macOS)
    let session = AVAudioSession.sharedInstance()
    try? session.setCategory(.playback, mode: .moviePlayback)
    try? session.setActive(true)
#endif
}


#if canImport(UIKit)
private struct RivuneNativePlayer: UIViewControllerRepresentable {
    let player: AVPlayer
    let aspect: RivuneVideoAspect
    let frameRateMatching: RivuneFrameRatePreference
    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let controller = AVPlayerViewController()
        controller.player = player
        controller.showsPlaybackControls = false
        return controller
    }
    func updateUIViewController(_ controller: AVPlayerViewController, context: Context) {
        controller.player = player
        controller.videoGravity = aspect == .fit ? .resizeAspect : .resizeAspectFill
#if os(tvOS) || os(visionOS)
        controller.appliesPreferredDisplayCriteriaAutomatically = frameRateMatching != .disabled
#endif
    }
}

#if os(iOS) || os(visionOS)
struct RivuneExternalPlaybackSheet: UIViewControllerRepresentable {
    let url: URL
    func makeUIViewController(context: Context) -> UIActivityViewController { UIActivityViewController(activityItems: [url], applicationActivities: nil) }
    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}
#endif
#elseif canImport(AppKit)
private struct RivuneExternalApplicationPicker: View {
    let url: URL
    let cancel: () -> Void
    let opened: () -> Void
    @State private var errorMessage: String?

    private var applications: [RivuneExternalApplication] {
        RivuneExternalApplication.discover(for: url)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(alignment: .top, spacing: 14) {
                Image(systemName: "play.rectangle.on.rectangle")
                    .font(.title2)
                    .frame(width: 36, height: 36)
                VStack(alignment: .leading, spacing: 4) {
                    Text("Open in a player").font(.title2.bold())
                    Text("Choose which installed video app should open this stream.")
                        .foregroundStyle(.secondary)
                }
            }

            if applications.isEmpty {
                VStack(spacing: 10) {
                    Image(systemName: "play.slash").font(.largeTitle)
                    Text("No video player found").font(.headline)
                    Text("Choose another application installed on this Mac.")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, minHeight: 150)
            } else {
                ScrollView {
                    LazyVStack(spacing: 8) {
                        ForEach(applications) { application in
                            Button { open(url, with: application) } label: {
                                HStack(spacing: 12) {
                                    Image(nsImage: application.icon)
                                        .resizable()
                                        .frame(width: 34, height: 34)
                                    Text(application.name)
                                        .font(.headline)
                                    Spacer()
                                    Image(systemName: "arrow.up.forward.app")
                                        .foregroundStyle(.secondary)
                                }
                                .padding(.horizontal, 12)
                                .frame(maxWidth: .infinity, minHeight: 52)
                            }
                            .buttonStyle(.plain)
                            .contentShape(Rectangle())
                            .accessibilityLabel(rivuneLocalizedFormat("Open in %@", application.name))
                        }
                    }
                }
                .frame(maxHeight: 320)
            }

            Divider()
            HStack {
                Button("Cancel", role: .cancel, action: cancel)
                    .keyboardShortcut(.cancelAction)
                    .rivuneGlassButton()
                Spacer()
                Button(action: chooseApplication) {
                    Label("Choose Application…", systemImage: "folder.badge.plus")
                }
                .rivuneGlassButton()
            }
        }
        .padding(24)
        .frame(minWidth: 430, idealWidth: 500, minHeight: 260)
        .alert("Could not open player", isPresented: Binding(
            get: { errorMessage != nil },
            set: { if !$0 { errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }

    private func chooseApplication() {
        let panel = NSOpenPanel()
        panel.title = rivuneLocalized("Choose a video player")
        panel.prompt = rivuneLocalized("Open")
        panel.message = rivuneLocalized("Select an application to open this stream.")
        panel.allowedContentTypes = [.applicationBundle]
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.begin { response in
            guard response == .OK, let applicationURL = panel.url else { return }
            let application = RivuneExternalApplication(url: applicationURL)
            guard application.bundleIdentifier != Bundle.main.bundleIdentifier else {
                errorMessage = rivuneLocalized("Choose an application other than Rivune.")
                return
            }
            open(url, with: application)
        }
    }

    private func open(_ streamURL: URL, with application: RivuneExternalApplication) {
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        NSWorkspace.shared.open(
            [streamURL],
            withApplicationAt: application.url,
            configuration: configuration
        ) { _, error in
            DispatchQueue.main.async {
                if let error {
                    errorMessage = error.localizedDescription
                } else {
                    opened()
                }
            }
        }
    }
}

struct RivuneExternalApplication: Identifiable {
    private static let knownBundleIDs = [
        "com.colliderli.iina",
        "org.videolan.vlc",
        "io.mpv",
        "com.firecore.infuse",
        "com.firecore.infuse.mac",
        "com.apple.QuickTimePlayerX",
        "com.movist.Movist",
        "com.movist.MovistPro",
    ]
    private static let browserBundleIDs: Set<String> = [
        "com.apple.Safari", "com.google.Chrome", "org.mozilla.firefox",
        "com.microsoft.edgemac", "com.brave.Browser", "company.thebrowser.Browser",
        "com.operasoftware.Opera", "com.vivaldi.Vivaldi",
    ]
    private static let videoNameFragments = ["iina", "vlc", "mpv", "infuse", "quicktime", "movist", "elmedia", "optimus", "mplayer", "plex"]
    private static let knownApplicationNames = ["IINA.app", "VLC.app", "mpv.app", "Infuse.app", "Movist.app", "Movist Pro.app", "Elmedia Player.app"]

    let url: URL
    let name: String
    let bundleIdentifier: String?
    let icon: NSImage

    var id: String { url.standardizedFileURL.path }

    init(url: URL) {
        self.url = url
        let bundle = Bundle(url: url)
        bundleIdentifier = bundle?.bundleIdentifier
        name = (bundle?.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String)?.nilIfEmpty
            ?? (bundle?.object(forInfoDictionaryKey: "CFBundleName") as? String)?.nilIfEmpty
            ?? url.deletingPathExtension().lastPathComponent
        icon = NSWorkspace.shared.icon(forFile: url.path)
    }

    static func discover(for streamURL: URL) -> [Self] {
        let workspace = NSWorkspace.shared
        var candidates = knownBundleIDs.compactMap { workspace.urlForApplication(withBundleIdentifier: $0) }
        let applicationDirectories = FileManager.default.urls(for: .applicationDirectory, in: .allDomainsMask)
        for directory in applicationDirectories {
            candidates.append(contentsOf: knownApplicationNames.compactMap { name in
                let candidate = directory.appendingPathComponent(name, isDirectory: true)
                return FileManager.default.fileExists(atPath: candidate.path) ? candidate : nil
            })
        }
        candidates.append(contentsOf: workspace.urlsForApplications(toOpen: streamURL))
        return rankedCandidates(candidates)
    }

    static func rankedCandidates(
        _ candidates: [URL],
        mainBundleURL: URL = Bundle.main.bundleURL,
        mainBundleIdentifier: String? = Bundle.main.bundleIdentifier
    ) -> [Self] {
        var seen = Set<String>()
        return candidates.compactMap { applicationURL -> Self? in
            let application = Self(url: applicationURL)
            guard seen.insert(application.id).inserted,
                  application.bundleIdentifier != mainBundleIdentifier,
                  application.url.standardizedFileURL != mainBundleURL.standardizedFileURL,
                  !browserBundleIDs.contains(application.bundleIdentifier ?? "") else { return nil }
            let normalizedName = application.name.lowercased()
            let recognized = application.bundleIdentifier.map { knownBundleIDs.contains($0) } == true
                || videoNameFragments.contains(where: normalizedName.contains)
            return recognized ? application : nil
        }
        .sorted { lhs, rhs in
            let leftPriority = lhs.bundleIdentifier.flatMap { knownBundleIDs.firstIndex(of: $0) } ?? knownBundleIDs.count
            let rightPriority = rhs.bundleIdentifier.flatMap { knownBundleIDs.firstIndex(of: $0) } ?? knownBundleIDs.count
            return leftPriority == rightPriority
                ? lhs.name.localizedCaseInsensitiveCompare(rhs.name) == .orderedAscending
                : leftPriority < rightPriority
        }
    }
}

private struct RivuneNativePlayer: NSViewRepresentable {
    let player: AVPlayer
    let aspect: RivuneVideoAspect
    let frameRateMatching: RivuneFrameRatePreference
    func makeNSView(context: Context) -> AVPlayerView { let view = AVPlayerView(); view.controlsStyle = .none; return view }
    func updateNSView(_ view: AVPlayerView, context: Context) { view.player = player; view.videoGravity = aspect == .fit ? .resizeAspect : .resizeAspectFill }
}
#endif

extension View {
    @ViewBuilder
    func mediaPlayerPresentation<Content: View>(
        item: Binding<RivunePlaybackPresentation?>,
        @ViewBuilder content: @escaping (RivunePlaybackPresentation) -> Content
    ) -> some View {
#if os(macOS)
        self
#else
        fullScreenCover(item: item, content: content)
#endif
    }
}

private extension String { var nilIfEmpty: String? { isEmpty ? nil : self } }
