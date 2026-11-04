// Package server exposes a single mysql_query tool over MCP.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
	"github.com/dexical30/mysql-mcp-1password/internal/query"
	"github.com/dexical30/mysql-mcp-1password/internal/sanitize"
	"github.com/dexical30/mysql-mcp-1password/internal/sqlclass"
)

// Runner provides an unlocked database; session.Gate satisfies it.
type Runner interface {
	Do(ctx context.Context, fn func(db *sql.DB) error) error
	Redactions() []string
}

type Executor struct {
	Read  func(ctx context.Context, db *sql.DB, q string, maxRows int) (*query.ReadResult, error)
	Write func(ctx context.Context, db *sql.DB, q string) (*query.WriteResult, error)
}

type QueryInput struct {
	SQL string `json:"sql" jsonschema:"the single MySQL statement to execute"`
}

// Version is set at release time from the git tag via -ldflags -X.
var Version = "dev"

func New(cfg config.Config, cls *sqlclass.Classifier, r Runner, exec Executor) *mcp.Server {
	if exec.Read == nil {
		exec.Read = query.RunRead
	}
	if exec.Write == nil {
		exec.Write = query.RunWrite
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "mysql-mcp-1password", Version: Version}, nil)
	h := &handler{cfg: cfg, cls: cls, r: r, exec: exec}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "mysql_query",
		Description: Description(cfg),
		Annotations: &mcp.ToolAnnotations{Title: "MySQL Query", ReadOnlyHint: cfg.ReadOnly()},
	}, h.handle)
	return srv
}

// Description tells the model what the server will accept.
func Description(cfg config.Config) string {
	allowed := []string{"SELECT", "SHOW", "EXPLAIN", "DESCRIBE"}
	for _, w := range []struct {
		on   bool
		name string
	}{
		{cfg.AllowInsert, "INSERT/REPLACE"},
		{cfg.AllowUpdate, "UPDATE"},
		{cfg.AllowDelete, "DELETE"},
		{cfg.AllowDDL, "DDL on tables, indexes and views"},
	} {
		if w.on {
			allowed = append(allowed, w.name)
		}
	}
	return fmt.Sprintf("Run one MySQL statement. Allowed: %s. Results are limited to %d rows. "+
		"Multiple statements, SET, USE, CALL, locking reads and SELECT ... INTO are rejected.",
		strings.Join(allowed, ", "), cfg.MaxRows)
}

type handler struct {
	cfg  config.Config
	cls  *sqlclass.Classifier
	r    Runner
	exec Executor
}

func (h *handler) handle(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, any, error) {
	kind, err := h.cls.Classify(in.SQL)
	if err != nil {
		var re *sqlclass.RejectError
		if errors.As(err, &re) {
			return errorResult("Rejected: " + re.Reason), nil, nil
		}
		return errorResult("Rejected"), nil, nil
	}
	if !h.allowed(kind) {
		flags := "MYSQL_ALLOW_" + kind.String() + "=true"
		if kind == sqlclass.Upsert {
			flags = "MYSQL_ALLOW_INSERT=true and MYSQL_ALLOW_UPDATE=true"
		}
		return errorResult(fmt.Sprintf("%s is not enabled (set %s)", kind, flags)), nil, nil
	}

	var out any
	err = h.r.Do(ctx, func(db *sql.DB) error {
		// The timeout starts once unlocked, so time spent on the Touch ID
		// prompt does not count against the query.
		ctx, cancel := context.WithTimeout(ctx, h.cfg.QueryTimeout)
		defer cancel()
		var err error
		if kind == sqlclass.Read {
			out, err = h.exec.Read(ctx, db, in.SQL, h.cfg.MaxRows)
		} else {
			out, err = h.exec.Write(ctx, db, in.SQL)
		}
		return err
	})
	if err != nil {
		return errorResult(sanitize.New(h.r.Redactions()...).Message(err)), nil, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return errorResult("could not encode result"), nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

func (h *handler) allowed(k sqlclass.Kind) bool {
	switch k {
	case sqlclass.Read:
		return true
	case sqlclass.Insert:
		return h.cfg.AllowInsert
	case sqlclass.Update:
		return h.cfg.AllowUpdate
	case sqlclass.Delete:
		return h.cfg.AllowDelete
	case sqlclass.DDL:
		return h.cfg.AllowDDL
	case sqlclass.Upsert:
		return h.cfg.AllowInsert && h.cfg.AllowUpdate
	}
	return false
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
