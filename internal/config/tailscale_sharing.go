package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/thiagojdb/rementor/internal/tailshare"
)

type TailscaleSharingStore struct{}

func (TailscaleSharingStore) Load() (tailshare.Configuration, error) {
	db, err := readyDB()
	if err != nil {
		return tailshare.Configuration{}, err
	}
	defer db.Close()
	var c tailshare.Configuration
	var raw string
	err = db.QueryRow("SELECT version, state_json FROM tailscale_sharing WHERE id=1").Scan(&c.Version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func (TailscaleSharingStore) Save(c tailshare.Configuration, expected uint64) (tailshare.Configuration, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if expected >= math.MaxInt64 || c.Revision > math.MaxInt64 {
		return c, errors.New("publication version exceeds storage range")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	db, err := readyDB()
	if err != nil {
		return c, err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return c, err
	}
	var result sql.Result
	if expected == 0 {
		result, err = db.Exec("INSERT INTO tailscale_sharing(id,version,state_json) VALUES(1,1,?) ON CONFLICT(id) DO NOTHING", string(raw))
	} else {
		result, err = db.Exec("UPDATE tailscale_sharing SET version=version+1,state_json=? WHERE id=1 AND version=?", string(raw), expected)
	}
	if err != nil {
		return c, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return c, err
	}
	if count != 1 {
		return c, tailshare.ErrConflict
	}
	c.Version = expected + 1
	return c, nil
}
