package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pincherMCP/pincher/internal/db"
	"github.com/pincherMCP/pincher/internal/index"
	"github.com/pincherMCP/pincher/internal/server"
)

const version = "0.1.0"

func main() {
	var (
		showVersion = flag.Bool("version", false, "Print version and exit")
		dataDir     = flag.String("data-dir", "", "Override data directory (default: platform-appropriate)")
		verbose     = flag.Bool("verbose", false, "Enable verbose logging")
		httpAddr    = flag.String("http", "", "Also listen for HTTP requests on this address (e.g. :8080). Enables any HTTP client to call all tools via POST /v1/{tool}.")
		httpKey     = flag.String("http-key", "", "Require this bearer token on all HTTP requests (recommended for non-localhost deployments).")
		httpRate    = flag.Int("http-rate", 0, "Max HTTP requests per IP per minute. 0 = unlimited.")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("pincherMCP v%s\n", version)
		os.Exit(0)
	}

	if !*verbose {
		log.SetOutput(os.Stderr)
		log.SetFlags(0)
	}

	// Determine data directory
	dir := *dataDir
	if dir == "" {
		var err error
		dir, err = db.DataDir()
		if err != nil {
			log.Fatalf("pincherMCP: failed to determine data directory: %v", err)
		}
	}

	// Open SQLite store
	store, err := db.Open(dir)
	if err != nil {
		log.Fatalf("pincherMCP: failed to open database: %v", err)
	}
	defer store.Close()

	// Build indexer
	idx := index.New(store)

	// Build MCP server with all 14 tools
	srv := server.New(store, idx, version)

	// Context with graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start background file watcher and session persistence flusher
	go idx.Watch(ctx)
	srv.StartSessionFlusher(ctx)

	// Optionally run HTTP server for platform-agnostic access.
	if *httpAddr != "" {
		if *httpKey != "" {
			srv.SetHTTPKey(*httpKey)
		}
		if *httpRate > 0 {
			srv.SetRateLimit(*httpRate, time.Minute)
		}
		go func() {
			if err := srv.ListenAndServeHTTP(ctx, *httpAddr); err != nil {
				log.Printf("pincherMCP: http server error: %v", err)
			}
		}()
	}

	// Run MCP server over stdio
	if err := srv.MCPServer().Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		log.Fatalf("pincherMCP: server error: %v", err)
	}
}
