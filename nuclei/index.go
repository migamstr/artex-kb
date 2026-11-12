// Package nuclei provides a local tag index over a nuclei-templates directory.
package nuclei

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultTemplatesDir = "/opt/nuclei-templates"

type templateInfo struct {
	ID   string `yaml:"id"`
	Info struct {
		Name     string      `yaml:"name"`
		Severity string      `yaml:"severity"`
		Tags     interface{} `yaml:"tags"`
	} `yaml:"info"`
}

// Entry is one indexed template.
type Entry struct {
	ID       string
	Path     string
	Severity string
	Tags     []string
}

// Index is a thread-safe in-memory tag index.
type Index struct {
	mu          sync.RWMutex
	dir         string
	byTag       map[string][]Entry
	all         []Entry
	builtAt     time.Time
	templateCnt int
}

func NewIndex(dir string) *Index {
	if dir == "" {
		dir = DefaultTemplatesDir
	}
	idx := &Index{dir: dir}
	idx.Rebuild()
	return idx
}

func (idx *Index) Dir() string { return idx.dir }

func (idx *Index) Stats() (int, int, time.Time) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.templateCnt, len(idx.byTag), idx.builtAt
}

func (idx *Index) Rebuild() {
	dir := idx.dir
	if _, err := os.Stat(dir); err != nil {
		log.Printf("[nuclei-index] templates dir %s not found, index empty", dir)
		idx.mu.Lock()
		idx.byTag = map[string][]Entry{}
		idx.all = nil
		idx.templateCnt = 0
		idx.builtAt = time.Now()
		idx.mu.Unlock()
		return
	}
	byTag := map[string][]Entry{}
	var all []Entry
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		e, ok := parseTemplate(path, dir)
		if !ok {
			return nil
		}
		all = append(all, e)
		for _, t := range e.Tags {
			byTag[t] = append(byTag[t], e)
		}
		return nil
	})
	idx.mu.Lock()
	idx.byTag = byTag
	idx.all = all
	idx.templateCnt = len(all)
	idx.builtAt = time.Now()
	idx.mu.Unlock()
	log.Printf("[nuclei-index] built: %d templates, %d tags (dir=%s)", len(all), len(byTag), dir)
}

func (idx *Index) Search(tags []string, severities []string) []Entry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	sevSet := map[string]bool{}
	for _, s := range severities {
		sevSet[strings.ToLower(s)] = true
	}
	seen := map[string]bool{}
	var out []Entry
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		for _, e := range idx.byTag[tag] {
			if seen[e.Path] {
				continue
			}
			if len(sevSet) > 0 && !sevSet[strings.ToLower(e.Severity)] {
				continue
			}
			seen[e.Path] = true
			out = append(out, e)
		}
	}
	return out
}

func (idx *Index) AbsPath(rel string) string {
	return filepath.Join(idx.dir, rel)
}

func parseTags(raw interface{}) []string {
	tagSet := map[string]bool{}
	switch v := raw.(type) {
	case string:
		for _, part := range strings.Split(v, ",") {
			if s := strings.TrimSpace(strings.ToLower(part)); s != "" {
				tagSet[s] = true
			}
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				for _, part := range strings.Split(s, ",") {
					if p := strings.TrimSpace(strings.ToLower(part)); p != "" {
						tagSet[p] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(tagSet))
	for t := range tagSet {
		out = append(out, t)
	}
	return out
}

func parseTemplate(path, rootDir string) (Entry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, false
	}
	var m templateInfo
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Entry{}, false
	}
	if m.ID == "" {
		return Entry{}, false
	}
	tags := parseTags(m.Info.Tags)
	rel, _ := filepath.Rel(rootDir, path)
	rel = filepath.ToSlash(rel)
	return Entry{
		ID:       m.ID,
		Path:     rel,
		Severity: strings.ToLower(m.Info.Severity),
		Tags:     tags,
	}, true
}
