package main

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestRouterWiresYouTubeStudioProductionLogger(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	hub := realtime.NewHub()
	go hub.Run()
	_, h := NewRouterWithOptions(pool, hub, events.New(), analytics.NoopClient{}, nil, RouterOptions{})
	if h == nil || h.YouTubeStudioWorker == nil {
		t.Fatal("router did not assemble YouTube Studio worker")
	}
	if h.YouTubeStudioWorker.Logger == nil {
		t.Fatal("router did not supply the application logger")
	}
}
