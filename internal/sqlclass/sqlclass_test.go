package sqlclass

import (
	"errors"
	"testing"
)

func TestClassify(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		sql    string
		want   Kind
		reject bool
	}{
		{sql: "SELECT 1", want: Read},
		{sql: "select id from t where x=1;", want: Read},
		{sql: "(SELECT 1) UNION (SELECT 2)", want: Read},
		{sql: "SHOW TABLES", want: Read},
		{sql: "SHOW COLUMNS FROM t", want: Read},
		{sql: "DESCRIBE t", want: Read},
		{sql: "DESC t", want: Read},
		{sql: "EXPLAIN SELECT * FROM t", want: Read},
		{sql: "WITH c AS (SELECT 1) SELECT * FROM c", want: Read},

		{sql: "INSERT INTO t VALUES (1)", want: Insert},
		{sql: "REPLACE INTO t VALUES (1)", want: Insert},
		{sql: "INSERT INTO t VALUES (1) ON DUPLICATE KEY UPDATE a = 2", want: Upsert},
		{sql: "UPDATE t SET a=1", want: Update},
		{sql: "DELETE FROM t", want: Delete},
		{sql: "WITH c AS (SELECT 1) DELETE FROM t", want: Delete},

		{sql: "CREATE TABLE t (a int)", want: DDL},
		{sql: "ALTER TABLE t ADD COLUMN b int", want: DDL},
		{sql: "DROP TABLE t", want: DDL},
		{sql: "TRUNCATE TABLE t", want: DDL},
		{sql: "RENAME TABLE a TO b", want: DDL},
		{sql: "CREATE INDEX i ON t(a)", want: DDL},
		{sql: "CREATE VIEW v AS SELECT 1", want: DDL},
		{sql: "DROP VIEW v", want: DDL},
		{sql: "CREATE TABLE t2 AS SELECT * FROM t", want: DDL},
		{sql: "/*!50000 DROP TABLE t */", want: DDL},
		{sql: "/*!99999 DROP TABLE t */", want: DDL},
		{sql: "SELECT 1 /*!80401 INTO OUTFILE '/tmp/pwn.txt' */", reject: true},
		{sql: "SELECT * FROM t /*!80401 FOR UPDATE */", reject: true},
		{sql: "SELECT * FROM t /*!90000 FOR UPDATE */", reject: true},

		{sql: "SELECT GET_LOCK('x', 0)", reject: true},
		{sql: "SELECT IS_USED_LOCK('x')", reject: true},
		{sql: "SELECT SLEEP(10)", reject: true},
		{sql: "SELECT BENCHMARK(100000000, MD5('a'))", reject: true},
		{sql: "SELECT LOAD_FILE('/etc/passwd')", reject: true},
		{sql: "SELECT SOURCE_POS_WAIT('f', 4)", reject: true},
		{sql: "SELECT MASTER_POS_WAIT('f', 4)", reject: true},
		{sql: "SELECT WAIT_FOR_EXECUTED_GTID_SET('x:1')", reject: true},
		{sql: "SELECT * FROM t WHERE id = (SELECT GET_LOCK('x', 0))", reject: true},
		{sql: "UPDATE t SET a = SLEEP(5)", reject: true},
		{sql: "SELECT COUNT(*), MD5(name), NOW() FROM t", want: Read},

		{sql: "SELECT 1; DROP TABLE t", reject: true},
		{sql: "SELECT 1; SELECT 2", reject: true},
		{sql: "", reject: true},
		{sql: "   ", reject: true},
		{sql: "-- only comment", reject: true},
		{sql: "not sql at all", reject: true},
		{sql: "SELECT * FROM t INTO OUTFILE '/tmp/x'", reject: true},
		{sql: "SELECT a INTO @v FROM t", reject: true},
		{sql: "SELECT * FROM t FOR UPDATE", reject: true},
		{sql: "SELECT * FROM t LOCK IN SHARE MODE", reject: true},
		{sql: "SELECT * FROM t FOR SHARE", reject: true},
		{sql: "EXPLAIN DELETE FROM t", reject: true},
		{sql: "EXPLAIN ANALYZE UPDATE t SET a=1", reject: true},
		{sql: "CREATE DATABASE d", reject: true},
		{sql: "DROP DATABASE d", reject: true},
		{sql: "ALTER DATABASE d CHARACTER SET utf8mb4", reject: true},
		{sql: "CREATE USER u", reject: true},
		{sql: "DROP USER u", reject: true},
		{sql: "GRANT SELECT ON *.* TO u", reject: true},
		{sql: "REVOKE SELECT ON *.* FROM u", reject: true},
		{sql: "CREATE PROCEDURE p() SELECT 1", reject: true},
		{sql: "DROP PROCEDURE p", reject: true},
		{sql: "CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW SET @a=1", reject: true},
		{sql: "CREATE EVENT e ON SCHEDULE EVERY 1 DAY DO SELECT 1", reject: true},
		{sql: "SET @a=1", reject: true},
		{sql: "SET GLOBAL x=1", reject: true},
		{sql: "USE d", reject: true},
		{sql: "CALL p()", reject: true},
		{sql: "LOCK TABLES t READ", reject: true},
		{sql: "LOAD DATA INFILE 'x' INTO TABLE t", reject: true},
		{sql: "BEGIN", reject: true},
		{sql: "COMMIT", reject: true},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			got, err := c.Classify(tt.sql)
			if tt.reject {
				var re *RejectError
				if !errors.As(err, &re) {
					t.Fatalf("Classify(%q) = %v, %v; want *RejectError", tt.sql, got, err)
				}
				if re.Reason == "" {
					t.Fatalf("Classify(%q): empty reject reason", tt.sql)
				}
				return
			}
			if err != nil {
				t.Fatalf("Classify(%q) error: %v", tt.sql, err)
			}
			if got != tt.want {
				t.Fatalf("Classify(%q) = %v, want %v", tt.sql, got, tt.want)
			}
		})
	}
}
