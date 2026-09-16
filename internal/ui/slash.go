package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// Slash commands are the typed equivalent of the shortcuts. They exist because
// a key you have to remember is worth less than a name you can discover: Tab
// completes them, and an unknown one says so instead of being handed to the
// agent as a question.

type slashCmd struct {
	name string
	arg  string // usage hint for the help screen
	desc string
	run  func(m *Model, arg string) tea.Cmd
}

var slashCmds = []slashCmd{
	{"new", "", "start an empty session", func(m *Model, _ string) tea.Cmd {
		s := m.mgr.New()
		s.Engine = m.lastEngine
		cmd := m.onSessionSwitch()
		m.notice = "new session"
		return cmd
	}},
	{"fork", "", "branch this session, keeping the agent's context", func(m *Model, _ string) tea.Cmd {
		return m.forkSession(m.mgr.Active())
	}},
	{"close", "", "close this session", func(m *Model, _ string) tea.Cmd {
		m.mgr.Close(m.mgr.ActiveIndex())
		cmd := m.onSessionSwitch()
		m.notice = "session closed"
		return cmd
	}},
	{"cd", "<dir>", "move this session to another directory", func(m *Model, arg string) tea.Cmd {
		if arg == "" {
			arg = "~"
		}
		return m.changeDir(arg)
	}},
	{"mode", "[plan|ask|auto|full]", "how much the agent may do without asking",
		func(m *Model, arg string) tea.Cmd {
			if arg == "" {
				m.setMode(sessionMode(m.mgr.Active()).Next())
				return nil
			}
			m.setMode(agent.ParseMode(arg))
			return nil
		}},
	{"engine", "[name]", "choose the agent: api, claude, codex, opencode", func(m *Model, arg string) tea.Cmd {
		if arg == "" {
			m.overlay = overlayEngine
			m.engineSel = m.engineIndex(m.mgr.Active().Engine)
			return nil
		}
		return m.setEngineByName(arg)
	}},
	{"model", "[name]", "choose the model this session runs on", func(m *Model, arg string) tea.Cmd {
		if arg == "" {
			m.openModelPicker()
			return nil
		}
		return m.setModelByName(arg)
	}},
	{"target", "[host|container|distro]", "work on the host, in a container, or in WSL", func(m *Model, arg string) tea.Cmd {
		m.refreshTargets()
		if arg == "" {
			m.overlay = overlayTarget
			return nil
		}
		return m.setTargetByName(arg)
	}},
	{"files", "", "fuzzy-find a file", func(m *Model, _ string) tea.Cmd {
		m.overlay = overlayFinder
		m.finderIn.SetValue("")
		m.finderIn.Focus()
		m.refreshFinder()
		return nil
	}},
	{"search", "[text]", "search file contents", func(m *Model, arg string) tea.Cmd {
		m.overlay = overlayGrep
		m.grepIn.SetValue(arg)
		m.grepIn.Focus()
		if strings.TrimSpace(arg) != "" {
			m.grepBusy = true
			return m.runGrep(arg)
		}
		return nil
	}},
	{"tasks", "", "background commands, and their output", func(m *Model, _ string) tea.Cmd {
		m.overlay = overlayTasks
		m.taskSel, m.taskOpen = 0, ""
		return nil
	}},
	{"help", "", "show every shortcut", func(m *Model, _ string) tea.Cmd {
		m.overlay = overlayHelp
		return nil
	}},
	{"quit", "", "leave agent-tui", func(m *Model, _ string) tea.Cmd {
		return tea.Quit
	}},
}

// slashNames lists the commands as they are typed, for completion and help.
func slashNames() []string {
	out := make([]string, len(slashCmds))
	for i, c := range slashCmds {
		out[i] = "/" + c.name
	}
	return out
}

// parseSlash splits a command line. ok is false for anything that is not a
// single leading slash followed by a name, so a message that merely mentions a
// path still reaches the agent.
func parseSlash(text string) (name, arg string, ok bool) {
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return "", "", false
	}
	body := strings.TrimPrefix(text, "/")
	if body == "" || strings.ContainsAny(body, "\n") {
		return "", "", false
	}
	name, arg, _ = strings.Cut(body, " ")
	if name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return strings.ToLower(name), strings.TrimSpace(arg), true
}

// runSlash executes a command, or explains that there is no such thing.
func (m *Model) runSlash(name, arg string) tea.Cmd {
	for _, c := range slashCmds {
		if c.name == name {
			return c.run(m, arg)
		}
	}
	m.notice = "no command /" + name + " — try " + strings.Join(slashNames(), " ")
	return nil
}

// setEngineByName switches engine without opening the picker.
func (m *Model) setEngineByName(name string) tea.Cmd {
	name = strings.ToLower(name)
	for _, e := range m.reg.All() {
		if e.ID() != name {
			continue
		}
		if !e.Available() {
			m.notice = e.Label() + ": " + e.Detail()
			return nil
		}
		m.engineSel = m.engineIndex(e.ID())
		return m.engineKey("enter")
	}
	m.notice = "no engine called " + name
	return nil
}

// setTargetByName switches filesystem without opening the picker.
func (m *Model) setTargetByName(name string) tea.Cmd {
	name = strings.ToLower(name)
	for _, t := range m.targets {
		if strings.ToLower(t.label) == name || strings.ToLower(t.id) == name {
			return m.chooseTarget(t)
		}
	}
	if name == "wsl" {
		return m.enterWSL("")
	}
	m.notice = "no target called " + name
	return nil
}
