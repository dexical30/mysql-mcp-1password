//go:build integration

package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/dexical30/mysql-mcp-1password/internal/opcred"
	"github.com/dexical30/mysql-mcp-1password/internal/session"
)

var db *sql.DB

func TestMain(m *testing.M) {
	creds := &opcred.Credentials{Host: "127.0.0.1", Port: 13306, User: "root", Password: []byte("itest"), Database: "itest"}
	var err error
	db, err = session.MySQLOpener(false)(context.Background(), creds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration MySQL not reachable (run scripts/itest-mysql.sh):", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func seed(t *testing.T) {
	t.Helper()
	stmts := []string{
		"DROP TABLE IF EXISTS items",
		"CREATE TABLE items (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(50), big BIGINT, price DECIMAL(10,2), data BLOB)",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	b.WriteString("INSERT INTO items (name, big, price, data) VALUES ")
	for i := 1; i <= 1500; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "('n%d', 9007199254740993, 1.10, 0xff00)", i)
	}
	if _, err := db.Exec(b.String()); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunRead_Basic(t *testing.T) {
	seed(t)
	r, err := RunRead(context.Background(), db, "SELECT id, name, big, price, data FROM items WHERE id <= 2 ORDER BY id", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Columns, ",") != "id,name,big,price,data" || r.RowCount != 2 || r.Truncated {
		t.Fatalf("got %+v", r)
	}
	row := r.Rows[0]
	if row[0] != int64(1) || row[1] != "n1" || row[2] != "9007199254740993" || row[3] != "1.10" || row[4] != "base64:/wA=" {
		t.Fatalf("row = %#v", row)
	}
}

func TestRunRead_TruncatesAtMaxRows(t *testing.T) {
	seed(t)
	start := time.Now()
	r, err := RunRead(context.Background(), db, "SELECT id FROM items", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r.RowCount != 1000 || len(r.Rows) != 1000 || !r.Truncated {
		t.Fatalf("RowCount=%d len=%d Truncated=%v", r.RowCount, len(r.Rows), r.Truncated)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestRunRead_ReadOnlyTxBlocksWrite(t *testing.T) {
	seed(t)
	_, err := RunRead(context.Background(), db, "DELETE FROM items", 1000)
	var myErr *mysql.MySQLError
	if !errors.As(err, &myErr) || myErr.Number != 1792 {
		t.Fatalf("err = %v, want MySQL 1792", err)
	}
	if n := count(t); n != 1500 {
		t.Fatalf("rows = %d, want 1500", n)
	}
}

func TestRunRead_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := RunRead(ctx, db, "SELECT SLEEP(5)", 1000)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestRunWrite_InsertCommits(t *testing.T) {
	seed(t)
	r, err := RunWrite(context.Background(), db, "INSERT INTO items(name) VALUES ('x')")
	if err != nil {
		t.Fatal(err)
	}
	if r.AffectedRows != 1 || r.LastInsertID != 1501 {
		t.Fatalf("got %+v", r)
	}
	if n := count(t); n != 1501 {
		t.Fatalf("rows = %d, want 1501", n)
	}
}

func TestRunWrite_FailureRollsBack(t *testing.T) {
	seed(t)
	if _, err := RunWrite(context.Background(), db, "INSERT INTO items(id, name) VALUES (1, 'dup')"); err == nil {
		t.Fatal("want duplicate key error")
	}
	if n := count(t); n != 1500 {
		t.Fatalf("rows = %d, want 1500", n)
	}
}

func TestRunRead_TruncationDoesNotDrainHugeResult(t *testing.T) {
	seed(t)
	start := time.Now()
	r, err := RunRead(context.Background(), db, "SELECT a.id FROM items a, items b, items c", 10)
	if err != nil {
		t.Fatal(err)
	}
	if r.RowCount != 10 || !r.Truncated {
		t.Fatalf("RowCount=%d Truncated=%v", r.RowCount, r.Truncated)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("truncated read of 3.4B rows took %v; remaining rows were drained", d)
	}
}
