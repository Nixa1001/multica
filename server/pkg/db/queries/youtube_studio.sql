-- name: RecordYouTubeMarkdownResult :one
WITH candidate AS (
    SELECT b.id AS binding_id, b.workspace_id, b.project_id, b.issue_id,
           a.id AS artifact_id, gen_random_uuid() AS result_id,
           gen_random_uuid() AS event_id, now() AS recorded_at
    FROM agent_task_queue t
    JOIN issue i ON i.id = t.issue_id
    JOIN youtube_issue_binding b ON b.issue_id = i.id
        AND b.workspace_id = i.workspace_id AND b.project_id = i.project_id AND b.active
    JOIN youtube_video_project v ON v.project_id = i.project_id
        AND v.workspace_id = i.workspace_id
    JOIN youtube_artifact a ON a.project_id = v.project_id
        AND a.workspace_id = v.workspace_id AND a.artifact_key = b.artifact_key
    WHERE t.id = sqlc.arg('source_task_id') AND t.status = 'completed'
      AND i.workspace_id = sqlc.arg('workspace_id')
      AND (SELECT count(*) FROM youtube_issue_binding active_binding
           WHERE active_binding.workspace_id = i.workspace_id
             AND active_binding.project_id = i.project_id
             AND active_binding.issue_id = i.id
             AND active_binding.active) = 1
      AND (i.parent_issue_id IS NULL OR EXISTS (
          SELECT 1 FROM issue parent
          WHERE parent.id = i.parent_issue_id
            AND parent.workspace_id = i.workspace_id
            AND parent.project_id = i.project_id
      ))
    LIMIT 1
), result AS (
    INSERT INTO youtube_issue_result (
        id, event_id, workspace_id, project_id, binding_id, artifact_id,
        source_issue_id, source_task_id, markdown, sha256, recorded_at
    )
    SELECT result_id, event_id, workspace_id, project_id, binding_id, artifact_id,
           issue_id, sqlc.arg('source_task_id'), sqlc.arg('markdown'),
           sqlc.arg('sha256'), recorded_at
    FROM candidate
    ON CONFLICT (workspace_id, binding_id, source_task_id) DO NOTHING
    RETURNING *
), outbox AS (
    INSERT INTO youtube_studio_outbox (event_id, workspace_id, result_id, event_kind, payload)
    SELECT event_id, workspace_id, id, 'IssueResultRecordedV1',
           jsonb_build_object(
             'event_id', event_id, 'workspace_id', workspace_id,
             'video_id', project_id, 'binding_id', binding_id,
             'artifact_id', artifact_id, 'source_issue_id', source_issue_id,
             'source_task_id', source_task_id, 'kind', 'markdown',
             'markdown', markdown, 'sha256', sha256, 'recorded_at', recorded_at
           )
    FROM result
    ON CONFLICT (event_id) DO NOTHING
)
SELECT * FROM result;

-- name: ProjectYouTubeStudioEvent :one
WITH source AS (
    SELECT o.result_id, r.artifact_id, r.workspace_id, r.source_issue_id,
           r.source_task_id, r.markdown, r.sha256, r.recorded_at,
           a.current_version_number
    FROM youtube_studio_outbox o
    JOIN youtube_issue_result r ON r.id = o.result_id
    JOIN youtube_artifact a ON a.id = r.artifact_id
    WHERE o.event_id = sqlc.arg('event_id')
      AND o.lease_token = sqlc.arg('lease_token')
    FOR UPDATE OF o, a
), inserted AS (
    INSERT INTO youtube_artifact_version (
        workspace_id, artifact_id, version_number, source_result_id,
        source_issue_id, source_task_id, markdown, sha256, recorded_at
    )
    SELECT workspace_id, artifact_id, current_version_number + 1, result_id,
           source_issue_id, source_task_id, markdown, sha256, recorded_at
    FROM source
    WHERE NOT EXISTS (
        SELECT 1 FROM youtube_artifact_version v
        WHERE v.source_result_id = source.result_id
    )
    ON CONFLICT (source_result_id) DO NOTHING
    RETURNING artifact_id, version_number
), bumped AS (
    UPDATE youtube_artifact a SET current_version_number = inserted.version_number,
        updated_at = now()
    FROM inserted WHERE a.id = inserted.artifact_id
    RETURNING a.id AS artifact_id, inserted.version_number
), existing AS (
    SELECT v.artifact_id, v.version_number
    FROM source
    JOIN youtube_artifact_version v ON v.source_result_id = source.result_id
), projected AS (
    SELECT artifact_id, version_number FROM bumped
    UNION ALL
    SELECT artifact_id, version_number FROM existing
)
UPDATE youtube_studio_outbox o SET consumed_at = now(), lease_token = NULL,
    attempt_count = attempt_count + 1, last_error = NULL, updated_at = now()
FROM projected
WHERE o.event_id = sqlc.arg('event_id') AND o.lease_token = sqlc.arg('lease_token')
RETURNING o.event_id;

-- name: ClaimYouTubeStudioEvent :one
WITH due AS (
    SELECT event_id FROM youtube_studio_outbox
    WHERE consumed_at IS NULL AND dead_lettered_at IS NULL AND next_attempt_at <= now()
    ORDER BY next_attempt_at, created_at
    FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE youtube_studio_outbox o
SET lease_token = gen_random_uuid(), next_attempt_at = sqlc.arg('lease_until'), updated_at = now()
FROM due WHERE o.event_id = due.event_id
RETURNING o.*;

-- name: MarkYouTubeStudioEventConsumed :exec
UPDATE youtube_studio_outbox SET consumed_at = now(), lease_token = NULL,
    attempt_count = attempt_count + 1, last_error = NULL, updated_at = now()
WHERE event_id = sqlc.arg('event_id') AND lease_token = sqlc.arg('lease_token');

-- name: MarkYouTubeStudioEventFailed :exec
UPDATE youtube_studio_outbox SET attempt_count = attempt_count + 1,
    last_error = left(sqlc.arg('last_error'), 1000), lease_token = NULL,
    next_attempt_at = sqlc.arg('next_attempt_at'), updated_at = now()
WHERE event_id = sqlc.arg('event_id') AND lease_token = sqlc.arg('lease_token');

-- name: MarkYouTubeStudioEventDeadLettered :exec
UPDATE youtube_studio_outbox SET attempt_count = attempt_count + 1,
    last_error = left(sqlc.arg('last_error'), 1000), dead_lettered_at = now(),
    lease_token = NULL, updated_at = now()
WHERE event_id = sqlc.arg('event_id') AND lease_token = sqlc.arg('lease_token');

-- name: DeleteYouTubeStudioByProject :exec
DELETE FROM youtube_studio_outbox o WHERE o.workspace_id = $1 AND o.result_id IN (SELECT r.id FROM youtube_issue_result r WHERE r.workspace_id = $1 AND r.project_id = $2);

-- name: DeleteYouTubeStudioArtifactsByProject :exec
DELETE FROM youtube_artifact_version v WHERE v.workspace_id = $1 AND v.artifact_id IN (SELECT a.id FROM youtube_artifact a WHERE a.workspace_id = $1 AND a.project_id = $2);

-- name: DeleteYouTubeStudioResultsByProject :exec
DELETE FROM youtube_issue_result WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteYouTubeStudioArtifactsProject :exec
DELETE FROM youtube_artifact WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteYouTubeStudioBindingsByProject :exec
DELETE FROM youtube_issue_binding WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteYouTubeStudioProfileByProject :exec
DELETE FROM youtube_video_project WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteYouTubeStudioWorkspace :exec
DELETE FROM youtube_studio_outbox WHERE workspace_id = $1;

-- name: DeleteYouTubeStudioWorkspaceVersions :exec
DELETE FROM youtube_artifact_version WHERE workspace_id = $1;

-- name: DeleteYouTubeStudioWorkspaceResults :exec
DELETE FROM youtube_issue_result WHERE workspace_id = $1;

-- name: DeleteYouTubeStudioWorkspaceArtifacts :exec
DELETE FROM youtube_artifact WHERE workspace_id = $1;

-- name: DeleteYouTubeStudioWorkspaceBindings :exec
DELETE FROM youtube_issue_binding WHERE workspace_id = $1;

-- name: DeleteYouTubeStudioWorkspaceProfiles :exec
DELETE FROM youtube_video_project WHERE workspace_id = $1;

-- name: BootstrapYouTubeMarkdownBinding :exec
WITH profile AS (
    INSERT INTO youtube_video_project (project_id, workspace_id)
    SELECT p.id, p.workspace_id FROM project p
    WHERE p.id = $2 AND p.workspace_id = $1
    ON CONFLICT (project_id) DO NOTHING
), artifact AS (
    INSERT INTO youtube_artifact (workspace_id, project_id, artifact_key)
    SELECT p.workspace_id, p.id, $3
    FROM project p
    WHERE p.id = $2 AND p.workspace_id = $1
    ON CONFLICT (workspace_id, project_id, artifact_key) DO NOTHING
)
INSERT INTO youtube_issue_binding (workspace_id, project_id, issue_id, artifact_key, kind)
SELECT i.workspace_id, i.project_id, i.id, $3, 'markdown'
FROM issue i
WHERE i.id = $4 AND i.workspace_id = $1 AND i.project_id = $2
  AND (i.parent_issue_id IS NULL OR EXISTS (
      SELECT 1 FROM issue parent WHERE parent.id = i.parent_issue_id
        AND parent.workspace_id = i.workspace_id AND parent.project_id = i.project_id
  ))
  AND EXISTS (SELECT 1 FROM project p WHERE p.id = $2 AND p.workspace_id = $1)
ON CONFLICT (workspace_id, issue_id) WHERE active DO NOTHING;
