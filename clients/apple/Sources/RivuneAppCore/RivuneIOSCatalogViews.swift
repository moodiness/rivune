#if os(iOS)
  import SwiftUI
  import RivuneAPI

  private struct RivuneIOSAccountFocusGenerationKey: EnvironmentKey {
    static let defaultValue = 0
  }

  private extension EnvironmentValues {
    var rivuneIOSAccountFocusGeneration: Int {
      get { self[RivuneIOSAccountFocusGenerationKey.self] }
      set { self[RivuneIOSAccountFocusGenerationKey.self] = newValue }
    }
  }

  private struct RivuneIOSProfileSurfaceStatus: View {
    @ObservedObject var model: RivuneAppModel
    let surfaces: [RivuneProfileExperienceSurface]
    let empty: String
    private var message: String {
      if surfaces.contains(where: model.isProfileExperienceLoading) { return "Loading \(empty.lowercased())" }
      return surfaces.compactMap({ model.profileExperienceFailure(for: $0) }).first?.localizedDescription ?? empty
    }
    var body: some View {
      Group {
        if surfaces.contains(where: model.isProfileExperienceLoading) {
          RivuneIOSStatusView(state: .loading("Loading \(empty.lowercased())…"))
        } else if let failure = surfaces.compactMap({ model.profileExperienceFailure(for: $0) }).first {
          VStack(spacing: 10) {
            RivuneIOSStatusView(state: .failure(failure))
            Button("Retry", action: model.loadProfileExperiences).rivuneIOSPrimaryButton()
              .accessibilityLabel("Retry loading \(empty.lowercased())")
          }
        } else {
          RivuneIOSStatusView(state: .empty(icon: "tray", title: empty, message: ""))
        }
      }
      .accessibilityLabel(message)
      .rivuneStatusAnnouncement(message)
    }
  }

  struct RivuneIOSLibraryView: View {
    @ObservedObject var model: RivuneAppModel
    @State private var showSettings = false
    @State private var showAccountActions = false
    @State private var confirmDisconnect = false
    @State private var selectedCollection: Collection?
    @State private var pendingAccountAction: AccountAction?
    @State private var accountFocusGeneration = 0

    var body: some View {
      compactNavigation
        .background(RivuneIOSTheme.canvas)
        .environment(\.rivuneIOSAccountFocusGeneration, accountFocusGeneration)
        .sheet(isPresented: $showAccountActions, onDismiss: completeAccountAction) {
          RivuneIOSAccountActionsModal(
            select: selectAccountAction,
            dismiss: { showAccountActions = false }
          )
        }
        .confirmationDialog("Disconnect from \(model.serverName)?", isPresented: $confirmDisconnect)
      {
        Button(role: .destructive, action: model.disconnect) {
          Label("Disconnect", systemImage: "rectangle.portrait.and.arrow.right")
        }
        Button("Cancel", role: .cancel) {}
      } message: {
        Text("The locally stored session for this server will be removed.")
      }
        .fullScreenCover(
          isPresented: $showSettings,
          onDismiss: { accountFocusGeneration &+= 1 }
        ) {
          RivuneIOSSettingsView(model: model, dismiss: { showSettings = false })
        }
        .fullScreenCover(item: $selectedCollection) { collection in
          RivuneIOSCollectionOverviewView(collection: collection, model: model)
        }
        .fullScreenCover(
          item: Binding(
            get: { model.openedFolder },
            set: { if $0 == nil { model.closeFolder() } }
          ), onDismiss: model.closeFolder
        ) { _ in
          RivuneIOSFolderView(model: model)
        }
        .fullScreenCover(
          isPresented: Binding(
            get: { model.mediaLoading || model.mediaDetail != nil || model.mediaFailure != nil },
            set: { if !$0 { model.closeMedia() } }
          )
        ) {
          RivuneIOSMediaDetailView(model: model)
        }
        .onChange(of: confirmDisconnect) { presented in
          if !presented { accountFocusGeneration &+= 1 }
        }
        .onChange(of: model.destination) { destination in
          if destination != .library {
            showAccountActions = false
            pendingAccountAction = nil
            showSettings = false
            selectedCollection = nil
          }
        }
    }

    private enum AccountAction {
      case settings
      case switchProfile
      case disconnect
    }

    private func selectAccountAction(_ action: AccountAction) {
      pendingAccountAction = action
      showAccountActions = false
    }

    private func completeAccountAction() {
      guard let action = pendingAccountAction else {
        accountFocusGeneration &+= 1
        return
      }
      pendingAccountAction = nil
      switch action {
      case .settings:
        showSettings = true
      case .switchProfile:
        model.chooseAnotherProfile()
      case .disconnect:
        confirmDisconnect = true
      }
    }

    private struct RivuneIOSAccountActionsModal: View {
      let select: (AccountAction) -> Void
      let dismiss: () -> Void
      @AccessibilityFocusState private var firstActionFocused: Bool

      var body: some View {
        NavigationView {
          ZStack {
            RivuneIOSCanvas()
            VStack(spacing: 10) {
              action("Settings", systemImage: "gearshape.fill") { select(.settings) }
                .accessibilityFocused($firstActionFocused)
              action("Switch profile", systemImage: "person.2.fill") { select(.switchProfile) }
              action(
                "Disconnect",
                systemImage: "rectangle.portrait.and.arrow.right",
                color: RivuneIOSTheme.danger
              ) { select(.disconnect) }
            }
            .padding(20)
            .frame(maxWidth: 420)
          }
          .navigationTitle(rivuneLocalized("Account actions"))
          .navigationBarTitleDisplayMode(.inline)
          .toolbar {
            ToolbarItem(placement: .cancellationAction) {
              Button("Cancel", action: dismiss)
            }
          }
        }
        .navigationViewStyle(StackNavigationViewStyle())
        .accessibilityAction(.escape) { dismiss() }
        .onAppear {
          DispatchQueue.main.async { firstActionFocused = true }
        }
      }

      private func action(
        _ title: String,
        systemImage: String,
        color: Color = RivuneIOSTheme.primaryText,
        action: @escaping () -> Void
      ) -> some View {
        Button(action: action) {
          HStack(spacing: 12) {
            Image(systemName: systemImage)
              .frame(width: 22)
            Text(rivuneLocalized(title))
            Spacer(minLength: 0)
          }
          .font(.body.weight(.semibold))
          .foregroundStyle(color)
          .padding(.horizontal, 18)
          .frame(maxWidth: .infinity, minHeight: 56)
          .background(
            RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 18, style: .continuous)
          )
          .overlay {
            RoundedRectangle(cornerRadius: 18, style: .continuous)
              .stroke(RivuneIOSTheme.outline, lineWidth: 1)
          }
          .contentShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
        .buttonStyle(.plain)
      }
    }

    private var compactNavigation: some View {
      TabView(selection: Binding(get: { model.selectedTab }, set: model.selectTab)) {
        tab(.home)
          .tag(RivuneViewerTab.home)
          .tabItem { Label("Home", systemImage: "house.fill") }
        tab(.search)
          .tag(RivuneViewerTab.search)
          .tabItem { Label("Search", systemImage: "magnifyingglass") }
        tab(.library)
          .tag(RivuneViewerTab.library)
          .tabItem { Label("Library", systemImage: "rectangle.stack.fill") }
        tab(.calendar)
          .tag(RivuneViewerTab.calendar)
          .tabItem { Label("Calendar", systemImage: "calendar") }
      }
      .modifier(RivuneIOSTabBarModifier())
      .environment(\.horizontalSizeClass, .compact)
    }

    @ViewBuilder
    private func tab(_ tab: RivuneViewerTab) -> some View {
      switch tab {
      case .home:
        RivuneIOSHomeView(
          model: model,
          account: { showAccountActions = true },
          openCollection: { selectedCollection = $0 }
        )
      case .search:
        RivuneIOSSearchView(model: model, account: { showAccountActions = true })
      case .library:
        RivuneIOSPersonalLibraryView(model: model, account: { showAccountActions = true })
      case .calendar:
        RivuneIOSCalendarView(model: model, account: { showAccountActions = true })
      }
    }
  }

  private struct RivuneIOSTabBarModifier: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
      if #available(iOS 16.0, *) {
        content
          .toolbarBackground(RivuneIOSTheme.surface, for: .tabBar)
          .toolbarBackground(.visible, for: .tabBar)
      } else {
        content
      }
    }
  }

  private struct RivuneIOSPrimaryHeader: View {
    @ObservedObject var model: RivuneAppModel
    let title: String
    let tab: RivuneViewerTab
    @Environment(\.rivuneIOSAccountFocusGeneration) private var accountFocusGeneration
    @AccessibilityFocusState private var accountButtonFocused: Bool
    let account: () -> Void

    var body: some View {
      HStack(spacing: 12) {
        VStack(alignment: .leading, spacing: 2) {
          Text(model.serverName.uppercased())
            .font(.caption2.weight(.bold))
            .tracking(1.2)
            .foregroundStyle(RivuneIOSTheme.ember)
          Text(rivuneLocalized(title))
            .font(.largeTitle.bold())
            .foregroundStyle(RivuneIOSTheme.primaryText)
        }
        Spacer()
        RivuneIOSAccountButton(
          profile: model.activeProfile,
          imageData: model.activeProfile.flatMap { model.profileAvatarData[$0.id] },
          action: {
            accountButtonFocused = false
            account()
          }
        )
        .accessibilityFocused($accountButtonFocused)
      }
      .onChange(of: accountFocusGeneration) { _ in
        if model.selectedTab == tab { accountButtonFocused = true }
      }
    }
  }

  private struct RivuneIOSHomeView: View {
    @ObservedObject var model: RivuneAppModel
    let account: () -> Void
    let openCollection: (Collection) -> Void

    var body: some View {
      GeometryReader { proxy in
        ScrollView {
          VStack(alignment: .leading, spacing: 32) {
            RivuneIOSPrimaryHeader(model: model, title: "Home", tab: .home, account: account)
            if let queue = model.readingQueue, !queue.items.isEmpty {
              VStack(alignment: .leading, spacing: 12) {
                RivuneIOSSectionHeader(title: "Up next", subtitle: "Synced with this profile")
                ForEach(queue.items.prefix(6)) { item in
                  HStack {
                    Image(systemName: "play.rectangle")
                    Text(item.title).font(.headline).rivuneIOSDynamicTitle(standardLimit: 2)
                    Spacer()
                    Button { model.moveQueueItem(item, offset: -1) } label: { Image(systemName: "arrow.up") }
                      .disabled(item.position == 0)
                      .accessibilityLabel("Move \(item.title), position \(item.position + 1), earlier")
                    Button(role: .destructive) { model.removeQueueItem(item) } label: { Image(systemName: "trash") }
                      .accessibilityLabel("Remove \(item.title), position \(item.position + 1), from queue")
                  }
                  .padding(12)
                  .background(RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 14))
                }
              }
            }
            else {
              RivuneIOSProfileSurfaceStatus(model: model, surfaces: [.queue], empty: "Queue is empty")
            }
            VStack(alignment: .leading, spacing: 12) {
              RivuneIOSSectionHeader(title: "Inbox")
              ForEach(model.mediaNotifications.prefix(5)) { notification in
                HStack {
                  Image(systemName: notification.readAt == nil ? "bell.badge.fill" : "bell")
                  Text(notification.title).font(.headline)
                  Spacer()
                  Button("Read") { model.acknowledgeMediaNotification(notification, state: .read) }
                    .accessibilityLabel(rivuneMediaNotificationActionLabel("Mark as read", notification: notification))
                  Button(role: .destructive) {
                    model.acknowledgeMediaNotification(notification, state: .dismissed)
                  } label: { Image(systemName: "xmark") }
                  .accessibilityLabel(rivuneMediaNotificationActionLabel("Dismiss", notification: notification))
                }.padding(12).background(RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 14))
              }
              if model.mediaNotifications.isEmpty {
                RivuneIOSProfileSurfaceStatus(
                  model: model, surfaces: [.notifications], empty: "No media notifications")
              }
              ForEach(model.extensionIncidents.prefix(5)) { incident in
                HStack {
                  Image(systemName: incident.state == .resolved ? "checkmark.circle" : "exclamationmark.triangle")
                  Text(incident.addonName).font(.headline)
                  Spacer()
                  if incident.acknowledgedAt == nil {
                    Button("Acknowledge") { model.acknowledgeIncident(incident) }
                      .accessibilityLabel(rivuneIncidentActionLabel("Acknowledge", incident: incident))
                  }
                }.padding(12).background(RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 14))
              }
              if model.extensionIncidents.isEmpty {
                RivuneIOSProfileSurfaceStatus(
                  model: model, surfaces: [.incidents], empty: "No add-on incidents")
              }
            }

            if !model.heroItems.isEmpty {
              RivuneIOSHeroCarousel(
                items: model.heroItems,
                model: model,
                height: heroHeight(for: proxy.size.width)
              )
            }

            if !model.continueWatchingItems.isEmpty {
              RivuneIOSContinueRail(
                items: model.continueWatchingItems, model: model,
                availableWidth: contentWidth(proxy.size.width))
            }
            if !model.recommendationItems.isEmpty {
              RivuneIOSRecommendationRail(
                items: model.recommendationItems, model: model,
                availableWidth: contentWidth(proxy.size.width))
            }
            if !model.offlineItems.isEmpty {
              RivuneIOSDownloadRail(
                items: model.offlineItems, model: model,
                availableWidth: contentWidth(proxy.size.width))
            }

            if model.isBusy {
              RivuneIOSStatusView(state: .loading("Loading your home…"))
            } else if let failure = model.failure {
              VStack(spacing: 12) {
                RivuneIOSStatusView(state: .failure(failure))
                Button("Try again", action: model.retryLibrary)
                  .rivuneIOSPrimaryButton()
              }
            } else if model.collections.isEmpty && model.heroItems.isEmpty {
              RivuneIOSStatusView(
                state: .empty(
                  icon: "rectangle.stack.badge.minus",
                  title: "Nothing here yet",
                  message:
                    "This profile has no visible collections. Configure them from your Rivune web interface."
                ))
            }

            ForEach(model.collections) { collection in
              RivuneIOSCollectionRail(
                collection: collection,
                model: model,
                availableWidth: contentWidth(proxy.size.width),
                viewAll: { openCollection(collection) }
              )
            }
          }
          .frame(maxWidth: 1320, alignment: .leading)
          .padding(.horizontal, RivuneIOSTheme.pageInset(for: proxy.size.width))
          .padding(.top, RivuneIOSTheme.pageTopInset(for: proxy.size.width))
          .padding(.bottom, 48)
          .frame(maxWidth: .infinity)
        }
      }
      .background(RivuneIOSTheme.canvas)
    }

    private func contentWidth(_ width: CGFloat) -> CGFloat {
      max(width - RivuneIOSTheme.pageInset(for: width) * 2, 280)
    }

    private func heroHeight(for width: CGFloat) -> CGFloat {
      contentWidth(width) * 9 / 16
    }
  }

  private struct RivuneIOSHeroCarousel: View {
    let items: [RivuneHeroItem]
    @ObservedObject var model: RivuneAppModel
    let height: CGFloat
    @State private var selection = 0

    var body: some View {
      TabView(selection: $selection) {
        ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
          Button {
            model.openMedia(item.target)
          } label: {
            hero(item)
          }
          .buttonStyle(.plain)
          .tag(index)
        }
      }
      .tabViewStyle(PageTabViewStyle(indexDisplayMode: items.count > 1 ? .automatic : .never))
      .frame(height: height)
      .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
      .overlay { RoundedRectangle(cornerRadius: 24).stroke(RivuneIOSTheme.hairline, lineWidth: 1) }
      .onChange(of: items.map(\.id)) { _ in if selection >= items.count { selection = 0 } }
    }

    private func hero(_ item: RivuneHeroItem) -> some View {
      ZStack(alignment: .bottomLeading) {
        RivuneIOSTheme.raised
        AsyncImage(url: item.backgroundUrl.flatMap(model.resolvedResourceURL)) { phase in
          if let image = phase.image { image.resizable().scaledToFit() } else { Color.clear }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        LinearGradient(
          colors: [.clear, Color.black.opacity(0.38), Color.black.opacity(0.92)],
          startPoint: .top,
          endPoint: .bottom
        )
        VStack(alignment: .leading, spacing: 10) {
          if let logo = item.logoUrl.flatMap(model.resolvedResourceURL) {
            AsyncImage(url: logo) { phase in
              if let image = phase.image {
                image.resizable().scaledToFit()
              } else {
                Text(item.title).font(heroTitleFont)
              }
            }
            .frame(maxWidth: heroLogoWidth, maxHeight: heroLogoHeight, alignment: .leading)
          } else {
            Text(item.title).font(heroTitleFont)
          }
        }
        .foregroundStyle(.white)
        .padding(height < 300 ? 18 : 28)
      }
      .contentShape(Rectangle())
      .accessibilityElement(children: .combine)
    }

    private var heroLogoWidth: CGFloat { height < 300 ? 190 : min(height * 0.72, 380) }
    private var heroLogoHeight: CGFloat { height < 300 ? 60 : min(height * 0.24, 130) }
    private var heroTitleFont: Font { height < 300 ? .title3.bold() : .largeTitle.bold() }
  }

  private struct RivuneIOSSearchView: View {
    @ObservedObject var model: RivuneAppModel
    let account: () -> Void

    var body: some View {
      GeometryReader { proxy in
        RivuneIOSPage(maximumWidth: 1200) {
          VStack(alignment: .leading, spacing: 24) {
            RivuneIOSPrimaryHeader(model: model, title: "Search", tab: .search, account: account)
            Text("Search every compatible catalog connected to your server.")
              .foregroundStyle(RivuneIOSTheme.secondaryText)

            HStack(spacing: 10) {
              HStack(spacing: 10) {
                Image(systemName: "magnifyingglass")
                  .foregroundStyle(RivuneIOSTheme.mutedText)
                TextField("Movies, series, anime…", text: $model.searchQuery)
                  .foregroundStyle(RivuneIOSTheme.primaryText)
                  .submitLabel(.search)
                  .onSubmit(model.search)
                if !model.searchQuery.isEmpty {
                  Button {
                    model.searchQuery = ""
                    model.search()
                  } label: {
                    Image(systemName: "xmark.circle.fill")
                      .foregroundStyle(RivuneIOSTheme.mutedText)
                  }
                  .buttonStyle(.plain)
                  .accessibilityLabel(rivuneLocalized("Clear search"))
                }
              }
              .padding(.horizontal, 16)
              .frame(minHeight: 54)
              .background(
                RivuneIOSTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous)
              )
              .overlay {
                RoundedRectangle(cornerRadius: 14).stroke(RivuneIOSTheme.outline, lineWidth: 1)
              }

              Button(action: model.search) { Image(systemName: "arrow.right") }
                .rivuneIOSIconButton()
                .disabled(
                  model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).count < 2
                    || model.tabLoading
                )
                .accessibilityLabel(rivuneLocalized("Search"))
            }
            ScrollView(.horizontal, showsIndicators: false) {
              HStack(spacing: 8) {
                ForEach(model.savedSearches) { saved in
                  Button { model.runSavedSearch(saved) } label: {
                    RivuneIOSChip(title: saved.name, icon: "bookmark.fill", selected: false)
                  }.buttonStyle(.plain)
                }
                ForEach(model.smartCollections) { collection in
                  Button { model.openSmartCollection(collection) } label: {
                    RivuneIOSChip(title: collection.name, icon: "wand.and.stars", selected: false)
                  }.buttonStyle(.plain)
                }
              }
            }
            if model.savedSearches.isEmpty {
              RivuneIOSProfileSurfaceStatus(model: model, surfaces: [.savedSearches], empty: "No saved searches")
            }
            if model.smartCollections.isEmpty {
              RivuneIOSProfileSurfaceStatus(model: model, surfaces: [.smartCollections], empty: "No smart collections")
            }
            if !model.searchItems.isEmpty {
              Button { model.saveCurrentSearch() } label: {
                Label("Save this search", systemImage: "bookmark")
              }.rivuneIOSSecondaryButton()
            }

            if !model.searchMediaTypes.isEmpty {
              ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                  searchTypeFilter(nil)
                  ForEach(model.searchMediaTypes, id: \.self) { type in
                    searchTypeFilter(type)
                  }
                }
              }
            }

            if !model.searchIntents.isEmpty {
              ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                  ForEach(model.searchIntents) { intent in
                    Button {
                      model.removeSearchIntent(id: intent.id)
                    } label: {
                      RivuneIOSChip(title: intent.label, icon: "xmark", selected: true)
                    }
                    .buttonStyle(.plain)
                    .disabled(model.tabLoading)
                    .accessibilityLabel(
                      rivuneLocalizedFormat("Remove %@ filter", intent.label))
                  }
                }
              }
            }

            tabStatus(
              emptyTitle: model.searchQuery.isEmpty
                ? "Start typing to search" : "No matching titles",
              emptyMessage: model.searchQuery.isEmpty ? "Enter at least two characters." : nil)

            if !model.searchItems.isEmpty {
              LazyVGrid(columns: columns(for: proxy.size.width), alignment: .leading, spacing: 22) {
                ForEach(model.searchItems, id: \.stableSearchPresentationID) { item in
                  Button {
                    model.openMedia(item)
                  } label: {
                    RivuneIOSMediaCard(
                      title: item.title,
                      subtitle: item.releaseInfo,
                      mediaType: item.mediaType,
                      imageURL: (item.posterUrl ?? item.backgroundUrl).flatMap(
                        model.resolvedResourceURL)
                    )
                  }
                  .buttonStyle(.plain)
                }
              }
            }

            if model.searchHasMore {
              Button(action: model.loadMoreSearch) {
                HStack {
                  if model.tabLoading { ProgressView().tint(.black) }
                  Label("Load more", systemImage: "arrow.down.circle")
                }
                .frame(maxWidth: .infinity)
              }
              .rivuneIOSPrimaryButton()
              .disabled(model.tabLoading)
            }
          }
        }
      }
      .background(RivuneIOSTheme.canvas)
    }

    @ViewBuilder
    private func tabStatus(emptyTitle: String, emptyMessage: String?) -> some View {
      if model.tabLoading {
        RivuneIOSStatusView(
          state: .loading(
            model.searchItems.isEmpty ? "Searching…" : "Enriching results…"))
          .accessibilityLabel(
            rivuneLocalized(model.searchItems.isEmpty ? "Searching…" : "Enriching results…"))
      } else if let failure = model.tabFailure {
        RivuneIOSStatusView(state: .failure(failure))
      } else if model.searchItems.isEmpty {
        RivuneIOSStatusView(
          state: .empty(icon: "magnifyingglass", title: emptyTitle, message: emptyMessage))
      }
    }

    private func searchTypeFilter(_ type: String?) -> some View {
      let presentation = searchTypePresentation(type)
      return Button {
        model.setSearchMediaType(type)
      } label: {
        RivuneIOSChip(
          title: presentation.title,
          icon: presentation.icon,
          selected: model.searchMediaType == type
        )
      }
      .buttonStyle(.plain)
      .disabled(model.tabLoading)
      .accessibilityAddTraits(model.searchMediaType == type ? .isSelected : [])
    }

    private func searchTypePresentation(_ type: String?) -> (title: String, icon: String) {
      switch type {
      case nil: return ("All", "square.grid.2x2.fill")
      case "movie": return ("Movies", "film.fill")
      case "series": return ("Series", "tv.fill")
      case "anime": return ("Anime", "sparkles.tv")
      case "tv": return ("Live TV", "dot.radiowaves.left.and.right")
      case "other": return ("Other", "square.grid.2x2")
      case .some(let value):
        return (
          value.split(whereSeparator: { $0 == "-" || $0 == "_" }).map(\.capitalized)
            .joined(separator: " "),
          "tag.fill"
        )
      }
    }

    private func columns(for width: CGFloat) -> [GridItem] {
      [
        GridItem(
          .adaptive(minimum: RivuneIOSTheme.gridMinimum(for: width), maximum: 210), spacing: 18)
      ]
    }
  }

  private struct RivuneIOSPersonalLibraryView: View {
    @ObservedObject var model: RivuneAppModel
    let account: () -> Void

    var body: some View {
      GeometryReader { proxy in
        RivuneIOSPage(maximumWidth: 1200) {
          VStack(alignment: .leading, spacing: 24) {
            RivuneIOSPrimaryHeader(model: model, title: "Library", tab: .library, account: account)
            Text("Titles saved to this profile.")
              .foregroundStyle(RivuneIOSTheme.secondaryText)

            ScrollView(.horizontal, showsIndicators: false) {
              HStack(spacing: 8) {
                filter("All", type: nil, icon: "rectangle.stack.fill")
                filter("Movies", type: .movie, icon: "film.fill")
                filter("Series", type: .series, icon: "tv.fill")
                filter("Live TV", type: .tv, icon: "dot.radiowaves.left.and.right")
              }
            }

            if model.tabLoading && model.libraryItems.isEmpty {
              RivuneIOSStatusView(state: .loading("Loading library…"))
            } else if let failure = model.tabFailure {
              RivuneIOSStatusView(state: .failure(failure))
            } else if model.libraryItems.isEmpty {
              RivuneIOSStatusView(
                state: .empty(
                  icon: "rectangle.stack.badge.plus",
                  title: "Your library is empty",
                  message: "Add titles from their detail page to find them here."
                ))
            }

            if !model.libraryItems.isEmpty {
              LazyVGrid(
                columns: [
                  GridItem(
                    .adaptive(
                      minimum: RivuneIOSTheme.gridMinimum(for: proxy.size.width), maximum: 210),
                    spacing: 18)
                ], alignment: .leading, spacing: 22
              ) {
                ForEach(model.libraryItems) { item in
                  Button {
                    model.openMedia(item)
                  } label: {
                    RivuneIOSMediaCard(
                      title: item.title ?? rivuneLocalized("Untitled"),
                      subtitle: item.releaseInfo,
                      mediaType: item.mediaType.rawValue,
                      imageURL: (item.posterUrl ?? item.backgroundUrl).flatMap(
                        model.resolvedResourceURL)
                    )
                  }
                  .buttonStyle(.plain)
                  .disabled(!item.available)
                  .opacity(item.available ? 1 : 0.45)
                }
              }
            }

            if model.libraryPage < model.libraryTotalPages {
              Button(action: model.loadMoreLibrary) {
                HStack {
                  if model.tabLoading { ProgressView().tint(.black) }
                  Label("Load more", systemImage: "arrow.down.circle")
                }
                .frame(maxWidth: .infinity)
              }
              .rivuneIOSPrimaryButton()
              .disabled(model.tabLoading)
            }
          }
        }
      }
      .background(RivuneIOSTheme.canvas)
    }

    private func filter(_ title: String, type: TitleMediaType?, icon: String) -> some View {
      Button {
        model.setLibraryMediaType(type)
      } label: {
        RivuneIOSChip(title: title, icon: icon, selected: model.libraryMediaType == type)
      }
      .buttonStyle(.plain)
      .disabled(model.tabLoading)
      .accessibilityAddTraits(model.libraryMediaType == type ? .isSelected : [])
    }
  }

  private struct RivuneIOSCalendarView: View {
    @ObservedObject var model: RivuneAppModel
    let account: () -> Void

    var body: some View {
      RivuneIOSPage(maximumWidth: 920) {
        VStack(alignment: .leading, spacing: 24) {
          RivuneIOSPrimaryHeader(model: model, title: "Calendar", tab: .calendar, account: account)
          Text("Upcoming movies and episodes from your library.")
            .foregroundStyle(RivuneIOSTheme.secondaryText)

          HStack(spacing: 14) {
            Button(action: model.previousCalendarMonth) { Image(systemName: "chevron.left") }
              .rivuneIOSIconButton()
              .accessibilityLabel(rivuneLocalized("Previous month"))
            Spacer()
            Text(model.calendarMonth.formatted(.dateTime.month(.wide).year()))
              .font(.title3.bold())
              .foregroundStyle(RivuneIOSTheme.primaryText)
              .multilineTextAlignment(.center)
            Spacer()
            Button(action: model.nextCalendarMonth) { Image(systemName: "chevron.right") }
              .rivuneIOSIconButton()
              .accessibilityLabel(rivuneLocalized("Next month"))
          }
          .disabled(model.tabLoading)

          if model.tabLoading && model.calendarEvents.isEmpty {
            RivuneIOSStatusView(state: .loading("Loading releases…"))
          } else if let failure = model.tabFailure {
            RivuneIOSStatusView(state: .failure(failure))
          } else if model.calendarEvents.isEmpty {
            RivuneIOSStatusView(
              state: .empty(
                icon: "calendar.badge.minus",
                title: "No releases this month",
                message: "Only upcoming titles from your library appear here."
              ))
          }

          LazyVStack(spacing: 12) {
            ForEach(model.calendarEvents) { event in
              Button {
                model.openMedia(event)
              } label: {
                HStack(spacing: 15) {
                  RivuneIOSArtwork(
                    url: event.posterUrl.flatMap(model.resolvedResourceURL),
                    aspectRatio: 2 / 3,
                    fallbackSystemImage: event.mediaType == "episode" ? "tv.fill" : "film.fill",
                    cornerRadius: 10
                  )
                  .frame(width: 70, height: 105)
                  .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                  VStack(alignment: .leading, spacing: 5) {
                    Text(event.title)
                      .font(.headline)
                      .foregroundStyle(RivuneIOSTheme.primaryText)
                      .rivuneIOSDynamicTitle(standardLimit: 2)
                    if let seriesTitle = event.seriesTitle {
                      Text(seriesTitle)
                        .font(.subheadline)
                        .foregroundStyle(RivuneIOSTheme.secondaryText)
                        .lineLimit(1)
                    }
                    Text(event.releaseDate)
                      .font(.caption.weight(.semibold))
                      .foregroundStyle(RivuneIOSTheme.ember)
                  }
                  Spacer()
                  Image(systemName: "chevron.right")
                    .font(.caption.bold())
                    .foregroundStyle(RivuneIOSTheme.mutedText)
                }
                .frame(maxWidth: .infinity)
                .rivuneIOSCard(inset: 14)
              }
              .buttonStyle(.plain)
            }
          }
        }
      }
      .background(RivuneIOSTheme.canvas)
    }
  }

  private struct RivuneIOSMediaCard: View {
    let title: String
    let subtitle: String?
    let mediaType: String
    let imageURL: URL?

    var body: some View {
      VStack(alignment: .leading, spacing: 9) {
        RivuneIOSArtwork(
          url: imageURL,
          aspectRatio: 2 / 3,
          fallbackSystemImage: ["series", "tv"].contains(mediaType) ? "tv" : "film"
        )
        RivuneIOSTileTitle(title: title)
        if let subtitle = rivuneIOSMeaningfulSubtitle(subtitle) {
          Text(subtitle)
            .font(.caption)
            .foregroundStyle(RivuneIOSTheme.mutedText)
            .lineLimit(1)
        }
      }
      .accessibilityElement(children: .combine)
    }
  }

  private struct RivuneIOSContinueRail: View {
    let items: [ContinueWatchingItem]
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
      VStack(alignment: .leading, spacing: 14) {
        RivuneIOSSectionHeader(title: "Continue watching")
        ScrollView(.horizontal, showsIndicators: false) {
          LazyHStack(alignment: .top, spacing: 14) {
            ForEach(items) { item in
              let width = landscapeWidth
              Button {
                model.openMedia(item)
              } label: {
                VStack(alignment: .leading, spacing: 8) {
                  ZStack(alignment: .bottom) {
                    RivuneIOSArtwork(
                      url: (item.episodeStillUrl ?? item.backgroundUrl ?? item.posterUrl).flatMap(
                        model.resolvedResourceURL),
                      aspectRatio: 16 / 9,
                      fallbackSystemImage: "play.rectangle.fill"
                    )
                    GeometryReader { geometry in
                      let progress =
                        item.durationSeconds > 0
                        ? min(
                          max(CGFloat(item.positionSeconds) / CGFloat(item.durationSeconds), 0), 1)
                        : 0
                      HStack(spacing: 0) {
                        RivuneIOSTheme.ember.frame(width: geometry.size.width * progress)
                        Color.white.opacity(0.22)
                      }
                    }
                    .frame(height: 3)
                  }
                  .overlay(alignment: .topTrailing) {
                    if let badge = rivuneContinueWatchingBadge(item) {
                      Text(badge)
                        .font(.caption2.weight(.bold))
                        .foregroundStyle(.white)
                        .lineLimit(1)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 5)
                        .background(
                          Color.black.opacity(0.72),
                          in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                        )
                        .padding(8)
                    }
                  }
                  .frame(width: width, height: width * 9 / 16)
                  .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                  VStack(alignment: .leading, spacing: 3) {
                    HStack(alignment: .firstTextBaseline, spacing: 4) {
                      Text(item.title ?? item.episodeTitle ?? rivuneLocalized("Continue watching"))
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(RivuneIOSTheme.primaryText)
                        .rivuneIOSDynamicTitle(standardLimit: 1)
                      if let code = rivuneContinueWatchingEpisodeCode(item) {
                        Text("· \(code)")
                          .font(.caption.weight(.medium).monospacedDigit())
                          .foregroundStyle(RivuneIOSTheme.mutedText)
                          .fixedSize(horizontal: true, vertical: false)
                      }
                    }
                    let subtitle = rivuneContinueWatchingSubtitle(item)
                    Text(subtitle ?? " ")
                      .font(.caption)
                      .foregroundStyle(RivuneIOSTheme.mutedText)
                      .lineLimit(1)
                      .opacity(subtitle == nil ? 0 : 1)
                      .frame(height: 16, alignment: .topLeading)
                  }
                  .frame(width: width, alignment: .leading)
                }
              }
              .buttonStyle(.plain)
            }
          }
        }
      }
    }

    private var landscapeWidth: CGFloat { min(max((availableWidth - 14) / 1.45, 230), 360) }

  }

  private struct RivuneIOSRecommendationRail: View {
    let items: [RivuneRecommendationItem]
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
      VStack(alignment: .leading, spacing: 14) {
        RivuneIOSSectionHeader(title: "Recommended for you")
        ScrollView(.horizontal, showsIndicators: false) {
          LazyHStack(alignment: .top, spacing: 14) {
            ForEach(items) { item in
              let landscape = model.recommendationLayout == .landscape
              let width = landscape ? landscapeWidth : posterWidth
              Button {
                model.openMedia(item.target)
              } label: {
                VStack(alignment: .leading, spacing: 8) {
                  RivuneIOSArtwork(
                    url: (landscape ? item.target.backgroundUrl : item.target.posterUrl).flatMap(
                      model.resolvedResourceURL),
                    aspectRatio: landscape ? 16 / 9 : 2 / 3,
                    fallbackSystemImage: "sparkles.tv"
                  )
                  .frame(width: width, height: landscape ? width * 9 / 16 : width * 1.5)
                  Text(item.target.title)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(RivuneIOSTheme.primaryText)
                    .rivuneIOSDynamicTitle(standardLimit: 2)
                    .frame(width: width, alignment: .topLeading)
                }
              }
              .buttonStyle(.plain)
              .accessibilityHint(item.reason)
            }
          }
        }
      }
    }

    private var posterWidth: CGFloat { min(max(availableWidth / 2.65, 124), 176) }
    private var landscapeWidth: CGFloat { min(max((availableWidth - 14) / 1.45, 230), 360) }
  }

  private struct RivuneIOSDownloadRail: View {
    let items: [RivuneOfflineMediaItem]
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat

    var body: some View {
      VStack(alignment: .leading, spacing: 14) {
        RivuneIOSSectionHeader(title: "Downloads", subtitle: "Available offline")
        ScrollView(.horizontal, showsIndicators: false) {
          LazyHStack(alignment: .top, spacing: 14) {
            ForEach(items) { item in
              let width = min(max((availableWidth - 14) / 1.45, 230), 360)
              VStack(alignment: .leading, spacing: 8) {
                Button {
                  model.playOffline(item)
                } label: {
                  ZStack {
                    RivuneIOSTheme.raised
                    Image(systemName: "play.circle.fill")
                      .font(.system(size: 42))
                      .foregroundStyle(RivuneIOSTheme.primaryText)
                  }
                  .aspectRatio(16 / 9, contentMode: .fit)
                  .frame(width: width)
                  .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                  .overlay {
                    RoundedRectangle(cornerRadius: 14).stroke(RivuneIOSTheme.hairline, lineWidth: 1)
                  }
                }
                .buttonStyle(.plain)
                Text(item.title)
                  .font(.subheadline.weight(.semibold))
                  .foregroundStyle(RivuneIOSTheme.primaryText)
                  .rivuneIOSDynamicTitle(standardLimit: 1)
                  .frame(width: width, alignment: .leading)
                HStack {
                  Text(ByteCountFormatter.string(fromByteCount: item.sizeBytes, countStyle: .file))
                  Spacer()
                  Button(role: .destructive) {
                    model.removeOffline(item)
                  } label: {
                    Image(systemName: "trash")
                      .frame(width: 36, height: 36)
                  }
                  .buttonStyle(.plain)
                  .foregroundStyle(RivuneIOSTheme.danger)
                  .accessibilityLabel(rivuneLocalized("Delete download"))
                }
                .font(.caption)
                .foregroundStyle(RivuneIOSTheme.mutedText)
                .frame(width: width)
              }
            }
          }
        }
      }
    }
  }

  private struct RivuneIOSCollectionRail: View {
    let collection: Collection
    @ObservedObject var model: RivuneAppModel
    let availableWidth: CGFloat
    let viewAll: () -> Void

    var body: some View {
      VStack(alignment: .leading, spacing: 14) {
        RivuneIOSSectionHeader(title: collection.title, actionTitle: "View all", action: viewAll)
        ScrollView(.horizontal, showsIndicators: false) {
          LazyHStack(alignment: .top, spacing: 14) {
            ForEach(collection.folders) { folder in
              let shape = rivuneIOSEffectiveShape(collection: collection, folder: folder)
              let width = rivuneIOSTileWidth(shape, availableWidth: availableWidth)
              Button {
                model.openFolder(in: collection, folder: folder)
              } label: {
                RivuneIOSFolderCard(
                  folder: folder,
                  shape: shape,
                  width: width,
                  imageURL: model.folderArtworkURL(for: folder)
                )
              }
              .buttonStyle(.plain)
              .disabled(folder.id == nil)
              .opacity(folder.id == nil ? 0.45 : 1)
            }
          }
          .padding(.vertical, 2)
        }
      }
    }
  }

  private struct RivuneIOSFolderCard: View {
    let folder: CollectionFolder
    let shape: CollectionTileShape
    let width: CGFloat
    let imageURL: URL?

    var body: some View {
      VStack(alignment: .center, spacing: 8) {
        AsyncImage(url: imageURL) { phase in
          if let image = phase.image {
            image.resizable().scaledToFill()
          } else {
            ZStack {
              RivuneIOSTheme.raised
              if let emoji = folder.coverEmoji, !emoji.isEmpty {
                Text(emoji).font(.system(size: 38))
              } else {
                Image(systemName: "rectangle.stack.fill")
                  .font(.system(size: 28))
                  .foregroundStyle(RivuneIOSTheme.mutedText)
              }
            }
          }
        }
        .frame(width: width, height: height)
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay {
          RoundedRectangle(cornerRadius: 14).stroke(RivuneIOSTheme.hairline, lineWidth: 1)
        }
        if !folder.hideTitle {
          RivuneIOSTileTitle(title: folder.title, width: width, centered: true)
        }
      }
      .accessibilityElement(children: .combine)
    }

    private var height: CGFloat {
      switch shape {
      case .poster: return width * 1.5
      case .landscape: return width * 9 / 16
      case .square: return width
      }
    }
  }

  private struct RivuneIOSCollectionFolderTile: View {
    let folder: CollectionFolder
    let imageURL: URL?

    var body: some View {
      ZStack(alignment: .bottomLeading) {
        AsyncImage(url: imageURL) { phase in
          if let image = phase.image {
            image.resizable().scaledToFill()
          } else {
            ZStack {
              RivuneIOSTheme.raised
              if let emoji = folder.coverEmoji, !emoji.isEmpty {
                Text(emoji).font(.system(size: 34))
              } else {
                Image(systemName: "folder.fill")
                  .font(.system(size: 26, weight: .medium))
                  .foregroundStyle(RivuneIOSTheme.mutedText)
              }
            }
          }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .clipped()

        LinearGradient(
          colors: [.clear, Color.black.opacity(0.78)],
          startPoint: .center,
          endPoint: .bottom
        )

        HStack(alignment: .center, spacing: 8) {
          if !folder.hideTitle {
            Text(folder.title)
              .font(.subheadline.weight(.semibold))
              .foregroundStyle(RivuneIOSTheme.primaryText)
              .lineLimit(1)
          }
          Spacer(minLength: 0)
          Image(systemName: "chevron.right")
            .font(.caption.weight(.bold))
            .foregroundStyle(RivuneIOSTheme.secondaryText)
        }
        .padding(12)
      }
      .aspectRatio(16 / 9, contentMode: .fit)
      .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
      .overlay {
        RoundedRectangle(cornerRadius: 16, style: .continuous)
          .stroke(RivuneIOSTheme.hairline, lineWidth: 1)
      }
      .contentShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
      .accessibilityElement(children: .combine)
    }
  }

  struct RivuneIOSCollectionOverviewView: View {
    let collection: Collection
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
      GeometryReader { proxy in
        let columns = Array(
          repeating: GridItem(.flexible(), spacing: 14),
          count: proxy.size.width < 700 ? 2 : 3
        )
        ZStack {
          RivuneIOSCanvas()
          RivuneIOSPage(maximumWidth: 1200) {
            VStack(alignment: .leading, spacing: 24) {
              HStack {
                Button(action: { dismiss() }) { Image(systemName: "chevron.left") }
                  .rivuneIOSIconButton()
                  .accessibilityLabel(rivuneLocalized("Back"))
                Spacer()
              }
              RivuneIOSHeading(eyebrow: model.serverName, title: collection.title)
              LazyVGrid(columns: columns, alignment: .leading, spacing: 14) {
                ForEach(collection.folders) { folder in
                  Button {
                    model.openFolder(in: collection, folder: folder)
                  } label: {
                    RivuneIOSCollectionFolderTile(
                      folder: folder,
                      imageURL: model.folderArtworkURL(for: folder)
                    )
                  }
                  .buttonStyle(.plain)
                  .disabled(folder.id == nil)
                  .opacity(folder.id == nil ? 0.45 : 1)
                }
              }
            }
          }
        }
      }
      .fullScreenCover(
        item: Binding(
          get: { model.openedFolder },
          set: { if $0 == nil { model.closeFolder() } }
        ), onDismiss: model.closeFolder
      ) { _ in
        RivuneIOSFolderView(model: model)
      }
    }

  }

  struct RivuneIOSFolderView: View {
    @ObservedObject var model: RivuneAppModel
    @Environment(\.dismiss) private var dismiss
    @State private var mediaFilter: String?
    @State private var selectedSourceID: UUID?

    var body: some View {
      GeometryReader { proxy in
        ZStack {
          RivuneIOSCanvas()
          RivuneIOSPage(maximumWidth: 1200) {
            VStack(alignment: .leading, spacing: 24) {
              HStack {
                Button(action: navigateBack) { Image(systemName: "chevron.left") }
                  .rivuneIOSIconButton()
                  .accessibilityLabel(rivuneLocalized(backLabel))
                Spacer()
              }
              if let opened = model.openedFolder {
                contents(opened, viewport: proxy.size.width)
              }
            }
          }
        }
      }
      .onChange(of: model.openedFolder?.id) { _ in resetFilters() }
      .onChange(of: model.openedFolder?.folder.sourceView) { sourceView in
        mediaFilter = nil
        selectedSourceID =
          sourceView == .categories
          ? model.openedFolder.flatMap { $0.folder.sources.compactMap(\.id).first }
          : nil
      }
      .fullScreenCover(
        isPresented: Binding(
          get: { model.mediaLoading || model.mediaDetail != nil || model.mediaFailure != nil },
          set: { if !$0 { model.closeMedia() } }
        )
      ) {
        RivuneIOSMediaDetailView(model: model)
      }
    }

    @ViewBuilder
    private func contents(_ opened: OpenedCollectionFolder, viewport: CGFloat) -> some View {
      let sources = opened.folder.sources.filter { $0.id != nil }
      let sourceView = sources.count > 1 ? opened.folder.sourceView ?? .merged : .merged
      let effectiveSourceID =
        sourceView == .categories ? selectedSourceID ?? sources.first?.id : selectedSourceID
      let browsingFolders = sourceView == .folders && effectiveSourceID == nil
      let scoped = opened.items?.filter { item in
        guard let effectiveSourceID else { return true }
        return item.sources.contains { $0.id == effectiveSourceID }
      }

      RivuneIOSHeading(
        eyebrow: model.serverName,
        title: effectiveSourceID.flatMap { id in
          sources.first { $0.id == id }.map(rivuneIOSSourceLabel)
        } ?? opened.folder.title,
        message: !browsingFolders && scoped?.isEmpty == true
          ? "This folder contains no visible titles." : nil
      )

      if model.isBusy && opened.items == nil {
        RivuneIOSStatusView(state: .loading("Loading titles…"))
      } else if let failure = model.failure {
        RivuneIOSStatusView(state: .failure(failure))
      }

      if let items = opened.items {
        if sourceView == .categories {
          chipRow(sources: sources, selected: effectiveSourceID)
        }
        if browsingFolders {
          sourceFolders(sources: sources, opened: opened, items: items, viewport: viewport)
        } else {
          if rivuneIOSSourcesSupportFilter(
            effectiveSourceID.flatMap { id in sources.first { $0.id == id }.map { [$0] } }
              ?? sources, items: scoped ?? [])
          {
            mediaFilterRow
          }
          mediaGrid(items: scoped ?? [], shape: opened.folder.tileShape, viewport: viewport)
        }
        if !opened.errors.isEmpty {
          Label(
            "Some collection sources were unavailable.",
            systemImage: "exclamationmark.triangle.fill"
          )
          .font(.footnote)
          .foregroundStyle(RivuneIOSTheme.warning)
          .padding(14)
          .frame(maxWidth: .infinity, alignment: .leading)
          .background(RivuneIOSTheme.warning.opacity(0.08), in: RoundedRectangle(cornerRadius: 14))
        }
        if opened.hasMore {
          Button(action: model.loadMoreFolderItems) {
            HStack {
              if model.isBusy { ProgressView().tint(.black) }
              Label("Load more", systemImage: "arrow.down.circle")
            }
            .frame(maxWidth: .infinity)
          }
          .rivuneIOSPrimaryButton()
          .disabled(model.isBusy)
        }
      }
    }

    private func chipRow(sources: [CollectionSource], selected: UUID?) -> some View {
      ScrollView(.horizontal, showsIndicators: false) {
        HStack(spacing: 8) {
          ForEach(sources) { source in
            Button {
              selectedSourceID = source.id
              mediaFilter = nil
            } label: {
              RivuneIOSChip(
                title: rivuneIOSSourceLabel(source),
                icon: rivuneIOSSourceIcon(source),
                selected: source.id == selected
              )
            }
            .buttonStyle(.plain)
          }
        }
      }
    }

    private var mediaFilterRow: some View {
      ScrollView(.horizontal, showsIndicators: false) {
        HStack(spacing: 8) {
          filterChip("All", value: nil, icon: "rectangle.stack.fill")
          filterChip("Movies", value: "movie", icon: "film.fill")
          filterChip("Series", value: "series", icon: "tv.fill")
        }
      }
    }

    private func filterChip(_ title: String, value: String?, icon: String) -> some View {
      Button {
        mediaFilter = value
      } label: {
        RivuneIOSChip(title: title, icon: icon, selected: mediaFilter == value)
      }
      .buttonStyle(.plain)
    }

    private func sourceFolders(
      sources: [CollectionSource],
      opened: OpenedCollectionFolder,
      items: [CollectionItem],
      viewport: CGFloat
    ) -> some View {
      let landscape = opened.folder.tileShape == .landscape
      return LazyVGrid(
        columns: [
          GridItem(
            .adaptive(
              minimum: landscape ? 240 : RivuneIOSTheme.gridMinimum(for: viewport),
              maximum: landscape ? 330 : 210), spacing: 18)
        ],
        alignment: .leading,
        spacing: 22
      ) {
        ForEach(sources) { source in
          let sourceItems = items.filter { item in item.sources.contains { $0.id == source.id } }
          let key = source.id?.uuidString.lowercased()
          let art =
            key.flatMap { opened.sourcePosterUrls?[$0] }
            ?? sourceItems.compactMap { $0.posterUrl ?? $0.backgroundUrl }.first
          Button {
            selectedSourceID = source.id
            mediaFilter = nil
          } label: {
            VStack(alignment: .center, spacing: 8) {
              RivuneIOSArtwork(
                url: art.flatMap(model.resolvedResourceURL),
                aspectRatio: landscape ? 16 / 9 : 2 / 3,
                fallbackSystemImage: "folder.fill"
              )
              RivuneIOSTileTitle(title: rivuneIOSSourceLabel(source), centered: true)
                .frame(maxWidth: .infinity, alignment: .top)
            }
          }
          .buttonStyle(.plain)
        }
      }
    }

    @ViewBuilder
    private func mediaGrid(items: [CollectionItem], shape: CollectionTileShape, viewport: CGFloat)
      -> some View
    {
      let visible =
        mediaFilter.map { filter in
          items.filter {
            filter == "series" ? ["series", "tv"].contains($0.mediaType) : $0.mediaType == filter
          }
        } ?? items
      let movies = visible.filter { $0.mediaType == "movie" }
      let series = visible.filter { ["series", "tv"].contains($0.mediaType) }
      let other = visible.filter {
        $0.mediaType != "movie" && !["series", "tv"].contains($0.mediaType)
      }

      if mediaFilter == nil, !movies.isEmpty, !series.isEmpty {
        mediaSection("Movies", items: movies, shape: shape, viewport: viewport)
        mediaSection("Series", items: series, shape: shape, viewport: viewport)
        if !other.isEmpty { mediaTileGrid(items: other, shape: shape, viewport: viewport) }
      } else if visible.isEmpty {
        RivuneIOSStatusView(
          state: .empty(icon: "rectangle.stack.badge.minus", title: "No titles", message: nil))
      } else {
        mediaTileGrid(items: visible, shape: shape, viewport: viewport)
      }
    }

    private func mediaSection(
      _ title: String, items: [CollectionItem], shape: CollectionTileShape, viewport: CGFloat
    ) -> some View {
      VStack(alignment: .leading, spacing: 14) {
        RivuneIOSSectionHeader(title: title)
        mediaTileGrid(items: items, shape: shape, viewport: viewport)
      }
    }

    private func mediaTileGrid(
      items: [CollectionItem], shape: CollectionTileShape, viewport: CGFloat
    ) -> some View {
      let landscape = shape == .landscape
      return LazyVGrid(
        columns: [
          GridItem(
            .adaptive(
              minimum: landscape ? 240 : RivuneIOSTheme.gridMinimum(for: viewport),
              maximum: landscape ? 340 : 210), spacing: 18)
        ],
        alignment: .leading,
        spacing: 22
      ) {
        ForEach(items) { item in
          let artwork =
            landscape ? item.backgroundUrl ?? item.posterUrl : item.posterUrl ?? item.backgroundUrl
          Button {
            model.openMedia(item)
          } label: {
            VStack(alignment: .leading, spacing: 8) {
              RivuneIOSArtwork(
                url: artwork.flatMap(model.resolvedResourceURL),
                aspectRatio: rivuneIOSAspectRatio(shape),
                fallbackSystemImage: ["series", "tv"].contains(item.mediaType) ? "tv" : "film"
              )
              RivuneIOSTileTitle(title: item.title)
              if let releaseInfo = rivuneIOSMeaningfulSubtitle(item.releaseInfo) {
                Text(releaseInfo)
                  .font(.caption)
                  .foregroundStyle(RivuneIOSTheme.mutedText)
                  .lineLimit(1)
              }
            }
          }
          .buttonStyle(.plain)
        }
      }
    }

    private var backLabel: String {
      guard let opened = model.openedFolder,
        opened.folder.sourceView == .folders,
        selectedSourceID != nil
      else { return rivuneLocalized("Library") }
      return opened.folder.title
    }

    private func navigateBack() {
      if model.openedFolder?.folder.sourceView == .folders, selectedSourceID != nil {
        selectedSourceID = nil
        mediaFilter = nil
      } else {
        model.closeFolder()
        dismiss()
      }
    }

    private func resetFilters() {
      mediaFilter = nil
      selectedSourceID = nil
    }
  }

  private func rivuneIOSMeaningfulSubtitle(_ value: String?) -> String? {
    guard let value else { return nil }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return nil }
    if trimmed.count == 4, let year = Int(trimmed), (1800...2200).contains(year) { return nil }
    return trimmed
  }

  private func rivuneIOSEffectiveShape(collection: Collection, folder: CollectionFolder)
    -> CollectionTileShape
  {
    collection.viewMode == .followLayout ? folder.tileShape : collection.folderCoverShape
  }

  private func rivuneIOSTileWidth(_ shape: CollectionTileShape, availableWidth: CGFloat) -> CGFloat
  {
    switch shape {
    case .poster: return min(max(availableWidth / 2.65, 124), 176)
    case .landscape: return min(max((availableWidth - 14) / 1.45, 230), 360)
    case .square: return min(max(availableWidth / 2.4, 136), 190)
    }
  }

  private func rivuneIOSAspectRatio(_ shape: CollectionTileShape) -> CGFloat {
    switch shape {
    case .poster: return 2 / 3
    case .landscape: return 16 / 9
    case .square: return 1
    }
  }

  private let rivuneIOSMovieNames: Set<String> = ["movie", "movies", "film", "films"]
  private let rivuneIOSSeriesNames: Set<String> = [
    "series", "tv", "show", "shows", "tv show", "tv shows", "série", "séries",
  ]

  private func rivuneIOSSourceMediaType(_ source: CollectionSource) -> String? {
    if let value = source.tmdb?.mediaType.rawValue { return value }
    if let value = source.trakt?.mediaType.rawValue { return value }
    if let value = source.mdblist?.mediaType.rawValue { return value }
    return source.addonCatalog?.type.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
  }

  private func rivuneIOSSourceLabel(_ source: CollectionSource) -> String {
    let type = rivuneIOSSourceMediaType(source)
    let title = source.title.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if type == "movie", rivuneIOSMovieNames.contains(title) { return rivuneLocalized("Movies") }
    if type.map({ ["series", "tv"].contains($0) }) == true, rivuneIOSSeriesNames.contains(title) {
      return rivuneLocalized("Series")
    }
    return source.title
  }

  private func rivuneIOSSourceIcon(_ source: CollectionSource) -> String {
    let type = rivuneIOSSourceMediaType(source)
    let title = source.title.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if type == "movie" || rivuneIOSMovieNames.contains(title) { return "film.fill" }
    if type.map({ ["series", "tv"].contains($0) }) == true || rivuneIOSSeriesNames.contains(title) {
      return "tv.fill"
    }
    return "rectangle.stack.fill"
  }

  private func rivuneIOSSourcesSupportFilter(_ sources: [CollectionSource], items: [CollectionItem])
    -> Bool
  {
    if sources.contains(where: { rivuneIOSSourceMediaType($0) == "both" }) { return true }
    let sourceTypes = Set(sources.compactMap(rivuneIOSSourceMediaType))
    let itemTypes = Set(items.map(\.mediaType))
    return sourceTypes.contains("movie") && !sourceTypes.isDisjoint(with: ["series", "tv"])
      || itemTypes.contains("movie") && !itemTypes.isDisjoint(with: ["series", "tv"])
  }
#endif
