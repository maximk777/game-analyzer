// overlay is the HUD's window: a floating panel that hosts the page the agent
// serves, on its own and independent of the game window.
//
// A browser tab cannot float above other apps, so the page is hosted in a native
// panel. The panel is non-activating (showing it or clicking it never steals
// focus from the table) and floats above normal windows. It is not tied to the
// game: it is always visible, stays where you put it -- the position is
// remembered between launches -- and works on a second monitor or with the
// table in any state.
//
// Pass --match to get the old behaviour back: the panel then shows only while
// an app whose name contains the string is in front, and hides when you switch
// to anything else.
//
//	swiftc -O tools/overlay/overlay.swift -o bin/overlay
//	bin/overlay                       # localhost:8080/hud.html, always visible
//	bin/overlay --url http://localhost:8080/hud.html
//	bin/overlay --match CoinPoker     # opt in: show only while CoinPoker is in front
//	bin/overlay --reset-position      # forget the saved position
//
// It needs the agent (cmd/sfsagent) running to serve the page.

import AppKit
import WebKit

// Config is what is worth changing: where the HUD is served, and, optionally,
// an app the panel should ride with.
struct Config {
    var url = "http://localhost:8080/hud.html"
    var match = "" // empty: always visible. Else a case-insensitive substring of an app name
    var resetPosition = false
    var width = 400.0
    var height = 900.0
    var margin = 24.0 // gap from the screen's top-right corner
}

func parseArgs() -> Config {
    var cfg = Config()
    var it = CommandLine.arguments.dropFirst().makeIterator()
    while let a = it.next() {
        switch a {
        case "--url": if let v = it.next() { cfg.url = v }
        case "--match": if let v = it.next() { cfg.match = v }
        case "--reset-position": cfg.resetPosition = true
        case "--width": if let v = it.next(), let n = Double(v) { cfg.width = n }
        case "--height": if let v = it.next(), let n = Double(v) { cfg.height = n }
        default: break
        }
    }
    return cfg
}

final class OverlayController: NSObject {
    let cfg: Config
    let panel: NSPanel
    let web: WKWebView

    init(cfg: Config) {
        self.cfg = cfg

        let frame = NSRect(x: 0, y: 0, width: cfg.width, height: cfg.height)

        // A non-activating panel: it can show and take clicks without becoming
        // the active application, so the poker table keeps the keyboard and the
        // dealer never waits on a misplaced focus.
        panel = NSPanel(
            contentRect: frame,
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false)
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isMovableByWindowBackground = true
        panel.backgroundColor = .clear
        panel.hasShadow = true
        // Ride along across Spaces and over a fullscreen table, but never show
        // in Mission Control's window list or Exposé as a separate thing.
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary, .ignoresCycle]

        web = WKWebView(frame: frame)
        web.autoresizingMask = [.width, .height]
        web.setValue(false, forKey: "drawsBackground") // let the page's own dark ground show
        panel.contentView = web

        super.init()

        if let url = URL(string: cfg.url) {
            web.load(URLRequest(url: url))
        }

        // Where the user put it last time, if anywhere sensible. The panel is
        // dragged by its background, and AppKit saves the frame under this name
        // every time it moves, so the position survives a restart.
        let name = "PokerAnalyzerOverlay"
        if cfg.resetPosition {
            NSWindow.removeFrame(usingName: name)
        }
        // Default spot first: the first launch has nothing to restore, and
        // AppKit records the frame at the moment the name is set, so it has to be
        // the placed one and not the origin the panel was created at.
        position()
        if panel.setFrameAutosaveName(name), !isOnScreen(panel.frame) {
            position()
        }
    }

    // isOnScreen reports whether enough of the frame is visible to grab. A saved
    // position on a monitor that has since been unplugged would otherwise leave
    // the panel running somewhere nobody can reach it.
    func isOnScreen(_ frame: NSRect) -> Bool {
        for screen in NSScreen.screens {
            let hit = screen.visibleFrame.intersection(frame)
            if hit.width >= 100 && hit.height >= 100 { return true }
        }
        return false
    }

    // position parks the panel at the top-right of the main screen, inside the
    // visible frame (below the menu bar). It is only the first-run default; after
    // that the panel stays wherever it was dragged.
    func position() {
        guard let screen = NSScreen.main else { return }
        let vf = screen.visibleFrame
        let x = vf.maxX - cfg.width - cfg.margin
        let y = vf.maxY - cfg.height - cfg.margin
        panel.setFrame(NSRect(x: x, y: y, width: cfg.width, height: cfg.height), display: true)
    }

    // show / hide without stealing focus. orderFrontRegardless brings the panel
    // up while another app stays active; orderOut simply removes it.
    func show() { panel.orderFrontRegardless() }
    func hide() { panel.orderOut(nil) }

    // rideWith shows the panel only while the named app is frontmost. It is
    // called on every application switch, and once at launch for the app that is
    // already in front. Without --match there is nothing to ride with and the
    // panel is simply always shown.
    func rideWith(_ app: NSRunningApplication?) {
        if cfg.match.isEmpty {
            show()
            return
        }
        let name = app?.localizedName ?? ""
        let mine = app?.processIdentifier == ProcessInfo.processInfo.processIdentifier
        if name.range(of: cfg.match, options: .caseInsensitive) != nil || mine {
            show()
        } else {
            hide()
        }
    }
}

let cfg = parseArgs()
let appKit = NSApplication.shared
// Accessory: no Dock icon, no menu bar; the overlay is a companion, not an app
// the user switches to.
appKit.setActivationPolicy(.accessory)

let controller = OverlayController(cfg: cfg)

// Only with --match: react to every application activation, which makes the
// panel follow that app and leave when you go to the terminal.
let ws = NSWorkspace.shared
if !cfg.match.isEmpty {
    ws.notificationCenter.addObserver(
        forName: NSWorkspace.didActivateApplicationNotification,
        object: nil, queue: .main
    ) { note in
        let app = note.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
        controller.rideWith(app)
    }
}

// Set the initial state from whatever is in front right now.
controller.rideWith(ws.frontmostApplication)

appKit.run()
