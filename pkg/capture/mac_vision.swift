import Cocoa
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

func findPokerWindowID() -> (id: Int, title: String)? {
    let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    for w in list {
        let id = w[kCGWindowNumber as String] as? Int ?? 0
        let owner = w[kCGWindowOwnerName as String] as? String ?? ""
        let title = w[kCGWindowName as String] as? String ?? ""
        
        if owner.lowercased().contains("coin") || title.lowercased().contains("nlh") || title.lowercased().contains("table") || title.lowercased().contains("poker") {
            if title.lowercased().contains("nlh") || title.lowercased().contains("table") || title.lowercased().contains("1228") {
                return (id, title)
            }
        }
    }
    // Fallback
    for w in list {
        let id = w[kCGWindowNumber as String] as? Int ?? 0
        let owner = w[kCGWindowOwnerName as String] as? String ?? ""
        let title = w[kCGWindowName as String] as? String ?? ""
        if owner.lowercased().contains("coin") || title.lowercased().contains("coin") {
            return (id, title)
        }
    }
    return nil
}

/// Captures one window through the screencapture tool.
///
/// screencapture cannot write to standard output. Its usage line is
/// `screencapture [-options] [files]`, and a trailing "-" is taken as a file
/// named "-" rather than as a stream -- so the previous version wrote a PNG
/// into the working directory under that name and then read an empty pipe. It
/// could not return an image under any circumstances, and it left the stray
/// file behind on every call. The same mistake was in the Go grabber, where it
/// was on a live path.
///
/// Nothing in the Makefile builds this file; the agent that runs captures
/// through ScreenCaptureKit instead. It is corrected rather than left as it
/// was because a wrong pattern sitting in the repository is how the wrong
/// pattern spreads.
func captureWindowImage(windowID: Int) -> CGImage? {
    let path = NSTemporaryDirectory() + "poker-rta-frame-\(UUID().uuidString).png"
    defer { try? FileManager.default.removeItem(atPath: path) }

    let proc = Process()
    proc.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    proc.arguments = ["-l\(windowID)", "-x", "-o", "-tpng", path]
    proc.standardError = FileHandle.nullDevice

    do {
        try proc.run()
        proc.waitUntilExit()
    } catch {
        return nil
    }

    // screencapture exits zero whether or not it captured anything -- a window
    // that has gone, or no screen-recording permission, both leave no file --
    // so the file is the only evidence that it worked.
    guard let data = FileManager.default.contents(atPath: path), !data.isEmpty,
          let nsImg = NSImage(data: data),
          let cgImg = nsImg.cgImage(forProposedRect: nil, context: nil, hints: nil) else {
        return nil
    }
    return cgImg
}

func analyzeTableImage(cgImg: CGImage, title: String) -> ParsedTableState {
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

    var detectedSeats: [ParsedSeat] = []

    for item in texts {
        let t = item.text
        let b = item.box
        let lower = t.lowercased()

        // Pot
        if lower.contains("pot") {
            state.pot = parseAmount(t)
        }

        // Hero Turn
        if lower == "check" || lower.contains("call") || lower.contains("bet") || lower.contains("raise") || lower.contains("fold") {
            if b.origin.y < 0.15 && b.origin.x > 0.50 {
                state.is_hero_turn = true
            }
        }

        // Made Hand
        if lower.contains("pair") || lower.contains("high card") || lower.contains("straight") || lower.contains("flush") || lower.contains("full house") || lower.contains("three of a kind") {
            if b.origin.y < 0.10 && b.origin.x > 0.35 && b.origin.x < 0.65 {
                state.hero_made_hand = t
            }
        }

        // Player Nameplates (names have alphanumeric letters, not pure numbers or labels)
        if !lower.contains("pot") && !lower.contains("check") && !lower.contains("fold") && !lower.contains("call") && !lower.contains("bet") && !lower.contains("empty") && !lower.contains("nlh") {
            let isNumber = Double(t.replacingOccurrences(of: ",", with: "").replacingOccurrences(of: "$", with: "")) != nil
            if !isNumber && t.count >= 3 && (b.origin.y > 0.60 || b.origin.y < 0.30) {
                let seatNum = detectedSeats.count
                detectedSeats.append(ParsedSeat(
                    seat_number: seatNum,
                    player_id: t,
                    player_name: t,
                    stack: 0,
                    current_bet: 0,
                    is_active: true,
                    is_folded: false
                ))
            }
        }
    }

    state.seats = detectedSeats
    return state
}

@main
struct VisionMain {
    static func main() {
        guard let win = findPokerWindowID() else {
            let empty = ["status": "searching", "message": "No CoinPoker table window found"]
            let data = try! JSONSerialization.data(withJSONObject: empty, options: [])
            print(String(data: data, encoding: .utf8)!)
            return
        }

        guard let img = captureWindowImage(windowID: win.id) else {
            print("{\"status\":\"error\",\"message\":\"Failed to capture window \(win.id)\"}")
            return
        }

        let parsed = analyzeTableImage(cgImg: img, title: win.title)
        let encoder = JSONEncoder()
        encoder.outputFormatting = .prettyPrinted
        let jsonData = try! encoder.encode(parsed)
        print(String(data: jsonData, encoding: .utf8)!)
    }
}
