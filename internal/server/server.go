// Package server exposes the JSON API and the embedded web GUI.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tickr/internal/config"
	"tickr/internal/connectors"
	"tickr/internal/logging"
	"tickr/internal/modules"
	"tickr/internal/render"
	"tickr/internal/scheduler"
)

//go:embed web
var webFS embed.FS

// Version is stamped by the build.
var Version = "dev"

// Server wires the store, renderer and scheduler to HTTP.
type Server struct {
	store    *config.Store
	renderer *render.Renderer
	sched    *scheduler.Scheduler
	dataDir  string

	mu         sync.Mutex
	generating bool
	last       *render.Result
	lastErr    string
	lastPrint  string
	history    []runRecord
	notes      []noteRecord

	authMu   sync.Mutex
	authSrvs map[string]*http.Server

	printMu   sync.Mutex
	printSrv  *http.Server
	printPort int

	telegramOffset int64
}

type runRecord struct {
	Time     time.Time `json:"time"`
	Trigger  string    `json:"trigger"`
	OK       bool      `json:"ok"`
	Message  string    `json:"message"`
	HeightMM int       `json:"heightMm"`
	Printed  bool      `json:"printed"`
}

// noteRecord is one "text was sent in and printed" event, from Telegram or
// the generic HTTP endpoint — kept separate from runRecord since it is not a
// report run.
type noteRecord struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"` // "Telegram: Ben" or "API"
	Text   string    `json:"text"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
}

// New creates a server.
func New(store *config.Store, dataDir string) *Server {
	s := &Server{store: store, renderer: render.New(dataDir), dataDir: dataDir, authSrvs: map[string]*http.Server{}}
	s.sched = scheduler.New(store, dataDir, s.scheduledRun)
	s.loadHistory()
	s.loadNotes()
	s.loadTelegramOffset()
	return s
}

// scheduledRun is what the scheduler calls: generate the configured sections
// and print if asked. Returning an error makes the scheduler retry.
func (s *Server) scheduledRun(ctx context.Context, sch config.Schedule, attempt int) error {
	var only map[string]bool
	if len(sch.Sections) > 0 {
		only = map[string]bool{}
		for _, id := range sch.Sections {
			only[strings.TrimSpace(id)] = true
		}
	}
	trigger := "schedule: " + sch.Name
	if attempt > 0 {
		trigger += fmt.Sprintf(" (retry %d)", attempt)
	}
	runCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	res, err := s.generate(runCtx, only, trigger)
	if err != nil {
		return err
	}
	if !sch.Print {
		return nil
	}
	if sch.PrintOnlyIfComplete {
		for _, sec := range res.Sections {
			if sec.Status == "error" {
				return fmt.Errorf("not printing: section %s failed (%s)", sec.ID, sec.Error)
			}
		}
	}
	if _, err := s.print(runCtx); err != nil {
		return err
	}
	return nil
}

// Run starts the scheduler and HTTP server until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.store.Get()
	go s.sched.Start(ctx)
	go s.guardConfig(ctx)
	go s.pollTelegram(ctx)
	go s.runPrintAPILoop(ctx)
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port)
	srv := &http.Server{Addr: addr, Handler: s.routes()}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is busy: %w", cfg.Server.Port, err)
	}
	log.Printf("Tickr GUI running at http://%s — reachable from the LAN; it has no login, so keep this network trusted", addr)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// guardConfig rewrites tickr.json from memory if a sync client removes it.
func (s *Server) guardConfig(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if restored, err := s.store.EnsureOnDisk(); restored {
				if err != nil {
					log.Printf("config file went missing and could not be restored: %v", err)
				} else {
					log.Printf("config file went missing on disk; rewrote it from memory")
				}
			}
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.Handle("/output/", http.StripPrefix("/output/", noCache(http.FileServer(http.Dir(s.renderer.OutDir)))))

	mux.HandleFunc("GET /api/catalog", s.handleCatalog)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/generate", s.handleGenerate)
	mux.HandleFunc("POST /api/preview/{id}", s.handlePreview)
	mux.HandleFunc("POST /api/print", s.handlePrint)
	mux.HandleFunc("POST /api/test/homeassistant", s.handleTestHA)
	mux.HandleFunc("GET /api/ha/entities", s.handleHAEntities)
	mux.HandleFunc("POST /api/test/weather", s.handleTestWeather)
	mux.HandleFunc("POST /api/test/aeris", s.handleTestAeris)
	mux.HandleFunc("POST /api/test/ai", s.handleTestAI)
	mux.HandleFunc("POST /api/test/grafana", s.handleTestGrafana)
	mux.HandleFunc("GET /api/grafana/datasources", s.handleGrafanaDatasources)
	mux.HandleFunc("GET /api/log", s.handleLog)
	mux.HandleFunc("POST /api/schedules/{index}/run", s.handleRunSchedule)
	mux.HandleFunc("GET /api/google/calendars", s.handleGoogleCalendars)
	mux.HandleFunc("POST /api/auth/google/start", s.handleGoogleAuthStart)
	mux.HandleFunc("POST /api/auth/dropbox/start", s.handleDropboxAuthStart)
	mux.HandleFunc("POST /api/disconnect/{name}", s.handleDisconnect)
	mux.HandleFunc("POST /api/layout/reset", s.handleLayoutReset)
	mux.HandleFunc("POST /api/test/telegram", s.handleTestTelegram)
	mux.HandleFunc("POST /api/messaging/token", s.handleGenerateAPIToken)
	mux.HandleFunc("POST /api/print-text", s.handlePrintTextLocal) // no token: same trust boundary as /api/config — anyone who can reach the GUI can already change settings
	return logRequests(mux)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r)
	})
}

// ── config & catalog ─────────────────────────────────────────────────────────

func (s *Server) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, modules.Catalog())
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.store.Get()
	modules.NormalizeSections(cfg)
	writeJSON(w, 200, cfg.Redacted())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var next config.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&next); err != nil {
		writeErr(w, 400, fmt.Errorf("bad config JSON: %w", err))
		return
	}
	prev := s.store.Get()
	config.MergeSecrets(&next, prev)
	modules.NormalizeSections(&next)
	if err := s.store.Save(&next); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, next.Redacted())
}

func (s *Server) handleLayoutReset(w http.ResponseWriter, _ *http.Request) {
	cfg := s.store.Get()
	cfg.Sections = modules.DefaultLayout()
	if err := s.store.Save(cfg); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, cfg.Redacted())
}

// ── status & generation ──────────────────────────────────────────────────────

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.store.Get()
	next, name, ok := s.sched.Next()
	_, pdfErr := os.Stat(s.renderer.PDFPath())
	st := map[string]any{
		"version":    Version,
		"dataDir":    s.dataDir,
		"configPath": s.store.Path(),
		"generating": s.generating,
		"last":       s.last,
		"lastError":  s.lastErr,
		"lastPrint":  s.lastPrint,
		"pdfExists":  pdfErr == nil,
		"history":    s.history,
		"notes":      s.notes,
		"now":        time.Now().In(cfg.Location()).Format(time.RFC1123),
		"connectors": map[string]bool{
			"google":        cfg.Connectors.Google.RefreshToken != "",
			"dropbox":       cfg.Connectors.Dropbox.RefreshToken != "",
			"homeassistant": cfg.Connectors.HomeAssistant.URL != "" && cfg.Connectors.HomeAssistant.Token != "",
			"aeris":         cfg.Connectors.Aeris.ClientID != "" && cfg.Connectors.Aeris.StationID != "",
			"weather":       cfg.General.Location != "" && (cfg.Connectors.Weather.Provider == "openmeteo" || cfg.Connectors.Weather.TomorrowAPIKey != ""),
			"ai": connectors.AIKey(cfg.Connectors.AI, connectors.ProviderAnthropic) != "" || connectors.AIKey(cfg.Connectors.AI, connectors.ProviderOpenAI) != "" ||
				connectors.AIKey(cfg.Connectors.AI, connectors.ProviderOpenRouter) != "" || cfg.Connectors.AI.LocalBaseURL != "",
			"ticketmaster": cfg.Connectors.Events.TicketmasterKey != "",
			"grafana":      cfg.Connectors.Grafana.URL != "" && cfg.Connectors.Grafana.Token != "",
			"telegram":     cfg.Connectors.Telegram.BotToken != "" && len(cfg.Connectors.Telegram.AllowedChatIDs) > 0,
			"messaging":    cfg.Connectors.Messaging.APIToken != "",
		},
		"printPort": cfg.Server.PrintPort,
		"aiEnv": map[string]bool{
			"anthropic":  os.Getenv("ANTHROPIC_API_KEY") != "",
			"openai":     os.Getenv("OPENAI_API_KEY") != "",
			"openrouter": os.Getenv("OPENROUTER_API_KEY") != "",
		},
	}
	if ok {
		st["nextRun"] = next.Format(time.RFC1123)
		st["nextRunName"] = name
	}
	st["schedules"] = s.sched.Statuses()
	st["logPath"] = logging.Path()
	writeJSON(w, 200, st)
}

func (s *Server) generate(ctx context.Context, only map[string]bool, trigger string) (*render.Result, error) {
	s.mu.Lock()
	if s.generating {
		s.mu.Unlock()
		return nil, fmt.Errorf("a report is already being generated")
	}
	s.generating = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.generating = false
		s.mu.Unlock()
	}()
	cfg := s.store.Get()
	modules.NormalizeSections(cfg)
	log.Printf("generating report (%s)", trigger)
	res, err := s.renderer.Generate(ctx, cfg, only)
	rec := runRecord{Time: time.Now(), Trigger: trigger, OK: err == nil}
	s.mu.Lock()
	s.last = res
	if err != nil {
		s.lastErr = err.Error()
		rec.Message = err.Error()
		log.Printf("generation failed: %v", err)
	} else {
		s.lastErr = ""
		rec.HeightMM = res.HeightMM
		rec.Message = fmt.Sprintf("%d sections, %dmm", countOK(res), res.HeightMM)
	}
	s.history = append([]runRecord{rec}, s.history...)
	if len(s.history) > 30 {
		s.history = s.history[:30]
	}
	s.saveHistoryLocked()
	s.mu.Unlock()
	return res, err
}

func (s *Server) historyPath() string { return filepath.Join(s.renderer.OutDir, "runs.json") }

// saveHistoryLocked persists the run log so it survives restarts. Caller holds s.mu.
func (s *Server) saveHistoryLocked() {
	data, err := json.MarshalIndent(struct {
		History []runRecord    `json:"history"`
		Last    *render.Result `json:"last"`
	}{s.history, s.last}, "", "  ")
	if err == nil {
		_ = os.MkdirAll(s.renderer.OutDir, 0o755)
		_ = os.WriteFile(s.historyPath(), data, 0o644)
	}
}

func (s *Server) loadHistory() {
	data, err := os.ReadFile(s.historyPath())
	if err != nil {
		return
	}
	var saved struct {
		History []runRecord    `json:"history"`
		Last    *render.Result `json:"last"`
	}
	if json.Unmarshal(data, &saved) == nil {
		s.mu.Lock()
		s.history, s.last = saved.History, saved.Last
		s.mu.Unlock()
	}
}

func countOK(res *render.Result) int {
	n := 0
	for _, sec := range res.Sections {
		if sec.Status == "ok" {
			n++
		}
	}
	return n
}

func (s *Server) print(ctx context.Context) (string, error) {
	out, err := s.renderer.Print(ctx, s.store.Get())
	s.mu.Lock()
	if err != nil {
		s.lastPrint = "failed: " + err.Error()
	} else {
		s.lastPrint = "sent " + time.Now().Format(time.Kitchen)
		if len(s.history) > 0 {
			s.history[0].Printed = true
		}
	}
	s.mu.Unlock()
	return out, err
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Only  []string `json:"only"`
		Print bool     `json:"print"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var only map[string]bool
	if len(body.Only) > 0 {
		only = map[string]bool{}
		for _, id := range body.Only {
			only[id] = true
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := s.generate(ctx, only, "manual")
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error(), "result": res})
		return
	}
	printMsg := ""
	if body.Print {
		if out, perr := s.print(ctx); perr != nil {
			printMsg = perr.Error()
		} else {
			printMsg = "sent to printer " + out
		}
	}
	writeJSON(w, 200, map[string]any{"result": res, "print": printMsg})
}

// handlePreview renders a single module with the options supplied in the body
// (unsaved edits included) and returns the HTML fragment.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mod, ok := modules.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("unknown module %s", id))
		return
	}
	var body struct {
		Options map[string]any `json:"options"`
		Config  *config.Config `json:"config"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cfg := s.store.Get()
	if body.Config != nil {
		config.MergeSecrets(body.Config, cfg)
		cfg = body.Config
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var logs []string
	env := modules.NewEnv(cfg, s.dataDir, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	start := time.Now()
	sec, err := mod.Render(ctx, env, modules.ApplyDefaults(mod.Info(), body.Options))
	resp := map[string]any{"id": id, "ms": time.Since(start).Milliseconds(), "log": logs}
	switch {
	case err != nil:
		resp["error"] = err.Error()
	case sec == nil || sec.Empty:
		resp["empty"] = true
	default:
		page, perr := s.renderer.Page(cfg, []*modules.Section{sec})
		if perr != nil {
			resp["error"] = perr.Error()
		} else {
			resp["html"] = page
			resp["estPx"] = sec.EstPx
		}
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	out, err := s.print(ctx)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"output": out})
}

// ── connector tests ──────────────────────────────────────────────────────────

func (s *Server) haFromRequest(r *http.Request) (*connectors.HomeAssistant, error) {
	cfg := s.store.Get()
	hc := cfg.Connectors.HomeAssistant
	if r.Method == http.MethodPost {
		var body config.HomeAssistant
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if body.URL != "" {
				hc.URL = body.URL
			}
			if body.Token != "" && body.Token != config.Masked {
				hc.Token = body.Token
			}
		}
	}
	return connectors.NewHomeAssistant(hc)
}

func (s *Server) handleTestHA(w http.ResponseWriter, r *http.Request) {
	ha, err := s.haFromRequest(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	msg, err := ha.Ping(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	states, _ := ha.States(ctx)
	writeJSON(w, 200, map[string]any{"ok": true, "message": msg, "entities": len(states)})
}

func (s *Server) handleHAEntities(w http.ResponseWriter, r *http.Request) {
	ha, err := s.haFromRequest(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	states, err := ha.States(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	type ent struct {
		ID, Name, State, Unit string
	}
	out := make([]ent, 0, len(states))
	for _, st := range states {
		out = append(out, ent{st.EntityID, st.FriendlyName, st.State, st.Unit})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleTestWeather(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var body struct {
		Location string          `json:"location"`
		Weather  *config.Weather `json:"weather"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Location != "" {
		cfg.General.Location = body.Location
	}
	if body.Weather != nil {
		if body.Weather.TomorrowAPIKey == config.Masked {
			body.Weather.TomorrowAPIKey = cfg.Connectors.Weather.TomorrowAPIKey
		}
		cfg.Connectors.Weather = *body.Weather
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	fc, err := connectors.GetForecast(ctx, cfg)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	msg := fmt.Sprintf("%s: %d hourly and %d daily points", fc.Provider, len(fc.Hourly), len(fc.Daily))
	if len(fc.Daily) > 0 {
		msg += fmt.Sprintf("; today high %.0f°C", fc.Daily[0].MaxC)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleTestAeris(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var body config.Aeris
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ClientSecret == config.Masked || body.ClientSecret == "" {
		body.ClientSecret = cfg.Connectors.Aeris.ClientSecret
	}
	if body.ClientID == "" {
		body.ClientID = cfg.Connectors.Aeris.ClientID
	}
	if body.StationID == "" {
		body.StationID = cfg.Connectors.Aeris.StationID
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, err := connectors.GetPWSStats(ctx, body)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": fmt.Sprintf("station reports %.1f°C, %.0f%% humidity", st.TempC, st.Humidity)})
}

func (s *Server) handleTestAI(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var body struct {
		Provider string    `json:"provider"`
		AI       config.AI `json:"ai"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ai := cfg.Connectors.AI
	if body.AI.AnthropicKey != "" && body.AI.AnthropicKey != config.Masked {
		ai.AnthropicKey = body.AI.AnthropicKey
	}
	if body.AI.OpenAIKey != "" && body.AI.OpenAIKey != config.Masked {
		ai.OpenAIKey = body.AI.OpenAIKey
	}
	if body.AI.OpenAIBaseURL != "" {
		ai.OpenAIBaseURL = body.AI.OpenAIBaseURL
	}
	if body.AI.OpenRouterKey != "" && body.AI.OpenRouterKey != config.Masked {
		ai.OpenRouterKey = body.AI.OpenRouterKey
	}
	if body.AI.LocalBaseURL != "" {
		ai.LocalBaseURL = body.AI.LocalBaseURL
	}
	if body.AI.LocalKey != "" && body.AI.LocalKey != config.Masked {
		ai.LocalKey = body.AI.LocalKey
	}
	if body.Provider == "" {
		body.Provider = connectors.ProviderAnthropic
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := connectors.Complete(ctx, ai, connectors.AIRequest{Provider: body.Provider, Prompt: "Reply with the single word: ready", MaxTokens: 10})
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": fmt.Sprintf("%s (%s) replied: %s", body.Provider, connectors.DefaultModel(body.Provider), out)})
}

func (s *Server) grafanaFromRequest(r *http.Request) (*connectors.Grafana, error) {
	cfg := s.store.Get()
	gc := cfg.Connectors.Grafana
	if r.Method == http.MethodPost {
		var body config.Grafana
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if body.URL != "" {
				gc.URL = body.URL
			}
			if body.Token != "" && body.Token != config.Masked {
				gc.Token = body.Token
			}
		}
	}
	return connectors.NewGrafana(gc)
}

func (s *Server) handleTestGrafana(w http.ResponseWriter, r *http.Request) {
	g, err := s.grafanaFromRequest(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	msg, err := g.Health(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	ds, _ := g.Datasources(ctx)
	var names []string
	for _, d := range ds {
		names = append(names, fmt.Sprintf("%s (%s, uid %s)", d.Name, d.Type, d.UID))
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": msg + "\nData sources:\n" + strings.Join(names, "\n")})
}

func (s *Server) handleGrafanaDatasources(w http.ResponseWriter, r *http.Request) {
	g, err := s.grafanaFromRequest(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ds, err := g.Datasources(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, ds)
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	n := 300
	if v := r.URL.Query().Get("n"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	if n < 10 {
		n = 10
	}
	if n > 5000 {
		n = 5000
	}
	writeJSON(w, 200, map[string]any{"path": logging.Path(), "lines": logging.Tail(n)})
}

func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	var idx int
	if _, err := fmt.Sscanf(r.PathValue("index"), "%d", &idx); err != nil {
		writeErr(w, 400, fmt.Errorf("bad schedule index"))
		return
	}
	// Detach from the request so the run survives the browser tab closing.
	if err := s.sched.RunNow(context.Background(), idx); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "started"})
}

// ── Print-a-note-of-text (Telegram + generic HTTP API) ───────────────────────
//
// This is the "not a full report, just this text" feature: a message from an
// allowed Telegram chat, or an authenticated POST from any app that can make
// an HTTP request, becomes its own small PDF and is sent straight to the
// printer — nothing else on the report changes or reruns.

// printText renders and prints one note, records it in history, and returns
// the note result so callers can report success/failure back to the sender.
func (s *Server) printText(ctx context.Context, cfg *config.Config, title, text, source string) (*render.NoteResult, error) {
	res, err := s.renderer.GenerateNote(ctx, cfg, title, text)
	if err != nil {
		s.recordNote(source, text, err)
		return nil, err
	}
	if _, err := s.renderer.PrintFile(ctx, cfg, res.PDFPath); err != nil {
		s.recordNote(source, text, err)
		return res, err
	}
	s.recordNote(source, text, nil)
	return res, nil
}

func (s *Server) notesPath() string { return filepath.Join(s.renderer.OutDir, "notes-history.json") }

func (s *Server) recordNote(source, text string, err error) {
	rec := noteRecord{Time: time.Now(), Source: source, Text: text, OK: err == nil}
	if err != nil {
		rec.Error = err.Error()
	}
	s.mu.Lock()
	s.notes = append([]noteRecord{rec}, s.notes...)
	if len(s.notes) > 20 {
		s.notes = s.notes[:20]
	}
	data, mErr := json.MarshalIndent(s.notes, "", "  ")
	s.mu.Unlock()
	if mErr == nil {
		_ = os.MkdirAll(s.renderer.OutDir, 0o755)
		_ = os.WriteFile(s.notesPath(), data, 0o644)
	}
}

func (s *Server) loadNotes() {
	data, err := os.ReadFile(s.notesPath())
	if err != nil {
		return
	}
	var notes []noteRecord
	if json.Unmarshal(data, &notes) == nil {
		s.mu.Lock()
		s.notes = notes
		s.mu.Unlock()
	}
}

// handlePrintTextLocal serves the GUI's own "send a note" box. It is mounted
// on the main GUI mux, so it shares that mux's trust boundary (no token) the
// same way /api/config does.
func (s *Server) handlePrintTextLocal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, 400, fmt.Errorf("bad JSON body"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.printText(ctx, s.store.Get(), body.Title, body.Text, "GUI")
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "pdfPath": res.PDFPath})
}

// handlePrintTextAPI is the external, bearer-token-authenticated endpoint —
// see reconcilePrintAPI for the dedicated listener it runs on.
func (s *Server) handlePrintTextAPI(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	want := cfg.Connectors.Messaging.APIToken
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if want == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		writeErr(w, 401, fmt.Errorf("missing or invalid bearer token"))
		return
	}
	var body struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, 400, fmt.Errorf("bad JSON body"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.printText(ctx, cfg, body.Title, body.Text, "API")
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "pdfPath": res.PDFPath})
}

func (s *Server) handleGenerateAPIToken(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		writeErr(w, 500, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	cfg := s.store.Get()
	cfg.Connectors.Messaging.APIToken = token
	if err := s.store.Save(cfg); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"token": token})
}

func (s *Server) handleTestTelegram(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var body config.Telegram
	_ = json.NewDecoder(r.Body).Decode(&body)
	token := cfg.Connectors.Telegram.BotToken
	if body.BotToken != "" && body.BotToken != config.Masked {
		token = body.BotToken
	}
	tg, err := connectors.NewTelegram(token)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	username, err := tg.GetMe(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": fmt.Sprintf("Connected as @%s. Message the bot from your phone to find your chat ID, then add it below.", username)})
}

// pollTelegram long-polls Telegram for new messages for as long as the
// server runs. Long polling is outbound-only, so this needs no open port or
// public address — the printer can receive a message from anywhere the
// moment it is sent. The bot token is re-read from config each loop so it
// picks up a change made through the GUI without a restart.
func (s *Server) pollTelegram(ctx context.Context) {
	var tg *connectors.Telegram
	var lastToken string
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		cfg := s.store.Get()
		token := cfg.Connectors.Telegram.BotToken
		if token == "" {
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if tg == nil || token != lastToken {
			var err error
			tg, err = connectors.NewTelegram(token)
			if err != nil {
				log.Printf("telegram: %v", err)
				if !sleepCtx(ctx, 5*time.Second) {
					return
				}
				continue
			}
			lastToken = token
		}
		s.mu.Lock()
		offset := s.telegramOffset
		s.mu.Unlock()
		msgs, err := tg.GetUpdates(ctx, offset, 25)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram: poll failed: %v", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, m := range msgs {
			if m.UpdateID+1 > offset {
				offset = m.UpdateID + 1
			}
			if m.ChatID == 0 {
				continue // non-message update
			}
			s.handleTelegramMessage(ctx, tg, cfg, m)
		}
		if len(msgs) > 0 {
			s.mu.Lock()
			s.telegramOffset = offset
			s.mu.Unlock()
			s.saveTelegramOffset(offset)
		}
	}
}

func (s *Server) handleTelegramMessage(ctx context.Context, tg *connectors.Telegram, cfg *config.Config, m connectors.TelegramMessage) {
	if !telegramChatAllowed(cfg.Connectors.Telegram.AllowedChatIDs, m.ChatID) {
		log.Printf("telegram: message from unapproved chat %d (%s)", m.ChatID, m.From)
		_ = tg.SendMessage(ctx, m.ChatID, fmt.Sprintf(
			"This bot isn't set up to print for you yet. Your chat ID is %d — add it under Connectors → Telegram in Tickr to enable printing.", m.ChatID))
		return
	}
	if !m.IsText {
		_ = tg.SendMessage(ctx, m.ChatID, "Only plain text can be printed right now.")
		return
	}
	if _, err := s.printText(ctx, cfg, strings.TrimSpace(m.From), m.Text, "Telegram: "+m.From); err != nil {
		_ = tg.SendMessage(ctx, m.ChatID, "Sorry, that didn't print: "+err.Error())
		return
	}
	_ = tg.SendMessage(ctx, m.ChatID, "🖨️ Printed.")
}

func telegramChatAllowed(allowed []string, chatID int64) bool {
	want := strconv.FormatInt(chatID, 10)
	for _, a := range allowed {
		if strings.TrimSpace(a) == want {
			return true
		}
	}
	return false
}

func (s *Server) telegramOffsetPath() string {
	return filepath.Join(s.renderer.OutDir, "telegram-offset.json")
}

func (s *Server) loadTelegramOffset() {
	data, err := os.ReadFile(s.telegramOffsetPath())
	if err != nil {
		return
	}
	var offset int64
	if json.Unmarshal(data, &offset) == nil {
		s.telegramOffset = offset
	}
}

func (s *Server) saveTelegramOffset(offset int64) {
	data, err := json.Marshal(offset)
	if err != nil {
		return
	}
	_ = os.MkdirAll(s.renderer.OutDir, 0o755)
	_ = os.WriteFile(s.telegramOffsetPath(), data, 0o644)
}

// sleepCtx sleeps for d or returns false early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// reconcilePrintAPI starts, stops or rebinds the external print-text listener
// to match the current config. It is deliberately separate from the main GUI
// listener so a device with only the print token, not full LAN trust, is
// limited to this one narrow capability — print a piece of text, nothing
// else. The listener only opens once a token is set, and only serves POST
// /print-text.
func (s *Server) reconcilePrintAPI() {
	cfg := s.store.Get()
	token := cfg.Connectors.Messaging.APIToken
	port := cfg.Server.PrintPort
	if port == 0 {
		port = 8788
	}
	s.printMu.Lock()
	defer s.printMu.Unlock()
	if token == "" {
		if s.printSrv != nil {
			_ = s.printSrv.Close()
			s.printSrv = nil
			log.Printf("print-text API stopped (no token configured)")
		}
		return
	}
	if s.printSrv != nil {
		if s.printPort == port {
			return // already listening on the right port
		}
		_ = s.printSrv.Close()
		s.printSrv = nil
	}
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /print-text", s.handlePrintTextAPI)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	srv := &http.Server{Addr: addr, Handler: mux}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("print-text API: cannot listen on %s: %v", addr, err)
		return
	}
	s.printSrv = srv
	s.printPort = port
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("print-text API server error: %v", err)
		}
	}()
	log.Printf("print-text API listening on %s (bearer token required)", addr)
}

// runPrintAPILoop keeps the listener in sync with config changes made
// through the GUI (token added/removed/rotated, port changed) without
// requiring a restart.
func (s *Server) runPrintAPILoop(ctx context.Context) {
	s.reconcilePrintAPI()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.printMu.Lock()
			if s.printSrv != nil {
				_ = s.printSrv.Close()
			}
			s.printMu.Unlock()
			return
		case <-ticker.C:
			s.reconcilePrintAPI()
		}
	}
}

func (s *Server) handleGoogleCalendars(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cals, err := connectors.GoogleCalendars(ctx, s.store.Get().Connectors.Google)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, cals)
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	switch r.PathValue("name") {
	case "google":
		cfg.Connectors.Google.RefreshToken = ""
	case "dropbox":
		cfg.Connectors.Dropbox.RefreshToken = ""
		cfg.Tasks.Source = "local"
	case "homeassistant":
		cfg.Connectors.HomeAssistant.Token = ""
	default:
		writeErr(w, 404, fmt.Errorf("unknown connector"))
		return
	}
	if err := s.store.Save(cfg); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, cfg.Redacted())
}

// ── OAuth flows ──────────────────────────────────────────────────────────────
//
// Google and Dropbox redirect to fixed localhost ports registered on their
// developer consoles, so a throwaway listener is started per flow.

func (s *Server) handleGoogleAuthStart(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var body config.Google
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ClientID != "" {
		cfg.Connectors.Google.ClientID = body.ClientID
	}
	if body.ClientSecret != "" && body.ClientSecret != config.Masked {
		cfg.Connectors.Google.ClientSecret = body.ClientSecret
	}
	if len(body.Scopes) > 0 {
		cfg.Connectors.Google.Scopes = body.Scopes
	}
	g := cfg.Connectors.Google
	if g.ClientID == "" || g.ClientSecret == "" {
		writeErr(w, 400, fmt.Errorf("enter the Google client id and secret first, then save"))
		return
	}
	_ = s.store.Save(cfg)
	err := s.startCallback("google", ":3031", "/auth/google/callback", func(ctx context.Context, code string) error {
		token, err := connectors.GoogleExchangeCode(ctx, g, code)
		if err != nil {
			return err
		}
		c := s.store.Get()
		c.Connectors.Google.RefreshToken = token
		return s.store.Save(c)
	})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": connectors.GoogleAuthURL(g)})
}

func (s *Server) handleDropboxAuthStart(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	var body config.Dropbox
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.AppKey != "" {
		cfg.Connectors.Dropbox.AppKey = body.AppKey
	}
	if body.FilePath != "" {
		cfg.Connectors.Dropbox.FilePath = body.FilePath
	}
	_ = s.store.Save(cfg)
	appKey := cfg.Connectors.Dropbox.AppKey
	authURL, verifier, err := connectors.DropboxAuthURL(appKey)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	err = s.startCallback("dropbox", ":3030", "/auth/dropbox/callback", func(ctx context.Context, code string) error {
		token, err := connectors.DropboxExchangeCode(ctx, appKey, code, verifier)
		if err != nil {
			return err
		}
		c := s.store.Get()
		c.Connectors.Dropbox.RefreshToken = token
		c.Tasks.Source = "dropbox"
		return s.store.Save(c)
	})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": authURL})
}

func (s *Server) startCallback(name, addr, path string, exchange func(context.Context, string) error) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if old := s.authSrvs[name]; old != nil {
		_ = old.Close()
	}
	mux := http.NewServeMux()
	srv := &http.Server{Addr: addr, Handler: mux}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if code == "" {
			fmt.Fprint(w, authPage("Missing authorization code", "Please try again from Tickr.", false))
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := exchange(ctx, code); err != nil {
			log.Printf("%s auth failed: %v", name, err)
			fmt.Fprint(w, authPage("Authorization failed", err.Error(), false))
			return
		}
		log.Printf("%s linked", name)
		fmt.Fprint(w, authPage("Linked!", "You can close this tab and return to Tickr.", true))
		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = srv.Close()
		}()
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s for the OAuth callback: %w", addr, err)
	}
	s.authSrvs[name] = srv
	go func() { _ = srv.Serve(ln) }()
	// Give up after ten minutes so a forgotten flow does not hold the port.
	go func() {
		time.Sleep(10 * time.Minute)
		_ = srv.Close()
	}()
	return nil
}

func authPage(title, msg string, ok bool) string {
	color := "#c0392b"
	if ok {
		color = "#2e7d32"
	}
	return fmt.Sprintf(`<!DOCTYPE html><html><head><title>%s</title><style>body{font-family:sans-serif;text-align:center;margin-top:100px;background:#f7f9fa}.card{background:#fff;padding:40px;border-radius:8px;box-shadow:0 4px 12px rgba(0,0,0,.08);display:inline-block}h1{color:%s}</style></head><body><div class="card"><h1>%s</h1><p>%s</p></div></body></html>`, title, color, title, msg)
}
