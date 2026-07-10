package config

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

type Runtime struct {
	Version          int64
	DownloadAttempts int
	RetryBaseDelay   time.Duration
	MaxRedirects     int
}

func LoadRuntime(ctx context.Context, db *sql.DB) (Runtime, error) {
	result := Runtime{DownloadAttempts: 3, RetryBaseDelay: 300 * time.Millisecond, MaxRedirects: 5}
	if err := db.QueryRowContext(ctx, `SELECT version FROM config_meta WHERE id=1`).Scan(&result.Version); err != nil {
		return Runtime{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM runtime_config`)
	if err != nil {
		return Runtime{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Runtime{}, err
		}
		switch k {
		case "download_attempts":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 10 {
				return Runtime{}, fmt.Errorf("invalid download_attempts")
			}
			result.DownloadAttempts = n
		case "retry_base_delay":
			d, e := time.ParseDuration(v)
			if e != nil || d < 0 {
				return Runtime{}, fmt.Errorf("invalid retry_base_delay")
			}
			result.RetryBaseDelay = d
		case "max_redirects":
			n, e := strconv.Atoi(v)
			if e != nil || n < 0 || n > 20 {
				return Runtime{}, fmt.Errorf("invalid max_redirects")
			}
			result.MaxRedirects = n
		}
	}
	return result, rows.Err()
}

func UpdateRuntime(ctx context.Context, db *sql.DB, values map[string]string) (Runtime, error) {
	allowed := map[string]bool{"download_attempts": true, "retry_base_delay": true, "max_redirects": true}
	for k := range values {
		if !allowed[k] {
			return Runtime{}, fmt.Errorf("unsupported runtime setting %q", k)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Runtime{}, err
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return Runtime{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE config_meta SET version=version+1 WHERE id=1`); err != nil {
		return Runtime{}, err
	}
	if err = tx.Commit(); err != nil {
		return Runtime{}, err
	}
	return LoadRuntime(ctx, db)
}
