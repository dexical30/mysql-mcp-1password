package session

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/dexical30/mysql-mcp-1password/internal/opcred"
)

// MySQLOpener connects with a pool sized for a single interactive user.
// Without TLS, the driver fetches the server's RSA key for the first
// caching_sha2_password login on its own (documented MITM risk).
func MySQLOpener(tls bool) Opener {
	return func(ctx context.Context, c *opcred.Credentials) (*sql.DB, error) {
		cfg := mysql.NewConfig()
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		cfg.User = c.User
		cfg.Passwd = string(c.Password)
		cfg.DBName = c.Database
		cfg.Timeout = 10 * time.Second
		cfg.MultiStatements = false
		cfg.ParseTime = false
		cfg.InterpolateParams = false
		if tls {
			cfg.TLSConfig = "true"
		}
		connector, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		db := sql.OpenDB(connector)
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
}
