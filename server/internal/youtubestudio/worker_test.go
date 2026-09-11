package youtubestudio

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workerTestQueries struct {
	event       db.YoutubeStudioOutbox
	claimErr    error
	stateErr    error
	failedCalls int
	deadCalls   int
}

func (q *workerTestQueries) ClaimYouTubeStudioEvent(context.Context, pgtype.Timestamptz) (db.YoutubeStudioOutbox, error) {
	if q.claimErr != nil {
		err := q.claimErr
		q.claimErr = pgx.ErrNoRows
		return db.YoutubeStudioOutbox{}, err
	}
	event := q.event
	q.claimErr = pgx.ErrNoRows
	return event, nil
}

func (*workerTestQueries) WithTx(pgx.Tx) *db.Queries { return nil }

func (q *workerTestQueries) MarkYouTubeStudioEventFailed(context.Context, db.MarkYouTubeStudioEventFailedParams) error {
	q.failedCalls++
	return q.stateErr
}

func (q *workerTestQueries) MarkYouTubeStudioEventDeadLettered(context.Context, db.MarkYouTubeStudioEventDeadLetteredParams) error {
	q.deadCalls++
	return q.stateErr
}

type workerTestTxStarter struct{ err error }

func (s workerTestTxStarter) Begin(context.Context) (pgx.Tx, error) { return nil, s.err }

type workerTestHandler struct {
	mu   sync.Mutex
	text []string
}

func (h *workerTestHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *workerTestHandler) WithGroup(string) slog.Handler            { return h }
func (h *workerTestHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *workerTestHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
		return true
	})
	h.mu.Lock()
	h.text = append(h.text, b.String())
	h.mu.Unlock()
	return nil
}

func TestWorkerRunReportsReconcileErrors(t *testing.T) {
	h := &workerTestHandler{}
	w := &Worker{Queries: &workerTestQueries{claimErr: errors.New("claim failed")}, TxStarter: workerTestTxStarter{err: errors.New("unused")}, Logger: slog.New(h), Interval: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.text) == 0 || !strings.Contains(strings.Join(h.text, "\n"), "youtube studio reconciliation failed") {
		t.Fatalf("reconciliation error was not logged: %v", h.text)
	}
}

func TestWorkerReportsRetryStateUpdateFailureWithEventID(t *testing.T) {
	eventID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	q := &workerTestQueries{event: db.YoutubeStudioOutbox{EventID: eventID, AttemptCount: 0}, stateErr: errors.New("retry update failed")}
	h := &workerTestHandler{}
	w := &Worker{Queries: q, TxStarter: workerTestTxStarter{err: errors.New("projection failed")}, Logger: slog.New(h), MaxAttempts: 3}
	if err := w.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	joined := strings.Join(h.text, "\n")
	if q.failedCalls != 1 || !strings.Contains(joined, "youtube studio retry state update failed") || !strings.Contains(joined, "event_id") {
		t.Fatalf("retry failure log/state = calls %d/logs %v", q.failedCalls, h.text)
	}
}

func TestWorkerReportsDeadLetterStateUpdateFailureWithEventID(t *testing.T) {
	eventID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	q := &workerTestQueries{event: db.YoutubeStudioOutbox{EventID: eventID, AttemptCount: 9}, stateErr: errors.New("dead-letter update failed")}
	h := &workerTestHandler{}
	w := &Worker{Queries: q, TxStarter: workerTestTxStarter{err: errors.New("projection failed")}, Logger: slog.New(h), MaxAttempts: 10}
	if err := w.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	joined := strings.Join(h.text, "\n")
	if q.deadCalls != 1 || !strings.Contains(joined, "youtube studio dead-letter state update failed") || !strings.Contains(joined, "event_id") {
		t.Fatalf("dead-letter failure log/state = calls %d/logs %v", q.deadCalls, h.text)
	}
}

func TestWorkerUsesSafeDefaultLogger(t *testing.T) {
	if (&Worker{}).logger() == nil {
		t.Fatal("nil logger did not resolve to slog.Default")
	}
}
