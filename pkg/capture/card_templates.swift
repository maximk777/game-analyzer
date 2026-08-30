import Foundation
import CoreGraphics
import CoreText
import ImageIO

// Card recognition by comparison against the client's own artwork, rather than
// by reading the card as text.
//
// Everything before this was an attempt to work around one fact: Apple's text
// recogniser is built for words and is unreliable on a single isolated glyph.
// Tiling the glyph so it looked like a word, voting across the copies, deciding
// "ten" from blob count, counting enclosed loops to separate a six from an
// eight -- each fixed a symptom of reading a picture of a known shape as though
// it were unknown text.
//
// The shapes are not unknown. CoinPoker is an Electron client, and its own
// bundle carries both halves of a card: the four suit sprites as images, and
// Fira Sans, the typeface the ranks are set in. Rendering the reference from
// the same font and comparing shapes answers the question directly.

/// A reference shape, normalized to a fixed grid so comparison is scale-free.
struct ShapeTemplate {
    let label: String
    let grid: [Bool]
}

/// Grid the shapes are normalized onto. Large enough to separate a six from an
/// eight, small enough that comparison costs nothing.
private let templateWidth = 28
private let templateHeight = 32

/// Normalizes an ink mask onto the comparison grid by sampling its bounding box.
func normalizeShape(_ m: InkMask, width: Int = templateWidth, height: Int = templateHeight) -> [Bool]? {
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

    let bw = maxX - minX + 1
    let bh = maxY - minY + 1
    var grid = [Bool](repeating: false, count: width * height)

    // The shape is stretched to fill the grid rather than letterboxed. The
    // ranks are set in the same typeface the references are rendered from, so
    // their internal shape matches closely once scale is removed -- and the
    // client applies its own scaling, so the observed proportions are not
    // reliable enough to compare against. Letterboxing was tried and read most
    // ranks wrong.
    for gy in 0..<height {
        let y0 = minY + (bh * gy) / height
        let y1 = max(y0 + 1, minY + (bh * (gy + 1)) / height)
        for gx in 0..<width {
            let x0 = minX + (bw * gx) / width
            let x1 = max(x0 + 1, minX + (bw * (gx + 1)) / width)
            var on = 0, total = 0
            for y in y0..<min(y1, m.height) {
                for x in x0..<min(x1, m.width) {
                    total += 1
                    if m.mask[y * m.width + x] { on += 1 }
                }
            }
            grid[gy * width + gx] = total > 0 && on * 2 >= total
        }
    }

    return grid
}

/// Intersection over union of two normalized shapes: 1.0 is identical.
func shapeSimilarity(_ a: [Bool], _ b: [Bool]) -> Double {
    guard a.count == b.count, !a.isEmpty else { return 0 }
    var intersection = 0
    var union = 0
    for i in 0..<a.count {
        if a[i] && b[i] { intersection += 1 }
        if a[i] || b[i] { union += 1 }
    }
    guard union > 0 else { return 0 }
    return Double(intersection) / Double(union)
}

/// Reference shapes for ranks and suits, built from the client's own assets.
final class CardTemplates {
    static let shared = CardTemplates()

    private(set) var ranks: [ShapeTemplate] = []
    private(set) var suits: [ShapeTemplate] = []

    var isLoaded: Bool { !ranks.isEmpty && !suits.isEmpty }

    /// Loads templates from a directory of extracted client assets. Returns
    /// false when the assets are absent, in which case the caller keeps using
    /// text recognition: a missing asset directory must degrade the reading,
    /// never stop it.
    @discardableResult
    func load(assetsDir: String) -> Bool {
        ranks = loadRanks(assetsDir: assetsDir)
        suits = loadSuits(assetsDir: assetsDir)
        return isLoaded
    }

    /// Best matching rank, with its similarity score.
    ///
    /// `holes` is the number of enclosed loops counted in the observed glyph.
    /// Shape similarity alone cannot separate a queen from an eight -- measured
    /// on real cards they land within 0.02 of each other -- but their topology
    /// differs absolutely, and a candidate whose loop count disagrees is simply
    /// not that character.
    func matchRank(_ m: InkMask, holes: Int = -1, holeY: Double = -1) -> (label: String, score: Double)? {
        var candidates = ranks
        if holes >= 0 {
            let filtered = ranks.filter { t in
                guard let want = rankHoleCount[t.label] else { return true }
                return want == holes
            }
            if !filtered.isEmpty { candidates = filtered }
        }

        // Among the single-loop ranks, where the loop sits separates the ones
        // shape alone cannot: a six carries it low, a nine high, a queen in the
        // middle. At reduced capture sizes a queen was read as a six without
        // this.
        if holes == 1, holeY >= 0 {
            let filtered = candidates.filter { t in
                guard let want = rankHoleCentre[t.label] else { return true }
                return abs(want - holeY) < 0.20
            }
            if !filtered.isEmpty { candidates = filtered }
        }

        return bestMatch(m, in: candidates)
    }

    /// Best matching suit, with its similarity score. When the colour of the
    /// pip is known the comparison is restricted to the two suits of that
    /// colour, which turns an unreliable four-way shape decision into a binary
    /// one.
    func matchSuit(_ m: InkMask, isRed: Bool? = nil) -> (label: String, score: Double)? {
        var candidates = suits
        if let isRed {
            let wanted = isRed ? ["h", "d"] : ["s", "c"]
            let filtered = suits.filter { wanted.contains($0.label) }
            if !filtered.isEmpty { candidates = filtered }
        }
        return bestMatch(m, in: candidates)
    }

    /// Similarity against every suit reference, for inspection.
    func suitScores(_ m: InkMask) -> [(String, Double)] {
        guard let grid = normalizeShape(m) else { return [] }
        return suits.map { ($0.label, shapeSimilarity(grid, $0.grid)) }
            .sorted { $0.1 > $1.1 }
    }

    /// Similarity against every rank reference, for inspection.
    func rankScores(_ m: InkMask) -> [(String, Double)] {
        guard let grid = normalizeShape(m) else { return [] }
        return ranks.map { ($0.label, shapeSimilarity(grid, $0.grid)) }
            .sorted { $0.1 > $1.1 }
    }

    private func bestMatch(_ m: InkMask, in templates: [ShapeTemplate]) -> (label: String, score: Double)? {
        guard !templates.isEmpty, let grid = normalizeShape(m) else { return nil }
        var best = ""
        var bestScore = -1.0
        for t in templates {
            let score = shapeSimilarity(grid, t.grid)
            if score > bestScore {
                bestScore = score
                best = t.label
            }
        }
        guard bestScore >= 0 else { return nil }
        return (best, bestScore)
    }

    // MARK: - Building the references

    private func loadSuits(assetsDir: String) -> [ShapeTemplate] {
        // The client ships two sprites per suit, a large colour one and a small
        // one. Either shape serves; the larger is cleaner to normalize.
        let names = ["s": "spade", "h": "heart", "d": "diamond", "c": "clubs"]
        var out: [ShapeTemplate] = []

        guard let entries = try? FileManager.default.contentsOfDirectory(atPath: assetsDir) else {
            return []
        }

        for (label, stem) in names {
            let candidates = entries
                .filter { $0.hasPrefix(stem + ".") && $0.hasSuffix(".webp") }
                .sorted()
            // The smallest adequate sprite. The client ships a ~950px colour
            // sprite and a ~320px one; both normalize to the same grid, and
            // decoding the large one cost most of the startup budget.
            var chosen: [Bool]?
            var chosenPixels = Int.max
            for name in candidates {
                let url = URL(fileURLWithPath: assetsDir).appendingPathComponent(name)
                guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
                      let img = CGImageSourceCreateImageAtIndex(src, 0, nil),
                      let bmp = Bitmap(cgImage: img) else { continue }
                // Sprites are a solid shape on transparency, so any pixel that
                // is not background is part of the pip.
                let mask = spriteMask(bmp)
                if mask.inkCount < chosenPixels, mask.inkCount > 0, let grid = normalizeShape(mask) {
                    chosen = grid
                    chosenPixels = mask.inkCount
                }
            }
            if let grid = chosen {
                out.append(ShapeTemplate(label: label, grid: grid))
            }
        }

        return out
    }

    /// A sprite is a coloured shape on a transparent or white ground.
    private func spriteMask(_ bmp: Bitmap) -> InkMask {
        var mask = [Bool](repeating: false, count: bmp.width * bmp.height)
        var ink = 0
        for y in 0..<bmp.height {
            for x in 0..<bmp.width {
                let o = (y * bmp.width + x) * 4
                let alpha = bmp.pixels[o + 3]
                let (r, g, b) = bmp.rgb(x, y)
                let lum = 0.299 * r + 0.587 * g + 0.114 * b
                if alpha > 40 && lum < 230 {
                    mask[y * bmp.width + x] = true
                    ink += 1
                }
            }
        }
        return InkMask(width: bmp.width, height: bmp.height, mask: mask,
                       inkCount: ink, redCount: 0, blackCount: 0)
    }

    private func loadRanks(assetsDir: String) -> [ShapeTemplate] {
        guard let font = registerFont(assetsDir: assetsDir) else { return [] }

        let labels = ["2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"]
        var out: [ShapeTemplate] = []
        for label in labels {
            guard let img = renderGlyph(label, font: font),
                  let bmp = Bitmap(cgImage: img) else { continue }
            let mask = makeInkMask(bmp)
            guard let grid = normalizeShape(mask) else { continue }
            out.append(ShapeTemplate(label: label, grid: grid))
        }
        return out
    }

    /// Registers the typeface extracted from the client bundle.
    private func registerFont(assetsDir: String) -> CTFont? {
        guard let entries = try? FileManager.default.contentsOfDirectory(atPath: assetsDir) else {
            return nil
        }
        // Card ranks are set heavy; the bold face matches them most closely.
        let preferred = ["FiraSans-700.ttf", "FiraSans-900.ttf", "FiraSans-400.ttf"]
        for name in preferred where entries.contains(name) {
            let url = URL(fileURLWithPath: assetsDir).appendingPathComponent(name)
            var err: Unmanaged<CFError>?
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, &err)
            guard let provider = CGDataProvider(url: url as CFURL),
                  let cgFont = CGFont(provider) else { continue }
            return CTFontCreateWithGraphicsFont(cgFont, 96, nil, nil)
        }
        return nil
    }

    private func renderGlyph(_ text: String, font: CTFont) -> CGImage? {
        let size = 160
        var buf = [UInt8](repeating: 255, count: size * size * 4)
        let space = CGColorSpaceCreateDeviceRGB()
        let info = CGImageAlphaInfo.premultipliedLast.rawValue
        guard let ctx = CGContext(data: &buf, width: size, height: size, bitsPerComponent: 8,
                                  bytesPerRow: size * 4, space: space, bitmapInfo: info) else {
            return nil
        }

        // CoreText attribute keys rather than AppKit's: this file draws with
        // CoreText alone so the agent needs no window-server framework.
        let attrs: [CFString: Any] = [
            kCTFontAttributeName: font,
            kCTForegroundColorAttributeName: CGColor(red: 0, green: 0, blue: 0, alpha: 1),
        ]
        let attributed = CFAttributedStringCreate(nil, text as CFString, attrs as CFDictionary)
        guard let attributed else { return nil }
        let line = CTLineCreateWithAttributedString(attributed)
        let bounds = CTLineGetBoundsWithOptions(line, [])
        ctx.textPosition = CGPoint(x: (CGFloat(size) - bounds.width) / 2 - bounds.minX,
                                   y: (CGFloat(size) - bounds.height) / 2 - bounds.minY)
        CTLineDraw(line, ctx)
        return ctx.makeImage()
    }
}

/// Vertical position of the enclosed loop within each single-loop rank, as a
/// fraction of the glyph height. Measured from references rendered in the
/// client's own typeface.
let rankHoleCentre: [String: Double] = [
    "6": 0.68,
    "9": 0.32,
    "Q": 0.47,
    "A": 0.38,
    "4": 0.45,
    "10": 0.50,
]

/// Where the extracted client assets live. Overridable so the offline harness
/// and the live agent can both find them regardless of working directory.
func defaultTemplateAssetsDir() -> String {
    if let env = ProcessInfo.processInfo.environment["POKER_RTA_ASSETS"], !env.isEmpty {
        return env
    }
    return FileManager.default.currentDirectoryPath + "/bin/assets/coinpoker"
}
