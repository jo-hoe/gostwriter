 package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jo-hoe/gostwriter/internal/common"
	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/llm/mock"
	"github.com/jo-hoe/gostwriter/internal/processor"
	"github.com/jo-hoe/gostwriter/internal/server"
	"github.com/jo-hoe/gostwriter/internal/storage"
	"github.com/jo-hoe/gostwriter/internal/targets"
	gitTarget "github.com/jo-hoe/gostwriter/internal/targets/git"
)

func main() {
	// Logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Load config
	cfg, err := appcfg.Load("")
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

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
	reposRoot := filepath.Join(cfg.Server.StorageDir, common.ReposDirName)
	switch cfg.Target.Type {
	case "git":
		t, err := gitTarget.New(cfg.Target.Name, cfg.Target.Git, reposRoot)
		if err != nil {
			logger.Error("init git target", "name", cfg.Target.Name, "err", err)
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
