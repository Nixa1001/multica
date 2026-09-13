import { describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { youtubeStudioVersionsOptions, youtubeStudioVideosOptions } from "./queries";

vi.mock("../api", () => ({
  api: {
    listYoutubeStudioVideos: vi.fn(),
    listYoutubeStudioVersions: vi.fn(),
  },
}));

describe("YouTube Studio query options", () => {
  it("passes the video cursor to the real API query function", async () => {
    vi.mocked(api.listYoutubeStudioVideos).mockResolvedValue({ videos: [], next_cursor: null });
    await youtubeStudioVideosOptions("ws").queryFn!({} as never);
    await youtubeStudioVideosOptions("ws", "cursor-2").queryFn!({} as never);
    expect(api.listYoutubeStudioVideos).toHaveBeenLastCalledWith("cursor-2");
  });

  it("passes the version before cursor to the real API query function", async () => {
    vi.mocked(api.listYoutubeStudioVersions).mockResolvedValue({ versions: [], next_before_version: null });
    await youtubeStudioVersionsOptions("ws", "video", "artifact", 12).queryFn!({} as never);
    expect(api.listYoutubeStudioVersions).toHaveBeenCalledWith("video", "artifact", 12);
  });
});
