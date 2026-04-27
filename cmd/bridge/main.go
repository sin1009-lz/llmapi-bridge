package main

import (
	"api-bridge/internal/account"
	"api-bridge/internal/config"
	"api-bridge/internal/model"
	"api-bridge/internal/provider"
	"api-bridge/internal/proxy"
	"api-bridge/internal/router"
	"api-bridge/internal/scanner"
	"api-bridge/internal/store"
	"api-bridge/internal/version"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("api-bridge starting", "version", version.Version, "build", version.BuildTime)

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("config.yaml not found, using default configuration")
			cfg = config.DefaultConfig()
		} else {
			slog.Error("failed to load config", "error", err)
			os.Exit(1)
		}
	}

	s := store.NewJSONStore(cfg.Storage.DataDir)
	if err := s.Load(); err != nil {
		slog.Error("failed to load data store", "error", err)
		os.Exit(1)
	}

	accManager := account.NewManager(s)
	provRegistry := provider.NewRegistry(s)

	modelFetcher := model.NewFetcher(s, provRegistry)
	modelMapper := model.NewMapper(s)

	proxyHandler := proxy.NewProxy(provRegistry, modelMapper)

	scanner := scanner.New(provRegistry)

	modelFetcher.Start()

	initProvidersFromConfig(cfg, provRegistry)

	r := router.New(s, s, accManager, provRegistry, modelFetcher, modelMapper, proxyHandler, scanner, cfg.Server.AdminKey)
	webHandler := r.WebHandler()

	apiSrv := &http.Server{
		Addr:    cfg.Server.ListenAddr,
		Handler: r,
	}
	webSrv := &http.Server{
		Addr:    cfg.Server.WebListenAddr,
		Handler: webHandler,
	}

	go func() {
		slog.Info("starting API server", "addr", cfg.Server.ListenAddr)
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("API server error", "error", err)
			os.Exit(1)
		}
	}()

	go func() {
		slog.Info("starting Web server", "addr", cfg.Server.WebListenAddr)
		if err := webSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Web server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down servers...")

	modelFetcher.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := webSrv.Shutdown(ctx); err != nil {
		slog.Error("web server forced to shutdown", "error", err)
	}
	if err := apiSrv.Shutdown(ctx); err != nil {
		slog.Error("API server forced to shutdown", "error", err)
	}

	if err := s.Save(); err != nil {
		slog.Error("failed to save data on shutdown", "error", err)
	}

	slog.Info("servers stopped")
}

func initProvidersFromConfig(cfg *config.Config, reg *provider.Registry) {
	existing, err := reg.List()
	if err != nil {
		slog.Error("failed to list existing providers", "error", err)
		return
	}

	existingSet := make(map[string]bool)
	for _, p := range existing {
		existingSet[p.Name] = true
	}

	for _, pc := range cfg.Providers {
		if existingSet[pc.Name] {
			slog.Info("provider already exists, skipping", "name", pc.Name)
			continue
		}

		_, err := reg.Create(pc.Name, provider.ProviderType(pc.Type), pc.BaseURL, pc.APIKeys, pc.Enabled)
		if err != nil {
			slog.Error("failed to create provider from config", "name", pc.Name, "error", err)
		} else {
			slog.Info("created provider from config", "name", pc.Name)
		}
	}
}
