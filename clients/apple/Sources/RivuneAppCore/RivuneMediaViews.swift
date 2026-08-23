import AVFoundation
import AVKit
import Combine
import SwiftUI
#if canImport(AppKit)
import AppKit
#endif
import RivuneAPI

struct RivuneMediaDetailView: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var showJoinRoom = false
    @State private var roomCode = ""

    var body: some View {
        RivuneSingleColumnNavigation {
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
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(rivuneLocalized(model.canNavigateBackFromMedia ? "Back" : "Close")) {
                        if model.selectedSeason != nil { model.closeSeason() }
                        else if model.canNavigateBackFromMedia { model.closeMedia() }
                        else { model.closeMedia(); dismiss() }
                    }
                }
            }
        }
        .preferredColorScheme(.dark)
        .sheet(isPresented: Binding(
            get: { model.showPlaybackSources },
            set: { if !$0 { model.closePlaybackSources() } }
        )) { RivunePlaybackSourcesView(model: model) }
        .mediaPlayerPresentation(item: Binding(
            get: { model.playbackPresentation },
            set: { _ in }
        )) { presentation in
            RivuneInternalPlayerView(presentation: presentation, model: model)
        }
#if os(iOS) || os(visionOS)
        .sheet(isPresented: Binding(
            get: { model.externalPlaybackURL != nil },
            set: { if !$0 { model.clearExternalPlaybackURL() } }
        )) {
            if let url = model.externalPlaybackURL { RivuneExternalPlaybackSheet(url: url) }
        }
#elseif os(macOS)
        .onChange(of: model.externalPlaybackURL) { url in
            if let url { NSWorkspace.shared.open(url); model.clearExternalPlaybackURL() }
        }
#endif
    }

    private func detailContent(_ detail: RivuneMediaDetail) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                hero(detail)
                actionRow(detail)
                if model.playbackCoordinationAvailable { coordinationControls }
                if let tagline = tagline(detail)?.nilIfEmpty {
                    Text(tagline).font(.headline).foregroundStyle(Color.white.opacity(0.72))
                }
                if let overview = overview(detail)?.nilIfEmpty {
                    Text(overview)
                        .foregroundStyle(Color.white.opacity(0.82))
                        .fixedSize(horizontal: false, vertical: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                genres(detail)
                if let series = detail.series { seasons(series) }
                cast(detail.movie?.cast ?? detail.series?.cast ?? detail.parentSeries?.cast ?? [])
                if let failure = model.mediaFailure {
                    Label(rivuneLocalized(failure.localizedDescription), systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                }
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 16)
            .frame(maxWidth: 1100, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .center)
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
                .buttonStyle(.bordered)
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
                    Button("Leave", action: model.leavePlaybackRoom).buttonStyle(.bordered)
                } else {
                    Button { model.createPlaybackRoom() } label: { Label("Start watch room", systemImage: "person.2.wave.2") }.buttonStyle(.bordered)
                    Button("Join room") { showJoinRoom = true }.buttonStyle(.bordered)
                }
            }
        }
        .alert("Join watch room", isPresented: $showJoinRoom) {
            TextField("Room code", text: $roomCode)
            Button("Join") { model.joinPlaybackRoom(code: roomCode); roomCode = "" }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func hero(_ detail: RivuneMediaDetail) -> some View {
        let background = detail.movie?.backdropUrl ?? detail.series?.backdropUrl ?? detail.episode?.backdropUrl ?? detail.target.backgroundUrl
        let poster = detail.movie?.posterUrl ?? detail.series?.posterUrl ?? detail.episode?.stillUrl ?? detail.target.posterUrl
        return ZStack(alignment: .bottomLeading) {
            AsyncImage(url: background.flatMap(model.resolvedResourceURL)) { phase in
                if let image = phase.image { image.resizable().scaledToFill() }
                else { Color.white.opacity(0.08) }
            }
            LinearGradient(colors: [.clear, .black.opacity(0.98)], startPoint: .center, endPoint: .bottom)
            HStack(alignment: .bottom, spacing: 18) {
                AsyncImage(url: poster.flatMap(model.resolvedResourceURL)) { phase in
                    if let image = phase.image { image.resizable().scaledToFill() }
                    else { Color.white.opacity(0.08) }
                }
                .frame(width: 104, height: detail.target.mediaType == "episode" ? 78 : 156)
                .clipShape(RoundedRectangle(cornerRadius: 13, style: .continuous))
                VStack(alignment: .leading, spacing: 8) {
                    Text(displayTitle(detail)).font(.largeTitle.bold()).lineLimit(3)
                    if let subtitle = releaseLine(detail) {
                        Text(subtitle).font(.subheadline.weight(.medium)).foregroundStyle(Color.white.opacity(0.76)).lineLimit(3)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(18)
        }
        .aspectRatio(16 / 8, contentMode: .fit)
        .frame(maxWidth: .infinity)
        .clipShape(RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    private func actionRow(_ detail: RivuneMediaDetail) -> some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 150, maximum: 240), spacing: 10)], alignment: .leading, spacing: 10) {
            if detail.target.mediaType != "series" {
                Button(action: model.loadPlaybackSources) {
                    Label(rivuneLocalized(detail.progress?.positionSeconds ?? 0 > 0 ? "Resume" : "Play"), systemImage: "play.fill")
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.buttonStyle(.borderedProminent)
            }
            if detail.target.mediaType != "episode" {
                Button(action: model.toggleLibrary) {
                    Label(rivuneLocalized(detail.inLibrary ? "In library" : "Add to library"), systemImage: detail.inLibrary ? "checkmark" : "plus")
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.buttonStyle(.bordered)
            }
            if detail.target.mediaType != "series" {
                Button(action: model.toggleWatched) {
                    Label(rivuneLocalized(detail.progress?.completed == true ? "Mark as unwatched" : "Mark as watched"), systemImage: detail.progress?.completed == true ? "eye.slash" : "checkmark.circle")
                        .lineLimit(1)
                        .minimumScaleFactor(0.75)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.buttonStyle(.bordered)
            }
            if let trailer = detail.trailers.first, let url = trailerURL(trailer) {
                Button { openURL(url) } label: {
                    Label("Trailer", systemImage: "play.rectangle")
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, minHeight: 22, alignment: .center)
                }.buttonStyle(.bordered)
            }
            if model.mediaActionLoading || model.mediaLoading { ProgressView() }
        }
        .disabled(model.mediaActionLoading)
    }

    @ViewBuilder private func genres(_ detail: RivuneMediaDetail) -> some View {
        let values = detail.movie?.genres ?? detail.series?.genres ?? []
        if !values.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack { ForEach(values, id: \.id) { Text($0.name).padding(.horizontal, 12).padding(.vertical, 7).background(Color.white.opacity(0.10), in: Capsule()) } }
            }
        }
    }

    @ViewBuilder private func seasons(_ series: Series) -> some View {
        if !series.seasons.isEmpty {
            VStack(alignment: .leading, spacing: 14) {
                Text("Seasons").font(.title2.bold())
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(spacing: 14) {
                        ForEach(series.seasons) { season in
                            Button { model.openSeason(season) } label: {
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
                            }.buttonStyle(.plain)
                        }
                    }
                }
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
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text(season.name).font(.largeTitle.bold())
                HStack(spacing: 10) {
                    Button(action: model.toggleWatched) {
                        let watched = season.episodes.allSatisfy { model.episodeProgress[$0.id]?.completed == true }
                        Label(rivuneLocalized(watched ? "Mark as unwatched" : "Mark as watched"), systemImage: watched ? "eye.slash" : "checkmark.circle")
                            .lineLimit(1)
                            .minimumScaleFactor(0.8)
                            .frame(minHeight: 22, alignment: .center)
                    }.buttonStyle(.borderedProminent)
                    if let trailer = model.seasonTrailers.first, let url = trailerURL(trailer) {
                        Button { openURL(url) } label: { Label("Trailer", systemImage: "play.rectangle") }.buttonStyle(.bordered)
                    }
                    if model.mediaActionLoading { ProgressView() }
                }
                if !season.overview.isEmpty { Text(season.overview).foregroundStyle(Color.white.opacity(0.78)).frame(maxWidth: .infinity, alignment: .leading) }
                ForEach(season.episodes) { episode in
                    Button { model.openEpisode(episode) } label: {
                        VStack(alignment: .leading, spacing: 0) {
                            HStack(spacing: 16) {
                                AsyncImage(url: (episode.stillUrl ?? episode.backdropUrl).flatMap(model.resolvedResourceURL)) { phase in
                                    if let image = phase.image { image.resizable().scaledToFill() }
                                    else { Color.white.opacity(0.08) }
                                }
                                .frame(width: 150, height: 84).clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                                VStack(alignment: .leading, spacing: 6) {
                                    Text(rivuneLocalizedFormat("%d. %@", episode.episodeNumber, episode.name)).font(.headline).lineLimit(2)
                                    if let runtime = episode.runtimeMinutes { Text(rivuneLocalizedFormat("%d min", runtime)).font(.caption).foregroundStyle(Color.white.opacity(0.64)) }
                                    Text(episode.overview).font(.caption).foregroundStyle(Color.white.opacity(0.72)).lineLimit(2)
                                }
                                Spacer()
                                Image(systemName: model.episodeProgress[episode.id]?.completed == true ? "checkmark.circle.fill" : "chevron.right")
                            }.padding(12)
                            if let progress = model.episodeProgress[episode.id], progress.durationSeconds > 0, !progress.completed {
                                ProgressView(value: Double(progress.positionSeconds), total: Double(progress.durationSeconds)).tint(.accentColor)
                            }
                        }
                        .background(Color.white.opacity(0.07), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                    }.buttonStyle(.plain)
                }
                if let failure = model.mediaFailure { Text(rivuneLocalized(failure.localizedDescription)).foregroundStyle(.red) }
            }
            .padding(20)
            .frame(maxWidth: 1000, alignment: .leading)
            .frame(maxWidth: .infinity)
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

struct RivunePlaybackSourcesView: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        RivuneSingleColumnNavigation {
            List {
                if model.mediaLoading && model.playbackSources.isEmpty { ProgressView("Finding streams…") }
                ForEach(model.playbackSources) { source in
                    VStack(alignment: .leading, spacing: 10) {
                        Text(source.name).font(.headline)
                        if let description = source.description { Text(description).font(.caption).foregroundStyle(.secondary) }
                        Text([source.protocol.uppercased(), source.container?.uppercased(), source.mode?.rawValue.replacingOccurrences(of: "_", with: " ").capitalized].compactMap { $0 }.joined(separator: " · ")).font(.caption2).foregroundStyle(.secondary)
                        HStack {
                            if model.preferredPlayer != .external {
                                Button { model.play(source, externally: false); dismiss() } label: { Label("Play in Rivune", systemImage: "play.fill") }.buttonStyle(.borderedProminent)
                            }
#if !os(tvOS)
                            if model.preferredPlayer != .rivune {
                                Button { model.play(source, externally: true); dismiss() } label: { Label("Open in app", systemImage: "square.and.arrow.up") }.buttonStyle(.bordered)
                            }
#endif
                            if !["hls", "dash"].contains(source.protocol.lowercased()) {
                                Button { model.download(source) } label: {
                                    Label(rivuneLocalized(model.offlineDownloadActive ? "Downloading…" : "Download"), systemImage: "arrow.down.circle")
                                }
                                .disabled(model.offlineDownloadActive)
                                .buttonStyle(.bordered)
                            }
                        }
                    }.padding(.vertical, 8)
                }
                if !model.mediaLoading && model.playbackSources.isEmpty { Text("No compatible stream was returned by your server.").foregroundStyle(.secondary) }
            }
            .navigationTitle("Choose a stream")
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { model.closePlaybackSources(); dismiss() } } }
        }
    }
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
                LinearGradient(colors: [.black.opacity(0.72), .clear, .black.opacity(0.84)], startPoint: .top, endPoint: .bottom).ignoresSafeArea()
                VStack(spacing: 0) {
                    HStack(spacing: 16) {
                        Button { finish(completed: false) } label: {
                            Image(systemName: "xmark").font(.headline).frame(width: 44, height: 44).background(.ultraThinMaterial, in: Circle())
                        }
                        .accessibilityLabel("Close player")
                        Text(activePresentation.title).font(.headline).lineLimit(1)
                        Spacer()
                        Button { minimize() } label: {
                            Image(systemName: "pip.enter").font(.headline).frame(width: 44, height: 44).background(.ultraThinMaterial, in: Circle())
                        }
                        .accessibilityLabel("Mini player")
                    }
                    .padding(.horizontal, 22).padding(.top, 14)
                    Spacer()
                    HStack(spacing: 38) {
                        Button { seek(by: -10) } label: { Image(systemName: "gobackward.10").font(.system(size: 30, weight: .semibold)) }.accessibilityLabel(rivuneLocalized("Back 10 seconds"))
                        Button { togglePlayback() } label: {
                            Image(systemName: playing ? "pause.fill" : "play.fill")
                                .font(.system(size: 34, weight: .bold)).frame(width: 68, height: 68).background(.ultraThinMaterial, in: Circle())
                        }
                        .accessibilityLabel(rivuneLocalized(playing ? "Pause" : "Play"))
                        Button { seek(by: 10) } label: { Image(systemName: "goforward.10").font(.system(size: 30, weight: .semibold)) }.accessibilityLabel(rivuneLocalized("Forward 10 seconds"))
                    }
                    .buttonStyle(.plain)
                    Spacer()
                    HStack(spacing: 12) {
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
                                    if activePresentation.selectedAudioTrack == track.index { Label(trackLabel(track), systemImage: "checkmark") }
                                    else { Text(trackLabel(track)) }
                                }
                            }
                            ForEach(Array(audioOptions.enumerated()), id: \.offset) { _, option in
                                Button(option.displayName) { select(option, in: audioGroup) }
                            }
                        } label: {
                            Image(systemName: "speaker.wave.2").frame(minWidth: 24)
                                .accessibilityLabel(rivuneLocalized("Audio"))
                        }

                        Menu {
                            Button {
                                changeServerOptions(audioTrack: activePresentation.selectedAudioTrack, subtitleId: "none")
                                select(nil, in: subtitleGroup)
                            } label: {
                                if activePresentation.selectedSubtitleId == nil || activePresentation.selectedSubtitleId == "none" { Label("Off", systemImage: "checkmark") }
                                else { Text("Off") }
                            }
                            ForEach(activePresentation.subtitles) { subtitle in
                                Button {
                                    changeServerOptions(audioTrack: activePresentation.selectedAudioTrack, subtitleId: subtitle.id)
                                } label: {
                                    if activePresentation.selectedSubtitleId == subtitle.id { Label(subtitleLabel(subtitle), systemImage: "checkmark") }
                                    else { Text(subtitleLabel(subtitle)) }
                                }
                            }
                            ForEach(Array(subtitleOptions.enumerated()), id: \.offset) { _, option in
                                Button(option.displayName) { select(option, in: subtitleGroup) }
                            }
                        } label: {
                            Image(systemName: "captions.bubble").frame(minWidth: 24)
                                .accessibilityLabel(rivuneLocalized("Subtitles"))
                        }

                        Menu {
                            ForEach(RivuneVideoAspect.allCases) { aspect in
                                Button {
                                    sessionAspect = aspect
                                    scheduleControlsHide()
                                } label: {
                                    if sessionAspect == aspect { Label(rivuneLocalized(aspect.displayName), systemImage: "checkmark") }
                                    else { Text(rivuneLocalized(aspect.displayName)) }
                                }
                            }
                        } label: {
                            Label(rivuneLocalized(sessionAspect.displayName), systemImage: "aspectratio")
                        }

                        Menu {
                            ForEach([0.5, 0.75, 1.0, 1.25, 1.5, 2.0], id: \.self) { speed in
                                Button(speed == 1 ? rivuneLocalized("Normal") : "\(speed.formatted())×") { setSpeed(speed) }
                            }
                        } label: {
                            Label(playbackSpeed == 1 ? "1×" : "\(playbackSpeed.formatted())×", systemImage: "speedometer")
                        }
#endif
                    }
                    .disabled(model.playbackOptionLoading)
                    .font(.subheadline.weight(.semibold))
                    .buttonStyle(.bordered)
                    .padding(.horizontal, 28).padding(.bottom, 14)
                    VStack(spacing: 8) {
#if os(tvOS)
                        GeometryReader { proxy in
                            ZStack(alignment: .leading) {
                                Capsule().fill(Color.white.opacity(0.28))
                                Capsule().fill(Color.accentColor).frame(width: proxy.size.width * min(max(position / max(duration, 1), 0), 1))
                            }
                        }.frame(height: 8)
#else
                        Slider(
                            value: Binding(get: { position }, set: { position = $0 }),
                            in: 0...max(duration, 1),
                            onEditingChanged: { editing in
                                scrubbing = editing
                                if !editing {
                                    player.seek(to: CMTime(seconds: position, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
                                    scheduleControlsHide()
                                }
                            }
                        )
#endif
                        HStack {
                            Text(formatTime(position))
                            Spacer()
                            Text("−\(formatTime(max(duration - position, 0)))  /  \(formatTime(duration))")
                        }
                        .font(.caption.monospacedDigit()).foregroundStyle(Color.white.opacity(0.82))
                    }
                    .padding(.horizontal, 28).padding(.bottom, 22)
                }
                .transition(.opacity)
            }
            if let marker = activeMarker {
                Button { skip(marker) } label: {
                    Label(skipTitle(marker.type), systemImage: "forward.fill")
                        .font(.headline).padding(.horizontal, 18).padding(.vertical, 12)
                        .background(.ultraThinMaterial, in: Capsule())
                }
                .buttonStyle(.plain)
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
            finish(completed: true)
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
                LinearGradient(colors: [.black.opacity(0.72), .clear, .black.opacity(0.84)], startPoint: .top, endPoint: .bottom).ignoresSafeArea()
                VStack(spacing: 0) {
                    HStack(spacing: 16) {
                        Button { finish(completed: false) } label: {
                            Image(systemName: "xmark").font(.headline).frame(width: 44, height: 44).background(.ultraThinMaterial, in: Circle())
                        }
                        .accessibilityLabel("Close player")
                        Text(activePresentation.title).font(.headline).lineLimit(1)
                        Text("MPV").font(.caption2.weight(.bold)).padding(.horizontal, 8).padding(.vertical, 4).background(.ultraThinMaterial, in: Capsule())
                        Spacer()
                        Button { minimize() } label: {
                            Image(systemName: "pip.enter").font(.headline).frame(width: 44, height: 44).background(.ultraThinMaterial, in: Circle())
                        }
                        .accessibilityLabel("Mini player")
                    }
                    .padding(.horizontal, 22).padding(.top, 14)
                    Spacer()
                    HStack(spacing: 38) {
                        Button { seek(by: -10) } label: { Image(systemName: "gobackward.10").font(.system(size: 30, weight: .semibold)) }.accessibilityLabel(rivuneLocalized("Back 10 seconds"))
                        Button { togglePlayback() } label: {
                            Image(systemName: player.playing ? "pause.fill" : "play.fill")
                                .font(.system(size: 34, weight: .bold)).frame(width: 68, height: 68).background(.ultraThinMaterial, in: Circle())
                        }
                        .accessibilityLabel(rivuneLocalized(player.playing ? "Pause" : "Play"))
                        Button { seek(by: 10) } label: { Image(systemName: "goforward.10").font(.system(size: 30, weight: .semibold)) }.accessibilityLabel(rivuneLocalized("Forward 10 seconds"))
                    }
                    .buttonStyle(.plain)
                    Spacer()
                    HStack(spacing: 12) {
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
                                    if selectedAudioTrack == track.index { Label(trackLabel(track), systemImage: "checkmark") }
                                    else { Text(trackLabel(track)) }
                                }
                            }
                        } label: { Image(systemName: "speaker.wave.2").frame(minWidth: 24).accessibilityLabel(rivuneLocalized("Audio")) }

                        Menu {
                            Button { selectSubtitle(nil) } label: {
                                if selectedSubtitleId == nil || selectedSubtitleId == "none" { Label("Off", systemImage: "checkmark") }
                                else { Text("Off") }
                            }
                            ForEach(activePresentation.subtitles) { subtitle in
                                Button { selectSubtitle(subtitle) } label: {
                                    if selectedSubtitleId == subtitle.id { Label(subtitleLabel(subtitle), systemImage: "checkmark") }
                                    else { Text(subtitleLabel(subtitle)) }
                                }
                            }
                        } label: { Image(systemName: "captions.bubble").frame(minWidth: 24).accessibilityLabel(rivuneLocalized("Subtitles")) }

                        Menu {
                            ForEach(RivuneVideoAspect.allCases) { aspect in
                                Button { sessionAspect = aspect; scheduleControlsHide() } label: {
                                    if sessionAspect == aspect { Label(rivuneLocalized(aspect.displayName), systemImage: "checkmark") }
                                    else { Text(rivuneLocalized(aspect.displayName)) }
                                }
                            }
                        } label: { Label(rivuneLocalized(sessionAspect.displayName), systemImage: "aspectratio") }

                        Menu {
                            ForEach([0.5, 0.75, 1.0, 1.25, 1.5, 2.0], id: \.self) { speed in
                                Button(speed == 1 ? rivuneLocalized("Normal") : "\(speed.formatted())×") { setSpeed(speed) }
                            }
                        } label: { Label(playbackSpeed == 1 ? "1×" : "\(playbackSpeed.formatted())×", systemImage: "speedometer") }
#endif
                    }
                    .font(.subheadline.weight(.semibold)).buttonStyle(.bordered)
                    .padding(.horizontal, 28).padding(.bottom, 14)
                    VStack(spacing: 8) {
#if os(tvOS)
                        GeometryReader { proxy in
                            ZStack(alignment: .leading) {
                                Capsule().fill(Color.white.opacity(0.28))
                                Capsule().fill(Color.accentColor).frame(width: proxy.size.width * min(max(player.position / max(player.duration, 1), 0), 1))
                            }
                        }.frame(height: 8)
#else
                        Slider(value: Binding(
                            get: { player.position },
                            set: { if !$0.isNaN { player.seek(to: $0) } }
                        ), in: 0...max(player.duration, 1), onEditingChanged: { editing in
                            scrubbing = editing
                            if !editing { scheduleControlsHide() }
                        })
#endif
                        HStack {
                            Text(formatTime(player.position))
                            Spacer()
                            Text("−\(formatTime(max(player.duration - player.position, 0)))  /  \(formatTime(player.duration))")
                        }
                        .font(.caption.monospacedDigit()).foregroundStyle(Color.white.opacity(0.82))
                    }
                    .padding(.horizontal, 28).padding(.bottom, 22)
                }
                .transition(.opacity)
            }
            if player.buffering && failureMessage == nil {
                ProgressView("Buffering…").padding(18).background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 14))
            }
            if let marker = activeMarker {
                Button { skip(marker) } label: {
                    Label(skipTitle(marker.type), systemImage: "forward.fill")
                        .font(.headline).padding(.horizontal, 18).padding(.vertical, 12).background(.ultraThinMaterial, in: Capsule())
                }
                .buttonStyle(.plain).padding(24).frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
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
        .onReceive(player.$ended.filter { $0 }) { _ in finish(completed: true) }
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
                Button("Close", action: close).buttonStyle(.bordered)
                Button("Try again", action: retry).buttonStyle(.borderedProminent)
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
                    Button { finish(completed: false) } label: { Image(systemName: "xmark") }
                        .accessibilityLabel("Close player")
                }
                Spacer()
                HStack(spacing: 18) {
                    Button { togglePlayback() } label: { Image(systemName: playing ? "pause.fill" : "play.fill").font(.title2) }
                        .accessibilityLabel(rivuneLocalized(playing ? "Pause" : "Play"))
                    Spacer()
                    Button { restore() } label: { Image(systemName: "pip.exit").font(.title3) }
                        .accessibilityLabel(rivuneLocalized("Return to full player"))
                }
            }
            .buttonStyle(.plain)
            .padding(12)
            if let failureMessage {
                VStack(spacing: 8) {
                    Text("Playback failed").font(.caption.bold())
                    Text(rivuneLocalized(failureMessage)).font(.caption2).lineLimit(3).multilineTextAlignment(.center)
                    Button("Try again") { self.failureMessage = nil; loadAttempt += 1 }.buttonStyle(.borderedProminent)
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
            finish(completed: true)
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
                    Button { finish(completed: false) } label: { Image(systemName: "xmark") }
                        .accessibilityLabel("Close player")
                }
                Spacer()
                HStack(spacing: 18) {
                    Button { player.playing ? player.pause() : player.play() } label: { Image(systemName: player.playing ? "pause.fill" : "play.fill").font(.title2) }
                        .accessibilityLabel(rivuneLocalized(player.playing ? "Pause" : "Play"))
                    Spacer()
                    Button { restore() } label: { Image(systemName: "pip.exit").font(.title3) }.accessibilityLabel(rivuneLocalized("Return to full player"))
                }
            }
            .buttonStyle(.plain).padding(12)
            if let failureMessage {
                VStack(spacing: 8) {
                    Text("Playback failed").font(.caption.bold())
                    Text(rivuneLocalized(failureMessage)).font(.caption2).lineLimit(3).multilineTextAlignment(.center)
                    Button("Try again") { self.failureMessage = nil; loadAttempt += 1 }.buttonStyle(.borderedProminent)
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
        .onReceive(player.$ended.filter { $0 }) { _ in finish(completed: true) }
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
        sheet(item: item, content: content)
#else
        fullScreenCover(item: item, content: content)
#endif
    }
}

private extension String { var nilIfEmpty: String? { isEmpty ? nil : self } }
