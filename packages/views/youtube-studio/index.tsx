"use client";

import { useEffect, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, FileVideo, RefreshCw } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueListOptions } from "@multica/core/issues/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { api } from "@multica/core/api";
import { mergeVersionOptions, youtubeStudioKeys, youtubeStudioVideoOptions, youtubeStudioVideosOptions, youtubeStudioVersionOptions, youtubeStudioVersionsOptions } from "@multica/core/youtube-studio";
import { useWorkspacePaths } from "@multica/core/paths";
import type { StudioIngestionState, StudioMaterial } from "@multica/core/types/youtube-studio";
import { useT } from "../i18n";
import { AppLink, useNavigation } from "../navigation";
import { RichContent } from "../rich-content";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";

type StudioTranslator = (key: string, options?: Record<string, unknown>) => string;

function ErrorPanel({ retry }: { retry: () => void }) {
  const { t } = useT();
  const translate = t as unknown as StudioTranslator;
  return <Card role="alert"><CardContent className="flex items-center justify-between gap-4 p-6"><p>{translate("youtube-studio.load_failed")}</p><Button variant="outline" onClick={retry}><RefreshCw aria-hidden="true" className="mr-2 size-4" />{translate("youtube-studio.retry")}</Button></CardContent></Card>;
}

function stateLabel(t: StudioTranslator, state: StudioIngestionState): string { return t(`youtube-studio.state_${state}`); }

export function YoutubeStudioPage({ videoId }: { videoId?: string }) {
  const { t } = useT(); const translate = t as unknown as StudioTranslator; const wsId = useWorkspaceId(); const paths = useWorkspacePaths(); const nav = useNavigation();
  const q = useInfiniteQuery({ queryKey: youtubeStudioVideosOptions(wsId).queryKey, initialPageParam: undefined as string | undefined, queryFn: ({ pageParam }) => api.listYoutubeStudioVideos(pageParam), getNextPageParam: (last) => last.next_cursor ?? undefined });
  if (videoId) return <YoutubeStudioDetail videoId={videoId} />;
  if (q.isPending) return <main aria-busy="true" className="p-6"><p className="text-muted-foreground">{translate("youtube-studio.loading")}</p><div className="mt-3 h-8 w-56 animate-pulse rounded bg-muted" /></main>;
  if (q.isError) return <main className="p-6"><ErrorPanel retry={() => void q.refetch()} /></main>;
  const videos = q.data.pages.flatMap((page) => page.videos);
  return <main className="mx-auto flex w-full max-w-5xl flex-col gap-8 p-6"><header><p className="text-sm font-medium uppercase tracking-widest text-muted-foreground">{translate("youtube-studio.eyebrow")}</p><h1 className="mt-2 text-3xl font-semibold">{translate("youtube-studio.title")}</h1><p className="mt-2 text-muted-foreground">{translate("youtube-studio.description")}</p></header>{videos.length > 0 ? <div className="grid gap-4 sm:grid-cols-2" aria-label={translate("youtube-studio.title")}>{videos.map((v) => <button type="button" key={v.video_id} className="rounded-lg text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => nav.push(paths.youtubeStudioVideo(v.video_id))}><Card className="transition-colors hover:border-foreground/30"><CardHeader><CardTitle className="flex items-center gap-3"><FileVideo aria-hidden="true" className="size-5 text-red-500" />{v.name}</CardTitle></CardHeader><CardContent className="text-sm text-muted-foreground">{translate("youtube-studio.materials", { count: v.material_count })}</CardContent></Card></button>)}</div> : <Card><CardContent className="p-6"><p>{translate("youtube-studio.empty")}</p><p className="mt-1 text-sm text-muted-foreground">{translate("youtube-studio.empty_hint")}</p></CardContent></Card>}{q.hasNextPage && <Button variant="outline" onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage}>{q.isFetchingNextPage ? translate("youtube-studio.loading_more") : translate("youtube-studio.load_more_videos")}</Button>}<ActivationCard /></main>;
}

function ActivationCard() {
  const { t: rawT } = useT(); const t = rawT as unknown as StudioTranslator; const wsId = useWorkspaceId(); const paths = useWorkspacePaths(); const nav = useNavigation(); const qc = useQueryClient();
  const [projectId, setProjectId] = useState(""); const [issueId, setIssueId] = useState(""); const projects = useQuery(projectListOptions(wsId)); const issues = useQuery(issueListOptions(wsId));
  const bind = useMutation({ mutationFn: () => api.bindYoutubeStudio(projectId, issueId), onSuccess: (d) => { void qc.invalidateQueries({ queryKey: youtubeStudioKeys.all(wsId) }); nav.push(paths.youtubeStudioVideo(d.video_id)); } });
  return <Card className="max-w-xl"><CardHeader><CardTitle>{t("youtube-studio.activation_title")}</CardTitle></CardHeader><CardContent className="space-y-4"><p className="text-sm text-muted-foreground">{t("youtube-studio.activation_hint")}</p><label className="block space-y-1 text-sm"><span>{t("youtube-studio.project_label")}</span><select aria-label={t("youtube-studio.project_label")} className="w-full rounded-md border bg-background p-2" value={projectId} onChange={(e) => { setProjectId(e.target.value); setIssueId(""); }}><option value="">{t("youtube-studio.choose_project")}</option>{(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.title}</option>)}</select></label><label className="block space-y-1 text-sm"><span>{t("youtube-studio.issue_label")}</span><select aria-label={t("youtube-studio.issue_label")} className="w-full rounded-md border bg-background p-2" value={issueId} onChange={(e) => setIssueId(e.target.value)}><option value="">{t("youtube-studio.choose_issue")}</option>{(issues.data ?? []).filter((i) => i.project_id === projectId).map((i) => <option key={i.id} value={i.id}>{i.identifier} · {i.title}</option>)}</select></label><Button disabled={!projectId || !issueId || bind.isPending} onClick={() => bind.mutate()}>{t("youtube-studio.activate")}</Button>{bind.isError && <p role="alert" className="text-sm text-destructive">{t("youtube-studio.activation_failed")}</p>}</CardContent></Card>;
}

function YoutubeStudioDetail({ videoId }: { videoId: string }) {
  const { t: rawT } = useT(); const t = rawT as unknown as StudioTranslator; const wsId = useWorkspaceId(); const paths = useWorkspacePaths(); const nav = useNavigation(); const q = useQuery(youtubeStudioVideoOptions(wsId, videoId));
  if (q.isPending) return <main aria-busy="true" className="p-6"><p className="text-muted-foreground">{t("youtube-studio.loading")}</p></main>;
  if (q.isError) return <main className="p-6"><ErrorPanel retry={() => void q.refetch()} /></main>;
  return <main className="mx-auto flex w-full max-w-6xl flex-col gap-6 p-6"><Button variant="ghost" className="w-fit" onClick={() => nav.push(paths.youtubeStudio())}><ArrowLeft aria-hidden="true" className="mr-2 size-4" />{t("youtube-studio.back")}</Button><header><p className="text-sm text-muted-foreground">{t("youtube-studio.detail_eyebrow")}</p><h1 className="mt-1 text-3xl font-semibold">{q.data.name}</h1></header>{q.data.materials.length === 0 ? <Card><CardContent className="p-6 text-muted-foreground">{t("youtube-studio.no_materials")}</CardContent></Card> : q.data.materials.map((m) => <Card key={m.artifact_id}><CardHeader><CardTitle className="flex justify-between"><span>{t("youtube-studio.markdown_material")}</span><span className="text-sm font-normal text-muted-foreground">{stateLabel(t, m.ingestion.state)}</span></CardTitle></CardHeader><CardContent className="space-y-4">{m.source_issue && <AppLink className="text-sm text-muted-foreground underline" href={paths.issueDetail(m.source_issue.id)}>{t("youtube-studio.source", { identifier: m.source_issue.identifier, title: m.source_issue.title })}</AppLink>}{m.current_version ? <VersionContent wsId={wsId} videoId={videoId} material={m} /> : <p className="text-muted-foreground">{t("youtube-studio.awaiting_result")}</p>}</CardContent></Card>)}</main>;
}

function VersionContent({ wsId, videoId, material }: { wsId: string; videoId: string; material: StudioMaterial }) {
  const { t: rawT } = useT(); const t = rawT as unknown as StudioTranslator; const current = material.current_version; const [versionId, setVersionId] = useState(current?.id ?? ""); const [explicit, setExplicit] = useState(false); const queryClient = useQueryClient();
  const list = useInfiniteQuery({ queryKey: youtubeStudioVersionsOptions(wsId, videoId, material.artifact_id).queryKey, initialPageParam: undefined as number | undefined, queryFn: ({ pageParam }) => api.listYoutubeStudioVersions(videoId, material.artifact_id, pageParam), getNextPageParam: (last) => last.next_before_version ?? undefined });
  useEffect(() => { void queryClient.invalidateQueries({ queryKey: youtubeStudioKeys.versions(wsId, videoId, material.artifact_id) }); if (!explicit && current?.id) setVersionId(current.id); }, [current?.id, explicit, material.artifact_id, queryClient, videoId, wsId]);
  const versions = useQuery({ ...youtubeStudioVersionOptions(wsId, videoId, material.artifact_id, versionId), enabled: !!versionId }); const summaries = list.data?.pages.flatMap((page) => page.versions) ?? []; const options = mergeVersionOptions(summaries, current);
  return <div className="space-y-3">{list.isPending ? <p className="text-muted-foreground">{t("youtube-studio.immutable_loading")}</p> : list.isError ? <ErrorPanel retry={() => void list.refetch()} /> : <label className="flex items-center gap-2 text-sm">{t("youtube-studio.version")}<select aria-label={t("youtube-studio.version")} className="rounded border bg-background p-1" value={versionId} onChange={(e) => { setExplicit(true); setVersionId(e.target.value); }}>{options.map((v) => <option key={v.id} value={v.id}>{v.version_number}</option>)}</select></label>}{list.hasNextPage && <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>{list.isFetchingNextPage ? t("youtube-studio.loading_more") : t("youtube-studio.older_versions")}</Button>}{versions.isPending ? <p className="text-muted-foreground">{t("youtube-studio.immutable_loading")}</p> : versions.isError ? <ErrorPanel retry={() => void versions.refetch()} /> : versions.data?.markdown === "" ? <p className="text-muted-foreground">{t("youtube-studio.empty_markdown")}</p> : <RichContent content={versions.data?.markdown ?? ""} />}{versions.data && <p className="font-mono text-xs text-muted-foreground">{t("youtube-studio.version")} {versions.data.version_number} · {t("youtube-studio.sha", { sha: versions.data.sha256 })}</p>}{versions.data?.provenance && <p className="text-xs text-muted-foreground">{t("youtube-studio.provenance", { result: versions.data.provenance.source_result_id, task: versions.data.provenance.source_task_id })}{versions.data.provenance.producer && t("youtube-studio.producer", { name: versions.data.provenance.producer.name })}</p>}</div>;
}
