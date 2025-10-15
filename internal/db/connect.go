package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/pressly/goose/v3"
)

func Connect(ctx context.Context, dataDir string) (*sql.DB, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("data.dir is not set")
	}
	dbPath := filepath.Join(dataDir, "crush.db")

	// Set pragmas for better performance and concurrency
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA page_size = 4096;",
		"PRAGMA cache_size = -16000;", // Increased cache size
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA secure_delete = ON;",
		"PRAGMA busy_timeout = 30000;", // 30 second timeout
		"PRAGMA temp_store = memory;",
		"PRAGMA mmap_size = 268435456;", // 256MB memory-mapped I/O
	}

	db, err := driver.Open(dbPath, func(c *sqlite3.Conn) error {
		for _, pragma := range pragmas {
			if err := c.Exec(pragma); err != nil {
				return fmt.Errorf("failed to set pragma `%s`: %w", pragma, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for better performance
	db.SetMaxOpenConns(25)        // Maximum number of open connections
	db.SetMaxIdleConns(10)        // Maximum number of idle connections
	db.SetConnMaxLifetime(5 * 60) // Maximum lifetime of a connection in seconds
	db.SetConnMaxIdleTime(2 * 60) // Maximum idle time for a connection in seconds

	// Verify connection with timeout
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	goose.SetBaseFS(FS)

	if err := goose.SetDialect("sqlite3"); err != nil {
		slog.Error("Failed to set dialect", "error", err)
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		slog.Error("Failed to apply migrations", "error", err)
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	// Prepare statements for common queries
	if err := prepareCommonStatements(db); err != nil {
		slog.Warn("Failed to prepare common statements", "error", err)
		// Don't fail initialization for this, just log warning
	}

	return db, nil
}
