import Cocoa
import ScreenCaptureKit
import Vision

func findTargetWindow() async -> SCWindow? {
    guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
        return nil
    }
    for w in content.windows {
        let app = (w.owningApplication?.applicationName ?? "").lowercased()
        if app.contains("coin") && w.frame.width > 500 {
            let ratio = w.frame.width / max(w.frame.height, 1)
            if ratio >= 1.15 && ratio <= 1.55 {
                return w
            }
        }
    }
    return nil
}

let boardSlotRects: [CGRect] = [
    CGRect(x: 0.235, y: 0.380, width: 0.058, height: 0.125), // Flop 1
    CGRect(x: 0.295, y: 0.380, width: 0.058, height: 0.125), // Flop 2
    CGRect(x: 0.355, y: 0.380, width: 0.058, height: 0.125), // Flop 3
    CGRect(x: 0.415, y: 0.380, width: 0.058, height: 0.125), // Turn
    CGRect(x: 0.475, y: 0.380, width: 0.058, height: 0.125)  // River
]

let heroSlotRects: [CGRect] = [
    CGRect(x: 0.435, y: 0.715, width: 0.058, height: 0.130), // Hero Card 1
    CGRect(x: 0.500, y: 0.715, width: 0.058, height: 0.130)  // Hero Card 2
]

func sampleSuit(cropped: CGImage) -> String {
    let w = cropped.width
    let h = cropped.height
    var rawData = [UInt8](repeating: 0, count: w * h * 4)
    let colorSpace = CGColorSpaceCreateDeviceRGB()
    guard let ctx = CGContext(data: &rawData, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: colorSpace, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
        return "s"
    }
    ctx.draw(cropped, in: CGRect(x: 0, y: 0, width: w, height: h))

    var redPixels = 0
    var bluePixels = 0
    var greenPixels = 0
    var blackPixels = 0

    // Also analyze heart vs diamond geometry in red pixels
    var redYSum = 0, redMinY = h, redMaxY = 0

    for y in 0..<h {
        for x in 0..<w {
            let offset = (y * w + x) * 4
            let r = Double(rawData[offset])
            let g = Double(rawData[offset + 1])
            let b = Double(rawData[offset + 2])

            // Ignore white background and green table felt
            if r > 200 && g > 200 && b > 200 { continue }
            if g > r + 35 && g > b + 35 { continue }

            if r > 150 && r > g + 40 && r > b + 40 {
                redPixels += 1
                redYSum += y
                if y < redMinY { redMinY = y }
                if y > redMaxY { redMaxY = y }
            } else if b > 150 && b > r + 35 && b > g + 35 {
                bluePixels += 1
            } else if g > 130 && g > r + 30 && g > b + 30 {
                greenPixels += 1
            } else if r < 70 && g < 70 && b < 70 {
                blackPixels += 1
            }
        }
    }

    if bluePixels > redPixels && bluePixels > 20 {
        return "d" // 4-color blue diamonds
    }
    if greenPixels > redPixels && greenPixels > 20 {
        return "c" // 4-color green clubs
    }
    if redPixels > blackPixels && redPixels > 20 {
        // Heart vs Diamond
        if redPixels > 30 && redMaxY > redMinY {
            let midY = (redMinY + redMaxY) / 2
            let avgY = Double(redYSum) / Double(redPixels)
            return (avgY < Double(midY)) ? "h" : "d"
        }
        return "h"
    }
    return "s" // Black spades / clubs
}

func extractCardAtSlot(cgImg: CGImage, slot: CGRect) -> String? {
    let imgW = CGFloat(cgImg.width)
    let imgH = CGFloat(cgImg.height)

    let cropX = Int(slot.origin.x * imgW)
    let cropY = Int(slot.origin.y * imgH)
    let cropW = Int(slot.size.width * imgW)
    let cropH = Int(slot.size.height * imgH)

    guard let cropped = cgImg.cropping(to: CGRect(x: cropX, y: cropY, width: cropW, height: cropH)) else {
        return nil
    }

    // 1. Check card presence (draw into buffer and count white pixels)
    let w = cropped.width
    let h = cropped.height
    var rawData = [UInt8](repeating: 0, count: w * h * 4)
    let colorSpace = CGColorSpaceCreateDeviceRGB()
    guard let ctx = CGContext(data: &rawData, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: colorSpace, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
        return nil
    }
    ctx.draw(cropped, in: CGRect(x: 0, y: 0, width: w, height: h))

    var whitePixels = 0
    let total = w * h

    for i in stride(from: 0, to: rawData.count, by: 4) {
        let r = Double(rawData[i])
        let g = Double(rawData[i + 1])
        let b = Double(rawData[i + 2])
        if r > 190 && g > 190 && b > 190 {
            whitePixels += 1
        }
    }

    let whiteRatio = Double(whitePixels) / Double(max(total, 1))
    if whiteRatio < 0.20 {
        return nil // No card
    }

    // 2. Classify Suit
    let suit = sampleSuit(cropped: cropped)

    // 3. Classify Rank using Vision OCR on the entire card
    var detectedRank = "?"
    let req = VNRecognizeTextRequest { request, _ in
        for o in (request.results as? [VNRecognizedTextObservation] ?? []) {
            if let top = o.topCandidates(1).first?.string.trimmingCharacters(in: .whitespacesAndNewlines).uppercased() {
                for r in ["10", "A", "K", "Q", "J", "9", "8", "7", "6", "5", "4", "3", "2"] {
                    if top == r || top.hasPrefix(r) {
                        detectedRank = r
                        return
                    }
                }
            }
        }
    }
    req.recognitionLevel = .accurate
    req.usesLanguageCorrection = false
    let handler = VNImageRequestHandler(cgImage: cropped, options: [:])
    try? handler.perform([req])

    if detectedRank == "?" {
        // Fallback: search substring
        let req2 = VNRecognizeTextRequest { request, _ in
            for o in (request.results as? [VNRecognizedTextObservation] ?? []) {
                if let top = o.topCandidates(1).first?.string.trimmingCharacters(in: .whitespacesAndNewlines).uppercased() {
                    for r in ["10", "A", "K", "Q", "J", "9", "8", "7", "6", "5", "4", "3", "2"] {
                        if top.contains(r) {
                            detectedRank = r
                            return
                        }
                    }
                }
            }
        }
        req2.recognitionLevel = .fast
        let handler2 = VNImageRequestHandler(cgImage: cropped, options: [:])
        try? handler2.perform([req2])
    }

    if detectedRank == "?" {
        return nil
    }

    return detectedRank + suit
}

@main
struct TestCardCropMain {
    static func main() async {
        _ = NSApplication.shared
        guard let win = await findTargetWindow() else {
            print("No CoinPoker table window found")
            return
        }

        let filter = SCContentFilter(desktopIndependentWindow: win)
        let config = SCStreamConfiguration()
        config.width = Int(win.frame.width * 2)
        config.height = Int(win.frame.height * 2)
        config.showsCursor = false

        do {
            let img = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
            print("Captured Table: \(img.width)x\(img.height)")

            var board: [String] = []
            for (i, slot) in boardSlotRects.enumerated() {
                if let card = extractCardAtSlot(cgImg: img, slot: slot) {
                    board.append(card)
                    print("  Board Slot \(i): \(card)")
                } else {
                    print("  Board Slot \(i): [Empty]")
                }
            }

            var hero: [String] = []
            for (i, slot) in heroSlotRects.enumerated() {
                if let card = extractCardAtSlot(cgImg: img, slot: slot) {
                    hero.append(card)
                    print("  Hero Slot \(i): \(card)")
                } else {
                    print("  Hero Slot \(i): [Empty / Spectating]")
                }
            }

            print("FINAL DETECTED BOARD: \(board)")
            print("FINAL DETECTED HERO: \(hero)")
        } catch {
            print("Capture error details: \(error)")
        }
    }
}
