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
            RivuneRootView(interfaceFamily: .desktop)
                .frame(minWidth: 760, idealWidth: 1120, minHeight: 680, idealHeight: 760)
        }
        .windowStyle(.hiddenTitleBar)
    }
}
