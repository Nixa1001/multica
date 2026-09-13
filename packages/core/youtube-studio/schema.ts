import { z } from "zod";
export const StudioVideosSchema = z.object({ videos: z.array(z.object({ video_id: z.string(), name: z.string(), icon: z.string().nullable(), lifecycle_state: z.string(), material_count: z.number(), attention_count: z.number(), updated_at: z.string() })), next_cursor: z.string().nullable() });
export const StudioVideoDetailSchema = z.object({ video_id: z.string(), name: z.string(), lifecycle_state: z.string(), materials: z.array(z.unknown()) });
export const StudioVersionsSchema = z.object({ versions: z.array(z.unknown()), next_before_version: z.number().nullable() });
export const StudioVersionSchema = z.object({ id: z.string(), artifact_id: z.string(), version_number: z.number(), content_kind: z.literal("markdown"), markdown: z.string(), sha256: z.string(), recorded_at: z.string(), provenance: z.record(z.string(), z.unknown()) });
