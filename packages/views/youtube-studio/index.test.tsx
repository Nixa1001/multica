import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const push = vi.fn();
let versionPage = 1;
const fetchNextPage = vi.fn(() => { versionPage = 2; });
const videos = [{ video_id: "video-1", name: "Episode one", icon: null, lifecycle_state: "active", material_count: 2, attention_count: 0, updated_at: "2026-09-13T10:00:00Z" }];
let listMode: "ready" | "loading" | "empty" | "error" = "ready";
let detailState = "processing";
let mutationError = false;
let currentVersion = 1;
let versionsLoading = false;
let versionsError = false;
let versionLoading = false;
let versionError = false;
let hasCurrentVersion = true;
let emptyMarkdown = false;
const refetch = vi.fn();
const versionsRefetch = vi.fn();
const versionRefetch = vi.fn();

vi.mock("../i18n", () => ({ useT: () => ({ t: (key: string, options?: Record<string, string | number>) => {
  if (key.endsWith(".sha")) return `SHA-256: ${options?.sha ?? ""}`;
  if (key.endsWith(".provenance")) return `Result ${options?.result ?? ""} · Task ${options?.task ?? ""}`;
  if (key.endsWith(".producer")) return ` · ${options?.name ?? ""}`;
  return Object.entries(options ?? {}).reduce((value, [name, replacement]) => value.replace(`{{${name}}}`, String(replacement)), key);
} }) }));
vi.mock("../navigation", () => ({ useNavigation: () => ({ push }), AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ youtubeStudio: () => "/studio", youtubeStudioVideo: (id: string) => `/studio/${id}`, issueDetail: (id: string) => `/issues/${id}` }) }));
vi.mock("@multica/core/api", () => ({ api: { listYoutubeStudioVideos: vi.fn(), bindYoutubeStudio: vi.fn(() => ({ video_id: "video-1" })) } }));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({ queryKey: ["issues"] }) }));
vi.mock("@multica/core/projects/queries", () => ({ projectListOptions: () => ({ queryKey: ["projects"] }) }));
vi.mock("@multica/core/youtube-studio", async (importOriginal) => ({ ...(await importOriginal<typeof import("@multica/core/youtube-studio")>()), youtubeStudioKeys: { all: () => [], versions: () => [] }, youtubeStudioVideosOptions: () => ({ queryKey: ["videos"] }), youtubeStudioVideoOptions: () => ({ queryKey: ["video-detail"] }), youtubeStudioVersionsOptions: () => ({ queryKey: ["versions"] }), youtubeStudioVersionOptions: (_ws: string, _video: string, _artifact: string, versionId: string) => ({ queryKey: ["version", versionId] }) }));
vi.mock("@tanstack/react-query", () => ({
  useInfiniteQuery: (options: { queryKey?: string[] }) => options.queryKey?.[0] === "versions"
    ? { isPending: versionsLoading, isError: versionsError, data: { pages: [{ versions: (versionPage === 1 ? [3, 2] : [1]).map((version_number) => ({ id: `version-${version_number}`, version_number, sha256: String(version_number).repeat(64).slice(0, 64), recorded_at: "2026-09-13T10:00:00Z", source_result_id: `result-${version_number}`, source_issue_id: "issue-1", source_task_id: `task-${version_number}` })), next_before_version: versionPage === 1 ? 1 : null }] }, hasNextPage: !versionsLoading && !versionsError && versionPage === 1, isFetchingNextPage: false, fetchNextPage, refetch: versionsRefetch }
    : { isPending: listMode === "loading", isError: listMode === "error", data: { pages: [{ videos: listMode === "empty" ? [] : videos, next_cursor: listMode === "ready" ? "cursor-2" : null }] }, hasNextPage: listMode === "ready", isFetchingNextPage: false, fetchNextPage, refetch },
  useQuery: (options: { queryKey?: string[] }) => options.queryKey?.[0] === "video-detail"
    ? { isPending: false, isError: false, data: { video_id: "video-1", name: "Episode one", lifecycle_state: "active", materials: [{ binding_id: "binding-1", artifact_id: "artifact-1", kind: "markdown", source_issue: null, source_state: "available", current_version: hasCurrentVersion ? { id: `version-${currentVersion}`, version_number: currentVersion, sha256: String(currentVersion).repeat(64).slice(0, 64), recorded_at: "2026-09-13T10:00:00Z", source_result_id: `result-${currentVersion}`, source_issue_id: "issue-1", source_task_id: `task-${currentVersion}` } : null, ingestion: { state: detailState, attempt_count: 1, next_attempt_at: null, failure_code: detailState === "failed" ? "projection_failed" : null } }] } }
    : options.queryKey?.[0] === "version"
      ? { isPending: versionLoading, isError: versionError, data: { id: options.queryKey[1], artifact_id: "artifact-1", version_number: Number(options.queryKey[1]?.split("-")[1] ?? 1), sha256: String(options.queryKey[1]?.split("-")[1] ?? 1).repeat(64).slice(0, 64), recorded_at: "2026-09-13T10:00:00Z", source_result_id: `result-${options.queryKey[1]?.split("-")[1] ?? 1}`, source_issue_id: "issue-1", source_task_id: `task-${options.queryKey[1]?.split("-")[1] ?? 1}`, content_kind: "markdown", markdown: emptyMarkdown ? "" : `# Immutable ${options.queryKey[1]}`, provenance: { source_result_id: `result-${options.queryKey[1]?.split("-")[1] ?? 1}`, source_issue_id: "issue-1", source_task_id: `task-${options.queryKey[1]?.split("-")[1] ?? 1}`, producer: { type: "agent", id: "agent-1", name: "Writer" } } }, refetch: versionRefetch }
      : options.queryKey?.[0] === "projects" ? { data: [{ id: "project-1", title: "Project one" }, { id: "project-2", title: "Project two" }] }
        : options.queryKey?.[0] === "issues" ? { data: [{ id: "issue-1", identifier: "PRI-1", title: "Issue one", project_id: "project-1" }] }
          : { data: [] },
  useMutation: (options: { mutationFn?: () => { video_id: string }; onSuccess?: (data: { video_id: string }) => void }) => ({ isPending: false, isError: mutationError, mutate: vi.fn(() => { const result = options.mutationFn?.(); if (result) options.onSuccess?.(result); }) }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));
vi.mock("../rich-content", () => ({ RichContent: ({ content }: { content: string }) => <div>{content}</div> }));

import { YoutubeStudioPage } from "./index";
import { api } from "@multica/core/api";

describe("YouTube Studio rendered surface", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listMode = "ready"; detailState = "processing"; mutationError = false; currentVersion = 1;
    versionPage = 1; versionsLoading = false; versionsError = false; versionLoading = false; versionError = false; hasCurrentVersion = true;
  });
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

  it("renders loading and retries a network error", () => {
    listMode = "loading";
    const view = render(<YoutubeStudioPage />);
    expect(screen.getByText("youtube-studio.loading")).toBeInTheDocument();
    view.unmount();
    listMode = "error";
    render(<YoutubeStudioPage />);
    fireEvent.click(screen.getByRole("button", { name: "youtube-studio.retry" }));
    expect(refetch).toHaveBeenCalled();
    listMode = "ready";
  });

  it("resets the issue when switching projects and exposes mutation errors", () => {
    mutationError = true;
    render(<YoutubeStudioPage />);
    const selects = screen.getAllByRole("combobox");
    fireEvent.change(selects[0]!, { target: { value: "project-1" } });
    fireEvent.change(selects[1]!, { target: { value: "issue-1" } });
    fireEvent.change(selects[0]!, { target: { value: "project-2" } });
    expect(selects[1]!).toHaveValue("");
    expect(screen.getByRole("alert")).toBeInTheDocument();
    mutationError = false;
  });

  it("activates the selected project and issue with keyboard and navigates on success", async () => {
    const user = userEvent.setup();
    render(<YoutubeStudioPage />);
    const selects = screen.getAllByRole("combobox");
    await user.selectOptions(selects[0]!, "project-1");
    await user.selectOptions(selects[1]!, "issue-1");
    const activate = screen.getByRole("button", { name: "youtube-studio.activate" });
    activate.focus();
    await user.keyboard("{Enter}");
    expect(api.bindYoutubeStudio).toHaveBeenCalledWith("project-1", "issue-1");
    expect(push).toHaveBeenCalledWith("/studio/video-1");
  });

  it.each(["awaiting_result", "processing", "retrying", "ready", "failed"])("renders ingestion state %s", (state) => {
    detailState = state;
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText(`youtube-studio.state_${state}`)).toBeInTheDocument();
  });

  it("renders immutable version content with matching hash and provenance", () => {
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("# Immutable version-1")).toBeInTheDocument();
    expect(screen.getByText(/SHA-256/)).toHaveTextContent("1".repeat(64));
    expect(screen.getByText(/Writer/)).toBeInTheDocument();
  });

  it("renders awaiting result without a current version", () => {
    hasCurrentVersion = false;
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("youtube-studio.awaiting_result")).toBeInTheDocument();
    hasCurrentVersion = true;
  });

  it("switches N to N+1, then preserves an explicit historical selection", async () => {
    const user = userEvent.setup();
    const view = render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("# Immutable version-1")).toBeInTheDocument();
    currentVersion = 2;
    view.rerender(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("# Immutable version-2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "youtube-studio.older_versions" }));
    view.rerender(<YoutubeStudioPage videoId="video-1" />);
    const versionSelect = screen.getByRole("combobox", { name: "youtube-studio.version" });
    await user.selectOptions(versionSelect, "version-1");
    currentVersion = 3;
    view.rerender(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("# Immutable version-1")).toBeInTheDocument();
  });

  it("renders versions pagination and its loading/error retry state", () => {
    const view = render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByRole("button", { name: "youtube-studio.older_versions" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "youtube-studio.older_versions" }));
    expect(fetchNextPage).toHaveBeenCalled();
    view.rerender(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByRole("combobox", { name: "youtube-studio.version" })).toHaveValue("version-1");
    versionsLoading = true;
    const loadingView = render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("youtube-studio.immutable_loading")).toBeInTheDocument();
    loadingView.unmount();
    versionsLoading = false;
    versionsError = true;
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
    fireEvent.click(screen.getAllByRole("button", { name: "youtube-studio.retry" })[0]!);
    expect(versionsRefetch).toHaveBeenCalled();
    versionsError = false;
  });

  it("renders empty Markdown and immutable content loading/error states", () => {
    emptyMarkdown = true;
    const view = render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getByText("youtube-studio.empty_markdown")).toBeInTheDocument();
    view.unmount();
    emptyMarkdown = false;
    versionLoading = true;
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getAllByText("youtube-studio.immutable_loading").length).toBeGreaterThan(0);
    versionLoading = false;
    versionError = true;
    render(<YoutubeStudioPage videoId="video-1" />);
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
    versionError = false;
  });
});
