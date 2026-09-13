// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { InboxItem, Issue, Project } from "@multica/core/types";
import { groupInboxItemsByProject } from "./inbox-project-grouping";

const item = (id: string, issueId: string | null): InboxItem =>
  ({ id, issue_id: issueId, created_at: id } as unknown as InboxItem);

describe("groupInboxItemsByProject", () => {
  it("groups by project id, keeps item order, and puts issue-less rows last", () => {
    const groups = groupInboxItemsByProject(
      [item("new", "issue-2"), item("middle", null), item("old", "issue-1")],
      [
        { id: "issue-1", project_id: "project-a" },
        { id: "issue-2", project_id: "project-b" },
      ] as unknown as Issue[],
      [
        { id: "project-a", title: "Alpha" },
        { id: "project-b", title: "Beta" },
      ] as unknown as Project[],
      { withoutProject: "Without project", unknownProject: "Unknown project" },
    );

    expect(groups.map((group) => group.title)).toEqual([
      "Alpha",
      "Beta",
      "Without project",
    ]);
    expect(groups[1]?.items.map((entry) => entry.id)).toEqual(["new"]);
    expect(groups[2]?.items.map((entry) => entry.id)).toEqual(["middle"]);
  });
});
