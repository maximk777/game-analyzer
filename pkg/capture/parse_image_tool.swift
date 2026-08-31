import Foundation
import CoreGraphics
import ImageIO
import Vision

/// Offline harness: runs the exact same table analysis the live agent runs, but
/// against a PNG on disk. This is what makes card recognition debuggable
/// without a live table open -- crops can be dumped and inspected, and expected
/// output can be asserted in CI.
///
///   swiftc -parse-as-library pkg/capture/table_vision.swift \
///          pkg/capture/parse_image_tool.swift -o bin/parse_image
///   bin/parse_image testdata/coinpoker_live_sample.png --dump /tmp/crops
@main
struct ParseImageTool {
    static func main() {
        let args = CommandLine.arguments
        guard args.count >= 2 else {
            FileHandle.standardError.write("usage: parse_image <image.png> [--dump <dir>] [--verbose] [--texts] [--title <window title>] [--amount <text>...]\n".data(using: .utf8)!)
            exit(2)
        }

        let path = args[1]
        var dumpDir: URL?
        var verbose = false
        var title: String? = nil

        var i = 2
        while i < args.count {
            switch args[i] {
            case "--dump":
                guard i + 1 < args.count else {
                    FileHandle.standardError.write("--dump needs a directory\n".data(using: .utf8)!)
                    exit(2)
                }
                let dir = URL(fileURLWithPath: args[i + 1])
                try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                dumpDir = dir
                i += 2
            case "--verbose":
                verbose = true
                i += 1
            case "--texts":
                textSink = { text, box in
                    FileHandle.standardError.write(
                        String(format: "text %-24s x=%.3f y=%.3f w=%.3f h=%.3f\n",
                               (text as NSString).utf8String!, box.midX, box.midY, box.width, box.height)
                            .data(using: .utf8)!)
                }
                i += 1
            case "--amount":
                // Reads one amount the way a frame's text would be read, so a
                // stake or a pot that comes back wrong can be checked without a
                // table in front of you.
                i += 1
                while i < args.count {
                    let raw = args[i]
                    print(String(format: "  %-24s -> %@", (raw as NSString).utf8String!,
                                 String(describing: parseAmount(raw))))
                    i += 1
                }
                return
            case "--title":
                // Live, the window title comes from the system and is where the
                // blinds are read from. Offline there is no window, so it can be
                // supplied -- which is the only way to exercise a stake this
                // repository has no capture of.
                i += 1
                if i < args.count { title = args[i] }
                i += 1
            default:
                i += 1
            }
        }

        let url = URL(fileURLWithPath: path)
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let img = CGImageSourceCreateImageAtIndex(src, 0, nil) else {
            FileHandle.standardError.write("could not decode image: \(path)\n".data(using: .utf8)!)
            exit(1)
        }

        let assetsDir = defaultTemplateAssetsDir()
        let loaded = CardTemplates.shared.load(assetsDir: assetsDir)
        print("image: \(img.width)x\(img.height)  templates: \(loaded ? "loaded" : "absent")")
        if let bmp = Bitmap(cgImage: img), let btn = findDealerButton(bmp: bmp) {
            print(String(format: "dealer button at (%.3f, %.3f) top-left normalized", btn.x, btn.y))
        } else {
            print("dealer button: not found")
        }

        if verbose {
            for (n, slot) in TableGeometry.boardSlots.enumerated() {
                report(readCardSlot(cgImg: img, slot: slot, debugDir: dumpDir, label: "board\(n + 1)"), name: "board\(n + 1)")
            }
            for (n, slot) in TableGeometry.heroSlots.enumerated() {
                report(readCardSlot(cgImg: img, slot: slot, debugDir: dumpDir, label: "hero\(n + 1)"), name: "hero\(n + 1)")
            }
        }

        // Hero card slots are located per frame, so report what each reading
        // actually saw rather than probing fixed coordinates.
        if let frame = Bitmap(cgImage: img, downscale: 4) {
            let board = findBoardRows(bmp: frame).first ?? []
            for (i, region) in findHeroCardRegions(bmp: frame, excluding: board).enumerated() {
                let aspect = region.width / max(region.height, 1)
                print(String(format: "  heroRegion%d %d,%d %dx%d aspect=%.2f",
                             i + 1, Int(region.minX), Int(region.minY),
                             Int(region.width), Int(region.height), aspect))
                for (n, found) in readCardsInRegion(cgImg: img, region: region, label: "heroDebug\(i)-").enumerated() {
                    print("    card\(n + 1) at \(Int(found.rect.minX)),\(Int(found.rect.minY)) = \(found.reading.card ?? "nil") \(found.reading.debug)")
                }
            }
        }

        if ProcessInfo.processInfo.environment["ocrDump"] != nil {
            var items: [(String, CGRect)] = []
            let req = VNRecognizeTextRequest { r, _ in
                for o in (r.results as? [VNRecognizedTextObservation] ?? []) {
                    if let t = o.topCandidates(1).first?.string { items.append((t, o.boundingBox)) }
                }
            }
            req.recognitionLevel = .accurate
            req.usesLanguageCorrection = false
            try? VNImageRequestHandler(cgImage: img, options: [:]).perform([req])
            print("--- OCR (координаты Vision, низ-лево) ---")
            for (t, b) in items.sorted(by: { $0.1.midY > $1.1.midY }) {
                print(String(format: "  %-22s x=%.3f y=%.3f w=%.3f h=%.3f", (t as NSString).utf8String!, b.midX, b.midY, b.width, b.height))
            }
        }

        let state = analyzeTable(cgImg: img, title: title ?? url.lastPathComponent,
                                 debugDir: verbose ? nil : dumpDir)

        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? enc.encode(state), let s = String(data: data, encoding: .utf8) {
            print(s)
        }
    }

    static func report(_ r: CardReading, name: String) {
        let prof = r.profile.map { String(format: "%.2f", $0) }.joined(separator: " ")
        let dist = r.suitDistance.isFinite ? String(format: "%.3f", r.suitDistance) : "-"
        print("""
        \(name): card=\(r.card ?? "nil") rank=\(r.rank ?? "nil") suit=\(r.suit ?? "nil") \
        white=\(String(format: "%.2f", r.whiteRatio)) dist=\(dist)
          profile: [\(prof)]
          \(r.debug)
        """)
    }
}
