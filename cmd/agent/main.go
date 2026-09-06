package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"poker-game-analyzer/pkg/capture"
	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/server"
	"sort"
	"strings"

	"poker-game-analyzer/pkg/audit"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/vision"
)

// Config holds configuration parameters for the unified live assistant.
type Config struct {
	WindowQuery string
	Port        int
	FPS         int
	DBPath      string
	OpenHUD     bool
	TableID     string
	HeroID      string
	MockLLM     bool
	LLM         llm.Settings
	WebDir      string
	AuditPath   string
	// VisionHelperPath is the ScreenCaptureKit reader. It is configurable so a
	// test can start something harmless in its place.
	VisionHelperPath string
}

// AgentApp encapsulates the entire runtime environment for the live assistant.
type AgentApp struct {
	cfg          Config
	db           *storage.SQLiteDB
	cache        *storage.MemoryCache
	llmClient    llm.Client
	prof         *profiler.Profiler
	srv          *server.Server
	liveAgent    *capture.LiveAgent
	grabber      capture.FrameGrabber
	macVisionCmd *exec.Cmd
	auditLog     *audit.Logger

	errCh chan error
}

// NewAgentApp initializes all components (database, cache, profiler, server, live grabber).
func NewAgentApp(cfg Config, grabber capture.FrameGrabber) (*AgentApp, error) {
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 3
	}
	if cfg.WindowQuery == "" {
		cfg.WindowQuery = "CoinPoker"
	}
	if cfg.TableID == "" {
		cfg.TableID = "coinpoker-live"
	}
	if cfg.HeroID == "" {
		cfg.HeroID = "Hero"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "./bin/db/poker_analyzer.db"
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "web"
	}

	// 1. Initialize SQLite storage
	db, err := storage.NewSQLiteDB(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database at %q: %w", cfg.DBPath, err)
	}

	// 2. Initialize in-memory cache
	cache := storage.NewMemoryCache()

	// 3. Initialize LLM Client
	var llmClient llm.Client
	if cfg.MockLLM || !cfg.LLM.Live() {
		llmClient = llm.NewMockClient()
	} else {
		llmClient = llm.NewOpenAIClient(cfg.LLM.Key, cfg.LLM.BaseURL, cfg.LLM.Model)
	}

	// 4. Initialize Opponent Profiler
	prof := profiler.NewProfiler(cache, db, llmClient)

	// 5. Initialize Server & WebSocket Hub
	srv := server.NewServer(cache, db, prof)

	// The second opinion at the table, when there is a model to ask. It is a
	// separate panel and never the decision: the tool's own answer is computed
	// and reproducible, this one is a reading in words, and the disagreements
	// are the part worth looking at.
	if !cfg.MockLLM && cfg.LLM.Live() {
		if c, ok := llmClient.(llm.Coach); ok {
			srv.SetCoach(c)
		}
	}

	// The built-in layout and a hand-made one behave very differently, and
	// there was no way to tell from the outside which was in use.
	if path, loaded, err := srv.LoadROIConfig(); err != nil {
		log.Printf("table layout at %s could not be read, using the built-in one: %v", path, err)
	} else if loaded {
		log.Printf("table layout loaded from %s", path)
	} else {
		log.Printf("no saved table layout at %s, using the built-in one; calibrate to replace it", path)
	}

	// 6. Mount static web assets if available
	if cfg.WebDir != "" {
		if _, err := os.Stat(cfg.WebDir); err == nil {
			srv.MountStatic(cfg.WebDir)
		}
	}

	// 7. Initialize FrameGrabber
	if grabber == nil {
		grabber = capture.NewNativeGrabber()
	}

	// 8. Initialize LiveAgent
	liveCfg := capture.LiveAgentConfig{
		WindowQuery: cfg.WindowQuery,
		FPS:         cfg.FPS,
		TableID:     cfg.TableID,
		HeroID:      cfg.HeroID,
		ROIConfig:   vision.DefaultCoinPoker6MaxROI(),
	}
	liveAgent := capture.NewLiveAgent(grabber, srv, liveCfg)

	// The decision audit is what makes a live session diagnosable afterwards:
	// each recommendation is stored with the inputs it had and the ones it was
	// missing. A failure to open it must not stop the assistant.
	var auditLog *audit.Logger
	if cfg.AuditPath != "" {
		lg, err := audit.NewLogger(cfg.AuditPath)
		if err != nil {
			log.Printf("[AGENT] Warning: decision audit disabled: %v", err)
		} else {
			auditLog = lg
			srv.SetAuditLogger(lg)
		}
	}

	return &AgentApp{
		cfg:       cfg,
		db:        db,
		cache:     cache,
		llmClient: llmClient,
		prof:      prof,
		srv:       srv,
		liveAgent: liveAgent,
		grabber:   grabber,
		auditLog:  auditLog,
		errCh:     make(chan error, 1),
	}, nil
}

// Start launches the HTTP server and background frame capture loop.
func (app *AgentApp) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", app.cfg.Port)

	go func() {
		if err := app.srv.Start(addr); err != nil && err != http.ErrServerClosed {
			app.errCh <- err
		}
	}()

	// One reader, not two.
	//
	// Both of these read the same table and post to the same server, and the
	// stabiliser merged whatever arrived: names came from one source and money
	// from the other. Live on 2026-09-06 the panel showed the right five
	// nicknames beside a pot of 7.9 and stacks of 5, 0.6 and 55, at a table
	// playing 1K/2K with two hundred thousand in front of every seat. The
	// audit log has both readings of the same second, one of them carrying a
	// player called "F7FFFF$" -- and no blinds, which is the tell, because the
	// helper takes them from the window title and the ROI reader has no title
	// to take them from.
	//
	// The helper wins where it exists: it uses the system's own text
	// recognition, while the ROI reader matches glyphs against a hand-drawn
	// 5x7 bitmap font that was never going to read anti-aliased text drawn by
	// a game engine.
	if app.startVisionHelper() {
		log.Printf("[AGENT] Reading the table with the ScreenCaptureKit helper")
	} else {
		log.Printf("[AGENT] Reading the table with the built-in ROI reader; its text recognition is a bitmap font and reads the client badly")
		if err := app.liveAgent.Start(ctx); err != nil {
			_ = app.srv.Stop(context.Background())
			return fmt.Errorf("failed to start live agent: %w", err)
		}
	}

	hudURL := fmt.Sprintf("http://localhost:%d/hud.html", app.cfg.Port)
	if app.cfg.OpenHUD {
		go func() {
			time.Sleep(150 * time.Millisecond)
			if err := LaunchHUD(hudURL); err != nil {
				log.Printf("[AGENT] HUD not started: %v", err)
			}
		}()
	} else {
		log.Printf("[AGENT] HUD not started. Run `make ui` for the floating panel, or open %s", hudURL)
	}

	return nil
}

// defaultVisionHelper is the ScreenCaptureKit reader as `make vision` builds it.
const defaultVisionHelper = "./bin/mac_vision_agent"

// startVisionHelper launches the ScreenCaptureKit reader and reports whether it
// is running. Nothing else may read the table while it is.
func (app *AgentApp) startVisionHelper() bool {
	binPath := app.cfg.VisionHelperPath
	if binPath == "" {
		if runtime.GOOS != "darwin" {
			return false
		}
		binPath = defaultVisionHelper
	}

	info, err := os.Stat(binPath)
	if err != nil {
		log.Printf("[AGENT] %s is not built -- run `make vision`", binPath)
		return false
	}

	// `go run` rebuilds the Go half and nothing else, so a stale helper keeps
	// running old recognition while the logs look fresh. Saying so out loud
	// costs a stat call and saves a long evening of debugging behaviour that
	// was already fixed.
	if newer := swiftSourcesNewerThan(info.ModTime()); len(newer) > 0 {
		log.Printf("[AGENT] WARNING: %s is older than %s -- run `make` to rebuild the vision helper",
			binPath, strings.Join(newer, ", "))
	}

	cmd := exec.Command(binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Without this the helper posts to port 8080 regardless of -port.
	cmd.Env = append(os.Environ(), fmt.Sprintf(
		"POKER_RTA_ENDPOINT=http://127.0.0.1:%d/api/v1/tables/%s/events",
		app.cfg.Port, app.cfg.TableID))
	if err := cmd.Start(); err != nil {
		log.Printf("[AGENT] %s would not start: %v", binPath, err)
		return false
	}

	app.macVisionCmd = cmd
	log.Printf("[AGENT] Native macOS ScreenCaptureKit Vision helper started (PID %d)", cmd.Process.Pid)
	return true
}

// Stop gracefully shuts down all running agent services.
func (app *AgentApp) Stop(ctx context.Context) error {
	var firstErr error

	if app.macVisionCmd != nil && app.macVisionCmd.Process != nil {
		_ = app.macVisionCmd.Process.Kill()
	}

	if app.liveAgent != nil {
		app.liveAgent.Stop()
	}

	if app.srv != nil {
		if err := app.srv.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if app.auditLog != nil {
		summary := app.auditLog.GapSummary()
		log.Printf("[AUDIT] %d distinct decisions recorded to %s", app.auditLog.Written(), app.cfg.AuditPath)
		if len(summary) == 0 {
			log.Printf("[AUDIT] No missing inputs recorded.")
		} else {
			keys := make([]string, 0, len(summary))
			for k := range summary {
				keys = append(keys, string(k))
			}
			sort.Strings(keys)
			for _, k := range keys {
				log.Printf("[AUDIT]   missing %-20s in %d decisions", k, summary[audit.Gap(k)])
			}
		}
		_ = app.auditLog.Close()
	}

	if app.prof != nil {
		app.prof.Close()
	}

	if app.db != nil {
		if err := app.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// DB returns the underlying SQLite storage instance.
func (app *AgentApp) DB() *storage.SQLiteDB { return app.db }

// Cache returns the underlying MemoryCache instance.
func (app *AgentApp) Cache() *storage.MemoryCache { return app.cache }

// Server returns the underlying Server instance.
func (app *AgentApp) Server() *server.Server { return app.srv }

// LiveAgent returns the active LiveAgent orchestration instance.
func (app *AgentApp) LiveAgent() *capture.LiveAgent { return app.liveAgent }

// Profiler returns the Opponent Profiler instance.
func (app *AgentApp) Profiler() *profiler.Profiler { return app.prof }

// Config returns the configured runtime parameters.
func (app *AgentApp) Config() Config { return app.cfg }

// Errors returns a channel receiving asynchronous server fatal errors.
func (app *AgentApp) Errors() <-chan error { return app.errCh }

// swiftSourcesNewerThan lists the vision sources modified after the helper was
// built. Empty means the running helper matches the code on disk.
func swiftSourcesNewerThan(builtAt time.Time) []string {
	entries, err := os.ReadDir("pkg/capture")
	if err != nil {
		return nil
	}
	var stale []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".swift") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(builtAt) {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	return stale
}

// LaunchHUD opens the HUD as a native floating panel, and only as that.
//
// It used to fall back through Chrome, Chromium and finally the default
// browser. A browser window cannot be kept above another application's window,
// so the advice went behind the client at exactly the moment it was being
// acted on -- and a build that opened a browser tab on its own was noise on
// top of that. The panel floats, takes no focus when clicked, and follows the
// table; nothing else is worth opening.
//
// Not finding the panel is not an error. The server is useful on its own, and
// the interface is a separate thing to start -- see `make ui`.
func LaunchHUD(hudURL string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("the HUD panel is macOS only; open %s in a browser", hudURL)
	}

	for _, panel := range hudPanelCandidates() {
		if _, err := os.Stat(panel); err != nil {
			continue
		}
		cmd := exec.Command(panel, "--url", hudURL)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting %s: %w", panel, err)
		}
		return nil
	}

	return fmt.Errorf("bin/hud_panel not built -- run `make` and then `make ui`")
}

// hudPanelCandidates looks beside the running binary first, so an installed
// copy finds its own panel rather than one left in a working directory.
func hudPanelCandidates() []string {
	out := []string{filepath.Join("bin", "hud_panel")}
	if exe, err := os.Executable(); err == nil {
		out = append([]string{filepath.Join(filepath.Dir(exe), "hud_panel")}, out...)
	}
	return out
}
func main() {
	var (
		windowFlag    = flag.String("window", "CoinPoker", "Target poker window query")
		portFlag      = flag.Int("port", 8080, "Server HTTP/WebSocket port")
		fpsFlag       = flag.Int("fps", 3, "Frame grabber capture rate")
		dbPathFlag    = flag.String("db", "./bin/db/poker_analyzer.db", "SQLite database file path")
		openHUDFlag   = flag.Bool("open-hud", false, "Open the native floating HUD panel with the server (see `make ui` to run it separately)")
		tableIDFlag   = flag.String("table-id", "coinpoker-live", "Active table ID")
		heroIDFlag    = flag.String("hero-id", "Hero", "Active hero player ID")
		mockLLMFlag   = flag.Bool("mock-llm", false, "Use offline deterministic mock profiler")
		llmKeyFlag    = flag.String("llm-key", "", "API key; falls back to GEMINI_API_KEY, GOOGLE_API_KEY or OPENAI_API_KEY")
		llmURLFlag    = flag.String("llm-url", "", "OpenAI-compatible base URL; defaults to Gemini, or to OpenAI when the key came from OPENAI_API_KEY")
		llmModelFlag  = flag.String("llm-model", "", "Model name; defaults to "+llm.GeminiModel)
		webDirFlag    = flag.String("web-dir", "web", "Web assets directory")
		auditPathFlag = flag.String("audit", "./bin/logs/decisions.jsonl", "Decision audit log (JSONL); empty to disable")
	)
	flag.Parse()

	settings := llm.Resolve(*llmKeyFlag, *llmURLFlag, *llmModelFlag, os.Getenv)
	useMockLLM := *mockLLMFlag || !settings.Live()

	cfg := Config{
		WindowQuery: *windowFlag,
		Port:        *portFlag,
		FPS:         *fpsFlag,
		DBPath:      *dbPathFlag,
		OpenHUD:     *openHUDFlag,
		TableID:     *tableIDFlag,
		HeroID:      *heroIDFlag,
		MockLLM:     useMockLLM,
		LLM:         settings,
		WebDir:      *webDirFlag,
		AuditPath:   *auditPathFlag,
	}

	log.Printf("[AGENT] ===================================================")
	log.Printf("[AGENT] Starting Poker RTA Unified Live Assistant...")
	log.Printf("[AGENT] Target Window: %q | Capture FPS: %d", cfg.WindowQuery, cfg.FPS)
	log.Printf("[AGENT] Table ID: %q | Hero ID: %q", cfg.TableID, cfg.HeroID)
	log.Printf("[AGENT] Server Port: %d | Database: %q", cfg.Port, cfg.DBPath)
	if cfg.MockLLM {
		log.Printf("[AGENT] Profiler: rules only, no model (%s)", cfg.LLM.Describe())
	} else {
		log.Printf("[AGENT] Profiler: %s", cfg.LLM.Describe())
	}
	log.Printf("[AGENT] Open Floating HUD: %v", cfg.OpenHUD)
	log.Printf("[AGENT] ===================================================")

	app, err := NewAgentApp(cfg, nil)
	if err != nil {
		log.Fatalf("[AGENT] Initialization error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.Start(ctx); err != nil {
		log.Fatalf("[AGENT] Failed to start assistant: %v", err)
	}

	log.Printf("[AGENT] Live assistant is running.")
	log.Printf("[AGENT] HUD URL:        http://localhost:%d/hud.html", cfg.Port)
	log.Printf("[AGENT] Table Felt:     http://localhost:%d/", cfg.Port)
	log.Printf("[AGENT] ROI Calibrator: http://localhost:%d/calibrate.html", cfg.Port)
	log.Printf("[AGENT] WebSocket:      ws://localhost:%d/ws/tables/%s", cfg.Port, cfg.TableID)

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stopCh:
		log.Printf("[AGENT] Received signal %v. Shutting down gracefully...", sig)
	case err := <-app.Errors():
		log.Printf("[AGENT] Server fatal error: %v. Initiating shutdown...", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := app.Stop(shutdownCtx); err != nil {
		log.Printf("[AGENT] Error during shutdown: %v", err)
	}

	log.Printf("[AGENT] Live assistant stopped cleanly.")
}
