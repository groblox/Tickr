package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"breaklist/internal/config"
)

func newTestStore(t *testing.T, sch config.Schedule) *config.Store {
	t.Helper()
	store := config.NewStore(t.TempDir())
	cfg, _ := store.Load()
	cfg.General.Timezone = "UTC"
	cfg.Schedules = []config.Schedule{sch}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return store
}

type recorder struct {
	mu   sync.Mutex
	runs []time.Time
	fail int // number of initial attempts that should fail
}

func (r *recorder) run(_ context.Context, _ config.Schedule, attempt int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, time.Now())
	if attempt < r.fail {
		return errors.New("boom")
	}
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

func waitIdle(s *Scheduler) {
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		running := false
		for _, st := range s.state {
			running = running || st.Running
		}
		s.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFiresOnceAndPersists(t *testing.T) {
	store := newTestStore(t, config.Schedule{Name: "test", Enabled: true, Time: "07:30", Days: []int{1, 2, 3, 4, 5}, CatchUpMinutes: 120})
	rec := &recorder{}
	s := New(store, t.TempDir(), rec.run)
	fake := time.Date(2024, 6, 3, 7, 30, 5, 0, time.UTC) // Monday
	s.now = func() time.Time { return fake }
	s.Tick(context.Background())
	waitIdle(s)
	s.Tick(context.Background())
	fake = fake.Add(40 * time.Second)
	s.Tick(context.Background())
	waitIdle(s)
	if rec.count() != 1 {
		t.Fatalf("expected 1 run, got %d", rec.count())
	}
	// A restart with persisted state must not fire again for the same slot.
	s2 := New(store, s.statePath[:len(s.statePath)-len("/output/scheduler.json")], rec.run)
	s2.now = s.now
	s2.Tick(context.Background())
	waitIdle(s2)
	if rec.count() != 1 {
		t.Fatalf("restart double-fired: %d", rec.count())
	}
	// Saturday: disabled day.
	fake = time.Date(2024, 6, 8, 7, 30, 0, 0, time.UTC)
	s.Tick(context.Background())
	waitIdle(s)
	if rec.count() != 1 {
		t.Fatalf("weekend should not fire, got %d", rec.count())
	}
	next, name, ok := s.Next()
	if !ok || name != "test" || next.Weekday() != time.Monday || next.Hour() != 7 {
		t.Fatalf("next run wrong: %v %s %v", next, name, ok)
	}
}

func TestCatchUpWindow(t *testing.T) {
	store := newTestStore(t, config.Schedule{Name: "morning", Enabled: true, Time: "06:30", CatchUpMinutes: 120})
	rec := &recorder{}
	s := New(store, "", rec.run)
	// Server starts 45 minutes late: inside the window, so it should run.
	s.now = func() time.Time { return time.Date(2024, 6, 3, 7, 15, 0, 0, time.UTC) }
	s.Tick(context.Background())
	waitIdle(s)
	if rec.count() != 1 {
		t.Fatalf("late start inside window should fire, got %d", rec.count())
	}
	// Next day, 5 hours late: outside the window, skip.
	s.now = func() time.Time { return time.Date(2024, 6, 4, 11, 30, 0, 0, time.UTC) }
	s.Tick(context.Background())
	waitIdle(s)
	if rec.count() != 1 {
		t.Fatalf("late start outside window should not fire, got %d", rec.count())
	}
	st := s.Statuses()[0].State
	if st.LastMessage == "" || st.LastPlanned.Day() != 4 {
		t.Fatalf("missed run should be recorded: %+v", st)
	}
}

func TestRetries(t *testing.T) {
	store := newTestStore(t, config.Schedule{Name: "r", Enabled: true, Time: "08:00", Retries: 2, RetryDelayMinutes: 1})
	rec := &recorder{fail: 2}
	s := New(store, "", rec.run)
	s.sleep = func(context.Context, time.Duration) bool { return true }
	s.now = func() time.Time { return time.Date(2024, 6, 3, 8, 0, 10, 0, time.UTC) }
	s.Tick(context.Background())
	waitIdle(s)
	if rec.count() != 3 {
		t.Fatalf("expected 3 attempts (2 failures + success), got %d", rec.count())
	}
	st := s.Statuses()[0].State
	if !st.LastOK || st.Attempts != 3 {
		t.Fatalf("state wrong: %+v", st)
	}
	if err := s.RunNow(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	waitIdle(s)
	// RunNow goes through the same retry loop: two failures then success.
	if rec.count() != 6 {
		t.Fatalf("RunNow should add three attempts, got %d", rec.count())
	}
}
