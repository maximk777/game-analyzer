import Foundation
import CoreGraphics
import ImageIO
import Vision

// MARK: - Wire types

struct ParsedSeat: Codable {
    var seat_number: Int
    var player_id: String
    var player_name: String
    var stack: Double
    var current_bet: Double
    var is_active: Bool
    var is_folded: Bool
    /// The action badge currently printed on this player's nameplate, lowercased
    /// ("fold", "check", "call", "bet", "raise", "all-in"), or empty. This is the
    /// only observable record of what each player just did, so it is both how a
    /// folded player is recognised and the source of the action stream the
    /// opponent profiler needs.
    var last_action: String
    /// Table position ("BTN", "SB", "BB", "UTG", "MP", "CO"), or empty when the
    /// dealer button could not be located. Everything range-based depends on
    /// this: without position there is no equity realisation and no preflop
    /// chart, so a button call reads as a fold.
    var position: String
    /// Cards this player has turned face up, positionally, empty where unread.
    /// Only a showdown reveals them, and they are the only ground truth about
    /// what a player actually held for a given line -- frequencies say how often
    /// someone bets, showdowns say with what.
    var cards: [String]
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
    /// Blinds, read from the table title. They set the scale of everything on
    /// the felt: without them a stray "15" from the action timer is
    /// indistinguishable from a bet.
    var small_blind: Double
    var big_blind: Double
    /// The second board, when the hand was run twice. CoinPoker deals two
    /// boards once everyone is all-in, and which one won decides half the pot,
    /// so a hand history without it is incomplete.
    var second_board: [String]
}

// MARK: - Coordinate conventions
//
// Two different normalized spaces are in play here, and mixing them up is what
// produced the misaligned card crops this file used to have:
//
//   * Vision (VNRecognizedTextObservation.boundingBox): origin BOTTOM-left.
//   * CGImage.cropping(to:) and every rect in TableGeometry: origin TOP-left.
//
// Anything named `slot` or `subRect` below is top-left. Anything named `box`
// coming out of Vision is bottom-left.

enum TableGeometry {
    // Measured against a real CoinPoker 6-max table capture (2326x1758).
    // Board cards are evenly pitched, so they are derived rather than listed,
    // which keeps the 5th card from accumulating rounding error.
    static let boardCardWidth: CGFloat = 0.0665
    static let boardCardHeight: CGFloat = 0.1260
    static let boardCardTop: CGFloat = 0.3990
    static let boardFirstLeft: CGFloat = 0.3150
    static let boardPitch: CGFloat = 0.0760

    static var boardSlots: [CGRect] {
        (0..<5).map { i in
            CGRect(x: boardFirstLeft + CGFloat(i) * boardPitch,
                   y: boardCardTop,
                   width: boardCardWidth,
                   height: boardCardHeight)
        }
    }

    static let heroSlots: [CGRect] = [
        CGRect(x: 0.4360, y: 0.7255, width: 0.0590, height: 0.1340),
        CGRect(x: 0.4975, y: 0.7255, width: 0.0590, height: 0.1340)
    ]

    // The card's left index column, relative to the detected white card FACE.
    // Rank and suit are located *within* this column by finding its rows rather
    // than by more hardcoded rectangles, so board cards and the slightly
    // differently proportioned hero cards both work without separate constants.
    //
    // The index column is used rather than the large centre pip because face
    // cards replace the centre pip with artwork.
    // Sized relative to the card face. The origin is not a fixed inset: the
    // card's dark rounded border must be excluded (un-inset it fuses with the
    // "1" of a ten), but a fixed inset large enough to clear the border on one
    // card clips the glyph on another. The border thickness is measured per
    // card instead -- see readCardSlot.
    static let indexSize = CGSize(width: 0.38, height: 0.60)
}

// MARK: - Bitmap helpers

struct Bitmap {
    let width: Int
    let height: Int
    var pixels: [UInt8] // RGBA, row-major, top-left origin
    /// How much the source was shrunk to make this bitmap. Geometric searches
    /// run on a reduced copy -- finding a card-sized rectangle needs nothing
    /// like full resolution, and the pixel count is what the blob passes cost
    /// -- then report their results back in full-frame coordinates.
    let scale: Double

    init?(cgImage: CGImage, downscale: Int = 1) {
        let w = max(cgImage.width / max(downscale, 1), 1)
        let h = max(cgImage.height / max(downscale, 1), 1)
        guard w > 0, h > 0 else { return nil }

        var buf = [UInt8](repeating: 0, count: w * h * 4)
        let space = CGColorSpaceCreateDeviceRGB()
        let info = CGImageAlphaInfo.premultipliedLast.rawValue
        guard let ctx = CGContext(data: &buf, width: w, height: h,
                                  bitsPerComponent: 8, bytesPerRow: w * 4,
                                  space: space, bitmapInfo: info) else { return nil }
        ctx.draw(cgImage, in: CGRect(x: 0, y: 0, width: w, height: h))

        self.width = w
        self.height = h
        self.pixels = buf
        self.scale = Double(max(downscale, 1))
    }

    @inline(__always)
    func rgb(_ x: Int, _ y: Int) -> (r: Double, g: Double, b: Double) {
        let o = (y * width + x) * 4
        return (Double(pixels[o]), Double(pixels[o + 1]), Double(pixels[o + 2]))
    }
}

/// Absolute pixel rect for a sub-rect expressed relative to a parent rect.
func subPixelRect(of parent: CGRect, _ sub: CGRect, imageWidth: Int, imageHeight: Int) -> CGRect {
    let px = (parent.origin.x + sub.origin.x * parent.size.width) * CGFloat(imageWidth)
    let py = (parent.origin.y + sub.origin.y * parent.size.height) * CGFloat(imageHeight)
    let pw = sub.size.width * parent.size.width * CGFloat(imageWidth)
    let ph = sub.size.height * parent.size.height * CGFloat(imageHeight)
    return CGRect(x: floor(px), y: floor(py), width: ceil(pw), height: ceil(ph))
}

// MARK: - Ink extraction

/// A binary mask of "ink" (any non-background marking) plus its colour.
struct InkMask {
    let width: Int
    let height: Int
    var mask: [Bool]
    let inkCount: Int
    let redCount: Int
    let blackCount: Int

    var isRed: Bool { redCount > blackCount }
}

/// Card faces are white, so anything appreciably darker is ink. Colour is
/// classified per-pixel so a red rank glyph and a red pip both count as red.
func makeInkMask(_ bmp: Bitmap) -> InkMask {
    var mask = [Bool](repeating: false, count: bmp.width * bmp.height)
    var ink = 0, red = 0, black = 0

    for y in 0..<bmp.height {
        for x in 0..<bmp.width {
            let (r, g, b) = bmp.rgb(x, y)
            let lum = 0.299 * r + 0.587 * g + 0.114 * b
            guard lum < 190 else { continue }

            // Green is felt caught along the crop edge, not ink -- with one
            // exception. The client ships two decks, and on the four-colour one
            // a club is printed green: measured from its own sprites, felt is
            // (9,67,50) and a club is (48,135,0). What separates them is blue.
            // Felt carries about three quarters as much blue as green; a club
            // carries none, and only picks any up where it blends into the
            // white card beneath it. So the felt test keeps its shape and gains
            // one clause, which leaves the felt margin comfortable (100 against
            // 67) and costs a club only its outermost blended pixels.
            //
            // Without this clause a green club is deleted outright, and the
            // card comes back with a rank and no suit.
            if g > r + 18 && g > b + 10 && b * 2 > g { continue }

            mask[y * bmp.width + x] = true
            ink += 1
            if r > g + 40 && r > b + 40 {
                red += 1
            } else if lum < 120 {
                black += 1
            }
        }
    }

    return InkMask(width: bmp.width, height: bmp.height, mask: mask,
                   inkCount: ink, redCount: red, blackCount: black)
}

struct Blob {
    var pixels: [Int] = []
    var minX = Int.max, maxX = Int.min, minY = Int.max, maxY = Int.min
    var touchesBorder = false

    var size: Int { pixels.count }
    var width: Int { maxX - minX + 1 }
    var height: Int { maxY - minY + 1 }
}

/// A blob reduced to what the geometric searches actually need.
///
/// findBlobs keeps every pixel of every component, which is fine for the few
/// hundred pixels of a rank glyph and ruinous for a white mask over the whole
/// table: the allocation churn alone accounted for seconds per frame.
struct BlobBox {
    var minX = Int.max, maxX = Int.min, minY = Int.max, maxY = Int.min
    var size = 0

    var width: Int { maxX - minX + 1 }
    var height: Int { maxY - minY + 1 }
    var rect: CGRect { CGRect(x: minX, y: minY, width: width, height: height) }
}

/// 4-connected component labelling that records bounding boxes only.
func findBlobBoxes(mask: [Bool], width: Int, height: Int) -> [BlobBox] {
    var seen = [Bool](repeating: false, count: width * height)
    var boxes: [BlobBox] = []
    var queue = [Int]()

    for start in 0..<seen.count where mask[start] && !seen[start] {
        var box = BlobBox()
        queue.removeAll(keepingCapacity: true)
        queue.append(start)
        seen[start] = true

        var head = 0
        while head < queue.count {
            let idx = queue[head]
            head += 1
            let x = idx % width
            let y = idx / width

            box.size += 1
            if x < box.minX { box.minX = x }
            if x > box.maxX { box.maxX = x }
            if y < box.minY { box.minY = y }
            if y > box.maxY { box.maxY = y }

            for (dx, dy) in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
                let nx = x + dx, ny = y + dy
                guard nx >= 0, nx < width, ny >= 0, ny < height else { continue }
                let nIdx = ny * width + nx
                if mask[nIdx] && !seen[nIdx] {
                    seen[nIdx] = true
                    queue.append(nIdx)
                }
            }
        }

        boxes.append(box)
    }

    return boxes
}

/// Clears a rectangle from a mask. Excluding regions this way costs a pass over
/// the rectangles; testing every pixel against every rectangle inside the mask
/// loop cost twenty million rect containment checks a frame.
func clearRect(_ mask: inout [Bool], width: Int, height: Int, rect: CGRect) {
    let x0 = max(Int(rect.minX), 0)
    let x1 = min(Int(rect.maxX), width - 1)
    let y0 = max(Int(rect.minY), 0)
    let y1 = min(Int(rect.maxY), height - 1)
    guard x0 <= x1, y0 <= y1 else { return }
    for y in y0...y1 {
        let row = y * width
        for x in x0...x1 {
            mask[row + x] = false
        }
    }
}

/// 4-connected component labelling.
func findBlobs(_ m: InkMask) -> [Blob] {
    var seen = [Bool](repeating: false, count: m.width * m.height)
    var blobs: [Blob] = []
    var queue = [Int]()

    for start in 0..<seen.count where m.mask[start] && !seen[start] {
        var blob = Blob()
        queue.removeAll(keepingCapacity: true)
        queue.append(start)
        seen[start] = true

        var head = 0
        while head < queue.count {
            let idx = queue[head]
            head += 1
            let x = idx % m.width
            let y = idx / m.width

            blob.pixels.append(idx)
            if x < blob.minX { blob.minX = x }
            if x > blob.maxX { blob.maxX = x }
            if y < blob.minY { blob.minY = y }
            if y > blob.maxY { blob.maxY = y }
            if x == 0 || y == 0 || x == m.width - 1 || y == m.height - 1 {
                blob.touchesBorder = true
            }

            for (dx, dy) in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
                let nx = x + dx, ny = y + dy
                guard nx >= 0, nx < m.width, ny >= 0, ny < m.height else { continue }
                let nIdx = ny * m.width + nx
                if m.mask[nIdx] && !seen[nIdx] {
                    seen[nIdx] = true
                    queue.append(nIdx)
                }
            }
        }

        blobs.append(blob)
    }

    return blobs
}

func maskFromBlobs(_ m: InkMask, _ blobs: [Blob]) -> InkMask {
    var out = [Bool](repeating: false, count: m.mask.count)
    var count = 0
    for blob in blobs {
        for idx in blob.pixels {
            out[idx] = true
            count += 1
        }
    }
    return InkMask(width: m.width, height: m.height, mask: out,
                   inkCount: count, redCount: m.redCount, blackCount: m.blackCount)
}

/// Keeps only the largest blob. The corner pip is the dominant shape in its
/// sub-rect; this drops anti-aliasing specks and any stray sliver of the rank
/// glyph or card border that crept into the crop.
func largestBlob(_ m: InkMask) -> InkMask {
    let blobs = findBlobs(m)
    guard let best = blobs.max(by: { $0.size < $1.size }) else { return m }
    return maskFromBlobs(m, [best])
}

/// Bounding box of the card's white face within a slot crop, in slot-crop
/// pixels. Returns nil when the slot holds no face-up card.
func findCardFace(_ bmp: Bitmap) -> (rect: CGRect, whiteRatio: Double, mask: [Bool])? {
    var mask = [Bool](repeating: false, count: bmp.width * bmp.height)
    var white = 0
    for y in 0..<bmp.height {
        for x in 0..<bmp.width {
            let (r, g, b) = bmp.rgb(x, y)
            if r > 190 && g > 190 && b > 190 {
                mask[y * bmp.width + x] = true
                white += 1
            }
        }
    }

    let ratio = Double(white) / Double(max(bmp.width * bmp.height, 1))
    let faceMask = InkMask(width: bmp.width, height: bmp.height, mask: mask,
                           inkCount: white, redCount: 0, blackCount: 0)
    guard let face = findBlobs(faceMask).max(by: { $0.size < $1.size }) else { return nil }

    var faceOnly = [Bool](repeating: false, count: mask.count)
    for idx in face.pixels { faceOnly[idx] = true }

    let rect = CGRect(x: face.minX, y: face.minY, width: face.width, height: face.height)
    return (rect, ratio, faceOnly)
}

/// Thickness of the card's dark border along the left and top edges of the
/// face bounding box, in pixels. Measured per card rather than assumed, so the
/// index crop clears the border without ever clipping the glyph behind it.
func measureBorderInset(faceMask: [Bool], bmpWidth: Int, rect: CGRect) -> (left: Int, top: Int) {
    let x0 = Int(rect.minX), y0 = Int(rect.minY)
    let w = Int(rect.width), h = Int(rect.height)
    guard w > 4, h > 4 else { return (1, 1) }

    // Measured across the top tenth of the card, above both the rank glyph and
    // the large centre pip, and the smallest reading wins.
    //
    // A single scan line through the middle of the card runs straight into the
    // centre pip and counts its ink as border: measured at 38 pixels on a card
    // whose border is 7, which pushed the index column clear past the rank and
    // left the card unread. Sampling the whole height instead was worse still
    // -- a row crossing no ink at all can sit inside the rounded corner, giving
    // a border thinner than it is, and the felt then leaked into the crop.
    var left = w / 4
    for i in 1...4 {
        let y = y0 + (h * i) / 40
        guard y >= 0, y < y0 + h else { continue }
        var run = 0
        while run < w / 4 && !faceMask[y * bmpWidth + (x0 + run)] { run += 1 }
        if run < left { left = run }
    }

    var top = h / 4
    for i in 3...6 {
        let x = x0 + (w * i) / 10
        var run = 0
        while run < h / 4 && !faceMask[(y0 + run) * bmpWidth + x] { run += 1 }
        if run < top { top = run }
    }

    return (left + 1, top + 1)
}

// MARK: - Suit classification

/// Width of the mask sampled at `bands` evenly spaced heights over its bounding
/// box, normalized so the widest band is 1.0. Scale-free, so it works at any
/// window size, and it separates the four pips by shape rather than colour --
/// which matters because CoinPoker ships a two-colour deck where hearts and
/// diamonds are the same red.
func widthProfile(_ m: InkMask, bands: Int = 12) -> [Double] {
    var minX = m.width, maxX = -1, minY = m.height, maxY = -1
    for y in 0..<m.height {
        for x in 0..<m.width where m.mask[y * m.width + x] {
            if x < minX { minX = x }
            if x > maxX { maxX = x }
            if y < minY { minY = y }
            if y > maxY { maxY = y }
        }
    }
    guard maxX >= minX, maxY >= minY else { return [] }

    let bw = maxX - minX + 1
    let bh = maxY - minY + 1
    var profile = [Double](repeating: 0, count: bands)

    for band in 0..<bands {
        let y0 = minY + (bh * band) / bands
        let y1 = max(y0 + 1, minY + (bh * (band + 1)) / bands)
        var lo = m.width, hi = -1
        for y in y0..<min(y1, m.height) {
            for x in minX...maxX where m.mask[y * m.width + x] {
                if x < lo { lo = x }
                if x > hi { hi = x }
            }
        }
        profile[band] = hi >= lo ? Double(hi - lo + 1) / Double(bw) : 0
    }

    return profile
}

/// Reference width profiles for the four pips. Matching is always restricted to
/// the two suits of the detected colour, so this only ever decides heart vs
/// diamond or spade vs club.
///
/// Spade, heart and diamond are averaged from real CoinPoker card crops. Club
/// is derived from a font-rendered glyph with this client's chunkier stem
/// applied, because no CoinPoker club has been captured yet -- re-measure it
/// from a real frame when one is available.
enum SuitProfiles {
    // Heart: two lobes make it near-full width at the very top, then it tapers
    // to a point at the bottom.
    static let heart: [Double] = [0.71, 0.92, 0.98, 1.00, 1.00, 0.96, 0.92, 0.80, 0.67, 0.51, 0.37, 0.16]
    // Diamond: a single point at the top, widest dead centre, point at bottom.
    static let diamond: [Double] = [0.13, 0.23, 0.36, 0.55, 0.73, 0.94, 0.98, 0.76, 0.56, 0.40, 0.27, 0.13]
    // Spade: point at the top, widening monotonically to the shoulders.
    static let spade: [Double] = [0.19, 0.37, 0.55, 0.69, 0.82, 0.93, 0.99, 1.00, 1.00, 0.97, 0.88, 0.63]
    // Club: the top circle bulges early and then holds flat where the circles
    // meet, before the side lobes take it to full width. That flat shoulder,
    // against the spade's steady climb, is the whole of the distinction.
    static let club: [Double] = [0.35, 0.44, 0.46, 0.45, 0.82, 0.96, 1.00, 1.00, 0.98, 0.96, 0.86, 0.62]

    /// Spade and club only diverge over the upper third; the lower bands are
    /// nearly identical and would otherwise dilute the decision.
    static let bandWeights: [Double] = [3, 3, 3, 3, 2, 1, 1, 1, 1, 1, 1, 1]
}

func profileDistance(_ a: [Double], _ b: [Double]) -> Double {
    guard a.count == b.count, !a.isEmpty else { return .infinity }
    let w = SuitProfiles.bandWeights
    guard w.count == a.count else {
        var sum = 0.0
        for i in 0..<a.count {
            let d = a[i] - b[i]
            sum += d * d
        }
        return (sum / Double(a.count)).squareRoot()
    }

    var sum = 0.0
    var total = 0.0
    for i in 0..<a.count {
        let d = a[i] - b[i]
        sum += w[i] * d * d
        total += w[i]
    }
    return (sum / total).squareRoot()
}

func unweightedProfileDistance(_ a: [Double], _ b: [Double]) -> Double {
    guard a.count == b.count, !a.isEmpty else { return .infinity }
    var sum = 0.0
    for i in 0..<a.count {
        let d = a[i] - b[i]
        sum += d * d
    }
    return (sum / Double(a.count)).squareRoot()
}

struct SuitResult {
    let suit: String
    let isRed: Bool
    let profile: [Double]
    let distance: Double
    /// Distance to the other suit of the same colour. The pip is only ever
    /// decided between two candidates, so this is the whole of the evidence
    /// that the winner won: a pip that sits between them is not a reading.
    let runnerUp: Double
    /// True when the pip's colour named the suit on its own, which happens only
    /// on the four-colour deck. Shape thresholds do not apply to those.
    var decidedByColour: Bool = false
}

/// Mean colour of a mask's ink, taken from the bitmap it was built over.
func meanInkColour(_ m: InkMask, in bmp: Bitmap) -> (r: Double, g: Double, b: Double)? {
    guard m.inkCount > 0, m.width == bmp.width, m.height == bmp.height else { return nil }
    var sr = 0.0, sg = 0.0, sb = 0.0
    for y in 0..<m.height {
        for x in 0..<m.width where m.mask[y * m.width + x] {
            let (r, g, b) = bmp.rgb(x, y)
            sr += r; sg += g; sb += b
        }
    }
    let n = Double(m.inkCount)
    return (sr / n, sg / n, sb / n)
}

/// The suit a pip's colour names outright, if any.
///
/// The client ships two decks, and this is the whole difference between them.
/// On the two-colour deck both black suits are (1,1,1) and both red ones
/// (254,1,0), so colour narrows the field to a pair and shape has to finish the
/// job -- and the black pair is the hard one, a spade and a club sitting only
/// 0.11 apart where hearts and diamonds sit 0.44 apart. On the four-colour deck
/// a club is green (48,135,0) and a diamond blue (0,144,234), which are unique,
/// and no shape comparison can improve on an answer the colour already gives.
///
/// So the two suits that shape reads worst are exactly the two the four-colour
/// deck makes trivial. Every number above is measured from the sprites the
/// client itself draws with, in bin/assets/coinpoker.
func suitFromColour(_ c: (r: Double, g: Double, b: Double)) -> String? {
    if c.g > c.r + 40 && c.g > c.b + 40 { return "c" }
    if c.b > c.r + 60 && c.b > c.g + 40 { return "d" }
    return nil
}

func classifySuit(pip: InkMask, isRed: Bool, colour: (r: Double, g: Double, b: Double)? = nil) -> SuitResult? {
    guard pip.inkCount >= 24 else { return nil }

    let profile = widthProfile(pip)
    guard !profile.isEmpty else { return nil }

    // A colour that belongs to one suit only settles it. The profile is still
    // measured, so a disagreement between the two shows up in the diagnostics
    // rather than being silently discarded.
    if let colour, let named = suitFromColour(colour) {
        let ref = named == "c" ? SuitProfiles.club : SuitProfiles.diamond
        return SuitResult(suit: named, isRed: named == "d", profile: profile,
                          distance: profileDistance(profile, ref), runnerUp: .infinity,
                          decidedByColour: true)
    }

    let candidates: [(String, [Double])] = isRed
        ? [("h", SuitProfiles.heart), ("d", SuitProfiles.diamond)]
        : [("s", SuitProfiles.spade), ("c", SuitProfiles.club)]

    var bestSuit = candidates[0].0
    var bestDist = Double.infinity
    var runnerUp = Double.infinity
    for (name, ref) in candidates {
        let d = profileDistance(profile, ref)
        if d < bestDist {
            runnerUp = bestDist
            bestDist = d
            bestSuit = name
        } else if d < runnerUp {
            runnerUp = d
        }
    }

    // A pip that matches nothing is not a suit. Measured across the reference
    // frames, a clean pip sits 0.008-0.027 from its own reference; a crop that
    // straddled two overlapping cards measured 0.16-0.17. Until this gate
    // existed the nearest of the two same-colour candidates was returned no
    // matter how far away it was, so a garbage crop produced a confident suit
    // -- 10c 3s came back as 10c Kc, wrong in both halves and indistinguishable
    // downstream from a good reading.
    //
    // The margin is deliberately loose. Spade against club is the tight pair:
    // on a real board card they are only 0.11 apart, against 0.44 for hearts
    // against diamonds. A margin tuned to the red pair would reject every black
    // card on the table.
    guard bestDist <= 0.09 else { return nil }
    guard !runnerUp.isFinite || runnerUp - bestDist >= 0.04 else { return nil }


    return SuitResult(suit: bestSuit, isRed: isRed, profile: profile,
                      distance: bestDist, runnerUp: runnerUp)
}

// MARK: - Rank recognition

private let rankCandidates = ["10", "A", "K", "Q", "J", "9", "8", "7", "6", "5", "4", "3", "2"]

/// Vision routinely mangles isolated glyphs, so known confusions are folded
/// back rather than thrown away. "0"/"O" maps to Q because a lone zero is never
/// a rank -- a ten is two separate glyphs and is resolved structurally before
/// OCR is ever reached, so anything ring-shaped arriving here is a queen.
private let rankAliases: [String: String] = [
    "1O": "10", "IO": "10", "I0": "10", "1D": "10", "TO": "10", "T": "10",
    "O": "Q", "0": "Q", "D": "Q",
    "]": "J", "|": "J", ")": "J", "I": "J", "L": "J",
    "R": "K", "H": "K",
    "S": "5", "G": "6", "B": "8", "Z": "2", "?": "7"
]

func normalizeRank(_ raw: String) -> String? {
    let s = raw.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
    guard !s.isEmpty else { return nil }
    if rankCandidates.contains(s) { return s }
    if let mapped = rankAliases[s] { return mapped }
    if s.hasPrefix("10") { return "10" }
    for r in rankCandidates where s.hasPrefix(r) { return r }
    for r in rankCandidates where s.contains(r) { return r }
    return nil
}

/// Number of distinct vertical rows a blob set falls into.
func rowCount(_ blobs: [Blob]) -> Int {
    guard !blobs.isEmpty else { return 0 }
    let sorted = blobs.sorted { $0.minY < $1.minY }
    var rows = 1
    var maxY = sorted[0].maxY
    var minY = sorted[0].minY
    for b in sorted.dropFirst() {
        let overlap = min(maxY, b.maxY) - max(minY, b.minY)
        let smaller = min(maxY - minY, b.maxY - b.minY) + 1
        if overlap > 0 && Double(overlap) / Double(smaller) > 0.4 {
            maxY = max(maxY, b.maxY)
            minY = min(minY, b.minY)
        } else {
            rows += 1
            minY = b.minY
            maxY = b.maxY
        }
    }
    return rows
}

/// Splits the card's index column into its rows. The topmost row is the rank
/// glyph (two blobs for a ten, one otherwise); the row below it is the suit
/// pip. Locating them by structure rather than by fixed rectangles is what
/// makes this survive the card sitting a few pixels off where it was measured.
func splitIndexRows(_ m: InkMask) -> [[Blob]] {
    let area = m.width * m.height
    let all = findBlobs(m)
    let largest = all.map(\.size).max() ?? 0

    // The threshold is relative to the biggest blob in the column, not just an
    // absolute floor: what survives the border inset is a speck of the card's
    // rounded corner, and an absolute floor low enough to keep a thin "1" also
    // keeps that speck -- which then reads as the first row and displaces the
    // rank entirely.
    let minSize = max(8, max(area / 300, largest / 8))

    // Reject by shape, not by merely touching an edge: a rank glyph can sit
    // flush against the crop edge, but neither a glyph nor a pip spans the
    // whole crop. What does is card border (a full-height strip or a full-width
    // arc) and the large centre pip running in from the bottom.
    let keep = { (blob: Blob, allowBottom: Bool) -> Bool in
        if blob.size < minSize { return false }
        if blob.height >= (m.height * 3) / 4 { return false }
        if blob.width >= (m.width * 3) / 4 && blob.height <= m.height / 5 { return false }
        if !allowBottom && blob.maxY >= m.height - 1 { return false }
        return true
    }

    // Blobs running off the bottom are normally the large centre pip reaching
    // up into the crop. But the corner pip itself can touch that edge when the
    // measured border inset lands a pixel or two high, and dropping it left one
    // row where two were needed -- the card then went unread with the rank and
    // pip both plainly in the crop. So the rule is relaxed when enforcing it
    // would leave no index to read.
    var blobs = all.filter { keep($0, false) }
    if rowCount(blobs) < 2 {
        let relaxed = all.filter { keep($0, true) }
        if rowCount(relaxed) >= 2 { blobs = relaxed }
    }
    blobs.sort { $0.minY < $1.minY }
    guard !blobs.isEmpty else { return [] }

    var rows: [[Blob]] = []
    var current: [Blob] = [blobs[0]]
    var rowMinY = blobs[0].minY
    var rowMaxY = blobs[0].maxY

    for blob in blobs.dropFirst() {
        let overlap = min(rowMaxY, blob.maxY) - max(rowMinY, blob.minY)
        let smaller = min(rowMaxY - rowMinY, blob.maxY - blob.minY) + 1
        if overlap > 0 && Double(overlap) / Double(smaller) > 0.4 {
            current.append(blob)
            rowMinY = min(rowMinY, blob.minY)
            rowMaxY = max(rowMaxY, blob.maxY)
        } else {
            rows.append(current.sorted { $0.minX < $1.minX })
            current = [blob]
            rowMinY = blob.minY
            rowMaxY = blob.maxY
        }
    }
    rows.append(current.sorted { $0.minX < $1.minX })

    return rows
}

/// Renders an ink mask as clean black-on-white, cropped to the glyph and padded.
/// A tiny glyph lifted straight out of a screenshot is near the limit of what
/// Vision will read; upscaled, binarized and given a quiet margin it is close
/// to a printed character.
func renderMaskForOCR(_ m: InkMask, scale: Int = 6, padding: Int = 12, repeats: Int = 1) -> CGImage? {
    var minX = m.width, maxX = -1, minY = m.height, maxY = -1
    for y in 0..<m.height {
        for x in 0..<m.width where m.mask[y * m.width + x] {
            if x < minX { minX = x }
            if x > maxX { maxX = x }
            if y < minY { minY = y }
            if y > maxY { maxY = y }
        }
    }
    guard maxX >= minX, maxY >= minY else { return nil }

    let gw = maxX - minX + 1
    let gh = maxY - minY + 1
    let n = max(repeats, 1)
    let gap = n > 1 ? max(gw * scale / 3, scale) : 0
    let w = (gw * scale) * n + gap * (n - 1) + padding * 2
    let h = gh * scale + padding * 2
    var buf = [UInt8](repeating: 255, count: w * h * 4)

    for copy in 0..<n {
        let xOffset = padding + copy * (gw * scale + gap)
        for y in minY...maxY {
            for x in minX...maxX where m.mask[y * m.width + x] {
                for dy in 0..<scale {
                    for dx in 0..<scale {
                        let px = (x - minX) * scale + dx + xOffset
                        let py = (y - minY) * scale + dy + padding
                        let o = (py * w + px) * 4
                        buf[o] = 0; buf[o + 1] = 0; buf[o + 2] = 0; buf[o + 3] = 255
                    }
                }
            }
        }
    }

    let space = CGColorSpaceCreateDeviceRGB()
    let info = CGImageAlphaInfo.premultipliedLast.rawValue
    guard let ctx = CGContext(data: &buf, width: w, height: h, bitsPerComponent: 8,
                              bytesPerRow: w * 4, space: space, bitmapInfo: info) else { return nil }
    return ctx.makeImage()
}

/// Where a glyph's enclosed loop sits vertically, as a fraction of its height,
/// or -1 when there is no single loop.
///
/// Loop count separates an eight from a six, but not a six from a queen or a
/// nine -- all three have exactly one. Their loops sit in different places: low
/// in a six, high in a nine, centred in a queen. At reduced capture sizes the
/// shapes blur together and this is what still tells them apart.
func holeCentreY(_ m: InkMask) -> Double {
    return holeMetrics(m).centreY
}

/// Loop count and loop position from a single pass. Counting them separately
/// meant flooding the glyph's background twice for every card on the table.
func holeMetrics(_ m: InkMask) -> (count: Int, centreY: Double) {
    let w = m.width
    let h = m.height
    guard w > 2, h > 2 else { return (0, -1) }

    var reached = [Bool](repeating: false, count: w * h)
    var queue = [Int]()
    for x in 0..<w {
        for y in [0, h - 1] {
            let i = y * w + x
            if !m.mask[i] && !reached[i] { reached[i] = true; queue.append(i) }
        }
    }
    for y in 0..<h {
        for x in [0, w - 1] {
            let i = y * w + x
            if !m.mask[i] && !reached[i] { reached[i] = true; queue.append(i) }
        }
    }
    var head = 0
    while head < queue.count {
        let idx = queue[head]; head += 1
        let x = idx % w, y = idx / w
        for (dx, dy) in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
            let nx = x + dx, ny = y + dy
            guard nx >= 0, nx < w, ny >= 0, ny < h else { continue }
            let n = ny * w + nx
            if !m.mask[n] && !reached[n] { reached[n] = true; queue.append(n) }
        }
    }

    var minY = h, maxY = -1
    for y in 0..<h {
        for x in 0..<w where m.mask[y * w + x] {
            if y < minY { minY = y }
            if y > maxY { maxY = y }
        }
    }

    // Enclosed background components; anti-aliasing leaves single-pixel gaps
    // that are not loops.
    let minSize = max(4, (w * h) / 400)
    var seen = [Bool](repeating: false, count: w * h)
    var holes = 0
    var sumY = 0, pixels = 0
    for start in 0..<seen.count where !m.mask[start] && !reached[start] && !seen[start] {
        var size = 0
        var localSum = 0
        queue.removeAll(keepingCapacity: true)
        queue.append(start)
        seen[start] = true
        var head = 0
        while head < queue.count {
            let idx = queue[head]; head += 1
            size += 1
            let x = idx % w, y = idx / w
            localSum += y
            for (dx, dy) in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
                let nx = x + dx, ny = y + dy
                guard nx >= 0, nx < w, ny >= 0, ny < h else { continue }
                let n = ny * w + nx
                if !m.mask[n] && !seen[n] { seen[n] = true; queue.append(n) }
            }
        }
        if size >= minSize {
            holes += 1
            sumY += localSum
            pixels += size
        }
    }

    var centre = -1.0
    if holes == 1, pixels > 0, maxY > minY {
        centre = (Double(sumY) / Double(pixels) - Double(minY)) / Double(maxY - minY)
    }
    return (holes, centre)
}

/// Number of enclosed background regions in a glyph.
func countHoles(_ m: InkMask) -> Int {
    return holeMetrics(m).count
}

/// Enclosed loops each rank's glyph has. Ranks absent from the map are not
/// constrained -- a four is closed in some faces and open in others, so it is
/// deliberately left out rather than guessed at.
let rankHoleCount: [String: Int] = [
    "8": 2,
    "6": 1, "9": 1, "Q": 1, "A": 1, "10": 1,
    "7": 0, "5": 0, "3": 0, "2": 0, "K": 0, "J": 0,
]

func recognizeRankText(_ img: CGImage, holes: Int = -1) -> String? {
    var votes: [String: Int] = [:]

    // Fast first. The glyph handed over here is a clean binarized render, not a
    // photograph, so the fast recogniser reads it as well as the accurate one
    // at a fraction of the cost -- and card reading was the largest remaining
    // slice of the frame budget.
    for level in [VNRequestTextRecognitionLevel.fast, .accurate] {
        let req = VNRecognizeTextRequest()
        req.recognitionLevel = level
        req.usesLanguageCorrection = false
        req.minimumTextHeight = 0.05
        req.customWords = rankCandidates
        req.recognitionLanguages = ["en-US"]

        let handler = VNImageRequestHandler(cgImage: img, options: [:])
        try? handler.perform([req])

        for obs in (req.results ?? []) {
            for cand in obs.topCandidates(4) {
                // The glyph is tiled, so the reading is a run of the same
                // character. Each character votes; the winner survives one or
                // two of the copies being misread.
                for ch in cand.string where !ch.isWhitespace {
                    guard let r = normalizeRank(String(ch)) else { continue }
                    // A reading whose glyph topology disagrees is simply wrong.
                    if holes >= 0, let want = rankHoleCount[r], want != holes {
                        continue
                    }
                    votes[r, default: 0] += 1
                }
            }
        }

        if let best = votes.max(by: { $0.value < $1.value }) { return best.key }
    }

    return nil
}

// MARK: - Card extraction

struct CardReading {
    let card: String?
    let rank: String?
    let suit: String?
    let whiteRatio: Double
    let profile: [Double]
    let suitDistance: Double
    var debug: String = ""
}

/// Cache of card readings, keyed on the pixels of the card itself.
///
/// The capture loop runs several times a second and the board does not change
/// within a street, so the same card was being cropped, masked and passed
/// through text recognition dozens of times for the same answer. Keying on a
/// sample of the crop means a card is read once and then recognised for free
/// until it actually changes.
private var cardReadingCache: [UInt64: CardReading] = [:]

/// A cheap content hash of a crop: every eighth pixel is enough to tell one
/// card from another, and from the felt.
private func cropSignature(_ bmp: Bitmap) -> UInt64 {
    var hash: UInt64 = 1469598103934665603
    var i = 0
    while i < bmp.pixels.count {
        hash = (hash ^ UInt64(bmp.pixels[i])) &* 1099511628211
        i += 32
    }
    return hash ^ (UInt64(bmp.width) &* 31) ^ UInt64(bmp.height)
}

/// Reads one card slot. Returns a reading even when recognition fails, so the
/// offline harness can show *why* a slot came back empty.
func readCardSlot(cgImg: CGImage, slot: CGRect, debugDir: URL? = nil, label: String = "") -> CardReading {
    let slotRect = CGRect(x: floor(slot.origin.x * CGFloat(cgImg.width)),
                          y: floor(slot.origin.y * CGFloat(cgImg.height)),
                          width: ceil(slot.size.width * CGFloat(cgImg.width)),
                          height: ceil(slot.size.height * CGFloat(cgImg.height)))
    return readCardRect(cgImg: cgImg, slotRect: slotRect, debugDir: debugDir, label: label)
}

func readCardRect(cgImg: CGImage, slotRect: CGRect, debugDir: URL? = nil, label: String = "") -> CardReading {

    let empty = CardReading(card: nil, rank: nil, suit: nil, whiteRatio: 0, profile: [], suitDistance: .infinity)
    guard let slotImg = cgImg.cropping(to: slotRect), let slotBmp = Bitmap(cgImage: slotImg) else {
        return empty
    }

    let signature = cropSignature(slotBmp)
    if debugDir == nil, let cached = cardReadingCache[signature] {
        return cached
    }

    if let dir = debugDir {
        writePNG(slotImg, to: dir.appendingPathComponent("\(label)-card.png"))
    }

    // Is there a face-up card here at all? A card face is overwhelmingly white;
    // green felt or a face-down back is not.
    guard let face = findCardFace(slotBmp), face.whiteRatio >= 0.25 else {
        cardReadingCache[signature] = empty
        return empty
    }
    let faceRect = face.rect
    let whiteRatio = face.whiteRatio
    let faceMask = face.mask

    let inset = measureBorderInset(faceMask: faceMask, bmpWidth: slotBmp.width, rect: faceRect)
    let indexRect = CGRect(
        x: slotRect.origin.x + faceRect.origin.x + CGFloat(inset.left),
        y: slotRect.origin.y + faceRect.origin.y + CGFloat(inset.top),
        width: ceil(TableGeometry.indexSize.width * faceRect.size.width),
        height: ceil(TableGeometry.indexSize.height * faceRect.size.height))

    let reading = readIndexRect(cgImg: cgImg, indexRect: indexRect, whiteRatio: whiteRatio,
                                note: "inset=(\(inset.left),\(inset.top))",
                                debugDir: debugDir, label: label)

    // Bounded so a long session cannot grow it without limit; a table only ever
    // shows a handful of distinct cards at a time.
    if cardReadingCache.count > 512 {
        cardReadingCache.removeAll(keepingCapacity: true)
    }
    cardReadingCache[signature] = reading
    return reading
}

/// Reads rank and suit out of an already-located index corner.
///
/// Split out of readCardRect so the corner can be located two ways: derived
/// from a card rectangle (readCardRect, used for the board, whose cards never
/// touch) or found directly as ink (readCardsInRegion, used wherever cards
/// overlap). Everything below the corner -- the rank templates, the loop
/// topology, the suit width profile -- is shared by both.
func readIndexRect(cgImg: CGImage, indexRect: CGRect, whiteRatio: Double,
                   note: String = "", debugDir: URL? = nil, label: String = "") -> CardReading {
    guard let indexImg = cgImg.cropping(to: indexRect), let indexBmp = Bitmap(cgImage: indexImg) else {
        return CardReading(card: nil, rank: nil, suit: nil, whiteRatio: whiteRatio, profile: [], suitDistance: .infinity)
    }
    if let dir = debugDir {
        writePNG(indexImg, to: dir.appendingPathComponent("\(label)-index.png"))
    }

    let indexMask = makeInkMask(indexBmp)
    let rows = splitIndexRows(indexMask)
    var dbg = "index=\(indexBmp.width)x\(indexBmp.height) \(note) "
        + "allBlobs=" + findBlobs(indexMask).map { "\($0.width)x\($0.height)@\($0.minX),\($0.minY)#\($0.size)" }.joined(separator: ",")
        + " rows=" + rows.map { r in "[" + r.map { "\($0.width)x\($0.height)" }.joined(separator: "+") + "]" }.joined(separator: " ")
    guard rows.count >= 2 else {
        let all = findBlobs(indexMask).map {
            "\($0.width)x\($0.height)@\($0.minX),\($0.minY)#\($0.size)\($0.touchesBorder ? "B" : "")"
        }.joined(separator: " ")
        return CardReading(card: nil, rank: nil, suit: nil, whiteRatio: whiteRatio,
                           profile: [], suitDistance: .infinity,
                           debug: "white=\(String(format: "%.2f", whiteRatio)) \(note) index=\(indexBmp.width)x\(indexBmp.height) rows=\(rows.count) blobs=[\(all)]")
    }

    // Row 0: rank glyph.
    var rank: String?
    var rankEvidence = ""
    let rankMask = maskFromBlobs(indexMask, rows[0])
    do {
        // Ten is the only two-part rank, and deciding that structurally
        // sidesteps the single-character OCR that reads "10" as "0", "O" or
        // "IO". But blob count alone is not enough: a king whose strokes fail
        // to connect also arrives as two pieces, and was read as a ten with the
        // hand on screen. A ten is also much wider than any single glyph, so
        // the width of the whole group has to agree.
        var minX = Int.max, maxX = Int.min, minY = Int.max, maxY = Int.min
        for blob in rows[0] {
            minX = min(minX, blob.minX); maxX = max(maxX, blob.maxX)
            minY = min(minY, blob.minY); maxY = max(maxY, blob.maxY)
        }
        let groupAspect = Double(maxX - minX + 1) / Double(max(maxY - minY + 1, 1))

        // Shape comparison against references rendered from the client's own
        // typeface, constrained by how many closed loops the glyph has and
        // where they sit. Text recognition remains only as a fallback for when
        // the client assets have not been extracted: everything it needed --
        // tiling the glyph to look like a word, voting across copies, deciding
        // "ten" from the blob count -- was scaffolding around reading a known
        // shape as unknown text.
        let metrics = holeMetrics(rankMask)
        let scores = CardTemplates.shared.rankScores(rankMask)
        rankEvidence = "holes=\(metrics.count) aspect=" + String(format: "%.2f", groupAspect)
            + " parts=\(rows[0].count) top=" + scores.prefix(3)
                .map { $0.0 + String(format: ":%.3f", $0.1) }.joined(separator: ",")
        // With the client's own shapes in hand, a low score means the crop is
        // not a rank -- not that a weaker reader should be asked. Both weaker
        // readers below exist for the case where the assets could not be
        // extracted, and both of them guess: the two-part rule read a rank
        // glyph and its neighbour's, caught in one crop, as a ten, and text
        // recognition on an isolated glyph is the unreliability this whole
        // approach was built to escape. Measured, a real ten scores 0.75 and is
        // taken by the template outright; the two-part rule only ever fired on
        // a crop that had strayed onto a second card, where nothing scored
        // above 0.41.
        let templatesReady = CardTemplates.shared.isLoaded
        if let match = CardTemplates.shared.matchRank(rankMask, holes: metrics.count, holeY: metrics.centreY),
           match.score >= 0.45 {
            rank = match.label
            rankEvidence += " picked=" + match.label + String(format: ":%.3f", match.score)
        } else if templatesReady {
            rankEvidence += " picked=nil:belowFloor"
        } else if rows[0].count >= 2 && groupAspect > 0.70 {
            rank = "10"
            rankEvidence += " picked=10:byShape"
        } else if let img = renderMaskForOCR(rankMask, repeats: 4) {
            // Rendering the tiled image is only paid for on this path; doing it
            // unconditionally cost as much as the recognition it replaced.
            if let dir = debugDir {
                writePNG(img, to: dir.appendingPathComponent("\(label)-rank.png"))
            }
            rank = recognizeRankText(img, holes: metrics.count)
            rankEvidence += " picked=" + (rank ?? "nil") + ":byOCR"
        }
    }

    // Row 1: suit pip.
    let pipMask = maskFromBlobs(indexMask, rows[1])
    if let dir = debugDir, let img = renderMaskForOCR(pipMask, scale: 4, padding: 4) {
        writePNG(img, to: dir.appendingPathComponent("\(label)-pip.png"))
    }
    let pipColour = meanInkColour(pipMask, in: indexBmp)
    let suitResult = classifySuit(pip: pipMask, isRed: indexMask.isRed, colour: pipColour)

    if let c = pipColour {
        dbg += String(format: " pip=(%.0f,%.0f,%.0f)", c.r, c.g, c.b)
    }
    if let sr = suitResult {
        dbg += String(format: " suit=%@%@ d=%.4f runnerUp=%.4f margin=%.4f",
                      sr.suit, sr.decidedByColour ? "(colour)" : "",
                      sr.distance, sr.runnerUp, sr.runnerUp - sr.distance)
    }
    dbg += " rank=\(rank ?? "nil") \(rankEvidence)"

    var card: String?
    if let r = rank, let s = suitResult?.suit {
        card = r + s
    }

    return CardReading(card: card,
                       rank: rank,
                       suit: suitResult?.suit,
                       whiteRatio: whiteRatio,
                       profile: suitResult?.profile ?? [],
                       suitDistance: suitResult?.distance ?? .infinity,
                       debug: dbg)
}

func writePNG(_ img: CGImage, to url: URL) {
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil) else { return }
    CGImageDestinationAddImage(dest, img, nil)
    CGImageDestinationFinalize(dest)
}

// MARK: - Amount parsing

func parseAmount(_ str: String) -> Double {
    let clean = str.replacingOccurrences(of: "Pot", with: "")
                   .replacingOccurrences(of: "pot", with: "")
                   .replacingOccurrences(of: "POT", with: "")
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

// MARK: - Whole-table analysis

func analyzeTable(cgImg: CGImage, title: String, debugDir: URL? = nil) -> ParsedTableState {
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
        is_hero_turn: false,
        small_blind: 0,
        big_blind: 0,
        second_board: []
    )

    var nameItems: [(name: String, box: CGRect)] = []
    var numberItems: [(val: Double, box: CGRect)] = []
    var actionButtons: [(label: String, box: CGRect)] = []
    var seatBadges: [(label: String, box: CGRect)] = []

    for item in texts {
        let t = item.text.trimmingCharacters(in: .whitespacesAndNewlines)
        let b = item.box // Vision space: bottom-left origin
        let lower = t.lowercased()

        if lower.contains("nlh") || lower.contains("plo") {
            state.table_id = t
            let blinds = parseBlinds(t)
            state.small_blind = blinds.small
            state.big_blind = blinds.big
        }

        if lower.contains("pot") {
            let pVal = parseAmount(t)
            if pVal > 0 {
                state.pot = pVal
            }
        }

        // The action buttons across the bottom of the client are the one place
        // the amount owed is stated outright. Reading "Call 77,680" is far more
        // reliable than reconstructing it from chips on the felt, and without
        // it CurrentBet stays zero, pot odds stay zero, and folding becomes
        // unreachable -- the engine recommended CHECK facing an all-in for
        // hero's whole stack because it believed nothing was owed.
        if isActionButtonRegion(b) {
            if lower == "check" || lower == "fold" || lower.contains("call") || lower.contains("bet") || lower.contains("raise") || lower.contains("all-in") {
                state.is_hero_turn = true
                actionButtons.append((label: lower, box: b))
            }
        }

        for badge in seatActionBadges where lower == badge {
            seatBadges.append((label: badge == "all in" ? "all-in" : badge, box: b))
        }

        if lower.contains("pair") || lower.contains("high card") || lower.contains("straight") || lower.contains("flush") || lower.contains("full house") || lower.contains("three of a kind") {
            if b.origin.y < 0.15 && b.origin.x > 0.30 && b.origin.x < 0.70 {
                state.hero_made_hand = t
            }
        }

        let numVal = parseAmount(t)
        let isPureNumber = numVal > 0 && !lower.contains("pot") && !lower.contains("nlh") && !lower.contains("plo")
        if isPureNumber && b.size.height < 0.06 {
            numberItems.append((val: numVal, box: b))
        } else if looksLikeSeatName(t)
            && !lower.contains("pot") && !lower.contains("fold") && !lower.contains("check")
            && !lower.contains("call") && !lower.contains("bet") && !lower.contains("raise")
            && !lower.contains("empty") && !lower.contains("coin") && !lower.contains("nlh")
            && !lower.contains("plo") && !lower.contains("find") && !lower.contains("pair")
            && !lower.contains("flush") && !lower.contains("straight") && !lower.contains("house")
            && !lower.contains("poker") && !lower.contains("sit") {
            nameItems.append((name: t, box: b))
        }
    }

    // Amount owed, taken from the Call/Bet/Raise button. CoinPoker renders the
    // amount either on the same line ("Call 77,680") or stacked directly under
    // the word, so both are handled. Hero's own posted bet is not separated out
    // here: the button already states what is left to pay, which is exactly
    // what the engine needs.
    for button in actionButtons {
        if button.label.contains("fold") || button.label == "check" { continue }

        var amount = parseAmount(button.label)
        if amount <= 0 {
            var best: CGFloat = 1000
            for numItem in numberItems where isActionButtonRegion(numItem.box) {
                let dx = abs(numItem.box.midX - button.box.midX)
                let dy = button.box.midY - numItem.box.midY // stacked below
                if dx < 0.06 && dy >= -0.01 && dy < 0.06 && dx + abs(dy) < best {
                    best = dx + abs(dy)
                    amount = numItem.val
                }
            }
        }

        if amount > 0 {
            if button.label.contains("call") {
                state.current_bet = amount
            } else if state.min_raise <= 0 {
                // A Bet/Raise button states the currently proposed size, which
                // is the minimum the client will accept.
                state.min_raise = amount
            }
        }
    }

    // Seat numbers follow table position, not OCR emission order, so a name
    // appearing or dropping out for one frame no longer renumbers everyone.
    let ordered = nameItems.sorted { seatOrderKey($0.box) < seatOrderKey($1.box) }

    var players: [ParsedSeat] = []
    var seatBoxes: [CGRect] = []
    var usedAsStack = Set<String>()
    for nameItem in ordered {
        // A real seat is a nameplate with a chip count rendered directly below
        // it. Interface text has no such number, which separates seats from
        // buttons and overlays far more reliably than a blacklist of words:
        // live captures produced "Play Next Game", "9 OUTS", "21.43%", "100%"
        // and "V X/F" as players, putting seven of them on a six-max table.
        // Since opponent count now drives the EV calculation, those ghosts
        // corrupted the advice rather than merely cluttering a list.
        //
        // Note this distinguishes "a zero was read" from "no number was found",
        // so a genuinely all-in player showing 0 chips is still a seat.
        var stackVal: Double = 0.0
        var foundStack = false
        var closestDist: CGFloat = 1000.0
        var closestNum = CGRect.zero
        for numItem in numberItems {
            let dx = abs(numItem.box.origin.x - nameItem.box.origin.x)
            let dy = nameItem.box.origin.y - numItem.box.origin.y
            if dx < 0.08 && dy >= 0 && dy < 0.08 {
                let dist = dx + dy
                if dist < closestDist {
                    closestDist = dist
                    stackVal = numItem.val
                    closestNum = numItem.box
                    foundStack = true
                }
            }
        }
        guard foundStack else { continue }
        usedAsStack.insert(stackKey(closestNum))

        let badge = badgeForSeat(nameBox: nameItem.box, candidates: seatBadges)

        players.append(ParsedSeat(
            seat_number: seatIndex(for: nameItem.box),
            player_id: nameItem.name,
            player_name: nameItem.name,
            stack: stackVal,
            current_bet: 0,
            is_active: true,
            is_folded: badge == "fold",
            last_action: badge,
            position: "",
            cards: []
        ))
        seatBoxes.append(nameItem.box)
    }
    // One RGBA conversion of the frame, shared by every geometric search.
    // Each search used to convert the whole image again -- four times a frame,
    // on top of scanning it.
    // Half resolution: a card face is still ~75 pixels wide, which is ample for
    // locating a rectangle, and the blob passes cost a quarter as much.
    let frame = Bitmap(cgImage: cgImg, downscale: 4)

    let boardRows = frame.map { findBoardRows(bmp: $0) } ?? []
    let boardRects = boardRows.first ?? []
    let secondBoardRects = boardRows.count > 1 ? boardRows[1] : []
    // The whole white region, not cards cut out of it: hero's two overlap, and
    // how many cards a region holds is decided from the corners visible inside
    // it rather than from its outline.
    let heroRects = frame.map { findHeroCardRegions(bmp: $0, excluding: boardRects) } ?? []

    // Position, from the dealer button. Seat boxes come from Vision's
    // bottom-left space and the button from top-left pixel space, so the y axis
    // is flipped before comparing them -- mixing the two conventions is what
    // produced the misaligned card crops this file used to have.
    if !players.isEmpty, let frame, let button = findDealerButton(bmp: frame, excluding: boardRects + heroRects) {
        var nearest = 0
        var bestDist = Double.infinity
        for (i, box) in seatBoxes.enumerated() {
            let bx = Double(box.midX)
            let by = 1.0 - Double(box.midY)
            let d = (bx - Double(button.x)) * (bx - Double(button.x))
                + (by - Double(button.y)) * (by - Double(button.y))
            if d < bestDist {
                bestDist = d
                nearest = i
            }
        }

        // Seats in clockwise table order, so positions can be walked round.
        let order = players.indices.sorted {
            seatOrderKey(seatBoxes[$0]) < seatOrderKey(seatBoxes[$1])
        }
        var buttonRank = 0
        for (rank, idx) in order.enumerated() where idx == nearest {
            buttonRank = rank
        }

        let names = assignPositions(buttonIndex: buttonRank, seatCount: order.count)
        for (rank, idx) in order.enumerated() where rank < names.count {
            players[idx].position = names[rank]
        }
    }


    // Card slots are reported positionally, with an empty string where a slot
    // holds a card that did not resolve. Appending only successful reads
    // silently shifts every later card left: a flop whose middle card was
    // missed came through as two cards in the wrong places, and the state
    // stabiliser -- which fills unknown slots from earlier frames -- then had
    // nothing to line up against. Trailing empties are dropped so the street
    // still follows the number of cards actually dealt.
    var board: [String] = []
    for (i, rect) in boardRects.enumerated() {
        let reading = readCardRect(cgImg: cgImg, slotRect: rect, debugDir: debugDir, label: "board\(i + 1)")
        board.append(reading.card ?? "")
    }
    while let last = board.last, last.isEmpty {
        board.removeLast()
    }
    state.community_cards = board

    // A hand run twice shows a second board beneath the first. It carries no
    // decision -- everyone is already all-in -- but half the pot turns on it,
    // so it is recorded rather than discarded.
    var second: [String] = []
    for (i, rect) in secondBoardRects.enumerated() {
        let reading = readCardRect(cgImg: cgImg, slotRect: rect, debugDir: debugDir, label: "board2-\(i + 1)")
        second.append(reading.card ?? "")
    }
    while let last = second.last, last.isEmpty {
        second.removeLast()
    }
    state.second_board = second

    // Both hero slots are always reported, empty when unresolved. Live, a king
    // whose corner was covered by the position badge dropped out and the queen
    // beside it slid into slot 0; hero then looked like a one-card hand, no
    // advice was produced at all, and the HUD kept showing the previous one.
    // Held in place, the stabiliser fills the missing slot from a later frame.
    var hero: [String] = []
    var heroResolved = false
    for (i, region) in heroRects.enumerated() {
        for (_, reading) in readCardsInRegion(cgImg: cgImg, region: region,
                                              debugDir: debugDir, label: "hero\(i + 1)-") {
            guard hero.count < 2 else { break }
            hero.append(reading.card ?? "")
            if reading.card != nil { heroResolved = true }
        }
    }
    while hero.count < 2 { hero.append("") }
    state.hero_cards = heroResolved ? hero : []

    // Hero is whoever sits at the bottom of the table, and only when hole cards
    // are actually visible there -- otherwise this is a spectator view and
    // there is no hero at all. Until now hero_id was the constant "Hero", which
    // matched no seat, so hero's own stack was never read and every effective
    // stack calculation silently fell back to the opponents'.
    // Bets sitting on the felt in front of each player. This is the only place
    // the amount of anyone's wager is written down, and without it the action
    // stream carried nothing but action types: every recorded bet and raise had
    // an amount of zero, so no opponent model could tell a minimum raise from a
    // shove.
    //
    // A wager is a number that is not a stack, sits between its owner and the
    // middle of the table, and is at least a small blind. The last condition is
    // what keeps the action timer counting down from fifteen out of the data.
    if !players.isEmpty {
        let minBet = state.small_blind > 0 ? state.small_blind : 1
        for numItem in numberItems {
            if usedAsStack.contains(stackKey(numItem.box)) { continue }
            if isActionButtonRegion(numItem.box) { continue }
            if numItem.val < minBet { continue }

            let nx = Double(numItem.box.midX)
            let ny = Double(numItem.box.midY)
            let toCentre = (nx - 0.5) * (nx - 0.5) + (ny - 0.5) * (ny - 0.5)

            var nearest = -1
            var bestDist = Double.infinity
            for (i, box) in seatBoxes.enumerated() {
                let dx = Double(box.midX) - nx
                let dy = Double(box.midY) - ny
                let d = dx * dx + dy * dy
                if d < bestDist {
                    bestDist = d
                    nearest = i
                }
            }

            // Chips gathered in the middle belong to the pot, not to a player.
            guard nearest >= 0, bestDist < toCentre, bestDist < 0.0625 else { continue }
            if players[nearest].current_bet < numItem.val {
                players[nearest].current_bet = numItem.val
            }
        }
    }

    // The table's current bet is the largest wager standing on the felt. Taken
    // from the action button alone it was only ever hero's own debt, so nobody
    // else's price to continue could be worked out at all.
    let highestBet = players.map(\.current_bet).max() ?? 0
    if highestBet > 0 {
        state.current_bet = highestBet
    }

    // Cards turned face up anywhere on the felt, attributed to whichever seat
    // they sit nearest. Face-down cards are red backs, so anything found here
    // is genuinely revealed -- which happens only at a showdown.
    let allFaces = frame.map {
        findCardFaceRects(bmp: $0,
                          region: CGRect(x: 0.02, y: 0.10, width: 0.96, height: 0.86),
                          limit: 14,
                          excluding: boardRects + heroRects)
    } ?? []

    if !players.isEmpty {
        let imgW = Double(cgImg.width)
        let imgH = Double(cgImg.height)

        var perSeat = [[CGRect]](repeating: [], count: players.count)
        for face in allFaces {
            let fx = (Double(face.midX)) / imgW
            let fy = (Double(face.midY)) / imgH

            var nearest = -1
            var bestDist = Double.infinity
            for (i, box) in seatBoxes.enumerated() {
                let bx = Double(box.midX)
                // Vision boxes are bottom-left origin; card rects are top-left.
                let by = 1.0 - Double(box.midY)
                let d = (bx - fx) * (bx - fx) + (by - fy) * (by - fy)
                if d < bestDist {
                    bestDist = d
                    nearest = i
                }
            }
            // Cards belong to a seat only if they are actually beside it; a
            // stray white shape in the middle of the felt belongs to nobody.
            if nearest >= 0 && bestDist < 0.045 {
                perSeat[nearest].append(face)
            }
        }

        for i in players.indices {
            let rects = perSeat[i].sorted { $0.minX < $1.minX }.prefix(2)
            guard rects.count == 2 else { continue }
            var cards: [String] = []
            var anyRead = false
            for (n, rect) in rects.enumerated() {
                let reading = readCardRect(cgImg: cgImg, slotRect: rect,
                                           debugDir: debugDir,
                                           label: "seat\(i)-card\(n + 1)")
                cards.append(reading.card ?? "")
                if reading.card != nil { anyRead = true }
            }
            if anyRead {
                players[i].cards = cards
            }
        }
    }

    // Hero is the bottom seat, found by lowest nameplate rather than by the
    // clockwise angle used for seat numbering: that angle wraps from 2pi back
    // to 0 exactly at bottom-centre, which is where hero sits, so a nameplate a
    // pixel left of centre scored ~2pi and lost to a seat across the table.
    // Vision's boxes are bottom-left origin, so the bottom of the screen is the
    // smallest y.
    if heroResolved, !players.isEmpty {
        var bestIdx = 0
        var lowest = CGFloat.infinity
        for (i, box) in seatBoxes.enumerated() where box.midY < lowest {
            lowest = box.midY
            bestIdx = i
        }
        state.hero_id = players[bestIdx].player_id
    }

    // A showdown is simply more than one player with cards face up. This is
    // the terminal state the pipeline never had: street was inferred from the
    // number of board cards, which tops out at five, so no hand ever ended and
    // none was persisted or profiled. Recognising it here also captures the one
    // moment opponents' holdings are visible.
    // Assigned only once every per-seat field is final: Swift arrays are value
    // types, so writing state.seats earlier and mutating `players` afterwards
    // silently discarded the revealed cards.
    state.seats = players

    // A showdown is more than one player with cards face up, or a hand being
    // run twice -- which only happens once everyone is already all-in. Either
    // way there is no decision left to advise on, and this is the terminal
    // state the pipeline never had: street was inferred from the number of
    // board cards, which tops out at five, so no hand ever ended and none was
    // persisted or profiled.
    let showing = players.filter { $0.cards.contains { !$0.isEmpty } }.count
    if showing >= 2 || !state.second_board.isEmpty {
        state.street = "showdown"
        return state
    }

    switch state.community_cards.count {
    case 0: state.street = "preflop"
    case 3: state.street = "flop"
    case 4: state.street = "turn"
    case 5: state.street = "river"
    default:
        state.street = state.community_cards.count >= 4 ? "turn" : "flop"
    }

    return state
}

/// Whether an OCR string can be a player nickname at all.
///
/// The rejected forms are not guesses -- they are what a recorded live session
/// actually produced as "players": action badges printed on the nameplate
/// itself ("ALL-IN", "STRADDLE"), lobby chrome ("Join", "Quick Join", "Play
/// Next Game", "App Health", "Helpdesk"), the odds overlay ("84.44%", "100%"),
/// stake labels ("1K/2K", "250/500", "2.5X") and OCR runs that merged two chip
/// counts into one string ("280,837 205,468"). Seven such ghosts appeared on a
/// six-max table, and since opponent count now drives the EV calculation they
/// corrupted the advice rather than merely cluttering a list.
func looksLikeSeatName(_ t: String) -> Bool {
    let trimmed = t.trimmingCharacters(in: .whitespacesAndNewlines)
    guard trimmed.count >= 4 else { return false }

    if trimmed.contains("%") || trimmed.contains("/") { return false }

    let lower = trimmed.lowercased()
    let badges = ["all-in", "all in", "allin", "straddle", "sitting out",
                  "quick join", "ouick join", "join", "play next", "app health",
                  "helpdesk", "waiting", "deciding", "next game", "buy in",
                  "buy-in", "rebuy", "add chips", "outs"]
    for badge in badges where lower.contains(badge) { return false }

    // A nickname carries letters. Strings that are only digits, separators and
    // spaces are chip counts the recognizer ran together.
    let letters = trimmed.unicodeScalars.filter { CharacterSet.letters.contains($0) }
    if letters.count < 3 { return false }

    // Badges are set in full caps; nicknames essentially never are.
    let cased = trimmed.unicodeScalars.filter { CharacterSet.letters.contains($0) }
    let uppers = cased.filter { CharacterSet.uppercaseLetters.contains($0) }
    if cased.count >= 4 && uppers.count == cased.count { return false }

    return true
}

/// Finds hero's hole cards by looking for card faces, rather than at fixed
/// coordinates.
///
/// Fixed hero slots were measured once and then failed live: the client does
/// not place hero's cards at a constant offset, so a slot that framed them on
/// one table missed them on the next and hero read as a spectator with cards
/// plainly on screen. Board cards sit in a fixed rack and stay measured; hero's
/// two do not, so they are located by their white faces each frame.
func findHeroCardRects(bmp: Bitmap, excluding board: [CGRect]) -> [CGRect] {
    // The lower middle of the table, clear of the board rack above and the
    // action buttons below.
    return findCardFaceRects(bmp: bmp,
                             region: CGRect(x: 0.20, y: 0.58, width: 0.60, height: 0.36),
                             limit: 2,
                             excluding: board)
}

/// The community cards, located by their faces rather than by a measured rack.
///
/// The rack was the last fixed geometry left. Normalized coordinates survive a
/// proportional resize, but nothing else: change the window's aspect ratio and
/// a measured rectangle slides off the cards it was measured against. Finding
/// the faces costs one more pass over the middle band and removes the
/// assumption entirely.
func findBoardCardRects(bmp: Bitmap) -> [CGRect] {
    return findBoardRows(bmp: bmp).first ?? []
}

/// Board rows, top first. There is normally one; a hand run twice has two.
func findBoardRows(bmp: Bitmap) -> [[CGRect]] {
    let rects = findCardFaceRects(bmp: bmp,
                                  region: CGRect(x: 0.10, y: 0.24, width: 0.80, height: 0.40),
                                  limit: 12)
    guard !rects.isEmpty else { return [] }

    // Group into rows. Normally there is one, but the showdown animation adds a
    // second beneath it holding the winner's five cards. The community board is
    // the upper row, so rows are walked from the top and the first that could
    // be a board is taken.
    let sorted = rects.sorted { $0.midY < $1.midY }
    var rows: [[CGRect]] = []
    for rect in sorted {
        if var last = rows.last, let first = last.first,
           abs(rect.midY - first.midY) < first.height * 0.6 {
            last.append(rect)
            rows[rows.count - 1] = last
        } else {
            rows.append([rect])
        }
    }

    let boards = rows.filter { $0.count >= 3 }.map { $0.sorted { $0.minX < $1.minX } }
    if boards.isEmpty {
        return [rows[0].sorted { $0.minX < $1.minX }]
    }
    return boards
}

/// Width of a card as a fraction of its height, measured from this client's
/// artwork (150x216 at a 2290-pixel-wide capture, and the table scales as a
/// whole so the ratio holds at any window size).
let cardAspectRatio: CGFloat = 0.694

/// Card faces within a region of the frame, largest first, left to right.
///
/// Face-down cards are red backs, so searching for white faces finds exactly
/// the cards that are actually showing -- which is what makes the same routine
/// serve both hero's hand and an opponent's cards at showdown.
func findCardFaceRects(bmp: Bitmap, region: CGRect, limit: Int, excluding: [CGRect] = []) -> [CGRect] {
    var faces: [CGRect] = []
    for rect in findCardFaceBlobs(bmp: bmp, region: region, excluding: excluding) {
        let aspect = Double(rect.width) / Double(max(rect.height, 1))
        switch aspect {
        case 0.55..<0.95:
            faces.append(rect)
        case 0.95..<1.90:
            // Two cards side by side, their white faces touching. Splitting the
            // merged region down the middle is wrong whenever they overlap: the
            // left card shows less than half, so the midpoint falls inside the
            // right card and its index column -- rank and pip both -- ends up
            // outside the crop.
            //
            // This proportional split is now only a fallback. Where cards
            // overlap -- hero's hand, an opponent's at showdown -- the caller
            // uses findCardIndexes instead, which finds each card's corner as
            // ink. This path still serves the board, whose cards never touch.
            let cardW = min(rect.height * cardAspectRatio, rect.width)
            faces.append(CGRect(x: rect.minX, y: rect.minY, width: cardW, height: rect.height))
            faces.append(CGRect(x: rect.maxX - cardW, y: rect.minY, width: cardW, height: rect.height))
        case 0.28..<0.45:
            // Two cards stacked vertically: a hand run twice shows a second
            // board beneath the first.
            let half = rect.height / 2
            faces.append(CGRect(x: rect.minX, y: rect.minY, width: rect.width, height: half))
            faces.append(CGRect(x: rect.minX, y: rect.midY, width: rect.width, height: half))
        default:
            continue
        }
    }
    return faces.sorted { $0.width * $0.height > $1.width * $1.height }
        .prefix(limit)
        .sorted { $0.minX < $1.minX }
}

/// The white card-face regions themselves, unsplit and in full-frame pixels.
///
/// A region is whatever the white mask came back as: one card, or several whose
/// faces touch. Deciding how many cards are inside is deliberately left to the
/// caller, because deciding it from the region's shape is what failed. The
/// bands used to be 0.55..0.95 for one card and 1.05..1.80 for two, and they
/// did not meet: a pair overlapped by 41-52% of a card came back at aspect
/// 0.97..1.04, matched neither band, and hero's whole hand was dropped without
/// a trace. Overlapped further still it matched the *single* card band, and a
/// crop straddling the seam read 6d 3d as a confident "Jd".
func findCardFaceBlobs(bmp: Bitmap, region: CGRect, excluding: [CGRect] = []) -> [CGRect] {
    let w = bmp.width
    let h = bmp.height

    let yStart = max(Int(region.minY * Double(h)), 0)
    let yEnd = min(Int(region.maxY * Double(h)), h)
    let xStart = max(Int(region.minX * Double(w)), 0)
    let xEnd = min(Int(region.maxX * Double(w)), w)
    guard yEnd > yStart, xEnd > xStart else { return [] }

    var mask = [Bool](repeating: false, count: w * h)
    var hits = 0
    for y in yStart..<yEnd {
        let row = y * w
        for x in xStart..<xEnd {
            let (r, g, b) = bmp.rgb(x, y)
            if r > 190 && g > 190 && b > 190 {
                mask[row + x] = true
                hits += 1
            }
        }
    }
    guard hits > 0 else { return [] }

    // Regions already claimed by something else -- in practice the board, which
    // is read first and would otherwise be handed to whichever seat happened to
    // sit nearest.
    let k = CGFloat(bmp.scale)
    for rect in excluding {
        let scaled = CGRect(x: rect.minX / k, y: rect.minY / k,
                            width: rect.width / k, height: rect.height / k)
        clearRect(&mask, width: w, height: h,
                  rect: scaled.insetBy(dx: -CGFloat(w) * 0.004, dy: -CGFloat(h) * 0.004))
    }

    // A card face is a tall rectangle. The bands are wide because the client
    // scales with the window. The upper width bound allows for two cards
    // touching: they are drawn overlapping, and when their white faces meet
    // they come back as one wide blob rather than two -- which silently dropped
    // a whole hand on the frames where they happened to touch.
    // A card is about 6.5% of the frame width and roughly 0.69 as wide as it is
    // tall, at every window size -- the client scales the whole table together.
    // The lower bound used to be loose enough to admit a 60x64 fragment of
    // interface, which was read as a king and handed to hero as a hole card on
    // a hand hero had already folded. Advice computed on a fabricated hand is
    // worse than no advice.
    let minW = Int(0.040 * Double(w))
    let maxW = Int(0.180 * Double(w))

    var regions: [CGRect] = []
    for blob in findBlobBoxes(mask: mask, width: w, height: h) {
        guard blob.width >= minW, blob.width <= maxW else { continue }
        let fill = Double(blob.size) / Double(max(blob.width * blob.height, 1))
        guard fill > 0.55 else { continue }

        let aspect = Double(blob.width) / Double(max(blob.height, 1))
        guard aspect >= 0.28, aspect < 1.90 else { continue }
        regions.append(blob.rect)
    }

    return regions.sorted { $0.width * $0.height > $1.width * $1.height }
        .map { CGRect(x: $0.minX * k, y: $0.minY * k, width: $0.width * k, height: $0.height * k) }
        .sorted { $0.minX < $1.minX }
}

/// One card's index corner -- the rank over its pip -- located as ink.
struct CardIndex {
    /// The corner itself, in full-frame pixels, ready to be read.
    let indexRect: CGRect
    /// The card the corner belongs to, inferred from it. Only the corner is
    /// read; this exists for callers that need to know where the card sits.
    let cardRect: CGRect
}

/// Finds every card inside a white region by clustering the ink of their index
/// corners, rather than by cutting the region at a boundary.
///
/// The reason this works is a property of how cards are dealt on screen: they
/// are fanned so that every index corner stays visible, because a player has to
/// read them. So overlap never hides a corner -- it only destroys the geometry
/// that was being used to find one. Measured on two live frames, the corners of
/// an overlapping pair come back as two clean clusters about 90 pixels apart on
/// a 149-pixel card, with the rank at 29x57 and the pip at 44x49.
///
/// Deriving the split instead cost three separate failures live: a pair
/// overlapped 41-52% vanished entirely, past that it read as one card with both
/// rank and suit wrong, and in between the answer depended on where the blob's
/// aspect landed relative to a threshold.
func findCardIndexes(cgImg: CGImage, region: CGRect) -> [CardIndex] {
    guard let img = cgImg.cropping(to: region), let bmp = Bitmap(cgImage: img) else { return [] }
    let h = Double(bmp.height)
    guard h >= 40 else { return [] }

    let ink = makeInkMask(bmp)
    let blobs = findBlobs(ink)

    // Everything is sized against the card's height, which is the one dimension
    // overlap cannot change. Measured on this client at a 216-pixel card: rank
    // glyph 29x57 (0.13 x 0.26), index pip 44x49 (0.20 x 0.23). The centre
    // artwork is both larger and lower; the card's own border and the seam
    // where the next card lies over it run the full height.
    var candidates: [Blob] = []
    for b in blobs {
        let bh = Double(b.height) / h
        let bw = Double(b.width) / h
        guard bh >= 0.08, bh <= 0.38, bw >= 0.03, bw <= 0.32 else { continue }
        guard Double(b.maxY) / h <= 0.72 else { continue }
        guard b.size >= 40 else { continue }
        candidates.append(b)
    }
    guard !candidates.isEmpty else { return [] }

    // Cluster on the left edge. Within one card the rank and its pip start
    // within a few pixels of each other; between cards they are a whole card
    // pitch apart, and the pitch cannot fall below the width of the corner
    // itself without hiding it. Clustering on the gap between blobs instead
    // would not survive a deep overlap, where that gap shrinks to nothing.
    let sorted = candidates.sorted { $0.minX < $1.minX }
    let spread = 0.22 * h
    var clusters: [[Blob]] = []
    for b in sorted {
        if let last = clusters.last, let anchor = last.map({ $0.minX }).min(),
           Double(b.minX - anchor) <= spread {
            clusters[clusters.count - 1].append(b)
        } else {
            clusters.append([b])
        }
    }

    var found: [CardIndex] = []
    for cluster in clusters {
        // A corner is a rank over a pip: one blob alone cannot be read, and a
        // cluster that starts halfway down the card is the centre artwork of a
        // face card, not a corner.
        guard cluster.count >= 2 else { continue }
        guard let top = cluster.map({ $0.minY }).min(), Double(top) / h <= 0.24 else { continue }

        let minX = cluster.map { $0.minX }.min()!
        let maxX = cluster.map { $0.maxX }.max()!
        let maxY = cluster.map { $0.maxY }.max()!

        // A pixel of margin either side: the crop is re-masked when it is read,
        // and a glyph flush against the edge loses its outermost stroke.
        let pad = 2.0
        let ix = region.minX + CGFloat(minX) - pad
        let iy = region.minY + CGFloat(top) - pad
        let iw = CGFloat(maxX - minX) + 1 + pad * 2
        let ih = CGFloat(maxY - top) + 1 + pad * 2

        // The corner sits about 0.05 of a card height in from the card's left
        // edge; the card is as wide as its height times the client's ratio.
        let cardW = CGFloat(h) * cardAspectRatio
        let cardX = max(region.minX, region.minX + CGFloat(minX) - CGFloat(0.05 * h))

        found.append(CardIndex(
            indexRect: CGRect(x: ix, y: iy, width: iw, height: ih),
            cardRect: CGRect(x: cardX, y: region.minY, width: cardW, height: region.height)))
    }

    return found.sorted { $0.indexRect.minX < $1.indexRect.minX }
}

/// Reads every card in a white region, overlapping or not.
func readCardsInRegion(cgImg: CGImage, region: CGRect,
                       debugDir: URL? = nil, label: String = "") -> [(rect: CGRect, reading: CardReading)] {
    return findCardIndexes(cgImg: cgImg, region: region).enumerated().map { i, found in
        (found.cardRect,
         readIndexRect(cgImg: cgImg, indexRect: found.indexRect, whiteRatio: 1.0,
                       note: "clustered", debugDir: debugDir, label: "\(label)\(i + 1)"))
    }
}

/// Hero's hole cards, as white regions rather than as individual cards.
///
/// Hero's two are drawn overlapping, so how many cards a region holds is
/// decided by readCardsInRegion from the corners it can see, not here from the
/// region's shape.
func findHeroCardRegions(bmp: Bitmap, excluding board: [CGRect]) -> [CGRect] {
    return findCardFaceBlobs(bmp: bmp,
                             region: CGRect(x: 0.20, y: 0.58, width: 0.60, height: 0.36),
                             excluding: board)
        .sorted { $0.width * $0.height > $1.width * $1.height }
        .prefix(2)
        .sorted { $0.minX < $1.minX }
}

/// Locates the dealer button on the felt.
///
/// The button is read by colour and shape rather than by its "D" glyph: Vision
/// will not reliably read an isolated single character, which is the same
/// limitation that made card ranks need tiling. The button is a saturated red
/// near-circular chip of a distinctive size, and nothing else on the felt
/// matches all three at once.
///
/// Returns the centre in normalized top-left coordinates.
func findDealerButton(bmp: Bitmap, excluding cardRects: [CGRect] = []) -> CGPoint? {
    let w = bmp.width
    let h = bmp.height

    // Confine the search to the felt: the action buttons along the bottom are
    // also red, and much larger.
    let yStart = Int(0.10 * Double(h))
    let yEnd = Int(0.80 * Double(h))
    guard yEnd > yStart else { return nil }

    var mask = [Bool](repeating: false, count: w * h)
    var hits = 0
    for y in yStart..<yEnd {
        let row = y * w
        for x in 0..<w {
            let (r, g, b) = bmp.rgb(x, y)
            if r > 130 && r > g * 2.0 && r > b * 2.0 {
                mask[row + x] = true
                hits += 1
            }
        }
    }
    guard hits > 0 else { return nil }

    // Card faces are full of red: pips, and the red artwork on every face card.
    // Searching over them found the queen's robe before it found the button.
    let k = CGFloat(bmp.scale)
    for rect in cardRects {
        let scaled = CGRect(x: rect.minX / k, y: rect.minY / k,
                            width: rect.width / k, height: rect.height / k)
        clearRect(&mask, width: w, height: h,
                  rect: scaled.insetBy(dx: -CGFloat(w) * 0.005, dy: -CGFloat(h) * 0.005))
    }

    // The chip is roughly this fraction of the frame width; the tolerance is
    // wide because the client scales with the window.
    let minSide = Int(0.010 * Double(w))
    let maxSide = Int(0.040 * Double(w))

    var best: BlobBox?
    for blob in findBlobBoxes(mask: mask, width: w, height: h) {
        guard blob.width >= minSide, blob.width <= maxSide,
              blob.height >= minSide, blob.height <= maxSide else { continue }
        let aspect = Double(blob.width) / Double(max(blob.height, 1))
        guard aspect > 0.65, aspect < 1.55 else { continue }
        // The chip is a red disc with a white "D" punched out of it, so only
        // about 40% of its bounding box is saturated red. The threshold rejects
        // thin highlights and glare streaks without demanding a solid circle --
        // measured at 0.38 on a real capture.
        let fill = Double(blob.size) / Double(max(blob.width * blob.height, 1))
        guard fill > 0.30 else { continue }
        if best == nil || blob.size > best!.size {
            best = blob
        }
    }

    guard let chip = best else { return nil }
    return CGPoint(x: (Double(chip.minX) + Double(chip.width) / 2.0) / Double(w),
                   y: (Double(chip.minY) + Double(chip.height) / 2.0) / Double(h))
}

/// Assigns table positions by walking clockwise from the button.
///
/// Only the seats still at the table are named, so the ordering follows who is
/// actually there rather than a fixed six-seat layout.
func assignPositions(buttonIndex: Int, seatCount: Int) -> [String] {
    guard seatCount > 0, buttonIndex >= 0, buttonIndex < seatCount else {
        return [String](repeating: "", count: max(seatCount, 0))
    }

    // Order from the button outward: the two blinds first, then the earliest
    // position, and the two seats before the button last.
    var names: [String]
    switch seatCount {
    case 1:
        names = ["BTN"]
    case 2:
        names = ["BTN", "BB"]
    case 3:
        names = ["BTN", "SB", "BB"]
    case 4:
        names = ["BTN", "SB", "BB", "UTG"]
    case 5:
        names = ["BTN", "SB", "BB", "UTG", "CO"]
    default:
        names = ["BTN", "SB", "BB", "UTG", "MP", "CO"]
        // Any seats beyond six are middle positions.
        while names.count < seatCount {
            names.insert("MP", at: 4)
        }
    }

    // The walk runs against the seat ordering, not with it. seatOrderKey
    // increases bottom -> right -> top -> left, whereas the blinds sit the
    // other way round: on a live capture showing the badges outright, the
    // button was on hero at the bottom, SB was the seat below-left of hero and
    // BB the one above it. Walking with the ordering put the blinds on the far
    // side of the table.
    var out = [String](repeating: "", count: seatCount)
    for (offset, name) in names.enumerated() where offset < seatCount {
        let idx = ((buttonIndex - offset) % seatCount + seatCount) % seatCount
        out[idx] = name
    }
    return out
}

/// Action badges as the client prints them on a player's nameplate.
let seatActionBadges: [String] = ["fold", "check", "call", "raise", "bet", "all-in", "all in"]

/// The badge sitting directly above a nameplate, if any.
///
/// Above is the discriminator that matters: hero's own nameplate has the action
/// buttons below it and its badge above, so requiring the badge to be higher on
/// screen keeps the "Fold" button from being read as hero having folded. Vision
/// boxes are bottom-left origin, so higher on screen means a larger y.
func badgeForSeat(nameBox: CGRect, candidates: [(label: String, box: CGRect)]) -> String {
    var best = ""
    var bestDist = CGFloat.infinity
    for c in candidates {
        let dx = abs(c.box.midX - nameBox.midX)
        let dy = c.box.midY - nameBox.midY
        guard dx < 0.07, dy > 0, dy < 0.07 else { continue }
        let dist = dx + dy
        if dist < bestDist {
            bestDist = dist
            best = c.label
        }
    }
    return best
}

/// The strip of action buttons along the bottom of the client, in Vision's
/// bottom-left normalized space. Restricting button parsing to this band keeps
/// the word "call" in a chat message or a player nickname from being read as
/// hero facing a bet.
func isActionButtonRegion(_ box: CGRect) -> Bool {
    return box.origin.y < 0.20 && box.origin.x > 0.40
}

/// Identifies a recognised number by where it sits, so the same reading is not
/// counted both as somebody's stack and as their wager.
func stackKey(_ box: CGRect) -> String {
    return String(format: "%.4f,%.4f", box.midX, box.midY)
}

/// Blinds from a table title such as "NLH 1229111 - 1K/2K (320)".
///
/// They give the scale of the table, which is what separates a bet from the
/// numbers that merely look like one: the action timer counting down from
/// fifteen is not a wager at a table with a two-thousand big blind.
func parseBlinds(_ title: String) -> (small: Double, big: Double) {
    // The pair is written with a slash, after the table number.
    guard let slash = title.firstIndex(of: "/") else { return (0, 0) }

    let before = title[title.startIndex..<slash]
    let after = title[title.index(after: slash)...]

    func trailingAmount(_ s: Substring) -> Double {
        var digits = ""
        for ch in s.reversed() {
            if ch.isNumber || ch == "." || ch == "," || ch == "K" || ch == "k" || ch == "M" || ch == "m" {
                digits.append(ch)
            } else if !digits.isEmpty {
                break
            } else if ch == " " {
                continue
            } else {
                break
            }
        }
        return parseAmount(String(digits.reversed()))
    }

    func leadingAmount(_ s: Substring) -> Double {
        var digits = ""
        for ch in s {
            if ch.isNumber || ch == "." || ch == "," || ch == "K" || ch == "k" || ch == "M" || ch == "m" {
                digits.append(ch)
            } else if !digits.isEmpty {
                break
            } else if ch == " " {
                continue
            } else {
                break
            }
        }
        return parseAmount(digits)
    }

    let small = trailingAmount(before)
    let big = leadingAmount(after)
    guard small > 0, big > 0, big >= small else { return (0, 0) }
    return (small, big)
}

/// Clockwise ordering starting from the hero seat at the bottom of the table.
/// Vision boxes are bottom-left origin, so low `y` is the bottom of the screen.
func seatOrderKey(_ box: CGRect) -> Double {
    let x = Double(box.midX)
    let y = Double(box.midY)
    // Angle around the table centre, measured clockwise from straight down.
    let angle = atan2(x - 0.5, -(y - 0.5))
    return angle < 0 ? angle + 2 * .pi : angle
}

func seatIndex(for box: CGRect) -> Int {
    let key = seatOrderKey(box)
    let idx = Int((key / (2 * .pi)) * 6.0)
    return min(max(idx, 0), 5)
}
