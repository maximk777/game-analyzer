import Cocoa
import ScreenCaptureKit
import Vision

struct ParsedSeat: Codable {
    var seat_number: Int
    var player_id: String
    var player_name: String
    var stack: Double
    var current_bet: Double
    var is_active: Bool
    var is_folded: Bool
}

struct ParsedTableState: Codable {
    var hand_id: String
    var table_id: String
    var street: String
    var pot: Double
    var current_bet: Double
    var min_raise: Double
    var community_cards: [String]
    var hero_id: String
    var hero_cards: [String]
    var seats: [ParsedSeat]
    var hero_made_hand: String
    var is_hero_turn: Bool
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

    let coinWins = content.windows.filter {
        let app = ($0.owningApplication?.applicationName ?? "").lowercased()
        let isCoinPoker = app.contains("coin")
        let isTableSize = $0.frame.width > 500 && $0.frame.height > 350
        let ratio = $0.frame.width / max($0.frame.height, 1)
        return isCoinPoker && isTableSize && ratio >= 1.15 && ratio <= 1.55
    }

    return coinWins.first
}

func analyzeTable(cgImg: CGImage, title: String) -> ParsedTableState {
    var texts: [(text: String, box: CGRect)] = []

    let request = VNRecognizeTextRequest { req, _ in
        guard let obs = req.results as? [VNRecognizedTextObservation] else { return }
        for o in obs {
            if let top = o.topCandidates(1).first?.string {
                texts.append((text: top, box: o.boundingBox))
            }
        }
    }
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = false

    let handler = VNImageRequestHandler(cgImage: cgImg, options: [:])
    try? handler.perform([request])

    var state = ParsedTableState(
        hand_id: "live-hand",
        table_id: "coinpoker-live",
        street: "preflop",
        pot: 0.0,
        current_bet: 0.0,
        min_raise: 0.0,
        community_cards: [],
        hero_id: "Hero",
        hero_cards: [],
        seats: [],
        hero_made_hand: "",
        is_hero_turn: false
    )

    var players: [ParsedSeat] = []
    var detectedBoardCards: [(rank: String, x: CGFloat)] = []
    var detectedHeroCards: [(rank: String, x: CGFloat)] = []

    for item in texts {
        let t = item.text.trimmingCharacters(in: .whitespacesAndNewlines)
        let b = item.box
        let lower = t.lowercased()

        // 1. Table Title
        if lower.contains("nlh") || lower.contains("plo") {
            state.table_id = t
        }

        // 2. Pot
        if lower.contains("pot") {
            state.pot = parseAmount(t)
        }

        // 3. Hero Turn Detection
        if lower == "check" || lower == "fold" || lower.contains("call") || lower.contains("bet") || lower.contains("raise") || lower.contains("all-in") {
            if b.origin.y < 0.20 && b.origin.x > 0.45 {
                state.is_hero_turn = true
            }
        }

        // 4. Hero Made Hand
        if lower.contains("pair") || lower.contains("high card") || lower.contains("straight") || lower.contains("flush") || lower.contains("full house") || lower.contains("three of a kind") {
            if b.origin.y < 0.15 && b.origin.x > 0.30 && b.origin.x < 0.70 {
                state.hero_made_hand = t
            }
        }

        // 5. Board & Hero Cards by rank detection
        let validRanks = ["A", "K", "Q", "J", "10", "9", "8", "7", "6", "5", "4", "3", "2"]
        let upper = t.uppercased()
        if validRanks.contains(upper) && b.size.height < 0.06 {
            // Community Cards band (center Y: 0.45..0.65 in Vision coords)
            if b.origin.y > 0.45 && b.origin.y < 0.65 && b.origin.x > 0.25 && b.origin.x < 0.75 {
                detectedBoardCards.append((rank: upper, x: b.origin.x))
            }
            // Hero Cards band (bottom Y: 0.15..0.35)
            if b.origin.y > 0.15 && b.origin.y < 0.35 && b.origin.x > 0.30 && b.origin.x < 0.60 {
                detectedHeroCards.append((rank: upper, x: b.origin.x))
            }
        }

        // 6. Seats & Players
        let isNumber = Double(t.replacingOccurrences(of: ",", with: "").replacingOccurrences(of: "$", with: "")) != nil
        if !isNumber && t.count >= 4 && !lower.contains("pot") && !lower.contains("fold") && !lower.contains("check") && !lower.contains("call") && !lower.contains("bet") && !lower.contains("raise") && !lower.contains("empty") && !lower.contains("coin") && !lower.contains("nlh") && !lower.contains("plo") && !lower.contains("wait") && !lower.contains("find") {
            let seatIndex = players.count
            players.append(ParsedSeat(
                seat_number: seatIndex,
                player_id: t,
                player_name: t,
                stack: 0,
                current_bet: 0,
                is_active: true,
                is_folded: false
            ))
        }
    }

    // Sort detected board cards left-to-right by X
    detectedBoardCards.sort { $0.x < $1.x }
    var boardCardStrings: [String] = []
    for c in detectedBoardCards {
        boardCardStrings.append("\(c.rank)s")
    }
    state.community_cards = boardCardStrings

    // Sort detected hero cards left-to-right
    detectedHeroCards.sort { $0.x < $1.x }
    var heroCardStrings: [String] = []
    for c in detectedHeroCards {
        heroCardStrings.append("\(c.rank)h")
    }
    state.hero_cards = heroCardStrings

    // Determine street
    switch state.community_cards.count {
    case 0:
        state.street = "preflop"
    case 3:
        state.street = "flop"
    case 4:
        state.street = "turn"
    case 5:
        state.street = "river"
    default:
        state.street = state.community_cards.count > 0 ? "flop" : "preflop"
    }

    state.seats = players
    return state
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

        let serverURL = URL(string: "http://127.0.0.1:8080/api/v1/tables/coinpoker-live/events")!
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
                        // Write to live log file
                        if let handle = try? FileHandle(forWritingTo: logFile) {
                            handle.seekToEndOfFile()
                            handle.write(jsonData)
                            handle.write("\n".data(using: .utf8)!)
                            try? handle.close()
                        } else {
                            try? jsonData.write(to: logFile)
                        }

                        // Forward to Go Server
                        var req = URLRequest(url: serverURL)
                        req.httpMethod = "POST"
                        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                        req.httpBody = jsonData

                        _ = try? await URLSession.shared.data(for: req)
                        print("[MAC-VISION] Live Update: Pot=\(parsed.pot), Street=\(parsed.street), Board=\(parsed.community_cards), Players=\(parsed.seats.map { $0.player_name }.joined(separator: ", "))")
                    }
                } else {
                    print("[MAC-VISION] Waiting for CoinPoker table window...")
                }
            } catch {
                // Ignore brief audio/video stream hiccups
            }

            try? await Task.sleep(nanoseconds: 500_000_000) // 500ms
        }
    }
}
