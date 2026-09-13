"use client";
import { use } from "react";
import { YoutubeStudioPage } from "@multica/views/youtube-studio";
export default function Page({ params }: { params: Promise<{ videoId: string }> }) { return <YoutubeStudioPage videoId={use(params).videoId} />; }
