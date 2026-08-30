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

    let pokerWindows = content.windows.filter {
        let owner = ($0.owningApplication?.applicationName ?? "").lowercased()
        let title = ($0.title ?? "").lowercased()
        let isLarge = $0.frame.width > 500 && $0.frame.height > 350
        return isLarge && (owner.contains("coin") || title.contains("nlh") || title.contains("plo") || title.contains("table") || title.contains("1228"))
    }

    // Prioritize table windows over lobby
    return pokerWindows.first(where: {
        let t = ($0.title ?? "").lowercased()
        return t.contains("nlh") || t.contains("plo") || t.contains("table") || t.contains("1228")
    }) ?? pokerWindows.first
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
        table_id: title.isEmpty ? "coinpoker-live" : title,
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

    for item in texts {
        let t = item.text
        let b = item.box
        let lower = t.lowercased()

        // 1. Pot
        if lower.contains("pot") {
            state.pot = parseAmount(t)
        }

        // 2. Hero Turn Detection
        if lower == "check" || lower == "fold" || lower.contains("call") || lower.contains("bet") || lower.contains("raise") || lower.contains("all-in") {
            if b.origin.y < 0.20 && b.origin.x > 0.45 {
                state.is_hero_turn = true
            }
        }

        // 3. Hero Made Hand
        if lower.contains("pair") || lower.contains("high card") || lower.contains("straight") || lower.contains("flush") || lower.contains("full house") || lower.contains("three of a kind") {
            if b.origin.y < 0.15 && b.origin.x > 0.30 && b.origin.x < 0.70 {
                state.hero_made_hand = t
            }
        }

        // 4. Seats & Players
        let isNumber = Double(t.replacingOccurrences(of: ",", with: "").replacingOccurrences(of: "$", with: "")) != nil
        if !isNumber && t.count >= 4 && !lower.contains("pot") && !lower.contains("fold") && !lower.contains("check") && !lower.contains("call") && !lower.contains("bet") && !lower.contains("raise") && !lower.contains("empty") && !lower.contains("coin") && !lower.contains("nlh") {
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

    state.seats = players
    return state
}

@main
struct MacVisionAgent {
    static func main() async {
        _ = NSApplication.shared
        
        let serverURL = URL(string: "http://127.0.0.1:8080/api/v1/tables/coinpoker-live/events")!
        print("[MAC-VISION] ScreenCaptureKit Vision Agent initialized. Monitoring CoinPoker window...")

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

                    // Post to Go Server
                    var req = URLRequest(url: serverURL)
                    req.httpMethod = "POST"
                    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    req.httpBody = try JSONEncoder().encode(parsed)

                    let (_, resp) = try await URLSession.shared.data(for: req)
                    if let httpResp = resp as? HTTPURLResponse, httpResp.statusCode == 200 {
                        // Success
                    }
                }
            } catch {
                // Sleep briefly on error
            }

            try? await Task.sleep(nanoseconds: 333_000_000) // ~3 FPS
        }
    }
}
