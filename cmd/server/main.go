package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gsb/internal/mapping"
	"gsb/internal/rdf"
	"gsb/internal/service"
	"gsb/internal/store"
	"gsb/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5350", "listen address")
	dbPath := flag.String("db", "gsb.db", "SQLite database path")
	fixturesDir := flag.String("fixtures", "fixtures", "fixture directory")
	auto := flag.Bool("auto-seed", true, "seed bundled fixtures when the database is empty")
	flag.Parse()

	ctx := context.Background()
	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	if *auto {
		if err := seedIfEmpty(ctx, st, *fixturesDir); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	srv := service.New(st)
	handler := web.New(srv).Handler()
	log.Printf("ontology migration review listening on http://%s (db=%s)", *listen, *dbPath)
	hs := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// seedIfEmpty imports the bundled reduced Turtle/JSON-LD fixtures once,
// keyed by content fingerprint so repeated starts are idempotent.
func seedIfEmpty(ctx context.Context, st *store.Store, dir string) error {
	docs, err := st.ListDocuments(ctx)
	if err != nil {
		return err
	}
	if len(docs) > 0 {
		return nil
	}
	type seed struct {
		kind, name, format, file, graph string
	}
	seeds := []seed{
		{"ontology_old", "hr-ontology v1", "turtle", "ontology_old.ttl", ""},
		{"ontology_new", "hr-ontology v2", "turtle", "ontology_new.ttl", ""},
		{"candidates", "explicit mapping candidates", "turtle", "mappings.ttl", ""},
		{"instances", "sample instance graph", "jsonld", "instances.jsonld", "http://gsb.local/graph/instances"},
		{"instances", "extra source instances (trig/turtle)", "turtle", "instances_more.ttl", "http://gsb.local/graph/instances-more"},
		{"shapes", "target SHACL shapes", "turtle", "shapes_new.ttl", ""},
	}
	for _, sd := range seeds {
		path := filepath.Join(dir, sd.file)
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				log.Printf("seed: skipping missing %s", path)
				continue
			}
			return err
		}
		var quads []rdf.Quad
		switch sd.format {
		case "turtle", "trig":
			quads, err = rdf.ParseTurtle(string(body))
		case "jsonld":
			quads, err = rdf.ParseJSONLD(string(body), sd.graph)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", sd.file, err)
		}
		doc := &store.Document{
			Kind: sd.kind, Name: sd.name, Format: sd.format,
			GraphIRI: sd.graph, Body: string(body), Quads: quads,
			ContentFP: rdf.GraphFingerprint(quads),
		}
		inserted, err := st.ImportDocument(ctx, doc)
		if err != nil {
			return err
		}
		// Candidates also feed the mapping set.
		if sd.kind == "candidates" {
			ms, err := st.LoadMapping(ctx)
			if err != nil {
				return err
			}
			rules, err := mapping.ParseCandidates(quads)
			if err != nil {
				return err
			}
			for _, r := range rules {
				ms.AddCandidate(r)
			}
			if err := st.SaveMapping(ctx, ms); err != nil {
				return err
			}
		}
		log.Printf("seed: %-12s %s  quads=%d inserted=%v", sd.kind, sd.file, len(quads), inserted)
	}
	return nil
}
