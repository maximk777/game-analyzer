import Cocoa
import ScreenCaptureKit

@main
struct TargetTest {
    static func main() async {
        _ = NSApplication.shared
        guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
            print("Failed to get content")
            return
        }

        print("Total windows found: \(content.windows.count)")
        for w in content.windows {
            let app = (w.owningApplication?.applicationName ?? "").lowercased()
            let title = (w.title ?? "").lowercased()
            if app.contains("coin") || title.contains("nlh") || title.contains("poker") {
                let ratio = w.frame.width / max(w.frame.height, 1)
                print("Candidate: App='\(app)' | Title='\(title)' | Size=\(w.frame.width)x\(w.frame.height) | Ratio=\(ratio)")
            }
        }
    }
}
