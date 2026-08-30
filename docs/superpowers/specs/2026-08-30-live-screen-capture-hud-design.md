# Live Screen Capture & Always-On-Top Floating HUD Assistant Design

**Date:** 2026-08-30  
**Status:** Approved  
**Language/Stack:** Go (Golang) 1.25, CoreGraphics / screencapture Window Grabber, HTML5/CSS/Vanilla JS Always-on-Top Floating HUD  

---

## 1. Executive Summary

This specification defines the real-time game ingestion and assistant interface subsystem that bridges the live **CoinPoker** client with our Go analytical engine. The user plays manually; the assistant operates purely in non-invasive **RTA (Real-Time Assistance) mode**.

The system continuously captures the CoinPoker game window in the background (using native window enumeration and capture by Window ID), parses table state and cards via `pkg/vision`, triggers sub-5ms Monte Carlo equity and EV calculations, and streams real-time recommendations, bet sizings, action history, and opponent profiles directly into a dedicated **Always-On-Top Floating HUD widget**.

---

## 2. System Architecture & Data Flow

```
┌──────────────────────────────────────────────────────────┐
│                   CoinPoker Table Window                 │
│                (macOS / Windows / Emulated)              │
└────────────────────────────┬─────────────────────────────┘
                             │
                             │ WindowID Target Capture (2–5 FPS)
                             ▼
┌──────────────────────────────────────────────────────────┐
│                Module: pkg/capture                       │
│  - Window Enumerator (CoreGraphics / screencapture)      │
│  - Frame Grabber (Background Ticker Loop)                │
└────────────────────────────┬─────────────────────────────┘
                             │
                             │ Raw image.Image
                             ▼
┌──────────────────────────────────────────────────────────┐
│                Module: pkg/vision                        │
│  - ROI Normalization (CoinPoker 6-max layout)            │
│  - Card Recognition (Template / Perceptual Matcher)      │
│  - Segment OCR (Pot, Chip Stacks, Bets)                  │
│  - State Differ (Emits HandStart, Action, HeroTurn)      │
└────────────────────────────┬─────────────────────────────┘
                             │
                             │ Structured Game Events & State
                             ▼
┌──────────────────────────────────────────────────────────┐
│             Go Real-Time Analysis Core                   │
│  - Monte Carlo Equity Simulator (1.10 ms / 10k iters)    │
│  - EV Decision Advisor (1.15 μs)                         │
│  - SQLite Persistence & Async LLM Opponent Profiler      │
└────────────────────────────┬─────────────────────────────┘
                             │
                             │ WebSocket Stream (/ws/tables/live)
                             ▼
┌──────────────────────────────────────────────────────────┐
│          Always-On-Top Floating HUD Widget               │
│  - Hero Hand & Made Combination                          │
│  - Real-time Equity % & Pot Odds % Gauges                │
│  - Primary Recommended Action (RAISE / CALL / FOLD)      │
│  - Sizing Buttons (2.5x, 33%, 66%, Pot, All-in)          │
│  - Tactical Reasoning & Opponent Tendency Badges         │
│  - Action History Log & Calibration Tool (/calibrate)    │
└──────────────────────────────────────────────────────────┘
```

---

## 3. Subsystem Breakdown

### 3.1 Window Discovery & Capture Engine (`pkg/capture`)
* **Window Discovery:**
  * Enumerate active windows via `CGWindowListCopyWindowInfo` (macOS) or process title matching.
  * Filters for windows matching owner names (`CoinPoker`, `poker`, `Wine`, `CrossOver`, `Parallels`) or titles containing `Hold'em`, `Table`, `CoinPoker`.
  * Returns `WindowInfo{ID: uint32, Title: string, Owner: string, Bounds: Rect}`.
* **Target Window Frame Grabber:**
  * Captures only the targeted window via `screencapture -l<windowID> -x` / `CGWindowListCreateImage` into an in-memory buffer without writing to disk.
  * Ticker loop runs at 2–5 FPS (configurable, default 3 FPS / 333ms tick).
  * Safe fallback: if window ID is not found, captures a user-configured desktop bounding box.

### 3.2 Vision & State Differ Pipeline (`pkg/capture/agent.go`)
* Feeds each captured frame to `vision.FrameParser.ParseFrame(img, roiConfig)`.
* `vision.StateDiffer` detects:
  * Hand start / Hero hole cards revealed;
  * Community cards dealt (Flop, Turn, River);
  * Opponent action (Fold, Call, Bet, Raise, All-in) and bet amount updates;
  * Hero turn detected (action buttons / timer active);
  * Showdown / Hand completion.
* Automatically posts state changes to `pkg/server.Server` via internal event channel.

### 3.3 Always-On-Top Floating HUD Widget (`web/hud.html`, `web/hud.js`, `web/hud.css`)
* **Widget Form Factor:**
  * Compact floating widget (~320 × 200 px), movable, styled with dark poker glassmorphism.
  * Can be launched via lightweight browser window (`--app=http://localhost:8080/hud.html` with always-on-top flags) or embedded in a compact window.
* **Widget Display Components:**
  1. **Connection & Window Status:** `● Live · CoinPoker Table 1` (green) or `○ Searching for window...` (yellow).
  2. **Hero Hand Box:** Hero cards with suit colors + current hand category (e.g., `A♠ K♥ · Overcards` or `A♠ K♥ · Top Pair, Top Kicker`).
  3. **Action Recommendation Badge:** Large, high-visibility badge for the optimal action: `RAISE 0.75`, `CALL 0.50`, `CHECK`, `FOLD`.
  4. **Metrics Bar:** Side-by-side gauges for `Equity` (e.g. `68.4%`) and `Pot Odds` (e.g. `22.0%`).
  5. **Sizing Matrix:** Sizing buttons (`Min`, `2.5x`, `33% Pot`, `66% Pot`, `Pot`, `All-in`) showing exact chip amounts.
  6. **Tactical Reason:** 1-sentence explanation of the EV decision.
  7. **Collapsible History & Opponent Drawer:** Shows live hand actions and opponent archetype tags (TAG, LAG, Nit, Fish/Whale) + bluff frequency.

### 3.4 Interactive Calibration UI (`web/calibrate.html`)
* Serves `/calibrate` endpoint:
  * Shows live camera/screen snapshot from the capture engine.
  * Draws interactive ROI boxes (Hero Cards, Board Cards, Pot, Seats 0–5).
  * Allows saving adjusted ROI offsets to `config/roi_coinpoker.json`.

### 3.5 Unified CLI Launcher (`cmd/agent/main.go`)
* Single command startup:
  ```bash
  go run cmd/agent/main.go -window "CoinPoker" -port 8080 -open-hud
  ```
  1. Starts the real-time analytical core & WebSocket server;
  2. Spawns the background window grabber targeting the CoinPoker window;
  3. Launches the Always-on-Top Floating HUD window.

---

## 4. Testing & Verification

1. **Window Enumerator Tests (`pkg/capture/window_test.go`):** Test window parsing, filtering, and mock capture routines.
2. **Synthetic Screen Grabber Integration Test (`pkg/capture/grabber_test.go`):** Test continuous frame streaming into `vision.FrameParser` with simulated state transitions.
3. **HUD WebSocket & REST Verification:** Verify real-time updates and low-latency rendering in the HUD widget.
4. **End-to-End Test (`go test -race ./...`):** Full automated regression run across all packages.
