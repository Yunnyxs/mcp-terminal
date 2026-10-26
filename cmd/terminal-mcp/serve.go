package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/server"
)

const shutdownTimeout = 5 * time.Second

func run(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	stdio := fs.Bool("stdio", false, "run the stdio transport (Claude Desktop) instead of HTTP")
	_ = fs.Parse(args)

	s, err := config.FromEnv()
	if err != nil {
		return err
	}

	serverLog, err := openLog(s.ServerLog, config.ServerLogMaxBytes)
	if err != nil {
		return fmt.Errorf("log server: %w", err)
	}

	log.SetOutput(io.MultiWriter(os.Stderr, serverLog))

	activity, err := activityMiddleware(s.ActivityLog, s.LogMaxBytes)
	if err != nil {
		return fmt.Errorf("activity log: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(s, server.Options{Middleware: []mcp.Middleware{activity}})
	if *stdio {
		log.Printf("start: mode stdio (root=%s)", s.Root)
		return srv.Run(ctx, &mcp.StdioTransport{})
	}
	return serveHTTP(ctx, srv, s)
}

func serveHTTP(ctx context.Context, srv *mcp.Server, s config.Settings) error {
	if s.Token == "" {
		return errors.New("TERMINAL_MCP_TOKEN is not set")
	}
	handler, err := server.NewHandler(srv, s.Token, nil)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              net.JoinHostPort(s.Host, strconv.Itoa(s.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		log.Printf("shutdown: signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		shutdownDone <- httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on http://%s/<TOKEN>/mcp (root=%s)", httpSrv.Addr, s.Root)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownDone
	if errors.Is(err, context.DeadlineExceeded) {
		log.Printf("shutdown exceeded %s, remaining requests are forcibly stopped", shutdownTimeout)
		return nil
	}
	log.Printf("shutdown: server stopped")
	return err
}
