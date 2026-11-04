package session

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dexical30/mysql-mcp-1password/internal/opcred"
)

// fakeClock fires AfterFunc callbacks only when Advance moves past them.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Duration
	f       func()
	stopped bool
	fired   bool
	clk     *fakeClock
}

func (t *fakeTimer) Stop() bool {
	t.clk.mu.Lock()
	defer t.clk.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now + d, f: f, clk: c}
	c.timers = append(c.timers, t)
	return t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []func()
	for _, t := range c.timers {
		if !t.stopped && !t.fired && t.at <= c.now {
			t.fired = true
			due = append(due, t.f)
		}
	}
	c.mu.Unlock()
	for _, f := range due {
		f()
	}
}

type fakeFetcher struct {
	calls atomic.Int32
	block chan struct{} // if non-nil, Fetch waits for it to close
	err   error
	host  atomic.Value // string
	last  atomic.Pointer[opcred.Credentials]
}

func (f *fakeFetcher) Fetch(ctx context.Context) (*opcred.Credentials, error) {
	f.calls.Add(1)
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return nil, f.err
	}
	host, _ := f.host.Load().(string)
	if host == "" {
		host = "h1"
	}
	c := &opcred.Credentials{Host: host, Port: 3306, User: "u", Password: []byte("pw")}
	f.last.Store(c)
	return c, nil
}

type openRecord struct {
	mu    sync.Mutex
	hosts []string
}

func (r *openRecord) opener() Opener {
	return func(ctx context.Context, c *opcred.Credentials) (*sql.DB, error) {
		r.mu.Lock()
		r.hosts = append(r.hosts, c.Host)
		r.mu.Unlock()
		return nil, nil
	}
}

func newTestGate(f *fakeFetcher) (*Gate, *fakeClock, *openRecord) {
	clk := &fakeClock{}
	rec := &openRecord{}
	return NewGate(f, rec.opener(), 15*time.Minute, clk, func(string, ...any) {}), clk, rec
}

func noop(*sql.DB) error { return nil }

func TestGate_NoFetchUntilFirstDo(t *testing.T) {
	f := &fakeFetcher{}
	newTestGate(f)
	if n := f.calls.Load(); n != 0 {
		t.Fatalf("Fetch called %d times before any request", n)
	}
}

func TestGate_FirstDoFetchesOnce(t *testing.T) {
	f := &fakeFetcher{}
	g, _, _ := newTestGate(f)
	for i := 0; i < 2; i++ {
		if err := g.Do(context.Background(), noop); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("Fetch called %d times, want 1", n)
	}
}

func TestGate_SingleFlight(t *testing.T) {
	f := &fakeFetcher{block: make(chan struct{})}
	g, _, _ := newTestGate(f)
	var ran atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Do(context.Background(), func(*sql.DB) error { ran.Add(1); return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(f.block)
	wg.Wait()
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("Fetch called %d times, want 1", n)
	}
	if n := ran.Load(); n != 10 {
		t.Fatalf("fn ran %d times, want 10", n)
	}
}

func TestGate_FetchFailureStaysLocked(t *testing.T) {
	denied := errors.New("authorization denied")
	f := &fakeFetcher{block: make(chan struct{}), err: denied}
	g, _, _ := newTestGate(f)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- g.Do(context.Background(), noop) }()
	}
	time.Sleep(50 * time.Millisecond)
	close(f.block)
	for i := 0; i < 2; i++ {
		if err := <-errs; !errors.Is(err, denied) {
			t.Fatalf("Do error = %v, want %v", err, denied)
		}
	}
	before := f.calls.Load()
	f.err = nil
	if err := g.Do(context.Background(), noop); err != nil {
		t.Fatalf("Do after failure: %v", err)
	}
	if f.calls.Load() != before+1 {
		t.Fatal("expected a fresh Fetch after a failed unlock")
	}
}

func TestGate_LocksAfterIdle(t *testing.T) {
	f := &fakeFetcher{}
	g, clk, _ := newTestGate(f)
	_ = g.Do(context.Background(), noop)
	clk.Advance(14*time.Minute + 59*time.Second)
	_ = g.Do(context.Background(), noop)
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("locked too early: Fetch called %d times", n)
	}
	clk.Advance(15 * time.Minute)
	_ = g.Do(context.Background(), noop)
	if n := f.calls.Load(); n != 2 {
		t.Fatalf("Fetch called %d times after idle, want 2", n)
	}
}

func TestGate_IdleTimerResetsOnEachRequest(t *testing.T) {
	f := &fakeFetcher{}
	g, clk, _ := newTestGate(f)
	_ = g.Do(context.Background(), noop)
	clk.Advance(10 * time.Minute)
	_ = g.Do(context.Background(), noop)
	clk.Advance(10 * time.Minute)
	_ = g.Do(context.Background(), noop)
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("Fetch called %d times, want 1", n)
	}
}

func TestGate_DoesNotLockWhileInFlight(t *testing.T) {
	f := &fakeFetcher{}
	g, clk, _ := newTestGate(f)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		done <- g.Do(context.Background(), func(*sql.DB) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	clk.Advance(20 * time.Minute)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = g.Do(context.Background(), noop)
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("locked while a request was running: Fetch called %d times", n)
	}
	clk.Advance(15 * time.Minute)
	_ = g.Do(context.Background(), noop)
	if n := f.calls.Load(); n != 2 {
		t.Fatalf("Fetch called %d times, want 2", n)
	}
}

func TestGate_RefetchesAfterLock(t *testing.T) {
	f := &fakeFetcher{}
	g, clk, rec := newTestGate(f)
	_ = g.Do(context.Background(), noop)
	clk.Advance(15 * time.Minute)
	f.host.Store("h2")
	_ = g.Do(context.Background(), noop)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.hosts) != 2 || rec.hosts[1] != "h2" {
		t.Fatalf("opener hosts = %v, want [h1 h2]", rec.hosts)
	}
}

func TestGate_WipesCredentials(t *testing.T) {
	f := &fakeFetcher{}
	g, _, _ := newTestGate(f)
	_ = g.Do(context.Background(), noop)
	c := f.last.Load()
	if c == nil {
		t.Fatal("no credentials fetched")
	}
	if c.Password != nil || c.Host != "" {
		t.Fatalf("credentials not wiped: %+v", c)
	}
}

func TestGate_RedactionsOnlyWhileUnlocked(t *testing.T) {
	f := &fakeFetcher{}
	g, clk, _ := newTestGate(f)
	if r := g.Redactions(); r != nil {
		t.Fatalf("Redactions before unlock = %v", r)
	}
	_ = g.Do(context.Background(), noop)
	r := g.Redactions()
	if len(r) != 3 || r[0] != "h1" || r[1] != "u" || r[2] != "3306" {
		t.Fatalf("Redactions = %v", r)
	}
	clk.Advance(15 * time.Minute)
	if r := g.Redactions(); r != nil {
		t.Fatalf("Redactions after lock = %v", r)
	}
}

func TestGate_ContextCancelWhileUnlocking(t *testing.T) {
	f := &fakeFetcher{block: make(chan struct{})}
	defer close(f.block)
	g, _, _ := newTestGate(f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- g.Do(ctx, noop) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Do did not return after its context was cancelled")
	}
}

func TestGate_OpenErrorCarriesRedactions(t *testing.T) {
	f := &fakeFetcher{}
	g := NewGate(f, func(ctx context.Context, c *opcred.Credentials) (*sql.DB, error) {
		return nil, errors.New("x509: certificate is valid for a.example.com, not h1")
	}, 15*time.Minute, &fakeClock{}, func(string, ...any) {})
	err := g.Do(context.Background(), noop)
	var r interface{ Redactions() []string }
	if !errors.As(err, &r) {
		t.Fatalf("unlock error %v does not carry redactions", err)
	}
	got := r.Redactions()
	if len(got) != 3 || got[0] != "h1" || got[1] != "u" || got[2] != "3306" {
		t.Fatalf("Redactions = %v", got)
	}
}
