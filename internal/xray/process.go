package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"common-vpn-router/internal/storage"
)

type ProcessStatus struct {
	Running   bool      `json:"running"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	LastExit  string    `json:"lastExit,omitempty"`
}

type ProcessManager struct {
	mu        sync.Mutex
	binary    string
	config    string
	cmd       *exec.Cmd
	done      chan struct{}
	startedAt time.Time
	lastExit  string
	assetDir  string
}

func NewProcessManager(binary, config string) *ProcessManager {
	return &ProcessManager{binary: binary, config: config}
}

func (p *ProcessManager) ValidateConfig(ctx context.Context, path, assetDir string) error {
	binary, err := exec.LookPath(p.binary)
	if err != nil {
		return errors.New("Xray executable was not found")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, binary, "run", "-test", "-config", path)
	cmd.Env = xrayEnvironment(assetDir)
	output := &limitedOutput{limit: 2048}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		captured, truncated := output.snapshot()
		detail := strings.Join(strings.Fields(strings.ToValidUTF8(string(captured), "�")), " ")
		if truncated {
			detail += " … (вывод сокращён)"
		}
		if checkCtx.Err() != nil {
			if detail != "" {
				return fmt.Errorf("проверка конфигурации Xray прервана: %s", detail)
			}
			return fmt.Errorf("проверка конфигурации Xray прервана: %w", checkCtx.Err())
		}
		if detail != "" {
			return fmt.Errorf("Xray отклонил созданную конфигурацию: %s", detail)
		}
		return fmt.Errorf("Xray отклонил созданную конфигурацию (%v)", err)
	}
	return nil
}

// limitedOutput captures enough validator output to diagnose common config
// errors without allowing a broken binary to fill router memory or the UI.
type limitedOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (o *limitedOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	remaining := o.limit - len(o.data)
	if remaining > 0 {
		if len(data) > remaining {
			o.data = append(o.data, data[:remaining]...)
			o.truncated = true
		} else {
			o.data = append(o.data, data...)
		}
	} else if len(data) > 0 {
		o.truncated = true
	}
	return len(data), nil
}

func (o *limitedOutput) snapshot() ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data...), o.truncated
}

func (p *ProcessManager) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runningLocked() {
		return nil
	}
	binary, err := exec.LookPath(p.binary)
	if err != nil {
		return errors.New("Xray executable was not found")
	}
	cmd := exec.Command(binary, "run", "-config", p.config)
	cmd.Env = xrayEnvironment(p.assetDir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Xray: %w", err)
	}
	done := make(chan struct{})
	p.cmd, p.done, p.startedAt = cmd, done, time.Now().UTC()
	p.lastExit = ""
	go p.wait(cmd, done)
	select {
	case <-done:
		return errors.New("Xray exited during startup")
	case <-time.After(200 * time.Millisecond):
	}
	return nil
}

func (p *ProcessManager) wait(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	message := "exited successfully"
	if err != nil {
		message = fmt.Sprintf("process exited with an error: %v", err)
	}
	close(done)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == cmd {
		p.lastExit = message
		p.cmd, p.done = nil, nil
	}
}

func (p *ProcessManager) Stop() error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil || done == nil {
		return nil
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return errors.New("Xray did not stop")
		}
	}
	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd, p.done = nil, nil
	}
	p.mu.Unlock()
	return nil
}

func (p *ProcessManager) Restart() error {
	if err := p.Stop(); err != nil {
		return err
	}
	return p.Start()
}

func (p *ProcessManager) Status() ProcessStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.runningLocked() {
		return ProcessStatus{StartedAt: p.startedAt, LastExit: p.lastExit}
	}
	return ProcessStatus{Running: true, PID: p.cmd.Process.Pid, StartedAt: p.startedAt, LastExit: p.lastExit}
}

func (p *ProcessManager) runningLocked() bool {
	if p.cmd == nil || p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *ProcessManager) setAssetDir(value string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.assetDir
	p.assetDir = value
	return previous
}

func xrayEnvironment(assetDir string) []string {
	if assetDir == "" {
		return nil // inherit the service environment
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "XRAY_LOCATION_ASSET=") {
			env = append(env, item)
		}
	}
	return append(env, "XRAY_LOCATION_ASSET="+assetDir)
}

type Controller struct {
	path    string
	process *ProcessManager
}

func NewController(binary, configPath string) *Controller {
	return &Controller{path: configPath, process: NewProcessManager(binary, configPath)}
}

func (c *Controller) Apply(ctx context.Context, content []byte) error {
	return c.ApplyWithAssets(ctx, content, "")
}

func (c *Controller) ApplyWithAssets(ctx context.Context, content []byte, assetDir string) error {
	if !json.Valid(content) {
		return errors.New("generated Xray configuration is invalid JSON")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create Xray config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("secure Xray config directory permissions: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(c.path), ".xray-candidate-*.json")
	if err != nil {
		return fmt.Errorf("create candidate Xray config: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := c.process.ValidateConfig(ctx, tempPath, assetDir); err != nil {
		return err
	}
	previous, readErr := os.ReadFile(c.path)
	hadPrevious := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read current Xray config: %w", readErr)
	}
	if hadPrevious {
		if err := storage.AtomicWrite(c.path+".previous", previous, 0o600); err != nil {
			return fmt.Errorf("save previous Xray config: %w", err)
		}
	}
	wasRunning := c.process.Status().Running
	if wasRunning {
		if err := c.process.Stop(); err != nil {
			return fmt.Errorf("stop current Xray: %w", err)
		}
	}
	previousAssetDir := c.process.setAssetDir(assetDir)
	if err := storage.AtomicWrite(c.path, content, 0o600); err != nil {
		c.process.setAssetDir(previousAssetDir)
		if wasRunning {
			_ = c.process.Start()
		}
		return fmt.Errorf("install Xray config: %w", err)
	}
	if err := c.process.Start(); err != nil {
		if hadPrevious {
			_ = storage.AtomicWrite(c.path, previous, 0o600)
		} else {
			_ = os.Remove(c.path)
		}
		c.process.setAssetDir(previousAssetDir)
		if wasRunning && hadPrevious {
			_ = c.process.Start()
		}
		return fmt.Errorf("new Xray config could not be started; previous config restored: %w", err)
	}
	return nil
}

func (c *Controller) Start() error          { return c.process.Start() }
func (c *Controller) Stop() error           { return c.process.Stop() }
func (c *Controller) Restart() error        { return c.process.Restart() }
func (c *Controller) Status() ProcessStatus { return c.process.Status() }
