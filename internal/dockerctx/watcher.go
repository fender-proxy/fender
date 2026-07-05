package dockerctx

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// OnChangeFunc is called when the resolved Docker upstream socket changes.
// socket is the new socket path; source is a human-readable description of
// where it was resolved from.
type OnChangeFunc func(socket, source string)

// Watcher monitors Docker context configuration files using inotify/kqueue
// and calls fn whenever the active Docker upstream socket changes.
type Watcher struct {
	fn      OnChangeFunc
	fw      *fsnotify.Watcher
	mu      sync.Mutex
	timer   *time.Timer
	current string // last known socket path, used to suppress no-op changes
}

// NewWatcher creates a Watcher that calls fn when the active Docker context
// changes. Call Start() in a goroutine to begin watching, and Stop() to shut
// it down.
func NewWatcher(initialSocket string, fn OnChangeFunc) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{fn: fn, fw: fw, current: initialSocket}

	// Watch directories rather than individual files so we detect atomic
	// writes (temp-file rename) that many tools use for config updates.
	for _, dir := range WatchPaths() {
		if err := fw.Add(dir); err != nil {
			// Non-fatal: directory may not exist (e.g. no contexts configured yet)
			slog.Debug("fender: cannot watch directory", "path", dir, "err", err)
		}
	}

	return w, nil
}

// Start begins the watch loop. It blocks until Stop is called.
func (w *Watcher) Start() {
	for {
		select {
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			// Only react to changes in config.json or context meta.json.
			base := filepath.Base(event.Name)
			if base != "config.json" && base != "meta.json" {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				slog.Debug("docker config change detected", "file", event.Name)
				w.scheduleResolve()
			}

		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("docker context watcher error", "err", err)
		}
	}
}

// Stop shuts down the watcher.
func (w *Watcher) Stop() {
	w.fw.Close()
}

// scheduleResolve debounces rapid filesystem events (e.g. editor temp-file
// swaps) and performs a single resolution after the burst settles.
func (w *Watcher) scheduleResolve() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(150*time.Millisecond, func() {
		w.mu.Lock()
		w.timer = nil
		w.mu.Unlock()

		socket, source, err := Resolve()
		if err != nil {
			slog.Warn("failed to resolve Docker socket after context change", "err", err)
			return
		}

		w.mu.Lock()
		unchanged := socket == w.current
		if !unchanged {
			w.current = socket
		}
		w.mu.Unlock()

		if unchanged {
			return // same socket — nothing to do
		}
		w.fn(socket, source)
	})
}
