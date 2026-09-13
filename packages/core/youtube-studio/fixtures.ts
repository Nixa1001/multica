import type { StudioMaterial } from "../types/youtube-studio";

export const studioMaterialFixture = (state: StudioMaterial["ingestion"]["state"], withVersion = false): StudioMaterial => ({
  binding_id: "binding-1", artifact_id: "artifact-1", kind: "markdown",
  source_issue: { id: "issue-1", identifier: "PRI-42", title: "Manuskript", status: "in_progress" },
  source_state: "available",
  current_version: withVersion ? { id: "version-1", version_number: 1, sha256: "a".repeat(64), recorded_at: "2026-09-13T10:00:00Z", source_result_id: "result-1", source_issue_id: "issue-1", source_task_id: "task-1" } : null,
  ingestion: { state, attempt_count: 1, next_attempt_at: state === "retrying" ? "2026-09-13T10:00:02Z" : null, failure_code: state === "failed" ? "projection_failed" : null },
});
