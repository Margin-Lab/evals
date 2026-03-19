package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/api"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/logutil"
	"github.com/marginlab/margin-eval/agent-server/internal/run"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		logutil.Fatal("server.startup_failed", map[string]any{
			"stage": "config.from_env",
			"error": err.Error(),
		})
		os.Exit(1)
	}

	if err := ensureRuntimeDirs(cfg); err != nil {
		logutil.Fatal("server.startup_failed", map[string]any{
			"stage": "runtime_dirs.ensure",
			"error": err.Error(),
		})
		os.Exit(1)
	}

	store := state.NewStore(cfg.StateFile)
	if err := store.Init(); err != nil {
		logutil.Fatal("server.startup_failed", map[string]any{
			"stage": "state_store.init",
			"error": err.Error(),
		})
		os.Exit(1)
	}

	runtime, err := agentruntime.New(cfg)
	if err != nil {
		logutil.Fatal("server.startup_failed", map[string]any{
			"stage": "agentruntime.new",
			"error": err.Error(),
		})
		os.Exit(1)
	}
	runManager := run.NewManager(cfg, store, runtime)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.NewServer(cfg, store, runtime, runManager).Router(),
		ErrorLog:          logutil.NewStdlibLogger("server.http_internal_error"),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logutil.Info("server.listening", map[string]any{"listen_addr": cfg.ListenAddr})
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logutil.Fatal("server.http_listen_failed", map[string]any{
				"listen_addr": cfg.ListenAddr,
				"error":       err.Error(),
			})
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := runManager.Shutdown(shutdownCtx); err != nil {
		logutil.Error("server.run_manager_shutdown_failed", map[string]any{"error": err.Error()})
	}
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logutil.Error("server.http_shutdown_failed", map[string]any{"error": err.Error()})
	}
}

func ensureRuntimeDirs(cfg config.Config) error {
	if err := fsutil.EnsureDir(cfg.RootDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(cfg.BinDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(cfg.StateDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(cfg.WorkspacesDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(cfg.ConfigDir, 0o755); err != nil {
		return err
	}
	return nil
}
