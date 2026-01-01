package injection

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

type clipboardBackend struct{}

func NewClipboardBackend() Backend {
	return &clipboardBackend{}
}

func (c *clipboardBackend) Name() string {
	return "clipboard"
}

func (c *clipboardBackend) Available() error {
	if _, err := exec.LookPath("wl-copy"); err != nil {
		return fmt.Errorf("wl-copy not found: %w (install wl-clipboard)", err)
	}

	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("WAYLAND_DISPLAY not set - clipboard operations require Wayland session")
	}

	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		return fmt.Errorf("XDG_RUNTIME_DIR not set - clipboard operations require proper session environment")
	}

	return nil
}

func (c *clipboardBackend) Inject(ctx context.Context, text string, timeout time.Duration, windowAddress string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := c.Available(); err != nil {
		return err
	}

	// Copy text to clipboard
	cmd := exec.CommandContext(ctx, "wl-copy")
	cmd.Stdin = strings.NewReader(text)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wl-copy failed: %w", err)
	}

	// If window address is provided, focus the window and paste
	if windowAddress != "" {
		if err := c.focusWindow(ctx, windowAddress); err != nil {
			log.Printf("Clipboard: Failed to focus window %s: %v, continuing with clipboard copy only", windowAddress, err)
			// Don't fail the injection if focusing fails - clipboard copy succeeded
		} else {
			// Small delay to ensure window is focused before pasting
			time.Sleep(100 * time.Millisecond)
			if err := c.pasteFromClipboard(ctx); err != nil {
				log.Printf("Clipboard: Failed to paste: %v, text is still in clipboard", err)
				// Don't fail the injection if paste fails - clipboard copy succeeded
			} else {
				log.Printf("Clipboard: Successfully pasted to window %s", windowAddress)
			}
		}
	}

	return nil
}

// focusWindow focuses the specified window using hyprctl
func (c *clipboardBackend) focusWindow(ctx context.Context, windowAddress string) error {
	cmd := exec.CommandContext(ctx, "hyprctl", "dispatch", "focuswindow", windowAddress)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hyprctl focuswindow failed: %w", err)
	}
	return nil
}

// pasteFromClipboard simulates Ctrl+Shift+V to paste from clipboard
// Uses Ctrl+Shift+V which works in terminals (Ghostty, etc.) and most GUI apps
func (c *clipboardBackend) pasteFromClipboard(ctx context.Context) error {
	// Try wtype first (Wayland native)
	if wtypePath, err := exec.LookPath("wtype"); err == nil {
		// Use Ctrl+Shift+V - works in terminals and most GUI apps
		cmd := exec.CommandContext(ctx, wtypePath, "-M", "ctrl", "-M", "shift", "v", "-m", "shift", "-m", "ctrl")
		if err := cmd.Run(); err != nil {
			log.Printf("Clipboard: wtype paste failed: %v, trying ydotool", err)
		} else {
			return nil
		}
	}

	// Fallback to ydotool
	if _, err := exec.LookPath("ydotool"); err == nil {
		cmd := exec.CommandContext(ctx, "ydotool", "key", "ctrl+shift+v")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ydotool paste failed: %w", err)
		}
		return nil
	}

	return fmt.Errorf("neither wtype nor ydotool available for pasting")
}
