package services

import (
	"fmt"
	"github.com/thiagojdb/rementor/internal/models"
	"reflect"
	"time"
)

// A probe may outlive a routing mutation. Apply its result to the current
// binding only when that binding still names the endpoints actually checked.
func (r *Registry) refreshApplicationHealth(workspace *models.Workspace, app *models.Application) {
	observed := cloneWorkspace(workspace)
	var target *models.Application
	for _, binding := range observed.Applications {
		if binding.ID == app.ID {
			target = binding
			break
		}
	}
	if target == nil {
		return
	}
	localOK := r.checkLocalApplicationHealth(target)
	remoteBase := ""
	if !observed.IsLocalApps() {
		remoteBase = target.GetRemoteBaseUrl(observed)
	}
	remoteOK := false
	if remoteBase != "" {
		remoteOK = CheckHealth(target.RemoteHealthURL(remoteBase))
	}
	now := time.Now()
	var remoteChecked *time.Time
	if remoteBase != "" {
		remoteChecked = &now
	}
	r.workspacesMu.Lock()
	current := findWorkspace(r.workspaces, observed.WorkspaceID)
	if current == nil {
		r.workspacesMu.Unlock()
		return
	}
	var binding *models.Application
	for _, item := range current.Applications {
		if item.ID == target.ID {
			binding = item
			break
		}
	}
	currentRemote := ""
	if !current.IsLocalApps() && binding != nil {
		currentRemote = binding.GetRemoteBaseUrl(current)
	}
	if binding == nil || binding.Port != target.Port || binding.Health != target.Health || binding.BackendContextPath() != target.BackendContextPath() || !reflect.DeepEqual(binding.PeerEndpoint, target.PeerEndpoint) || currentRemote != remoteBase || binding.RemoteHealthURL(currentRemote) != target.RemoteHealthURL(remoteBase) {
		r.workspacesMu.Unlock()
		return
	}
	binding.Runtime.UpdateBothStatuses(localOK, &now, remoteOK, remoteChecked)
	r.workspacesMu.Unlock()
	update := models.HealthUpdate{WsID: observed.WorkspaceID, AppName: target.ID, LocalOk: localOK, RemoteOk: remoteOK, LocalChecked: now}
	if remoteChecked != nil {
		update.RemoteChecked = *remoteChecked
	}
	r.publishHealth(update)
}

// Each pass is bounded, and a newly opened workspace gets its own immediate
// pass rather than waiting behind every environment's background checks.
func (r *Registry) scheduleHealthChecks(workspaces []*models.Workspace, foreground bool) {
	r.healthProbeMu.Lock()
	if r.healthProbes == nil {
		r.healthProbes = make(map[string]bool)
		r.healthForeground = make(chan struct{}, 8)
		r.healthBackground = make(chan struct{}, 2)
	}
	slots := r.healthBackground
	if foreground {
		slots = r.healthForeground
	}
	r.healthProbeMu.Unlock()
	go func() {
		for _, workspace := range workspaces {
			observed := cloneWorkspace(workspace)
			for _, app := range observed.Applications {
				key := fmt.Sprintf("%s/%s", observed.WorkspaceID, app.ID)
				r.healthProbeMu.Lock()
				if r.healthProbes[key] {
					r.healthProbeMu.Unlock()
					continue
				}
				r.healthProbes[key] = true
				r.healthProbeMu.Unlock()
				select {
				case slots <- struct{}{}:
				case <-r.stopChan:
					r.healthProbeMu.Lock()
					delete(r.healthProbes, key)
					r.healthProbeMu.Unlock()
					return
				}
				go func(w *models.Workspace, a *models.Application, key string) {
					defer func() { <-slots; r.healthProbeMu.Lock(); delete(r.healthProbes, key); r.healthProbeMu.Unlock() }()
					r.refreshApplicationHealth(w, a)
				}(observed, app, key)
			}
		}
	}()
}

func (r *Registry) RefreshWorkspaceHealth(id string) {
	if id == "" {
		r.scheduleHealthChecks(r.workspaceSnapshot(), true)
		return
	}
	if workspace := r.GetWorkspaceView(id); workspace != nil {
		r.scheduleHealthChecks([]*models.Workspace{workspace}, true)
	}
}
