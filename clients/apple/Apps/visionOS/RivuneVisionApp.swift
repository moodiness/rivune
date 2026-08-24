import SwiftUI
import RivuneAppCore

@main
struct RivuneVisionApp: App {
    var body: some Scene {
        WindowGroup {
            RivuneRootView(interfaceFamily: .spatial)
        }
        .defaultSize(width: 1280, height: 800)
    }
}
