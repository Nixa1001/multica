package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type youtubeFaultRow struct{ err error }

func (r youtubeFaultRow) Scan(...any) error { return r.err }

type youtubeFaultTx struct {
	pgx.Tx
	fault string
}

func (tx youtubeFaultTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, tx.fault) {
		return youtubeFaultRow{err: errors.New("injected database failure")}
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

type youtubeFaultStarter struct {
	pool  *pgxpool.Pool
	fault string
}

func (s youtubeFaultStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return youtubeFaultTx{Tx: tx, fault: s.fault}, nil
}

type youtubeRowsError struct{ pgx.Rows }

func (youtubeRowsError) Err() error { return errors.New("injected rows failure") }

type youtubeRowsFaultDB struct{ dbExecutor }

func (db youtubeRowsFaultDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := db.dbExecutor.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return youtubeRowsError{Rows: rows}, nil
}

func youtubeHandlerURL(r *http.Request, video, issue string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("video_id", video)
	ctx.URLParams.Add("issue_id", issue)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}

func youtubeVersionURL(r *http.Request, video, artifact string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("video_id", video)
	ctx.URLParams.Add("artifact_id", artifact)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}

func youtubeVersionDetailURL(r *http.Request, video, artifact, version string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("video_id", video)
	ctx.URLParams.Add("artifact_id", artifact)
	ctx.URLParams.Add("version_id", version)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}

func TestYouTubeStudioBindRetryAndFirstRead(t *testing.T) {
	ctx := context.Background()
	var project, issue string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP Studio contract') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,'HTTP source','in_progress','member',$3,900000001) RETURNING id`, testWorkspaceID, project, testUserID).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issue)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
	})
	type result struct {
		status int
		body   map[string]any
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := youtubeHandlerURL(httptest.NewRequest(http.MethodPut, "/api/youtube-studio/videos/"+project+"/markdown-bindings/"+issue+"?workspace_id="+testWorkspaceID, nil), project, issue)
			w := httptest.NewRecorder()
			testHandler.YouTubeStudioBind(w, req)
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			results <- result{w.Code, body}
		}()
	}
	wg.Wait()
	close(results)
	var created, okCount int
	var bindingID, artifactID string
	for got := range results {
		if got.status == http.StatusCreated {
			created++
		}
		if got.status == http.StatusOK {
			okCount++
		}
		binding, _ := got.body["binding"].(map[string]any)
		if bindingID == "" {
			bindingID, _ = binding["id"].(string)
			artifactID, _ = binding["artifact_id"].(string)
		} else {
			otherID, _ := binding["id"].(string)
			otherArtifact, _ := binding["artifact_id"].(string)
			if otherID != bindingID || otherArtifact != artifactID {
				t.Fatalf("concurrent retry returned different IDs")
			}
		}
	}
	if created != 1 || okCount != 1 {
		t.Fatalf("concurrent statuses created=%d ok=%d want 1/1", created, okCount)
	}
	// A subsequent exact retry remains 200 and preserves the same binding.
	req := youtubeHandlerURL(httptest.NewRequest(http.MethodPut, "/api/youtube-studio/videos/"+project+"/markdown-bindings/"+issue+"?workspace_id="+testWorkspaceID, nil), project, issue)
	w := httptest.NewRecorder()
	testHandler.YouTubeStudioBind(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sequential retry status=%d want 200", w.Code)
	}
	var profiles, artifacts, bindings int
	if err := testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM youtube_video_project WHERE project_id=$1),(SELECT count(*) FROM youtube_artifact WHERE project_id=$1),(SELECT count(*) FROM youtube_issue_binding WHERE issue_id=$2)`, project, issue).Scan(&profiles, &artifacts, &bindings); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || artifacts != 1 || bindings != 1 {
		t.Fatalf("rows=%d/%d/%d want 1/1/1", profiles, artifacts, bindings)
	}
	w = httptest.NewRecorder()
	testHandler.YouTubeStudioVideo(w, youtubeHandlerURL(httptest.NewRequest(http.MethodGet, "/api/youtube-studio/videos/"+project+"?workspace_id="+testWorkspaceID, nil), project, issue))
	if w.Code != http.StatusOK {
		t.Fatalf("detail status=%d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Materials []struct {
			Ingestion struct {
				State string `json:"state"`
			} `json:"ingestion"`
		} `json:"materials"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Materials) != 1 || body.Materials[0].Ingestion.State != "awaiting_result" {
		t.Fatalf("unexpected first state: %s", w.Body.String())
	}
}

func TestYouTubeStudioVideoHTTPIngestionStateMatrix(t *testing.T) {
	ctx := context.Background()
	var project string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP ingestion matrix') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	keys := []string{"awaiting", "processing", "retrying", "ready", "failed", "retained"}
	want := map[string]string{"awaiting": "awaiting_result", "processing": "processing", "retrying": "retrying", "ready": "ready", "failed": "failed", "retained": "processing"}
	artifacts := make(map[string]string, len(keys))
	for i, key := range keys {
		var issue, binding, artifact string
		if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,$3,'in_progress','member',$4,$5) RETURNING id`, testWorkspaceID, project, "HTTP "+key, testUserID, 910000000+i).Scan(&issue); err != nil {
			t.Fatal(err)
		}
		if err := testPool.QueryRow(ctx, `INSERT INTO youtube_issue_binding(workspace_id,project_id,issue_id,artifact_key,kind) VALUES($1,$2,$3,$4,'markdown') RETURNING id`, testWorkspaceID, project, issue, key).Scan(&binding); err != nil {
			t.Fatal(err)
		}
		if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact(workspace_id,project_id,artifact_key) VALUES($1,$2,$3) RETURNING id`, testWorkspaceID, project, key).Scan(&artifact); err != nil {
			t.Fatal(err)
		}
		artifacts[key] = artifact
		if key == "awaiting" {
			continue
		}
		var oldResult string
		if key == "retained" {
			if err := testPool.QueryRow(ctx, `INSERT INTO youtube_issue_result(id,event_id,workspace_id,project_id,binding_id,artifact_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) VALUES('00000000-0000-0000-0000-000000000001',gen_random_uuid(),$1,$2,$3,$4,$5,gen_random_uuid(),'old retained markdown',repeat('a',64),'2025-01-01T00:00:00Z') RETURNING id`, testWorkspaceID, project, binding, artifact, issue).Scan(&oldResult); err != nil {
				t.Fatal(err)
			}
			if _, err := testPool.Exec(ctx, `INSERT INTO youtube_artifact_version(workspace_id,artifact_id,version_number,source_result_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) SELECT $1,$2,1,id,$3,source_task_id,markdown,sha256,recorded_at FROM youtube_issue_result WHERE id=$4`, testWorkspaceID, artifact, issue, oldResult); err != nil {
				t.Fatal(err)
			}
			if _, err := testPool.Exec(ctx, `UPDATE youtube_artifact SET current_version_number=1 WHERE id=$1`, artifact); err != nil {
				t.Fatal(err)
			}
		}
		var result string
		resultSQL := `INSERT INTO youtube_issue_result(event_id,workspace_id,project_id,binding_id,artifact_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,gen_random_uuid(),$6,repeat('b',64),'2025-01-01T00:00:00Z') RETURNING id`
		if key == "retained" {
			resultSQL = `INSERT INTO youtube_issue_result(id,event_id,workspace_id,project_id,binding_id,artifact_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) VALUES('00000000-0000-0000-0000-000000000002',gen_random_uuid(),$1,$2,$3,$4,$5,gen_random_uuid(),$6,repeat('b',64),'2025-01-01T00:00:00Z') RETURNING id`
		}
		if err := testPool.QueryRow(ctx, resultSQL, testWorkspaceID, project, binding, artifact, issue, key+" markdown").Scan(&result); err != nil {
			t.Fatal(err)
		}
		dead := "NULL"
		attempts := 1
		if key == "retrying" {
			attempts = 2
		}
		if key == "failed" {
			dead = "now()"
		}
		if _, err := testPool.Exec(ctx, `INSERT INTO youtube_studio_outbox(event_id,workspace_id,result_id,event_kind,payload,attempt_count,dead_lettered_at,last_error) VALUES(gen_random_uuid(),$1,$2,'IssueResultRecordedV1','{}'::jsonb,$3,`+dead+`,'internal detail must not escape')`, testWorkspaceID, result, attempts); err != nil {
			t.Fatal(err)
		}
		if key == "ready" {
			if _, err := testPool.Exec(ctx, `INSERT INTO youtube_artifact_version(workspace_id,artifact_id,version_number,source_result_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) SELECT $1,$2,1,id,$3,source_task_id,markdown,sha256,recorded_at FROM youtube_issue_result WHERE id=$4`, testWorkspaceID, artifact, issue, result); err != nil {
				t.Fatal(err)
			}
			if _, err := testPool.Exec(ctx, `UPDATE youtube_artifact SET current_version_number=1 WHERE id=$1`, artifact); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_studio_outbox WHERE workspace_id=$1 AND result_id IN (SELECT id FROM youtube_issue_result WHERE project_id=$2)`, testWorkspaceID, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_issue_result WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact_version WHERE artifact_id IN (SELECT id FROM youtube_artifact WHERE project_id=$1)`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_issue_binding WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_video_project WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
	})
	if _, err := testPool.Exec(ctx, `INSERT INTO youtube_video_project(workspace_id,project_id) VALUES($1,$2)`, testWorkspaceID, project); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/youtube-studio/videos/"+project+"?workspace_id="+testWorkspaceID, nil)
	req = youtubeHandlerURL(req, project, "00000000-0000-0000-0000-000000000001")
	testHandler.YouTubeStudioVideo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	if string(w.Body.Bytes()) == "" || bytes.Contains(w.Body.Bytes(), []byte("last_error")) || bytes.Contains(w.Body.Bytes(), []byte("internal detail")) {
		t.Fatalf("raw failure detail escaped: %s", w.Body.String())
	}
	var body struct {
		Materials []struct {
			ArtifactID string `json:"artifact_id"`
			Current    *struct {
				Version int `json:"version_number"`
			} `json:"current_version"`
			Ingestion struct {
				State       string `json:"state"`
				Attempt     int    `json:"attempt_count"`
				FailureCode any    `json:"failure_code"`
			} `json:"ingestion"`
		} `json:"materials"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, material := range body.Materials {
		for key, artifact := range artifacts {
			if artifact == material.ArtifactID {
				seen[key] = true
				if material.Ingestion.State != want[key] {
					t.Errorf("%s state=%s want %s", key, material.Ingestion.State, want[key])
				}
				if key == "retrying" && material.Ingestion.Attempt != 2 {
					t.Errorf("retry attempts=%d want 2", material.Ingestion.Attempt)
				}
				if key == "failed" && material.Ingestion.FailureCode != "projection_failed" {
					t.Errorf("failed code=%v want projection_failed", material.Ingestion.FailureCode)
				}
				if key == "retained" && (material.Current == nil || material.Current.Version != 1) {
					t.Errorf("retained current version=%v want 1", material.Current)
				}
			}
		}
	}
	if len(seen) != len(keys) {
		t.Fatalf("state matrix seen=%v materials=%d", seen, len(body.Materials))
	}
}

func TestYouTubeStudioVersionsHTTPImmutableProvenanceAndPagination(t *testing.T) {
	ctx := context.Background()
	var project, issue, artifact, taskN, taskN1 string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP immutable provenance') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,'immutable source','completed','member',$3,920000001) RETURNING id`, testWorkspaceID, project, testUserID).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	producerN := createHandlerTestAgent(t, "HTTP Producer N", []byte("[]"))
	producerN1 := createHandlerTestAgent(t, "HTTP Producer N+1", []byte("[]"))
	if err := testPool.QueryRow(ctx, `INSERT INTO agent_task_queue(agent_id,runtime_id,issue_id,status,completed_at) VALUES($1,$2,$3,'completed',now()) RETURNING id`, producerN, testRuntimeID, issue).Scan(&taskN); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO agent_task_queue(agent_id,runtime_id,issue_id,status,completed_at) VALUES($1,$2,$3,'completed',now()) RETURNING id`, producerN1, testRuntimeID, issue).Scan(&taskN1); err != nil {
		t.Fatal(err)
	}
	var binding string
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_issue_binding(workspace_id,project_id,issue_id,artifact_key,kind) VALUES($1,$2,$3,'immutable','markdown') RETURNING id`, testWorkspaceID, project, issue).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact(workspace_id,project_id,artifact_key,current_version_number) VALUES($1,$2,'immutable',2) RETURNING id`, testWorkspaceID, project).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO youtube_issue_result(id,event_id,workspace_id,project_id,binding_id,artifact_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) VALUES('00000000-0000-0000-0000-000000000011',gen_random_uuid(),$1,$2,$3,$4,$5,$6,'immutable N markdown',repeat('1',64),'2025-02-01T00:00:00Z'),('00000000-0000-0000-0000-000000000012',gen_random_uuid(),$1,$2,$3,$4,$5,$7,'immutable N+1 markdown',repeat('2',64),'2025-02-01T00:00:00Z')`, testWorkspaceID, project, binding, artifact, issue, taskN, taskN1); err != nil {
		t.Fatal(err)
	}
	var versionN, versionN1 string
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact_version(id,workspace_id,artifact_id,version_number,source_result_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) VALUES('00000000-0000-0000-0000-000000000021',$1,$2,1,'00000000-0000-0000-0000-000000000011',$3,$4,'immutable N markdown',repeat('1',64),'2025-02-01T00:00:00Z') RETURNING id`, testWorkspaceID, artifact, issue, taskN).Scan(&versionN); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact_version(id,workspace_id,artifact_id,version_number,source_result_id,source_issue_id,source_task_id,markdown,sha256,recorded_at) VALUES('00000000-0000-0000-0000-000000000022',$1,$2,2,'00000000-0000-0000-0000-000000000012',$3,$4,'immutable N+1 markdown',repeat('2',64),'2025-02-01T00:00:00Z') RETURNING id`, testWorkspaceID, artifact, issue, taskN1).Scan(&versionN1); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO youtube_video_project(workspace_id,project_id) VALUES($1,$2)`, testWorkspaceID, project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_studio_outbox WHERE result_id IN (SELECT id FROM youtube_issue_result WHERE project_id=$1)`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_issue_result WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact_version WHERE artifact_id=$1`, artifact)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact WHERE id=$1`, artifact)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_issue_binding WHERE id=$1`, binding)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_video_project WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id=$1`, issue)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issue)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id IN ($1,$2)`, producerN, producerN1)
	})
	list := func(before string) map[string]any {
		path := "/api/youtube-studio/videos/" + project + "/artifacts/" + artifact + "/versions?workspace_id=" + testWorkspaceID + "&limit=1"
		if before != "" {
			path += "&before_version=" + before
		}
		w := httptest.NewRecorder()
		testHandler.YouTubeStudioVersions(w, youtubeVersionURL(httptest.NewRequest(http.MethodGet, path, nil), project, artifact))
		if w.Code != http.StatusOK {
			t.Fatalf("list status=%d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	first := list("")
	firstItem := first["versions"].([]any)[0].(map[string]any)
	if firstItem["id"] != versionN1 || firstItem["version_number"].(float64) != 2 || first["next_before_version"].(float64) != 2 {
		t.Fatalf("first versions page=%v", first)
	}
	second := list("2")
	secondItem := second["versions"].([]any)[0].(map[string]any)
	if secondItem["id"] != versionN || secondItem["version_number"].(float64) != 1 || second["next_before_version"].(float64) != 1 {
		t.Fatalf("second versions page=%v", second)
	}
	third := list("1")
	if len(third["versions"].([]any)) != 0 || third["next_before_version"] != nil {
		t.Fatalf("empty versions page=%v", third)
	}
	checkDetail := func(version, markdown, hash, result, task, producerID, producer string) {
		path := "/api/youtube-studio/videos/" + project + "/artifacts/" + artifact + "/versions/" + version + "?workspace_id=" + testWorkspaceID
		w := httptest.NewRecorder()
		testHandler.YouTubeStudioVersion(w, youtubeVersionDetailURL(httptest.NewRequest(http.MethodGet, path, nil), project, artifact, version))
		if w.Code != http.StatusOK {
			t.Fatalf("detail status=%d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Markdown   string `json:"markdown"`
			Hash       string `json:"sha256"`
			Provenance struct {
				Result   string `json:"source_result_id"`
				Issue    string `json:"source_issue_id"`
				Task     string `json:"source_task_id"`
				Producer *struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"producer"`
			} `json:"provenance"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		producerOK := producer == "" && body.Provenance.Producer == nil
		if producer != "" {
			producerOK = body.Provenance.Producer != nil && body.Provenance.Producer.Type == "agent" && body.Provenance.Producer.ID == producerID && body.Provenance.Producer.Name == producer
		}
		if body.Markdown != markdown || body.Hash != hash || body.Provenance.Result != result || body.Provenance.Issue != issue || body.Provenance.Task != task || !producerOK {
			t.Fatalf("version %s body=%s", version, w.Body.String())
		}
	}
	checkDetail(versionN, "immutable N markdown", string(repeatByte('1', 64)), "00000000-0000-0000-0000-000000000011", taskN, producerN, "HTTP Producer N")
	checkDetail(versionN1, "immutable N+1 markdown", string(repeatByte('2', 64)), "00000000-0000-0000-0000-000000000012", taskN1, producerN1, "HTTP Producer N+1")
	if _, err := testPool.Exec(ctx, `DELETE FROM agent WHERE id=$1`, producerN1); err != nil {
		t.Fatal(err)
	}
	checkDetail(versionN1, "immutable N+1 markdown", string(repeatByte('2', 64)), "00000000-0000-0000-0000-000000000012", taskN1, "", "")
}

func TestYouTubeStudioBindWrongExistingArtifactKeyRollsBack(t *testing.T) {
	ctx := context.Background()
	var project, issue, artifact string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP binding conflict') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,'conflict source','in_progress','member',$3,930000001) RETURNING id`, testWorkspaceID, project, testUserID).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact(workspace_id,project_id,artifact_key) VALUES($1,$2,'wrong-key') RETURNING id`, testWorkspaceID, project).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO youtube_issue_binding(workspace_id,project_id,issue_id,artifact_key,kind) VALUES($1,$2,$3,'wrong-key','markdown')`, testWorkspaceID, project, issue); err != nil {
		t.Fatal(err)
	}
	var beforeProfiles, beforeArtifacts, beforeBindings int
	if err := testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM youtube_video_project WHERE project_id=$1),(SELECT count(*) FROM youtube_artifact WHERE project_id=$1),(SELECT count(*) FROM youtube_issue_binding WHERE project_id=$1)`, project).Scan(&beforeProfiles, &beforeArtifacts, &beforeBindings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_issue_binding WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_video_project WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issue)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
	})
	req := youtubeHandlerURL(httptest.NewRequest(http.MethodPut, "/api/youtube-studio/videos/"+project+"/markdown-bindings/"+issue+"?workspace_id="+testWorkspaceID, nil), project, issue)
	w := httptest.NewRecorder()
	testHandler.YouTubeStudioBind(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409: %s", w.Code, w.Body.String())
	}
	var profiles, artifacts, bindings int
	if err := testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM youtube_video_project WHERE project_id=$1),(SELECT count(*) FROM youtube_artifact WHERE project_id=$1),(SELECT count(*) FROM youtube_issue_binding WHERE project_id=$1)`, project).Scan(&profiles, &artifacts, &bindings); err != nil {
		t.Fatal(err)
	}
	if profiles != beforeProfiles || artifacts != beforeArtifacts || bindings != beforeBindings {
		t.Fatalf("conflict changed rows from %d/%d/%d to %d/%d/%d", beforeProfiles, beforeArtifacts, beforeBindings, profiles, artifacts, bindings)
	}
}

func TestYouTubeStudioBindDatabaseFaultsReturn500AndRollback(t *testing.T) {
	ctx := context.Background()
	var project, parent, child string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP injected faults') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,'parent','in_progress','member',$3,940000001) RETURNING id`, testWorkspaceID, project, testUserID).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,parent_issue_id,title,status,creator_type,creator_id,number) VALUES($1,$2,$3,'child','in_progress','member',$4,940000002) RETURNING id`, testWorkspaceID, project, parent, testUserID).Scan(&child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_issue_binding WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_video_project WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE project_id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
	})
	call := func(issueID, fault string) {
		clone := *testHandler
		clone.TxStarter = youtubeFaultStarter{pool: testPool, fault: fault}
		req := youtubeHandlerURL(httptest.NewRequest(http.MethodPut, "/api/youtube-studio/videos/"+project+"/markdown-bindings/"+issueID+"?workspace_id="+testWorkspaceID, nil), project, issueID)
		w := httptest.NewRecorder()
		clone.YouTubeStudioBind(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("fault %q status=%d want 500: %s", fault, w.Code, w.Body.String())
		}
	}
	call(child, "SELECT project_id,workspace_id,parent_issue_id")
	call(child, "SELECT project_id,workspace_id FROM issue")
	var profiles, artifacts, bindings int
	if err := testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM youtube_video_project WHERE project_id=$1),(SELECT count(*) FROM youtube_artifact WHERE project_id=$1),(SELECT count(*) FROM youtube_issue_binding WHERE project_id=$1)`, project).Scan(&profiles, &artifacts, &bindings); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || artifacts != 0 || bindings != 0 {
		t.Fatalf("fault rollback rows=%d/%d/%d", profiles, artifacts, bindings)
	}
}

func TestYouTubeStudioVideoRowsErrorReturns500WithoutPartialSuccess(t *testing.T) {
	clone := *testHandler
	clone.DB = youtubeRowsFaultDB{dbExecutor: testPool}
	req := httptest.NewRequest(http.MethodGet, "/api/youtube-studio/videos?workspace_id="+testWorkspaceID, nil)
	w := httptest.NewRecorder()
	clone.YouTubeStudioVideos(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500: %s", w.Code, w.Body.String())
	}
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestYouTubeStudioVersionsRejectsMismatchedArtifact(t *testing.T) {
	ctx := context.Background()
	var project, other, artifact string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP scope'),($1,'HTTP other') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	// Use an explicit second project so the artifact UUID is valid but foreign.
	if err := testPool.QueryRow(ctx, `SELECT id FROM project WHERE workspace_id=$1 AND title='HTTP other' ORDER BY created_at DESC LIMIT 1`, testWorkspaceID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_video_project(workspace_id,project_id) VALUES($1,$2) RETURNING project_id`, testWorkspaceID, other).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO youtube_artifact(workspace_id,project_id,artifact_key) VALUES($1,$2,'foreign') RETURNING id`, testWorkspaceID, other).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_artifact WHERE id=$1`, artifact)
		_, _ = testPool.Exec(ctx, `DELETE FROM youtube_video_project WHERE project_id=$1`, other)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id IN ($1,$2)`, project, other)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/youtube-studio/videos/"+project+"/artifacts/"+artifact+"/versions?workspace_id="+testWorkspaceID, nil)
	req = youtubeVersionURL(req, project, artifact)
	w := httptest.NewRecorder()
	testHandler.YouTubeStudioVersions(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404: %s", w.Code, w.Body.String())
	}
}

func TestYouTubeStudioBindRejectsForeignIssueWithoutWrites(t *testing.T) {
	ctx := context.Background()
	var project, foreignProject, issue string
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP target'),($1,'HTTP foreign') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT id FROM project WHERE workspace_id=$1 AND title='HTTP foreign' ORDER BY created_at DESC LIMIT 1`, testWorkspaceID).Scan(&foreignProject); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,'foreign source','in_progress','member',$3,900000002) RETURNING id`, testWorkspaceID, foreignProject, testUserID).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issue)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id IN ($1,$2)`, project, foreignProject)
	})
	req := youtubeHandlerURL(httptest.NewRequest(http.MethodPut, "/api/youtube-studio/videos/"+project+"/markdown-bindings/"+issue+"?workspace_id="+testWorkspaceID, nil), project, issue)
	w := httptest.NewRecorder()
	testHandler.YouTubeStudioBind(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404: %s", w.Code, w.Body.String())
	}
	var profiles, artifacts, bindings int
	if err := testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM youtube_video_project WHERE project_id=$1),(SELECT count(*) FROM youtube_artifact WHERE project_id=$1),(SELECT count(*) FROM youtube_issue_binding WHERE project_id=$1)`, project).Scan(&profiles, &artifacts, &bindings); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || artifacts != 0 || bindings != 0 {
		t.Fatalf("scope rejection wrote rows=%d/%d/%d", profiles, artifacts, bindings)
	}
}

func TestYouTubeStudioBindRejectsCrossWorkspaceIssueWithoutWrites(t *testing.T) {
	ctx := context.Background()
	var foreignWorkspace, project, issue string
	slug := "studio-cross-" + time.Now().Format("150405.000000")
	if err := testPool.QueryRow(ctx, `INSERT INTO workspace(name,slug,description,issue_prefix) VALUES('HTTP cross workspace',$1,'','CRS') RETURNING id`, slug).Scan(&foreignWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,'HTTP cross project') RETURNING id`, testWorkspaceID).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number) VALUES($1,$2,'cross source','in_progress','member',$3,900000003) RETURNING id`, foreignWorkspace, project, testUserID).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issue)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
		_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, foreignWorkspace)
	})
	req := youtubeHandlerURL(httptest.NewRequest(http.MethodPut, "/api/youtube-studio/videos/"+project+"/markdown-bindings/"+issue+"?workspace_id="+testWorkspaceID, nil), project, issue)
	w := httptest.NewRecorder()
	testHandler.YouTubeStudioBind(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404: %s", w.Code, w.Body.String())
	}
	var profiles, artifacts, bindings int
	if err := testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM youtube_video_project WHERE project_id=$1),(SELECT count(*) FROM youtube_artifact WHERE project_id=$1),(SELECT count(*) FROM youtube_issue_binding WHERE project_id=$1)`, project).Scan(&profiles, &artifacts, &bindings); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || artifacts != 0 || bindings != 0 {
		t.Fatalf("cross-workspace rejection wrote rows=%d/%d/%d", profiles, artifacts, bindings)
	}
}

func TestYouTubeStudioVideosPaginationHasStablePages(t *testing.T) {
	ctx := context.Background()
	var projects [4]string
	for i := range projects {
		if err := testPool.QueryRow(ctx, `INSERT INTO project(workspace_id,title) VALUES($1,$2) RETURNING id`, testWorkspaceID, "HTTP page "+string(rune('A'+i))).Scan(&projects[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := testPool.Exec(ctx, `INSERT INTO youtube_video_project(workspace_id,project_id,updated_at) VALUES($1,$2,now()+($3 * interval '1 second'))`, testWorkspaceID, projects[i], 3-i); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, project := range projects {
			_, _ = testPool.Exec(ctx, `DELETE FROM youtube_video_project WHERE project_id=$1`, project)
			_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1`, project)
		}
	})
	call := func(cursor string) (int, map[string]any) {
		path := "/api/youtube-studio/videos?workspace_id=" + testWorkspaceID + "&limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := httptest.NewRecorder()
		testHandler.YouTubeStudioVideos(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d want 200: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return w.Code, body
	}
	assertPage := func(body map[string]any, want string, next bool) {
		t.Helper()
		videos := body["videos"].([]any)
		if len(videos) != 1 || videos[0].(map[string]any)["video_id"] != want || (next && body["next_cursor"] == nil) || (!next && body["next_cursor"] != nil) {
			t.Fatalf("page=%v want id=%s next=%v", body, want, next)
		}
	}
	_, first := call("")
	assertPage(first, projects[0], true)
	_, second := call(first["next_cursor"].(string))
	assertPage(second, projects[1], true)
	_, third := call(second["next_cursor"].(string))
	assertPage(third, projects[2], true)
	_, fourth := call(third["next_cursor"].(string))
	assertPage(fourth, projects[3], true)
	_, end := call(fourth["next_cursor"].(string))
	if len(end["videos"].([]any)) != 0 || end["next_cursor"] != nil {
		t.Fatalf("empty end page=%v", end)
	}
}

func TestYouTubeStudioPaginationRejectsInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		h    func(http.ResponseWriter, *http.Request)
	}{
		{name: "video limit", path: "/api/youtube-studio/videos?workspace_id=" + testWorkspaceID + "&limit=0", h: testHandler.YouTubeStudioVideos},
		{name: "version cursor", path: "/api/youtube-studio/videos/00000000-0000-0000-0000-000000000001/artifacts/00000000-0000-0000-0000-000000000002/versions?workspace_id=" + testWorkspaceID + "&before_version=0", h: testHandler.YouTubeStudioVersions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.name == "version cursor" {
				req = youtubeVersionURL(req, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
			}
			w := httptest.NewRecorder()
			tc.h(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}
