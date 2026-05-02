package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vigil/internal/app"
	"vigil/internal/config"
)

func main() {
	cfg := config.Load()
	vigilApp, err := app.New(context.Background(), cfg)
	if err != nil {
		log.Fatalf("boot vigil: %v", err)
	}
	defer vigilApp.Close()

	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: vigilApp.Handler,
	}

	go func() {
		log.Printf("vigil listening on %s", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown failed: %v", err)
	}
}
