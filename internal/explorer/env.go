// Package explorer is the project file tree: a lazily expanded view of the
// directory the active session's agent is actually working in, kept current as
// files change underneath it.
package explorer

import (
	"os"
	"runtime"
	"strings"
)

// Env describes where the tree is being read from. It exists because the
// answer changes how the tree stays current: bind mounts and WSL's drvfs do not
// deliver filesystem events, so on those the poll is not a safety net, it is
// the only thing that works.
type Env struct {
	Kind          string // "", "docker", "wsl"
	WatchReliable bool
	Note          string
}

// Label is a short badge for the pane title.
func (e Env) Label() string {
	switch e.Kind {
	case "docker":
		return "docker"
	case "wsl":
		return "wsl"
	}
	return ""
}

// DetectEnv inspects the machine and the path. It is deliberately cheap: a
// couple of stats and one small file read at startup.
func DetectEnv(root string) Env {
	e := Env{WatchReliable: true}

	if inDocker() {
		e.Kind = "docker"
		// A bind mount from a macOS or Windows host does not forward inotify
		// into the container, and we cannot tell from inside which it is.
		e.WatchReliable = false
		e.Note = "bind mounts do not deliver file events; polling instead"
		return e
	}

	if distro, ok := inWSL(); ok {
		e.Kind = "wsl"
		e.Note = distro
		// /mnt/<drive> is drvfs, which has no inotify. The Linux-side
		// filesystem is fine.
		if strings.HasPrefix(root, "/mnt/") {
			e.WatchReliable = false
			e.Note = "Windows drive via /mnt has no file events; polling instead"
		}
		return e
	}
	return e
}

func inDocker() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd") ||
		strings.Contains(s, "kubepods")
}

func inWSL() (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}
	if d := os.Getenv("WSL_DISTRO_NAME"); d != "" {
		return d, true
	}
	b, err := os.ReadFile("/proc/version")
	if err != nil {
		return "", false
	}
	v := strings.ToLower(string(b))
	if strings.Contains(v, "microsoft") || strings.Contains(v, "wsl") {
		return "wsl", true
	}
	return "", false
}
