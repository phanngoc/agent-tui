package vfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Docker reads the filesystem inside a running container.
//
// Every operation is a `docker exec` of a shell script; the scripts themselves
// live in posixFS, which a WSL distribution drives the same way.
type Docker struct {
	posixFS
	container string // name or id
	image     string
	workdir   string
}

// NewDocker targets a running container. workdir is where new sessions start,
// falling back to / when the image declares none.
func NewDocker(container, image, workdir string) *Docker {
	if workdir == "" {
		workdir = "/"
	}
	d := &Docker{container: container, image: image, workdir: workdir}
	d.run = d
	return d
}

func (d *Docker) ID() string         { return "docker:" + d.container }
func (d *Docker) Label() string      { return d.container }
func (d *Docker) IsLocal() bool      { return false }
func (d *Docker) DefaultDir() string { return d.workdir }
func (d *Docker) Image() string      { return d.image }
func (d *Docker) Container() string  { return d.container }

// Health reports whether the container is still running, which is the failure
// everything else would otherwise surface as a confusing exec error.
func (d *Docker) Health(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"-f", "{{.State.Running}}", d.container).Output()
	if err != nil {
		return fmt.Errorf("container %s is not reachable", d.container)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("container %s is not running", d.container)
	}
	return nil
}

// sh runs a shell snippet inside the container and returns its stdout.
func (d *Docker) sh(ctx context.Context, dir, script string) ([]byte, error) {
	args := []string{"exec"}
	if dir != "" {
		args = append(args, "-w", dir)
	}
	args = append(args, d.container, "sh", "-c", script)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// shIn runs a script with data on its stdin.
func (d *Docker) shIn(ctx context.Context, script string, stdin io.Reader) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", d.container, "sh", "-c", script)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// Command runs a program inside the container, so an agent CLI executes next to
// the files it is editing.
func (d *Docker) Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	full := []string{"exec", "-i"}
	if dir != "" {
		full = append(full, "-w", dir)
	}
	full = append(full, d.container, name)
	full = append(full, args...)
	return exec.CommandContext(ctx, "docker", full...)
}
