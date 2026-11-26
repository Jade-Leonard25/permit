package db

import (
	"fmt"
	"time"

	"backend-business-permit-data/schema"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect opens the connection and pings. It does NOT migrate,
// so the server starts without touching the schema.
func Connect(databaseURL string) (*gorm.DB, error) {
	gormDB, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}

	// Keep idle connections at 0 so Neon can suspend compute when idle
	sqlDB.SetMaxIdleConns(0)
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(2 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return gormDB, nil
}

// Migrate creates or updates tables and the full-text search column and index.
// Run it on purpose with: go run ./cmd/migrate
func Migrate(gormDB *gorm.DB) error {
	if err := gormDB.AutoMigrate(
		&schema.Document{},
		&schema.Chunk{},
		&schema.APIKey{},
		&schema.UsageLog{},
	); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}

	searchSQL := `
		ALTER TABLE chunks
		ADD COLUMN IF NOT EXISTS search_tsv TSVECTOR GENERATED ALWAYS AS (
			setweight(to_tsvector('english'::regconfig, COALESCE(section_number, '') || ' ' || COALESCE(heading, '')), 'A') ||
			setweight(to_tsvector('english'::regconfig, COALESCE(verbatim_text, '')), 'B')
		) STORED;

		CREATE INDEX IF NOT EXISTS idx_chunks_search_tsv ON chunks USING GIN (search_tsv);
	`
	if err := gormDB.Exec(searchSQL).Error; err != nil {
		return fmt.Errorf("create full-text search index: %w", err)
	}

	return nil
}
