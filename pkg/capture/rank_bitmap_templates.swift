import Foundation
import CoreGraphics
import ImageIO

// Rank recognition against bitmaps rendered by the client itself.
//
// Until now the references were rendered here, from the client's font, and
// compared as binary masks stretched onto a 28x32 grid. That is one remove from
// what is actually on screen, and the removes add up: the client rasterises at
// its own size with its own hinting and anti-aliasing, and a stretched binary
// mask throws away the very edges where a four differs from an ace.
//
// This path compares against the glyphs as the client draws them. Three
// differences from the old one, and each is a correction rather than a taste:
//
//   * the observed glyph is letterboxed, not stretched. Both sides are now the
//     same rendering, so their proportions agree and distorting one of them can
//     only lose information. (Letterboxing against *font* references was tried
//     and was worse, for the same reason in reverse.)
//
//   * scoring is normalised cross-correlation rather than overlap of two binary
//     masks. It subtracts the mean and divides by the spread, so a glyph that
//     came out heavier or lighter than the reference still matches; overlap
//     counts every anti-aliased pixel that fell the other side of a threshold
//     as a disagreement.
//
//   * the number of enclosed loops is a penalty, not a filter. This is the one
//     that was actively breaking cards. Filtering hard means a glyph whose loop
//     was eaten -- by anti-aliasing, by a threshold, by the felt rule trimming a
//     green club -- has the right answer removed from the candidates entirely.
//     Live, a four came back as an ace: both carry one loop, the four's had
//     been gnawed away, and the four was no longer allowed to win. As a penalty
//     it costs a mismatch 0.12 and the shape still decides.

/// A 64x64 reference glyph, values 0...1.
struct BitmapTemplate {
    let label: String
    let pixels: [Float]
    let mean: Float
    let norm: Float
    let holes: Int
}

enum RankBitmaps {
    static let side = 64
    /// Inner glyph box before padding, matching how the references were built:
    /// letterboxed onto a square, resized to 48, then given an 8-pixel margin.
    static let inner = 48
    static let pad = 8

    /// Cost of disagreeing with a reference about how many enclosed loops the
    /// glyph has. Enough to separate an ace from a king, which correlate almost
    /// identically, and not enough to overrule a clear shape.
    static let holePenalty: Float = 0.12

    /// Minimum correlation to accept a reading. Below it the crop is not a rank
    /// and saying so is the answer.
    static let floor: Float = 0.55
}

/// Loads the 13 references from a directory of 64x64 greyscale PNGs.
func loadRankBitmaps(from dir: String) -> [BitmapTemplate] {
    let ranks = ["2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"]
    var out: [BitmapTemplate] = []
    for r in ranks {
        let path = (dir as NSString).appendingPathComponent("\(r).png")
        guard FileManager.default.fileExists(atPath: path),
              let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
              let img = CGImageSourceCreateImageAtIndex(src, 0, nil),
              let grid = grayGrid(img, side: RankBitmaps.side) else { continue }
        out.append(makeTemplate(label: r, pixels: grid))
    }
    return out
}

/// Draws an image into a square grid of grey values, 0...1.
func grayGrid(_ img: CGImage, side: Int) -> [Float]? {
    var buf = [UInt8](repeating: 0, count: side * side)
    guard let ctx = CGContext(data: &buf, width: side, height: side,
                              bitsPerComponent: 8, bytesPerRow: side,
                              space: CGColorSpaceCreateDeviceGray(),
                              bitmapInfo: CGImageAlphaInfo.none.rawValue) else { return nil }
    ctx.interpolationQuality = .high
    ctx.draw(img, in: CGRect(x: 0, y: 0, width: side, height: side))
    return buf.map { Float($0) / 255.0 }
}

func makeTemplate(label: String, pixels: [Float]) -> BitmapTemplate {
    let n = Float(pixels.count)
    let mean = pixels.reduce(0, +) / n
    var ss: Float = 0
    for p in pixels { ss += (p - mean) * (p - mean) }
    return BitmapTemplate(label: label, pixels: pixels, mean: mean,
                          norm: max(ss.squareRoot(), 1e-6),
                          holes: countHolesInGrid(pixels, side: RankBitmaps.side))
}

/// Normalised cross-correlation of two equally sized grids, -1...1.
///
/// The mean is subtracted and the spread divided out, which is what makes this
/// survive a glyph that came out heavier or lighter than its reference. A plain
/// overlap of binary masks does not: every anti-aliased pixel that fell the
/// other side of a threshold counts against the match.
func correlate(_ a: [Float], _ aMean: Float, _ aNorm: Float, _ t: BitmapTemplate) -> Float {
    guard a.count == t.pixels.count else { return -1 }
    var dot: Float = 0
    for i in 0..<a.count {
        dot += (a[i] - aMean) * (t.pixels[i] - t.mean)
    }
    return dot / (aNorm * t.norm)
}

/// Enclosed loops in a grid: background regions that cannot reach the border.
func countHolesInGrid(_ pixels: [Float], side: Int) -> Int {
    var background = [Bool](repeating: false, count: pixels.count)
    for i in 0..<pixels.count { background[i] = pixels[i] < 0.5 }

    var seen = [Bool](repeating: false, count: pixels.count)
    var stack: [Int] = []
    // Everything reachable from the border is outside.
    for i in 0..<side {
        for idx in [i, (side - 1) * side + i, i * side, i * side + side - 1] where background[idx] && !seen[idx] {
            seen[idx] = true
            stack.append(idx)
        }
    }
    while let p = stack.popLast() {
        let x = p % side, y = p / side
        for (dx, dy) in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
            let nx = x + dx, ny = y + dy
            guard nx >= 0, ny >= 0, nx < side, ny < side else { continue }
            let q = ny * side + nx
            if background[q] && !seen[q] {
                seen[q] = true
                stack.append(q)
            }
        }
    }

    // What is left is enclosed. Specks are not loops.
    var holes = 0
    for start in 0..<pixels.count where background[start] && !seen[start] {
        var size = 0
        seen[start] = true
        stack.append(start)
        while let p = stack.popLast() {
            size += 1
            let x = p % side, y = p / side
            for (dx, dy) in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
                let nx = x + dx, ny = y + dy
                guard nx >= 0, ny >= 0, nx < side, ny < side else { continue }
                let q = ny * side + nx
                if background[q] && !seen[q] {
                    seen[q] = true
                    stack.append(q)
                }
            }
        }
        if size >= 8 { holes += 1 }
    }
    return holes
}

/// Letterboxes an observed glyph onto the reference grid.
///
/// The glyph's bounding box is centred on a square of its longer side, so the
/// proportions survive, then resized and given the same margin the references
/// carry. Stretching instead -- which is what the font-rendered path does --
/// distorts exactly the strokes that separate look-alikes.
func letterboxGlyph(_ m: InkMask) -> [Float]? {
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
    let square = max(gw, gh)
    let offX = (square - gw) / 2
    let offY = (square - gh) / 2

    let inner = RankBitmaps.inner
    let side = RankBitmaps.side
    let pad = RankBitmaps.pad

    var out = [Float](repeating: 0, count: side * side)
    for ty in 0..<inner {
        // Area-averaged rather than sampled: at these sizes a nearest-neighbour
        // shrink drops whole strokes of a four.
        let sy0 = ty * square / inner
        let sy1 = max(sy0 + 1, (ty + 1) * square / inner)
        for tx in 0..<inner {
            let sx0 = tx * square / inner
            let sx1 = max(sx0 + 1, (tx + 1) * square / inner)

            var hits = 0, total = 0
            for sy in sy0..<sy1 {
                for sx in sx0..<sx1 {
                    total += 1
                    let gx = sx - offX, gy = sy - offY
                    guard gx >= 0, gy >= 0, gx < gw, gy < gh else { continue }
                    if m.mask[(minY + gy) * m.width + (minX + gx)] { hits += 1 }
                }
            }
            if total > 0 {
                out[(ty + pad) * side + (tx + pad)] = Float(hits) / Float(total)
            }
        }
    }
    return out
}

/// Best rank for an observed glyph, with the raw correlation behind it.
func matchRankBitmap(_ m: InkMask, templates: [BitmapTemplate]) -> (label: String, score: Float)? {
    guard !templates.isEmpty, let grid = letterboxGlyph(m) else { return nil }

    let n = Float(grid.count)
    let mean = grid.reduce(0, +) / n
    var ss: Float = 0
    for p in grid { ss += (p - mean) * (p - mean) }
    let norm = max(ss.squareRoot(), 1e-6)
    let holes = countHolesInGrid(grid, side: RankBitmaps.side)

    var bestLabel = ""
    var bestScore: Float = -2
    var bestRaw: Float = -2
    for t in templates {
        let raw = correlate(grid, mean, norm, t)
        let score = raw - RankBitmaps.holePenalty * Float(abs(holes - t.holes))
        if score > bestScore {
            bestScore = score
            bestRaw = raw
            bestLabel = t.label
        }
    }
    guard !bestLabel.isEmpty else { return nil }
    return (bestLabel, bestRaw)
}
