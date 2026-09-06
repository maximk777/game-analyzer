// Command sfsagent is the live assistant driven by the game wire instead of the
// screen. It runs the same server, profiler and HUD as before, but its table
// state comes from CoinPoker's SmartFoxServer traffic (pkg/sfs) rather than from
// reading frames -- exact seats, stacks, cards, actions and pot, with no OCR.
//
//	# live (needs BPF access: ChmodBPF, or run under sudo)
//	sfsagent -hero-id 712757
//
//	# replay a captured session
//	sfsagent -hero-id 712757 -r testdata/sfs/session_hero.pcap
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"poker-game-analyzer/pkg/audit"
	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/sfs"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func main() {
	var (
		httpPort = flag.Int("port", 8080, "HTTP and WebSocket server port")
		dbPath   = flag.String("db", "./bin/db/poker_analyzer.db", "SQLite database file path")
		webDir   = flag.String("web-dir", "web", "static frontend assets directory")
		iface    = flag.String("i", "en0", "capture interface")
		pcapFile = flag.String("r", "", "replay a pcap file instead of capturing live")
		heroID   = flag.Int64("hero-id", 0, "hero's userId (identifies our seat)")
		heroName = flag.String("hero-name", "", "hero's screen name (alternative to -hero-id)")
		mockLLM  = flag.Bool("mock-llm", false, "use the deterministic mock profiler")
		tableID  = flag.String("table-id", "coinpoker-live", "table id the HUD subscribes to; the live table is broadcast under it")
		auditLog = flag.String("audit", "./bin/logs/decisions.jsonl", "decision audit log (JSONL); empty to disable")
	)
	flag.Parse()

	if *heroID == 0 && *heroName == "" {
		log.Fatal("sfsagent: set -hero-id (or -hero-name) so the assistant knows which seat is yours")
	}

	srv, prof, cleanup := buildServer(*dbPath, *webDir, *auditLog, *mockLLM)
	defer cleanup()

	go serve(srv, *httpPort)

	live := &sfs.Live{
		HeroID:   *heroID,
		HeroName: *heroName,
		OnState: func(hs *table.HandState) {
			// Broadcast the live table under the id the HUD listens on. The
			// real wire table number stays the hand id's prefix for the record;
			// this is only the room the single HUD subscribes to.
			hs.TableID = *tableID
			if _, err := srv.IngestLiveState(hs); err != nil {
				log.Printf("[SFS] ingest: %v", err)
			}
		},
	}
	log.Printf("[SFS] hero=%q table=%q — open a table and it's your move to see advice", heroLabel(*heroID, *heroName), *tableID)

	stream, stop, err := openStream(*pcapFile, *iface)
	if err != nil {
		log.Fatalf("sfsagent: %v", err)
	}
	defer stop()

	// Detect the game port set for live capture -- it is not fixed.
	ports := detectPorts(*pcapFile)

	done := make(chan error, 1)
	go func() { done <- live.Run(stream, ports) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		log.Print("[SFS] shutting down")
	case err := <-done:
		if err != nil {
			log.Printf("[SFS] source ended: %v", err)
		} else {
			log.Print("[SFS] capture ended")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Stop(ctx)
	_ = prof
}

// openStream returns the pcap byte stream: a file for replay, or a live tcpdump.
func openStream(pcapFile, iface string) (io.Reader, func(), error) {
	if pcapFile != "" {
		f, err := os.Open(pcapFile)
		if err != nil {
			return nil, nil, err
		}
		return f, func() { f.Close() }, nil
	}
	eps, err := sfs.DetectGamePorts()
	if err != nil {
		log.Printf("[SFS] port detection: %v", err)
	}
	ports := sfs.Ports(eps)
	if len(ports) == 0 {
		log.Printf("[SFS] no game connection found -- open a table; falling back to the port range")
	} else {
		log.Printf("[SFS] capturing game port(s) %v", ports)
	}
	stream, stop, err := sfs.Capture(iface, ports)
	if err != nil {
		return nil, nil, err
	}
	return stream, func() { stop() }, nil
}

// detectPorts returns the port set to match while reading. For a replay the file
// carries whatever it carries, so the range is used; live, it is the detected
// set (re-detected so the reader and the capture agree).
func detectPorts(pcapFile string) []int {
	if pcapFile != "" {
		return nil
	}
	eps, _ := sfs.DetectGamePorts()
	return sfs.Ports(eps)
}

// buildServer wires the storage, LLM, profiler and HTTP server, mirroring
// cmd/server. Returns the server, profiler and a cleanup.
func buildServer(dbPath, webDir, auditPath string, mockLLM bool) (*server.Server, *profiler.Profiler, func()) {
	db, err := storage.NewSQLiteDB(dbPath)
	if err != nil {
		log.Fatalf("sfsagent: open db: %v", err)
	}
	cache := storage.NewMemoryCache()

	var llmClient llm.Client
	var coach llm.Coach
	if mockLLM {
		llmClient = llm.NewMockClient()
	} else if c, _, err := llm.NewClient(context.Background(), llm.Resolve("", "", "", os.Getenv)); err != nil {
		log.Printf("[SFS] no model: %v", err)
		llmClient = llm.NewMockClient()
	} else {
		llmClient, coach = c, c
	}

	prof := profiler.NewProfiler(cache, db, llmClient)
	srv := server.NewServer(cache, db, prof)
	if coach != nil {
		srv.SetCoach(coach)
	}
	if webDir != "" {
		if _, err := os.Stat(webDir); err == nil {
			srv.MountStatic(webDir)
		}
	}

	// The decision audit is what makes a live session diagnosable afterwards:
	// every recommendation is recorded with the inputs it had and, more
	// usefully, the ones it was missing. Without it a session leaves nothing
	// behind but the showdowns, and "the panel said nothing on the new hand" has
	// to be reproduced by hand. Failing to open it must not stop the assistant.
	var auditLog *audit.Logger
	if auditPath != "" {
		if lg, err := audit.NewLogger(auditPath); err != nil {
			log.Printf("[SFS] decision audit disabled: %v", err)
		} else {
			auditLog = lg
			srv.SetAuditLogger(lg)
			log.Printf("[SFS] decision audit → %s", auditPath)
		}
	}

	cleanup := func() {
		if auditLog != nil {
			log.Printf("[AUDIT] %d distinct decisions recorded to %s", auditLog.Written(), auditPath)
			summary := auditLog.GapSummary()
			keys := make([]string, 0, len(summary))
			for k := range summary {
				keys = append(keys, string(k))
			}
			sort.Strings(keys)
			for _, k := range keys {
				log.Printf("[AUDIT]   missing %-20s in %d decisions", k, summary[audit.Gap(k)])
			}
			_ = auditLog.Close()
		}
		prof.Close()
		_ = db.Close()
	}
	return srv, prof, cleanup
}

func serve(srv *server.Server, port int) {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("[SFS] HUD at http://localhost:%d  ws://localhost:%d/ws/tables/{id}", port, port)
	if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[SFS] server: %v", err)
	}
}

// heroLabel is the hero identity for the startup log.
func heroLabel(id int64, name string) string {
	if name != "" {
		return name
	}
	if id != 0 {
		return fmt.Sprintf("id=%d", id)
	}
	return "?"
}
