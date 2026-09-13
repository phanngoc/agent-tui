package explorer

import (
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// watcher makes the tree react instantly where the filesystem cooperates.
//
// It is strictly an accelerator: the tree is kept correct by polling, because
// inotify does not cross a Docker bind mount and does not exist at all on WSL's
// /mnt drives. When events do arrive they simply cut the latency from the poll
// interval to nothing.
type watcher struct {
	mu      sync.Mutex
	fsw     *fsnotify.Watcher
	watched map[string]bool
	signal  chan struct{}
	closed  bool
}

// Watch starts a watcher. A failure to create one is not an error worth
// reporting: the poll still keeps the tree current, just less promptly.
func (t *Tree) Watch() {
	// Only a filesystem this process can see has events to listen for. A
	// container is polled, which is what the interval is tuned for.
	if f := t.FS(); f != nil && !f.IsLocal() {
		return
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	w := &watcher{
		fsw:     fsw,
		watched: map[string]bool{},
		signal:  make(chan struct{}, 1),
	}
	t.watch = w

	go func() {
		for {
			select {
			case _, ok := <-fsw.Events:
				if !ok {
					return
				}
				w.ping()
			case _, ok := <-fsw.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	w.sync(t.Root(), t.openDirs())
}

// Changes fires when the filesystem reported something. A caller that also
// polls can treat it as "look now" rather than as the source of truth.
func (t *Tree) Changes() <-chan struct{} {
	if t.watch == nil {
		return nil
	}
	return t.watch.signal
}

// Close releases the watcher.
func (t *Tree) Close() {
	if t.watch != nil {
		t.watch.close()
		t.watch = nil
	}
}

func (w *watcher) ping() {
	select {
	case w.signal <- struct{}{}:
	default: // a pending signal is as good as two
	}
}

// sync makes the watched set match the open directories. Watching only what is
// expanded keeps the descriptor count bounded on large trees.
func (w *watcher) sync(root string, open []string) {
	if w == nil || root == "" {
		return
	}
	want := make(map[string]bool, len(open))
	for _, rel := range open {
		p := root
		if rel != "" {
			p = filepath.Join(root, filepath.FromSlash(rel))
		}
		want[p] = true
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for p := range w.watched {
		if !want[p] {
			_ = w.fsw.Remove(p)
			delete(w.watched, p)
		}
	}
	for p := range want {
		if !w.watched[p] {
			if w.fsw.Add(p) == nil {
				w.watched[p] = true
			}
		}
	}
}

func (w *watcher) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for p := range w.watched {
		_ = w.fsw.Remove(p)
	}
	w.watched = map[string]bool{}
}

func (w *watcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	_ = w.fsw.Close()
}
