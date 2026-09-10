package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type youtubeDeleteRaceFixture struct {
	projectID pgtype.UUID
	issueID   pgtype.UUID
	taskID    pgtype.UUID
}

func seedYouTubeDeleteRaceFixture(t *testing.T) youtubeDeleteRaceFixture {
	t.Helper()
	ctx := context.Background()
	var f youtubeDeleteRaceFixture
	var agentID, runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`, testWorkspaceID).Scan(&runtimeID); err != nil {
		t.Fatalf("load test runtime: %v", err)
	}
	agentIDText := createHandlerTestAgent(t, "StudioDeleteRaceAgent", []byte("[]"))
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent WHERE id = $1`, agentIDText).Scan(&agentID); err != nil {
		t.Fatalf("load test agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, 'Studio delete race') RETURNING id`, testWorkspaceID).Scan(&f.projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number) VALUES ($1, $2, 'Studio delete race issue', 'in_progress', 'member', $3, floor(extract(epoch from clock_timestamp()) * 1000000)::bigint % 2000000000) RETURNING id`, testWorkspaceID, f.projectID, testUserID).Scan(&f.issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO youtube_video_project (workspace_id, project_id) VALUES ($1, $2)`, testWorkspaceID, f.projectID); err != nil {
		t.Fatalf("seed Studio profile: %v", err)
	}
	var artifactID, bindingID pgtype.UUID
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact (workspace_id, project_id, artifact_key) VALUES ($1, $2, 'brief') RETURNING id`, testWorkspaceID, f.projectID).Scan(&artifactID); err != nil {
		t.Fatalf("seed Studio artifact: %v", err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_issue_binding (workspace_id, project_id, issue_id, artifact_key, kind) VALUES ($1, $2, $3, 'brief', 'markdown') RETURNING id`, testWorkspaceID, f.projectID, f.issueID).Scan(&bindingID); err != nil {
		t.Fatalf("seed Studio binding: %v", err)
	}
	_ = artifactID
	_ = bindingID
	if err := testPool.QueryRow(ctx, `INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, started_at) VALUES ($1, $2, $3, 'running', now()) RETURNING id`, agentID, runtimeID, f.issueID).Scan(&f.taskID); err != nil {
		t.Fatalf("seed completion task: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent_task_queue WHERE id = $1`, f.taskID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM issue WHERE id = $1`, f.issueID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM youtube_studio_outbox WHERE workspace_id = $1 AND result_id IN (SELECT id FROM youtube_issue_result WHERE project_id = $2)`, testWorkspaceID, f.projectID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM youtube_issue_result WHERE project_id = $1`, f.projectID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM youtube_artifact_version WHERE workspace_id = $1 AND artifact_id IN (SELECT id FROM youtube_artifact WHERE project_id = $2)`, testWorkspaceID, f.projectID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM youtube_artifact WHERE project_id = $1`, f.projectID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM youtube_issue_binding WHERE project_id = $1`, f.projectID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM youtube_video_project WHERE project_id = $1`, f.projectID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM project WHERE id = $1`, f.projectID)
	})
	return f
}

func assertNoYouTubeStudioProjectRows(t *testing.T, f youtubeDeleteRaceFixture) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"youtube_studio_outbox", "youtube_issue_result", "youtube_artifact_version", "youtube_artifact", "youtube_issue_binding", "youtube_video_project"} {
		var count int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE workspace_id = $1 AND (`+youtubeProjectPredicate(table)+`)`, testWorkspaceID, f.projectID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows for deleted project", table, count)
		}
	}
	var projectCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM project WHERE id = $1`, f.projectID).Scan(&projectCount); err != nil {
		t.Fatalf("count deleted project: %v", err)
	}
	if projectCount != 0 {
		t.Fatalf("project remained after DeleteProject: %d rows", projectCount)
	}
}

func youtubeProjectPredicate(table string) string {
	if table == "youtube_artifact_version" {
		return "artifact_id IN (SELECT id FROM youtube_artifact WHERE project_id = $2)"
	}
	if table == "youtube_studio_outbox" {
		return "result_id IN (SELECT id FROM youtube_issue_result WHERE project_id = $2)"
	}
	return "project_id = $2"
}

func runYouTubeDeleteRace(t *testing.T, f youtubeDeleteRaceFixture, deletionFirst bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completionResult, _ := json.Marshal(map[string]string{"output": "race markdown"})
	var blockerTx interface {
		Commit(context.Context) error
		Rollback(context.Context) error
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock-order fixture: %v", err)
	}
	blockerTx = tx
	lockMode := "FOR KEY SHARE"
	if deletionFirst {
		lockMode = "FOR UPDATE"
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM project WHERE id = $1 `+lockMode, f.projectID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("hold %s project lock: %v", lockMode, err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	startDelete := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startDelete
		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodDelete, "/api/projects/"+f.projectID.String(), nil), "id", f.projectID.String())
		testHandler.DeleteProject(w, req)
		if w.Code != http.StatusNoContent {
			errs <- &httpError{code: w.Code, body: w.Body.String()}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if deletionFirst {
			<-startDelete
		}
		_, err := testHandler.TaskService.CompleteTask(ctx, f.taskID, completionResult, "", "", "", false, "", "")
		if err != nil {
			errs <- err
		}
	}()
	close(startDelete)
	if err := blockerTx.Rollback(ctx); err != nil {
		t.Fatalf("release project lock: %v", err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent %s ordering: %v", map[bool]string{true: "deletion-first", false: "completion-first"}[deletionFirst], err)
	}
	assertNoYouTubeStudioProjectRows(t, f)
}

type httpError struct {
	code int
	body string
}

func (e *httpError) Error() string { return "unexpected DeleteProject status" }

func TestYouTubeStudioDeleteAndCompleteTaskConcurrentBothOrders(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Run("completion first is swept", func(t *testing.T) {
		f := seedYouTubeDeleteRaceFixture(t)
		runYouTubeDeleteRace(t, f, false)
	})
	t.Run("deletion first is a no-op", func(t *testing.T) {
		f := seedYouTubeDeleteRaceFixture(t)
		runYouTubeDeleteRace(t, f, true)
	})
}
