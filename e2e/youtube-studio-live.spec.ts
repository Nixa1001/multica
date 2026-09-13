import "./env";
import { test, expect } from "@playwright/test";
import pg from "pg";
import { ApiClient } from "@multica/core/api";
import { setCurrentWorkspace } from "@multica/core/platform";
import { TestApiClient } from "./fixtures";

const apiBase = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const databaseUrl = process.env.DATABASE_URL;

if (!databaseUrl) throw new Error("DATABASE_URL is required; refusing to use a shared default database");

type Fixture = {
  api: TestApiClient;
  client: ApiClient;
  workspace: { id: string; slug: string };
  projectId: string;
  issueId: string;
  artifactId: string;
};

async function seedVersions(fixture: Fixture) {
  const db = new pg.Client(databaseUrl);
  await db.connect();
  try {
    await db.query("BEGIN");
    const runtime = await db.query<{ id: string }>(
      `INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status)
       VALUES ($1, 'Studio live runtime', 'cloud', 'studio-live-e2e', 'online') RETURNING id`,
      [fixture.workspace.id],
    );
    const runtimeId = runtime.rows[0]?.id;
    if (!runtimeId) throw new Error("unable to create isolated runtime fixture");
    const agent = await db.query<{ id: string }>(
      `INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id)
       VALUES ($1, 'Studio live agent', 'cloud', $2) RETURNING id`,
      [fixture.workspace.id, runtimeId],
    );
    const agentId = agent.rows[0]?.id;
    if (!agentId) throw new Error("unable to create isolated agent fixture");
    const binding = await db.query<{ id: string }>(
      `SELECT id FROM youtube_issue_binding WHERE project_id = $1 AND issue_id = $2`,
      [fixture.projectId, fixture.issueId],
    );
    const bindingId = binding.rows[0]?.id;
    if (!bindingId) throw new Error("bind endpoint did not create a Studio binding");
    const versions = Array.from({ length: 21 }, (_, index) => ({
      number: index + 1,
      markdown: `Studio live version ${index + 1}`,
      hash: String(index + 1).repeat(64).slice(0, 64),
    }));
    for (const version of versions) {
      const task = await db.query<{ id: string }>(
        `INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, completed_at)
         VALUES ($1, $2, $3, 'completed', now()) RETURNING id`,
        [agentId, runtimeId, fixture.issueId],
      );
      const taskId = task.rows[0]?.id;
      if (!taskId) throw new Error("unable to create isolated completed task fixture");
      const resultRow = await db.query<{ id: string; event_id: string }>(
        `INSERT INTO youtube_issue_result
          (event_id, workspace_id, project_id, binding_id, artifact_id, source_issue_id, source_task_id, markdown, sha256, recorded_at)
         VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, now()) RETURNING id, event_id`,
        [fixture.workspace.id, fixture.projectId, bindingId, fixture.artifactId, fixture.issueId, taskId, version.markdown, version.hash],
      );
      await db.query(
        `INSERT INTO youtube_studio_outbox (event_id, workspace_id, result_id, event_kind, payload, consumed_at)
         VALUES ($1, $2, $3, 'IssueResultRecordedV1', '{}'::jsonb, now())`,
        [resultRow.rows[0]?.event_id, fixture.workspace.id, resultRow.rows[0]?.id],
      );
      await db.query(
        `INSERT INTO youtube_artifact_version
          (workspace_id, artifact_id, version_number, source_result_id, source_issue_id, source_task_id, markdown, sha256, recorded_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())`,
        [fixture.workspace.id, fixture.artifactId, version.number, resultRow.rows[0]?.id, fixture.issueId, taskId, version.markdown, version.hash],
      );
    }
    await db.query(
      `UPDATE youtube_artifact SET current_version_number = $2 WHERE id = $1`,
      [fixture.artifactId, versions.length],
    );
    await db.query("COMMIT");
  } catch (error) {
    await db.query("ROLLBACK");
    throw error;
  } finally {
    await db.end();
  }
}

async function makeFixture(): Promise<Fixture> {
  const api = new TestApiClient();
  const email = `studio-live-${Date.now()}-${process.pid}@localhost`;
  await api.login(email, "Studio Live E2E");
  const workspace = await api.ensureWorkspace(`Studio Live ${Date.now()}`, `studio-live-${Date.now()}-${process.pid}`);
  await api.markUserOnboarded();
  const client = new ApiClient(apiBase, { identity: { platform: "web", version: "e2e", os: "linux" } });
  client.setToken(api.getToken());
  setCurrentWorkspace(workspace.slug, workspace.id);
  const project = await client.createProject({ title: `Studio live ${Date.now()}` });
  const issue = await api.createIssue(`Studio live source ${Date.now()}`, { project_id: project.id, status: "in_progress" });
  await client.bindYoutubeStudio(project.id, issue.id);
  const detail = await client.getYoutubeStudioVideo(project.id);
  const artifactId = detail.materials[0]?.artifact_id;
  if (!artifactId) throw new Error("Studio bind returned no artifact");
  const fixture = { api, client, workspace, projectId: project.id, issueId: issue.id, artifactId };
  await seedVersions(fixture);
  return fixture;
}

test.describe("YouTube Studio live harness", () => {
  let fixture: Fixture;

  test.beforeEach(async () => { fixture = await makeFixture(); });
  test.afterEach(async () => {
    if (!fixture) return;
    await fixture.api.cleanup();
    await fixture.client.deleteProject(fixture.projectId).catch(() => undefined);
    await fixture.client.deleteWorkspace(fixture.workspace.id).catch(() => undefined);
  });

  test("real ApiClient follows before_version without overlap", async () => {
    const first = await fixture.client.listYoutubeStudioVersions(fixture.projectId, fixture.artifactId);
    const cursor = first.next_before_version;
    expect(first.versions.map((item) => item.version_number)).toEqual(Array.from({ length: 20 }, (_, index) => 21 - index));
    expect(cursor).toBe(2);
    const second = await fixture.client.listYoutubeStudioVersions(fixture.projectId, fixture.artifactId, cursor);
    expect(second.versions.map((item) => item.version_number)).toEqual([1]);
    expect(second.versions.map((item) => item.id).some((id) => first.versions.some((item) => item.id === id))).toBe(false);
    expect(second.next_before_version).toBeNull();
    const detailResponse = await fetch(`${apiBase}/api/youtube-studio/videos/${fixture.projectId}/artifacts/${fixture.artifactId}/versions/${second.versions[0]!.id}`, {
      headers: { Authorization: `Bearer ${fixture.api.getToken()}`, "X-Workspace-Slug": fixture.workspace.slug },
    });
    const detail = await detailResponse.json() as { markdown: string; sha256: string; provenance: { source_issue_id: string } };
    expect(detail).toMatchObject({ markdown: "Studio live version 1", sha256: "1".repeat(64), provenance: { source_issue_id: fixture.issueId } });

    const regression = await fetch(`${apiBase}/api/youtube-studio/videos/${fixture.projectId}/artifacts/${fixture.artifactId}/versions?limit=1&before=2`, {
      headers: { Authorization: `Bearer ${fixture.api.getToken()}`, "X-Workspace-Slug": fixture.workspace.slug },
    });
    const regressionBody = await regression.json() as { versions: Array<{ version_number: number }> };
    expect(regressionBody.versions[0]?.version_number).toBe(21);
  });

  test("authenticated web route renders versions and pagination", async ({ page }) => {
    const browserErrors: string[] = [];
    const apiResponses: string[] = [];
    page.on("console", (message) => { if (message.type() === "error") browserErrors.push(`console: ${message.text()}`); });
    page.on("pageerror", (error) => browserErrors.push(`pageerror: ${error.message}`));
    page.on("response", async (response) => {
      if (response.url().includes("/api/youtube-studio/videos/") && response.request().method() === "GET") {
        apiResponses.push(`${response.status()} ${response.url()} ${(await response.text()).slice(0, 2000)}`);
      }
    });
    page.on("requestfailed", (request) => {
      const failure = request.failure()?.errorText;
      if (failure && failure !== "net::ERR_ABORTED") browserErrors.push(`request: ${request.url()} — ${failure}`);
    });
    await page.addInitScript((token) => localStorage.setItem("multica_token", token), fixture.api.getToken());
    await page.goto(`/${fixture.workspace.slug}/youtube-studio/${fixture.projectId}`);
    await page.waitForTimeout(1000);
    if (browserErrors.length > 0) throw new Error(`browser diagnostics: ${browserErrors.join(" | ")}`);
    try {
      await expect(page.getByText("Studio live version 21")).toBeVisible({ timeout: 20000 });
    } catch (error) {
      throw new Error(`${error instanceof Error ? error.message : String(error)}; url=${page.url()}; body=${(await page.locator("body").innerText()).slice(0, 1000)}; api=${apiResponses.join(" | ")}`);
    }
    await expect(page.getByText("Studio live version 1")).toHaveCount(0);
    await page.getByRole("button", { name: /older versions/i }).click();
    await expect(page.getByText("Studio live version 1")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("Studio live version 21")).toBeVisible();
    await page.reload();
    await expect(page.getByText("Studio live version 21")).toBeVisible({ timeout: 20000 });
  });
});
