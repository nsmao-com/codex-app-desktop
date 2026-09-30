package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ScheduledTask struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	Workspace       string `json:"workspace"`
	Enabled         bool   `json:"enabled"`
	IntervalMin     int    `json:"intervalMin"`
	UseWorktree     bool   `json:"useWorktree"`
	LastRunAt       int64  `json:"lastRunAt"`
	NextRunAt       int64  `json:"nextRunAt"`
	LastError       string `json:"lastError,omitempty"`
	ActiveSessionID string `json:"activeSessionId,omitempty"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type scheduledTaskStore struct {
	mu       sync.Mutex
	path     string
	tasks    []ScheduledTask
	loadErr  error
	writeErr error
}

func newScheduledTaskStore(settingsPath string) *scheduledTaskStore {
	dir := filepath.Dir(settingsPath)
	return &scheduledTaskStore{
		path:  filepath.Join(dir, "scheduled_tasks.json"),
		tasks: []ScheduledTask{},
	}
}

func (st *scheduledTaskStore) load() (err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	defer func() { st.loadErr = err }()
	payload, err := readProviderFileRecoverable(st.path)
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

func (st *scheduledTaskStore) persistLocked(tasks []ScheduledTask) (err error) {
	if st.loadErr != nil {
		return fmt.Errorf("scheduled tasks could not be loaded; repair %s before saving: %w", st.path, st.loadErr)
	}
	defer func() { st.writeErr = err }()
	payload, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return writeProviderFileAtomic(st.path, payload)
}

func (st *scheduledTaskStore) list() ([]ScheduledTask, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]ScheduledTask, len(st.tasks))
	copy(out, st.tasks)
	if st.loadErr != nil {
		return out, fmt.Errorf("read scheduled tasks: %w", st.loadErr)
	}
	if st.writeErr != nil {
		return out, fmt.Errorf("save scheduled tasks: %w", st.writeErr)
	}
	return out, nil
}

func (st *scheduledTaskStore) retryLoad() {
	st.mu.Lock()
	failed := st.loadErr != nil
	st.mu.Unlock()
	if failed {
		_ = st.load()
	}
}

func (st *scheduledTaskStore) upsert(task ScheduledTask) (ScheduledTask, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now().Unix()
	task.Title = strings.TrimSpace(task.Title)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.Workspace = strings.TrimSpace(task.Workspace)
	task.ActiveSessionID = ""
	if task.Title == "" {
		return ScheduledTask{}, errors.New("title is required")
	}
	if task.Prompt == "" {
		return ScheduledTask{}, errors.New("prompt is required")
	}
	if len([]rune(task.Title)) > 200 || len([]rune(task.Prompt)) > 32_000 {
		return ScheduledTask{}, errors.New("task title or prompt is too long")
	}
	if task.IntervalMin < 5 {
		task.IntervalMin = 5
	}
	if task.IntervalMin > 7*24*60 {
		task.IntervalMin = 7 * 24 * 60
	}
	next := append([]ScheduledTask(nil), st.tasks...)
	task.UpdatedAt = now
	if task.ID == "" {
		task.ID = newScheduledTaskID()
		task.CreatedAt = now
		task.LastRunAt = 0
		task.LastError = ""
		task.NextRunAt = now + int64(task.IntervalMin*60)
		next = append(next, task)
	} else {
		found := false
		for i, previous := range next {
			if previous.ID == task.ID {
				if previous.ActiveSessionID != "" && (task.Prompt != previous.Prompt || task.Title != previous.Title || task.Workspace != previous.Workspace || task.IntervalMin != previous.IntervalMin || task.UseWorktree != previous.UseWorktree) {
					return ScheduledTask{}, errors.New("wait for the running scheduled task to finish before editing")
				}
				task.ActiveSessionID = previous.ActiveSessionID
				task.CreatedAt = previous.CreatedAt
				task.LastRunAt = previous.LastRunAt
				task.LastError = previous.LastError
				task.NextRunAt = previous.NextRunAt
				if task.IntervalMin != previous.IntervalMin || (!previous.Enabled && task.Enabled) || task.NextRunAt <= 0 {
					task.NextRunAt = now + int64(task.IntervalMin*60)
				}
				next[i] = task
				found = true
				break
			}
		}
		if !found {
			return ScheduledTask{}, errors.New("scheduled task no longer exists")
		}
	}
	if err := st.persistLocked(next); err != nil {
		return ScheduledTask{}, err
	}
	st.tasks = next
	return task, nil
}

func (st *scheduledTaskStore) delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("task id is required")
	}
	next := make([]ScheduledTask, 0, len(st.tasks))
	for _, task := range st.tasks {
		if task.ID == id && task.ActiveSessionID != "" {
			return errors.New("wait for the running scheduled task to finish before deleting")
		}
		if task.ID != id {
			next = append(next, task)
		}
	}
	if len(next) == len(st.tasks) {
		return errors.New("scheduled task no longer exists")
	}
	if err := st.persistLocked(next); err != nil {
		return err
	}
	st.tasks = next
	return nil
}

func (st *scheduledTaskStore) due(now int64) []ScheduledTask {
	st.mu.Lock()
	defer st.mu.Unlock()
	var due []ScheduledTask
	for _, task := range st.tasks {
		if st.loadErr == nil && task.ActiveSessionID == "" && task.Enabled && task.NextRunAt > 0 && task.NextRunAt <= now {
			due = append(due, task)
		}
	}
	return due
}

// Claim and persist the execution identity before creating a conversation or
// submitting a turn. A failed write must never launch an untracked task.
func (st *scheduledTaskStore) claim(id, sessionID string, now int64) (ScheduledTask, bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, task := range st.tasks {
		if task.ID != id || !task.Enabled || task.ActiveSessionID != "" || task.NextRunAt <= 0 || task.NextRunAt > now {
			continue
		}
		task.ActiveSessionID = sessionID
		task.LastRunAt = now
		task.NextRunAt = now + int64(task.IntervalMin*60)
		task.UpdatedAt = now
		task.LastError = ""
		next := append([]ScheduledTask(nil), st.tasks...)
		next[i] = task
		if err := st.persistLocked(next); err != nil {
			return ScheduledTask{}, false, err
		}
		st.tasks = next
		return task, true, nil
	}
	return ScheduledTask{}, false, nil
}

func (st *scheduledTaskStore) active() []ScheduledTask {
	st.mu.Lock()
	defer st.mu.Unlock()
	var result []ScheduledTask
	for _, task := range st.tasks {
		if task.ActiveSessionID != "" {
			result = append(result, task)
		}
	}
	return result
}

func (st *scheduledTaskStore) finish(id, sessionID string, runErr error) error {
	return st.recordResult(id, sessionID, runErr, true)
}

func (st *scheduledTaskStore) recordResult(id, sessionID string, runErr error, finished bool) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, task := range st.tasks {
		if task.ID != id || task.ActiveSessionID != sessionID {
			continue
		}
		if finished {
			task.ActiveSessionID = ""
		}
		task.UpdatedAt = time.Now().Unix()
		task.LastError = ""
		if runErr != nil {
			task.LastError = runErr.Error()
		}
		next := append([]ScheduledTask(nil), st.tasks...)
		next[i] = task
		if err := st.persistLocked(next); err != nil {
			return err
		}
		st.tasks = next
		return nil
	}
	return nil
}

func newScheduledTaskID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(buf[:])
}

func (s *AppService) runScheduledTaskLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.schedulerStop:
			return
		case <-ticker.C:
			s.tickScheduledTasks()
		}
	}
}

func (s *AppService) tickScheduledTasks() {
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.scheduledTasks == nil || s.serviceContext().Err() != nil {
		return
	}
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	ready := client != nil && client.Status().State == "ready"
	for _, task := range s.scheduledTasks.active() {
		if s.serviceContext().Err() != nil {
			return
		}
		if client == nil || !client.Status().Running {
			_ = s.scheduledTasks.finish(task.ID, task.ActiveSessionID, errors.New("Codex disconnected before the scheduled task completed"))
			continue
		}
		if !ready {
			continue
		}
		record := s.sessionForIDAny(task.ActiveSessionID)
		if record == nil || record.BackendRef == "" {
			_ = s.scheduledTasks.finish(task.ID, task.ActiveSessionID, errors.New("scheduled task was interrupted before its conversation was ready"))
			continue
		}
		live, err := s.ReadThreadLiveness(task.ActiveSessionID)
		if err != nil || live.Running || lifecycleStatusIsActive(live.LatestTurnStatus) {
			continue
		} // Unknown state must never permit a second run.
		var runErr error
		if task.LastError != "" && live.LatestTurnStatus == "" {
			runErr = errors.New(task.LastError)
		}
		switch live.LatestTurnStatus {
		case "failed", "error", "interrupted", "cancelled", "canceled", "aborted":
			runErr = fmt.Errorf("scheduled conversation ended with status %s", live.LatestTurnStatus)
		}
		_ = s.scheduledTasks.finish(task.ID, task.ActiveSessionID, runErr)
	}
	if !ready {
		return
	} // Leave overdue tasks pending until the app-server is connected.
	for _, candidate := range s.scheduledTasks.due(time.Now().Unix()) {
		if s.serviceContext().Err() != nil {
			return
		}
		task, claimed, err := s.scheduledTasks.claim(candidate.ID, newUUID(), time.Now().Unix())
		if err != nil || !claimed {
			continue
		}
		if err := s.executeScheduledTask(task); err != nil {
			record := s.sessionForIDAny(task.ActiveSessionID)
			// A transport timeout after turn/start is ambiguous. Keep ownership
			// until a native thread snapshot proves that execution has stopped.
			finished := record == nil || record.BackendRef == ""
			_ = s.scheduledTasks.recordResult(task.ID, task.ActiveSessionID, err, finished)
		}
	}
}

func (s *AppService) executeScheduledTask(task ScheduledTask) error {
	if err := s.serviceContext().Err(); err != nil {
		return err
	}
	clean, err := validateWorkspace(task.Workspace)
	if err != nil {
		return err
	}
	runWorkspace := clean
	if task.UseWorktree {
		runWorkspace, err = ensureScheduledWorktree(s.serviceContext(), clean, task.ID)
		if err != nil {
			return err
		}
	}
	if err := s.serviceContext().Err(); err != nil {
		return err
	}
	settings := s.Settings()
	collaborationMode := normalizeCollaborationMode(settings.CollaborationMode)
	if collaborationMode == "" {
		collaborationMode = "default"
	}
	note := "Scheduled task: " + task.Title
	if task.UseWorktree && runWorkspace != clean {
		note += " (git worktree: " + runWorkspace + ")"
	}
	prompt := strings.TrimSpace(task.Prompt)
	if prompt == "" {
		return errors.New("empty scheduled prompt")
	}
	// Scheduled tasks use Codex even when another runtime is visible.
	record := s.createSessionRecord(runWorkspace, "", "", settings.Model, settings.Effort, collaborationMode, normalizeWorkMode(settings.WorkMode))
	record.ID = task.ActiveSessionID
	record.Name = task.Title
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[string]*SessionRecord)
	}
	s.sessions[record.ID] = record
	err = s.persistSessionsLocked()
	if err != nil {
		delete(s.sessions, record.ID)
	}
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("save scheduled conversation: %w", err)
	}
	s.rememberThread(record.ID, runWorkspace)
	_, err = s.SendMessage(SendMessageRequest{ThreadID: record.ID, Text: note + "\n\n" + prompt})
	return err
}

func ensureScheduledWorktree(ctx context.Context, workspace, taskID string) (string, error) {
	safeID := sanitizeFileToken(taskID)
	if safeID == "" {
		return "", errors.New("scheduled task id is required")
	}
	if len(safeID) > 24 {
		safeID = safeID[:24]
	}
	root := filepath.Join(workspace, ".nice-codex", "worktrees")
	target := filepath.Join(root, safeID)
	branch := "nice-codex/sched-" + safeID
	listing, err := runGitWithContext(ctx, workspace, 5*time.Second, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", errors.New("a scheduled worktree requires a Git repository")
	}
	if _, statErr := os.Stat(target); statErr == nil {
		for _, field := range strings.Split(listing, "\x00") {
			if strings.HasPrefix(field, "worktree ") && samePath(strings.TrimPrefix(field, "worktree "), target) {
				return target, nil
			}
		}
		return "", errors.New("scheduled worktree path exists but is not registered with this repository")
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	args := []string{"worktree", "add", "-b", branch, target}
	if _, err := runGitWithContext(ctx, workspace, 5*time.Second, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		args = []string{"worktree", "add", target, branch}
	}
	output, err := runGitWithContext(ctx, workspace, 90*time.Second, args...)
	if err != nil {
		return "", fmt.Errorf("git worktree add failed: %w: %s", err, strings.TrimSpace(output))
	}
	return target, nil
}
