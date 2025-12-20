# Injection Backend Improvements

## Problem

The current fallback chain (`ydotool → wtype → clipboard`) uses timeout-based failure detection to select backends. This causes text repetition when a backend partially succeeds before timing out.

## Proposed Solution: Proactive Backend Selection

Replace reactive timeout-based fallback with proactive selection based on:
1. Active window type detection
2. Text length threshold

### Tasks

- [ ] Add Hyprland integration to detect active window class
  - Use `hyprctl activewindow -j` to get window info
  - Parse JSON response for `class` field
  - Handle cases where hyprctl is unavailable (fall back to default backend)

- [ ] Add configurable electron app patterns
  ```toml
  [injection]
    electron_apps = ["code", "Code", "discord", "Discord", "slack", "Slack", "obsidian"]
  ```

- [ ] Add text length threshold config
  ```toml
  [injection]
    clipboard_threshold = 200  # characters - use clipboard for longer text
  ```

- [ ] Implement `selectBackend()` function
  - Check text length against threshold → clipboard if exceeded
  - Check window class against electron patterns → wtype for electron
  - Default to ydotool for native apps

- [ ] Refactor `Inject()` to use single-backend selection
  - Remove try-each-until-success loop
  - Select backend once, attempt once
  - Only fall back on genuine errors (tool not installed, daemon not running)

- [ ] Update config validation and defaults

- [ ] Add tests for window detection and backend selection logic

### Notes

- Timeouts should remain as a safety net for genuine failures (daemon crash, compositor freeze) but set to 60s+ since they shouldn't trigger during normal operation
- The fallback chain can remain as a last resort, but the primary selection should be proactive
