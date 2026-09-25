package dbstatus

import (
	"context"
	"database/sql"
	"time"
)

type Database interface {
	Stats() sql.DBStats
	PingContext(ctx context.Context) error
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

type Status struct {
	OK             bool      `json:"ok"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Database       string    `json:"database,omitempty"`
	User           string    `json:"user,omitempty"`
	Target         string    `json:"target,omitempty"`
	OpenConns      int       `json:"open_conns"`
	InUse          int       `json:"in_use"`
	Idle           int       `json:"idle"`
	Error          string    `json:"error,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	LatencyMS      int64     `json:"latency_ms"`
	SelectVerified bool      `json:"select_verified"`
}

func Check(db Database) Status {
	st := Status{CheckedAt: time.Now()}
	if db == nil {
		st.Error = "database pool is nil"
		return st
	}
	stats := db.Stats()
	st.OpenConns = stats.OpenConnections
	st.InUse = stats.InUse
	st.Idle = stats.Idle

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := db.PingContext(ctx); err != nil {
		st.LatencyMS = time.Since(start).Milliseconds()
		st.Error = err.Error()
		return st
	}
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		st.LatencyMS = time.Since(start).Milliseconds()
		st.Error = err.Error()
		return st
	}
	st.LatencyMS = time.Since(start).Milliseconds()
	st.SelectVerified = one == 1
	st.OK = st.SelectVerified
	return st
}
