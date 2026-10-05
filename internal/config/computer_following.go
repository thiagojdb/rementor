package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/thiagojdb/rementor/internal/models"
)

func LoadComputerFollowing() (models.ComputerFollowing, error) {
	db, err := readyDB()
	if err != nil {
		return models.ComputerFollowing{}, err
	}
	defer db.Close()
	var revision uint64
	var raw string
	err = db.QueryRow("SELECT revision,state_json FROM computer_following WHERE id=1").Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ComputerFollowing{}, nil
	}
	if err != nil {
		return models.ComputerFollowing{}, err
	}
	var state models.ComputerFollowing
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return state, err
	}
	if state.Revision != revision {
		return state, errors.New("invalid computer following revision")
	}
	return state, nil
}
func SaveComputerFollowing(workspaces []*models.Workspace, following models.ComputerFollowing, expected uint64) error {
	if following.Revision != expected+1 {
		return errors.New("invalid computer following revision")
	}
	return replaceWorkspaces(workspaces, &following, expected)
}
func saveComputerFollowingTx(tx *sql.Tx, state *models.ComputerFollowing, expected uint64) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	var result sql.Result
	if expected == 0 {
		result, err = tx.Exec("INSERT INTO computer_following(id,revision,state_json) VALUES(1,?,?) ON CONFLICT(id) DO NOTHING", state.Revision, string(raw))
	} else {
		result, err = tx.Exec("UPDATE computer_following SET revision=?,state_json=? WHERE id=1 AND revision=?", state.Revision, string(raw), expected)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrFleetConflict
	}
	return nil
}
