package services

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

// LoggerService handles logger management operations
type LoggerService struct {
	httpClient *http.Client
}

// LoggersResponse represents the response from Spring Boot Actuator loggers endpoint
type LoggersResponse struct {
	Levels  []string                `json:"levels"`
	Loggers map[string]LoggerDetail `json:"loggers"`
}

// LoggerDetail represents the details of a single logger
type LoggerDetail struct {
	EffectiveLevel  string `json:"effectiveLevel"`
	ConfiguredLevel string `json:"configuredLevel,omitempty"`
}

// LoggerLevelRequest represents a request to change a logger level
type LoggerLevelRequest struct {
	ConfiguredLevel string `json:"configuredLevel"`
}

// NewLoggerService creates a new logger service instance
func NewLoggerService() *LoggerService {
	return &LoggerService{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetLoggers fetches all loggers from an application
func (ls *LoggerService) GetLoggers(app *models.Application, defaultRemoteBaseURL string) (*LoggersResponse, error) {
	url, err := app.LoggersURLWithHost(defaultRemoteBaseURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication if configured
	ls.addAuthHeader(req, app)

	resp, err := ls.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch loggers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var loggersResp LoggersResponse
	if err := json.NewDecoder(resp.Body).Decode(&loggersResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &loggersResp, nil
}

// SetLoggerLevel sets the level for a specific logger
func (ls *LoggerService) SetLoggerLevel(app *models.Application, defaultRemoteBaseURL string, loggerName, level string) error {
	url, err := app.LoggerURLWithHost(loggerName, defaultRemoteBaseURL)
	if err != nil {
		return err
	}

	reqBody := LoggerLevelRequest{ConfiguredLevel: level}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add authentication if configured
	ls.addAuthHeader(req, app)

	resp, err := ls.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set logger level: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// addAuthHeader adds authentication headers to the request
func (ls *LoggerService) addAuthHeader(req *http.Request, app *models.Application) {
	// Check if application has specific logger config
	if app.LoggerConfig != nil && !app.LoggerConfig.UseProjectConfig {
		switch app.LoggerConfig.AuthType {
		case "basic":
			if app.LoggerConfig.AuthUsername != "" && app.LoggerConfig.AuthPassword != "" {
				req.Header.Set("Authorization", "Basic "+basicAuth(app.LoggerConfig.AuthUsername, app.LoggerConfig.AuthPassword))
			}
			return
		case "bearer":
			if app.LoggerConfig.AuthToken != "" {
				req.Header.Set("Authorization", "Bearer "+app.LoggerConfig.AuthToken)
			}
			return
		case "none":
			return
		}
	}

	// Fall back to global config
	if config.Config.LoggerAuth != "" {
		req.Header.Set("Authorization", config.Config.LoggerAuth)
	}
}

// basicAuth creates a basic auth string
func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

// LoggerManager manages logger operations with persistence
type LoggerManager struct {
	service *LoggerService
}

// NewLoggerManager creates a new logger manager
func NewLoggerManager() *LoggerManager {
	return &LoggerManager{
		service: NewLoggerService(),
	}
}

// GetLoggersWithState fetches loggers from the application
func (lm *LoggerManager) GetLoggersWithState(wsID string, app *models.Application, defaultRemoteBaseURL string, isLocal bool) (*LoggersResponse, error) {
	// Fetch current loggers from application
	resp, err := lm.service.GetLoggers(app, defaultRemoteBaseURL)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// SetLoggerLevelAndPersist sets logger level
func (lm *LoggerManager) SetLoggerLevelAndPersist(wsID string, app *models.Application, defaultRemoteBaseURL string, loggerName, level string, isLocal bool) error {
	// Set the level on the application
	if err := lm.service.SetLoggerLevel(app, defaultRemoteBaseURL, loggerName, level); err != nil {
		return err
	}

	return nil
}
