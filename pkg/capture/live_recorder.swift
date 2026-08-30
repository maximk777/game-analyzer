import Cocoa
import ScreenCaptureKit
import Vision

struct LiveLogEntry: Codable {
    var timestamp: Double
    var window_id: Int
    var window_title: String
    var pot: Double
    var community_cards: [String]
    var hero_cards: [String]
    var hero_made_hand: String
    var is_hero_turn: Bool
    var all_ocr_texts: [String]
}

func parseAmount(_ str: String) -> Double {
    let clean = str.replacingOccurrences(of: "Pot", with: "")
                   .replacingOccurrences(of: "pot", with: "")
                   .replacingOccurrences(of: ":", with: "")
                   .replacingOccurrences(of: ",", with: "")
                   .replacingOccurrences(of: "$", with: "")
                   .trimmingCharacters(in: .whitespacesAndNewlines)
    
    var multiplier = 1.0
    var numStr = clean
    if numStr.lowercased().hasSuffix("k") {
        multiplier = 1000.0
        numStr = String(numStr.dropLast())
    } else if numStr.lowercased().hasSuffix("m") {
        multiplier = 1000000.0
        numStr = String(numStr.dropLast())
    }
    return (Double(numStr) ?? 0.0) * multiplier
}

func findTargetWindow() async -> SCWindow? {
    guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
        return nil
    }

    let pokerWindows = content.windows.filter {
        let owner = ($0.owningApplication?.applicationName ?? "").lowercased()
        let title = ($0.title ?? "").lowercased()
        let isLarge = $0.frame.width > 500 && $0.frame.height > 350
        return isLarge && (owner.contains("coin") || title.contains("nlh") || title.contains("plo") || title.contains("table") || title.contains("1228"))
    }

    return pokerWindows.first(where: {
        let t = ($0.title ?? "").lowercased()
        return t.contains("nlh") || t.contains("plo") || t.contains("table") || t.contains("1228")
    }) ?? pokerWindows.first
}

@main
struct LiveRecorder {
    static func main() async {
        _ = NSApplication.shared
        let logsDir = URL(fileURLWithPath: "bin/logs")
        try? FileManager.default.createDirectory(at: logsDir, withIntermediateDirectories: true)
        let logFile = logsDir.appendingPathComponent("live_session.jsonl")

        print("[RECORDER] Starting live CoinPoker background recorder...")
        print("[RECORDER] Saving log to: \(logFile.path)")

        let serverURL = URL(string: "http://127.0.0.1:8080/api/v1/tables/coinpoker-live/events")!

        for i in 1...20 {
            do {
                if let win = await findTargetWindow() {
                    let filter = SCContentFilter(desktopIndependentWindow: win)
                    let config = SCStreamConfiguration()
                    config.width = Int(win.frame.width * 2)
                    config.height = Int(win.frame.height * 2)
                    config.showsCursor = false

                    let img = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)

                    var ocrTexts: [String] = []
                    let request = VNRecognizeTextRequest { req, _ in
                        guard let obs = req.results as? [VNRecognizedTextObservation] else { return }
                        for o in obs {
                            if let top = o.topCandidates(1).first?.string {
                                ocrTexts.append(top)
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

                    for t in ocrTexts {
                        let lower = t.lowercased()
                        if lower.contains("pot") {
                            pot = parseAmount(t)
                        }
                        if lower == "check" || lower == "fold" || lower.contains("call") || lower.contains("bet") || lower.contains("raise") {
                            isHeroTurn = true
                        }
                        if lower.contains("pair") || lower.contains("high card") || lower.contains("straight") || lower.contains("flush") || lower.contains("full house") {
                            madeHand = t
                        }
                    }

                    let entry = LiveLogEntry(
                        timestamp: Date().timeIntervalSince1970,
                        window_id: Int(win.windowID),
                        window_title: win.title ?? "CoinPoker Table",
                        pot: pot,
                        community_cards: [],
                        hero_cards: [],
                        hero_made_hand: madeHand,
                        is_hero_turn: isHeroTurn,
                        all_ocr_texts: ocrTexts
                    )

                    let entryData = try JSONEncoder().encode(entry)
                    if let handle = try? FileHandle(forWritingTo: logFile) {
                        handle.seekToEndOfFile()
                        handle.write(entryData)
                        handle.write("\n".data(using: .utf8)!)
                        try? handle.close()
                    } else {
                        try? entryData.write(to: logFile)
                    }

                    print("[RECORDER] [\(i)/20] Captured Window '\(win.title ?? "")': Pot=\(pot), OCR items=\(ocrTexts.count), HeroTurn=\(isHeroTurn)")

                    // Forward to Go Server
                    var req = URLRequest(url: serverURL)
                    req.httpMethod = "POST"
                    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    let payload: [String: Any] = [
                        "table_id": "coinpoker-live",
                        "pot": pot,
                        "hero_made_hand": madeHand,
                        "is_hero_turn": isHeroTurn,
                    ]
                    req.httpBody = try? JSONSerialization.data(withJSONObject: payload)
                    _ = try? await URLSession.shared.data(for: req)
                } else {
                    print("[RECORDER] [\(i)/20] Waiting for CoinPoker table window...")
                }
            } catch {
                print("[RECORDER] Error: \(error)")
            }

            try? await Task.sleep(nanoseconds: 500_000_000) // 500ms
        }
        print("[RECORDER] Live recording completed. Check bin/logs/live_session.jsonl")
    }
}
