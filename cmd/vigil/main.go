package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vigil/internal/app"
	"vigil/internal/cli"
	"vigil/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		if err := (cli.Runner{
			Out: os.Stdout,
			Err: os.Stderr,
		}).Run(context.Background(), os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 2 {
		fmt.Fprintf(os.Stderr, "serve does not accept arguments\n")
		os.Exit(1)
	}
	runServer()
}

func runServer() {
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
