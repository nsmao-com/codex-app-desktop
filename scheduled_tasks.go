package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ScheduledTask struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Prompt       string `json:"prompt"`
	Workspace    string `json:"workspace"`
	Enabled      bool   `json:"enabled"`
	IntervalMin  int    `json:"intervalMin"`
	UseWorktree  bool   `json:"useWorktree"`
	LastRunAt    int64  `json:"lastRunAt"`
	NextRunAt    int64  `json:"nextRunAt"`
	LastError    string `json:"lastError,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

type scheduledTaskStore struct {
	mu    sync.Mutex
	path  string
	tasks []ScheduledTask
}

func newScheduledTaskStore(settingsPath string) *scheduledTaskStore {
	dir := filepath.Dir(settingsPath)
	return &scheduledTaskStore{
		path:  filepath.Join(dir, "scheduled_tasks.json"),
		tasks: []ScheduledTask{},
	}
}

func (st *scheduledTaskStore) load() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	payload, err := os.ReadFile(st.path)
	if err != nil {
		if os.IsNotExist(err) {
			st.tasks = []ScheduledTask{}
			return nil
		}
		return err
	}
	var tasks []ScheduledTask
	if err := json.Unmarshal(payload, &tasks); err != nil {
		return err
	}
	st.tasks = tasks
	return nil
}

func (st *scheduledTaskStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(st.tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(st.path, payload, 0o600)
}

func (st *scheduledTaskStore) list() []ScheduledTask {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]ScheduledTask, len(st.tasks))
	copy(out, st.tasks)
	return out
}

func (st *scheduledTaskStore) upsert(task ScheduledTask) (ScheduledTask, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now().Unix()
	task.Title = strings.TrimSpace(task.Title)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.Workspace = strings.TrimSpace(task.Workspace)
	if task.Title == "" {
		return ScheduledTask{}, errors.New("title is required")
	}
	if task.Prompt == "" {
		return ScheduledTask{}, errors.New("prompt is required")
	}
	if task.IntervalMin < 5 {
		task.IntervalMin = 5
	}
	if task.IntervalMin > 7*24*60 {
		task.IntervalMin = 7 * 24 * 60
	}
	if task.ID == "" {
		task.ID = newScheduledTaskID()
		task.CreatedAt = now
		if task.NextRunAt == 0 {
			task.NextRunAt = now + int64(task.IntervalMin*60)
		}
		st.tasks = append(st.tasks, task)
	} else {
		found := false
		for i := range st.tasks {
			if st.tasks[i].ID == task.ID {
				task.CreatedAt = st.tasks[i].CreatedAt
				task.LastRunAt = st.tasks[i].LastRunAt
				if task.NextRunAt == 0 {
					task.NextRunAt = st.tasks[i].NextRunAt
				}
				st.tasks[i] = task
				found = true
				break
			}
		}
		if !found {
			task.CreatedAt = now
			st.tasks = append(st.tasks, task)
		}
	}
	task.UpdatedAt = now
	for i := range st.tasks {
		if st.tasks[i].ID == task.ID {
			st.tasks[i].UpdatedAt = now
			task = st.tasks[i]
			break
		}
	}
	if err := st.persistLocked(); err != nil {
		return ScheduledTask{}, err
	}
	return task, nil
}

func (st *scheduledTaskStore) delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	id = strings.TrimSpace(id)
	next := st.tasks[:0]
	for _, task := range st.tasks {
		if task.ID != id {
			next = append(next, task)
		}
	}
	st.tasks = next
	return st.persistLocked()
}

func (st *scheduledTaskStore) due(now int64) []ScheduledTask {
	st.mu.Lock()
	defer st.mu.Unlock()
	var due []ScheduledTask
	for _, task := range st.tasks {
		if task.Enabled && task.NextRunAt > 0 && task.NextRunAt <= now {
			due = append(due, task)
		}
	}
	return due
}

func (st *scheduledTaskStore) markRan(id string, runErr error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now().Unix()
	for i := range st.tasks {
		if st.tasks[i].ID != id {
			continue
		}
		st.tasks[i].LastRunAt = now
		st.tasks[i].NextRunAt = now + int64(st.tasks[i].IntervalMin*60)
		st.tasks[i].UpdatedAt = now
		if runErr != nil {
			st.tasks[i].LastError = runErr.Error()
		} else {
			st.tasks[i].LastError = ""
		}
		break
	}
	_ = st.persistLocked()
}

func newScheduledTaskID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(buf[:])
}
