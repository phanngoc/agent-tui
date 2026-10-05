package memory

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Scene is one L2 block: a narrative that consolidates the records of one
// situation — a workflow, an area of the code, a recurring kind of task.
type Scene struct {
	File    string    `json:"file"`
	Summary string    `json:"summary"`
	Heat    int       `json:"heat"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Body    string    `json:"body"`
}

const (
	metaStart = "-----META-START-----"
	metaEnd   = "-----META-END-----"
)

var sceneFileRE = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

// SceneFile normalises a name into a file name.
func SceneFile(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".md")
	name = strings.Trim(sceneFileRE.ReplaceAllString(name, "-"), "-")
	if name == "" {
		name = "scene"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name + ".md"
}

func (s *Store) sceneDir() string { return filepath.Join(s.Dir, "scenes") }

// Scenes lists the scene blocks, hottest first.
func (s *Store) Scenes() []Scene {
	files, _ := filepath.Glob(filepath.Join(s.sceneDir(), "*.md"))
	var out []Scene
	for _, f := range files {
		if sc, err := s.Scene(filepath.Base(f)); err == nil {
			out = append(out, sc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Heat != out[j].Heat {
			return out[i].Heat > out[j].Heat
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

// Scene reads one block.
func (s *Store) Scene(file string) (Scene, error) {
	if file != filepath.Base(file) || !strings.HasSuffix(file, ".md") {
		return Scene{}, errors.New("not a scene file: " + file)
	}
	b, err := os.ReadFile(filepath.Join(s.sceneDir(), file))
	if err != nil {
		return Scene{}, err
	}
	sc := Scene{File: file}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if i := strings.Index(text, metaStart); i >= 0 {
		if j := strings.Index(text, metaEnd); j > i {
			for _, line := range strings.Split(text[i+len(metaStart):j], "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				v = strings.TrimSpace(v)
				switch strings.TrimSpace(k) {
				case "summary":
					sc.Summary = v
				case "heat":
					sc.Heat, _ = strconv.Atoi(v)
				case "created":
					sc.Created, _ = time.Parse(time.RFC3339, v)
				case "updated":
					sc.Updated, _ = time.Parse(time.RFC3339, v)
				}
			}
			text = text[j+len(metaEnd):]
		}
	}
	sc.Body = strings.TrimSpace(text)
	return sc, nil
}

// PutScene writes a block, keeping its creation time if it existed.
func (s *Store) PutScene(sc Scene) (Scene, error) {
	sc.File = SceneFile(sc.File)
	now := time.Now().UTC().Truncate(time.Second)
	if old, err := s.Scene(sc.File); err == nil && !old.Created.IsZero() {
		sc.Created = old.Created
	}
	if sc.Created.IsZero() {
		sc.Created = now
	}
	sc.Updated = now
	if sc.Heat < 1 {
		sc.Heat = 1
	}
	var b strings.Builder
	b.WriteString(metaStart + "\n")
	b.WriteString("created: " + sc.Created.Format(time.RFC3339) + "\n")
	b.WriteString("updated: " + sc.Updated.Format(time.RFC3339) + "\n")
	b.WriteString("summary: " + strings.Join(strings.Fields(sc.Summary), " ") + "\n")
	b.WriteString("heat: " + strconv.Itoa(sc.Heat) + "\n")
	b.WriteString(metaEnd + "\n")
	b.WriteString(strings.TrimSpace(sc.Body) + "\n")
	if err := os.MkdirAll(s.sceneDir(), 0o755); err != nil {
		return sc, err
	}
	return sc, os.WriteFile(filepath.Join(s.sceneDir(), sc.File), []byte(b.String()), 0o600)
}

// DeleteScene removes a block.
func (s *Store) DeleteScene(file string) error {
	if file != filepath.Base(file) || !strings.HasSuffix(file, ".md") {
		return errors.New("not a scene file: " + file)
	}
	return os.Remove(filepath.Join(s.sceneDir(), file))
}

// Persona returns the L3 persona, empty when there is none yet.
func (s *Store) Persona() string {
	b, err := os.ReadFile(filepath.Join(s.Dir, "persona.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetPersona replaces it; the previous one is kept as persona.prev.md.
func (s *Store) SetPersona(text string) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(s.Dir, "persona.md")
	if old, err := os.ReadFile(p); err == nil && len(old) > 0 {
		_ = os.WriteFile(filepath.Join(s.Dir, "persona.prev.md"), old, 0o600)
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(text)+"\n"), 0o600)
}

// PersonaPrev returns the version SetPersona replaced last, for comparison.
func (s *Store) PersonaPrev() string {
	b, err := os.ReadFile(filepath.Join(s.Dir, "persona.prev.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
