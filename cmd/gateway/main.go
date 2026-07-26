package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"image-gateway-middleware/internal/access"
	"image-gateway-middleware/internal/admin"
	"image-gateway-middleware/internal/app"
	"image-gateway-middleware/internal/audit"
	"image-gateway-middleware/internal/config"
	"image-gateway-middleware/internal/httpdata"
	"image-gateway-middleware/internal/image"
	"image-gateway-middleware/internal/persistence"
	"image-gateway-middleware/internal/processor"
	"image-gateway-middleware/internal/storage"
	"image-gateway-middleware/internal/upstream"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.LoadBootstrap()
	if err != nil {
		log.Error("configuration error", "error", err)
		os.Exit(1)
	}
	layout, err := storage.Prepare(cfg.DataDir)
	if err != nil {
		log.Error("prepare data directories", "error", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	store, err := persistence.Open(ctx, cfg.DatabasePath)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	runtimeCfg, err := config.LoadRuntime(ctx, store.DB)
	if err != nil {
		log.Error("load runtime configuration", "error", err)
		os.Exit(1)
	}
	upstreamClient := upstream.New(cfg.NewAPIBaseURL, cfg.UpstreamTimeout)
	downloader := image.NewDownloader(image.Storage{Images: layout.Images, Temp: layout.Temp}, cfg.ImageTimeout, runtimeCfg.DownloadAttempts, runtimeCfg.RetryBaseDelay, cfg.MaxImageBytes, cfg.DownloadWorkers, runtimeCfg.MaxRedirects)
	imageProcessor := processor.New(downloader, audit.New(store.DB), cfg.PublicImageBase, store.DB)
	proxy := httpdata.NewProxy(upstreamClient, cfg.MaxJSONBodyBytes, cfg.MaxResponseBytes, imageProcessor, func(ctx context.Context) error {
		if err := store.DB.PingContext(ctx); err != nil {
			return err
		}
		return imageProcessor.Preflight(ctx, cfg.MinFreeBytes, cfg.DataDir)
	})
	dataHandler := access.AllowClients(
		httpdata.NewRouter(proxy, http.HandlerFunc(imageProcessor.ServeImage), httpdata.Health(store.DB)),
		cfg.DataAllowedClients,
	)
	auth := admin.NewAuth(store.DB, cfg.CookieSecure)
	if err = auth.EnsureAdmin(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Error("initialize administrator", "error", err)
		os.Exit(1)
	}
	adminHandler := access.AllowClients(
		admin.NewServer(store.DB, auth, downloader, cfg.PublicImageBase, cfg.DataDir, func(runtime config.Runtime) {
			downloader.UpdatePolicy(runtime.DownloadAttempts, runtime.RetryBaseDelay, runtime.MaxRedirects)
		}).Handler(),
		cfg.AdminAllowedClients,
	)
	a := app.App{Config: cfg, Runtime: runtimeCfg, Store: store, Layout: layout, DataHandler: dataHandler, AdminHandler: adminHandler, Log: log}
	if err = a.Run(ctx); err != nil {
		log.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
