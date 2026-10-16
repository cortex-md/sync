package main

import (
	"os"

	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres"
	"github.com/cortexnotes/cortex-sync/internal/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	if cfg.Server.Environment == "production" {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	for _, warning := range cfg.Warnings {
		log.Warn().Msg(warning)
	}

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := postgres.RunMigrationCommand(cfg.Database, os.Args[2:]); err != nil {
			log.Fatal().Err(err).Msg("migration failed")
		}
		return
	}

	log.Info().Fields(cfg.Summary()).Msg("configuration")
	if err := server.Run(cfg); err != nil {
		log.Fatal().Err(err).Msg("server failed")
	}
}
