package editor

import (
	"net/http"
	"os"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/debug"
	"dmed/internal/update"
)

// updateCheckDelay is how long the editor waits after startup before it asks
// GitHub whether a newer release exists, so the first frame is not spent on a
// network round trip and a slow connection cannot stall the UI.
const updateCheckDelay = 2 * time.Second

// startUpdateCheck bumps the update generation and runs the check in the
// background, returning the channel the answer lands on and the generation
// the reply carries. Bumping the generation lets the editor drop a late
// reply from a superseded request — the same pattern every other async
// subsystem uses.
func (m *Model) startUpdateCheck(force bool) (chan tea.Msg, int) {
	m.updateGen++
	gen := m.updateGen
	ch := m.updateCh
	// The client is created here, on the UI thread, and captured by value
	// before the goroutine starts: a background goroutine must not race the
	// model's field, and a lazy client that only exists after the first
	// check would be nil on the first one.
	m.ensureUpdateHTTPClient()
	client := m.updateHTTPClient
	go func() {
		defer debug.CapturePanicReport(func() {})
		if !force {
			time.Sleep(updateCheckDelay)
		}
		rel, err := update.Check(update.CheckOptions{
			CurrentVersion: m.Version,
			Force:          force,
			CacheFile:      update.DefaultCacheFile(),
			HTTPClient:     client,
			GOOS:           runtime.GOOS,
			GOARCH:         runtime.GOARCH,
		})
		ch <- updateCheckMsg{release: rel, err: err, gen: gen}
	}()
	return ch, gen
}

// maybeCheckUpdate arms the one-shot background check when the user has it on.
// The check is skipped for builds that cannot be updated (a development build
// has no asset to download).
func (m *Model) maybeCheckUpdate() {
	if m.Version == "dev" || m.Version == "" {
		return
	}
	if !m.cfg.Editor.CheckUpdates {
		return
	}
	m.updateChecking = true
	m.msg = m.t("msg.update_checking")
	m.startUpdateCheck(false)
}

// waitForUpdate waits for the next message of the update pipeline and
// re-arms itself, like the other waiters of the editor.
func waitForUpdate(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// updateCheckMsg carries the answer of a background update check. `downloaded`
// is set when Apply replaced the binary and the editor must relaunch. gen
// stamps the request so a reply from a superseded check is dropped.
type updateCheckMsg struct {
	release    *update.Release
	err        error
	downloaded bool
	gen        int
}

// applyUpdateCheck stores a check result, dropping replies from a superseded
// generation. A network error is shown on the status bar and does not
// interrupt the user: the check is cached for 24 hours, so it only surfaces
// again when the cache is stale and the user explicitly asks.
func (m *Model) applyUpdateCheck(msg updateCheckMsg) {
	if msg.gen != m.updateGen {
		return
	}
	m.updateChecking = false
	if msg.downloaded {
		m.msg = m.t("msg.update_done")
		return
	}
	if msg.err != nil {
		m.updateErr = msg.err.Error()
		m.msg = m.t("msg.update_failed", msg.err)
		return
	}
	m.updateErr = ""
	m.updateAvailable = msg.release
	if msg.release == nil {
		m.msg = m.t("msg.update_up_to_date")
		return
	}
	m.msg = m.t("msg.update_available", msg.release.Version)
}

// checkUpdateNow runs an immediate, forced check and reports the outcome on
// the status bar. It is bound to the palette command "update.check".
func (m *Model) checkUpdateNow() tea.Cmd {
	m.updateChecking = true
	m.msg = m.t("msg.update_checking")
	m.startUpdateCheck(true)
	return nil
}

// applyUpdate is the palette command "update.apply": it downloads and
// installs the release that check found, then relaunches the editor so the
// new binary takes over the terminal. A failure is reported on the status bar
// and the running process is left alone. The download runs in the
// background and reports through updateCh, like every other async subsystem,
// so the model is only ever mutated on the UI thread.
func (m *Model) applyUpdate() tea.Cmd {
	rel := m.updateAvailable
	if rel == nil {
		m.msg = m.t("msg.update_none")
		return nil
	}
	m.updateChecking = true
	m.updateGen++
	gen := m.updateGen
	ch := m.updateCh
	m.ensureUpdateHTTPClient()
	client := m.updateHTTPClient
	go func() {
		defer debug.CapturePanicReport(func() {})
		err := update.Apply(update.ApplyOptions{
			Release:    rel,
			HTTPClient: client,
		})
		if err != nil {
			ch <- updateCheckMsg{err: err, gen: gen}
			return
		}
		ch <- updateCheckMsg{downloaded: true, gen: gen}
	}()
	return nil
}

// relaunchUpdate restarts dmed with the same arguments after Apply replaced
// the binary. It is called from Update when updateDownloaded is set, so the
// user sees the new version in a fresh process rather than a stale one.
func (m *Model) relaunchUpdate() {
	_ = update.Restart(os.Args[1:])
}

// ensureUpdateHTTPClient lazily creates the http.Client the update check and
// the apply use. It is a seam: a unit test injects a test server over it so
// no check ever reaches the network, and the production client is created on
// first use rather than in New so the editor starts without one.
func (m *Model) ensureUpdateHTTPClient() {
	if m.updateHTTPClient == nil {
		m.updateHTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
}
