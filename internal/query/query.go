// Package query executes one already-classified statement and returns a
// JSON-serialisable result.
package query

import (
	"context"
	"database/sql"
	"time"
)

type ReadResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Truncated bool     `json:"truncated"`
	ElapsedMs int64    `json:"elapsedMs"`
}

type WriteResult struct {
	AffectedRows int64 `json:"affectedRows"`
	LastInsertID int64 `json:"lastInsertId"`
	ElapsedMs    int64 `json:"elapsedMs"`
}

// RunRead executes q inside a READ ONLY transaction that is always rolled
// back, so the database itself refuses writes even if classification erred.
func RunRead(ctx context.Context, db *sql.DB, q string, maxRows int) (*ReadResult, error) {
	start := time.Now()
	// Cancelling this context when truncating makes the driver drop the
	// connection instead of reading every remaining row to discard it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	res := &ReadResult{Columns: make([]string, len(types)), Rows: [][]any{}}
	for i, ct := range types {
		res.Columns[i] = ct.Name()
	}

	raw := make([]sql.RawBytes, len(types))
	dest := make([]any, len(types))
	for i := range raw {
		dest[i] = &raw[i]
	}
	for rows.Next() {
		if res.RowCount == maxRows {
			res.Truncated = true
			cancel()
			break
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make([]any, len(types))
		for i, ct := range types {
			row[i] = convert(ct.DatabaseTypeName(), raw[i])
		}
		res.Rows = append(res.Rows, row)
		res.RowCount++
	}
	if err := rows.Err(); err != nil && !res.Truncated {
		return nil, err
	}
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// RunWrite executes q in a transaction and commits it.
func RunWrite(ctx context.Context, db *sql.DB, q string) (*WriteResult, error) {
	start := time.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	r, err := tx.ExecContext(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	affected, _ := r.RowsAffected()
	lastID, _ := r.LastInsertId()
	return &WriteResult{AffectedRows: affected, LastInsertID: lastID, ElapsedMs: time.Since(start).Milliseconds()}, nil
}
