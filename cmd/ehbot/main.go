package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"DojinGo/internal/bot"
	"DojinGo/internal/collector"
	"DojinGo/internal/config"
	"DojinGo/internal/httpclient"
	"DojinGo/internal/storage"
	syncsvc "DojinGo/internal/sync"
	"DojinGo/internal/telegraph"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "config file path")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmsgprefix)

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}

	store, err := storage.New(cfg.Storage)
	if err != nil {
		logger.Fatalf("initialize storage: %v", err)
	}

	registry, err := collector.NewRegistry(cfg)
	if err != nil {
		logger.Fatalf("initialize collectors: %v", err)
	}

	tgHTTPClient, err := httpclient.New(cfg, nil)
	if err != nil {
		logger.Fatalf("initialize telegraph client: %v", err)
	}
	tgClient := telegraph.New(tgHTTPClient, cfg.Telegraph.Tokens, cfg.Telegraph.CatboxUserHash)
	syncer := syncsvc.New(
		tgClient,
		registry,
		store,
		cfg.Telegraph.AuthorName,
		cfg.Telegraph.AuthorURL,
		cfg.StorageTTL(),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	botService, err := bot.New(cfg, syncer, logger)
	if err != nil {
		logger.Fatalf("initialize bot: %v", err)
	}

	if err := botService.Start(ctx); err != nil {
		logger.Fatalf("run bot: %v", err)
	}
}
