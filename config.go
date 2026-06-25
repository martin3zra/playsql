package playsql

import (
	"fmt"
	"strings"
	"time"
)

// Driver identifies a database/sql driver. The value must match the name the
// driver registered with database/sql (and that grammar.For understands).
type Driver string

const (
	SQLite    Driver = "sqlite"
	Postgres  Driver = "postgres"
	MySQL     Driver = "mysql"
	SQLServer Driver = "sqlserver"
)

// Config describes a connection. It is injected by the caller — the package
// reads no environment variables of its own.
type Config struct {
	Driver   Driver
	Host     string
	Port     int
	Database string // for SQLite, the file path or ":memory:"
	Username string
	Password string

	// Options are appended as driver-specific key=value pairs where supported.
	Options map[string]string

	// Pool settings (zero = database/sql default).
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DSN builds the data-source name for the configured driver.
func (c Config) DSN() (string, error) {
	switch c.Driver {
	case SQLite:
		if c.Database == "" {
			return "", fmt.Errorf("playsql: sqlite config requires Database (path or \":memory:\")")
		}
		return c.Database, nil

	case Postgres:
		parts := []string{
			kv("host", c.Host),
			kv("port", itoaNonZero(c.Port)),
			kv("dbname", c.Database),
			kv("user", c.Username),
			kv("password", c.Password),
		}
		if _, ok := c.Options["sslmode"]; !ok {
			parts = append(parts, "sslmode=disable")
		}
		for k, v := range c.Options {
			parts = append(parts, kv(k, v))
		}
		return strings.Join(nonEmpty(parts), " "), nil

	case MySQL:
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
			c.Username, c.Password, c.Host, c.Port, c.Database)
		return dsn, nil

	default:
		return "", fmt.Errorf("playsql: DSN not implemented for driver %q", c.Driver)
	}
}

func kv(k, v string) string {
	if v == "" {
		return ""
	}
	return k + "=" + v
}

func itoaNonZero(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func nonEmpty(s []string) []string {
	out := s[:0]
	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
