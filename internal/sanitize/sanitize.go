// Package sanitize turns errors into messages safe to return over MCP.
// Claude Code stores tool results on disk, so host names, IPs, ports,
// user names and DSNs must not appear in them.
package sanitize

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"syscall"

	"github.com/go-sql-driver/mysql"
)

const marker = "[redacted]"

var (
	userAtHost = regexp.MustCompile(`'[^']*'@'[^']*'`)
	ipv4       = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	ipv6Cand   = regexp.MustCompile(`[0-9A-Fa-f]*:[0-9A-Fa-f:]+`)
)

type Sanitizer struct{ redact []string }

// New returns a sanitizer that also scrubs the given literal values.
// Values shorter than 3 characters are ignored, since scrubbing them would
// mangle ordinary text.
func New(redact ...string) *Sanitizer {
	s := &Sanitizer{}
	for _, r := range redact {
		if len(r) >= 3 {
			s.redact = append(s.redact, r)
		}
	}
	return s
}

// Message classifies err and scrubs it. Errors may carry their own values to
// scrub by implementing Redactions() []string (for example a failed unlock,
// whose host and user are no longer known to the caller).
func (s *Sanitizer) Message(err error) string {
	var carrier interface{ Redactions() []string }
	if errors.As(err, &carrier) {
		s = New(append(append([]string(nil), s.redact...), carrier.Redactions()...)...)
	}
	return s.scrub(classify(err))
}

func classify(err error) string {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		if myErr.Number == 1045 {
			return "DB 인증 실패"
		}
		return fmt.Sprintf("MySQL %d: %s", myErr.Number, myErr.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "쿼리 시간 초과"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DB 연결 실패: " + dnsErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var errno syscall.Errno
		if errors.As(opErr.Err, &errno) {
			return "DB 연결 실패: " + errno.Error()
		}
		if opErr.Timeout() {
			return "DB 연결 실패: i/o timeout"
		}
		return "DB 연결 실패"
	}
	return err.Error()
}

func (s *Sanitizer) scrub(msg string) string {
	msg = userAtHost.ReplaceAllString(msg, marker)
	for _, r := range s.redact {
		msg = strings.ReplaceAll(msg, r, marker)
	}
	msg = ipv4.ReplaceAllString(msg, marker)
	return ipv6Cand.ReplaceAllStringFunc(msg, func(tok string) string {
		if net.ParseIP(strings.Trim(tok, ":")) != nil || net.ParseIP(tok) != nil {
			return marker
		}
		return tok
	})
}
