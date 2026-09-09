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
type Worker struct {
	Queries     *db.Queries
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
		_ = w.ReconcileOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
			err = q.ProjectYouTubeStudioEvent(ctx, db.ProjectYouTubeStudioEventParams{EventID: event.EventID, LeaseToken: event.LeaseToken})
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
		}
		if err != nil {
			if event.AttemptCount+1 >= max {
				_ = w.Queries.MarkYouTubeStudioEventDeadLettered(ctx, db.MarkYouTubeStudioEventDeadLetteredParams{EventID: event.EventID, LeaseToken: event.LeaseToken, LastError: err.Error()})
			} else {
				_ = w.Queries.MarkYouTubeStudioEventFailed(ctx, db.MarkYouTubeStudioEventFailedParams{EventID: event.EventID, LeaseToken: event.LeaseToken, LastError: err.Error(), NextAttemptAt: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}})
			}
			if w.Logger != nil {
				w.Logger.WarnContext(ctx, "youtube studio event projection failed", "error", err)
			}
		}
	}
	return nil
}
