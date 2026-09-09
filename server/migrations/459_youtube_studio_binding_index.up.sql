CREATE UNIQUE INDEX CONCURRENTLY youtube_issue_binding_active_issue_idx ON youtube_issue_binding (workspace_id, issue_id) WHERE active;
