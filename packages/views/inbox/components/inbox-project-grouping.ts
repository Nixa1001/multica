import type { InboxItem, Issue, Project } from "@multica/core/types";

export interface InboxProjectGroup {
  id: string;
  title: string;
  items: InboxItem[];
}

/** Preserve the inbox's newest-first row order while giving groups a stable order. */
export function groupInboxItemsByProject(
  items: InboxItem[],
  issues: Issue[],
  projects: Project[],
  labels: { withoutProject: string; unknownProject: string },
): InboxProjectGroup[] {
  const issueProjects = new Map(issues.map((issue) => [issue.id, issue.project_id]));
  const projectTitles = new Map(projects.map((project) => [project.id, project.title]));
  const groups = new Map<string, InboxProjectGroup>();

  for (const item of items) {
    const projectId = item.issue_id ? issueProjects.get(item.issue_id) ?? null : null;
    const id = projectId ?? "__no_project__";
    const title = projectId
      ? projectTitles.get(projectId) ?? labels.unknownProject
      : labels.withoutProject;
    const group = groups.get(id) ?? { id, title, items: [] };
    group.items.push(item);
    groups.set(id, group);
  }

  return [...groups.values()].sort((a, b) => {
    if (a.id === "__no_project__") return 1;
    if (b.id === "__no_project__") return -1;
    return a.title.localeCompare(b.title) || a.id.localeCompare(b.id);
  });
}
