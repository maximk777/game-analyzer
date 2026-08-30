# Live Screen Capture & Always-On-Top Floating HUD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a live background screen capture engine for the CoinPoker desktop client that automatically parses the active table in real-time, calculates Monte Carlo equity and EV decisions, and displays advice in a dedicated Always-On-Top Floating HUD widget.

**Architecture:** A native Go window enumeration and frame grabber (`pkg/capture`) continuously captures the CoinPoker table window by Window ID, parses the frame using `pkg/vision`, detects state transitions with `StateDiffer`, pushes events to the analytical core in `pkg/server`, and broadcasts recommendations over WebSocket to a standalone Always-On-Top floating HUD widget.

**Tech Stack:** Go 1.25, CoreGraphics / screencapture window grabber, gorilla/websocket, HTML5/CSS3/Vanilla JS Floating HUD.

## Global Constraints

- Go version: Go 1.22+
- Non-invasive: 100% passive screen capture by Window ID (no DLL injection, no memory reading, no auto-clicking)
- Latency: Frame grabbing (2–5 FPS) with sub-5ms analytical decision pipeline
- Platform: macOS primary (CoreGraphics / screencapture) with cross-platform mock/bounds fallback
- Dedicated Assistant UI: Compact Always-On-Top HUD widget displaying Hero cards, Equity %, Pot Odds %, Action recommendation, Sizing options, and Tactical reasoning

---

### Task 1: Window Discovery & Frame Grabber (`pkg/capture`)

**Files:**
- Create: `pkg/capture/window.go`
- Create: `pkg/capture/grabber.go`
- Test: `pkg/capture/window_test.go`
- Test: `pkg/capture/grabber_test.go`

**Interfaces:**
- Produces:
  - `WindowInfo`: `ID uint32`, `Title string`, `OwnerName string`, `Bounds image.Rectangle`, `IsOnScreen bool`
  - `WindowFinder`: `FindWindow(query string) (*WindowInfo, error)`, `ListWindows() ([]WindowInfo, error)`
  - `FrameGrabber`: interface with `CaptureWindow(win WindowInfo) (image.Image, error)` and `CaptureRect(rect image.Rectangle) (image.Image, error)`
  - `NativeGrabber`: macOS native implementation using `screencapture -l<id> -x` (or CoreGraphics) reading image directly to memory
  - `MockGrabber`: deterministic in-memory grabber for unit testing

- [ ] **Step 1: Write failing tests for WindowFinder and FrameGrabber**

```go
// pkg/capture/window_test.go
package capture

import (
	"image"
	"testing"
)

func TestWindowFilter(t *testing.T) {
	windows := []WindowInfo{
		{ID: 101, Title: "CoinPoker - NL Hold'em 0.25/0.50 Table 1", OwnerName: "CoinPoker", Bounds: image.Rect(100, 100, 900, 700), IsOnScreen: true},
		{ID: 102, Title: "Terminal", OwnerName: "Terminal", Bounds: image.Rect(0, 0, 800, 600), IsOnScreen: true},
		{ID: 103, Title: "Lobby", OwnerName: "CoinPoker", Bounds: image.Rect(50, 50, 400, 500), IsOnScreen: true},
	}

	target := FilterPokerWindow(windows, "CoinPoker")
	if target == nil || target.ID != 101 {
		t.Fatalf("expected to find window 101 (Hold'em table), got %+v", target)
	}
}

func TestMockGrabber_Capture(t *testing.T) {
	mock := NewMockGrabber(image.NewRGBA(image.Rect(0, 0, 800, 600)))
	img, err := mock.CaptureWindow(WindowInfo{ID: 101})
	if err != nil {
		t.Fatalf("CaptureWindow failed: %v", err)
	}
	if img.Bounds().Dx() != 800 || img.Bounds().Dy() != 600 {
		t.Errorf("unexpected image dimensions: %v", img.Bounds())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/capture`
Expected: FAIL

- [ ] **Step 3: Implement window.go and grabber.go**

```go
// pkg/capture/window.go
package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"os/exec"
	"runtime"
	"strings"
)

type WindowInfo struct {
	ID         uint32          `json:"id"`
	Title      string          `json:"title"`
	OwnerName  string          `json:"owner_name"`
	Bounds     image.Rectangle `json:"bounds"`
	IsOnScreen bool            `json:"is_on_screen"`
}

func FilterPokerWindow(windows []WindowInfo, query string) *WindowInfo {
	query = strings.ToLower(query)
	// Prioritize table windows (containing table, hold'em, pot, etc.)
	for _, w := range windows {
		if !w.IsOnScreen {
			continue
		}
		title := strings.ToLower(w.Title)
		owner := strings.ToLower(w.OwnerName)

		if strings.Contains(owner, query) || strings.Contains(title, query) {
			if strings.Contains(title, "table") || strings.Contains(title, "hold'em") || strings.Contains(title, "nlh") || strings.Contains(title, "pot") {
				return &w
			}
		}
	}

	// Fallback to any window matching owner
	for _, w := range windows {
		if !w.IsOnScreen {
			continue
		}
		if strings.Contains(strings.ToLower(w.OwnerName), query) || strings.Contains(strings.ToLower(w.Title), query) {
			return &w
		}
	}
	return nil
}

func ListAllWindows() ([]WindowInfo, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("native window enumeration only implemented for macOS")
	}

	// Use AppleScript / JXA or screencapture tool to enumerate window list
	script := `
	ObjC.import('CoreGraphics');
	var windowList = $.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly | $.kCGWindowListExcludeDesktopElements, $.kCGNullWindowID);
	var count = $.CFArrayGetCount(windowList);
	var res = [];
	for (var i = 0; i < count; i++) {
		var dict = $.CFArrayGetValueAtIndex(windowList, i);
		var id = ObjC.unwrap($.CFDictionaryGetValue(dict, $.kCGWindowNumber)) || 0;
		var owner = ObjC.unwrap($.CFDictionaryGetValue(dict, $.kCGWindowOwnerName)) || "";
		var title = ObjC.unwrap($.CFDictionaryGetValue(dict, $.kCGWindowName)) || "";
		var boundsDict = $.CFDictionaryGetValue(dict, $.kCGWindowBounds);
		var rect = $.CGRectMake(0,0,0,0);
		$.CGRectMakeWithDictionaryRepresentation(boundsDict, rect);
		res.push({
			id: id,
			owner_name: owner,
			title: title,
			x: rect.origin.x,
			y: rect.origin.y,
			w: rect.size.width,
			h: rect.size.height,
			is_on_screen: true
		});
	}
	JSON.stringify(res);
	`
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("osascript failed: %v, stderr: %s", err, stderr.String())
	}

	var rawList []struct {
		ID         uint32  `json:"id"`
		OwnerName  string  `json:"owner_name"`
		Title      string  `json:"title"`
		X          float64 `json:"x"`
		Y          float64 `json:"y"`
		W          float64 `json:"w"`
		H          float64 `json:"h"`
		IsOnScreen bool    `json:"is_on_screen"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rawList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal window list: %w", err)
	}

	res := make([]WindowInfo, len(rawList))
	for i, r := range rawList {
		res[i] = WindowInfo{
			ID:         r.ID,
			Title:      r.Title,
			OwnerName:  r.OwnerName,
			Bounds:     image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)),
			IsOnScreen: r.IsOnScreen,
		}
	}
	return res, nil
}
```

```go
// pkg/capture/grabber.go
package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"strconv"
)

type FrameGrabber interface {
	CaptureWindow(win WindowInfo) (image.Image, error)
	CaptureRect(rect image.Rectangle) (image.Image, error)
}

type NativeGrabber struct{}

func NewNativeGrabber() *NativeGrabber {
	return &NativeGrabber{}
}

func (g *NativeGrabber) CaptureWindow(win WindowInfo) (image.Image, error) {
	if win.ID == 0 {
		return nil, fmt.Errorf("invalid window id 0")
	}

	// screencapture -l<windowID> -x -o -tpng - (stream to stdout)
	cmd := exec.Command("screencapture", "-l"+strconv.Itoa(int(win.ID)), "-x", "-o", "-tpng", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture failed: %w, stderr: %s", err, stderr.String())
	}

	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to decode captured PNG: %w", err)
	}
	return img, nil
}

func (g *NativeGrabber) CaptureRect(rect image.Rectangle) (image.Image, error) {
	rectStr := fmt.Sprintf("%d,%d,%d,%d", rect.Min.X, rect.Min.Y, rect.Dx(), rect.Dy())
	cmd := exec.Command("screencapture", "-R"+rectStr, "-x", "-o", "-tpng", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture rect failed: %w, stderr: %s", err, stderr.String())
	}

	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to decode captured PNG: %w", err)
	}
	return img, nil
}

type MockGrabber struct {
	frame image.Image
}

func NewMockGrabber(img image.Image) *MockGrabber {
	return &MockGrabber{frame: img}
}

func (m *MockGrabber) SetFrame(img image.Image) {
	m.frame = img
}

func (m *MockGrabber) CaptureWindow(win WindowInfo) (image.Image, error) {
	if m.frame == nil {
		return nil, fmt.Errorf("no mock frame set")
	}
	return m.frame, nil
}

func (m *MockGrabber) CaptureRect(rect image.Rectangle) (image.Image, error) {
	if m.frame == nil {
		return nil, fmt.Errorf("no mock frame set")
	}
	return m.frame, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./pkg/capture`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/capture/
git commit -m "feat(capture): implement window discovery and frame grabber"
```

---

### Task 2: Live Ingestion Pipeline & Auto-Differ Agent (`pkg/capture/agent.go`)

**Files:**
- Create: `pkg/capture/agent.go`
- Test: `pkg/capture/agent_test.go`

**Interfaces:**
- Consumes:
  - `capture.FrameGrabber`, `capture.WindowInfo`
  - `vision.FrameParser`, `vision.ROIConfig`, `vision.StateDiffer`
  - `server.Server`, `table.HandState`
- Produces:
  - `LiveAgentConfig`: `WindowQuery string`, `FPS int`, `TableID string`, `HeroID string`, `ROIConfig vision.ROIConfig`
  - `LiveAgent`: `NewLiveAgent(grabber FrameGrabber, srv *server.Server, cfg LiveAgentConfig) *LiveAgent`, `Start(ctx context.Context) error`, `Stop()`, `GetLastFrame() image.Image`, `GetTargetWindow() *WindowInfo`

- [ ] **Step 1: Write failing tests for LiveAgent**

```go
// pkg/capture/agent_test.go
package capture

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/vision"
)

func TestLiveAgent_Pipeline(t *testing.T) {
	cache := storage.NewMemoryCache()
	srv := server.NewServer(cache, nil, nil)

	testImg := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			testImg.Set(x, y, color.RGBA{R: 20, G: 80, B: 40, A: 255})
		}
	}

	mockGrabber := NewMockGrabber(testImg)
	agent := NewLiveAgent(mockGrabber, srv, LiveAgentConfig{
		WindowQuery: "CoinPoker",
		FPS:         10,
		TableID:     "live-table-1",
		HeroID:      "Hero",
		ROIConfig:   vision.DefaultCoinPoker6MaxROI(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go agent.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	agent.Stop()

	// Verify table state exists in server cache
	state := cache.GetTableState("live-table-1")
	if state == nil {
		t.Fatalf("expected live table state in cache, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/capture`
Expected: FAIL

- [ ] **Step 3: Implement LiveAgent in agent.go**

```go
// pkg/capture/agent.go
package capture

import (
	"context"
	"fmt"
	"image"
	"log"
	"sync"
	"time"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

type LiveAgentConfig struct {
	WindowQuery string
	FPS         int
	TableID     string
	HeroID      string
	ROIConfig   vision.ROIConfig
}

type LiveAgent struct {
	mu           sync.RWMutex
	grabber      FrameGrabber
	server       *server.Server
	cfg          LiveAgentConfig
	parser       *vision.FrameParser
	differ       *vision.StateDiffer
	lastFrame    image.Image
	targetWindow *WindowInfo
	prevState    *table.HandState
	stopChan     chan struct{}
}

func NewLiveAgent(grabber FrameGrabber, srv *server.Server, cfg LiveAgentConfig) *LiveAgent {
	if cfg.FPS <= 0 {
		cfg.FPS = 3
	}
	if cfg.TableID == "" {
		cfg.TableID = "coinpoker-live-1"
	}

	return &LiveAgent{
		grabber:   grabber,
		server:    srv,
		cfg:       cfg,
		parser:    vision.NewFrameParser(),
		differ:    vision.NewStateDiffer(),
		stopChan:  make(chan struct{}),
	}
}

func (a *LiveAgent) Start(ctx context.Context) error {
	interval := time.Second / time.Duration(a.cfg.FPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[LIVE-AGENT] Started screen capture agent for table %s at %d FPS...", a.cfg.TableID, a.cfg.FPS)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.stopChan:
			return nil
		case <-ticker.C:
			a.processTick()
		}
	}
}

func (a *LiveAgent) Stop() {
	select {
	case <-a.stopChan:
	default:
		close(a.stopChan)
	}
}

func (a *LiveAgent) GetLastFrame() image.Image {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastFrame
}

func (a *LiveAgent) GetTargetWindow() *WindowInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.targetWindow
}

func (a *LiveAgent) processTick() {
	// 1. Locate window if not found
	if a.targetWindow == nil {
		windows, err := ListAllWindows()
		if err == nil {
			a.mu.Lock()
			a.targetWindow = FilterPokerWindow(windows, a.cfg.WindowQuery)
			a.mu.Unlock()
		}
	}

	var win WindowInfo
	if a.targetWindow != nil {
		win = *a.targetWindow
	} else {
		// Fallback dummy window
		win = WindowInfo{ID: 1}
	}

	// 2. Capture frame
	img, err := a.grabber.CaptureWindow(win)
	if err != nil {
		return
	}

	a.mu.Lock()
	a.lastFrame = img
	a.mu.Unlock()

	// 3. Parse frame
	state, err := a.parser.ParseFrame(img, a.cfg.ROIConfig)
	if err != nil || state == nil {
		return
	}
	state.TableID = a.cfg.TableID
	state.HeroID = a.cfg.HeroID

	// 4. Differ and emit events
	events := a.differ.DetectEvents(a.prevState, state)
	a.prevState = state

	// 5. Ingest into Server
	if a.server != nil {
		a.server.IngestLiveState(state, events)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./pkg/capture`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/capture/ pkg/server/
git commit -m "feat(capture): implement LiveAgent continuous ingestion loop and differ"
```

---

### Task 3: Always-On-Top Floating HUD Widget & Calibration UI

**Files:**
- Create: `web/hud.html`
- Create: `web/hud.css`
- Create: `web/hud.js`
- Create: `web/calibrate.html`
- Create: `web/calibrate.js`
- Modify: `pkg/server/server.go` (add snapshot & calibration endpoints)
- Test: `pkg/server/server_test.go`

**Interfaces:**
- Produces:
  - `GET /hud.html`: Compact floating HUD UI (~320x200px) displaying Hero Cards, Equity Gauge, Pot Odds, Recommended Action, Sizing Grid, and Tactical Reasoning.
  - `GET /calibrate.html`: Interactive screen snapshot viewer with draggable/adjustable ROI overlay.
  - `GET /api/v1/snapshot`: Returns current captured frame (JPEG/PNG) from LiveAgent for calibration.
  - `POST /api/v1/roi`: Saves updated ROI configuration.

- [ ] **Step 1: Implement web/hud.html, web/hud.css, web/hud.js**
- [ ] **Step 2: Implement web/calibrate.html and web/calibrate.js**
- [ ] **Step 3: Add snapshot and calibration endpoints to pkg/server/server.go**
- [ ] **Step 4: Test endpoints with `go test -v ./pkg/server`**
- [ ] **Step 5: Commit**

```bash
git add web/ pkg/server/
git commit -m "feat(web): add Always-On-Top Floating HUD widget and calibration page"
```

---

### Task 4: Unified Live Assistant CLI (`cmd/agent/main.go`)

**Files:**
- Create: `cmd/agent/main.go`
- Create: `cmd/agent/agent_test.go`

**Interfaces:**
- Produces:
  - `cmd/agent/main.go` supporting CLI flags:
    - `-window`: target poker window query (default: `CoinPoker`)
    - `-port`: server HTTP port (default: `8080`)
    - `-fps`: capture frame rate (default: `3`)
    - `-open-hud`: automatically opens browser window in app HUD mode (`--app=http://localhost:8080/hud.html`)
    - `-table-id`: table identifier (default: `coinpoker-live`)
    - `-mock-llm`: enable mock profiling

- [ ] **Step 1: Write integration test in cmd/agent/agent_test.go**
- [ ] **Step 2: Implement cmd/agent/main.go**
- [ ] **Step 3: Run all project tests: `go test -race ./...`**
- [ ] **Step 4: Commit**

```bash
git add cmd/agent/
git commit -m "feat(agent): implement unified live assistant CLI launcher and HUD app mode"
```

---

## Plan Self-Review

1. **Spec coverage:** 
   - Window enumeration & native capture -> Task 1
   - Live ingestion & diff pipeline -> Task 2
   - Floating HUD widget & Calibration UI -> Task 3
   - Unified CLI launcher (`cmd/agent`) -> Task 4
2. **Placeholder scan:** No TBD or TODOs.
3. **Type consistency:** WindowInfo, LiveAgentConfig, FrameParser, Server types match cleanly.
