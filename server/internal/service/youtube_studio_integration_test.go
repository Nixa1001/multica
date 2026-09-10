package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/youtubestudio"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// These tests intentionally require an explicit DATABASE_URL. The repository's
// default localhost:5432 is owned by another application on many developer
// machines and must never be used as an implicit integration-test target.
func youtubeStudioPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is required for YouTube Studio PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	var ready bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('youtube_studio_outbox') IS NOT NULL`).Scan(&ready); err != nil {
		pool.Close()
		t.Fatalf("check YouTube Studio schema: %v", err)
	}
	if !ready {
		pool.Close()
		t.Skip("YouTube Studio migrations are not applied")
	}
	t.Cleanup(pool.Close)
	return pool
}

type youtubeFixture struct {
	userID      pgtype.UUID
	workspaceID pgtype.UUID
	otherWS     pgtype.UUID
	projectID   pgtype.UUID
	otherProj   pgtype.UUID
	runtimeID   pgtype.UUID
	agentID     pgtype.UUID
	issueID     pgtype.UUID
	otherIssue  pgtype.UUID
}

func youtubeFixtureSeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) youtubeFixture {
	t.Helper()
	var f youtubeFixture
	name := fmt.Sprintf("youtube-studio-%d", time.Now().UnixNano())
	f.userID = youtubeMustUUID(t, pool, ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, "YouTube Studio integration", name+"@example.test")
	f.workspaceID = youtubeMustUUID(t, pool, ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`, name, name)
	f.otherWS = youtubeMustUUID(t, pool, ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`, name+" other", name+"-other")
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, f.workspaceID, f.userID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, f.otherWS, f.userID); err != nil {
		t.Fatalf("seed other member: %v", err)
	}
	f.projectID = youtubeMustUUID(t, pool, ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, 'Video') RETURNING id`, f.workspaceID)
	f.otherProj = youtubeMustUUID(t, pool, ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, 'Other video') RETURNING id`, f.otherWS)
	f.runtimeID = youtubeMustUUID(t, pool, ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id) VALUES ($1, NULL, 'YouTube test runtime', 'cloud', 'youtube_test', 'online', 'integration test', '{}'::jsonb, now(), 'private', $2) RETURNING id`, f.workspaceID, f.userID)
	f.agentID = youtubeMustUUID(t, pool, ctx, `INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id) VALUES ($1, 'Markdown agent', 'cloud', $2, $3) RETURNING id`, f.workspaceID, f.runtimeID, f.userID)
	baseNumber := int(time.Now().UnixNano() % 1000000000)
	f.issueID = youtubeMustUUID(t, pool, ctx, `INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number) VALUES ($1, $2, 'Brief', 'in_progress', 'member', $3, $4) RETURNING id`, f.workspaceID, f.projectID, f.userID, baseNumber)
	f.otherIssue = youtubeMustUUID(t, pool, ctx, `INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number) VALUES ($1, $2, 'Other brief', 'in_progress', 'member', $3, $4) RETURNING id`, f.otherWS, f.otherProj, f.userID, baseNumber)
	t.Cleanup(func() {
		// The fixture owns these rows; application cleanup is tested separately
		// below, so the final cleanup is deliberately scoped by generated ids.
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = ANY($1::uuid[])`, []pgtype.UUID{f.workspaceID, f.otherWS})
	})
	return f
}

func youtubeMustUUID(t *testing.T, pool *pgxpool.Pool, ctx context.Context, query string, args ...any) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		t.Fatalf("seed UUID with %q: %v", query, err)
	}
	return id
}

func youtubeBind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, q *db.Queries, f youtubeFixture, issueID pgtype.UUID, key string) {
	t.Helper()
	// Keep fixture setup independent from the bootstrap CTE's materialization
	// semantics; the service contract under test starts at task completion.
	if _, err := pool.Exec(ctx, `INSERT INTO youtube_video_project (workspace_id, project_id) VALUES ($1, $2) ON CONFLICT (project_id) DO NOTHING`, f.workspaceID, f.projectID); err != nil {
		t.Fatalf("seed video profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO youtube_artifact (workspace_id, project_id, artifact_key) VALUES ($1, $2, $3) ON CONFLICT (workspace_id, project_id, artifact_key) DO NOTHING`, f.workspaceID, f.projectID, key); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if err := q.BootstrapYouTubeMarkdownBinding(ctx, db.BootstrapYouTubeMarkdownBindingParams{
		WorkspaceID: f.workspaceID, ProjectID: f.projectID, ArtifactKey: key, ID: issueID,
	}); err != nil {
		t.Fatalf("bootstrap binding: %v", err)
	}
}

func TestYouTubeStudioBootstrapCreatesCleanProfileArtifactAndBinding(t *testing.T) {
	pool := youtubeStudioPool(t)
	ctx := context.Background()
	f := youtubeFixtureSeed(t, ctx, pool)
	q := db.New(pool)
	if err := q.BootstrapYouTubeMarkdownBinding(ctx, db.BootstrapYouTubeMarkdownBindingParams{
		WorkspaceID: f.workspaceID, ProjectID: f.projectID, ArtifactKey: "brief", ID: f.issueID,
	}); err != nil {
		t.Fatalf("clean bootstrap: %v", err)
	}
	var profiles, artifacts, bindings int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_video_project WHERE workspace_id = $1 AND project_id = $2`, f.workspaceID, f.projectID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_artifact WHERE workspace_id = $1 AND project_id = $2 AND artifact_key = 'brief'`, f.workspaceID, f.projectID).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_issue_binding WHERE workspace_id = $1 AND project_id = $2 AND issue_id = $3`, f.workspaceID, f.projectID, f.issueID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || artifacts != 1 || bindings != 1 {
		t.Fatalf("clean bootstrap counts = profile %d/artifact %d/binding %d, want 1/1/1", profiles, artifacts, bindings)
	}
}

func youtubeTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f youtubeFixture, issueID pgtype.UUID) pgtype.UUID {
	t.Helper()
	return youtubeMustUUID(t, pool, ctx, `INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, started_at) VALUES ($1, $2, $3, 'running', now()) RETURNING id`, f.agentID, f.runtimeID, issueID)
}

func youtubeResult(t *testing.T, output string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"output": output})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestYouTubeStudioMarkdownIngestionAndProjection(t *testing.T) {
	pool := youtubeStudioPool(t)
	ctx := context.Background()
	f := youtubeFixtureSeed(t, ctx, pool)
	q := db.New(pool)
	youtubeBind(t, ctx, pool, q, f, f.issueID, "brief")
	taskID := youtubeTask(t, ctx, pool, f, f.issueID)
	markdown := "# provenance\n\nThe source task owns this exact text."
	var bindings, profiles, artifacts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_issue_binding WHERE workspace_id = $1 AND project_id = $2 AND issue_id = $3`, f.workspaceID, f.projectID, f.issueID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_video_project WHERE workspace_id = $1 AND project_id = $2`, f.workspaceID, f.projectID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_artifact WHERE workspace_id = $1 AND project_id = $2 AND artifact_key = 'brief'`, f.workspaceID, f.projectID).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if bindings != 1 || profiles != 1 || artifacts != 1 {
		t.Fatalf("bootstrap counts = binding %d/profile %d/artifact %d, want 1/1/1", bindings, profiles, artifacts)
	}

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if _, err := svc.CompleteTask(ctx, taskID, youtubeResult(t, markdown), "", "", "", false, "", ""); err != nil {
		t.Fatalf("complete markdown task: %v", err)
	}
	// A replay of the terminal callback is a no-op and cannot create another
	// result, even though the callback body is identical.
	if _, err := svc.CompleteTask(ctx, taskID, youtubeResult(t, markdown), "", "", "", false, "", ""); err != nil {
		t.Fatalf("replay completed task: %v", err)
	}

	var resultCount, outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_issue_result WHERE source_task_id = $1`, taskID).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_studio_outbox WHERE workspace_id = $1 AND result_id IN (SELECT id FROM youtube_issue_result WHERE source_task_id = $2)`, f.workspaceID, taskID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 1 || outboxCount != 1 {
		t.Fatalf("replay counts = result %d/outbox %d, want 1/1", resultCount, outboxCount)
	}

	worker := &youtubestudio.Worker{Queries: q, TxStarter: pool, MaxAttempts: 3}
	for i := 0; i < 3; i++ {
		if err := worker.ReconcileOnce(ctx); err != nil {
			t.Fatalf("project outbox: %v", err)
		}
	}
	var version, current int
	if err := pool.QueryRow(ctx, `SELECT version_number FROM youtube_artifact_version v JOIN youtube_artifact a ON a.id = v.artifact_id WHERE v.source_task_id = $1`, taskID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT current_version_number FROM youtube_artifact WHERE workspace_id = $1 AND project_id = $2 AND artifact_key = 'brief'`, f.workspaceID, f.projectID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if version != 1 || current != 1 {
		t.Fatalf("projected version/current = %d/%d, want 1/1", version, current)
	}

	var gotMarkdown, gotHash string
	if err := pool.QueryRow(ctx, `SELECT markdown, sha256 FROM youtube_issue_result WHERE source_task_id = $1`, taskID).Scan(&gotMarkdown, &gotHash); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(markdown))
	if gotMarkdown != markdown || gotHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("provenance/hash mismatch: markdown=%q sha=%q", gotMarkdown, gotHash)
	}
}

func TestYouTubeStudioConcurrentVersionsAreMonotoneAndImmutable(t *testing.T) {
	pool := youtubeStudioPool(t)
	ctx := context.Background()
	f := youtubeFixtureSeed(t, ctx, pool)
	q := db.New(pool)
	const taskCount = 6
	taskIDs := make([]pgtype.UUID, taskCount)
	for i := range taskIDs {
		issueID := youtubeMustUUID(t, pool, ctx, `INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number) VALUES ($1, $2, $3, 'in_progress', 'member', $4, $5) RETURNING id`, f.workspaceID, f.projectID, fmt.Sprintf("parallel brief %d", i), f.userID, 200000000+i)
		youtubeBind(t, ctx, pool, q, f, issueID, "brief")
		taskIDs[i] = youtubeTask(t, ctx, pool, f, issueID)
	}

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	var wg sync.WaitGroup
	for i, taskID := range taskIDs {
		wg.Add(1)
		go func(i int, taskID pgtype.UUID) {
			defer wg.Done()
			if _, err := svc.CompleteTask(ctx, taskID, youtubeResult(t, fmt.Sprintf("revision %d", i)), "", "", "", false, "", ""); err != nil {
				t.Errorf("complete task %d: %v", i, err)
			}
		}(i, taskID)
	}
	wg.Wait()
	worker := &youtubestudio.Worker{Queries: q, TxStarter: pool, MaxAttempts: 3}
	for i := 0; i < 3; i++ {
		if err := worker.ReconcileOnce(ctx); err != nil {
			t.Fatalf("project concurrent outbox: %v", err)
		}
	}
	var count, current int
	if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(max(version_number), 0) FROM youtube_artifact_version v JOIN youtube_artifact a ON a.id = v.artifact_id WHERE a.workspace_id = $1 AND a.project_id = $2`, f.workspaceID, f.projectID).Scan(&count, &current); err != nil {
		t.Fatal(err)
	}
	if count != taskCount || current != taskCount {
		t.Fatalf("versions count/max = %d/%d, want %d/%d", count, current, taskCount, taskCount)
	}
	var immutable int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_artifact_version WHERE workspace_id = $1 AND version_number <= 0`, f.workspaceID).Scan(&immutable); err != nil {
		t.Fatal(err)
	}
	if immutable != 0 {
		t.Fatalf("found %d invalid/non-monotone versions", immutable)
	}
}

func TestYouTubeStudioRejectsOutOfScopeResultsAndCleansPendingOutbox(t *testing.T) {
	pool := youtubeStudioPool(t)
	ctx := context.Background()
	f := youtubeFixtureSeed(t, ctx, pool)
	q := db.New(pool)
	// Wrong workspace/project/parent combinations cannot produce a result.
	wrongTask := youtubeTask(t, ctx, pool, f, f.otherIssue)
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if _, err := svc.CompleteTask(ctx, wrongTask, youtubeResult(t, "must not ingest"), "", "", "", false, "", ""); err != nil {
		t.Fatalf("complete unbound task: %v", err)
	}
	var results int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM youtube_issue_result WHERE source_task_id = $1`, wrongTask).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if results != 0 {
		t.Fatalf("out-of-scope task produced %d result rows", results)
	}
	var issueStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, f.otherIssue).Scan(&issueStatus); err != nil {
		t.Fatal(err)
	}
	if issueStatus != "in_progress" {
		t.Fatalf("unbound completion changed issue status to %q", issueStatus)
	}

	// Seed a pending event and exercise the same explicit project cleanup used
	// by DeleteProject. A second workspace is asserted to remain untouched.
	goodIssue := youtubeMustUUID(t, pool, ctx, `INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number) VALUES ($1, $2, 'cleanup issue', 'in_progress', 'member', $3, 300000000) RETURNING id`, f.workspaceID, f.projectID, f.userID)
	youtubeBind(t, ctx, pool, q, f, goodIssue, "cleanup")
	if _, err := pool.Exec(ctx, `INSERT INTO youtube_issue_result (event_id, workspace_id, project_id, binding_id, artifact_id, source_issue_id, source_task_id, markdown, sha256, recorded_at) SELECT gen_random_uuid(), $1, $2, b.id, a.id, $3, gen_random_uuid(), 'pending', repeat('a', 64), now() FROM youtube_issue_binding b JOIN youtube_artifact a USING (workspace_id, project_id, artifact_key) WHERE b.issue_id = $3`, f.workspaceID, f.projectID, goodIssue); err != nil {
		t.Fatalf("seed cleanup result: %v", err)
	}
	if err := q.DeleteYouTubeStudioByProject(ctx, db.DeleteYouTubeStudioByProjectParams{WorkspaceID: f.workspaceID, ProjectID: f.projectID}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteYouTubeStudioArtifactsByProject(ctx, db.DeleteYouTubeStudioArtifactsByProjectParams{WorkspaceID: f.workspaceID, ProjectID: f.projectID}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteYouTubeStudioResultsByProject(ctx, db.DeleteYouTubeStudioResultsByProjectParams{WorkspaceID: f.workspaceID, ProjectID: f.projectID}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteYouTubeStudioArtifactsProject(ctx, db.DeleteYouTubeStudioArtifactsProjectParams{WorkspaceID: f.workspaceID, ProjectID: f.projectID}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteYouTubeStudioBindingsByProject(ctx, db.DeleteYouTubeStudioBindingsByProjectParams{WorkspaceID: f.workspaceID, ProjectID: f.projectID}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteYouTubeStudioProfileByProject(ctx, db.DeleteYouTubeStudioProfileByProjectParams{WorkspaceID: f.workspaceID, ProjectID: f.projectID}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"youtube_studio_outbox", "youtube_issue_result", "youtube_artifact_version", "youtube_artifact", "youtube_issue_binding", "youtube_video_project"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE workspace_id = $1`, f.workspaceID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("project cleanup left %d rows in %s", n, table)
		}
	}
}

func TestYouTubeStudioLeaseRetryAndDeadLetterStateMachine(t *testing.T) {
	pool := youtubeStudioPool(t)
	ctx := context.Background()
	f := youtubeFixtureSeed(t, ctx, pool)
	eventID := youtubeMustUUID(t, pool, ctx, `INSERT INTO youtube_studio_outbox (event_id, workspace_id, result_id, event_kind, payload, next_attempt_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'IssueResultRecordedV1', '{}'::jsonb, now()) RETURNING event_id`, f.workspaceID)
	q := db.New(pool)
	event, err := q.ClaimYouTubeStudioEvent(ctx, pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true})
	if err != nil {
		t.Fatalf("claim initial event: %v", err)
	}
	if event.EventID != eventID || !event.LeaseToken.Valid {
		t.Fatalf("claim returned event %v with lease validity %v", event.EventID, event.LeaseToken.Valid)
	}
	if err := q.MarkYouTubeStudioEventFailed(ctx, db.MarkYouTubeStudioEventFailedParams{EventID: event.EventID, LeaseToken: event.LeaseToken, LastError: "transient projection failure", NextAttemptAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}); err != nil {
		t.Fatalf("mark retryable failure: %v", err)
	}
	retry, err := q.ClaimYouTubeStudioEvent(ctx, pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true})
	if err != nil {
		t.Fatalf("claim retry event: %v", err)
	}
	if err := q.MarkYouTubeStudioEventDeadLettered(ctx, db.MarkYouTubeStudioEventDeadLetteredParams{EventID: retry.EventID, LeaseToken: retry.LeaseToken, LastError: "permanent projection failure"}); err != nil {
		t.Fatalf("dead-letter event: %v", err)
	}
	var attempts int
	var dead bool
	if err := pool.QueryRow(ctx, `SELECT attempt_count, dead_lettered_at IS NOT NULL FROM youtube_studio_outbox WHERE event_id = $1`, eventID).Scan(&attempts, &dead); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !dead {
		t.Fatalf("lease state = attempts %d/dead %t, want 2/true", attempts, dead)
	}
}

func TestYouTubeStudioMigrationContract(t *testing.T) {
	pool := youtubeStudioPool(t)
	ctx := context.Background()
	for _, indexName := range []string{
		"youtube_video_project_workspace_project_idx", "youtube_issue_binding_active_issue_idx",
		"youtube_artifact_workspace_project_key_idx", "youtube_issue_result_binding_task_idx",
		"youtube_artifact_version_source_result_idx", "youtube_artifact_version_artifact_number_idx",
		"youtube_studio_outbox_due_idx",
	} {
		var found bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`, indexName).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("migration index %s is missing", indexName)
		}
	}
	_, file, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(file), "../../migrations")
	for n := 457; n <= 464; n++ {
		up, err := os.ReadFile(filepath.Join(migrationsDir, fmt.Sprintf("%03d_youtube_studio", n)))
		if err != nil {
			// Migration names contain descriptive suffixes; resolve the one file
			// without allowing a missing pair to silently pass.
			matches, globErr := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_youtube_studio*up.sql", n)))
			if globErr != nil || len(matches) != 1 {
				t.Fatalf("locate migration %d: %v", n, err)
			}
			up, err = os.ReadFile(matches[0])
		}
		if err != nil || len(up) == 0 {
			t.Fatalf("read migration %d up: %v", n, err)
		}
		if n >= 458 && !strings.Contains(string(up), "CONCURRENTLY") {
			t.Errorf("migration %d creates an index without CONCURRENTLY", n)
		}
		downMatches, globErr := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_youtube_studio*down.sql", n)))
		if globErr != nil || len(downMatches) != 1 {
			t.Errorf("migration %d has no unique down migration", n)
		}
	}
	if os.Getenv("YOUTUBE_STUDIO_MIGRATION_TEST") != "1" {
		t.Skip("set YOUTUBE_STUDIO_MIGRATION_TEST=1 on the dedicated database to execute up/down lifecycle")
	}
	// This is deliberately opt-in: DATABASE_URL is caller supplied and the
	// lifecycle drops only the objects introduced by migrations 457-464.
	for n := 464; n >= 457; n-- {
		matches, err := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_youtube_studio*down.sql", n)))
		if err != nil || len(matches) != 1 {
			t.Fatalf("locate migration %d down: %v", n, err)
		}
		sql, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("read migration %d down: %v", n, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %d down: %v", n, err)
		}
	}
	for n := 457; n <= 464; n++ {
		matches, err := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_youtube_studio*up.sql", n)))
		if err != nil || len(matches) != 1 {
			t.Fatalf("locate migration %d up: %v", n, err)
		}
		sql, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("read migration %d up: %v", n, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %d up: %v", n, err)
		}
	}
}
