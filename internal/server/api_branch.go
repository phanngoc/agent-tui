package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/git"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// The branch under the composer: which one a conversation works on, and a
// switch to another. A conversation is asked about where it works — its
// worktree, in its distribution — and a project without one, about its root.

// treeOf is the working tree of a session, or of a project when session is
// empty.
func (s *Server) treeOf(session, root string) (vfs.FS, string, error) {
	if session != "" {
		sess, err := gateway.Load(session)
		if err != nil {
			return nil, "", err
		}
		if d, ok := strings.CutPrefix(sess.Target, "wsl:"); ok {
			dir := sess.CWD
			if dir == "" {
				if _, linux, ok := gateway.WSLPath(sess.Root); ok {
					dir = linux
				}
			}
			return vfs.NewWSL(d), dir, nil
		}
		if sess.Target != "" && sess.Target != "host" {
			return nil, "", errors.New("branches are shown for this machine and WSL only")
		}
		dir := sess.CWD
		if dir == "" {
			dir = sess.Root
		}
		return vfs.NewLocal(dir), dir, nil
	}
	p, err := filesOf(root)
	if err != nil {
		return nil, "", err
	}
	return p.fs, p.dir, nil
}

func (s *Server) branchRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/git/branches", func(w http.ResponseWriter, r *http.Request) {
		fsys, dir, err := s.treeOf(r.URL.Query().Get("session"), r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		b, err := git.ListBranches(ctx, fsys, dir)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, b)
	})
	m.HandleFunc("POST /api/git/switch", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Session string `json:"session"`
			Root    string `json:"root"`
			Branch  string `json:"branch"`
			Create  bool   `json:"create"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.Session != "" && s.Hub.Busy(in.Session) {
			fail(w, http.StatusConflict, errors.New("the agent is working in this tree: switch when its turn is over"))
			return
		}
		fsys, dir, err := s.treeOf(in.Session, in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if err := git.Switch(ctx, fsys, dir, in.Branch, in.Create); err != nil {
			fail(w, http.StatusConflict, err)
			return
		}
		b, err := git.ListBranches(ctx, fsys, dir)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, b)
	})
}

// worktreeFor makes the worktree a new conversation asked for, in the
// project's repository, and returns its directory as the project's
// filesystem names it.
func (s *Server) worktreeFor(ctx context.Context, root, branch, base string, create bool) (string, error) {
	p, err := filesOf(root)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return git.AddWorktree(ctx, p.fs, p.dir, branch, base, create)
}
