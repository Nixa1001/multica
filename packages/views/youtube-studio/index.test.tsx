import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

const push = vi.fn();
const fetchNextPage = vi.fn();
const videos = [{ video_id: "video-1", name: "Episode one", icon: null, lifecycle_state: "active", material_count: 2, attention_count: 0, updated_at: "2026-09-13T10:00:00Z" }];
let listMode: "ready" | "loading" | "empty" | "error" = "ready";
let detailState = "processing";

vi.mock("../i18n", () => ({ useT: () => ({ t: (key: string, options?: { count?: number }) => options?.count === undefined ? key : `${key}:${options.count}` }) }));
vi.mock("../navigation", () => ({ useNavigation: () => ({ push }), AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ youtubeStudio: () => "/studio", youtubeStudioVideo: (id: string) => `/studio/${id}`, issueDetail: (id: string) => `/issues/${id}` }) }));
vi.mock("@multica/core/api", () => ({ api: { listYoutubeStudioVideos: vi.fn(), bindYoutubeStudio: vi.fn() } }));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({}) }));
vi.mock("@multica/core/projects/queries", () => ({ projectListOptions: () => ({}) }));
vi.mock("@multica/core/youtube-studio", () => ({ youtubeStudioKeys: { all: () => [], versions: () => [] }, youtubeStudioVideosOptions: () => ({ queryKey: ["videos"] }), youtubeStudioVideoOptions: () => ({ queryKey: ["video-detail"] }), youtubeStudioVersionsOptions: () => ({ queryKey: ["versions"] }), youtubeStudioVersionOptions: () => ({ queryKey: ["version"] }), mergeVersionOptions: () => [] }));
vi.mock("@tanstack/react-query", () => ({
  useInfiniteQuery: (options: { queryKey?: string[] }) => options.queryKey?.[0] === "versions"
    ? { isPending: false, isError: false, data: { pages: [{ versions: [], next_before_version: null }] }, hasNextPage: false, isFetchingNextPage: false, fetchNextPage }
    : { isPending: listMode === "loading", isError: listMode === "error", data: { pages: [{ videos: listMode === "empty" ? [] : videos, next_cursor: listMode === "ready" ? "cursor-2" : null }] }, hasNextPage: listMode === "ready", isFetchingNextPage: false, fetchNextPage },
  useQuery: (options: { queryKey?: string[] }) => options.queryKey?.[0] === "video-detail"
    ? { isPending: false, isError: false, data: { video_id: "video-1", name: "Episode one", lifecycle_state: "active", materials: [{ binding_id: "binding-1", artifact_id: "artifact-1", kind: "markdown", source_issue: null, source_state: "available", current_version: { id: "version-1", version_number: 1, sha256: "a".repeat(64), recorded_at: "2026-09-13T10:00:00Z", source_result_id: "result-1", source_issue_id: "issue-1", source_task_id: "task-1" }, ingestion: { state: detailState, attempt_count: 1, next_attempt_at: null, failure_code: detailState === "failed" ? "projection_failed" : null } }] } }
    : { data: [] },
  useMutation: () => ({ isPending: false, isError: false, mutate: vi.fn() }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));
vi.mock("../rich-content", () => ({ RichContent: ({ content }: { content: string }) => <div>{content}</div> }));

import { YoutubeStudioPage } from "./index";

describe("YouTube Studio rendered surface", () => {
  it("navigates from keyboard Enter without a compensating click", async () => {
    const user = userEvent.setup();
    render(<YoutubeStudioPage />);
    const card = screen.getByRole("button", { name: /Episode one/ });
    card.focus();
    await user.keyboard("{Enter}");
    expect(push).toHaveBeenCalledWith("/studio/video-1");
  });

  it("keeps click navigation independently covered", () => {
    render(<YoutubeStudioPage />);
    fireEvent.click(screen.getByRole("button", { name: /Episode one/ }));
    expect(push).toHaveBeenCalledWith("/studio/video-1");
  });

  it("renders empty, error and video pagination states", () => {
    listMode = "empty";
    const view = render(<YoutubeStudioPage />);
    expect(screen.getByText("youtube-studio.empty")).toBeInTheDocument();
    view.unmount();
    listMode = "error";
    render(<YoutubeStudioPage />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    listMode = "ready";
    render(<YoutubeStudioPage />);
    fireEvent.click(screen.getByRole("button", { name: "youtube-studio.load_more_videos" }));
    expect(fetchNextPage).toHaveBeenCalled();
  });

  it.each(["awaiting_result", "processing", "retrying", "ready", "failed"])("renders ingestion state %s", (state) => {
    detailState = state;
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText(`youtube-studio.state_${state}`)).toBeInTheDocument();
  });
});
