import type { StudioMaterial } from "../types/youtube-studio";
export function shouldPollStudioDetail(materials: StudioMaterial[]): boolean {
  return materials.some((m) => m.ingestion.state === "processing" || m.ingestion.state === "retrying");
}
