package injection

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type ydotoolBackend struct{}

func NewYdotoolBackend() Backend {
	return &ydotoolBackend{}
}

func (y *ydotoolBackend) Name() string {
	return "ydotool"
}

func (y *ydotoolBackend) Available() error {
	if _, err := exec.LookPath("ydotool"); err != nil {
		return fmt.Errorf("ydotool not found: %w (install ydotool package)", err)
	}

	// Check if ydotoold is running by checking socket exists
	// Note: ydotoold uses SOCK_DGRAM, not SOCK_STREAM, so we can't dial it
	// Just verify the socket file exists - ydotool command will handle the rest
	socketPath := y.getSocketPath()
	if socketPath == "" {
		return fmt.Errorf("ydotoold socket not found - ensure ydotoold is running")
	}

	return nil
}

func (y *ydotoolBackend) getSocketPath() string {
	// Check YDOTOOL_SOCKET env var first
	if sock := os.Getenv("YDOTOOL_SOCKET"); sock != "" {
		if _, err := os.Stat(sock); err == nil {
			return sock
		}
	}

	// Check common locations
	paths := []string{
		"/run/user/" + fmt.Sprint(os.Getuid()) + "/.ydotool_socket",
		"/tmp/.ydotool_socket",
	}

	// Also check XDG_RUNTIME_DIR
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		paths = append([]string{filepath.Join(xdg, ".ydotool_socket")}, paths...)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

func (y *ydotoolBackend) Inject(ctx context.Context, text string, timeout time.Duration, windowAddress string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := y.Available(); err != nil {
		return err
	}

	// ydotool type -- "text"
	cmd := exec.CommandContext(ctx, "ydotool", "type", "--", text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ydotool failed: %w", err)
	}

	return nil
}
