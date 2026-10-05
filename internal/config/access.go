package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/thiagojdb/rementor/internal/access"
)

var ErrPublicAccessConflict = errors.New("public access configuration changed; reload and try again")
var accessWriteMu sync.Mutex

func LoadPublicAccessConfiguration() (access.Configuration, error) {
	db, err := readyDB()
	if err != nil {
		return access.Configuration{}, err
	}
	defer db.Close()
	var result access.Configuration
	var raw string
	err = db.QueryRow("SELECT revision, document_json FROM public_access_configuration WHERE id = 1").Scan(&result.Revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return access.Configuration{Document: access.Empty()}, nil
	}
	if err != nil {
		return result, err
	}
	doc, err := access.Decode(strings.NewReader(raw))
	if err != nil {
		return result, fmt.Errorf("read public access configuration: %w", err)
	}
	result.Document = doc
	return result, nil
}

// No external provider changes occur in this store. The registry validates the
// selected workspace hosts under its mutation lock before calling this CAS.
func SavePublicAccessConfiguration(doc access.Document, expected uint64) (access.Configuration, error) {
	if err := doc.Validate(); err != nil {
		return access.Configuration{}, err
	}
	if expected >= math.MaxInt64 {
		return access.Configuration{}, errors.New("public access revision exceeds the storage range")
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return access.Configuration{}, err
	}
	accessWriteMu.Lock()
	defer accessWriteMu.Unlock()
	db, err := readyDB()
	if err != nil {
		return access.Configuration{}, err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		return access.Configuration{}, err
	}
	var result sql.Result
	if expected == 0 {
		result, err = db.Exec("INSERT INTO public_access_configuration(id, revision, document_json) VALUES(1, 1, ?) ON CONFLICT(id) DO NOTHING", string(raw))
	} else {
		result, err = db.Exec("UPDATE public_access_configuration SET revision = revision + 1, document_json = ? WHERE id = 1 AND revision = ?", string(raw), expected)
	}
	if err != nil {
		return access.Configuration{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return access.Configuration{}, err
	}
	if count != 1 {
		return access.Configuration{}, ErrPublicAccessConflict
	}
	return access.Configuration{Revision: expected + 1, Document: doc.Clone()}, nil
}
