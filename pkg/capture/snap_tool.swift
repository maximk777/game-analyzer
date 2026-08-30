import Cocoa
import ScreenCaptureKit

// Captures the CoinPoker table window to a PNG, and nothing else.
//
// This existed once as a throwaway in a scratch directory, was lost between
// sessions, and had to be written again -- which cost more than keeping it
// does. Every card bug so far has been diagnosed from a frame rather than from
// reasoning about the frame, so the tool that produces frames belongs in the
// repository beside the ones that read them.
//
//   make && bin/snap [out.png]
//
// Needs Screen Recording permission for the terminal running it, the same as
// the live agent.
//
//   swiftc -parse-as-library pkg/capture/table_vision.swift \
//          pkg/capture/card_templates.swift \
//          pkg/capture/snap_tool.swift -o bin/snap

/// The CoinPoker table window, told apart from the lobby by its proportions:
/// the lobby is taller and narrower than a table. Kept identical to the live
/// agent's own finder on purpose -- a snapshot that came from a different
/// window than the agent reads would be worse than no snapshot.
func findTargetWindow() async -> SCWindow? {
    guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
        return nil
    }

    let coinWins = content.windows.filter {
        let app = ($0.owningApplication?.applicationName ?? "").lowercased()
        let isCoinPoker = app.contains("coin")
        let isTableSize = $0.frame.width > 500 && $0.frame.height > 350
        let ratio = $0.frame.width / max($0.frame.height, 1)
        return isCoinPoker && isTableSize && ratio >= 1.15 && ratio <= 1.55
    }

    return coinWins.first
}

@main
struct SnapTool {
    static func main() async {
        _ = NSApplication.shared
        setbuf(stdout, nil)

        let args = CommandLine.arguments.dropFirst()
        let outPath = args.first ?? defaultName()

        guard let win = await findTargetWindow() else {
            FileHandle.standardError.write("""
            no CoinPoker table window found.

            The window has to be open and a table, not the lobby. If it is, the
            likely cause is Screen Recording permission: System Settings ->
            Privacy & Security -> Screen Recording, for the terminal running
            this.

            """.data(using: .utf8)!)
            exit(1)
        }

        let filter = SCContentFilter(desktopIndependentWindow: win)
        let config = SCStreamConfiguration()
        // Captured at 2x, matching the live agent exactly. A snapshot taken at
        // a different scale would not reproduce what the agent saw, which is
        // the only reason to take one.
        config.width = Int(win.frame.width * 2)
        config.height = Int(win.frame.height * 2)
        config.showsCursor = false

        do {
            let img = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
            let url = URL(fileURLWithPath: outPath)
            writePNG(img, to: url)
            print("\(img.width)x\(img.height) -> \(url.path)")
            print("title: \(win.title ?? "(none)")")
            print("next:  bin/parse_image \(url.path)")
        } catch {
            FileHandle.standardError.write("capture failed: \(error)\n".data(using: .utf8)!)
            exit(1)
        }
    }

    /// Snapshots are taken in bursts while something is going wrong, so the
    /// default name carries the time rather than overwriting the last one.
    static func defaultName() -> String {
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyyMMdd-HHmmss"
        return "snap-\(fmt.string(from: Date())).png"
    }
}
