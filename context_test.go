package pdf

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// triggerCtx is a context.Context whose Done() channel is open for the first
// `limit` calls and closed on subsequent calls. This makes the in-loop
// `select { case <-ctx.Done(): ... default: }` deterministically fire after
// exactly `limit` per-page checks, without relying on goroutine timing.
type triggerCtx struct {
	mu     sync.Mutex
	limit  int
	polls  int
	closed chan struct{}
	once   sync.Once
}

func newTriggerCtx(limit int) *triggerCtx {
	return &triggerCtx{limit: limit, closed: make(chan struct{})}
}

func (c *triggerCtx) Done() <-chan struct{} {
	c.mu.Lock()
	c.polls++
	fired := c.polls > c.limit
	c.mu.Unlock()
	if fired {
		c.once.Do(func() { close(c.closed) })
		return c.closed
	}
	return make(chan struct{}) // open channel; select takes default
}

func (c *triggerCtx) Err() error {
	select {
	case <-c.closed:
		return context.Canceled
	default:
		return nil
	}
}

func (c *triggerCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *triggerCtx) Value(_ any) any             { return nil }

func TestGetPlainTextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := buildMinimalPDF("")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	_, err = r.GetPlainText(ctx)
	if err != context.Canceled {
		t.Errorf("GetPlainText with cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestGetStyledTextsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := buildMinimalPDF("")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	_, err = r.GetStyledTexts(ctx)
	if err != context.Canceled {
		t.Errorf("GetStyledTexts with cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestGetPlainTextBackground(t *testing.T) {
	data := buildMinimalPDF("")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	result, err := r.GetPlainText(context.Background())
	if err != nil {
		t.Errorf("GetPlainText with Background ctx: unexpected error %v", err)
	}
	if result == nil {
		t.Error("GetPlainText with Background ctx: got nil reader")
	}
}

func TestGetStyledTextsBackground(t *testing.T) {
	data := buildMinimalPDF("")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	_, err = r.GetStyledTexts(context.Background())
	if err != nil {
		t.Errorf("GetStyledTexts with Background ctx: unexpected error %v", err)
	}
}

// TestGetPlainTextMidwayCancelled verifies that the in-loop select fires:
// page 1 is allowed through (limit=1 → first Done() call returns open channel),
// page 2 triggers cancellation (second Done() call returns closed channel).
func TestGetPlainTextMidwayCancelled(t *testing.T) {
	data := buildOutlinePDF(3, "", 0, 0)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	if got := r.NumPage(); got != 3 {
		t.Fatalf("fixture NumPage = %d, want 3", got)
	}
	// limit=1: page 1's Done() call (poll 1) returns open → continues;
	// page 2's Done() call (poll 2) returns closed → returns context.Canceled.
	ctx := newTriggerCtx(1)
	_, err = r.GetPlainText(ctx)
	if err != context.Canceled {
		t.Errorf("GetPlainText midway: got %v, want context.Canceled", err)
	}
}

// TestGetStyledTextsMidwayCancelled mirrors TestGetPlainTextMidwayCancelled
// for GetStyledTexts.
func TestGetStyledTextsMidwayCancelled(t *testing.T) {
	data := buildOutlinePDF(3, "", 0, 0)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	if got := r.NumPage(); got != 3 {
		t.Fatalf("fixture NumPage = %d, want 3", got)
	}
	ctx := newTriggerCtx(1)
	_, err = r.GetStyledTexts(ctx)
	if err != context.Canceled {
		t.Errorf("GetStyledTexts midway: got %v, want context.Canceled", err)
	}
}

func TestGetPlainTextReturnsReader(t *testing.T) {
	data := buildOutlinePDF(1, "", 0, 0)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	result, err := r.GetPlainText(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := io.ReadAll(result); err != nil {
		t.Errorf("reading result: %v", err)
	}
}
