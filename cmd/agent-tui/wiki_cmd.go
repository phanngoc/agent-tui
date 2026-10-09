package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/wiki"
)

// `agent-tui wiki …` builds and reads a project's knowledge wiki from a shell.

const wikiUsage = `usage: agent-tui wiki [-C dir] [command]

  status              pages, documents, and what waits to be ingested (default)
  add PATH...         keep documents for the wiki: files, or folders of .md/.txt
  rm NAME...          drop documents; the next ingest removes their pages
  ingest [-model M]   read new and changed documents into pages
  search [-hops N] Q  search the pages, as the agent does
  show REF            print a page, "index", "overview" or "log"
  lint                links to nowhere, orphans, documents that failed
  dir                 print where the wiki is kept
`

// docExts are the files a folder given to add contributes.
var docExts = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".rst": true, ".adoc": true, ".org": true}

func wikiCmd(args []string) error {
	fs0 := flag.NewFlagSet("wiki", flag.ContinueOnError)
	root := fs0.String("C", ".", "project root directory")
	fs0.Usage = func() { fmt.Fprint(os.Stderr, wikiUsage) }
	if err := fs0.Parse(args); err != nil {
		return nil
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	w := wiki.For(abs)
	args = fs0.Args()
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "status":
		s := w.Stats()
		fmt.Printf("wiki of %s\n  %s\n", abs, s.Dir)
		var types []string
		for t, n := range s.ByType {
			types = append(types, fmt.Sprintf("%s %d", t, n))
		}
		sort.Strings(types)
		fmt.Printf("pages: %d (%s)\ndocuments: %d, %d to ingest, %d failed\n", s.Pages, strings.Join(types, ", "), s.Raw, s.Pending, s.Failed)
		if !s.Ingested.IsZero() {
			fmt.Printf("last ingest: %s (version %d)\n", s.Ingested.Local().Format("2006-01-02 15:04"), s.Version)
		}
		if w.Busy() {
			fmt.Println("an ingest is running now")
		}
		if s.Pending > 0 {
			fmt.Println("run `agent-tui wiki ingest` to read the pending documents")
		}
		return nil
	case "dir":
		fmt.Println(w.Dir)
		return nil
	case "add":
		if len(args) == 0 {
			return fmt.Errorf("add what? agent-tui wiki add docs/ spec.md")
		}
		n := 0
		for _, a := range args {
			added, err := addDocs(w, a)
			n += added
			if err != nil {
				fmt.Fprintln(os.Stderr, "  ", err)
			}
		}
		fmt.Printf("%d documents kept; %d wait to be ingested\n", n, len(w.Pending()))
		return nil
	case "rm":
		for _, a := range args {
			if err := w.RemoveRaw(a); err != nil {
				fmt.Fprintln(os.Stderr, "  ", err)
				continue
			}
			fmt.Println("removed", a)
		}
		return nil
	case "ingest":
		f := flag.NewFlagSet("wiki ingest", flag.ExitOnError)
		model := f.String("model", config.LoadPrefs().LearnModel, "the model that reads the documents")
		asJSON := f.Bool("json", false, "print the report as JSON")
		_ = f.Parse(args)
		llm, err := learn.NewLLM(*model)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		fmt.Fprintf(os.Stderr, "ingesting with %s\n", llm.Name())
		start := time.Now()
		rep, err := w.Ingest(ctx, llm, func(s string) {
			fmt.Fprintf(os.Stderr, "  %5s  %s\n", time.Since(start).Round(time.Second), s)
		})
		if *asJSON {
			b, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Printf("read %d, unchanged %d, removed %d · wrote %d pages, took out %d · %s\n",
				len(rep.Ingested), rep.Skipped, len(rep.Deleted), len(rep.Written), len(rep.Removed), rep.Took)
			for what, why := range rep.Failed {
				fmt.Printf("  failed %s: %s\n", what, why)
			}
		}
		return err
	case "search":
		f := flag.NewFlagSet("wiki search", flag.ExitOnError)
		hops := f.Int("hops", 0, "follow links this far from the matches")
		limit := f.Int("n", 10, "most results")
		_ = f.Parse(args)
		q := strings.Join(f.Args(), " ")
		if q == "" {
			return fmt.Errorf("search for what?")
		}
		for _, r := range w.Search(wiki.Query{Text: q, Hops: *hops, Limit: *limit}) {
			via := ""
			if r.Hop > 0 {
				via = fmt.Sprintf("  ← %s", r.Via)
			}
			fmt.Printf("%.2f  %-40s %s%s\n      %s\n", r.Score, r.Title, r.ID, via, r.Snippet)
		}
		return nil
	case "show":
		if len(args) == 0 {
			return fmt.Errorf("show what? a page title or id, index, overview or log")
		}
		ref := strings.Join(args, " ")
		switch strings.ToLower(ref) {
		case "index":
			fmt.Println(w.Index())
		case "overview":
			fmt.Println(w.Overview())
		case "log":
			fmt.Println(w.Log())
		default:
			g := wiki.NewGraph(w.Pages())
			id, ok := g.Resolve(ref)
			if !ok {
				return fmt.Errorf("no page %q", ref)
			}
			fmt.Print(g.Pages[id].String())
		}
		return nil
	case "lint":
		l := w.Lint()
		for id, links := range l.Broken {
			fmt.Printf("broken  %s → %s\n", id, strings.Join(links, ", "))
		}
		for _, id := range l.Orphans {
			fmt.Printf("orphan  %s\n", id)
		}
		for _, id := range l.NoSrc {
			fmt.Printf("no source  %s\n", id)
		}
		for name, why := range l.Failed {
			fmt.Printf("failed  %s: %s\n", name, why)
		}
		return nil
	case "help", "-h", "--help":
		fmt.Print(wikiUsage)
		return nil
	}
	return fmt.Errorf("no wiki command %q\n%s", sub, wikiUsage)
}

// addDocs keeps a file, or a folder's documents under the folder's name.
func addDocs(w *wiki.Wiki, arg string) (int, error) {
	st, err := os.Stat(arg)
	if err != nil {
		return 0, err
	}
	if !st.IsDir() {
		b, err := os.ReadFile(arg)
		if err != nil {
			return 0, err
		}
		name, err := w.AddRaw(filepath.Base(arg), b)
		if err != nil {
			return 0, err
		}
		fmt.Println("  +", name)
		return 1, nil
	}
	abs, _ := filepath.Abs(arg)
	base := filepath.Dir(abs)
	n := 0
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != abs && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !docExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		name, err := w.AddRaw(rel, b)
		if err != nil {
			fmt.Fprintln(os.Stderr, "  skipped", rel+":", err)
			return nil
		}
		fmt.Println("  +", name)
		n++
		return nil
	})
	return n, err
}
