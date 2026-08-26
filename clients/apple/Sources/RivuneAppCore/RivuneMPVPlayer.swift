import Combine
import Foundation
import Libmpv
import QuartzCore
import SwiftUI

#if canImport(UIKit)
import AVFoundation
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

final class RivuneMPVPlaybackController: ObservableObject {
    @Published private(set) var position = 0.0
    @Published private(set) var duration = 0.0
    @Published private(set) var playing = false
    @Published private(set) var buffering = false
    @Published private(set) var ended = false
    @Published private(set) var failureMessage: String?

    private struct LoadRequest {
        let url: URL
        let startSeconds: Int
        let timelineOffsetSeconds: Double
        let durationSeconds: Int?
        let selectedAudioTrack: Int?
        let selectedSubtitleURL: URL?

        var mediaStartSeconds: Int {
            max(Int(Double(startSeconds) - timelineOffsetSeconds), 0)
        }
    }

    private final class WakeupContext {
        weak var controller: RivuneMPVPlaybackController?

        init(controller: RivuneMPVPlaybackController) {
            self.controller = controller
        }
    }

    private let queue = DispatchQueue(label: "io.rivune.player.mpv", qos: .userInitiated)
    private var handle: OpaquePointer?
    private var surface: CAMetalLayer?
    private var wakeupContext: UnsafeMutableRawPointer?
    private var pendingLoad: LoadRequest?
    private var currentLoad: LoadRequest?
    private var shuttingDown = false

    deinit {
        queue.sync { tearDownLocked() }
    }

    func attach(to surface: CAMetalLayer) {
        queue.async { [weak self] in
            guard let self, !self.shuttingDown else { return }
            if self.surface === surface, self.handle != nil { return }
            self.tearDownLocked()
            self.surface = surface
            guard self.setUpLocked(surface: surface) else { return }
            if let request = self.pendingLoad {
                self.loadLocked(request)
            }
        }
    }

    func detach(from surface: CAMetalLayer) {
        queue.sync {
            guard self.surface === surface else { return }
            tearDownLocked()
            self.surface = nil
        }
    }

    func load(
        url: URL,
        startSeconds: Int,
        timelineOffsetSeconds: Double,
        durationSeconds: Int?,
        selectedAudioTrack: Int?,
        selectedSubtitleURL: URL?
    ) {
        let request = LoadRequest(
            url: url,
            startSeconds: max(startSeconds, 0),
            timelineOffsetSeconds: max(timelineOffsetSeconds, 0),
            durationSeconds: durationSeconds,
            selectedAudioTrack: selectedAudioTrack,
            selectedSubtitleURL: selectedSubtitleURL
        )
        publish {
            $0.position = Double(request.startSeconds)
            $0.duration = Double(max(request.durationSeconds ?? 0, 0))
            $0.ended = false
            $0.failureMessage = nil
        }
        queue.async { [weak self] in
            guard let self, !self.shuttingDown else { return }
            self.pendingLoad = request
            guard self.handle != nil else { return }
            self.loadLocked(request)
        }
    }

    func shutdown() {
        queue.sync {
            shuttingDown = true
            pendingLoad = nil
            currentLoad = nil
            tearDownLocked()
            surface = nil
        }
    }

    func play() { setFlag("pause", false) }
    func pause() { setFlag("pause", true) }

    func seek(to seconds: Double) {
        guard seconds.isFinite else { return }
        queue.async { [weak self] in
            guard let self, let handle = self.handle else { return }
            var mediaSeconds = max(seconds - (self.currentLoad?.timelineOffsetSeconds ?? 0), 0)
            let status = mpv_set_property(handle, "time-pos", MPV_FORMAT_DOUBLE, &mediaSeconds)
            if status < 0 { self.publishFailure(self.errorMessage(status, key: "MPV could not set %@: %@.", parameter: "time-pos")) }
        }
    }

    func setSpeed(_ speed: Double) {
        guard speed.isFinite, speed > 0 else { return }
        setDouble("speed", speed)
    }

    func setAspect(_ aspect: RivuneVideoAspect) {
        setDouble("panscan", aspect == .fill ? 1 : 0)
    }

    func selectAudio(streamIndex: Int) {
        queue.async { [weak self] in
            guard let self, let handle = self.handle else { return }
            let count = Int(self.int64Property("track-list/count", handle: handle) ?? 0)
            for index in 0..<count {
                guard self.stringProperty("track-list/\(index)/type", handle: handle) == "audio",
                      self.int64Property("track-list/\(index)/ff-index", handle: handle) == Int64(streamIndex),
                      let id = self.int64Property("track-list/\(index)/id", handle: handle) else { continue }
                self.setStringLocked("aid", String(id), handle: handle)
                return
            }
            self.publishFailure(rivuneLocalizedFormat("MPV could not select audio stream %d.", streamIndex))
        }
    }

    func selectSubtitle(url: URL?, title: String? = nil, language: String? = nil) {
        queue.async { [weak self] in
            guard let self, let handle = self.handle else { return }
            guard let url else {
                self.setStringLocked("sid", "no", handle: handle)
                return
            }
            var arguments = ["sub-add", url.absoluteString, "select"]
            if let title, !title.isEmpty { arguments.append(title) }
            if let language, !language.isEmpty {
                if arguments.count == 3 { arguments.append(language.uppercased()) }
                arguments.append(language)
            }
            let status = self.commandLocked(arguments, handle: handle)
            if status < 0 { self.publishFailure(self.errorMessage(status, key: "MPV could not load subtitles: %@.")) }
        }
    }

    private func setUpLocked(surface: CAMetalLayer) -> Bool {
        guard let handle = mpv_create() else {
            publishFailure(rivuneLocalized("MPV could not create a playback context."))
            return false
        }

        var windowID = Int64(bitPattern: UInt64(UInt(bitPattern: Unmanaged.passUnretained(surface).toOpaque())))
        let options: [(String, String)] = [
            ("vo", "gpu-next"),
            ("gpu-api", "vulkan"),
            ("gpu-context", "moltenvk"),
            ("hwdec", "videotoolbox"),
            ("ao", "avfoundation"),
            ("subs-match-os-language", "yes"),
            ("subs-fallback", "yes"),
            ("keep-open", "no"),
        ]
        for (name, value) in options {
            let status = mpv_set_option_string(handle, name, value)
            guard status >= 0 else {
                mpv_terminate_destroy(handle)
                publishFailure(errorMessage(status, key: "MPV could not configure %@: %@.", parameter: name))
                return false
            }
        }
        let windowStatus = mpv_set_option(handle, "wid", MPV_FORMAT_INT64, &windowID)
        guard windowStatus >= 0 else {
            mpv_terminate_destroy(handle)
            publishFailure(errorMessage(windowStatus, key: "MPV could not attach the video surface: %@."))
            return false
        }
        let initializeStatus = mpv_initialize(handle)
        guard initializeStatus >= 0 else {
            mpv_terminate_destroy(handle)
            publishFailure(errorMessage(initializeStatus, key: "MPV could not initialize: %@."))
            return false
        }

        self.handle = handle
        observe("time-pos", format: MPV_FORMAT_DOUBLE, handle: handle)
        observe("duration", format: MPV_FORMAT_DOUBLE, handle: handle)
        observe("pause", format: MPV_FORMAT_FLAG, handle: handle)
        observe("paused-for-cache", format: MPV_FORMAT_FLAG, handle: handle)

        let context = Unmanaged.passRetained(WakeupContext(controller: self)).toOpaque()
        wakeupContext = context
        mpv_set_wakeup_callback(handle, { rawContext in
            guard let rawContext else { return }
            let context = Unmanaged<WakeupContext>.fromOpaque(rawContext).takeUnretainedValue()
            context.controller?.enqueueEventRead()
        }, context)
        return true
    }

    private func loadLocked(_ request: LoadRequest) {
        guard let handle else { return }
        currentLoad = request
        var arguments = ["loadfile", request.url.absoluteString, "replace"]
        if request.mediaStartSeconds > 0 { arguments += ["-1", "start=\(request.mediaStartSeconds)"] }
        let status = commandLocked(arguments, handle: handle)
        if status < 0 { publishFailure(errorMessage(status, key: "MPV could not open the media URL: %@.")) }
    }

    private func enqueueEventRead() {
        queue.async { [weak self] in self?.readEventsLocked() }
    }

    private func readEventsLocked() {
        guard let handle else { return }
        while let event = mpv_wait_event(handle, 0), event.pointee.event_id != MPV_EVENT_NONE {
            switch event.pointee.event_id {
            case MPV_EVENT_FILE_LOADED:
                if let selectedAudioTrack = currentLoad?.selectedAudioTrack {
                    selectAudioLocked(streamIndex: selectedAudioTrack, handle: handle)
                }
                if let selectedSubtitleURL = currentLoad?.selectedSubtitleURL {
                    selectSubtitleLocked(url: selectedSubtitleURL, handle: handle)
                }
            case MPV_EVENT_END_FILE:
                guard let rawData = event.pointee.data else { break }
                let end = rawData.assumingMemoryBound(to: mpv_event_end_file.self).pointee
                if end.reason == MPV_END_FILE_REASON_EOF {
                    publish { $0.ended = true; $0.playing = false }
                } else if end.reason == MPV_END_FILE_REASON_ERROR || end.error < 0 {
                    publishFailure(errorMessage(Int32(end.error), key: "MPV could not play this media: %@."))
                }
            case MPV_EVENT_PROPERTY_CHANGE:
                handlePropertyChange(event.pointee.data)
            case MPV_EVENT_SHUTDOWN:
                return
            default:
                break
            }
        }
    }

    private func handlePropertyChange(_ rawData: UnsafeMutableRawPointer?) {
        guard let rawData else { return }
        let property = rawData.assumingMemoryBound(to: mpv_event_property.self).pointee
        guard let rawName = property.name, let data = property.data else { return }
        switch String(cString: rawName) {
        case "time-pos":
            let value = data.assumingMemoryBound(to: Double.self).pointee
            let offset = currentLoad?.timelineOffsetSeconds ?? 0
            if value.isFinite { publish { $0.position = max(value, 0) + offset } }
        case "duration":
            let value = data.assumingMemoryBound(to: Double.self).pointee
            let expected = Double(max(currentLoad?.durationSeconds ?? 0, 0))
            let offset = currentLoad?.timelineOffsetSeconds ?? 0
            if expected > 0 { publish { $0.duration = expected } }
            else if value.isFinite, value > 0 { publish { $0.duration = value + offset } }
        case "pause":
            let paused = data.assumingMemoryBound(to: Int32.self).pointee != 0
            publish { $0.playing = !paused }
        case "paused-for-cache":
            let buffering = data.assumingMemoryBound(to: Int32.self).pointee != 0
            publish { $0.buffering = buffering }
        default:
            break
        }
    }

    private func selectSubtitleLocked(url: URL, handle: OpaquePointer) {
        let status = commandLocked(["sub-add", url.absoluteString, "select"], handle: handle)
        if status < 0 { publishFailure(errorMessage(status, key: "MPV could not load subtitles: %@.")) }
    }

    private func selectAudioLocked(streamIndex: Int, handle: OpaquePointer) {
        let count = Int(int64Property("track-list/count", handle: handle) ?? 0)
        for index in 0..<count {
            guard stringProperty("track-list/\(index)/type", handle: handle) == "audio",
                  int64Property("track-list/\(index)/ff-index", handle: handle) == Int64(streamIndex),
                  let id = int64Property("track-list/\(index)/id", handle: handle) else { continue }
            setStringLocked("aid", String(id), handle: handle)
            return
        }
    }

    private func setFlag(_ name: String, _ value: Bool) {
        queue.async { [weak self] in
            guard let self, let handle = self.handle else { return }
            var flag: Int32 = value ? 1 : 0
            let status = mpv_set_property(handle, name, MPV_FORMAT_FLAG, &flag)
            if status < 0 { self.publishFailure(self.errorMessage(status, key: "MPV could not set %@: %@.", parameter: name)) }
        }
    }

    private func setDouble(_ name: String, _ value: Double) {
        queue.async { [weak self] in
            guard let self, let handle = self.handle else { return }
            var value = value
            let status = mpv_set_property(handle, name, MPV_FORMAT_DOUBLE, &value)
            if status < 0 { self.publishFailure(self.errorMessage(status, key: "MPV could not set %@: %@.", parameter: name)) }
        }
    }

    private func setStringLocked(_ name: String, _ value: String, handle: OpaquePointer) {
        let status = mpv_set_property_string(handle, name, value)
        if status < 0 { publishFailure(errorMessage(status, key: "MPV could not set %@: %@.", parameter: name)) }
    }

    private func observe(_ name: String, format: mpv_format, handle: OpaquePointer) {
        let status = mpv_observe_property(handle, 0, name, format)
        if status < 0 { publishFailure(errorMessage(status, key: "MPV could not observe %@: %@.", parameter: name)) }
    }

    private func int64Property(_ name: String, handle: OpaquePointer) -> Int64? {
        var value: Int64 = 0
        return mpv_get_property(handle, name, MPV_FORMAT_INT64, &value) >= 0 ? value : nil
    }

    private func stringProperty(_ name: String, handle: OpaquePointer) -> String? {
        guard let value = mpv_get_property_string(handle, name) else { return nil }
        defer { mpv_free(value) }
        return String(cString: value)
    }

    private func commandLocked(_ arguments: [String], handle: OpaquePointer) -> Int32 {
        let allocated = arguments.map { strdup($0) }
        defer { allocated.forEach { free($0) } }
        var pointers: [UnsafePointer<CChar>?] = allocated.map { pointer in
            pointer.map { UnsafePointer<CChar>($0) }
        }
        pointers.append(nil)
        return mpv_command(handle, &pointers)
    }

    private func tearDownLocked() {
        guard let handle else {
            releaseWakeupContext()
            return
        }
        mpv_set_wakeup_callback(handle, nil, nil)
        mpv_terminate_destroy(handle)
        self.handle = nil
        currentLoad = nil
        releaseWakeupContext()
    }

    private func releaseWakeupContext() {
        guard let wakeupContext else { return }
        Unmanaged<WakeupContext>.fromOpaque(wakeupContext).release()
        self.wakeupContext = nil
    }

    private func errorMessage(_ status: Int32, key: String, parameter: String? = nil) -> String {
        let detail = mpv_error_string(status).map(String.init(cString:)) ?? rivuneLocalized("unknown error")
        return parameter.map { rivuneLocalizedFormat(key, $0, detail) } ?? rivuneLocalizedFormat(key, detail)
    }

    private func publishFailure(_ message: String) {
        publish { $0.failureMessage = message; $0.playing = false }
    }

    private func publish(_ update: @escaping (RivuneMPVPlaybackController) -> Void) {
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            update(self)
        }
    }
}

final class RivuneMPVMetalLayer: CAMetalLayer {
    override var drawableSize: CGSize {
        get { super.drawableSize }
        set {
            guard newValue.width > 1, newValue.height > 1 else { return }
            super.drawableSize = newValue
        }
    }

#if !os(tvOS)
    @available(iOS 16.0, *)
    override var wantsExtendedDynamicRangeContent: Bool {
        get { super.wantsExtendedDynamicRangeContent }
        set {
            if Thread.isMainThread {
                super.wantsExtendedDynamicRangeContent = newValue
            } else {
                DispatchQueue.main.sync { super.wantsExtendedDynamicRangeContent = newValue }
            }
        }
    }
#endif
}

#if canImport(UIKit)
final class RivuneMPVSurfaceView: UIView {
    override class var layerClass: AnyClass { RivuneMPVMetalLayer.self }
    var metalLayer: CAMetalLayer { layer as! CAMetalLayer }
    weak var playbackController: RivuneMPVPlaybackController?

    override init(frame: CGRect) {
        super.init(frame: frame)
        metalLayer.framebufferOnly = true
        metalLayer.backgroundColor = UIColor.black.cgColor
#if !os(visionOS)
        metalLayer.contentsScale = UIScreen.main.nativeScale
#endif
    }

    required init?(coder: NSCoder) { nil }

    override func layoutSubviews() {
#if os(visionOS)
        metalLayer.contentsScale = traitCollection.displayScale
#endif
        super.layoutSubviews()
        metalLayer.drawableSize = CGSize(width: bounds.width * metalLayer.contentsScale, height: bounds.height * metalLayer.contentsScale)
    }
}

struct RivuneMPVPlayerSurface: UIViewRepresentable {
    @ObservedObject var controller: RivuneMPVPlaybackController
    let aspect: RivuneVideoAspect

    func makeUIView(context: Context) -> RivuneMPVSurfaceView {
        let view = RivuneMPVSurfaceView(frame: .zero)
        view.playbackController = controller
        controller.attach(to: view.metalLayer)
        controller.setAspect(aspect)
        return view
    }

    func updateUIView(_ view: RivuneMPVSurfaceView, context: Context) {
        controller.setAspect(aspect)
    }

    static func dismantleUIView(_ view: RivuneMPVSurfaceView, coordinator: ()) {
        view.playbackController?.detach(from: view.metalLayer)
    }
}
#elseif canImport(AppKit)
final class RivuneMPVSurfaceView: NSView {
    let metalLayer = RivuneMPVMetalLayer()
    weak var playbackController: RivuneMPVPlaybackController?

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        layer = metalLayer
        metalLayer.framebufferOnly = true
        metalLayer.backgroundColor = NSColor.black.cgColor
    }

    required init?(coder: NSCoder) { nil }

    override func layout() {
        super.layout()
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 1
        metalLayer.contentsScale = scale
        metalLayer.frame = bounds
        metalLayer.drawableSize = CGSize(width: bounds.width * scale, height: bounds.height * scale)
    }
}

struct RivuneMPVPlayerSurface: NSViewRepresentable {
    @ObservedObject var controller: RivuneMPVPlaybackController
    let aspect: RivuneVideoAspect

    func makeNSView(context: Context) -> RivuneMPVSurfaceView {
        let view = RivuneMPVSurfaceView(frame: .zero)
        view.playbackController = controller
        controller.attach(to: view.metalLayer)
        controller.setAspect(aspect)
        return view
    }

    func updateNSView(_ view: RivuneMPVSurfaceView, context: Context) {
        controller.setAspect(aspect)
    }

    static func dismantleNSView(_ view: RivuneMPVSurfaceView, coordinator: ()) {
        view.playbackController?.detach(from: view.metalLayer)
    }
}
#endif
