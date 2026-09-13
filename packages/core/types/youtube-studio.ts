export type StudioIngestionState = "awaiting_result" | "processing" | "retrying" | "ready" | "failed";
export interface StudioVideo { video_id: string; name: string; icon: string | null; lifecycle_state: string; material_count: number; attention_count: number; updated_at: string; }
export interface StudioVideosResponse { videos: StudioVideo[]; next_cursor: string | null; }
export interface StudioSourceIssue { id: string; identifier: string; title: string; status: string; }
export interface StudioVersionSummary { id: string; version_number: number; sha256: string; recorded_at: string; source_result_id: string; source_issue_id: string; source_task_id: string; }
export interface StudioMaterial { binding_id: string; artifact_id: string; kind: "markdown"; source_issue: StudioSourceIssue | null; source_state: "available" | "unavailable"; current_version: StudioVersionSummary | null; ingestion: { state: StudioIngestionState; attempt_count: number; next_attempt_at: string | null; failure_code: string | null; }; }
export interface StudioVideoDetail { video_id: string; name: string; lifecycle_state: string; materials: StudioMaterial[]; }
export interface StudioVersionsResponse { versions: StudioVersionSummary[]; next_before_version: number | null; }
export interface StudioProvenance { source_result_id: string; source_issue_id: string; source_task_id: string; producer: { type: string; id: string; name: string } | null; }
export interface StudioVersion extends StudioVersionSummary { artifact_id: string; content_kind: "markdown"; markdown: string; provenance: StudioProvenance; }
export interface StudioBindingResponse { created: boolean; video_id: string; binding: { id: string; issue_id: string; artifact_id: string; kind: "markdown"; active: boolean; created_at: string; }; }
