import type { StudioMaterial } from "../types/youtube-studio";
import type { StudioVersionSummary } from "../types/youtube-studio";
export function shouldPollStudioDetail(materials: StudioMaterial[]): boolean {
  return materials.some((m) => m.ingestion.state === "processing" || m.ingestion.state === "retrying");
}
export function mergeVersionOptions(versions: StudioVersionSummary[], current: StudioVersionSummary | null): StudioVersionSummary[] {
  const all = current ? [current, ...versions] : versions;
  return all.filter((version, index, list) => list.findIndex((candidate) => candidate.id === version.id) === index);
}
