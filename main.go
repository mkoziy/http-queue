// Command http-queue is a durable HTTP queue engine backed by BadgerDB.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mkoziy/http-queue/api"
	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/db"
	"github.com/mkoziy/http-queue/queue"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.Open(cfg.BadgerPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start sweeper.
	sweeper := queue.NewSweeper(database, cfg)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sweeper.Start(ctx)
	}()

	// Start BadgerDB value log GC goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			again:
				if err := database.RunValueLogGC(0.5); err == nil {
					goto again
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	router := api.New(database, cfg)

	// Bind to the configured port (or :0 for OS-assigned port).
	addr := fmt.Sprintf(":%s", cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	// Get the actual port the OS assigned (needed when Port is "0").
	actualPort := listener.Addr().(*net.TCPAddr).Port

	// Write the actual port to a file if PORT_FILE env var is set.
	if pf := os.Getenv("PORT_FILE"); pf != "" {
		if err := os.WriteFile(pf, []byte(strconv.Itoa(actualPort)), 0644); err != nil {
			log.Printf("warning: failed to write port file %q: %v", pf, err)
		}
	}

	server := &http.Server{
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	log.Printf("listening on :%d", actualPort)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cancel()
		log.Printf("serve error: %v", err)
	}

	// Server has stopped; clean up.
	cancel()
	wg.Wait()
	if err := db.Close(database); err != nil {
		log.Printf("db close error: %v", err)
	}
}
