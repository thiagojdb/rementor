package fleet

import "errors"
import "github.com/thiagojdb/rementor/internal/validation"
import "github.com/thiagojdb/rementor/internal/models"

var ErrConfigurationConflict = errors.New("source configuration changed; reload and review again")

// ConfigurationEdit changes the source's route intent, never a receiver override.
// Endpoint registrations remain in the source computer's network namespace.
type ConfigurationEdit struct {
	WorkspaceID      string                    `json:"workspaceId"`
	ApplicationID    string                    `json:"applicationId,omitempty"`
	AllApplications  bool                      `json:"allApplications,omitempty"`
	ExpectedVersion  uint64                    `json:"expectedVersion"`
	Mode             string                    `json:"mode,omitempty"`
	RoutePattern     *string                   `json:"routePattern,omitempty"`
	RemoteBaseURL    *string                   `json:"remoteBaseUrl,omitempty"`
	Action           string                    `json:"action,omitempty"`
	Application      *models.ApplicationConfig `json:"application,omitempty"`
	Routing          *models.RoutingConfig     `json:"routing,omitempty"`
	RegistrationPort *int                      `json:"registrationPort,omitempty"`
}

func (e ConfigurationEdit) Validate() error {
	if err := validation.Identifier("workspace ID", e.WorkspaceID); err != nil {
		return err
	}
	if e.Action != "" {
		if e.AllApplications || e.Mode != "" || e.RoutePattern != nil || e.RemoteBaseURL != nil {
			return errors.New("configuration action cannot include route selection fields")
		}
		if e.RegistrationPort != nil && (*e.RegistrationPort < 0 || *e.RegistrationPort > 65535) {
			return errors.New("registration port must be from 0 to 65535")
		}
		switch e.Action {
		case "upsert-application":
			if e.Application == nil || e.Routing != nil || e.Application.PeerEndpoint != nil || e.Application.Port != 0 {
				return errors.New("application metadata is required; registration must use registrationPort")
			}
			if e.ApplicationID != e.Application.CanonicalAppID() {
				return errors.New("application identity does not match")
			}
		case "register-port":
			if e.RegistrationPort == nil || e.Application != nil || e.Routing != nil {
				return errors.New("register-port requires only registrationPort")
			}
		case "delete-application":
			if e.Application != nil || e.Routing != nil || e.RegistrationPort != nil {
				return errors.New("delete-application accepts no settings")
			}
		case "update-routing", "delete-environment":
			if e.ApplicationID != "" || e.Application != nil || e.RegistrationPort != nil || (e.Action == "update-routing") != (e.Routing != nil) {
				return errors.New("invalid environment settings")
			}
			return nil
		default:
			return errors.New("unknown configuration action")
		}
		return validation.Identifier("application ID", e.ApplicationID)
	}
	if e.Application != nil || e.Routing != nil || e.RegistrationPort != nil {
		return errors.New("configuration fields require an action")
	}
	if e.AllApplications {
		if e.ApplicationID != "" || e.Mode == "" || e.RoutePattern != nil || e.RemoteBaseURL != nil {
			return errors.New("all applications requires only a mode")
		}
	} else if err := validation.Identifier("application ID", e.ApplicationID); err != nil {
		return err
	}
	if e.Mode != "" && e.Mode != "local" && e.Mode != "remote" {
		return errors.New("mode must be local or remote")
	}
	return nil
}
