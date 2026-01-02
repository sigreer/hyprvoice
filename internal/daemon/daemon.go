package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/leonardotrapani/hyprvoice/internal/bus"
	"github.com/leonardotrapani/hyprvoice/internal/config"
	"github.com/leonardotrapani/hyprvoice/internal/notify"
	"github.com/leonardotrapani/hyprvoice/internal/pipeline"
)

type Daemon struct {
	mu        sync.RWMutex
	notifier  notify.Notifier
	configMgr *config.Manager

	ctx    context.Context
	cancel context.CancelFunc

	pipeline pipeline.Pipeline

	wg sync.WaitGroup

	modeOverride string // Runtime mode override ("raw", "llm", or "" for config default)
}

func New() (*Daemon, error) {
	configMgr, err := config.NewManager()

	conf := configMgr.GetConfig()

	if err != nil {
		return nil, fmt.Errorf("failed to create config manager: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	n := notify.GetNotifierBasedOnConfig(conf)

	d := &Daemon{
		notifier:  n,
		configMgr: configMgr,
		ctx:       ctx,
		cancel:    cancel,
	}

	return d, nil
}

func (d *Daemon) onConfigReload() {
	log.Printf("Config reloaded, restarting pipeline")
	d.stopPipeline()

	d.notifier.Notify("Hyprvoice", "Config Reloaded")

	d.mu.Lock()
	d.notifier = notify.GetNotifierBasedOnConfig(d.configMgr.GetConfig())
	d.mu.Unlock()
}

func (d *Daemon) status() pipeline.Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.pipeline == nil {
		return pipeline.Idle
	}
	return d.pipeline.Status()
}

func (d *Daemon) stopPipeline() {
	d.mu.Lock()
	p := d.pipeline
	d.pipeline = nil
	d.mu.Unlock()

	if p != nil {
		p.Stop()
	}
}

func (d *Daemon) Run() error {
	if err := bus.CheckExistingDaemon(); err != nil {
		return err
	}

	d.configMgr.SetOnConfigReload(d.onConfigReload)

	ln, err := bus.Listen()
	if err != nil {
		return err
	}
	defer ln.Close()

	if err := bus.CreatePidFile(); err != nil {
		return fmt.Errorf("failed to create PID file: %w", err)
	}
	defer bus.RemovePidFile()

	if err := d.configMgr.StartWatching(d.ctx); err != nil {
		log.Printf("Warning: failed to start config file watching: %v", err)
	}
	defer d.configMgr.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, shutting down gracefully", sig)
		d.cancel()
	}()

	go func() {
		<-d.ctx.Done()
		if err := ln.Close(); err != nil {
			log.Printf("Error closing listener: %v", err)
		}
	}()

	log.Printf("Daemon started, listening on socket")

	for {
		c, err := ln.Accept()
		if err != nil {
			if d.ctx.Err() != nil {
				log.Printf("Shutdown requested, waiting for connections to finish")
				d.wg.Wait()
				return nil
			}
			log.Printf("Accept error: %v", err)
			return fmt.Errorf("accept failed: %w", err)
		}
		d.wg.Add(1)
		go d.handle(c)
	}
}

func (d *Daemon) handle(c net.Conn) {
	defer c.Close()
	defer d.wg.Done()

	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		log.Printf("Client read error: %v", err)
		fmt.Fprintf(c, "ERR read_error: %v\n", err)
		return
	}
	if len(line) == 0 {
		fmt.Fprint(c, "ERR empty\n")
		return
	}
	cmd := line[0]

	switch cmd {
	case 't':
		d.toggle()
		fmt.Fprint(c, "OK toggled\n")
	case 'c':
		d.cancelPipeline()
		fmt.Fprint(c, "OK cancelled\n")
	case 's':
		status := d.status()
		fmt.Fprintf(c, "STATUS status=%s\n", status)
	case 'v':
		fmt.Fprintf(c, "STATUS proto=%s\n", bus.ProtoVer)
	case 'q':
		fmt.Fprint(c, "OK quitting\n")
		d.cancel()
	case 'm':
		// Mode command - format: "m\n" (get) or "m:llm\n" (set)
		modeArg := strings.TrimSpace(line[1:])
		if modeArg == "" {
			// Get current mode
			mode := d.getEffectiveMode()
			fmt.Fprintf(c, "MODE mode=%s\n", mode)
		} else if strings.HasPrefix(modeArg, ":") {
			// Set mode
			newMode := strings.TrimPrefix(modeArg, ":")
			if newMode != "raw" && newMode != "llm" {
				fmt.Fprintf(c, "ERR invalid_mode=%s\n", newMode)
			} else {
				d.setModeOverride(newMode)
				log.Printf("Daemon: Processing mode changed to %s", newMode)
				fmt.Fprintf(c, "OK mode=%s\n", newMode)
			}
		} else {
			fmt.Fprintf(c, "ERR invalid_mode_command\n")
		}
	default:
		log.Printf("Unknown command: %c", cmd)
		fmt.Fprintf(c, "ERR unknown=%q\n", cmd)
	}
}

func (d *Daemon) toggle() {
	switch d.status() {
	case pipeline.Idle:
		config := d.getConfigWithModeOverride()
		
		// Capture active window when recording starts
		windowAddress := d.getActiveWindow()
		if windowAddress != "" {
			log.Printf("Daemon: Captured active window address: %s", windowAddress)
		} else {
			log.Printf("Daemon: Failed to capture active window, continuing without window tracking")
		}
		
		p := pipeline.New(config)
		if windowAddress != "" {
			p.SetWindowAddress(windowAddress)
		}
		p.Run(d.ctx)

		d.mu.Lock()
		d.pipeline = p
		d.mu.Unlock()

		go d.notifier.Notify("Hyprvoice", "Recording Started")
		go d.monitorPipelineErrors(p)

	case pipeline.Recording:
		d.stopPipeline()
		go d.notifier.Error("Recording Aborted")

	case pipeline.Transcribing:
		d.mu.RLock()
		if d.pipeline != nil {
			actionChan := d.pipeline.GetActionCh()
			log.Printf("Daemon: Sending inject action to pipeline")
			d.mu.RUnlock()
			actionChan <- pipeline.Inject
		} else {
			d.mu.RUnlock()
		}
		go d.notifier.Notify("Hyprvoice", "Recording Ended... Transcribing")

	case pipeline.Injecting:
		d.stopPipeline()
		go d.notifier.Error("Injection Aborted")
	}
}

func (d *Daemon) cancelPipeline() {
	switch d.status() {
	case pipeline.Idle:
		log.Printf("Daemon: Cancel requested but pipeline is idle, ignoring")
	default:
		d.stopPipeline()
		go d.notifier.Notify("Hyprvoice", "Operation Cancelled")
	}
}

func (d *Daemon) monitorPipelineErrors(p pipeline.Pipeline) {
	errorCh := p.GetErrorCh()
	for {
		select {
		case pipelineErr := <-errorCh:
			message := pipelineErr.Message

			if pipelineErr.Err != nil {
				message = fmt.Sprintf("%s: %v", message, pipelineErr.Err)
			}

			d.notifier.Error(message)
		case <-d.ctx.Done():
			return
		}
	}
}

// getActiveWindow retrieves the address of the currently active window using hyprctl
func (d *Daemon) getActiveWindow() string {
	cmd := exec.Command("hyprctl", "-j", "activewindow")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Daemon: Failed to get active window: %v", err)
		return ""
	}

	var window struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(output, &window); err != nil {
		log.Printf("Daemon: Failed to parse active window JSON: %v", err)
		return ""
	}

	return window.Address
}

// getEffectiveMode returns the current processing mode (runtime override or config default)
func (d *Daemon) getEffectiveMode() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.modeOverride != "" {
		return d.modeOverride
	}
	return d.configMgr.GetConfig().Processing.Mode
}

// setModeOverride sets a runtime mode override
func (d *Daemon) setModeOverride(mode string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.modeOverride = mode
}

// getConfigWithModeOverride returns a copy of the config with the mode override applied
func (d *Daemon) getConfigWithModeOverride() *config.Config {
	cfg := d.configMgr.GetConfig()

	d.mu.RLock()
	modeOverride := d.modeOverride
	d.mu.RUnlock()

	if modeOverride != "" {
		// Create a copy with the override applied
		cfgCopy := *cfg
		cfgCopy.Processing.Mode = modeOverride
		return &cfgCopy
	}
	return cfg
}
