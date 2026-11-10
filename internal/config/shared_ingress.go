package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/thiagojdb/rementor/internal/shared"
)

// SharedIngressStore captures this instance's existing SQLite path once. Peer
// tests and isolated daemons can use separate stores without consulting another
// instance's runtime environment when a request arrives.
type SharedIngressStore struct{ path string }

func NewSharedIngressStore() (*SharedIngressStore, error) {
	db, err := readyDB()
	if err != nil {
		return nil, err
	}
	db.Close()
	return &SharedIngressStore{path: GetDBFile()}, nil
}
func (s *SharedIngressStore) open() (*sql.DB, error) {
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func (s *SharedIngressStore) Load() (shared.State, error) {
	db, err := s.open()
	if err != nil {
		return shared.State{}, err
	}
	defer db.Close()
	var revision uint64
	var raw string
	err = db.QueryRow("SELECT revision,state_json FROM shared_ingress WHERE id=1").Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return shared.State{}, nil
	}
	if err != nil {
		return shared.State{}, err
	}
	if len(raw) > 4<<20 {
		return shared.State{}, errors.New("shared ingress state exceeds 4 MiB")
	}
	var state shared.State
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, fmt.Errorf("read shared ingress state: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return state, errors.New("unexpected data after shared ingress state")
	}
	if state.Revision != revision {
		return state, errors.New("shared ingress storage revision mismatch")
	}
	return state, nil
}
func (s *SharedIngressStore) CompareAndSwap(state shared.State, expected uint64) (shared.State, error) {
	if expected >= math.MaxInt64 {
		return shared.State{}, errors.New("shared ingress storage revision exceeds its range")
	}
	state = state.Clone()
	state.Revision = expected + 1
	raw, err := json.Marshal(state)
	if err != nil {
		return shared.State{}, err
	}
	if len(raw) > 4<<20 {
		return shared.State{}, errors.New("shared ingress state exceeds 4 MiB")
	}
	db, err := s.open()
	if err != nil {
		return shared.State{}, err
	}
	defer db.Close()
	var result sql.Result
	if expected == 0 {
		result, err = db.Exec("INSERT INTO shared_ingress(id,revision,state_json) VALUES(1,1,?) ON CONFLICT(id) DO NOTHING", string(raw))
	} else {
		result, err = db.Exec("UPDATE shared_ingress SET revision=?,state_json=? WHERE id=1 AND revision=?", state.Revision, string(raw), expected)
	}
	if err != nil {
		return shared.State{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return shared.State{}, err
	}
	if count != 1 {
		return shared.State{}, shared.ErrConflict
	}
	return state, nil
}
