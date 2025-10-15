-- +goose Up
-- +goose StatementBegin
-- Add composite indexes for better query performance on frequently accessed data
CREATE INDEX IF NOT EXISTS idx_messages_session_created_at ON messages (session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_files_session_created_at ON files (session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_parent_updated_at ON sessions (parent_session_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_messages_role_session_id ON messages (role, session_id);

-- Add partial indexes for common queries
CREATE INDEX IF NOT EXISTS idx_active_sessions ON sessions (updated_at) WHERE message_count > 0;
CREATE INDEX IF NOT EXISTS idx_unfinished_messages ON messages (session_id, created_at) WHERE finished_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_session_created_at;
DROP INDEX IF EXISTS idx_files_session_created_at;
DROP INDEX IF EXISTS idx_sessions_parent_updated_at;
DROP INDEX IF EXISTS idx_messages_role_session_id;
DROP INDEX IF EXISTS idx_active_sessions;
DROP INDEX IF EXISTS idx_unfinished_messages;
-- +goose StatementEnd