import Cocoa
import ScreenCaptureKit

@main
struct ListAppWindows {
    static func main() async {
        _ = NSApplication.shared
        guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
            return
        }
        for w in content.windows {
            let app = w.owningApplication?.applicationName ?? "nil"
            let title = w.title ?? "nil"
            if app.lowercased().contains("coin") || title.lowercased().contains("nlh") || app.lowercased().contains("poker") {
                print("App: '\(app)' | Title: '\(title)' | ID: \(w.windowID) | Size: \(w.frame.width)x\(w.frame.height)")
            }
        }
    }
}
