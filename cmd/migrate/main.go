// Package main provides the RentLoop database migration command
//
// This executable connects to PostgreSQL using DATABASE_URL
// discovers SQL migration files in the migrations directory,
// and applies any pending migrations in order while recording
// them in the schema_migrations tracking table
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsDir is the filesystem location containing SQL migrations
const migrationsDir = "internal/db/migrations"

// main initializes the database connection and runs pending migrations
func main() {
	ctx := context.Background()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is not set")
		os.Exit(1)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		slog.Error("failed to connect", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := run(ctx, pool); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

// run applies all pending SQL migrations in order
func run(ctx context.Context, pool *pgxpool.Pool) error {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}

	files, err := upFiles()
	if err != nil {
		return fmt.Errorf("read migration files: %w", err)
	}

	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		return fmt.Errorf("fetch applied migrations: %w", err)
	}

	var ran int

	for _, f := range files {
		name := filepath.Base(f)

		if applied[name] {
			slog.Info("skip (already applied)", "migration", name)
			continue
		}

		sql, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		up, err := parseUp(string(sql))
		if err != nil {
			return fmt.Errorf("parse up section in %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for %s : %w", name, err)
		}

		if _, err := tx.Exec(ctx, up); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("execute %s: %w", name, err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES ($1, $2)`,
			name, time.Now(),
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", name, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}

		slog.Info("applied", "migration", name)
		ran++
	}

	if ran == 0 {
		slog.Info("nothing to migrate - database is up to date")
	} else {
		slog.Info("migrations complete", "applied", ran)
	}

	return nil

}

// ensureMigrationsTable creates the schema_migrations table if it does not exist
func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		     		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL
		)
		 `)

	return err
}

// appliedMigrations returns a set of migration filenames already applied
func appliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}

	return applied, rows.Err()
}

// upFiles returns sorted SQL migrationd files from the migrations directory
func upFiles() ([]string, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, filepath.Join(migrationsDir, e.Name()))
		}
	}

	sort.Strings(files)
	return files, nil
}

// parseUp extracts SQL between migration markers : -- +migrate Up, -- +migrate Down
// If markers are missing the entire file is treated as the Up migration.
func parseUp(content string) (string, error) {
	const upMarker = "-- +migrate Up"
	const downMarker = "-- +migrate Down"

	upIdx := strings.Index(content, upMarker)

	if upIdx == -1 {
		return strings.TrimSpace(content), nil
	}

	start := upIdx + len(upMarker)
	end := strings.Index(content, downMarker)

	if end == -1 {
		return strings.TrimSpace(content[start:]), nil
	}

	if end < start {
		return "", fmt.Errorf("-- +migrate Down appears before -- +migrate Up")
	}

	return strings.TrimSpace(content[start:end]), nil
}
