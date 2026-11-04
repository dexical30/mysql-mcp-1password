package sanitize

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func newS() *Sanitizer { return New("db.internal", "app_user", "3307") }

func TestMessage_AccessDenied(t *testing.T) {
	err := &mysql.MySQLError{Number: 1045, Message: "Access denied for user 'app_user'@'10.0.0.12' (using password: YES)"}
	if got := newS().Message(err); got != "DB 인증 실패" {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_DialRefused(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 3306}, Err: syscall.ECONNREFUSED}
	if got := newS().Message(err); got != "DB 연결 실패: connection refused" {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_DNS(t *testing.T) {
	err := &net.DNSError{Err: "no such host", Name: "db.internal"}
	if got := newS().Message(err); got != "DB 연결 실패: no such host" {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_Timeout(t *testing.T) {
	if got := newS().Message(context.DeadlineExceeded); got != "쿼리 시간 초과" {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_OtherMySQLError(t *testing.T) {
	err := &mysql.MySQLError{Number: 1146, Message: "Table 'shop.x' doesn't exist"}
	if got := newS().Message(err); got != "MySQL 1146: Table 'shop.x' doesn't exist" {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_ScrubsLeftovers(t *testing.T) {
	err := errors.New("failed at db.internal:3307 as app_user from 192.168.0.5 and fe80::1")
	got := newS().Message(err)
	for _, s := range []string{"db.internal", "app_user", "3307", "192.168.0.5", "fe80::1"} {
		if strings.Contains(got, s) {
			t.Fatalf("message still contains %q: %s", s, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected [redacted] marker: %s", got)
	}
}

func TestMessage_WrappedErrors(t *testing.T) {
	err := fmt.Errorf("x: %w", &mysql.MySQLError{Number: 1045, Message: "Access denied for user 'app_user'@'h'"})
	if got := newS().Message(err); got != "DB 인증 실패" {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_MySQLErrorScrubbed(t *testing.T) {
	err := &mysql.MySQLError{Number: 1142, Message: "SELECT command denied to user 'app_user'@'10.0.0.12' for table 'x'"}
	got := newS().Message(err)
	if strings.Contains(got, "app_user") || strings.Contains(got, "10.0.0.12") {
		t.Fatalf("leaks user or IP: %s", got)
	}
	if !strings.HasPrefix(got, "MySQL 1142: ") {
		t.Fatalf("got %q", got)
	}
}

type redactingErr struct {
	msg    string
	values []string
}

func (e *redactingErr) Error() string        { return e.msg }
func (e *redactingErr) Redactions() []string { return e.values }

func TestMessage_UsesRedactionsCarriedByError(t *testing.T) {
	inner := &redactingErr{
		msg:    "x509: certificate is valid for a.example.com, not db.internal.corp",
		values: []string{"db.internal.corp", "app_user", "3306"},
	}
	got := New().Message(fmt.Errorf("unlock: %w", inner))
	if strings.Contains(got, "db.internal.corp") {
		t.Fatalf("host leaked: %s", got)
	}
}
