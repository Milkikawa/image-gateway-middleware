package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"image-gateway-middleware/internal/config"
	"image-gateway-middleware/internal/persistence"
	"image-gateway-middleware/internal/storage"
)

type App struct {
	Config                    config.Bootstrap
	Runtime                   config.Runtime
	Store                     *persistence.Store
	Layout                    storage.Layout
	DataHandler, AdminHandler http.Handler
	Log                       *slog.Logger
}

func (a *App) Run(ctx context.Context) error {
	data := &http.Server{Addr: a.Config.DataListenAddr, Handler: a.DataHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	admin := &http.Server{Addr: a.Config.AdminListenAddr, Handler: a.AdminHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	go func() { a.Log.Info("data plane listening", "addr", data.Addr); errs <- data.ListenAndServe() }()
	go func() { a.Log.Info("admin plane listening", "addr", admin.Addr); errs <- admin.ListenAndServe() }()
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e1 := data.Shutdown(shutdownCtx)
	e2 := admin.Shutdown(shutdownCtx)
	if runErr != nil {
		return runErr
	}
	if e1 != nil {
		return e1
	}
	return e2
}
