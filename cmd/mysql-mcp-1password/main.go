// Command mysql-mcp-1password is a stdio MCP server that runs MySQL queries with
// connection details fetched from 1Password on first use.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
	"github.com/dexical30/mysql-mcp-1password/internal/opcred"
	"github.com/dexical30/mysql-mcp-1password/internal/server"
	"github.com/dexical30/mysql-mcp-1password/internal/session"
	"github.com/dexical30/mysql-mcp-1password/internal/sqlclass"
)

func main() {
	logger := log.New(os.Stderr, "mysql-mcp-1password: ", log.LstdFlags)

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logger.Fatal(err)
	}
	// Fail fast on a missing or unsigned op. 1Password itself is not
	// contacted until the first query, so starting a session never shows a
	// Touch ID prompt. The binary is verified again before every lookup.
	if _, err := opcred.ResolveOpPath(cfg.OpPath); err != nil {
		logger.Fatal(err)
	}
	cls, err := sqlclass.New()
	if err != nil {
		logger.Fatal(err)
	}

	var src session.Fetcher = opcred.NewRefSource(cfg.OpPath, cfg.Refs)
	if cfg.UsesItem() {
		src = opcred.NewSource(cfg.OpPath, cfg.OpVault, cfg.OpItem, cfg.Fields)
	}
	gate := session.NewGate(src, session.MySQLOpener(cfg.TLS), cfg.IdleTimeout, session.RealClock, logger.Printf)
	defer gate.Lock()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, cls, gate, server.Executor{})
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		logger.Printf("server stopped: %v", err)
	}
}
