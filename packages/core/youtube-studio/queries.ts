import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const youtubeStudioKeys = {
  all: (wsId: string) => ["youtube-studio", wsId] as const,
  videos: (wsId: string) => [...youtubeStudioKeys.all(wsId), "videos"] as const,
  video: (wsId: string, videoId: string) => [...youtubeStudioKeys.videos(wsId), videoId] as const,
  versions: (wsId: string, videoId: string, artifactId: string) => [...youtubeStudioKeys.video(wsId, videoId), "versions", artifactId] as const,
  version: (wsId: string, videoId: string, artifactId: string, versionId: string) => [...youtubeStudioKeys.versions(wsId, videoId, artifactId), versionId] as const,
};
export const youtubeStudioVideosOptions = (wsId: string, cursor?: string) => queryOptions({ queryKey: [...youtubeStudioKeys.videos(wsId), cursor ?? "first"] as const, queryFn: () => api.listYoutubeStudioVideos(cursor) });
export const youtubeStudioVideoOptions = (wsId: string, videoId: string) => queryOptions({ queryKey: youtubeStudioKeys.video(wsId, videoId), queryFn: () => api.getYoutubeStudioVideo(videoId), refetchInterval: (query) => query.state.data?.materials.some((m) => m.ingestion.state === "processing" || m.ingestion.state === "retrying") ? 2000 : false });
export const youtubeStudioVersionsOptions = (wsId: string, videoId: string, artifactId: string, before?: number) => queryOptions({ queryKey: [...youtubeStudioKeys.versions(wsId, videoId, artifactId), before ?? "first"] as const, queryFn: () => api.listYoutubeStudioVersions(videoId, artifactId, before) });
export const youtubeStudioVersionOptions = (wsId: string, videoId: string, artifactId: string, versionId: string) => queryOptions({ queryKey: youtubeStudioKeys.version(wsId, videoId, artifactId, versionId), queryFn: () => api.getYoutubeStudioVersion(videoId, artifactId, versionId) });
