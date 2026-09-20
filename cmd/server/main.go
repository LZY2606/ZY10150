// Command gsb-server runs the RDF vocabulary migration review service.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/gsbmig/internal/app"
	"github.com/example/gsbmig/internal/store"
	"github.com/example/gsbmig/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5350", "HTTP listen address")
	dbPath := flag.String("db", "data/gsbmig.db", "SQLite database path")
	flag.Parse()

	if dir := dirOf(*dbPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, *dbPath)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	svc, err := app.New(ctx, db)
	if err != nil {
		log.Fatalf("init service: %v", err)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           web.New(svc).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("GSB migration review listening on http://%s (db %s)", *listen, *dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Println("shut down cleanly")
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == os.PathSeparator {
			return p[:i]
		}
	}
	return ""
}
