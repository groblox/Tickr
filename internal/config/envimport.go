package config

import (
	"bufio"
	"os"
	"strings"
)

// ImportEnv reads a legacy .env file (from Breaklist 1.x) into cfg.
// Unknown keys are ignored. It is only used the first time Breaklist 2 runs
// in a directory that has no breaklist.json yet.
func ImportEnv(cfg *Config, path string) {
	env := ReadEnvFile(path)
	set := func(dst *string, key string) {
		if v, ok := env[key]; ok && v != "" {
			*dst = v
		}
	}
	set(&cfg.Connectors.Weather.TomorrowAPIKey, "TOMORROW_API_KEY")
	if cfg.Connectors.Weather.TomorrowAPIKey != "" {
		cfg.Connectors.Weather.Provider = "tomorrowio"
	}
	set(&cfg.General.Location, "LOCATION")
	set(&cfg.General.Timezone, "TIMEZONE")
	set(&cfg.Tasks.TasksPath, "TASKS_LIST_PATH")
	set(&cfg.Tasks.RemindersPath, "REMINDERS_LIST_PATH")
	set(&cfg.Connectors.Google.ClientID, "GOOGLE_CLIENT_ID")
	set(&cfg.Connectors.Google.ClientSecret, "GOOGLE_CLIENT_SECRET")
	set(&cfg.Connectors.Google.CalendarID, "GOOGLE_CALENDAR_ID")
	set(&cfg.Connectors.Google.RefreshToken, "GOOGLE_REFRESH_TOKEN")
	set(&cfg.Connectors.Dropbox.AppKey, "DROPBOX_APP_KEY")
	set(&cfg.Connectors.Dropbox.RefreshToken, "DROPBOX_REFRESH_TOKEN")
	set(&cfg.Connectors.Dropbox.FilePath, "DROPBOX_FILE_PATH")
	if cfg.Connectors.Dropbox.RefreshToken != "" {
		cfg.Tasks.Source = "dropbox"
	}
	set(&cfg.Connectors.Aeris.ClientID, "AERIS_CLIENT_ID")
	set(&cfg.Connectors.Aeris.ClientSecret, "AERIS_CLIENT_SECRET")
	set(&cfg.Connectors.Aeris.StationID, "AERIS_STATION_ID")
	set(&cfg.Connectors.HomeAssistant.URL, "HOME_ASSISTANT_URL")
	set(&cfg.Connectors.HomeAssistant.Token, "HOME_ASSISTANT_TOKEN")
}

// ReadEnvFile parses KEY=VALUE lines. Quotes around values are stripped.
func ReadEnvFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 {
			first, last := v[0], v[len(v)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}
