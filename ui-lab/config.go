package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type mappingCfg struct {
	AzureProject    string   `json:"azureProject"`
	AzureRepository string   `json:"azureRepository"`
	DefaultBranch   string   `json:"defaultBranch"`
	AreaPath        string   `json:"areaPath"`
	Aliases         []string `json:"aliases"`
	LocalPath       string   `json:"localPath"`
}

type config struct {
	AzureOrg        string                `json:"azureOrg"`
	WorkItemProject string                `json:"workItemProject"`
	Mappings        map[string]mappingCfg `json:"azureProjectMappings"`
	ProjectPaths    map[string]string     `json:"projectPaths"`
	loadedFrom      string
}

// userConfigPath is the canonical per-user config location, written by the init
// wizard and read on every launch. On Windows os.UserConfigDir() is %AppData%
// (Roaming), so this is %AppData%\very-lazy\config.json; ~/.config/very-lazy on
// unix. This is what makes an installed binary work from any directory.
func userConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "very-lazy", "config.json")
	}
	return "config.json"
}

// loadConfig reads a vl-compatible config. It prefers the per-user config the
// installer/init writes, then falls back to dev locations (running from the repo:
// parent's real vl config, the sandbox-generated one, then cwd).
func loadConfig() (config, bool) {
	for _, p := range []string{userConfigPath(), "../config.json", "config.generated.json", "config.json"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c config
		if json.Unmarshal(data, &c) != nil {
			continue
		}
		if c.AzureOrg != "" && c.WorkItemProject != "" {
			c.loadedFrom = p
			return c, true
		}
	}
	return config{}, false
}
