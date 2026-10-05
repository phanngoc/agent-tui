// Command agent-tui is a terminal coding agent: a multi-session transcript, a
// fuzzy file finder, project-wide search, and a syntax-highlighted preview
// pane, all driven from the keyboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/kit"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/preview"
	"github.com/phanngoc/agent-tui/internal/server"
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

	// `agent-tui serve` is the gateway: the web admin's server, and the hub
	// every terminal app connects to.
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		return serve(cfg, os.Args[2:])
	}
	// `agent-tui gateway …` starts, stops and shows the gateway on its own;
	// `agent-tui web` opens the admin, starting the gateway if it is not up.
	if len(os.Args) > 1 && os.Args[1] == "gateway" {
		return gatewayCmd(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "web" {
		path := ""
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		return openWeb(path)
	}
	// `agent-tui kit-mcp -root R` serves a project's skill and memory tools
	// over MCP on stdio, for an engine that runs as a separate CLI.
	if len(os.Args) > 1 && os.Args[1] == "kit-mcp" {
		fs := flag.NewFlagSet("kit-mcp", flag.ExitOnError)
		root := fs.String("root", "", "project root")
		_ = fs.Parse(os.Args[2:])
		return kit.ServeMCP(context.Background(), *root, os.Stdin, os.Stdout)
	}

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
	// Where to open is a setting only when nobody said: -C is a request for
	// this folder now, and a preference set weeks ago does not outrank it.
	var startNote string
	prefs := config.LoadPrefs()
	if !flagSet("C") {
		abs, startNote = prefs.StartRoot(abs)
	}
	// The settings page outranks config.json, and a flag outranks both: the
	// file is the default you wrote once, the page is what you chose since,
	// and a flag is what you asked for this time.
	// A project's own settings (.agent-tui/settings.json, written by the web
	// admin) sit between the two: more specific than the page, less than a flag.
	proj := config.LoadProjectSettings(abs)
	pick := func(name string, flagVal *string, project, global string) {
		if flagSet(name) {
			return
		}
		if project != "" {
			*flagVal = project
		} else if global != "" {
			*flagVal = global
		}
	}
	pick("model", model, proj.Model, prefs.Model)
	pick("mode", modeFlag, proj.Mode, prefs.Mode)
	pick("effort", effort, proj.Effort, prefs.Effort)
	if prefs.Theme != "" {
		cfg.Theme = prefs.Theme
	}
	cfg.Root, cfg.Model, cfg.Effort = abs, *model, *effort
	cfg.AutoApprove = cfg.AutoApprove || *autoApprove

	mode := agent.ParseMode(*modeFlag)
	if cfg.AutoApprove {
		mode = agent.ModeFull
	}

	// A theme is taste, and a theme file that cannot be read is a fault the
	// reader will blame on this program rather than on their file — so a bad
	// one is refused out loud and the default is used, rather than shipping a
	// UI whose comment colour has vanished into the background.
	palette, themeErr := theme.Resolve(cfg.Theme)
	styles := theme.New(palette)
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

	// An engine chosen on the settings page that this machine cannot run now
	// is passed over rather than refused: unlike a flag, nobody asked for it
	// today, and failing to start over it would be the wrong way round.
	if !flagSet("engine") {
		for _, want := range []string{proj.Engine, prefs.Engine} {
			if want != "" && reg.Has(want) {
				*engineID = want
				break
			}
		}
	}
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
	// Join the gateway, starting one if there is none, so the web admin sees
	// this terminal's sessions live and can drive them.
	gw := gateway.Join("tui", abs, prefs.GatewayAutostart == nil || *prefs.GatewayAutostart)
	defer gw.Close()
	m.SetGateway(gw)
	m.SetLearner(learn.Default())
	m.UseKit()
	if startNote != "" {
		m.Notice(startNote)
	}
	if themeErr != nil {
		m.Notice("theme: " + themeErr.Error())
	}
	p := tea.NewProgram(m)

	_, err = p.Run()
	mgr.SaveAll()
	mgr.Shutdown()
	config.RememberRoot(abs)
	return err
}

// serve runs the gateway until interrupted.
func serve(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", gateway.ListenAddr(), "address to listen on (loopback only; or AGENT_TUI_GATEWAY_ADDR)")
	web := fs.String("web", os.Getenv("AGENT_TUI_WEB"), "folder of a static build of the admin to serve at / (or AGENT_TUI_WEB)")
	_ = fs.Parse(args)

	// One gateway per data folder: a second would overwrite the first's
	// discovery file and split the terminals between them. Another folder's
	// gateway (another XDG_DATA_HOME) is not this one's business.
	if i, ok := gateway.ReadInfo(); ok && gateway.Alive(i.Addr) {
		return fmt.Errorf("a gateway is already running on http://%s", i.Addr)
	}
	gateway.ClearStopped()
	prefs := config.LoadPrefs()
	if prefs.Model != "" {
		cfg.Model = prefs.Model
	}
	if prefs.Mode != "" {
		cfg.Mode = prefs.Mode
	}
	if prefs.Effort != "" {
		cfg.Effort = prefs.Effort
	}
	if *web == "" {
		// A build next to the binary, or in the repository it came from.
		for _, cand := range webCandidates() {
			if st, err := os.Stat(filepath.Join(cand, "index.html")); err == nil && !st.IsDir() {
				*web = cand
				break
			}
		}
	}
	if *web != "" {
		// A gateway restarting itself from the web serves the same pages.
		_ = os.Setenv("AGENT_TUI_WEB", *web)
	}
	srv := server.New(cfg, version(), *web)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		srv.Stop()
	}()
	return srv.ListenAndServe(*addr)
}

func webCandidates() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), "admin"))
	}
	if wd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(wd, "web", "admin", "out"))
	}
	return out
}

// flagSet reports whether a flag was given on the command line, as opposed to
// holding its default.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
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
