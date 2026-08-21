import SwiftUI
import RivuneAppCore

@main
struct RivuneMacApp: App {
    var body: some Scene {
        WindowGroup {
            RivuneRootView()
                .frame(minWidth: 760, minHeight: 560)
        }
        .windowStyle(.hiddenTitleBar)
    }
}
