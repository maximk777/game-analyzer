package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/server"
	"poker-game-analyzer/pkg/storage"
)

func main() {
	var (
		portFlag        = flag.Int("port", 8080, "HTTP and WebSocket server port")
		dbPathFlag      = flag.String("db", "./bin/db/poker_analyzer.db", "SQLite database file path")
		openAIKeyFlag   = flag.String("openai-key", "", "OpenAI API key (falls back to OPENAI_API_KEY env)")
		openAIModelFlag = flag.String("openai-model", "gpt-4o-mini", "OpenAI model name")
		mockLLMFlag     = flag.Bool("mock-llm", false, "Use mock deterministic LLM profiler")
		webDirFlag      = flag.String("web-dir", "web", "Directory containing static frontend assets")
	)
	flag.Parse()

	apiKey := *openAIKeyFlag
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	log.Printf("[SERVER] Starting Poker RTA Engine & Live HUD Server...")
	log.Printf("[SERVER] Database: %s", *dbPathFlag)

	// 1. Initialize SQLite storage
	db, err := storage.NewSQLiteDB(*dbPathFlag)
	if err != nil {
		log.Fatalf("[SERVER] Failed to open SQLite database: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	// 2. Initialize in-memory cache
	cache := storage.NewMemoryCache()

	// 3. Initialize LLM Client
	var llmClient llm.Client
	if *mockLLMFlag || apiKey == "" {
		log.Printf("[SERVER] Using deterministic Mock LLM Client (offline mode)")
		llmClient = llm.NewMockClient()
	} else {
		log.Printf("[SERVER] Using OpenAI LLM Client (model: %s)", *openAIModelFlag)
		llmClient = llm.NewOpenAIClient(apiKey, "", *openAIModelFlag)
	}

	// 4. Initialize Opponent Profiler
	prof := profiler.NewProfiler(cache, db, llmClient)
	defer prof.Close()

	// 5. Initialize Server & Hub
	srv := server.NewServer(cache, db, prof)

	// 6. Mount static web assets
	if *webDirFlag != "" {
		if _, err := os.Stat(*webDirFlag); err == nil {
			log.Printf("[SERVER] Serving Web HUD from directory: %s", *webDirFlag)
			srv.MountStatic(*webDirFlag)
		} else {
			log.Printf("[SERVER] Static web directory %q not found, running API only", *webDirFlag)
		}
	}

	addr := fmt.Sprintf(":%d", *portFlag)

	// Graceful shutdown channel
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[SERVER] Listening on http://localhost:%d", *portFlag)
		log.Printf("[SERVER] WebSocket endpoint: ws://localhost:%d/ws/tables/{tableId}", *portFlag)
		log.Printf("[SERVER] REST API endpoint: http://localhost:%d/api/v1/tables", *portFlag)

		if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[SERVER] HTTP server encountered fatal error: %v", err)
		}
	}()

	<-stopCh
	log.Printf("[SERVER] Shutdown signal received. Closing gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Stop(ctx); err != nil {
		log.Printf("[SERVER] Error during server shutdown: %v", err)
	}

	log.Printf("[SERVER] Server stopped successfully.")
}
