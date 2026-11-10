package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/thiagojdb/rementor/internal/publication"
)

type TailnetPublishingStore struct{}

func (TailnetPublishingStore) Load() (publication.Configuration, error) {
	db, err := readyDB()
	if err != nil {
		return publication.Configuration{}, err
	}
	defer db.Close()
	var revision uint64
	var raw string
	err = db.QueryRow("SELECT revision,state_json FROM tailnet_publishing WHERE id=1").Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return publication.Configuration{}, nil
	}
	if err != nil {
		return publication.Configuration{}, err
	}
	var c publication.Configuration
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	c.Revision = revision
	return c, c.Validate()
}
func (TailnetPublishingStore) Save(c publication.Configuration, expected uint64) (publication.Configuration, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if expected >= math.MaxInt64 || c.Revision != expected+1 {
		return c, errors.New("invalid publication revision")
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
		result, err = db.Exec("INSERT INTO tailnet_publishing(id,revision,state_json) VALUES(1,1,?) ON CONFLICT(id) DO NOTHING", string(raw))
	} else {
		result, err = db.Exec("UPDATE tailnet_publishing SET revision=revision+1,state_json=? WHERE id=1 AND revision=?", string(raw), expected)
	}
	if err != nil {
		return c, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return c, err
	}
	if n != 1 {
		return c, publication.ErrConflict
	}
	return c, nil
}
