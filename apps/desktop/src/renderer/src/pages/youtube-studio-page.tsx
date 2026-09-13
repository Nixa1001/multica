import { useParams } from "react-router-dom";
import { YoutubeStudioPage as SharedYoutubeStudioPage } from "@multica/views/youtube-studio";
export function YoutubeStudioPage() { const { videoId } = useParams<{ videoId?: string }>(); return <SharedYoutubeStudioPage videoId={videoId} />; }
