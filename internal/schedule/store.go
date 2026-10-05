package schedule

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Run is one run of a job, as its history keeps it.
type Run struct {
	ID      string    `json:"id"`
	Job     string    `json:"job"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end,omitempty"`
	Status  string    `json:"status"`
	Reason  string    `json:"reason,omitempty"` // scheduled, catch-up, manual, paced
	Session string    `json:"session,omitempty"`
	Text    string    `json:"text,omitempty"` // the answer, cut short
	Error   string    `json:"error,omitempty"`
	Skipped string    `json:"skipped,omitempty"` // why it did not run
}

// keepRuns is how many runs a job's history keeps.
const keepRuns = 200

// Store keeps jobs in <data>/schedule/jobs.json and each job's runs in
// <data>/schedule/runs/<id>.jsonl.
type Store struct {
	Dir string
	mu  sync.Mutex
}

// DefaultStore is the store in this user's data folder.
func DefaultStore() *Store { return &Store{Dir: filepath.Join(config.DataDir(), "schedule")} }

func (s *Store) jobsPath() string { return filepath.Join(s.Dir, "jobs.json") }

func (s *Store) load() ([]*Job, error) {
	b, err := os.ReadFile(s.jobsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []*Job
	if err := json.Unmarshal(b, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) save(jobs []*Job) error {
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].Created.Before(jobs[b].Created) })
	return config.WriteJSON(s.jobsPath(), jobs)
}

// List returns every job.
func (s *Store) List() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Get returns one job.
func (s *Store) Get(id string) (*Job, bool) {
	jobs, _ := s.List()
	for _, j := range jobs {
		if j.ID == id {
			return j, true
		}
	}
	return nil, false
}

// Update applies fn to the job with id under the store's lock and saves it.
// fn returning false deletes the job.
func (s *Store) Update(id string, fn func(j *Job) bool) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.load()
	if err != nil {
		return nil, err
	}
	for i, j := range jobs {
		if j.ID != id {
			continue
		}
		if !fn(j) {
			jobs = append(jobs[:i], jobs[i+1:]...)
			return nil, s.save(jobs)
		}
		return j, s.save(jobs)
	}
	return nil, errors.New("no such job")
}

// Put saves a job, new or edited. A new one gets an ID.
func (s *Store) Put(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.load()
	if err != nil {
		return err
	}
	now := time.Now()
	j.Updated = now
	if j.ID == "" {
		j.ID = newID()
		j.Created = now
		jobs = append(jobs, j)
		return s.save(jobs)
	}
	for i, old := range jobs {
		if old.ID == j.ID {
			j.Created = old.Created
			jobs[i] = j
			return s.save(jobs)
		}
	}
	return errors.New("no such job")
}

// Delete removes a job and its history.
func (s *Store) Delete(id string) error {
	if _, err := s.Update(id, func(*Job) bool { return false }); err != nil {
		return err
	}
	_ = os.Remove(s.runsPath(id))
	return nil
}

func (s *Store) runsPath(id string) string { return filepath.Join(s.Dir, "runs", id+".jsonl") }

// AddRun appends a run to its job's history, keeping the newest keepRuns.
func (s *Store) AddRun(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.runsPath(r.Job)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	f.Close()
	if err != nil {
		return err
	}
	lines := readLines(p)
	if len(lines) > keepRuns+keepRuns/4 {
		lines = lines[len(lines)-keepRuns:]
		_ = os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	return nil
}

// Runs returns a job's newest runs, newest first.
func (s *Store) Runs(id string, n int) []Run {
	s.mu.Lock()
	lines := readLines(s.runsPath(id))
	s.mu.Unlock()
	var out []Run
	for i := len(lines) - 1; i >= 0 && (n <= 0 || len(out) < n); i-- {
		var r Run
		if json.Unmarshal([]byte(lines[i]), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func readLines(p string) []string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
