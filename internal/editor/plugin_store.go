package editor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/bundled"
)

// storeItem is one plugin shown in the built-in store. Embedded items are
// always available offline; remote items are listed from the configured GitHub
// repo and downloaded on demand.
type storeItem struct {
	File   string
	Name   string
	Desc   string
	Remote bool
}

// pluginStoreMsg delivers the remote plugin listing to the model.
type pluginStoreMsg struct {
	items []storeItem
	err   error
}

// pluginSourceMsg delivers a downloaded plugin source to the model.
type pluginSourceMsg struct {
	file string
	src  string
	err  error
}

// pluginRepoConfig returns the configured GitHub source for the remote store.
func (m Model) pluginRepoConfig() (repo, dir, branch string) {
	repo, dir, branch = m.cfg.Plugins.Repo, m.cfg.Plugins.Dir, m.cfg.Plugins.Branch
	if repo == "" {
		repo = "dedomorozoff/dmed"
	}
	if dir == "" {
		dir = "plugins"
	}
	if branch == "" {
		branch = "main"
	}
	return repo, dir, branch
}

// parseRemotePluginList extracts *.lua file entries from a GitHub contents API
// response body.
func parseRemotePluginList(body []byte) ([]storeItem, error) {
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	var out []storeItem
	for _, e := range entries {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Name), ".lua") {
			continue
		}
		out = append(out, storeItem{
			File:   e.Name,
			Name:   strings.TrimSuffix(e.Name, ".lua"),
			Desc:   "github",
			Remote: true,
		})
	}
	return out, nil
}

// fetchRemotePluginList lists *.lua files in a GitHub repo directory via the
// contents API.
func fetchRemotePluginList(repo, dir, branch string) ([]storeItem, error) {
	u := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s",
		url.PathEscape(repo), url.PathEscape(dir), url.QueryEscape(branch))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseRemotePluginList(body)
}

// fetchRemoteSource downloads a single plugin's .lua from a GitHub repo.
func fetchRemoteSource(repo, dir, branch, file string) (string, error) {
	u := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, branch, dir+"/"+file)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github returned %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// fetchStoreListCmd starts a background fetch of the remote plugin listing.
func (m Model) fetchStoreListCmd(repo, dir, branch string) tea.Cmd {
	return func() tea.Msg {
		items, err := fetchRemotePluginList(repo, dir, branch)
		return pluginStoreMsg{items: items, err: err}
	}
}

// fetchStoreSourceCmd downloads a single remote plugin source in the background.
func (m Model) fetchStoreSourceCmd(repo, dir, branch, file string) tea.Cmd {
	return func() tea.Msg {
		src, err := fetchRemoteSource(repo, dir, branch, file)
		return pluginSourceMsg{file: file, src: src, err: err}
	}
}

// storeEmbeddedItems seeds the store with the always-available embedded plugins.
func storeEmbeddedItems() []storeItem {
	var out []storeItem
	for _, p := range bundled.Store {
		out = append(out, storeItem{File: p.File, Name: p.Name, Desc: p.Desc, Remote: false})
	}
	return out
}
