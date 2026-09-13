import { describe, expect, it } from "vitest";
import { mergeVersionOptions, shouldPollStudioDetail } from "./state";
import { studioMaterialFixture } from "./fixtures";
describe("YouTube Studio polling", () => {
  it("covers the terminal and in-flight state matrix", () => {
    expect(shouldPollStudioDetail([studioMaterialFixture("awaiting_result")])).toBe(false);
    expect(shouldPollStudioDetail([studioMaterialFixture("processing")])).toBe(true);
    expect(shouldPollStudioDetail([studioMaterialFixture("retrying")])).toBe(true);
    expect(shouldPollStudioDetail([studioMaterialFixture("ready")])).toBe(false);
    expect(shouldPollStudioDetail([studioMaterialFixture("failed")])).toBe(false);
  });
  it("keeps a previous version available while processing", () => {
    const material = studioMaterialFixture("processing", true);
    expect(material.current_version?.version_number).toBe(1);
    expect(shouldPollStudioDetail([material])).toBe(true);
  });
  it("keeps a newly selected current version in the options during refetch", () => {
    const old = studioMaterialFixture("ready", true).current_version!;
    const next = { ...old, id: "version-2", version_number: 2 };
    expect(mergeVersionOptions([old], next).map((version) => version.id)).toEqual(["version-2", "version-1"]);
  });
});
