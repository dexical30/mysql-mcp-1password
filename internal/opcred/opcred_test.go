package opcred

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dexical30/mysql-mcp-1password/internal/config"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/item.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixtureValues are the secret-ish values in testdata/item.json; none of
// them may leak into an error message.
var fixtureValues = []string{"fake-db.example.internal", "fake_user", "fake-p@ss word", "fake_shop"}

func assertNoLeak(t *testing.T, msg string) {
	t.Helper()
	for _, v := range fixtureValues {
		if strings.Contains(msg, v) {
			t.Fatalf("error message leaks %q: %s", v, msg)
		}
	}
}

func TestParseItem_DatabaseTemplate(t *testing.T) {
	c, err := parseItem(fixture(t), config.DefaultFieldMap())
	if err != nil {
		t.Fatalf("parseItem: %v", err)
	}
	if c.Host != "fake-db.example.internal" || c.Port != 3307 || c.User != "fake_user" ||
		string(c.Password) != "fake-p@ss word" || c.Database != "fake_shop" {
		t.Fatalf("got %+v", c)
	}
}

func TestParseItem_MatchesByIdOrLabelCaseInsensitive(t *testing.T) {
	byLabel := `{"fields":[{"id":"x1","label":"Server","value":"h1"},{"id":"username","value":"u"},{"id":"password","value":"p"}]}`
	byID := `{"fields":[{"id":"hostname","label":"something","value":"h2"},{"label":"USERNAME","value":"u"},{"label":"Password","value":"p"}]}`
	for raw, want := range map[string]string{byLabel: "h1", byID: "h2"} {
		c, err := parseItem([]byte(raw), config.DefaultFieldMap())
		if err != nil {
			t.Fatalf("parseItem(%s): %v", raw, err)
		}
		if c.Host != want {
			t.Fatalf("Host = %q, want %q", c.Host, want)
		}
	}
}

func TestParseItem_DefaultsPortAndEmptyDatabase(t *testing.T) {
	raw := `{"fields":[{"label":"server","value":"h"},{"label":"port","value":""},{"label":"username","value":"u"},{"label":"password","value":"p"}]}`
	c, err := parseItem([]byte(raw), config.DefaultFieldMap())
	if err != nil {
		t.Fatalf("parseItem: %v", err)
	}
	if c.Port != 3306 || c.Database != "" {
		t.Fatalf("Port=%d Database=%q", c.Port, c.Database)
	}
}

func TestParseItem_MissingRequired(t *testing.T) {
	raw := strings.Replace(string(fixture(t)), `"label": "password"`, `"label": "secret"`, 1)
	raw = strings.Replace(raw, `"id": "password"`, `"id": "secret"`, 1)
	_, err := parseItem([]byte(raw), config.DefaultFieldMap())
	if err == nil {
		t.Fatal("want error for missing password")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("error should name the missing field: %v", err)
	}
	assertNoLeak(t, err.Error())
}

func TestParseItem_BadPort(t *testing.T) {
	raw := `{"fields":[{"label":"server","value":"h"},{"label":"port","value":"abc"},{"label":"username","value":"u"},{"label":"password","value":"p"}]}`
	_, err := parseItem([]byte(raw), config.DefaultFieldMap())
	if err == nil {
		t.Fatal("want error for non-numeric port")
	}
	if strings.Contains(err.Error(), "abc") {
		t.Fatalf("error leaks port value: %v", err)
	}
}

func TestFetch_ArgsAndNoStdin(t *testing.T) {
	var gotName string
	var gotArgs []string
	s := NewSource("/fake/op", "Dev", "mysql-prod", config.DefaultFieldMap())
	s.resolve = func(p string) (string, error) { return p, nil }
	s.run = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName, gotArgs = name, args
		return fixture(t), nil, nil
	}
	if _, err := s.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := []string{"item", "get", "mysql-prod", "--vault", "Dev", "--format", "json"}
	if gotName != "/fake/op" || !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("ran %s %v, want /fake/op %v", gotName, gotArgs, want)
	}
}

func TestFetch_OpFailureMessage(t *testing.T) {
	s := NewSource("/fake/op", "Dev", "mysql-prod", config.DefaultFieldMap())
	s.resolve = func(p string) (string, error) { return p, nil }
	s.run = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return fixture(t), []byte("[ERROR] authorization prompt dismissed\n"), errors.New("exit status 1")
	}
	_, err := s.Fetch(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "authorization prompt dismissed") {
		t.Fatalf("error should carry op stderr: %v", err)
	}
	assertNoLeak(t, err.Error())
}

func TestCredentials_Wipe(t *testing.T) {
	pw := []byte("secret")
	c := &Credentials{Host: "h", Port: 1, User: "u", Password: pw, Database: "d"}
	c.Wipe()
	for i, b := range pw {
		if b != 0 {
			t.Fatalf("byte %d not wiped", i)
		}
	}
	if c.Password != nil || c.Host != "" || c.User != "" || c.Database != "" || c.Port != 0 {
		t.Fatalf("fields not cleared: %+v", c)
	}
}

func TestFetch_VerifiesOpBeforeEachCall(t *testing.T) {
	resolves, runs := 0, 0
	s := NewSource("/fake/op", "Dev", "mysql-prod", config.DefaultFieldMap())
	s.run = func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		runs++
		return fixture(t), nil, nil
	}
	s.resolve = func(p string) (string, error) { resolves++; return p, nil }
	for i := 0; i < 2; i++ {
		if _, err := s.Fetch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if resolves != 2 {
		t.Fatalf("op verified %d times for 2 fetches", resolves)
	}

	s.resolve = func(string) (string, error) { return "", errors.New("not signed by 1Password") }
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch ran with an unverified op")
	}
	if runs != 2 {
		t.Fatalf("op executed %d times, want 2 (not after failed verification)", runs)
	}
}

func TestExecRun_ReturnsWhenChildHoldsPipe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	execRun(ctx, "/bin/sh", "-c", "sleep 20 &")
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("execRun blocked %v on a pipe held by a child process", d)
	}
}
