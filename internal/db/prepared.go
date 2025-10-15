package db

import (
	"database/sql"
	"fmt"
)

// prepareCommonStatements prepares frequently used statements for better performance
func prepareCommonStatements(db *sql.DB) error {
	statements := []string{
		// Session queries
		"SELECT id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at FROM sessions ORDER BY updated_at DESC LIMIT ?",
		"SELECT COUNT(*) FROM sessions WHERE updated_at > datetime('now', '-1 day')",

		// Message queries
		"SELECT id, session_id, role, parts, model, created_at, updated_at, finished_at, provider FROM messages WHERE session_id = ? ORDER BY created_at ASC",
		"SELECT COUNT(*) FROM messages WHERE session_id = ? AND role = 'user'",

		// File queries
		"SELECT id, session_id, path, content, version, created_at, updated_at FROM files WHERE session_id = ? ORDER BY updated_at DESC",
		"SELECT DISTINCT path FROM files WHERE session_id = ?",
	}

	for _, stmt := range statements {
		if _, err := db.Prepare(stmt); err != nil {
			return fmt.Errorf("failed to prepare statement: %w", err)
		}
	}

	return nil
}
