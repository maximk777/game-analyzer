import Cocoa
import ScreenCaptureKit
import Vision

@main
struct MacSCKRunner {
    static func main() async {
        _ = NSApplication.shared
        setbuf(stdout, nil)
        
        let cwd = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
        let logsDir = cwd.appendingPathComponent("bin/logs")
        try? FileManager.default.createDirectory(at: logsDir, withIntermediateDirectories: true)
        let logFile = logsDir.appendingPathComponent("live_session.jsonl")

        let serverURL = URL(string: "http://127.0.0.1:8080/api/v1/tables/coinpoker-live/events")!
        print("[MAC-VISION] ScreenCaptureKit Vision Agent active.")
        print("[MAC-VISION] Logging live session to: \(logFile.path)")

        for iter in 1...20 {
            do {
                let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
                let coinWins = content.windows.filter {
                    let app = ($0.owningApplication?.applicationName ?? "").lowercased()
                    let isCoinPoker = app.contains("coin")
                    let isTableSize = $0.frame.width > 500 && $0.frame.height > 350
                    let ratio = $0.frame.width / max($0.frame.height, 1)
                    return isCoinPoker && isTableSize && ratio >= 1.15 && ratio <= 1.55
                }

                if let win = coinWins.first {
                    let filter = SCContentFilter(desktopIndependentWindow: win)
                    let config = SCStreamConfiguration()
                    config.width = Int(win.frame.width * 2)
                    config.height = Int(win.frame.height * 2)
                    config.showsCursor = false

                    let img = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)

                    var texts: [String] = []
                    let request = VNRecognizeTextRequest { req, _ in
                        guard let obs = req.results as? [VNRecognizedTextObservation] else { return }
                        for o in obs {
                            if let top = o.topCandidates(1).first?.string {
                                texts.append(top)
                            }
                        }
                    }
                    request.recognitionLevel = .accurate
                    request.usesLanguageCorrection = false

                    let handler = VNImageRequestHandler(cgImage: img, options: [:])
                    try? handler.perform([request])

                    var pot = 0.0
                    var isHeroTurn = false
                    var madeHand = ""

                    for t in texts {
                        let lower = t.lowercased()
                        if lower.contains("pot") {
                            let clean = t.replacingOccurrences(of: "Pot", with: "")
                                         .replacingOccurrences(of: "pot", with: "")
                                         .replacingOccurrences(of: ":", with: "")
                                         .replacingOccurrences(of: ",", with: "")
                                         .replacingOccurrences(of: "$", with: "")
                                         .trimmingCharacters(in: .whitespacesAndNewlines)
                            pot = Double(clean) ?? 0.0
                        }
                        if lower == "check" || lower == "fold" || lower.contains("call") || lower.contains("bet") || lower.contains("raise") {
                            isHeroTurn = true
                        }
                        if lower.contains("pair") || lower.contains("high card") || lower.contains("straight") || lower.contains("flush") || lower.contains("full house") {
                            madeHand = t
                        }
                    }

                    let payload: [String: Any] = [
                        "table_id": "coinpoker-live",
                        "pot": pot,
                        "hero_made_hand": madeHand,
                        "is_hero_turn": isHeroTurn,
                        "street": "preflop"
                    ]

                    if let jsonData = try? JSONSerialization.data(withJSONObject: payload, options: []) {
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
                    }

                    print("[MAC-VISION] [\(iter)/20] Captured Window \(win.windowID): Pot=\(pot), MadeHand='\(madeHand)', OCR Items=\(texts.count)")
                } else {
                    print("[MAC-VISION] [\(iter)/20] CoinPoker table window not found...")
                }
            } catch {
                print("[MAC-VISION] Capture error: \(error)")
            }

            try? await Task.sleep(nanoseconds: 500_000_000) // 500ms
        }
    }
}
