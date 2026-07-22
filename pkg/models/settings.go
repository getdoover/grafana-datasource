package models

import (
	"encoding/json"
	"fmt"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const DefaultBaseURL = "https://data.doover.com"

type PluginSettings struct {
	BaseURL string `json:"baseUrl"`
	// OrgID is a Doover organisation snowflake ID. Kept as a string because
	// snowflakes exceed JavaScript's safe-integer range.
	OrgID    string                `json:"orgId"`
	ClientID string                `json:"clientId"`
	Secrets  *SecretPluginSettings `json:"-"`
}

type SecretPluginSettings struct {
	ClientSecret string `json:"clientSecret"`
}

func LoadPluginSettings(source backend.DataSourceInstanceSettings) (*PluginSettings, error) {
	settings := PluginSettings{}
	err := json.Unmarshal(source.JSONData, &settings)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal PluginSettings json: %w", err)
	}
	if settings.BaseURL == "" {
		settings.BaseURL = DefaultBaseURL
	}
	settings.Secrets = &SecretPluginSettings{
		ClientSecret: source.DecryptedSecureJSONData["clientSecret"],
	}
	return &settings, nil
}
