package main

import (
	"log"

	"ai-auto-annotator/backend/internal/config"
	"ai-auto-annotator/backend/internal/engine"
	"ai-auto-annotator/backend/internal/httpapi"
	"ai-auto-annotator/backend/internal/repository"
	"ai-auto-annotator/backend/internal/service"
	"ai-auto-annotator/backend/internal/ws"
)

func main() {
	cfg := config.Load()

	store, err := repository.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	// Jobs run in goroutines of this process; anything still marked
	// pending/running belongs to a dead process and would jam the
	// active-job guards forever.
	if err := store.FailInterruptedJobs(); err != nil {
		log.Fatalf("reset interrupted jobs: %v", err)
	}

	eng := engine.NewConfigured(cfg.EngineMode, cfg.LibPath, cfg.ModelPath, cfg.Threads, cfg.PythonURL)
	hub := ws.NewHub()
	prompts := service.NewPromptService(cfg)

	router := httpapi.NewRouter(cfg, store, eng, prompts, hub)
	log.Printf("ai-auto-annotator listening on %s", cfg.Addr)
	if err := router.Run(cfg.Addr); err != nil {
		log.Fatal(err)
	}
}
