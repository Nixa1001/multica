package service

import "context"

// YouTubeStudioProjectLockObserver is intentionally narrow: it lets bounded
// integration tests observe the exact point at which the PostgreSQL project
// lock query has returned, without replacing the real transaction or query.
type YouTubeStudioProjectLockObserver struct {
	Before   func()
	Acquired func()
}

type youtubeStudioProjectLockObserverKey struct{}

func WithYouTubeStudioProjectLockObserver(ctx context.Context, observer *YouTubeStudioProjectLockObserver) context.Context {
	return context.WithValue(ctx, youtubeStudioProjectLockObserverKey{}, observer)
}

func NotifyYouTubeStudioProjectLockBefore(ctx context.Context) {
	if observer, ok := ctx.Value(youtubeStudioProjectLockObserverKey{}).(*YouTubeStudioProjectLockObserver); ok && observer != nil && observer.Before != nil {
		observer.Before()
	}
}

func NotifyYouTubeStudioProjectLockAcquired(ctx context.Context) {
	if observer, ok := ctx.Value(youtubeStudioProjectLockObserverKey{}).(*YouTubeStudioProjectLockObserver); ok && observer != nil && observer.Acquired != nil {
		observer.Acquired()
	}
}
