import { describe, expect, it } from "vitest";
import { shouldPollStudioDetail } from "./state";
const material = (state: "awaiting_result"|"processing"|"retrying"|"ready"|"failed") => ({ ingestion: { state } } as never);
describe("YouTube Studio polling", () => {
  it("polls only processing/retrying", () => {
    expect(shouldPollStudioDetail([material("awaiting_result")])).toBe(false);
    expect(shouldPollStudioDetail([material("processing")])).toBe(true);
    expect(shouldPollStudioDetail([material("retrying")])).toBe(true);
    expect(shouldPollStudioDetail([material("ready")])).toBe(false);
  });
});
