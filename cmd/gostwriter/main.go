package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jo-hoe/gostwriter/internal/common"
	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/llm/aiproxy"
	"github.com/jo-hoe/gostwriter/internal/llm/mock"
	"github.com/jo-hoe/gostwriter/internal/processor"
	"github.com/jo-hoe/gostwriter/internal/server"
	"github.com/jo-hoe/gostwriter/internal/storage"
	"github.com/jo-hoe/gostwriter/internal/targets"
	githubTarget "github.com/jo-hoe/gostwriter/internal/targets/github"
)

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func main() {
	// Provisional logger during early startup
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Load config
	cfg, err := appcfg.Load("")
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	// Reconfigure logger with configured level
	lvl := parseLogLevel(cfg.Server.LogLevel)
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(logger)

	// Store (SQLite)
	store, err := jobs.NewSQLiteStore(cfg.Server.DatabasePath)
	if err != nil {
		logger.Error("sqlite open", "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	// Uploader
	uploader := storage.NewUploader(cfg.Server.StorageDir)

	// Target (single)
	reg := targets.NewRegistry()
	switch cfg.Target.Type {
	case "github":
		t, err := githubTarget.New(cfg.Target.Name, cfg.Target.GitHub)
		if err != nil {
			logger.Error("init github target", "name", cfg.Target.Name, "err", err)
			os.Exit(1)
		}
		reg.Add(t)
	default:
		logger.Error("unsupported target type", "type", cfg.Target.Type, "name", cfg.Target.Name)
		os.Exit(1)
	}

	// LLM client
	var llmClient llm.Client
	switch cfg.LLM.Provider {
	case "mock":
		llmClient = mock.New(cfg.LLM.Mock)
	case "aiproxy":
		llmClient = aiproxy.New(cfg.LLM.AIProxy)
	default:
		logger.Error("unsupported llm provider", "provider", cfg.LLM.Provider)
		os.Exit(1)
	}

	// Worker and queue
	worker := processor.New(logger, cfg, store, llmClient, reg)
	queue := jobs.NewQueue(logger, common.DefaultQueueCapacity, cfg.Server.WorkerCount)
	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := queue.Start(rootCtx, worker); err != nil {
		logger.Error("start queue", "err", err)
		os.Exit(1)
	}

	// HTTP server
	svc := &server.Service{
		Log:       logger,
		Cfg:       cfg,
		Store:     store,
		Queue:     queue,
		Uploader:  uploader,
		Targets:   reg,
		Processor: worker,
	}
	httpSrv := server.NewHTTPServer(svc)

	// Run server in background
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "address", cfg.Server.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or server error
	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			logger.Error("server error", "err", err)
		}
	}

	// Graceful shutdown
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "err", err)
	}
	// Stop workers
	queue.Shutdown(cfg.Server.ShutdownGrace)
	logger.Info("server stopped")
}
