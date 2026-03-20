package migrator

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

func Run(ctx context.Context, db *pgxpool.Pool, sqlFS fs.FS) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	files, err := listSQL(sqlFS)
	if err != nil {
		return err
	}

	for _, file := range files {
		var v string
		err := db.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = $1`, file).Scan(&v)
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("checking migration %s: %w", file, err)
		}

		content, err := fs.ReadFile(sqlFS, file)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file, err)
		}

		if _, err := db.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("applying migration %s: %w", file, err)
		}

		if _, err := db.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, file); err != nil {
			return fmt.Errorf("recording migration %s: %w", file, err)
		}

		log.Info().Str("migration", file).Msg("applied")
	}

	return nil
}

func listSQL(sqlFS fs.FS) ([]string, error) {
	var files []string
	err := fs.WalkDir(sqlFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".sql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing migration files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}
