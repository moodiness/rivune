import Foundation
import UserNotifications
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

@MainActor
protocol RivuneAppleUpdateNotifying: AnyObject {
    func deliver(_ update: RivuneAppleUpdate) async -> Bool
    func requestPermission() async
}

func rivuneShouldPresentUpdateNotice(lastVersion: String?, candidateVersion: String) -> Bool {
    guard let candidate = RivuneSemanticVersion(candidateVersion) else { return false }
    guard let lastVersion else { return true }
    guard let last = RivuneSemanticVersion(lastVersion) else { return false }
    return last < candidate
}

@MainActor
final class RivuneAppleLocalUpdateNotifier: NSObject, RivuneAppleUpdateNotifying, UNUserNotificationCenterDelegate {
    private let center: UNUserNotificationCenter?

    override init() {
        if Bundle.main.bundleURL.pathExtension == "app" {
            center = .current()
        } else {
            center = nil
        }
        super.init()
        #if !os(tvOS)
        center?.delegate = self
        #endif
    }

    func requestPermission() async {
        #if os(tvOS)
        return
        #else
        guard let center else { return }
        _ = try? await center.requestAuthorization(options: [.alert, .sound])
        #endif
    }

    func deliver(_ update: RivuneAppleUpdate) async -> Bool {
        #if os(tvOS)
        return false
        #else
        guard let center else { return false }
        let settings = await center.notificationSettings()
        guard settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional else {
            return false
        }
        let content = UNMutableNotificationContent()
        content.title = "Rivune \(update.latestVersion) is available"
        content.body = "Open the verified release to review the update. Rivune will not download it automatically."
        content.sound = .default
        content.userInfo = ["releaseURL": update.releaseURL.absoluteString]
        let request = UNNotificationRequest(
            identifier: "app_update:stable:\(RivuneAppleUpdatePlatform.current.rawValue):\(update.latestVersion)",
            content: content,
            trigger: nil
        )
        do {
            try await center.add(request)
            return true
        } catch {
            return false
        }
        #endif
    }

    #if !os(tvOS)
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        defer { completionHandler() }
        guard let raw = response.notification.request.content.userInfo["releaseURL"] as? String,
              let url = URL(string: raw),
              url.scheme == "https",
              url.host == "github.com",
              url.path.hasPrefix("/moodiness/rivune/releases/tag/v") else { return }
        Task { @MainActor in
            #if canImport(UIKit)
            await UIApplication.shared.open(url)
            #elseif canImport(AppKit)
            NSWorkspace.shared.open(url)
            #endif
        }
    }
    #endif
}
