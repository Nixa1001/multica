CREATE TABLE youtube_video_project (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    lifecycle_state TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE youtube_issue_binding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    artifact_key TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind = 'markdown'),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE youtube_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    artifact_key TEXT NOT NULL,
    current_version_number INTEGER NOT NULL DEFAULT 0 CHECK (current_version_number >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE youtube_issue_result (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    binding_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    source_issue_id UUID NOT NULL,
    source_task_id UUID NOT NULL,
    markdown TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE youtube_artifact_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    source_result_id UUID NOT NULL,
    source_issue_id UUID NOT NULL,
    source_task_id UUID NOT NULL,
    markdown TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE youtube_studio_outbox (
    event_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    result_id UUID NOT NULL,
    event_kind TEXT NOT NULL CHECK (event_kind = 'IssueResultRecordedV1'),
    payload JSONB NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    last_error TEXT,
    dead_lettered_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
