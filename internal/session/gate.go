// Package session holds the database connection behind a lock: credentials
// are fetched from 1Password on the first request, and everything is dropped
// again after a period with no requests.
package session

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"time"

	"github.com/dexical30/mysql-mcp-1password/internal/opcred"
)

type Fetcher interface {
	Fetch(ctx context.Context) (*opcred.Credentials, error)
}

type Opener func(ctx context.Context, c *opcred.Credentials) (*sql.DB, error)

type Clock interface {
	AfterFunc(d time.Duration, f func()) Timer
}

type Timer interface{ Stop() bool }

type realClock struct{}

func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

var RealClock Clock = realClock{}

// UnlockError is a failed connection attempt that knows which connection
// details must be scrubbed from its message.
type UnlockError struct {
	Err    error
	redact []string
}

func (e *UnlockError) Error() string        { return e.Err.Error() }
func (e *UnlockError) Unwrap() error        { return e.Err }
func (e *UnlockError) Redactions() []string { return e.redact }

// attempt is one in-progress unlock shared by every request waiting on it.
type attempt struct {
	done chan struct{}
	err  error
}

type Gate struct {
	fetch Fetcher
	open  Opener
	idle  time.Duration
	clk   Clock
	logf  func(string, ...any)

	mu       sync.Mutex
	unlocked bool
	db       *sql.DB
	redact   []string
	pending  *attempt
	inflight int
	timer    Timer
	timerGen int
}

func NewGate(f Fetcher, open Opener, idle time.Duration, clk Clock, logf func(string, ...any)) *Gate {
	return &Gate{fetch: f, open: open, idle: idle, clk: clk, logf: logf}
}

// Do runs fn with an open database, unlocking first if needed.
func (g *Gate) Do(ctx context.Context, fn func(db *sql.DB) error) error {
	db, err := g.acquire(ctx)
	if err != nil {
		return err
	}
	defer g.release()
	return fn(db)
}

func (g *Gate) acquire(ctx context.Context) (*sql.DB, error) {
	for {
		g.mu.Lock()
		if g.unlocked {
			g.inflight++
			g.stopTimerLocked()
			db := g.db
			g.mu.Unlock()
			return db, nil
		}
		a := g.pending
		if a == nil {
			a = &attempt{done: make(chan struct{})}
			g.pending = a
			go g.unlock(a)
		}
		g.mu.Unlock()

		select {
		case <-a.done:
			if a.err != nil {
				return nil, a.err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// unlock runs detached from any single request so that one caller giving up
// does not fail the others waiting on the same Touch ID prompt.
func (g *Gate) unlock(a *attempt) {
	ctx := context.Background()
	db, redact, err := g.connect(ctx)

	g.mu.Lock()
	g.pending = nil
	if err != nil {
		a.err = err
		g.logf("unlock failed")
	} else {
		g.unlocked, g.db, g.redact = true, db, redact
		if g.inflight == 0 {
			g.armTimerLocked()
		}
		g.logf("unlocked")
	}
	g.mu.Unlock()
	close(a.done)
}

func (g *Gate) connect(ctx context.Context) (*sql.DB, []string, error) {
	creds, err := g.fetch.Fetch(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer creds.Wipe()
	redact := []string{creds.Host, creds.User, strconv.Itoa(creds.Port)}
	db, err := g.open(ctx, creds)
	if err != nil {
		// The caller sanitizes errors with the gate's redactions, which are
		// empty while locked, so the failing attempt's values travel along.
		return nil, nil, &UnlockError{Err: err, redact: redact}
	}
	return db, redact, nil
}

func (g *Gate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inflight--
	if g.inflight == 0 && g.unlocked {
		g.armTimerLocked()
	}
}

func (g *Gate) armTimerLocked() {
	g.stopTimerLocked()
	g.timerGen++
	gen := g.timerGen
	g.timer = g.clk.AfterFunc(g.idle, func() { g.onIdle(gen) })
}

func (g *Gate) stopTimerLocked() {
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	g.timerGen++
}

func (g *Gate) onIdle(gen int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen != g.timerGen || g.inflight > 0 || !g.unlocked {
		return
	}
	g.lockLocked()
}

// Lock closes the database and forgets the connection details.
func (g *Gate) Lock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.unlocked {
		g.lockLocked()
	}
}

func (g *Gate) lockLocked() {
	g.stopTimerLocked()
	if g.db != nil {
		g.db.Close()
	}
	g.unlocked, g.db, g.redact = false, nil, nil
	g.logf("locked")
}

// Redactions returns values that must be scrubbed from errors while unlocked.
func (g *Gate) Redactions() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.unlocked {
		return nil
	}
	return append([]string(nil), g.redact...)
}
