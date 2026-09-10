package youtubestudio

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"log/slog"
	"time"
)

type TxStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

type workerQueries interface {
	ClaimYouTubeStudioEvent(context.Context, pgtype.Timestamptz) (db.YoutubeStudioOutbox, error)
	WithTx(pgx.Tx) *db.Queries
	MarkYouTubeStudioEventFailed(context.Context, db.MarkYouTubeStudioEventFailedParams) error
	MarkYouTubeStudioEventDeadLettered(context.Context, db.MarkYouTubeStudioEventDeadLetteredParams) error
}
type Worker struct {
	Queries     workerQueries
	TxStarter   TxStarter
	Interval    time.Duration
	MaxAttempts int32
	Logger      *slog.Logger
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.Queries == nil || w.TxStarter == nil {
		return
	}
	interval := w.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.ReconcileOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(ctx, "youtube studio reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) logger() *slog.Logger {
	if w != nil && w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
func (w *Worker) ReconcileOnce(ctx context.Context) error {
	if w == nil || w.Queries == nil || w.TxStarter == nil {
		return nil
	}
	max := w.MaxAttempts
	if max <= 0 {
		max = 10
	}
	for i := 0; i < 100; i++ {
		event, err := w.Queries.ClaimYouTubeStudioEvent(ctx, pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		tx, err := w.TxStarter.Begin(ctx)
		if err == nil {
			q := w.Queries.WithTx(tx)
			_, err = q.ProjectYouTubeStudioEvent(ctx, db.ProjectYouTubeStudioEventParams{EventID: event.EventID, LeaseToken: event.LeaseToken})
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
		}
		if err != nil {
			if event.AttemptCount+1 >= max {
				if stateErr := w.Queries.MarkYouTubeStudioEventDeadLettered(ctx, db.MarkYouTubeStudioEventDeadLetteredParams{EventID: event.EventID, LeaseToken: event.LeaseToken, LastError: err.Error()}); stateErr != nil {
					w.logger().ErrorContext(ctx, "youtube studio dead-letter state update failed", "event_id", event.EventID, "error", stateErr, "projection_error", err)
				}
			} else {
				if stateErr := w.Queries.MarkYouTubeStudioEventFailed(ctx, db.MarkYouTubeStudioEventFailedParams{EventID: event.EventID, LeaseToken: event.LeaseToken, LastError: err.Error(), NextAttemptAt: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}}); stateErr != nil {
					w.logger().ErrorContext(ctx, "youtube studio retry state update failed", "event_id", event.EventID, "error", stateErr, "projection_error", err)
				}
			}
			w.logger().WarnContext(ctx, "youtube studio event projection failed", "event_id", event.EventID, "error", err)
		}
	}
	return nil
}
