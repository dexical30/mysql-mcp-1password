package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
	"github.com/dexical30/mysql-mcp-1password/internal/query"
	"github.com/dexical30/mysql-mcp-1password/internal/sqlclass"
)

type fakeRunner struct {
	calls  atomic.Int32
	redact []string
}

func (r *fakeRunner) Do(ctx context.Context, fn func(*sql.DB) error) error {
	r.calls.Add(1)
	return fn(nil)
}

func (r *fakeRunner) Redactions() []string { return r.redact }

type calls struct{ read, write atomic.Int32 }

func fakeExecutor(c *calls, readErr error) Executor {
	return Executor{
		Read: func(ctx context.Context, db *sql.DB, q string, maxRows int) (*query.ReadResult, error) {
			c.read.Add(1)
			if readErr != nil {
				return nil, readErr
			}
			return &query.ReadResult{Columns: []string{"1"}, Rows: [][]any{{int64(1)}}, RowCount: 1}, nil
		},
		Write: func(ctx context.Context, db *sql.DB, q string) (*query.WriteResult, error) {
			c.write.Add(1)
			return &query.WriteResult{AffectedRows: 1}, nil
		},
	}
}

func baseConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "MYSQL_OP_ITEM" {
			return "op://Dev/mysql"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func connect(t *testing.T, cfg config.Config, r Runner, ex Executor) *mcp.ClientSession {
	t.Helper()
	cls, err := sqlclass.New()
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, cls, r, ex)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, sql string) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "mysql_query", Arguments: map[string]any{"sql": sql}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(res.Content))
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func TestTools_ListHasSingleReadOnlyTool(t *testing.T) {
	cs := connect(t, baseConfig(t), &fakeRunner{}, fakeExecutor(&calls{}, nil))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "mysql_query" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	tool := res.Tools[0]
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatal("ReadOnlyHint should be true")
	}
	if !strings.Contains(tool.Description, "SELECT") || !strings.Contains(tool.Description, "limited to 100 rows") || strings.Contains(tool.Description, "INSERT") {
		t.Fatalf("description = %q", tool.Description)
	}
}

func TestTools_WriteFlagChangesHintAndDescription(t *testing.T) {
	cfg := baseConfig(t)
	cfg.AllowInsert = true
	cs := connect(t, cfg, &fakeRunner{}, fakeExecutor(&calls{}, nil))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := res.Tools[0]
	if tool.Annotations.ReadOnlyHint || !strings.Contains(tool.Description, "INSERT") {
		t.Fatalf("hint=%v description=%q", tool.Annotations.ReadOnlyHint, tool.Description)
	}
}

func TestCall_ReadReturnsJSON(t *testing.T) {
	cs := connect(t, baseConfig(t), &fakeRunner{}, fakeExecutor(&calls{}, nil))
	text, isErr := call(t, cs, "SELECT 1")
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var r query.ReadResult
	if err := json.Unmarshal([]byte(text), &r); err != nil || r.RowCount != 1 {
		t.Fatalf("result %q: %v", text, err)
	}
}

func TestCall_RejectedNeverTouchesGate(t *testing.T) {
	r := &fakeRunner{}
	cs := connect(t, baseConfig(t), r, fakeExecutor(&calls{}, nil))
	for _, q := range []string{"DROP TABLE t", "SELECT 1; SELECT 2", "SET @a=1", "DELETE FROM t"} {
		text, isErr := call(t, cs, q)
		if !isErr {
			t.Fatalf("%q was accepted: %s", q, text)
		}
	}
	if n := r.calls.Load(); n != 0 {
		t.Fatalf("gate used %d times for rejected statements", n)
	}
}

func TestCall_DisabledKindNamesFlag(t *testing.T) {
	cs := connect(t, baseConfig(t), &fakeRunner{}, fakeExecutor(&calls{}, nil))
	text, _ := call(t, cs, "DELETE FROM t")
	if !strings.Contains(text, "MYSQL_ALLOW_DELETE=true") {
		t.Fatalf("message should name the flag: %s", text)
	}
}

func TestCall_WriteRoutedWhenAllowed(t *testing.T) {
	cfg := baseConfig(t)
	cfg.AllowDelete = true
	c := &calls{}
	cs := connect(t, cfg, &fakeRunner{}, fakeExecutor(c, nil))
	if text, isErr := call(t, cs, "DELETE FROM t"); isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if c.write.Load() != 1 || c.read.Load() != 0 {
		t.Fatalf("write=%d read=%d", c.write.Load(), c.read.Load())
	}
}

func TestCall_ErrorsAreSanitized(t *testing.T) {
	r := &fakeRunner{redact: []string{"db.internal", "app_user", "3306"}}
	cs := connect(t, baseConfig(t), r, fakeExecutor(&calls{}, errors.New("dial tcp db.internal:3306 as app_user")))
	text, isErr := call(t, cs, "SELECT 1")
	if !isErr {
		t.Fatal("want error result")
	}
	for _, s := range r.redact {
		if strings.Contains(text, s) {
			t.Fatalf("error leaks %q: %s", s, text)
		}
	}
}

func TestCall_EmptySQL(t *testing.T) {
	r := &fakeRunner{}
	cs := connect(t, baseConfig(t), r, fakeExecutor(&calls{}, nil))
	if _, isErr := call(t, cs, ""); !isErr {
		t.Fatal("empty SQL accepted")
	}
	if r.calls.Load() != 0 {
		t.Fatal("gate used for empty SQL")
	}
}

type slowUnlockRunner struct{ fakeRunner }

func (r *slowUnlockRunner) Do(ctx context.Context, fn func(*sql.DB) error) error {
	time.Sleep(150 * time.Millisecond) // waiting for Touch ID
	return fn(nil)
}

func TestCall_QueryTimeoutExcludesUnlockWait(t *testing.T) {
	cfg := baseConfig(t)
	cfg.QueryTimeout = 100 * time.Millisecond
	ex := Executor{
		Read: func(ctx context.Context, db *sql.DB, q string, maxRows int) (*query.ReadResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &query.ReadResult{}, nil
		},
	}
	cs := connect(t, cfg, &slowUnlockRunner{}, ex)
	if text, isErr := call(t, cs, "SELECT 1"); isErr {
		t.Fatalf("query timed out while waiting for unlock: %s", text)
	}
}

func TestCall_UpsertNeedsInsertAndUpdate(t *testing.T) {
	const q = "INSERT INTO t VALUES (1) ON DUPLICATE KEY UPDATE a = 2"
	cfg := baseConfig(t)
	cfg.AllowInsert = true
	cs := connect(t, cfg, &fakeRunner{}, fakeExecutor(&calls{}, nil))
	text, isErr := call(t, cs, q)
	if !isErr || !strings.Contains(text, "MYSQL_ALLOW_UPDATE=true") {
		t.Fatalf("upsert with only INSERT enabled: isErr=%v %s", isErr, text)
	}

	cfg.AllowUpdate = true
	c := &calls{}
	cs = connect(t, cfg, &fakeRunner{}, fakeExecutor(c, nil))
	if text, isErr := call(t, cs, q); isErr || c.write.Load() != 1 {
		t.Fatalf("upsert with both flags: isErr=%v %s", isErr, text)
	}
}
