import { describe, expect, it } from "vitest";
import { StudioVideoDetailSchema, StudioVideosSchema } from "./schema";
import { studioMaterialFixture } from "./fixtures";

describe("YouTube Studio response guards", () => {
  it("reject malformed lists instead of turning them into empty success", () => {
    expect(StudioVideosSchema.safeParse({ videos: "not-an-array", next_cursor: null }).success).toBe(false);
  });
  it("accepts a contract-shaped detail fixture", () => {
    expect(StudioVideoDetailSchema.safeParse({ video_id: "video-1", name: "Pilot", lifecycle_state: "active", materials: [studioMaterialFixture("ready")] }).success).toBe(true);
  });
});
