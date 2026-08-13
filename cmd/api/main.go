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

	"github.com/autoservice/autoservice/internal/api"
	"github.com/autoservice/autoservice/internal/auth"
	"github.com/autoservice/autoservice/internal/config"
	"github.com/autoservice/autoservice/internal/enrichment"
	"github.com/autoservice/autoservice/internal/store"
)

func main() {
	cfg := config.Load()
	if cfg.JWTSecret == "" {
		log.Fatal("JWT_SECRET is required in .env")
	}

	authMgr, err := auth.NewManager(cfg.JWTSecret, cfg.JWTTTL)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	ctx := context.Background()
	st, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v (check DATABASE_URL in .env)", err)
	}
	defer st.Close()

	if email, password, name, ok := auth.BootstrapFromEnv(); ok {
		t, err := st.EnsureBootstrapAdmin(ctx, email, password, name)
		if err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
		if t != nil {
			log.Printf("bootstrapped super_admin %s", t.Email)
		}
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	worker := &enrichment.Worker{Store: st, Client: enrichment.NewClient(), Log: log.Default()}
	go worker.Run(workerCtx, 3*time.Second)
	if err := enrichment.EnqueueSeed(ctx, st); err != nil {
		log.Printf("enrichment seed enqueue: %v", err)
	} else {
		log.Printf("MechMind enrichment worker started (NG top-5 makes, 2010+ lean NHTSA data)")
	}

	srv := &api.Server{Store: st, Auth: authMgr}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("MechMind API listening on %s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	workerCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
	}
}
