package capture

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

// LiveAgentConfig specifies configuration options for the real-time capture agent.
type LiveAgentConfig struct {
	WindowQuery string           `json:"window_query"`
	FPS         int              `json:"fps"`
	TableID     string           `json:"table_id"`
	HeroID      string           `json:"hero_id"`
	ROIConfig   vision.ROIConfig `json:"roi_config"`
}

// LiveAgent orchestrates continuous screen capture, vision parsing, event diffing, and real-time ingestion.
type LiveAgent struct {
	grabber FrameGrabber
	srv     *server.Server
	parser  *vision.FrameParser
	differ  *vision.StateDiffer

	cfg          LiveAgentConfig
	targetWindow *WindowInfo
	lastFrame    image.Image
	lastState    *table.HandState

	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	mu      sync.RWMutex
}

// NewLiveAgent creates and initializes a new LiveAgent with defaults.
func NewLiveAgent(grabber FrameGrabber, srv *server.Server, cfg LiveAgentConfig) *LiveAgent {
	if cfg.FPS <= 0 {
		cfg.FPS = 3
	}
	if cfg.TableID == "" {
		cfg.TableID = "table-1"
	}
	if cfg.HeroID == "" {
		cfg.HeroID = "player-0"
	}
	if len(cfg.ROIConfig.Seats) == 0 {
		cfg.ROIConfig = vision.DefaultCoinPoker6MaxROI()
	}

	return &LiveAgent{
		grabber: grabber,
		srv:     srv,
		parser:  vision.NewFrameParser(nil, nil),
		differ:  vision.NewStateDiffer(),
		cfg:     cfg,
	}
}

// Start initiates the continuous background frame ingestion loop.
func (a *LiveAgent) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("live agent is already running")
	}

	agentCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.done = make(chan struct{})
	a.running = true

	// Attempt initial window lookup if native grabber and not already set
	if a.targetWindow == nil && a.cfg.WindowQuery != "" {
		if _, isNative := a.grabber.(*NativeGrabber); isNative {
			if windows, err := ListAllWindows(); err == nil {
				if win := FilterPokerWindow(windows, a.cfg.WindowQuery); win != nil {
					target := *win
					a.targetWindow = &target
				}
			}
		}
	}
	a.mu.Unlock()

	go a.loop(agentCtx)
	return nil
}

func (a *LiveAgent) loop(ctx context.Context) {
	defer close(a.done)

	a.mu.RLock()
	fps := a.cfg.FPS
	a.mu.RUnlock()

	if fps <= 0 {
		fps = 3
	}
	interval := time.Duration(1000/fps) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Perform initial immediate frame capture
	_ = a.Step(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = a.Step(ctx)
		}
	}
}

// Stop halts the background ingestion loop and waits for completion.
func (a *LiveAgent) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	done := a.done
	a.mu.Unlock()

	if done != nil {
		<-done
	}
}

// Step performs a single cycle of frame capture, vision parsing, differ event detection, and server ingestion.
func (a *LiveAgent) Step(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	a.mu.RLock()
	grabber := a.grabber
	win := a.targetWindow
	query := a.cfg.WindowQuery
	tableID := a.cfg.TableID
	heroID := a.cfg.HeroID
	roiCfg := a.cfg.ROIConfig
	srv := a.srv
	parser := a.parser
	differ := a.differ
	a.mu.RUnlock()

	if grabber == nil {
		return errors.New("nil frame grabber")
	}
	if parser == nil {
		return errors.New("nil frame parser")
	}
	if differ == nil {
		return errors.New("nil state differ")
	}

	var targetWin WindowInfo
	if win != nil {
		targetWin = *win
	} else {
		if _, isNative := grabber.(*NativeGrabber); isNative && query != "" {
			if windows, err := ListAllWindows(); err == nil {
				if found := FilterPokerWindow(windows, query); found != nil {
					a.SetTargetWindow(found)
					targetWin = *found
				}
			}
		}
		if targetWin.ID == 0 {
			targetWin = WindowInfo{
				ID:         1,
				Title:      query,
				IsOnScreen: true,
			}
		}
	}

	img, err := grabber.CaptureWindow(targetWin)
	if err != nil {
		return fmt.Errorf("frame capture failed: %w", err)
	}
	if img == nil {
		return errors.New("grabber returned nil frame")
	}

	a.mu.Lock()
	a.lastFrame = img
	a.mu.Unlock()

	state, err := parser.ParseFrame(img, roiCfg)
	if err != nil {
		return fmt.Errorf("frame parsing failed: %w", err)
	}
	if state == nil {
		return errors.New("parser returned nil state")
	}

	if tableID != "" {
		state.TableID = tableID
	}
	if heroID != "" {
		state.HeroID = heroID
	}

	a.mu.Lock()
	prevState := a.lastState
	a.lastState = state
	a.mu.Unlock()

	events := differ.DetectEvents(prevState, state)
	if srv != nil {
		for _, ev := range events {
			if ev.TableID == "" {
				ev.TableID = state.TableID
			}
			_, _ = srv.ProcessEvent(ev)
		}

		if len(events) == 0 {
			_, _ = srv.IngestLiveState(state)
		}
	}

	return nil
}

// IsRunning returns whether the agent background loop is active.
func (a *LiveAgent) IsRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

// GetLastFrame returns the most recently captured screen frame.
func (a *LiveAgent) GetLastFrame() image.Image {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastFrame
}

// GetLastState returns the most recently parsed table state.
func (a *LiveAgent) GetLastState() *table.HandState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastState
}

// GetTargetWindow returns a copy of the currently targeted window information.
func (a *LiveAgent) GetTargetWindow() *WindowInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.targetWindow == nil {
		return nil
	}
	copyWin := *a.targetWindow
	return &copyWin
}

// SetTargetWindow updates the target window for frame captures.
func (a *LiveAgent) SetTargetWindow(win *WindowInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if win == nil {
		a.targetWindow = nil
		return
	}
	copyWin := *win
	a.targetWindow = &copyWin
}

// GetROIConfig returns the current table Region of Interest layout.
func (a *LiveAgent) GetROIConfig() vision.ROIConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.ROIConfig
}

// SetROIConfig updates the table Region of Interest layout.
func (a *LiveAgent) SetROIConfig(cfg vision.ROIConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ROIConfig = cfg
}

// GetFPS returns the configured capture framerate.
func (a *LiveAgent) GetFPS() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.FPS
}

// GetTableID returns the configured table identifier.
func (a *LiveAgent) GetTableID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.TableID
}

// GetHeroID returns the configured hero player identifier.
func (a *LiveAgent) GetHeroID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.HeroID
}
