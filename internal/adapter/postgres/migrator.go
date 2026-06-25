package postgres

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/cortexnotes/cortex-sync/internal/config"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func RunMigrationCommand(cfg config.DatabaseConfig, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing migrate command")
	}

	m, err := migrate.New(cfg.MigrationsPath, cfg.URL)
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}
	defer m.Close()

	switch args[0] {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("running migrations: %w", err)
		}
		return nil
	case "down":
		steps := 1
		if len(args) > 1 {
			parsed, parseErr := strconv.Atoi(args[1])
			if parseErr != nil || parsed < 1 {
				return fmt.Errorf("down step count must be a positive integer")
			}
			steps = parsed
		}
		if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("rolling back migrations: %w", err)
		}
		return nil
	case "version":
		version, dirty, err := m.Version()
		if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
			return fmt.Errorf("reading migration version: %w", err)
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
		return nil
	default:
		return fmt.Errorf("unsupported migrate command %q", args[0])
	}
}
