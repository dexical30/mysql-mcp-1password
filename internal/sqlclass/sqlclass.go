// Package sqlclass decides what kind of statement a SQL string is, so the
// server can route reads, gate writes behind flags, and refuse everything
// else. It fails closed: anything it cannot positively classify is rejected.
package sqlclass

import (
	"errors"
	"strings"

	"vitess.io/vitess/go/vt/sqlparser"
)

// Kind is the operation class of an accepted statement.
type Kind int

const (
	Read Kind = iota
	Insert
	Update
	Delete
	DDL
	// Upsert is INSERT ... ON DUPLICATE KEY UPDATE: it inserts and can also
	// change existing rows, so it needs both the insert and update flags.
	Upsert
)

func (k Kind) String() string {
	switch k {
	case Read:
		return "READ"
	case Insert:
		return "INSERT"
	case Update:
		return "UPDATE"
	case Delete:
		return "DELETE"
	case DDL:
		return "DDL"
	case Upsert:
		return "UPSERT"
	}
	return "UNKNOWN"
}

// RejectError reports why a statement was refused.
type RejectError struct{ Reason string }

func (e *RejectError) Error() string { return "statement rejected: " + e.Reason }

func reject(reason string) (Kind, error) { return 0, &RejectError{Reason: reason} }

// Classifier wraps a MySQL-dialect parser.
type Classifier struct{ p *sqlparser.Parser }

// New returns a classifier that treats every version-gated /*!NNNNN ... */
// comment as code. The parser is told the highest possible server version,
// because any real server at or above NNNNN executes the comment's content
// and the classifier must see whatever the server might run.
func New() (*Classifier, error) {
	p, err := sqlparser.New(sqlparser.Options{MySQLServerVersion: "9.99.99"})
	if err != nil {
		return nil, err
	}
	return &Classifier{p: p}, nil
}

// Classify returns the Kind of a single statement, or a *RejectError.
func (c *Classifier) Classify(sql string) (Kind, error) {
	if strings.TrimSpace(sql) == "" {
		return reject("empty statement")
	}
	pieces, err := c.p.SplitStatementToPieces(sql)
	if err != nil {
		return reject("could not split statement")
	}
	if len(pieces) == 0 {
		return reject("empty statement")
	}
	if len(pieces) > 1 {
		return reject("multiple statements are not allowed")
	}
	stmt, err := c.p.Parse(sql)
	if errors.Is(err, sqlparser.ErrEmpty) {
		return reject("empty statement")
	}
	if err != nil {
		return reject("could not parse statement")
	}
	if name := sideEffectFunc(stmt); name != "" {
		return reject(name + " is not allowed")
	}
	return classify(stmt)
}

// deniedFuncs have effects that outlive the statement or the transaction
// (advisory locks stay held on the pooled connection after ROLLBACK), stall
// the server, or read server files.
var deniedFuncs = map[string]bool{
	"sleep": true, "benchmark": true, "load_file": true,
	"source_pos_wait": true, "master_pos_wait": true,
}

func sideEffectFunc(stmt sqlparser.SQLNode) string {
	var name string
	_ = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		switch n := node.(type) {
		case *sqlparser.LockingFunc:
			name = "advisory lock functions"
		case *sqlparser.GTIDFuncExpr:
			name = "GTID functions"
		case *sqlparser.FuncExpr:
			if deniedFuncs[n.Name.Lowered()] {
				name = n.Name.String() + "()"
			}
		}
		return name == "", nil
	}, stmt)
	return name
}

func classify(stmt sqlparser.Statement) (Kind, error) {
	switch s := stmt.(type) {
	case *sqlparser.Select, *sqlparser.Union:
		if reason := lockingOrInto(s); reason != "" {
			return reject(reason)
		}
		return Read, nil
	case *sqlparser.Show, *sqlparser.ExplainTab:
		return Read, nil
	case *sqlparser.ExplainStmt:
		if k, err := classify(s.Statement); err != nil || k != Read {
			return reject("EXPLAIN is only allowed for read statements")
		}
		return Read, nil
	case *sqlparser.Insert:
		if len(s.OnDup) > 0 {
			return Upsert, nil
		}
		return Insert, nil
	case *sqlparser.Update:
		return Update, nil
	case *sqlparser.Delete:
		return Delete, nil
	case *sqlparser.CreateTable, *sqlparser.AlterTable, *sqlparser.DropTable,
		*sqlparser.TruncateTable, *sqlparser.RenameTable,
		*sqlparser.CreateView, *sqlparser.AlterView, *sqlparser.DropView:
		return DDL, nil
	}
	return reject("statement type is not allowed")
}

// lockingOrInto walks every SELECT/UNION in the tree, including subqueries,
// because a locking read or an INTO target anywhere has side effects.
func lockingOrInto(stmt sqlparser.SQLNode) string {
	var reason string
	_ = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		switch n := node.(type) {
		case *sqlparser.Select:
			if n.Lock != sqlparser.NoLock {
				reason = "locking reads are not allowed"
			} else if n.Into != nil {
				reason = "SELECT ... INTO is not allowed"
			}
		case *sqlparser.Union:
			if n.Lock != sqlparser.NoLock {
				reason = "locking reads are not allowed"
			} else if n.Into != nil {
				reason = "SELECT ... INTO is not allowed"
			}
		}
		return reason == "", nil
	}, stmt)
	return reason
}
