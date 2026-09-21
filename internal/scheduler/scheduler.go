// Package scheduler fires report generation at the configured times of day.
//
// It is built to survive the realities of a home PC: the machine sleeps, the
// network drops, the server restarts. Each schedule tracks the last planned
// occurrence it handled (persisted to disk), so a restart never double-fires,
// a missed occurrence is caught up within a configurable window, and a failed
// run is retried a few times with a delay.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tickr/internal/config"
)

// RunFunc performs one scheduled run (generate and, if asked, print).
type RunFunc func(ctx context.Context, sched config.Schedule, attempt int) error

// State is what the scheduler remembers about one schedule.
type State struct {
	LastPlanned time.Time `json:"lastPlanned"` // occurrence most recently handled
	LastRun     time.Time `json:"lastRun"`
	LastOK      bool      `json:"lastOk"`
	LastMessage string    `json:"lastMessage"`
	Running     bool      `json:"running"`
	Attempts    int       `json:"attempts"`
}

// Scheduler polls the config store and triggers runs.
type Scheduler struct {
	store     *config.Store
	run       RunFunc
	statePath string
	mu        sync.Mutex
	state     map[string]*State
	now       func() time.Time
	sleep     func(context.Context, time.Duration) bool
}

// New creates a scheduler. dataDir is where scheduler state is persisted.
func New(store *config.Store, dataDir string, run RunFunc) *Scheduler {
	s := &Scheduler{store: store, run: run, state: map[string]*State{}, now: time.Now, sleep: sleepCtx}
	if dataDir != "" {
		s.statePath = filepath.Join(dataDir, "output", "scheduler.json")
		if data, err := os.ReadFile(s.statePath); err == nil {
			_ = json.Unmarshal(data, &s.state)
		}
		for _, st := range s.state {
			st.Running = false
		}
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// Start blocks until ctx is cancelled, checking every 20 seconds.
func (s *Scheduler) Start(ctx context.Context) {
	s.Tick(ctx)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

func key(i int, sch config.Schedule) string { return fmt.Sprintf("%d|%s|%s", i, sch.Name, sch.Time) }

// Tick checks every schedule once. Exposed for tests.
func (s *Scheduler) Tick(ctx context.Context) {
	cfg := s.store.Get()
	loc := cfg.Location()
	now := s.now().In(loc)
	for i, sch := range cfg.Schedules {
		if !sch.Enabled {
			continue
		}
		due, ok := lastDue(sch, now)
		if !ok {
			continue
		}
		k := key(i, sch)
		s.mu.Lock()
		st := s.state[k]
		if st == nil {
			st = &State{}
			s.state[k] = st
		}
		if !due.After(st.LastPlanned) || st.Running {
			s.mu.Unlock()
			continue
		}
		late := now.Sub(due)
		catchUp := time.Duration(sch.CatchUpMinutes) * time.Minute
		if late > 90*time.Second && late > catchUp {
			// Too late to catch up: mark it handled so it does not fire tomorrow's slot early.
			st.LastPlanned = due
			st.LastMessage = fmt.Sprintf("missed %s (server was not running)", due.Format("Mon 3:04 PM"))
			s.mu.Unlock()
			log.Printf("schedule %q: missed the %s run, outside the %d-minute catch-up window", sch.Name, due.Format("Mon 3:04 PM"), sch.CatchUpMinutes)
			s.persist()
			continue
		}
		st.LastPlanned = due
		st.Running = true
		st.Attempts = 0
		s.mu.Unlock()
		if late > 90*time.Second {
			log.Printf("schedule %q: catching up the %s run (%s late)", sch.Name, due.Format("3:04 PM"), late.Round(time.Minute))
		}
		s.persist()
		go s.execute(ctx, k, sch)
	}
}

// execute runs a schedule with retries and records the outcome.
func (s *Scheduler) execute(ctx context.Context, k string, sch config.Schedule) {
	retries := sch.Retries
	delay := time.Duration(sch.RetryDelayMinutes) * time.Minute
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	var err error
	for attempt := 0; attempt <= retries; attempt++ {
		s.mu.Lock()
		s.state[k].Attempts = attempt + 1
		s.mu.Unlock()
		log.Printf("schedule %q: run attempt %d/%d", sch.Name, attempt+1, retries+1)
		err = s.run(ctx, sch, attempt)
		if err == nil {
			break
		}
		log.Printf("schedule %q: attempt %d failed: %v", sch.Name, attempt+1, err)
		if attempt < retries && !s.sleep(ctx, delay) {
			break
		}
	}
	s.mu.Lock()
	st := s.state[k]
	st.Running = false
	st.LastRun = s.now()
	st.LastOK = err == nil
	if err != nil {
		st.LastMessage = err.Error()
	} else {
		st.LastMessage = "ok"
	}
	s.mu.Unlock()
	s.persist()
}

// RunNow triggers a schedule immediately, regardless of its time.
func (s *Scheduler) RunNow(ctx context.Context, index int) error {
	cfg := s.store.Get()
	if index < 0 || index >= len(cfg.Schedules) {
		return fmt.Errorf("no schedule %d", index)
	}
	sch := cfg.Schedules[index]
	k := key(index, sch)
	s.mu.Lock()
	st := s.state[k]
	if st == nil {
		st = &State{}
		s.state[k] = st
	}
	if st.Running {
		s.mu.Unlock()
		return fmt.Errorf("schedule %q is already running", sch.Name)
	}
	st.Running = true
	s.mu.Unlock()
	go s.execute(ctx, k, sch)
	return nil
}

// Status reports each schedule's state alongside its next occurrence.
type Status struct {
	Index   int       `json:"index"`
	Name    string    `json:"name"`
	Enabled bool      `json:"enabled"`
	Next    time.Time `json:"next"`
	State   State     `json:"state"`
}

// Statuses lists every schedule.
func (s *Scheduler) Statuses() []Status {
	cfg := s.store.Get()
	loc := cfg.Location()
	now := s.now().In(loc)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Status
	for i, sch := range cfg.Schedules {
		st := Status{Index: i, Name: sch.Name, Enabled: sch.Enabled}
		if sch.Enabled {
			st.Next, _ = nextDue(sch, now)
		}
		if ss := s.state[key(i, sch)]; ss != nil {
			st.State = *ss
		}
		out = append(out, st)
	}
	return out
}

// Next returns the next time any enabled schedule will fire, and its name.
func (s *Scheduler) Next() (time.Time, string, bool) {
	cfg := s.store.Get()
	now := s.now().In(cfg.Location())
	var best time.Time
	var name string
	for _, sch := range cfg.Schedules {
		if !sch.Enabled {
			continue
		}
		if t, ok := nextDue(sch, now); ok && (best.IsZero() || t.Before(best)) {
			best, name = t, sch.Name
		}
	}
	return best, name, !best.IsZero()
}

func (s *Scheduler) persist() {
	if s.statePath == "" {
		return
	}
	s.mu.Lock()
	data, err := json.MarshalIndent(s.state, "", " ")
	s.mu.Unlock()
	if err == nil {
		_ = os.MkdirAll(filepath.Dir(s.statePath), 0o755)
		_ = os.WriteFile(s.statePath, data, 0o644)
	}
}

// lastDue returns the most recent occurrence of the schedule at or before now.
func lastDue(sch config.Schedule, now time.Time) (time.Time, bool) {
	clock, err := config.ParseClock(sch.Time)
	if err != nil {
		return time.Time{}, false
	}
	for d := 0; d <= 7; d++ {
		day := now.AddDate(0, 0, -d)
		if !dayEnabled(sch, day.Weekday()) {
			continue
		}
		t := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour, clock.Minute, 0, 0, now.Location())
		if !t.After(now) {
			return t, true
		}
	}
	return time.Time{}, false
}

// nextDue returns the next occurrence strictly after now.
func nextDue(sch config.Schedule, now time.Time) (time.Time, bool) {
	clock, err := config.ParseClock(sch.Time)
	if err != nil {
		return time.Time{}, false
	}
	for d := 0; d <= 7; d++ {
		day := now.AddDate(0, 0, d)
		if !dayEnabled(sch, day.Weekday()) {
			continue
		}
		t := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour, clock.Minute, 0, 0, now.Location())
		if t.After(now) {
			return t, true
		}
	}
	return time.Time{}, false
}

func dayEnabled(sch config.Schedule, wd time.Weekday) bool {
	if len(sch.Days) == 0 {
		return true
	}
	for _, d := range sch.Days {
		if time.Weekday(d) == wd {
			return true
		}
	}
	return false
}
