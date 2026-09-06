// overlay is the HUD's window: a floating panel that shows the assistant only
// while CoinPoker is the app in front, and gets out of the way the moment you
// switch to anything else.
//
// The HUD is a web page served by the agent; a browser tab cannot float above
// one app and hide behind another, so this hosts the page in a native panel and
// ties its visibility to the frontmost application. Two window traits do the
// work: the panel is non-activating (showing it never steals focus from the
// table) and it floats above normal windows; a workspace observer flips it on
// when CoinPoker comes forward and off when it leaves.
//
//	swiftc -O tools/overlay/overlay.swift -o bin/overlay
//	bin/overlay                       # defaults: localhost:8080/hud.html, "CoinPoker"
//	bin/overlay --url http://localhost:8080/hud.html --match CoinPoker
//
// It needs the agent (cmd/sfsagent) running to serve the page.

import AppKit
import WebKit

// Config is the two things worth changing: where the HUD is served, and which
// app the panel should ride with.
struct Config {
    var url = "http://localhost:8080/hud.html"
    var match = "CoinPoker" // matched as a case-insensitive substring of the app name
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
        position()
    }

    // position parks the panel at the top-right of the main screen, inside the
    // visible frame (below the menu bar). Fixed placement is enough to keep it
    // off the table; riding the CoinPoker window's exact frame would need the
    // accessibility permission and is a later refinement.
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
    // already in front.
    func rideWith(_ app: NSRunningApplication?) {
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

// React to every application activation: this is what makes the panel follow
// CoinPoker and leave when you go to the terminal.
let ws = NSWorkspace.shared
ws.notificationCenter.addObserver(
    forName: NSWorkspace.didActivateApplicationNotification,
    object: nil, queue: .main
) { note in
    let app = note.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
    controller.rideWith(app)
}

// Set the initial state from whatever is in front right now.
controller.rideWith(ws.frontmostApplication)

appKit.run()
