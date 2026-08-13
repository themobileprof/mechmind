package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/autoservice/autoservice/internal/config"
	"github.com/autoservice/autoservice/internal/enrichment"
	"github.com/autoservice/autoservice/internal/store"
)

func main() {
	_ = godotenv.Load()
	seed := flag.Bool("seed", false, "enqueue NG catalog recall seed jobs")
	drain := flag.Bool("drain", false, "process pending jobs until queue empty or timeout")
	timeout := flag.Duration("timeout", 20*time.Minute, "drain timeout")
	statsOnly := flag.Bool("stats", false, "print enrichment stats and exit")
	flag.Parse()

	cfg := config.Load()
	ctx := context.Background()
	st, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	printStats := func() {
		s, err := st.EnrichmentStats(ctx)
		if err != nil {
			log.Fatal(err)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
	}

	if *statsOnly || (!*seed && !*drain) {
		printStats()
		if !*seed && !*drain {
			return
		}
	}

	if *seed {
		_, err := st.EnqueueEnrichmentJob(ctx, "seed_catalog", map[string]any{"forced": true}, time.Now().UTC())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("enqueued seed_catalog")
	}

	if *drain {
		w := &enrichment.Worker{Store: st, Client: enrichment.NewClient(), Log: log.Default()}
		deadline := time.Now().Add(*timeout)
		processed := 0
		for time.Now().Before(deadline) {
			job, err := st.ClaimEnrichmentJob(ctx)
			if err != nil {
				log.Fatal(err)
			}
			if job == nil {
				fmt.Printf("queue empty after %d jobs\n", processed)
				break
			}
			if err := w.Process(ctx, job); err != nil {
				log.Printf("job %s (%s): %v", job.ID, job.Kind, err)
				_ = st.FailEnrichmentJob(ctx, job.ID, err.Error(), time.Now().UTC().Add(time.Minute))
				continue
			}
			_ = st.CompleteEnrichmentJob(ctx, job.ID)
			processed++
			if processed%25 == 0 {
				log.Printf("processed %d jobs…", processed)
			}
		}
		printStats()
	}
}
