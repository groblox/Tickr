// Package config defines the Tickr configuration file and its persistence.
//
// Everything the GUI edits lives in one JSON document (tickr.json) in the
// data directory. Sections are stored in print order.
package config

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CurrentVersion is bumped when the on-disk format changes.
const CurrentVersion = 2

// FileName is the configuration file name inside the data directory.
const FileName = "tickr.json"

// Config is the whole user configuration.
type Config struct {
	Version    int         `json:"version"`
	SavedAt    time.Time   `json:"savedAt"`
	General    General     `json:"general"`
	Connectors Connectors  `json:"connectors"`
	Tasks      TasksConfig `json:"tasks"`
	Sections   []Section   `json:"sections"`
	Schedules  []Schedule  `json:"schedules"`
	Print      PrintConfig `json:"print"`
	Server     Server      `json:"server"`
}

// General holds report-wide settings.
type General struct {
	Timezone       string  `json:"timezone"`
	Location       string  `json:"location"`     // "lat,lng"
	LocationName   string  `json:"locationName"` // shown in weather headers
	Units          string  `json:"units"`        // "imperial" | "metric"
	ChildName      string  `json:"childName"`    // used by the kids modules
	PaperWidthMM   float64 `json:"paperWidthMm"`
	MarginBottomMM float64 `json:"marginBottomMm"`
	Wkhtmltopdf    string  `json:"wkhtmltopdf"` // explicit binary path, optional
	DateFormat     string  `json:"dateFormat"`  // Go layout for the header
	IconStyle      string  `json:"iconStyle"`   // "outline" (SVG line art) | "classic" (PNG set)
}

// Connectors holds credentials for every external service.
type Connectors struct {
	Weather       Weather       `json:"weather"`
	Aeris         Aeris         `json:"aeris"`
	Google        Google        `json:"google"`
	Dropbox       Dropbox       `json:"dropbox"`
	HomeAssistant HomeAssistant `json:"homeAssistant"`
	AI            AI            `json:"ai"`
	Events        Events        `json:"events"`
	Grafana       Grafana       `json:"grafana"`
	Telegram      Telegram      `json:"telegram"`
	Messaging     Messaging     `json:"messaging"`
}

// Telegram configures the "message the bot, it prints" channel. Long polling
// means no inbound port or public address is needed.
type Telegram struct {
	BotToken       string   `json:"botToken"`
	AllowedChatIDs []string `json:"allowedChatIds"` // numeric Telegram chat ids allowed to trigger a print
}

// Messaging configures the generic authenticated HTTP endpoint
// (POST /print-text on Server.PrintPort) for Shortcuts, Home Assistant
// automations, or a plain curl. The listener only starts once APIToken is set.
type Messaging struct {
	APIToken string `json:"apiToken"`
}

// Grafana configures the Grafana connector (service-account token).
type Grafana struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Events holds keys for the local events aggregator.
type Events struct {
	TicketmasterKey  string `json:"ticketmasterKey"`
	SeatGeekClientID string `json:"seatgeekClientId"`
}

// AI holds keys for the "Ask an AI" section. Empty keys fall back to the
// ANTHROPIC_API_KEY / OPENAI_API_KEY environment variables at run time.
type AI struct {
	AnthropicKey  string `json:"anthropicKey"`
	OpenAIKey     string `json:"openaiKey"`
	OpenAIBaseURL string `json:"openaiBaseUrl"` // for OpenAI-compatible servers (Ollama, LM Studio…)
}

// Weather selects the forecast provider.
type Weather struct {
	Provider       string `json:"provider"` // "openmeteo" | "tomorrowio"
	TomorrowAPIKey string `json:"tomorrowApiKey"`
}

// Aeris configures the personal weather station lookup.
type Aeris struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	StationID    string `json:"stationId"`
}

// Google configures calendar, tasks and Keep access through one OAuth client.
type Google struct {
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret"`
	RefreshToken string   `json:"refreshToken"`
	CalendarID   string   `json:"calendarId"`
	Scopes       []string `json:"scopes,omitempty"` // "calendar", "tasks", "keep"; empty = calendar only
}

// Dropbox configures task-list sync.
type Dropbox struct {
	AppKey       string `json:"appKey"`
	RefreshToken string `json:"refreshToken"`
	FilePath     string `json:"filePath"`
}

// HomeAssistant configures the Home Assistant REST connector.
type HomeAssistant struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// TasksConfig points at the task and reminder sources.
type TasksConfig struct {
	Source        string `json:"source"` // "local" | "dropbox"
	TasksPath     string `json:"tasksPath"`
	RemindersPath string `json:"remindersPath"`
}

// Section is one content block on the report, in print order.
type Section struct {
	ID      string         `json:"id"`
	Enabled bool           `json:"enabled"`
	Options map[string]any `json:"options,omitempty"`
}

// Schedule is one automatic generation slot.
type Schedule struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"` // "07:00" local time
	Days    []int  `json:"days"` // 0=Sunday … 6=Saturday
	Print   bool   `json:"print"`

	Sections            []string `json:"sections,omitempty"`  // section ids to include; empty = every enabled section
	CatchUpMinutes      int      `json:"catchUpMinutes"`      // run late if the slot was missed within this window
	Retries             int      `json:"retries"`             // extra attempts after a failure
	RetryDelayMinutes   int      `json:"retryDelayMinutes"`   // wait between attempts
	PrintOnlyIfComplete bool     `json:"printOnlyIfComplete"` // skip printing when any section failed
}

// PrintConfig describes how to send a finished PDF to the printer.
type PrintConfig struct {
	Command string `json:"command"` // e.g. lp "{pdf}"; {pdf} is replaced
}

// Server holds GUI server settings.
type Server struct {
	Port      int `json:"port"`
	PrintPort int `json:"printPort"` // POST /print-text, only listens once Messaging.APIToken is set
}

// Default returns a fresh configuration with sensible defaults.
func Default() *Config {
	return &Config{
		Version: CurrentVersion,
		General: General{
			Timezone:       localTimezoneName(),
			Location:       "",
			Units:          "imperial",
			PaperWidthMM:   47,
			MarginBottomMM: 7,
			DateFormat:     "Mon Jan 2, 2006",
			IconStyle:      "outline",
		},
		Connectors: Connectors{
			Weather: Weather{Provider: "openmeteo"},
			Dropbox: Dropbox{AppKey: "vmj3ivdahewiqzu"},
		},
		Tasks: TasksConfig{Source: "local", TasksPath: "tasks.list", RemindersPath: "reminders.list"},
		Schedules: []Schedule{
			{Name: "Weekday morning", Enabled: false, Time: "06:30", Days: []int{1, 2, 3, 4, 5}, Print: true, CatchUpMinutes: 120, Retries: 2, RetryDelayMinutes: 5},
		},
		Print:  PrintConfig{Command: defaultPrintCommand()},
		Server: Server{Port: 8787, PrintPort: 8788},
	}
}

func defaultPrintCommand() string {
	switch runtime.GOOS {
	case "windows":
		return `powershell -NoProfile -Command "Start-Process -FilePath '{pdf}' -Verb Print -WindowStyle Hidden"`
	default:
		return `lp "{pdf}"`
	}
}

func localTimezoneName() string {
	if name := time.Local.String(); name != "" && name != "Local" {
		return name
	}
	return "UTC"
}

// Store loads and saves the configuration with a mutex so the GUI server and
// scheduler can share it safely.
//
// The file is written to the data folder and mirrored to a per-user folder
// outside any cloud sync (os.UserConfigDir()/Tickr/<id>/). On load the
// copy with the newest SavedAt wins, which protects the settings from sync
// clients that roll files back or delete them.
type Store struct {
	path   string
	mirror string
	mu     sync.RWMutex
	cfg    *Config
}

// NewStore creates a store bound to dataDir/tickr.json.
func NewStore(dataDir string) *Store {
	s := &Store{path: filepath.Join(dataDir, FileName)}
	// Temporary data folders (tests, throwaway runs) get no mirror.
	if strings.HasPrefix(strings.ToLower(dataDir), strings.ToLower(os.TempDir())) {
		return s
	}
	if base, err := os.UserConfigDir(); err == nil {
		abs, _ := filepath.Abs(dataDir)
		sum := sha1.Sum([]byte(strings.ToLower(filepath.ToSlash(abs))))
		s.mirror = filepath.Join(base, "Tickr", fmt.Sprintf("%x", sum[:6]), FileName)
	}
	return s
}

// Path returns the config file path.
func (s *Store) Path() string { return s.path }

// MirrorPath returns the out-of-sync mirror path ("" if unavailable).
func (s *Store) MirrorPath() string { return s.mirror }

// Load reads the newest of the data-folder file, its .bak and the mirror, or
// creates defaults (importing a legacy .env if found).
func (s *Store) Load() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cfg *Config
	for _, p := range []string{s.path, s.path + ".bak", s.mirror} {
		if p == "" {
			continue
		}
		c, err := readFile(p)
		if err != nil {
			continue
		}
		if cfg == nil || c.SavedAt.After(cfg.SavedAt) {
			cfg = c
		}
	}
	var err error
	if cfg == nil {
		err = os.ErrNotExist
	}
	if errors.Is(err, os.ErrNotExist) {
		cfg = Default()
		dir := filepath.Dir(s.path)
		for _, envPath := range []string{filepath.Join(dir, ".env"), filepath.Join(dir, "reportGenerator", ".env")} {
			if _, statErr := os.Stat(envPath); statErr == nil {
				ImportEnv(cfg, envPath)
				break
			}
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	migrate(cfg)
	s.cfg = cfg
	// Make sure both locations hold the winning copy.
	_ = s.writeAllLocked()
	return cfg.Clone(), nil
}

func (s *Store) writeAllLocked() error {
	if err := writeFile(s.path, s.cfg); err != nil {
		return err
	}
	if s.mirror != "" {
		_ = writeFile(s.mirror, s.cfg)
	}
	return nil
}

// Get returns a copy of the current configuration.
func (s *Store) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return Default()
	}
	return s.cfg.Clone()
}

// Save validates, stores in memory and writes to disk atomically.
func (s *Store) Save(cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg.Version = CurrentVersion
	cfg.SavedAt = time.Now().UTC()
	s.cfg = cfg.Clone()
	return s.writeAllLocked()
}

func readFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

func writeFile(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write in place rather than tmp+rename: cloud-synced folders (iCloud
	// Drive in particular) have been seen to drop files replaced by rename.
	// A .bak copy of the previous version is kept for recovery.
	if prev, err := os.ReadFile(path); err == nil && len(prev) > 0 {
		_ = os.WriteFile(path+".bak", prev, 0o600)
	}
	return os.WriteFile(path, data, 0o600)
}

// EnsureOnDisk rewrites the data-folder file from memory if it has gone
// missing or been rolled back to an older version by a sync client.
func (s *Store) EnsureOnDisk() (restored bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg == nil {
		return false, nil
	}
	if onDisk, readErr := readFile(s.path); readErr == nil && !onDisk.SavedAt.Before(s.cfg.SavedAt) {
		return false, nil
	}
	return true, s.writeAllLocked()
}

// migrate fills in defaults for fields added after the file was written.
func migrate(cfg *Config) {
	def := Default()
	if cfg.General.Units == "" {
		cfg.General.Units = def.General.Units
	}
	if cfg.General.PaperWidthMM <= 0 {
		cfg.General.PaperWidthMM = def.General.PaperWidthMM
	}
	if cfg.General.MarginBottomMM < 0 {
		cfg.General.MarginBottomMM = def.General.MarginBottomMM
	}
	if cfg.General.Timezone == "" {
		cfg.General.Timezone = def.General.Timezone
	}
	if cfg.General.DateFormat == "" {
		cfg.General.DateFormat = def.General.DateFormat
	}
	if cfg.General.IconStyle == "" {
		cfg.General.IconStyle = def.General.IconStyle
	}
	if cfg.Connectors.Weather.Provider == "" {
		cfg.Connectors.Weather.Provider = "openmeteo"
	}
	if cfg.Connectors.Dropbox.AppKey == "" {
		cfg.Connectors.Dropbox.AppKey = def.Connectors.Dropbox.AppKey
	}
	if cfg.Tasks.Source == "" {
		cfg.Tasks.Source = "local"
	}
	if cfg.Tasks.TasksPath == "" {
		cfg.Tasks.TasksPath = def.Tasks.TasksPath
	}
	if cfg.Tasks.RemindersPath == "" {
		cfg.Tasks.RemindersPath = def.Tasks.RemindersPath
	}
	if cfg.Print.Command == "" {
		cfg.Print.Command = def.Print.Command
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = def.Server.Port
	}
	if cfg.Server.PrintPort == 0 {
		cfg.Server.PrintPort = def.Server.PrintPort
	}
	for i := range cfg.Schedules {
		s := &cfg.Schedules[i]
		if s.CatchUpMinutes == 0 && s.Retries == 0 && s.RetryDelayMinutes == 0 {
			s.CatchUpMinutes, s.Retries, s.RetryDelayMinutes = 120, 2, 5
		}
	}
	cfg.Version = CurrentVersion
}

// Validate checks the parts of the config that would break a run.
func (c *Config) Validate() error {
	if _, err := time.LoadLocation(c.General.Timezone); err != nil {
		return fmt.Errorf("timezone %q is not a valid IANA name", c.General.Timezone)
	}
	if c.General.Units != "imperial" && c.General.Units != "metric" {
		return fmt.Errorf("units must be imperial or metric")
	}
	if c.General.PaperWidthMM < 20 || c.General.PaperWidthMM > 300 {
		return fmt.Errorf("paper width must be between 20 and 300 mm")
	}
	if c.Connectors.Weather.Provider != "openmeteo" && c.Connectors.Weather.Provider != "tomorrowio" {
		return fmt.Errorf("weather provider must be openmeteo or tomorrowio")
	}
	if c.Tasks.Source != "local" && c.Tasks.Source != "dropbox" {
		return fmt.Errorf("task source must be local or dropbox")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server port out of range")
	}
	if c.Server.PrintPort < 1 || c.Server.PrintPort > 65535 {
		return fmt.Errorf("print API port out of range")
	}
	if c.Server.PrintPort == c.Server.Port {
		return fmt.Errorf("print API port must differ from the GUI port")
	}
	seen := map[string]bool{}
	for _, s := range c.Sections {
		if s.ID == "" {
			return fmt.Errorf("section without id")
		}
		if seen[s.ID] {
			return fmt.Errorf("section %q listed twice", s.ID)
		}
		seen[s.ID] = true
	}
	for i, sch := range c.Schedules {
		if _, err := ParseClock(sch.Time); err != nil {
			return fmt.Errorf("schedule %d: %w", i+1, err)
		}
		for _, d := range sch.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("schedule %d: day %d out of range", i+1, d)
			}
		}
	}
	return nil
}

// Clock is a time of day.
type Clock struct{ Hour, Minute int }

// ParseClock parses "HH:MM" into hour and minute.
func ParseClock(s string) (Clock, error) {
	var c Clock
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return c, fmt.Errorf("time %q must look like HH:MM", s)
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &c.Hour); err != nil || c.Hour < 0 || c.Hour > 23 {
		return c, fmt.Errorf("time %q has a bad hour", s)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &c.Minute); err != nil || c.Minute < 0 || c.Minute > 59 {
		return c, fmt.Errorf("time %q has a bad minute", s)
	}
	return c, nil
}

// Clone deep-copies the config through JSON.
func (c *Config) Clone() *Config {
	data, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(data, &out)
	return &out
}

// Location returns the configured time zone (UTC on failure).
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.General.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// LatLng splits General.Location into coordinates.
func (c *Config) LatLng() (lat, lng float64, ok bool) {
	parts := strings.Split(c.General.Location, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%f", &lat); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &lng); err != nil {
		return 0, 0, false
	}
	return lat, lng, true
}

// Imperial reports whether temperatures should print in °F.
func (c *Config) Imperial() bool { return c.General.Units != "metric" }

// SectionByID finds a section entry.
func (c *Config) SectionByID(id string) *Section {
	for i := range c.Sections {
		if c.Sections[i].ID == id {
			return &c.Sections[i]
		}
	}
	return nil
}

// Masked is the placeholder returned in place of stored secrets.
const Masked = "********"

// secretFields lists every credential so redaction and merging stay in sync.
func (c *Config) secretFields() []*string {
	return []*string{
		&c.Connectors.Weather.TomorrowAPIKey,
		&c.Connectors.Aeris.ClientSecret,
		&c.Connectors.Google.ClientSecret,
		&c.Connectors.Google.RefreshToken,
		&c.Connectors.Dropbox.RefreshToken,
		&c.Connectors.HomeAssistant.Token,
		&c.Connectors.AI.AnthropicKey,
		&c.Connectors.AI.OpenAIKey,
		&c.Connectors.Events.TicketmasterKey,
		&c.Connectors.Grafana.Token,
		&c.Connectors.Telegram.BotToken,
		&c.Connectors.Messaging.APIToken,
	}
}

// Redacted returns a copy with secrets replaced by a marker, for API responses.
func (c *Config) Redacted() *Config {
	out := c.Clone()
	for _, f := range out.secretFields() {
		if *f != "" {
			*f = Masked
		}
	}
	return out
}

// MergeSecrets copies secrets from prev into next wherever next still holds
// the Masked placeholder, so a GUI round-trip never wipes credentials.
func MergeSecrets(next, prev *Config) {
	n, p := next.secretFields(), prev.secretFields()
	for i := range n {
		if *n[i] == Masked {
			*n[i] = *p[i]
		}
	}
}
