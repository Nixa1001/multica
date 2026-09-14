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
-- name: GetYouTubeStudioBackendPID :one
SELECT pg_backend_pid();

-- name: LockProjectForYouTubeStudioBinding :one
SELECT id FROM project WHERE id = sqlc.arg(project_id) AND workspace_id = sqlc.arg(workspace_id) FOR KEY SHARE;

-- name: GetYouTubeStudioBinding :one
SELECT b.id, b.workspace_id, b.project_id, b.issue_id, b.artifact_key, b.kind, b.active, b.created_at,
       a.id AS artifact_id
FROM youtube_issue_binding b
JOIN youtube_artifact a ON a.workspace_id = b.workspace_id AND a.project_id = b.project_id AND a.artifact_key = b.artifact_key
WHERE b.workspace_id = sqlc.arg(workspace_id) AND b.project_id = sqlc.arg(project_id) AND b.issue_id = sqlc.arg(issue_id) AND b.active;

-- name: ListYouTubeStudioVideos :many
SELECT v.project_id AS video_id, p.title AS name, p.icon, v.lifecycle_state,
       count(b.id)::bigint AS material_count, 0::bigint AS attention_count, v.updated_at
FROM youtube_video_project v
JOIN project p ON p.id = v.project_id AND p.workspace_id = v.workspace_id
LEFT JOIN youtube_issue_binding b ON b.workspace_id = v.workspace_id AND b.project_id = v.project_id AND b.active
WHERE v.workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.narg('before_updated_at')::timestamptz IS NULL OR (v.updated_at, v.project_id) < (sqlc.narg('before_updated_at')::timestamptz, sqlc.narg('before_video_id')::uuid))
GROUP BY v.project_id, p.title, p.icon, v.lifecycle_state, v.updated_at
ORDER BY v.updated_at DESC, v.project_id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetYouTubeStudioVideo :one
SELECT v.project_id AS video_id, p.title AS name, v.lifecycle_state
FROM youtube_video_project v JOIN project p ON p.id = v.project_id AND p.workspace_id = v.workspace_id
WHERE v.workspace_id = sqlc.arg(workspace_id) AND v.project_id = sqlc.arg(project_id);

-- name: ListYouTubeStudioMaterials :many
SELECT b.id AS binding_id, a.id AS artifact_id, b.kind, b.issue_id, i.number AS issue_number,
       i.title AS issue_title, i.status AS issue_status, i.workspace_id AS issue_workspace_id,
       (SELECT issue_prefix FROM workspace WHERE id=b.workspace_id) AS issue_prefix,
       r.id AS latest_result_id, r.recorded_at AS latest_result_at,
       v.id AS current_version_id, v.version_number AS current_version_number, v.sha256 AS current_sha256, v.recorded_at AS current_recorded_at,
       CASE WHEN v.id IS NOT NULL THEN 'available' ELSE 'unavailable' END AS source_state,
       CASE WHEN v.id IS NOT NULL THEN 'ready' WHEN r.id IS NOT NULL THEN 'processing' ELSE 'awaiting_result' END AS ingestion_state,
       1::int AS attempt_count, NULL::timestamptz AS next_attempt_at, NULL::text AS failure_code
FROM youtube_issue_binding b
JOIN youtube_artifact a ON a.workspace_id=b.workspace_id AND a.project_id=b.project_id AND a.artifact_key=b.artifact_key
LEFT JOIN issue i ON i.id=b.issue_id AND i.workspace_id=b.workspace_id AND i.project_id=b.project_id
LEFT JOIN LATERAL (SELECT r.* FROM youtube_issue_result r WHERE r.workspace_id=b.workspace_id AND r.binding_id=b.id ORDER BY r.recorded_at DESC LIMIT 1) r ON true
LEFT JOIN youtube_artifact_version v ON v.artifact_id=a.id AND v.version_number=a.current_version_number
WHERE b.workspace_id=sqlc.arg(workspace_id) AND b.project_id=sqlc.arg(project_id) AND b.active
ORDER BY b.created_at ASC, b.id ASC;

-- name: ListYouTubeStudioVersions :many
SELECT v.id, v.artifact_id, v.version_number, v.sha256, v.recorded_at, v.source_result_id, v.source_issue_id, v.source_task_id
FROM youtube_artifact_version v JOIN youtube_artifact a ON a.id=v.artifact_id AND a.workspace_id=v.workspace_id
WHERE v.workspace_id=sqlc.arg(workspace_id) AND v.artifact_id=sqlc.arg(artifact_id)
  AND (sqlc.narg('before_version')::int IS NULL OR v.version_number < sqlc.narg('before_version')::int)
ORDER BY v.version_number DESC LIMIT sqlc.arg(row_limit);

-- name: GetYouTubeStudioVersion :one
SELECT v.id, v.artifact_id, v.version_number, v.markdown, v.sha256, v.recorded_at,
       v.source_result_id, v.source_issue_id, v.source_task_id, a.project_id
FROM youtube_artifact_version v JOIN youtube_artifact a ON a.id=v.artifact_id AND a.workspace_id=v.workspace_id
WHERE v.workspace_id=sqlc.arg(workspace_id) AND a.project_id=sqlc.arg(project_id) AND v.artifact_id=sqlc.arg(artifact_id) AND v.id=sqlc.arg(version_id);

-- name: GetYouTubeStudioProducer :one
SELECT a.id, a.name FROM agent_task_queue t JOIN agent a ON a.id=t.agent_id AND a.workspace_id=sqlc.arg(workspace_id)
WHERE t.id=sqlc.arg(task_id) AND t.workspace_id=sqlc.arg(workspace_id);
