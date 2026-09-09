CREATE INDEX CONCURRENTLY youtube_studio_outbox_due_idx ON youtube_studio_outbox (next_attempt_at, created_at) WHERE consumed_at IS NULL AND dead_lettered_at IS NULL;
