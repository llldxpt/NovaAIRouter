package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type ProcessState string

const (
	StateStopped  ProcessState = "stopped"
	StateStarting ProcessState = "starting"
	StateRunning  ProcessState = "running"
	StateStopping ProcessState = "stopping"
)

type ManagedProcess struct {
	Name       string
	State      ProcessState
	Binary     string
	Args       []string
	WorkDir    string
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	mu         sync.RWMutex
	logLines   []string
	logMax     int
	startTime  time.Time
	onStateChange func(string, ProcessState)
}

func NewManagedProcess(name, binary, workDir string, args []string) *ManagedProcess {
	return &ManagedProcess{
		Name:     name,
		State:    StateStopped,
		Binary:   binary,
		Args:     args,
		WorkDir:  workDir,
		logLines: make([]string, 0),
		logMax:   200,
	}
}

func (mp *ManagedProcess) SetState(state ProcessState) {
	mp.mu.Lock()
	old := mp.State
	mp.State = state
	if state == StateRunning {
		mp.startTime = time.Now()
	}
	mp.mu.Unlock()
	if old != state && mp.onStateChange != nil {
		mp.onStateChange(mp.Name, state)
	}
}

func (mp *ManagedProcess) GetState() ProcessState {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.State
}

func (mp *ManagedProcess) GetLogs() []string {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	result := make([]string, len(mp.logLines))
	copy(result, mp.logLines)
	return result
}

func (mp *ManagedProcess) addLog(line string) {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	mp.logLines = append(mp.logLines, line)
	if len(mp.logLines) > mp.logMax {
		mp.logLines = mp.logLines[len(mp.logLines)-mp.logMax:]
	}
}

func (mp *ManagedProcess) Uptime() time.Duration {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	if mp.State != StateRunning {
		return 0
	}
	return time.Since(mp.startTime).Truncate(time.Second)
}

func (mp *ManagedProcess) Start() error {
	mp.mu.Lock()
	if mp.State == StateRunning || mp.State == StateStarting {
		mp.mu.Unlock()
		return fmt.Errorf("%s is already running", mp.Name)
	}
	mp.mu.Unlock()

	mp.SetState(StateStarting)

	ctx, cancel := context.WithCancel(context.Background())
	mp.cancel = cancel

	mp.cmd = exec.CommandContext(ctx, mp.Binary, mp.Args...)
	if mp.WorkDir != "" {
		mp.cmd.Dir = mp.WorkDir
	}

	stdout, _ := mp.cmd.StdoutPipe()
	stderr, _ := mp.cmd.StderrPipe()

	if err := mp.cmd.Start(); err != nil {
		mp.SetState(StateStopped)
		return fmt.Errorf("failed to start %s: %w", mp.Name, err)
	}

	mp.SetState(StateRunning)

	// Read stdout/stderr in background
	go mp.readOutput(stdout, "OUT")
	go mp.readOutput(stderr, "ERR")

	// Monitor process exit
	go func() {
		err := mp.cmd.Wait()
		mp.mu.Lock()
		if mp.State == StateRunning {
			mp.State = StateStopped
			if err != nil {
				mp.addLog(fmt.Sprintf("[manager] %s exited: %v", mp.Name, err))
			} else {
				mp.addLog(fmt.Sprintf("[manager] %s exited normally", mp.Name))
			}
		}
		mp.mu.Unlock()
		if mp.onStateChange != nil {
			mp.onStateChange(mp.Name, StateStopped)
		}
	}()

	mp.addLog(fmt.Sprintf("[manager] %s started (PID: %d)", mp.Name, mp.cmd.Process.Pid))
	return nil
}

func (mp *ManagedProcess) Stop() error {
	mp.mu.Lock()
	if mp.State != StateRunning {
		mp.mu.Unlock()
		return fmt.Errorf("%s is not running (state: %s)", mp.Name, mp.State)
	}
	mp.mu.Unlock()

	mp.SetState(StateStopping)

	if mp.cancel != nil {
		mp.cancel()
	}

	// Give it a moment to exit gracefully
	time.Sleep(1 * time.Second)

	if mp.cmd != nil && mp.cmd.Process != nil {
		mp.cmd.Process.Kill()
	}

	mp.SetState(StateStopped)
	mp.addLog(fmt.Sprintf("[manager] %s stopped", mp.Name))
	return nil
}

func (mp *ManagedProcess) readOutput(r io.Reader, prefix string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		mp.addLog(scanner.Text())
	}
}
