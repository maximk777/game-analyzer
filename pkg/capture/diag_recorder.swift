import Cocoa
import ScreenCaptureKit
import Vision

/// Diagnostic recorder. Watches the live CoinPoker table and builds a corpus
/// that card recognition can be replayed against offline.
///
/// It deliberately does not record everything -- at 2 fps a session is tens of
/// thousands of near-identical frames. It keeps the frames that carry
/// information:
///
///   * FAILURES: a slot that clearly holds a face-up card but whose rank or
///     suit did not resolve. These are the frames worth looking at.
///   * CHANGES: the first frame of each new board, hero hand or pot value, so
///     the corpus covers every street of every hand actually played.
///
/// Each kept frame is written as a PNG next to a JSON sidecar holding the
/// parsed state and the per-slot reading, so a later run can diff against it.
///
///   swiftc -parse-as-library pkg/capture/table_vision.swift \
///          pkg/capture/diag_recorder.swift -o bin/diag_recorder
///   bin/diag_recorder                 # writes to testdata/diag/<timestamp>/
///   bin/diag_recorder --out DIR --fps 2 --max-frames 400

struct SlotReport: Codable {
    var slot: String
    var card: String?
    var rank: String?
    var suit: String?
    var white_ratio: Double
    var suit_distance: Double
    var profile: [Double]
    var debug: String
}

struct FrameReport: Codable {
    var frame: Int
    var timestamp: Double
    var reason: String
    var window_title: String
    var image: String
    var state: ParsedTableState
    var slots: [SlotReport]
}

func findRecorderWindow() async -> SCWindow? {
    guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
        return nil
    }
    return content.windows.first {
        let app = ($0.owningApplication?.applicationName ?? "").lowercased()
        let ratio = $0.frame.width / max($0.frame.height, 1)
        return app.contains("coin") && $0.frame.width > 500 && $0.frame.height > 350
            && ratio >= 1.15 && ratio <= 1.55
    }
}

@main
struct DiagRecorder {
    static func main() async {
        _ = NSApplication.shared
        setbuf(stdout, nil)

        var fps = 2.0
        var maxFrames = 600
        var outDir: URL?

        var i = 1
        let args = CommandLine.arguments
        while i < args.count {
            switch args[i] {
            case "--out" where i + 1 < args.count:
                outDir = URL(fileURLWithPath: args[i + 1]); i += 2
            case "--fps" where i + 1 < args.count:
                fps = Double(args[i + 1]) ?? 2.0; i += 2
            case "--max-frames" where i + 1 < args.count:
                maxFrames = Int(args[i + 1]) ?? 600; i += 2
            default:
                i += 1
            }
        }

        let stamp = ISO8601DateFormatter().string(from: Date())
            .replacingOccurrences(of: ":", with: "-")
        let dir = outDir ?? URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
            .appendingPathComponent("testdata/diag/\(stamp)")
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)

        let reportFile = dir.appendingPathComponent("frames.jsonl")
        FileManager.default.createFile(atPath: reportFile.path, contents: nil)

        _ = CardTemplates.shared.load(assetsDir: defaultTemplateAssetsDir())
        print("[DIAG] Recording to \(dir.path)")
        print("[DIAG] Keeping frames where recognition fails or table state changes.")
        print("[DIAG] Ctrl-C to stop. Max \(maxFrames) kept frames at \(fps) fps.")

        var kept = 0
        var seen = 0
        var lastSignature = ""
        var failuresBySlot: [String: Int] = [:]

        while kept < maxFrames {
            guard let win = await findRecorderWindow() else {
                print("[DIAG] Waiting for CoinPoker table window...")
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                continue
            }

            let filter = SCContentFilter(desktopIndependentWindow: win)
            let config = SCStreamConfiguration()
            config.width = Int(win.frame.width * 2)
            config.height = Int(win.frame.height * 2)
            config.showsCursor = false

            guard let img = try? await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config) else {
                try? await Task.sleep(nanoseconds: UInt64(1_000_000_000 / fps))
                continue
            }

            seen += 1

            var slots: [SlotReport] = []
            var failed: [String] = []

            for (n, slot) in TableGeometry.boardSlots.enumerated() {
                collect(img, slot, "board\(n + 1)", &slots, &failed)
            }
            for (n, slot) in TableGeometry.heroSlots.enumerated() {
                collect(img, slot, "hero\(n + 1)", &slots, &failed)
            }

            let state = analyzeTable(cgImg: img, title: win.title ?? "CoinPoker Table")
            let signature = "\(state.community_cards)|\(state.hero_cards)|\(Int(state.pot))"

            var reason = ""
            if !failed.isEmpty {
                reason = "unresolved:" + failed.joined(separator: ",")
            } else if signature != lastSignature {
                reason = "changed"
            }
            lastSignature = signature

            if !reason.isEmpty {
                kept += 1
                let name = String(format: "frame-%04d.png", kept)
                writePNG(img, to: dir.appendingPathComponent(name))

                for slot in failed { failuresBySlot[slot, default: 0] += 1 }

                let report = FrameReport(
                    frame: kept,
                    timestamp: Date().timeIntervalSince1970,
                    reason: reason,
                    window_title: win.title ?? "",
                    image: name,
                    state: state,
                    slots: slots
                )
                if let data = try? JSONEncoder().encode(report),
                   let handle = try? FileHandle(forWritingTo: reportFile) {
                    handle.seekToEndOfFile()
                    handle.write(data)
                    handle.write("\n".data(using: .utf8)!)
                    try? handle.close()
                }

                print("[DIAG] kept \(kept)/\(maxFrames) (seen \(seen)) \(reason) "
                    + "board=\(state.community_cards) hero=\(state.hero_cards) pot=\(Int(state.pot))")
            }

            try? await Task.sleep(nanoseconds: UInt64(1_000_000_000 / fps))
        }

        print("[DIAG] Done. \(kept) frames in \(dir.path)")
        if failuresBySlot.isEmpty {
            print("[DIAG] No unresolved card slots.")
        } else {
            print("[DIAG] Unresolved slots: "
                + failuresBySlot.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }.joined(separator: " "))
        }
    }

    /// A slot counts as failed only when a card is clearly present -- a high
    /// white ratio -- but rank or suit did not come out. An empty slot is not a
    /// failure, and recording it would bury the real ones.
    static func collect(_ img: CGImage, _ slot: CGRect, _ name: String,
                        _ slots: inout [SlotReport], _ failed: inout [String]) {
        let r = readCardSlot(cgImg: img, slot: slot, label: name)
        slots.append(SlotReport(
            slot: name,
            card: r.card,
            rank: r.rank,
            suit: r.suit,
            white_ratio: r.whiteRatio,
            suit_distance: r.suitDistance.isFinite ? r.suitDistance : -1,
            profile: r.profile,
            debug: r.debug
        ))
        if r.whiteRatio >= 0.45 && r.card == nil {
            failed.append(name)
        }
    }
}
