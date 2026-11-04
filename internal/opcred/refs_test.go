package opcred

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
)

var testRefs = config.Refs{
	Host:     "127.0.0.1",
	Port:     "op://Dev/mysql-dev/MYSQL_PORT",
	User:     "op://Dev/mysql-dev/MYSQL_USER",
	Password: "op://Dev/mysql-dev/MYSQL_PASS",
	Database: "op://Dev/mysql-dev/MYSQL_DB",
}

var refValues = map[string]string{
	"op://Dev/mysql-dev/MYSQL_PORT": "3307",
	"op://Dev/mysql-dev/MYSQL_USER": "fake_user",
	"op://Dev/mysql-dev/MYSQL_PASS": "fake-p@ss word",
	"op://Dev/mysql-dev/MYSQL_DB":   "fake_shop",
}

func fakeRefRunner(calls *[][]string) runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		*calls = append(*calls, args)
		ref := args[len(args)-1]
		v, ok := refValues[ref]
		if !ok {
			return nil, []byte("[ERROR] could not find item"), errors.New("exit status 1")
		}
		return []byte(v), nil, nil
	}
}

func TestRefSource_ReadsEachReference(t *testing.T) {
	var calls [][]string
	s := NewRefSource("/fake/op", testRefs)
	s.resolve = func(p string) (string, error) { return p, nil }
	s.run = fakeRefRunner(&calls)

	c, err := s.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if c.Host != "127.0.0.1" || c.Port != 3307 || c.User != "fake_user" ||
		string(c.Password) != "fake-p@ss word" || c.Database != "fake_shop" {
		t.Fatalf("got %+v", c)
	}
	// The literal host is used as is; only op:// values reach op.
	if len(calls) != 4 {
		t.Fatalf("op called %d times, want 4", len(calls))
	}
	want := []string{"read", "--no-newline", "op://Dev/mysql-dev/MYSQL_PASS"}
	found := false
	for _, a := range calls {
		if reflect.DeepEqual(a, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no call with args %v in %v", want, calls)
	}
}

func TestRefSource_OptionalPortAndDatabase(t *testing.T) {
	var calls [][]string
	s := NewRefSource("/fake/op", config.Refs{Host: "h", User: "u", Password: "op://Dev/mysql-dev/MYSQL_PASS"})
	s.resolve = func(p string) (string, error) { return p, nil }
	s.run = fakeRefRunner(&calls)
	c, err := s.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if c.Port != 3306 || c.Database != "" {
		t.Fatalf("Port=%d Database=%q", c.Port, c.Database)
	}
}

func TestRefSource_FailureNamesVariable(t *testing.T) {
	var calls [][]string
	refs := testRefs
	refs.Password = "op://Dev/missing/MYSQL_PASS"
	s := NewRefSource("/fake/op", refs)
	s.resolve = func(p string) (string, error) { return p, nil }
	s.run = fakeRefRunner(&calls)
	_, err := s.Fetch(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "MYSQL_PASS") || !strings.Contains(err.Error(), "could not find item") {
		t.Fatalf("error should name the variable and carry op stderr: %v", err)
	}
	for _, v := range refValues {
		if strings.Contains(err.Error(), v) {
			t.Fatalf("error leaks %q: %v", v, err)
		}
	}
}

func TestRefSource_BadPort(t *testing.T) {
	var calls [][]string
	refs := testRefs
	refs.Port = "abc"
	s := NewRefSource("/fake/op", refs)
	s.resolve = func(p string) (string, error) { return p, nil }
	s.run = fakeRefRunner(&calls)
	_, err := s.Fetch(context.Background())
	if err == nil || strings.Contains(err.Error(), "abc") {
		t.Fatalf("want port error without the value, got %v", err)
	}
}

func TestRefSource_VerifiesOpFirst(t *testing.T) {
	var calls [][]string
	s := NewRefSource("/fake/op", testRefs)
	s.resolve = func(string) (string, error) { return "", errors.New("not signed by 1Password") }
	s.run = fakeRefRunner(&calls)
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch ran with an unverified op")
	}
	if len(calls) != 0 {
		t.Fatalf("op executed %d times before verification", len(calls))
	}
}
