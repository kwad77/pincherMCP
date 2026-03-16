package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

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

	// Build MCP server with all 12 tools
	srv := server.New(store, idx, version)

	// Context with graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start background file watcher
	go idx.Watch(ctx)

	// Run MCP server over stdio
	if err := srv.MCPServer().Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		log.Fatalf("pincherMCP: server error: %v", err)
	}
}
