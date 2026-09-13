// Command agent-tui is a terminal coding agent: a multi-session transcript, a
// fuzzy file finder, project-wide search, and a syntax-highlighted preview
// pane, all driven from the keyboard.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/preview"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/theme"
	"github.com/phanngoc/agent-tui/internal/ui"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	var (
		root        = flag.String("C", ".", "project root directory")
		model       = flag.String("model", cfg.Model, "Claude model id")
		effort      = flag.String("effort", cfg.Effort, "reasoning effort: low|medium|high|xhigh|max")
		engineID    = flag.String("engine", cfg.Engine, "engine: api|claude|codex|opencode")
		modeFlag    = flag.String("mode", cfg.Mode, "mode for new sessions: plan|ask|auto|full")
		autoApprove = flag.Bool("yes", false, "run write and bash tools without asking")
		showVersion = flag.Bool("version", false, "print the version and exit")
		brokerSock  = flag.String("permission-broker", "", "internal: serve approval requests on this socket")
	)
	flag.Parse()

	// Internal mode: the external CLI spawned us to answer its permission
	// prompts. No UI, no index, just the MCP bridge back to the running app.
	if *brokerSock != "" {
		return engine.RunBroker(*brokerSock)
	}

	if *showVersion {
		fmt.Println("agent-tui", version())
		return nil
	}

	abs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if st, serr := os.Stat(abs); serr != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", *root)
	}
	cfg.Root, cfg.Model, cfg.Effort = abs, *model, *effort
	cfg.AutoApprove = cfg.AutoApprove || *autoApprove

	mode := agent.ParseMode(*modeFlag)
	if cfg.AutoApprove {
		mode = agent.ModeFull
	}

	styles := theme.New(theme.Dark)
	scheme := highlight.NewScheme(
		styles.P.Fg, styles.P.Keyword, styles.P.Type, styles.P.String,
		styles.P.Number, styles.P.Comment, styles.P.Func, styles.P.Punct,
	)

	hostFS := vfs.NewLocal(abs)
	idx := fsx.NewIndex(hostFS, abs, cfg.IndexLimit)
	loader := preview.NewLoader(scheme, cfg.MaxFileKB, 24)

	mgr := session.NewManager(config.DataDir(), abs, cfg.Model)
	mgr.Restore(20)

	tasks := task.NewRegistry()
	exec := &agent.Executor{
		FS:       hostFS,
		Tasks:    tasks,
		Root:     abs,
		Index:    idx,
		MaxBytes: int64(cfg.MaxFileKB) << 10,
		Workers:  cfg.Workers,
	}
	ag := agent.New(os.Getenv("ANTHROPIC_API_KEY"), exec, cfg.Model, cfg.Effort, cfg.MaxTokens)
	reg := engine.NewRegistry(engine.NewAPI(ag, cfg.Model), abs)

	mgr.Active().Mode = mode.String()

	if *engineID != "" {
		if !reg.Has(*engineID) {
			return fmt.Errorf("engine %q is not available; usable now: %s",
				*engineID, strings.Join(reg.AvailableIDs(), ", "))
		}
		mgr.Active().Engine = *engineID
	}

	if err := checkTerminal(); err != nil {
		return err
	}

	m := ui.New(cfg, styles, idx, loader, mgr, reg, tasks)
	p := tea.NewProgram(m)

	_, err = p.Run()
	mgr.SaveAll()
	mgr.Shutdown()
	return err
}

// checkTerminal fails early with something a user can act on. Bubble Tea's own
// error for a missing controlling terminal ("could not open TTY: open /dev/tty:
// device not configured") says nothing about why, and the usual cause is being
// launched from a pipe, a CI job, or an editor's embedded shell.
func checkTerminal() error {
	if term.IsTerminal(os.Stdout.Fd()) && term.IsTerminal(os.Stdin.Fd()) {
		return nil
	}
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		f.Close()
		return nil
	}
	return errors.New("no terminal attached: agent-tui is a full-screen app and " +
		"needs to run directly in a terminal, not through a pipe, a CI job, or an " +
		"embedded shell")
}

// version reports the module version stamped in by the Go toolchain, falling
// back to the VCS revision for local builds.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return "dev"
}
