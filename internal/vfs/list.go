package vfs

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// Container is a running container the user can point a session at.
type Container struct {
	ID      string
	Name    string
	Image   string
	Workdir string
}

// Containers lists running containers. A missing or stopped Docker daemon is
// not an error worth surfacing loudly: it just means there is nothing to offer
// beyond the host.
func Containers(ctx context.Context) []Container {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "ps",
		"--format", "{{json .}}", "--no-trunc").Output()
	if err != nil {
		return nil
	}

	var list []Container
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row struct {
			ID    string `json:"ID"`
			Names string `json:"Names"`
			Image string `json:"Image"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		name := row.Names
		if i := strings.IndexByte(name, ','); i > 0 {
			name = name[:i]
		}
		if name == "" {
			name = shortID(row.ID)
		}
		list = append(list, Container{
			ID: row.ID, Name: name, Image: row.Image,
			Workdir: workdirOf(ctx, name),
		})
	}
	return list
}

func workdirOf(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"-f", "{{.Config.WorkingDir}}", name).Output()
	if err != nil {
		return "/"
	}
	if w := strings.TrimSpace(string(out)); w != "" {
		return w
	}
	return "/"
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Open resolves a persisted filesystem id back into an FS. An id naming a
// container that is gone falls back to the host rather than failing the
// session outright.
func Open(ctx context.Context, id, hostRoot string) FS {
	if !strings.HasPrefix(id, "docker:") {
		return NewLocal(hostRoot)
	}
	name := strings.TrimPrefix(id, "docker:")
	for _, c := range Containers(ctx) {
		if c.Name == name || c.ID == name || shortID(c.ID) == name {
			return NewDocker(c.Name, c.Image, c.Workdir)
		}
	}
	return NewLocal(hostRoot)
}
