package editor

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

const (
	finderMaxFiles = 5000
	finderMaxShown = 8
)

func collectFiles(base string) []string {
	var out []string
	filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= finderMaxFiles {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

func fuzzyScore(q, s string) int {
	if q == "" {
		return 1
	}
	qr := []rune(strings.ToLower(q))
	sr := []rune(strings.ToLower(s))
	score := 0
	qi := 0
	prev := -2
	for i, r := range sr {
		if qi < len(qr) && r == qr[qi] {
			score++
			if i == prev+1 {
				score += 2
			}
			if i == 0 || isNameSep(sr[i-1]) {
				score += 3
			}
			prev = i
			qi++
		}
	}
	if qi < len(qr) {
		return -1
	}
	return score*10 - len(sr)
}

func isNameSep(r rune) bool {
	switch r {
	case '/', '\\', '_', '-', '.', ' ':
		return true
	}
	return false
}

func searchFiles(files []string, q string) []string {
	type hit struct {
		path  string
		score int
	}
	var hits []hit
	for _, f := range files {
		if sc := fuzzyScore(q, f); sc >= 0 {
			hits = append(hits, hit{f, sc})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if len(hits[i].path) != len(hits[j].path) {
			return len(hits[i].path) < len(hits[j].path)
		}
		return hits[i].path < hits[j].path
	})
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.path)
		if len(out) >= finderMaxShown {
			break
		}
	}
	return out
}

func shortenPath(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func normalizePath(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
