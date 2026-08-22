import SwiftUI
import AppKit
import RivuneAppCore

@main
struct RivuneMacApp: App {
    init() {
        NSApplication.shared.applicationIconImage = NSWorkspace.shared.icon(forFile: Bundle.main.bundlePath)
    }

    var body: some Scene {
        WindowGroup {
            RivuneRootView()
                .frame(minWidth: 760, minHeight: 680)
        }
        .windowStyle(.hiddenTitleBar)
    }
}
