package handler

// HTTP boundary for the first YouTube Studio slice.  The SQL is deliberately
// workspace-qualified at every hop; the PUT is transactional and holds the
// project key-share lock used by completion and deletion.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

type youtubeCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func youtubeCursorDecode(raw string) (youtubeCursor, error) {
	var c youtubeCursor
	if raw == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil || c.ID == "" || c.At.IsZero() {
		return c, fmt.Errorf("invalid cursor")
	}
	if _, err = util.ParseUUID(c.ID); err != nil {
		return c, fmt.Errorf("invalid cursor")
	}
	return c, nil
}
func youtubeCursorEncode(c youtubeCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func youtubeLimit(r *http.Request, fallback, max int) (int, bool) {
	n := fallback
	if s := r.URL.Query().Get("limit"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 || v > max {
			return 0, false
		}
		n = v
	}
	return n, true
}

func (h *Handler) YouTubeStudioBind(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ws := h.resolveWorkspaceID(r)
	workspaceID, ok := parseUUIDOrBadRequest(w, ws, "workspace_id")
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "video_id"), "video_id")
	if !ok {
		return
	}
	issueID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "issue_id"), "issue_id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeErrorCode(w, 500, "studio_bootstrap_failed", "Studio activation failed")
		return
	}
	defer tx.Rollback(ctx)
	var locked pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM project WHERE id=$1 AND workspace_id=$2 FOR KEY SHARE`, projectID, workspaceID).Scan(&locked); err != nil {
		writeErrorCode(w, 404, "studio_video_not_found", "Studio video not found")
		return
	}
	var issueProject pgtype.UUID
	var issueWorkspace pgtype.UUID
	var parent pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT project_id,workspace_id,parent_issue_id FROM issue WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID).Scan(&issueProject, &issueWorkspace, &parent)
	if err != nil || issueWorkspace != workspaceID || issueProject != projectID || (parent.Valid && func() bool {
		var p, ws pgtype.UUID
		e := tx.QueryRow(ctx, `SELECT project_id,workspace_id FROM issue WHERE id=$1`, parent).Scan(&p, &ws)
		return e != nil || p != projectID || ws != workspaceID
	}()) {
		writeErrorCode(w, 404, "studio_source_not_found", "Studio source not found")
		return
	}
	key := issueID.String()
	if _, err = tx.Exec(ctx, `INSERT INTO youtube_video_project(project_id,workspace_id) VALUES($1,$2) ON CONFLICT(project_id) DO NOTHING`, projectID, workspaceID); err != nil {
		writeErrorCode(w, 500, "studio_bootstrap_failed", "Studio activation failed")
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO youtube_artifact(workspace_id,project_id,artifact_key) VALUES($1,$2,$3) ON CONFLICT(workspace_id,project_id,artifact_key) DO NOTHING`, workspaceID, projectID, key); err != nil {
		writeErrorCode(w, 500, "studio_bootstrap_failed", "Studio activation failed")
		return
	}
	var id, artifactID pgtype.UUID
	var createdAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO youtube_issue_binding(workspace_id,project_id,issue_id,artifact_key,kind) VALUES($1,$2,$3,$4,'markdown') ON CONFLICT(workspace_id,issue_id) WHERE active DO NOTHING RETURNING id,created_at`, workspaceID, projectID, issueID, key).Scan(&id, &createdAt)
	created := err == nil
	if !created {
		err = tx.QueryRow(ctx, `SELECT b.id,b.created_at FROM youtube_issue_binding b WHERE b.workspace_id=$1 AND b.project_id=$2 AND b.issue_id=$3 AND b.active`, workspaceID, projectID, issueID).Scan(&id, &createdAt)
		if err != nil {
			writeErrorCode(w, 409, "studio_binding_conflict", "Studio binding conflicts with an existing binding")
			return
		}
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM youtube_artifact WHERE workspace_id=$1 AND project_id=$2 AND artifact_key=$3`, workspaceID, projectID, key).Scan(&artifactID); err != nil {
		writeErrorCode(w, 500, "studio_bootstrap_failed", "Studio activation failed")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeErrorCode(w, 500, "studio_bootstrap_failed", "Studio activation failed")
		return
	}
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], map[string]any{"created": created, "video_id": uuidToString(projectID), "binding": map[string]any{"id": uuidToString(id), "issue_id": uuidToString(issueID), "artifact_id": uuidToString(artifactID), "kind": "markdown", "active": true, "created_at": createdAt.UTC().Format(time.RFC3339Nano)}})
}

func (h *Handler) YouTubeStudioVideos(w http.ResponseWriter, r *http.Request) {
	ws := h.resolveWorkspaceID(r)
	wid, ok := parseUUIDOrBadRequest(w, ws, "workspace_id")
	if !ok {
		return
	}
	limit, ok := youtubeLimit(r, 50, 100)
	if !ok {
		writeErrorCode(w, 400, "invalid_pagination", "Invalid pagination")
		return
	}
	c, e := youtubeCursorDecode(r.URL.Query().Get("cursor"))
	if e != nil {
		writeErrorCode(w, 400, "invalid_pagination", "Invalid pagination")
		return
	}
	var rows pgx.Rows
	rows, e = h.DB.Query(r.Context(), `SELECT v.project_id,p.title,p.icon,v.lifecycle_state,count(b.id)::bigint,0::bigint,v.updated_at FROM youtube_video_project v JOIN project p ON p.id=v.project_id AND p.workspace_id=v.workspace_id LEFT JOIN youtube_issue_binding b ON b.workspace_id=v.workspace_id AND b.project_id=v.project_id AND b.active WHERE v.workspace_id=$1 AND ($2::timestamptz IS NULL OR (v.updated_at,v.project_id)<($2,$3)) GROUP BY v.project_id,p.title,p.icon,v.lifecycle_state,v.updated_at ORDER BY v.updated_at DESC,v.project_id DESC LIMIT $4`, wid, nullableTime(c.At), nullableUUID(c.ID), limit)
	if e != nil {
		writeErrorCode(w, 500, "studio_read_failed", "Studio read failed")
		return
	}
	defer rows.Close()
	videos := []map[string]any{}
	var last youtubeCursor
	for rows.Next() {
		var id pgtype.UUID
		var name, icon, state string
		var mc, ac int64
		var at time.Time
		if e = rows.Scan(&id, &name, &icon, &state, &mc, &ac, &at); e != nil {
			writeErrorCode(w, 500, "studio_read_failed", "Studio read failed")
			return
		}
		videos = append(videos, map[string]any{"video_id": uuidToString(id), "name": name, "icon": nilIfEmpty(icon), "lifecycle_state": state, "material_count": mc, "attention_count": ac, "updated_at": at.UTC().Format(time.RFC3339Nano)})
		last = youtubeCursor{At: at, ID: uuidToString(id)}
	}
	next := any(nil)
	if len(videos) == limit {
		next = youtubeCursorEncode(last)
	}
	writeJSON(w, 200, map[string]any{"videos": videos, "next_cursor": next})
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return parseUUID(s)
}
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (h *Handler) YouTubeStudioVideo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wid, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	vid, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "video_id"), "video_id")
	if !ok {
		return
	}
	var name, state string
	if e := h.DB.QueryRow(ctx, `SELECT p.title,v.lifecycle_state FROM youtube_video_project v JOIN project p ON p.id=v.project_id AND p.workspace_id=v.workspace_id WHERE v.workspace_id=$1 AND v.project_id=$2`, wid, vid).Scan(&name, &state); e != nil {
		writeErrorCode(w, 404, "studio_video_not_found", "Studio video not found")
		return
	}
	rows, e := h.DB.Query(ctx, `SELECT b.id,a.id,b.kind,b.issue_id,i.number,i.title,i.status,i.id IS NOT NULL,v.id,v.version_number,v.sha256,v.recorded_at,r.id,CASE WHEN v.id IS NOT NULL THEN 'available' ELSE 'unavailable' END,CASE WHEN v.id IS NOT NULL THEN 'ready' WHEN r.id IS NOT NULL THEN 'processing' ELSE 'awaiting_result' END FROM youtube_issue_binding b JOIN youtube_artifact a ON a.workspace_id=b.workspace_id AND a.project_id=b.project_id AND a.artifact_key=b.artifact_key LEFT JOIN issue i ON i.id=b.issue_id AND i.workspace_id=b.workspace_id AND i.project_id=b.project_id LEFT JOIN youtube_artifact_version v ON v.artifact_id=a.id AND v.version_number=a.current_version_number LEFT JOIN LATERAL (SELECT id FROM youtube_issue_result WHERE binding_id=b.id AND workspace_id=b.workspace_id ORDER BY recorded_at DESC LIMIT 1) r ON true WHERE b.workspace_id=$1 AND b.project_id=$2 AND b.active ORDER BY b.created_at,b.id`, wid, vid)
	if e != nil {
		writeErrorCode(w, 500, "studio_read_failed", "Studio read failed")
		return
	}
	defer rows.Close()
	materials := []any{}
	for rows.Next() {
		var bid, aid, iid, versionID, resultID pgtype.UUID
		var kind, prefixState, ingest string
		var number, version int
		var title, status string
		var hasIssue bool
		var hash string
		var recorded time.Time
		if e = rows.Scan(&bid, &aid, &kind, &iid, &number, &title, &status, &hasIssue, &versionID, &version, &hash, &recorded, &resultID, &prefixState, &ingest); e != nil {
			writeErrorCode(w, 500, "studio_read_failed", "Studio read failed")
			return
		}
		var source any
		if hasIssue {
			source = map[string]any{"id": uuidToString(iid), "identifier": strconv.Itoa(number), "title": title, "status": status}
		}
		var current any
		if versionID.Valid {
			current = map[string]any{"id": uuidToString(versionID), "version_number": version, "sha256": hash, "recorded_at": recorded.UTC().Format(time.RFC3339Nano)}
		}
		materials = append(materials, map[string]any{"binding_id": uuidToString(bid), "artifact_id": uuidToString(aid), "kind": kind, "source_issue": source, "source_state": prefixState, "current_version": current, "ingestion": map[string]any{"state": ingest, "attempt_count": 1, "next_attempt_at": nil, "failure_code": nil}})
	}
	writeJSON(w, 200, map[string]any{"video_id": uuidToString(vid), "name": name, "lifecycle_state": state, "materials": materials})
}

func (h *Handler) YouTubeStudioVersions(w http.ResponseWriter, r *http.Request) {
	wid, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	vid, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "video_id"), "video_id")
	if !ok {
		return
	}
	aid, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "artifact_id"), "artifact_id")
	if !ok {
		return
	}
	limit, ok := youtubeLimit(r, 20, 100)
	if !ok {
		writeErrorCode(w, 400, "invalid_pagination", "Invalid pagination")
		return
	}
	var before any
	if s := r.URL.Query().Get("before_version"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 {
			writeErrorCode(w, 400, "invalid_pagination", "Invalid pagination")
			return
		}
		before = n
	}
	rows, e := h.DB.Query(r.Context(), `SELECT v.id,v.artifact_id,v.version_number,v.sha256,v.recorded_at,v.source_result_id,v.source_issue_id,v.source_task_id FROM youtube_artifact_version v JOIN youtube_artifact a ON a.id=v.artifact_id AND a.workspace_id=v.workspace_id WHERE v.workspace_id=$1 AND a.project_id=$2 AND v.artifact_id=$3 AND ($4::int IS NULL OR v.version_number<$4) ORDER BY v.version_number DESC LIMIT $5`, wid, vid, aid, before, limit)
	if e != nil {
		writeErrorCode(w, 500, "studio_read_failed", "Studio read failed")
		return
	}
	defer rows.Close()
	items := []any{}
	last := 0
	for rows.Next() {
		var id, ar, sr, si, st pgtype.UUID
		var n int
		var hash string
		var at time.Time
		if e = rows.Scan(&id, &ar, &n, &hash, &at, &sr, &si, &st); e != nil {
			writeErrorCode(w, 500, "studio_read_failed", "Studio read failed")
			return
		}
		items = append(items, map[string]any{"id": uuidToString(id), "version_number": n, "sha256": hash, "recorded_at": at.UTC().Format(time.RFC3339Nano), "source_result_id": uuidToString(sr), "source_issue_id": uuidToString(si), "source_task_id": uuidToString(st)})
		last = n
	}
	var next any
	if len(items) == limit {
		next = last
	}
	writeJSON(w, 200, map[string]any{"versions": items, "next_before_version": next})
}

func (h *Handler) YouTubeStudioVersion(w http.ResponseWriter, r *http.Request) {
	wid, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	vid, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "video_id"), "video_id")
	if !ok {
		return
	}
	aid, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "artifact_id"), "artifact_id")
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "version_id"), "version_id")
	if !ok {
		return
	}
	var artifact, sourceResult, sourceIssue, sourceTask pgtype.UUID
	var n int
	var markdown, hash string
	var at time.Time
	if e := h.DB.QueryRow(r.Context(), `SELECT v.artifact_id,v.version_number,v.markdown,v.sha256,v.recorded_at,v.source_result_id,v.source_issue_id,v.source_task_id FROM youtube_artifact_version v JOIN youtube_artifact a ON a.id=v.artifact_id AND a.workspace_id=v.workspace_id WHERE v.workspace_id=$1 AND a.project_id=$2 AND v.artifact_id=$3 AND v.id=$4`, wid, vid, aid, id).Scan(&artifact, &n, &markdown, &hash, &at, &sourceResult, &sourceIssue, &sourceTask); e != nil {
		writeErrorCode(w, 404, "studio_version_not_found", "Studio version not found")
		return
	}
	writeJSON(w, 200, map[string]any{"id": uuidToString(id), "artifact_id": uuidToString(artifact), "version_number": n, "content_kind": "markdown", "markdown": markdown, "sha256": hash, "recorded_at": at.UTC().Format(time.RFC3339Nano), "provenance": map[string]any{"source_result_id": uuidToString(sourceResult), "source_issue_id": uuidToString(sourceIssue), "source_task_id": uuidToString(sourceTask), "producer": nil}})
}
