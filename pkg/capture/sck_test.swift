import Cocoa
import ScreenCaptureKit
import Vision

@main
struct SCKTest {
    static func main() async {
        _ = NSApplication.shared
        do {
            let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
            for w in content.windows {
                let owner = w.owningApplication?.applicationName ?? ""
                let title = w.title ?? ""
                if (owner.lowercased().contains("coin") || title.lowercased().contains("nlh") || title.lowercased().contains("poker")) && w.frame.width > 400 {
                    print("Found ScreenCaptureKit Window: \(w.windowID) | \(owner) | '\(title)' (\(w.frame.width)x\(w.frame.height))")
                    let filter = SCContentFilter(desktopIndependentWindow: w)
                    let config = SCStreamConfiguration()
                    config.width = Int(w.frame.width * 2)
                    config.height = Int(w.frame.height * 2)
                    config.showsCursor = false
                    
                    let img = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
                    print("  Successfully captured frame: \(img.width)x\(img.height)")
                    
                    let req = VNRecognizeTextRequest { req, _ in
                        for o in (req.results as? [VNRecognizedTextObservation] ?? []) {
                            if let t = o.topCandidates(1).first?.string {
                                print("    [OCR] \(t)")
                            }
                        }
                    }
                    req.recognitionLevel = .accurate
                    req.usesLanguageCorrection = false
                    let h = VNImageRequestHandler(cgImage: img, options: [:])
                    try? h.perform([req])
                }
            }
        } catch {
            print("Error: \(error)")
        }
    }
}
