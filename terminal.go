package main

import (
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"

	"nice_codex_desktop/internal/codex"

	gopty "github.com/aymanbagabas/go-pty"
)

type TerminalProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

type terminalSession struct {
	pty       gopty.Pty
	cmd       *gopty.Cmd
	writeMu   sync.Mutex
	stateMu   sync.Mutex
	stopped   bool
	closeOnce sync.Once
}

func (session *terminalSession) closePty() {
	if session == nil || session.pty == nil {
		return
	}
	session.closeOnce.Do(func() {
		_ = session.pty.Close()
	})
}

func listTerminalProfiles() []TerminalProfile {
	return platformTerminalProfiles()
}

func (s *AppService) OpenTerminal() error {
	settings := s.Settings()
	workspace, err := validateWorkspace(settings.Workspace)
	if err != nil {
		return err
	}
	profile := settings.TerminalProfile
	for _, option := range listTerminalProfiles() {
		if option.ID == profile && option.Available {
			return launchTerminal(profile, workspace)
		}
	}
	return errors.New("the selected terminal is not available on this computer")
}

func (s *AppService) StartTerminalSession(processID string) error {
	processID = strings.TrimSpace(processID)
	if processID == "" || len(processID) > 120 {
		return errors.New("a valid terminal process id is required")
	}
	settings := s.Settings()
	workspace, err := validateWorkspace(settings.Workspace)
	if err != nil {
		return err
	}
	command, err := terminalCommand(settings.TerminalProfile, workspace)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, exists := s.terminalSessions[processID]; exists {
		s.mu.Unlock()
		return errors.New("terminal process id is already in use")
	}
	s.mu.Unlock()

	ptySession, err := gopty.New()
	if err != nil {
		return err
	}
	_ = ptySession.Resize(120, 32)

	cmd := ptySession.Command(command[0], command[1:]...)
	cmd.Dir = workspace
	configureTerminalCmd(cmd)
	if err := cmd.Start(); err != nil {
		_ = ptySession.Close()
		return err
	}

	session := &terminalSession{pty: ptySession, cmd: cmd}
	s.mu.Lock()
	s.terminalSessions[processID] = session
	s.mu.Unlock()

	go s.readTerminalStream(processID, ptySession)
	go s.waitTerminalSession(processID, session)
	return nil
}

func (s *AppService) WriteTerminal(processID string, input string) error {
	processID = strings.TrimSpace(processID)
	if processID == "" || len(input) > 1024*1024 {
		return errors.New("invalid terminal input")
	}
	s.mu.Lock()
	session := s.terminalSessions[processID]
	s.mu.Unlock()
	if session == nil {
		return errors.New("terminal session is not running")
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	_, err := io.WriteString(session.pty, input)
	return err
}

func (s *AppService) ResizeTerminal(processID string, rows int, cols int) error {
	if rows < 4 || rows > 500 || cols < 20 || cols > 1000 {
		return errors.New("invalid terminal size")
	}
	processID = strings.TrimSpace(processID)
	s.mu.Lock()
	session := s.terminalSessions[processID]
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.pty.Resize(cols, rows)
}

func (s *AppService) StopTerminalSession(processID string) error {
	processID = strings.TrimSpace(processID)
	s.mu.Lock()
	session := s.terminalSessions[processID]
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	session.stateMu.Lock()
	if session.stopped {
		session.stateMu.Unlock()
		return nil
	}
	session.stopped = true
	session.stateMu.Unlock()

	// Close ConPTY first. Per Microsoft docs this ends attached console clients.
	// Avoid racing a second Close from Wait — closePty is once-only.
	session.closePty()
	if session.cmd != nil && session.cmd.Process != nil {
		_ = session.cmd.Process.Kill()
	}
	return nil
}

func (s *AppService) readTerminalStream(processID string, reader io.Reader) {
	buffer := make([]byte, 16*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			s.app.Event.Emit("codex:event", codex.Event{
				Type:   "notification",
				Method: "command/exec/outputDelta",
				Data: map[string]any{
					"processId":   processID,
					"deltaBase64": base64.StdEncoding.EncodeToString(buffer[:count]),
				},
			})
		}
		if err != nil {
			return
		}
	}
}

func (s *AppService) waitTerminalSession(processID string, session *terminalSession) {
	var waitErr error
	if session.cmd != nil {
		waitErr = session.cmd.Wait()
	}
	s.mu.Lock()
	if s.terminalSessions[processID] == session {
		delete(s.terminalSessions, processID)
	}
	s.mu.Unlock()
	session.stateMu.Lock()
	stopped := session.stopped
	session.stateMu.Unlock()
	session.closePty()
	result := map[string]any{"processId": processID}
	if waitErr != nil && !stopped {
		result["error"] = waitErr.Error()
	}
	s.app.Event.Emit("codex:event", codex.Event{Type: "notification", Method: "nice/terminal/exit", Data: result})
}

func (s *AppService) stopAllTerminalSessions() {
	s.mu.Lock()
	sessions := make([]*terminalSession, 0, len(s.terminalSessions))
	for _, session := range s.terminalSessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		session.stateMu.Lock()
		session.stopped = true
		session.stateMu.Unlock()
		session.closePty()
		if session.cmd != nil && session.cmd.Process != nil {
			_ = session.cmd.Process.Kill()
		}
	}
}
