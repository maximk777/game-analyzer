import Cocoa
import ScreenCaptureKit
import WebKit

// The HUD as a floating panel pinned to the table window.
//
// It used to be a Chrome window in app mode, and a Chrome window cannot be told
// to stay above another application's window: it sits in the normal window
// order, so the moment the poker client is clicked the advice disappears behind
// it. That is not a small annoyance -- the HUD is only read while acting, which
// is exactly when the client has focus.
//
// A panel solves both halves at once. NSPanel can sit at a floating level, and
// a non-activating one takes no focus when clicked, so the client keeps the
// keyboard. And because the table window is already located every frame for the
// screen reader, the panel can follow it: the same findTargetWindow the agent
// uses gives the frame to sit beside.
//
// Written in Swift rather than through Go bindings to AppKit for one reason:
// pinning needs the table window's frame, and the code that finds it is here.
//
//   swiftc -parse-as-library pkg/capture/table_vision.swift \
//          pkg/capture/card_templates.swift \
//          pkg/capture/hud_panel.swift -o bin/hud_panel
//
//   bin/hud_panel [--url http://localhost:8080/hud.html] [--width 400] [--side right]

/// The CoinPoker table window. Kept identical to the agent's own finder: a
/// panel pinned to a different window than the one being read would be worse
/// than a panel pinned to nothing.
func findTargetWindow() async -> SCWindow? {
    guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false) else {
        return nil
    }
    return content.windows.first {
        let app = ($0.owningApplication?.applicationName ?? "").lowercased()
        let isCoinPoker = app.contains("coin")
        let isTableSize = $0.frame.width > 500 && $0.frame.height > 350
        let ratio = $0.frame.width / max($0.frame.height, 1)
        return isCoinPoker && isTableSize && ratio >= 1.15 && ratio <= 1.55
    }
}

final class HUDPanel: NSPanel {
    // A borderless panel is not key by default, and without this it can never
    // take a click at all -- the HUD has buttons.
    override var canBecomeKey: Bool { true }
}

final class HUDController: NSObject, NSApplicationDelegate, WKNavigationDelegate {
    private let url: URL
    private let panelWidth: CGFloat
    private let side: String
    private var panel: HUDPanel!
    private var web: WKWebView!
    private var lastTableFrame: CGRect?
    private var pinned = true

    init(url: URL, width: CGFloat, side: String) {
        self.url = url
        self.panelWidth = width
        self.side = side
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        let height = (NSScreen.main?.visibleFrame.height ?? 900) * 0.86
        let rect = NSRect(x: 60, y: 60, width: panelWidth, height: min(height, 820))

        panel = HUDPanel(contentRect: rect,
                         // nonactivatingPanel is the whole point: clicking the
                         // HUD must not pull focus off the table, or every
                         // glance at the advice costs a click to get back.
                         styleMask: [.titled, .closable, .resizable, .fullSizeContentView, .nonactivatingPanel],
                         backing: .buffered,
                         defer: false)
        panel.title = "Poker RTA"
        panel.titlebarAppearsTransparent = true
        panel.titleVisibility = .hidden
        panel.isMovableByWindowBackground = true
        panel.hidesOnDeactivate = false
        panel.becomesKeyOnlyIfNeeded = true
        panel.isFloatingPanel = true
        // Above ordinary windows, including the client's. .floating alone sits
        // below a full-screen application, which is how a table is often
        // played.
        panel.level = .statusBar
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
        panel.backgroundColor = NSColor(calibratedWhite: 0.06, alpha: 1.0)

        let config = WKWebViewConfiguration()
        config.suppressesIncrementalRendering = false
        web = WKWebView(frame: rect, configuration: config)
        web.navigationDelegate = self
        web.setValue(false, forKey: "drawsBackground")
        web.autoresizingMask = [.width, .height]
        panel.contentView = web
        web.load(URLRequest(url: url))

        panel.orderFrontRegardless()

        // The panel follows the table. Once a second is enough: a window that
        // is being dragged is not being played, and a poll is a great deal
        // simpler than watching the accessibility API for move events.
        Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            guard let self, self.pinned else { return }
            Task { await self.followTable() }
        }

        // Reload if the page fails at start-up, which it will if the panel wins
        // the race against the agent's HTTP server.
        Timer.scheduledTimer(withTimeInterval: 3.0, repeats: true) { [weak self] _ in
            guard let self else { return }
            if self.web.url == nil || self.web.title?.isEmpty != false {
                self.web.load(URLRequest(url: self.url))
            }
        }

        print("[HUD] panel up at \(url.absoluteString); pinned to the CoinPoker table window")
    }

    /// Places the panel beside the table window, in screen coordinates.
    ///
    /// ScreenCaptureKit reports a window's frame with the origin at the top
    /// left, and AppKit places windows with the origin at the bottom left of
    /// the primary screen. Mixing the two conventions is what put the HUD off
    /// the bottom of the display the first time.
    @MainActor
    private func followTable() async {
        guard let win = await findTargetWindow() else { return }
        let table = win.frame
        if let last = lastTableFrame, last == table { return }
        lastTableFrame = table

        guard let screen = NSScreen.screens.first else { return }
        let flippedY = screen.frame.height - (table.origin.y + table.height)

        let gap: CGFloat = 8
        var x = table.origin.x + table.width + gap
        if side == "left" || x + panelWidth > screen.frame.maxX {
            // No room outside: sit just inside the table's own edge rather than
            // off the screen.
            x = max(screen.frame.minX + gap, table.origin.x - panelWidth - gap)
            if x < screen.frame.minX + gap {
                x = table.origin.x + table.width - panelWidth - gap
            }
        }

        let height = panel.frame.height
        var y = flippedY + table.height - height
        if y < screen.frame.minY + gap {
            y = screen.frame.minY + gap
        }

        panel.setFrameOrigin(NSPoint(x: x, y: y))
        panel.orderFrontRegardless()
    }
}

@main
struct HUDPanelApp {
    static func main() {
        var urlString = "http://localhost:8080/hud.html"
        var width: CGFloat = 400
        var side = "right"

        let args = Array(CommandLine.arguments.dropFirst())
        var i = 0
        while i < args.count {
            switch args[i] {
            case "--url":
                i += 1
                if i < args.count { urlString = args[i] }
            case "--port":
                i += 1
                if i < args.count { urlString = "http://localhost:\(args[i])/hud.html" }
            case "--width":
                i += 1
                if i < args.count, let w = Double(args[i]) { width = CGFloat(w) }
            case "--side":
                i += 1
                if i < args.count { side = args[i] }
            default:
                break
            }
            i += 1
        }

        guard let url = URL(string: urlString) else {
            FileHandle.standardError.write("bad --url \(urlString)\n".data(using: .utf8)!)
            exit(2)
        }

        let app = NSApplication.shared
        // An accessory application: no Dock icon, no menu bar, and it never
        // steals activation from the client.
        app.setActivationPolicy(.accessory)
        let controller = HUDController(url: url, width: width, side: side)
        app.delegate = controller
        app.run()
    }
}
