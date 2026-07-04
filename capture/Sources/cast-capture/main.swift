// cast-capture — a ScreenCaptureKit helper for `cast window`.
//
//   cast-capture list
//       Prints capturable on-screen windows as a JSON array to stdout.
//
//   cast-capture stream --window <id> [--fps 30]
//       Captures that window and its audio, encodes H.264/AAC, and writes a
//       fragmented MP4 byte stream to stdout until killed (SIGINT/SIGTERM).
//
// Requires the Screen Recording permission (System Settings ▸ Privacy &
// Security ▸ Screen Recording). ScreenCaptureKit captures the window's audio
// directly, so no virtual audio device is needed.

import AVFoundation
import CoreMedia
import Foundation
import ScreenCaptureKit
import UniformTypeIdentifiers

// MARK: - stderr logging

func warn(_ s: String) {
    FileHandle.standardError.write((s + "\n").data(using: .utf8)!)
}

func fail(_ s: String) -> Never {
    warn("cast-capture: " + s)
    exit(1)
}

// MARK: - shared content

func shareableContent() async -> SCShareableContent {
    do {
        return try await SCShareableContent.excludingDesktopWindows(
            false, onScreenWindowsOnly: true)
    } catch {
        fail("could not read shareable content — is Screen Recording allowed? (\(error.localizedDescription))")
    }
}

// Background apps whose windows are wallpaper/overlays, never something a user
// means to cast.
let systemApps: Set<String> = [
    "WindowManager", "Window Server", "Dock", "Control Center", "Notification Center",
    "SystemUIServer", "Wallpaper", "Spotlight",
]

// Normal, on-screen app windows big enough to be real content: layer 0 (not
// wallpaper/menubar overlays), owned by a foreground-ish app with a name.
func listableWindows(_ content: SCShareableContent) -> [SCWindow] {
    content.windows
        .filter { $0.isOnScreen && $0.windowLayer == 0 }
        .filter { $0.frame.width > 120 && $0.frame.height > 90 }
        .filter {
            guard let app = $0.owningApplication else { return false }
            return !app.applicationName.isEmpty && !systemApps.contains(app.applicationName)
        }
        .sorted { Int($0.windowID) < Int($1.windowID) }
}

func jsonEscape(_ s: String) -> String {
    var out = ""
    for c in s.unicodeScalars {
        switch c {
        case "\"": out += "\\\""
        case "\\": out += "\\\\"
        case "\n": out += "\\n"
        case "\r": out += "\\r"
        case "\t": out += "\\t"
        default:
            if c.value < 0x20 {
                out += String(format: "\\u%04x", c.value)
            } else {
                out.unicodeScalars.append(c)
            }
        }
    }
    return out
}

func cmdList() async {
    let content = await shareableContent()
    var rows: [String] = []
    for w in listableWindows(content) {
        let app = w.owningApplication?.applicationName ?? "?"
        let title = w.title ?? ""
        rows.append(
            "{\"id\":\(w.windowID),\"app\":\"\(jsonEscape(app))\",\"title\":\"\(jsonEscape(title))\",\"width\":\(Int(w.frame.width)),\"height\":\(Int(w.frame.height))}"
        )
    }
    print("[" + rows.joined(separator: ",") + "]")
}

// MARK: - streaming

final class Streamer: NSObject, SCStreamOutput, SCStreamDelegate, AVAssetWriterDelegate {
    private let writer: AVAssetWriter
    private let videoInput: AVAssetWriterInput
    private let audioInput: AVAssetWriterInput
    private let out = FileHandle.standardOutput
    private var started = false
    private let lock = NSLock()
    private var stream: SCStream?

    init(width: Int, height: Int) {
        writer = AVAssetWriter(contentType: UTType.mpeg4Movie)
        // Fragmented MP4: emit ~1s self-contained segments so the byte stream
        // can start playing before capture ends (no final moov needed).
        writer.outputFileTypeProfile = .mpeg4AppleHLS
        writer.preferredOutputSegmentInterval = CMTime(seconds: 1, preferredTimescale: 1)
        writer.initialSegmentStartTime = .zero

        let vSettings: [String: Any] = [
            AVVideoCodecKey: AVVideoCodecType.h264,
            AVVideoWidthKey: width,
            AVVideoHeightKey: height,
            AVVideoCompressionPropertiesKey: [
                AVVideoAverageBitRateKey: max(width * height * 4, 2_000_000),
                AVVideoMaxKeyFrameIntervalKey: 60,
                AVVideoProfileLevelKey: AVVideoProfileLevelH264HighAutoLevel,
            ],
        ]
        videoInput = AVAssetWriterInput(mediaType: .video, outputSettings: vSettings)
        videoInput.expectsMediaDataInRealTime = true

        let aSettings: [String: Any] = [
            AVFormatIDKey: kAudioFormatMPEG4AAC,
            AVSampleRateKey: 48_000,
            AVNumberOfChannelsKey: 2,
            AVEncoderBitRateKey: 128_000,
        ]
        audioInput = AVAssetWriterInput(mediaType: .audio, outputSettings: aSettings)
        audioInput.expectsMediaDataInRealTime = true

        super.init()
        writer.delegate = self
        if writer.canAdd(videoInput) { writer.add(videoInput) }
        if writer.canAdd(audioInput) { writer.add(audioInput) }
    }

    // AVAssetWriterDelegate: each finished fragment is written straight to
    // stdout, forming a continuous fragmented-MP4 stream.
    func assetWriter(_ writer: AVAssetWriter, didOutputSegmentData segmentData: Data,
                     segmentType: AVAssetSegmentType, segmentReport: AVAssetSegmentReport?) {
        out.write(segmentData)
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        fail("capture stopped: \(error.localizedDescription)")
    }

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer,
                of type: SCStreamOutputType) {
        guard CMSampleBufferDataIsReady(sampleBuffer) else { return }

        if type == .screen {
            // Only append frames the compositor marked complete.
            guard let attachments = CMSampleBufferGetSampleAttachmentsArray(
                sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
                let raw = attachments.first?[.status] as? Int,
                let status = SCFrameStatus(rawValue: raw), status == .complete
            else { return }

            lock.lock()
            if !started {
                writer.startWriting()
                writer.startSession(atSourceTime: CMSampleBufferGetPresentationTimeStamp(sampleBuffer))
                started = true
            }
            lock.unlock()

            if videoInput.isReadyForMoreMediaData {
                videoInput.append(sampleBuffer)
            }
        } else if type == .audio {
            lock.lock(); let go = started; lock.unlock()
            guard go else { return } // drop audio until the session clock exists
            if audioInput.isReadyForMoreMediaData {
                audioInput.append(sampleBuffer)
            }
        }
    }

    func start(window: SCWindow, fps: Int) async {
        let config = SCStreamConfiguration()
        config.width = Int(window.frame.width)
        config.height = Int(window.frame.height)
        config.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(fps))
        config.pixelFormat = kCVPixelFormatType_32BGRA
        config.showsCursor = true
        config.capturesAudio = true
        config.sampleRate = 48_000
        config.channelCount = 2

        let filter = SCContentFilter(desktopIndependentWindow: window)
        let s = SCStream(filter: filter, configuration: config, delegate: self)
        let queue = DispatchQueue(label: "cast-capture.samples")
        do {
            try s.addStreamOutput(self, type: .screen, sampleHandlerQueue: queue)
            try s.addStreamOutput(self, type: .audio, sampleHandlerQueue: queue)
            try await s.startCapture()
        } catch {
            fail("could not start capture: \(error.localizedDescription)")
        }
        stream = s
        warn("cast-capture: streaming \"\(window.title ?? "window")\" at \(config.width)x\(config.height)@\(fps)")
    }
}

func cmdStream(windowID: CGWindowID, fps: Int) async {
    let content = await shareableContent()
    guard let window = content.windows.first(where: { $0.windowID == windowID }) else {
        fail("window \(windowID) not found (it may have closed) — run `cast window` again")
    }
    let streamer = Streamer(width: Int(window.frame.width), height: Int(window.frame.height))
    await streamer.start(window: window, fps: fps)
    // Capture runs on the sample queue; park the main thread until signalled.
    dispatchMain()
}

// MARK: - entry

func arg(_ name: String) -> String? {
    let a = CommandLine.arguments
    guard let i = a.firstIndex(of: name), i + 1 < a.count else { return nil }
    return a[i + 1]
}

let args = CommandLine.arguments
guard args.count >= 2 else {
    fail("usage: cast-capture list | stream --window <id> [--fps 30]")
}

switch args[1] {
case "list":
    Task { await cmdList(); exit(0) }
    dispatchMain()
case "stream":
    guard let idStr = arg("--window"), let id = UInt32(idStr) else {
        fail("stream needs --window <id>")
    }
    let fps = Int(arg("--fps") ?? "30") ?? 30
    Task { await cmdStream(windowID: id, fps: fps) }
    dispatchMain()
default:
    fail("unknown command \"\(args[1])\" — use list or stream")
}
