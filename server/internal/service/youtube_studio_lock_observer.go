package service

import "context"

// YouTubeStudioProjectLockObserver is intentionally narrow: it lets bounded
// integration tests observe the exact point at which the PostgreSQL project
// lock query has returned, without replacing the real transaction or query.
type YouTubeStudioProjectLockObserver struct {
	Before   func(pid int32)
	Acquired func(pid int32)
}

type youtubeStudioProjectLockObserverKey struct{}

func WithYouTubeStudioProjectLockObserver(ctx context.Context, observer *YouTubeStudioProjectLockObserver) context.Context {
	return context.WithValue(ctx, youtubeStudioProjectLockObserverKey{}, observer)
}

func YouTubeStudioProjectLockObserverEnabled(ctx context.Context) bool {
	observer, ok := ctx.Value(youtubeStudioProjectLockObserverKey{}).(*YouTubeStudioProjectLockObserver)
	return ok && observer != nil
}

func NotifyYouTubeStudioProjectLockBefore(ctx context.Context, pid int32) {
	if observer, ok := ctx.Value(youtubeStudioProjectLockObserverKey{}).(*YouTubeStudioProjectLockObserver); ok && observer != nil && observer.Before != nil {
		observer.Before(pid)
	}
}

func NotifyYouTubeStudioProjectLockAcquired(ctx context.Context, pid int32) {
	if observer, ok := ctx.Value(youtubeStudioProjectLockObserverKey{}).(*YouTubeStudioProjectLockObserver); ok && observer != nil && observer.Acquired != nil {
		observer.Acquired(pid)
	}
}
