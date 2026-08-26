#if os(iOS)
import SwiftUI
import RivuneAPI

struct RivuneIOSMediaDetailView: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var showJoinRoom = false
    @State private var roomCode = ""

    var body: some View {
        Group {
            if model.showPlaybackSources {
                RivuneIOSPlaybackSourcesView(model: model)
            } else {
                detailPresentation
            }
        }
        .preferredColorScheme(.dark)
        .mediaPlayerPresentation(item: Binding(
            get: { model.playbackPresentation },
            set: { _ in }
        )) { presentation in
            RivuneInternalPlayerView(presentation: presentation, model: model)
        }
        .sheet(isPresented: Binding(
            get: { model.externalPlaybackURL != nil },
            set: { if !$0 { model.clearExternalPlaybackURL() } }
        )) {
            if let url = model.externalPlaybackURL {
                RivuneExternalPlaybackSheet(url: url)
            }
        }
    }

    private var detailPresentation: some View {
        ZStack(alignment: .topLeading) {
            RivuneIOSCanvas()
            if let season = model.selectedSeason {
                seasonView(season)
            } else if let detail = model.mediaDetail {
                detailView(detail)
            } else if model.mediaLoading {
                RivuneIOSStatusView(state: .loading("Loading details…"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let failure = model.mediaFailure {
                RivuneIOSStatusView(state: .failure(failure))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                RivuneIOSStatusView(state: .empty(icon: "film", title: "This title could not be loaded", message: nil))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Button(action: close) {
                Image(systemName: model.selectedSeason != nil || model.canNavigateBackFromMedia ? "chevron.left" : "xmark")
                    .font(.headline.bold())
            }
            .rivuneIOSIconButton()
            .padding(.leading, 18)
            .padding(.top, 12)
            .accessibilityLabel(rivuneLocalized(model.selectedSeason != nil || model.canNavigateBackFromMedia ? "Back" : "Close"))
            .zIndex(10)
        }
    }

    private func detailView(_ detail: RivuneMediaDetail) -> some View {
        GeometryReader { proxy in
            let inset = RivuneIOSTheme.pageInset(for: proxy.size.width)
            let contentWidth = max(min(proxy.size.width, 1080) - inset * 2, 0)
            ScrollView {
                VStack(alignment: .leading, spacing: 26) {
                    detailHero(detail, viewport: proxy.size.width)
                    actionRow(detail)
                    if model.playbackCoordinationAvailable { coordinationControls }
                    if let overview = rivuneIOSOverview(detail), !overview.isEmpty {
                        Text(overview)
                            .font(.body)
                            .foregroundStyle(RivuneIOSTheme.secondaryText)
                            .fixedSize(horizontal: false, vertical: true)
                            .frame(maxWidth: 820, alignment: .leading)
                    }
                    if let series = detail.series { seasons(series) }
                    cast(detail.movie?.cast ?? detail.series?.cast ?? detail.parentSeries?.cast ?? [])
                    if let failure = model.mediaFailure {
                        RivuneIOSErrorMessage(failure: failure)
                    }
                }
                .frame(width: contentWidth, alignment: .leading)
                .padding(.horizontal, inset)
                .padding(.bottom, 42)
                .frame(maxWidth: .infinity)
            }
        }
    }

    private func detailHero(_ detail: RivuneMediaDetail, viewport: CGFloat) -> some View {
        ZStack(alignment: .bottomLeading) {
            AsyncImage(url: rivuneIOSBackdropURL(detail, model: model)) { phase in
                if let image = phase.image { image.resizable().scaledToFill() }
                else { RivuneIOSTheme.raised }
            }
            .frame(maxWidth: .infinity)
            .frame(height: viewport < 700 ? 430 : 470)
            .clipped()
            LinearGradient(
                colors: [Color.black.opacity(0.06), Color.black.opacity(0.50), RivuneIOSTheme.canvas],
                startPoint: .top,
                endPoint: .bottom
            )
            .frame(height: viewport < 700 ? 430 : 470)

            Group {
                if viewport >= 720 {
                    HStack(alignment: .bottom, spacing: 24) {
                        poster(detail).frame(width: 170)
                        detailIdentity(detail)
                        Spacer(minLength: 0)
                    }
                } else {
                    detailIdentity(detail)
                }
            }
            .padding(.horizontal, RivuneIOSTheme.pageInset(for: viewport))
            .padding(.bottom, 22)
        }
        .frame(maxWidth: .infinity)
        .padding(.horizontal, -RivuneIOSTheme.pageInset(for: viewport))
    }

    private func poster(_ detail: RivuneMediaDetail) -> some View {
        RivuneIOSArtwork(
            url: (detail.target.posterUrl ?? detail.target.backgroundUrl).flatMap(model.resolvedResourceURL),
            aspectRatio: 2 / 3,
            fallbackSystemImage: ["series", "tv"].contains(detail.target.mediaType) ? "tv" : "film"
        )
    }

    private func detailIdentity(_ detail: RivuneMediaDetail) -> some View {
        VStack(alignment: .leading, spacing: 9) {
            Text(rivuneIOSDisplayTitle(detail))
                .font(.system(size: 38, weight: .bold))
                .foregroundStyle(RivuneIOSTheme.primaryText)
                .lineLimit(3)
                .minimumScaleFactor(0.78)
            if let tagline = rivuneIOSTagline(detail), !tagline.isEmpty {
                Text(tagline)
                    .font(.title3)
                    .foregroundStyle(Color.white.opacity(0.78))
                    .lineLimit(2)
            }
            if let metadata = rivuneIOSReleaseLine(detail), !metadata.isEmpty {
                Text(metadata)
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(Color.white.opacity(0.78))
                    .fixedSize(horizontal: false, vertical: true)
            }
            let genres = detail.movie?.genres.map(\.name) ?? detail.series?.genres.map(\.name) ?? []
            if !genres.isEmpty {
                Text(genres.joined(separator: " · "))
                    .font(.subheadline)
                    .foregroundStyle(Color.white.opacity(0.70))
                    .lineLimit(2)
            }
        }
        .frame(maxWidth: 760, alignment: .leading)
    }

    private func actionRow(_ detail: RivuneMediaDetail) -> some View {
        LazyVGrid(
            columns: [GridItem(.adaptive(minimum: 150, maximum: 260), spacing: 10)],
            alignment: .leading,
            spacing: 10
        ) {
            if detail.target.mediaType != "series" {
                Button(action: model.loadPlaybackSources) {
                    Label(detail.progress?.positionSeconds ?? 0 > 0 ? "Resume" : "Play", systemImage: "play.fill")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSPrimaryButton()
            }
            if detail.target.mediaType != "episode" {
                Button(action: model.toggleLibrary) {
                    Label(detail.inLibrary ? "In library" : "Add to library", systemImage: detail.inLibrary ? "checkmark" : "plus")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
            Button(action: model.addCurrentMediaToQueue) {
                Label("Add to queue", systemImage: "text.badge.plus").frame(maxWidth: .infinity)
            }
            .rivuneIOSSecondaryButton()
            Button(action: model.followCurrentMediaNotifications) {
                Label("Follow releases", systemImage: "bell.badge").frame(maxWidth: .infinity)
            }
            .rivuneIOSSecondaryButton()
            Button(action: model.toggleWatched) {
                let watched = detail.target.mediaType == "series"
                    ? model.seriesEpisodesWatched == true
                    : detail.progress?.completed == true
                Label(watched ? "Mark as unwatched" : "Mark as watched", systemImage: watched ? "eye.slash" : "checkmark.circle")
                    .frame(maxWidth: .infinity)
            }
            .rivuneIOSSecondaryButton()
            if let trailer = detail.trailers.first, let url = rivuneIOSTrailerURL(trailer) {
                Button { openURL(url) } label: {
                    Label("Trailer", systemImage: "play.rectangle")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
            if model.mediaActionLoading || model.mediaLoading {
                ProgressView().tint(RivuneIOSTheme.ember)
                    .frame(minHeight: 52)
            }
        }
        .disabled(model.mediaActionLoading)
    }

    private var coordinationControls: some View {
        VStack(alignment: .leading, spacing: 12) {
            RivuneIOSSectionHeader(title: "Watch together")
            if !model.playbackDevices.isEmpty {
                Menu {
                    ForEach(model.playbackDevices) { device in
                        Button("Play on \(device.name)") { model.handoffPlayback(to: device) }
                        Button("Play a copy on \(device.name)") { model.playCopy(to: device) }
                        Button("Play \(device.name)") { model.controlPlayback(on: device, command: .play) }
                        Button("Pause \(device.name)") { model.controlPlayback(on: device, command: .pause) }
                        Button("Match position on \(device.name)") { model.controlPlayback(on: device, command: .seek) }
                        Button("Stop \(device.name)", role: .destructive) { model.controlPlayback(on: device, command: .stop) }
                    }
                } label: {
                    Label("Play on another device", systemImage: "airplayvideo")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
            if let room = model.activePlaybackRoom {
                HStack(spacing: 10) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(room.joinCode.map { rivuneLocalizedFormat("Room %@", $0) } ?? rivuneLocalized("Watch room"))
                            .font(.headline.monospaced())
                            .foregroundStyle(RivuneIOSTheme.primaryText)
                        Text(rivuneLocalizedFormat("%d watching", room.members.count))
                            .font(.caption)
                            .foregroundStyle(RivuneIOSTheme.mutedText)
                    }
                    Spacer()
                    Button("Leave", action: model.leavePlaybackRoom)
                        .rivuneIOSSecondaryButton()
                }
                .rivuneIOSCard()
            } else {
                HStack(spacing: 10) {
                    Button { model.createPlaybackRoom() } label: {
                        Label("Start room", systemImage: "person.2.wave.2")
                            .frame(maxWidth: .infinity)
                    }
                    .rivuneIOSSecondaryButton()
                    Button("Join room") { showJoinRoom = true }
                        .rivuneIOSSecondaryButton()
                }
            }
        }
        .alert("Join watch room", isPresented: $showJoinRoom) {
            TextField("Room code", text: $roomCode)
            Button("Join") { model.joinPlaybackRoom(code: roomCode); roomCode = "" }
            Button("Cancel", role: .cancel) {}
        }
    }

    @ViewBuilder
    private func seasons(_ series: Series) -> some View {
        if !series.seasons.isEmpty {
            VStack(alignment: .leading, spacing: 14) {
                RivuneIOSSectionHeader(title: "Seasons")
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(alignment: .top, spacing: 14) {
                        ForEach(series.seasons) { season in
                            Button { model.openSeason(season) } label: {
                                VStack(alignment: .leading, spacing: 8) {
                                    RivuneIOSArtwork(
                                        url: season.posterUrl.flatMap(model.resolvedResourceURL),
                                        aspectRatio: 2 / 3,
                                        fallbackSystemImage: "tv"
                                    )
                                    .frame(width: 132)
                                    Text(season.name)
                                        .font(.subheadline.weight(.semibold))
                                        .foregroundStyle(RivuneIOSTheme.primaryText)
                                        .lineLimit(1)
                                        .frame(width: 132, alignment: .leading)
                                    Text(rivuneLocalizedFormat("%d episodes", season.episodeCount))
                                        .font(.caption)
                                        .foregroundStyle(RivuneIOSTheme.mutedText)
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func cast(_ members: [CastMember]) -> some View {
        if !members.isEmpty {
            VStack(alignment: .leading, spacing: 14) {
                RivuneIOSSectionHeader(title: "Cast")
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(alignment: .top, spacing: 14) {
                        ForEach(members) { member in
                            VStack(spacing: 8) {
                                AsyncImage(url: member.profileUrl.flatMap(model.resolvedResourceURL)) { phase in
                                    if let image = phase.image { image.resizable().scaledToFill() }
                                    else {
                                        ZStack {
                                            RivuneIOSTheme.raised
                                            Image(systemName: "person.fill")
                                                .foregroundStyle(RivuneIOSTheme.mutedText)
                                        }
                                    }
                                }
                                .frame(width: 84, height: 84)
                                .clipShape(Circle())
                                .overlay { Circle().stroke(RivuneIOSTheme.outline, lineWidth: 1) }
                                Text(member.name)
                                    .font(.caption.weight(.semibold))
                                    .foregroundStyle(RivuneIOSTheme.primaryText)
                                    .lineLimit(1)
                                if let character = member.character {
                                    Text(character)
                                        .font(.caption2)
                                        .foregroundStyle(RivuneIOSTheme.mutedText)
                                        .lineLimit(1)
                                }
                            }
                            .frame(width: 108)
                        }
                    }
                }
            }
        }
    }

    private func seasonView(_ season: Season) -> some View {
        GeometryReader { proxy in
            let inset = RivuneIOSTheme.pageInset(for: proxy.size.width)
            let contentWidth = max(min(proxy.size.width, 1100) - inset * 2, 0)
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    seasonHero(season, viewport: proxy.size.width)
                    seasonActions(season)
                    if !season.overview.isEmpty {
                        Text(season.overview)
                            .foregroundStyle(RivuneIOSTheme.secondaryText)
                            .fixedSize(horizontal: false, vertical: true)
                            .frame(maxWidth: 820, alignment: .leading)
                    }
                    RivuneIOSSectionHeader(title: "Episodes")
                    LazyVGrid(
                        columns: [GridItem(.adaptive(minimum: proxy.size.width < 700 ? 270 : 330, maximum: 440), spacing: 16)],
                        alignment: .leading,
                        spacing: 18
                    ) {
                        ForEach(season.episodes) { episode in
                            episodeCard(episode)
                        }
                    }
                    if let failure = model.mediaFailure {
                        RivuneIOSErrorMessage(failure: failure)
                    }
                }
                .frame(width: contentWidth, alignment: .leading)
                .padding(.horizontal, inset)
                .padding(.bottom, 42)
                .frame(maxWidth: .infinity)
            }
        }
    }

    private func seasonHero(_ season: Season, viewport: CGFloat) -> some View {
        ZStack(alignment: .bottomLeading) {
            AsyncImage(url: rivuneIOSSeasonBackdropURL(season, model: model)) { phase in
                if let image = phase.image { image.resizable().scaledToFill() }
                else { RivuneIOSTheme.raised }
            }
            .frame(maxWidth: .infinity)
            .frame(height: viewport < 700 ? 340 : 420)
            .clipped()
            LinearGradient(
                colors: [Color.black.opacity(0.10), Color.black.opacity(0.52), RivuneIOSTheme.canvas],
                startPoint: .top,
                endPoint: .bottom
            )
            VStack(alignment: .leading, spacing: 8) {
                if let seriesName = model.mediaDetail?.series?.name ?? model.mediaDetail?.parentSeries?.name {
                    Text(seriesName)
                        .font(.headline)
                        .foregroundStyle(Color.white.opacity(0.72))
                }
                Text(season.name)
                    .font(.system(size: 38, weight: .bold))
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                let metadata = [
                    season.airDate,
                    rivuneLocalizedFormat("%d episodes", season.episodes.count),
                    season.voteAverage > 0 ? String(format: "★ %.1f", season.voteAverage) : nil,
                ].compactMap { $0 }
                Text(metadata.joined(separator: " · "))
                    .font(.subheadline)
                    .foregroundStyle(Color.white.opacity(0.74))
            }
            .padding(.horizontal, RivuneIOSTheme.pageInset(for: viewport))
            .padding(.bottom, 22)
        }
        .frame(maxWidth: .infinity)
        .frame(height: viewport < 700 ? 340 : 420)
        .padding(.horizontal, -RivuneIOSTheme.pageInset(for: viewport))
    }

    private func seasonActions(_ season: Season) -> some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 150, maximum: 260), spacing: 10)], spacing: 10) {
            if let detail = model.mediaDetail, detail.target.mediaType != "episode" {
                Button(action: model.toggleLibrary) {
                    Label(detail.inLibrary ? "In library" : "Add to library", systemImage: detail.inLibrary ? "checkmark" : "plus")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
            Button(action: model.toggleWatched) {
                let watched = !season.episodes.isEmpty && season.episodes.allSatisfy { model.episodeProgress[$0.id]?.completed == true }
                Label(watched ? "Mark as unwatched" : "Mark as watched", systemImage: watched ? "eye.slash" : "checkmark.circle")
                    .frame(maxWidth: .infinity)
            }
            .rivuneIOSPrimaryButton()
            if let trailer = model.seasonTrailers.first, let url = rivuneIOSTrailerURL(trailer) {
                Button { openURL(url) } label: {
                    Label("Trailer", systemImage: "play.rectangle")
                        .frame(maxWidth: .infinity)
                }
                .rivuneIOSSecondaryButton()
            }
        }
        .disabled(model.mediaActionLoading)
    }

    private func episodeCard(_ episode: Episode) -> some View {
        Button { model.openEpisode(episode) } label: {
            VStack(alignment: .leading, spacing: 0) {
                ZStack(alignment: .bottom) {
                    RivuneIOSArtwork(
                        url: (episode.stillUrl ?? episode.backdropUrl).flatMap(model.resolvedResourceURL),
                        aspectRatio: 16 / 9,
                        fallbackSystemImage: "play.rectangle.fill",
                        cornerRadius: 0
                    )
                    if let progress = model.episodeProgress[episode.id], progress.durationSeconds > 0, !progress.completed {
                        GeometryReader { geometry in
                            let value = min(max(CGFloat(progress.positionSeconds) / CGFloat(progress.durationSeconds), 0), 1)
                            HStack(spacing: 0) {
                                RivuneIOSTheme.ember.frame(width: geometry.size.width * value)
                                Color.white.opacity(0.22)
                            }
                        }
                        .frame(height: 3)
                    }
                }
                VStack(alignment: .leading, spacing: 7) {
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(rivuneLocalizedFormat("%d. %@", episode.episodeNumber, episode.name))
                            .font(.headline)
                            .foregroundStyle(RivuneIOSTheme.primaryText)
                            .lineLimit(2)
                        Spacer(minLength: 4)
                        if model.episodeProgress[episode.id]?.completed == true {
                            Image(systemName: "checkmark.circle.fill")
                                .foregroundStyle(RivuneIOSTheme.success)
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
                            .foregroundStyle(RivuneIOSTheme.mutedText)
                    }
                    if !episode.overview.isEmpty {
                        Text(episode.overview)
                            .font(.caption)
                            .foregroundStyle(RivuneIOSTheme.secondaryText)
                            .lineLimit(3)
                    }
                }
                .padding(14)
            }
            .background(RivuneIOSTheme.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            .overlay { RoundedRectangle(cornerRadius: 16).stroke(RivuneIOSTheme.hairline, lineWidth: 1) }
        }
        .buttonStyle(.plain)
    }

    private func close() {
        if model.selectedSeason != nil {
            model.closeSeason()
        } else if model.canNavigateBackFromMedia {
            model.closeMedia()
        } else {
            model.closeMedia()
            dismiss()
        }
    }
}

struct RivuneIOSPlaybackSourcesView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var selectedAddonID: UUID?

    private var filteredSources: [PlaybackSourceOption] {
        guard let selectedAddonID else { return model.playbackSources }
        return model.playbackSources.filter { $0.addonId == selectedAddonID }
    }

    private var addonFilters: [(UUID, String)] {
        var seen = Set<UUID>()
        return model.playbackSources.compactMap { source in
            guard seen.insert(source.addonId).inserted else { return nil }
            return (source.addonId, source.addonName?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty ?? source.manifestId)
        }
    }

    var body: some View {
        ZStack {
            RivuneIOSCanvas()
            RivuneIOSPage(maximumWidth: 920) {
                VStack(alignment: .leading, spacing: 22) {
                    HStack(alignment: .top, spacing: 14) {
                        Button(action: model.closePlaybackSources) { Image(systemName: "chevron.left") }
                            .rivuneIOSIconButton()
                            .accessibilityLabel(rivuneLocalized("Back"))
                        RivuneIOSHeading(
                            eyebrow: "Choose a stream",
                            title: "Playback sources",
                            message: "Quality and playback routes returned by your server."
                        )
                    }

                    if !model.playbackSources.isEmpty {
                        HStack(spacing: 8) {
                            ScrollView(.horizontal, showsIndicators: false) {
                                HStack(spacing: 8) {
                                    filterButton("All", id: nil)
                                    ForEach(addonFilters, id: \.0) { filter in
                                        filterButton(filter.1, id: filter.0)
                                    }
                                }
                            }
                            Button(action: model.loadPlaybackSources) { Image(systemName: "arrow.clockwise") }
                                .rivuneIOSIconButton()
                                .disabled(model.mediaLoading)
                                .accessibilityLabel(rivuneLocalized("Refresh sources"))
                        }
                    }


                    if model.mediaLoading && model.playbackSources.isEmpty {
                        RivuneIOSStatusView(state: .loading("Finding streams…"))
                    } else if model.playbackSources.isEmpty {
                        RivuneIOSStatusView(state: .empty(
                            icon: "play.slash",
                            title: "No compatible stream",
                            message: "Your server did not return a source this device can play."
                        ))
                    } else if filteredSources.isEmpty {
                        RivuneIOSStatusView(state: .empty(icon: "line.3.horizontal.decrease.circle", title: "No sources from this add-on", message: nil))
                    } else {
                        LazyVStack(spacing: 12) {
                            ForEach(filteredSources) { source in sourceCard(source) }
                        }
                    }
                }
            }
            if let failure = model.mediaFailure {
                VStack {
                    Spacer()
                    RivuneIOSErrorMessage(failure: failure)
                        .frame(maxWidth: 920)
                        .padding(.horizontal, 20)
                        .padding(.bottom, 20)
                }
                .rivuneTransition(.move(edge: .bottom).combined(with: .opacity))
                .zIndex(1)
            }
        }
        .onChange(of: model.playbackSources) { sources in
            if let selectedAddonID, !sources.contains(where: { $0.addonId == selectedAddonID }) {
                self.selectedAddonID = nil
            }
        }
    }

    private func filterButton(_ title: String, id: UUID?) -> some View {
        Button { selectedAddonID = id } label: {
            RivuneIOSChip(title: title, selected: selectedAddonID == id)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selectedAddonID == id ? .isSelected : [])
    }

    private func sourceCard(_ source: PlaybackSourceOption) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                Text(source.name)
                    .font(.title3.weight(.semibold))
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                Spacer(minLength: 8)
                Text(source.protocol.uppercased())
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(RivuneIOSTheme.secondaryText)
                    .padding(.horizontal, 9)
                    .padding(.vertical, 5)
                    .background(RivuneIOSTheme.raised, in: Capsule())
                    .overlay { Capsule().stroke(RivuneIOSTheme.outline, lineWidth: 1) }
            }
            if let description = source.description, !description.isEmpty {
                Text(description)
                    .font(.callout)
                    .foregroundStyle(RivuneIOSTheme.secondaryText)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text([
                source.addonName?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty ?? source.manifestId,
                source.container?.uppercased(),
                source.mode?.rawValue.replacingOccurrences(of: "_", with: " ").capitalized,
            ].compactMap { $0 }.joined(separator: " · "))
                .font(.caption)
                .foregroundStyle(RivuneIOSTheme.mutedText)

            LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 10)], spacing: 10) {
                if model.preferredPlayer != .external {
                    Button { model.play(source, externally: false) } label: {
                        Label("Play in Rivune", systemImage: "play.fill")
                            .frame(maxWidth: .infinity)
                    }
                    .rivuneIOSPrimaryButton()
                }
                if model.preferredPlayer != .rivune {
                    Button { model.play(source, externally: true) } label: {
                        Label("Open in app", systemImage: "play.rectangle.on.rectangle")
                            .frame(maxWidth: .infinity)
                    }
                    .rivuneIOSSecondaryButton()
                }
                if !["hls", "dash"].contains(source.protocol.lowercased()) {
                    Button { model.download(source) } label: {
                        Label(model.isDownloading(source) ? "Downloading…" : "Download", systemImage: "arrow.down.circle")
                            .frame(maxWidth: .infinity)
                    }
                    .rivuneIOSSecondaryButton()
                    .disabled(model.offlineDownloadActive)
                }
            }
        }
        .rivuneIOSCard()
    }
}

private func rivuneIOSDisplayTitle(_ detail: RivuneMediaDetail) -> String {
    detail.movie?.title ?? detail.series?.name ?? detail.episode?.name ?? detail.target.title
}

private func rivuneIOSOverview(_ detail: RivuneMediaDetail) -> String? {
    detail.movie?.overview ?? detail.series?.overview ?? detail.episode?.overview ?? detail.target.overview
}

private func rivuneIOSTagline(_ detail: RivuneMediaDetail) -> String? {
    detail.movie?.tagline ?? detail.series?.tagline
}

private func rivuneIOSReleaseLine(_ detail: RivuneMediaDetail) -> String? {
    let date = detail.movie?.releaseDate ?? detail.series?.firstAirDate ?? detail.episode?.airDate ?? detail.target.releaseInfo
    let runtime = detail.movie?.runtimeMinutes ?? detail.episode?.runtimeMinutes ?? detail.target.runtimeMinutes
    let rating = detail.movie?.voteAverage ?? detail.series?.voteAverage ?? detail.episode?.voteAverage
    let type = rivuneLocalized(detail.target.mediaType == "series" ? "Series" : detail.target.mediaType.capitalized)
    let counts = detail.series.flatMap { series in
        [
            series.numberOfSeasons.map { rivuneLocalizedFormat("%d seasons", $0) },
            series.numberOfEpisodes.map { rivuneLocalizedFormat("%d episodes", $0) },
        ].compactMap { $0 }.joined(separator: " · ").nilIfEmpty
    }
    return [
        type,
        date,
        runtime.map { rivuneLocalizedFormat("%d min", $0) },
        rating.flatMap { $0 > 0 ? String(format: "★ %.1f", $0) : nil },
        counts,
        detail.series?.status,
    ].compactMap { $0 }.joined(separator: " · ").nilIfEmpty
}

@MainActor
private func rivuneIOSBackdropURL(_ detail: RivuneMediaDetail, model: RivuneAppModel) -> URL? {
    let value = detail.movie?.backdropUrl
        ?? detail.series?.backdropUrl
        ?? detail.episode?.backdropUrl
        ?? detail.episode?.stillUrl
        ?? detail.parentSeries?.backdropUrl
        ?? detail.target.backgroundUrl
        ?? detail.target.posterUrl
    return value.flatMap(model.resolvedResourceURL)
}

@MainActor
private func rivuneIOSSeasonBackdropURL(_ season: Season, model: RivuneAppModel) -> URL? {
    let value = season.backdropUrl
        ?? model.mediaDetail?.series?.backdropUrl
        ?? model.mediaDetail?.parentSeries?.backdropUrl
        ?? model.mediaDetail?.target.backgroundUrl
        ?? season.posterUrl
    return value.flatMap(model.resolvedResourceURL)
}

private func rivuneIOSTrailerURL(_ trailer: Trailer) -> URL? {
    var components = URLComponents(string: "https://www.youtube.com/watch")
    components?.queryItems = [URLQueryItem(name: "v", value: trailer.youtubeId)]
    return components?.url
}
private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}

#endif
