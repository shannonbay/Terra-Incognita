package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/shannonbay/terra-incognita/engine/harness"
)

func main() {
	configFile := flag.String("config", "", "Path to YAML config file")
	flag.Parse()

	cfg, err := harness.LoadConfig(*configFile, flag.Args())
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	// Auth check depends on provider.
	switch cfg.Provider {
	case "claude-code":
		log.Printf("provider=claude-code  using active `claude` CLI auth (Claude.ai plan or API key)")
	default:
		if cfg.APIKey() == "" {
			log.Fatalf("ANTHROPIC_API_KEY is not set (use --provider claude-code to use Claude.ai plan instead)")
		}
	}

	srv, err := harness.New(cfg)
	if err != nil {
		log.Fatalf("creating harness: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("Terra Incognita harness  provider=%s  model=%s  port=%d",
		cfg.Provider, cfg.Model, cfg.Port)

	if err := srv.ListenAndServe(ctx); err != nil {
		log.Printf("server stopped: %v", err)
	}

	if err := srv.Close(); err != nil {
		log.Printf("closing harness: %v", err)
	}
}
