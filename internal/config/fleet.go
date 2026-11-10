package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/thiagojdb/rementor/internal/routing"
)

var ErrFleetConflict = errors.New("fleet configuration changed; reload and review again")

var fleetWriteMu sync.Mutex

type FleetConfiguration struct {
	Revision uint64
	Topology routing.Topology
}

func initialFleetTopology() routing.Topology {
	return routing.Topology{
		Version: 1, Nodes: []routing.Node{{ID: "this-device"}},
		Instances:      []routing.Instance{{ID: "local", NodeID: "this-device"}},
		SharedInstance: "local", LocalInstance: "local",
	}
}

func LoadFleetConfiguration() (FleetConfiguration, error) {
	db, err := readyDB()
	if err != nil {
		return FleetConfiguration{}, err
	}
	defer db.Close()
	var result FleetConfiguration
	var raw string
	err = db.QueryRow("SELECT revision, topology_json FROM fleet_configuration WHERE id = 1").Scan(&result.Revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return FleetConfiguration{Topology: initialFleetTopology()}, nil
	}
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(raw), &result.Topology); err != nil {
		return result, fmt.Errorf("read fleet configuration: %w", err)
	}
	_, err = routing.Compile(result.Topology)
	return result, err
}

// SaveFleetConfiguration only stores intent. It never changes nginx, workspaces
// or session baselines. The conditional write prevents stale browser reviews
// from overwriting another window's edits.
func SaveFleetConfiguration(topology routing.Topology, expected uint64) (FleetConfiguration, error) {
	if _, err := routing.Compile(topology); err != nil {
		return FleetConfiguration{}, err
	}
	raw, err := json.Marshal(topology)
	if err != nil {
		return FleetConfiguration{}, err
	}
	fleetWriteMu.Lock()
	defer fleetWriteMu.Unlock()
	db, err := readyDB()
	if err != nil {
		return FleetConfiguration{}, err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		return FleetConfiguration{}, err
	}
	var result sql.Result
	if expected == 0 {
		result, err = db.Exec("INSERT INTO fleet_configuration (id, revision, topology_json) VALUES (1, 1, ?) ON CONFLICT(id) DO NOTHING", string(raw))
	} else {
		result, err = db.Exec("UPDATE fleet_configuration SET revision = revision + 1, topology_json = ? WHERE id = 1 AND revision = ?", string(raw), expected)
	}
	if err != nil {
		return FleetConfiguration{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return FleetConfiguration{}, err
	}
	if count != 1 {
		return FleetConfiguration{}, ErrFleetConflict
	}
	return FleetConfiguration{Revision: expected + 1, Topology: topology}, nil
}
