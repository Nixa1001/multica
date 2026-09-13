import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const push = vi.fn();
const videos = [{ video_id: "video-1", name: "Episode one", icon: null, lifecycle_state: "active", material_count: 2, attention_count: 0, updated_at: "2026-09-13T10:00:00Z" }];

vi.mock("../i18n", () => ({ useT: () => ({ t: (key: string, options?: { count?: number }) => options?.count === undefined ? key : `${key}:${options.count}` }) }));
vi.mock("../navigation", () => ({ useNavigation: () => ({ push }), AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ youtubeStudio: () => "/studio", youtubeStudioVideo: (id: string) => `/studio/${id}`, issueDetail: (id: string) => `/issues/${id}` }) }));
vi.mock("@multica/core/api", () => ({ api: { listYoutubeStudioVideos: vi.fn(), bindYoutubeStudio: vi.fn() } }));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({}) }));
vi.mock("@multica/core/projects/queries", () => ({ projectListOptions: () => ({}) }));
vi.mock("@multica/core/youtube-studio", () => ({ youtubeStudioKeys: { all: () => [], versions: () => [] }, youtubeStudioVideosOptions: () => ({ queryKey: ["videos"] }), youtubeStudioVideoOptions: () => ({}), youtubeStudioVersionsOptions: () => ({ queryKey: ["versions"] }), youtubeStudioVersionOptions: () => ({}), mergeVersionOptions: () => [] }));
vi.mock("@tanstack/react-query", () => ({ useInfiniteQuery: () => ({ isPending: false, isError: false, data: { pages: [{ videos, next_cursor: null }] }, hasNextPage: false, isFetchingNextPage: false, fetchNextPage: vi.fn() }), useQuery: (options: { queryKey?: string[] }) => options.queryKey?.[0] === "videos" ? { data: [] } : { data: [] }, useMutation: () => ({ isPending: false, isError: false, mutate: vi.fn() }), useQueryClient: () => ({ invalidateQueries: vi.fn() }) }));
vi.mock("../rich-content", () => ({ RichContent: ({ content }: { content: string }) => <div>{content}</div> }));

import { YoutubeStudioPage } from "./index";

describe("YouTube Studio rendered surface", () => {
  it("keeps video cards keyboard reachable and navigates on Enter", () => {
    render(<YoutubeStudioPage />);
    const card = screen.getByRole("button", { name: /Episode one/ });
    card.focus();
    expect(card).toHaveFocus();
    fireEvent.keyDown(card, { key: "Enter" });
    fireEvent.click(card);
    expect(push).toHaveBeenCalledWith("/studio/video-1");
  });
});
