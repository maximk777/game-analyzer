import Cocoa
import ScreenCaptureKit
import Vision

// Table parsing lives in table_vision.swift so the offline harness
// (parse_image_tool.swift) exercises exactly the same code path.
//
//   swiftc -parse-as-library pkg/capture/table_vision.swift \
//          pkg/capture/mac_vision_agent.swift -o bin/mac_vision_agent

func findTargetWindow() async -> SCWindow? {
    guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
        return nil
    }

    let coinWins = content.windows.filter {
        let app = ($0.owningApplication?.applicationName ?? "").lowercased()
        let isCoinPoker = app.contains("coin")
        let isTableSize = $0.frame.width > 500 && $0.frame.height > 350
        let ratio = $0.frame.width / max($0.frame.height, 1)
        return isCoinPoker && isTableSize && ratio >= 1.15 && ratio <= 1.55
    }

    return coinWins.first
}

@main
struct MacVisionAgent {
    static func main() async {
        _ = NSApplication.shared
        setbuf(stdout, nil)

        let cwd = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
        let logsDir = cwd.appendingPathComponent("bin/logs")
        try? FileManager.default.createDirectory(at: logsDir, withIntermediateDirectories: true)
        let logFile = logsDir.appendingPathComponent("live_session.jsonl")

        // The Go agent passes its actual address; the default only matches the
        // default port, so hardcoding it silently broke --port for the whole
        // vision pipeline: events went to a server that was not running.
        let endpoint = ProcessInfo.processInfo.environment["POKER_RTA_ENDPOINT"]
            ?? "http://127.0.0.1:8080/api/v1/tables/coinpoker-live/events"
        guard let serverURL = URL(string: endpoint) else {
            print("[MAC-VISION] Invalid endpoint: \(endpoint)")
            return
        }
        print("[MAC-VISION] Posting table state to \(serverURL.absoluteString)")
        // Card references built from the client's own assets. Absent, reading
        // falls back to text recognition, so this must never be fatal.
        let assetsDir = defaultTemplateAssetsDir()
        if CardTemplates.shared.load(assetsDir: assetsDir) {
            print("[MAC-VISION] Card templates loaded from \(assetsDir)")
        } else {
            print("[MAC-VISION] No card templates at \(assetsDir); falling back to text recognition")
        }

        print("[MAC-VISION] ScreenCaptureKit Vision Agent active. Monitoring CoinPoker table...")
        print("[MAC-VISION] Logging live session to: \(logFile.path)")

        while true {
            do {
                if let win = await findTargetWindow() {
                    let filter = SCContentFilter(desktopIndependentWindow: win)
                    let config = SCStreamConfiguration()
                    config.width = Int(win.frame.width * 2)
                    config.height = Int(win.frame.height * 2)
                    config.showsCursor = false

                    let img = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
                    let parsed = analyzeTable(cgImg: img, title: win.title ?? "CoinPoker Table")

                    if let jsonData = try? JSONEncoder().encode(parsed) {
                        if let handle = try? FileHandle(forWritingTo: logFile) {
                            handle.seekToEndOfFile()
                            handle.write(jsonData)
                            handle.write("\n".data(using: .utf8)!)
                            try? handle.close()
                        } else {
                            try? jsonData.write(to: logFile)
                        }

                        var req = URLRequest(url: serverURL)
                        req.httpMethod = "POST"
                        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                        req.httpBody = jsonData

                        _ = try? await URLSession.shared.data(for: req)
                        let playerSummary = parsed.seats.map { "\($0.player_name) (\(Int($0.stack)))" }.joined(separator: ", ")
                        print("[MAC-VISION] Live Update: Pot=\(Int(parsed.pot)), Street=\(parsed.street), Board=\(parsed.community_cards), Hero=\(parsed.hero_cards), Players=[\(playerSummary)]")
                    }
                } else {
                    print("[MAC-VISION] Waiting for CoinPoker table window...")
                }
            } catch {
                // Ignore transient stream errors
            }

            // The loop is analysis-bound, not sleep-bound: a frame takes about
            // 300ms to read, so a long sleep on top of it only adds lag. Live,
            // analysis took over eight seconds a frame and the HUD showed a
            // table several seconds stale -- decisions were being made on a
            // state that had already moved on.
            try? await Task.sleep(nanoseconds: 80_000_000)
        }
    }
}
