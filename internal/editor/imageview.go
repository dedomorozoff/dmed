package editor

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"dmed/internal/imgprev"
)

// A picture is not text, and this file is why the editor can hold one anyway.
//
// The alternative — leaving a PNG to the buffer — puts the bytes in as latin-1
// prose: a column of "ÿØ" boxes, one per pixel row, that scrolls like a file
// and saves back over the picture it came from. So a tab that opens an image
// keeps the decoded frames and an *empty* buffer, and the pane draws the picture
// where the lines would be.
//
// Three decisions are the load-bearing ones:
//
//   - The buffer stays. It is not deleted and not bypassed: about a hundred
//     places index tabs[pane.tabIdx] and call cur(), and an image tab with no
//     buffer is a nil dereference in a key handler rather than a picture. What
//     changes is that nothing may write to it, which is enforced where the keys
//     are handled rather than by making the type optional.
//
//   - Read-only is enforced at the key handler, not by convention. Ctrl+S on a
//     picture tab must refuse rather than write an empty buffer over the file;
//     that is the one path that could destroy what the user opened.
//
//   - The frames are decoded once, at open. Decoding is milliseconds for a
//     screenshot and hundreds for a large photo, and doing it inside View would
//     freeze the interface on every resize and on every frame of an animation.
//     As with the highlighting and the git diff, the view never decodes.

// imageExtensions are the suffixes worth handing to the decoder.
//
// It is a list rather than a sniff because the question being asked is "should I
// *look* at this", and a decode attempt on every file the editor opens is the
// wrong question to ask of a 4 MB log. The decoder still decides: a .png that
// does not decode opens as text, with the reason in the status line. Like the
// SQLite peek, the extension is the cheap first question and the bytes are the
// real one.
var imageExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".bmp":  true,
	".webp": true,
}

// maxImageBytes bounds what is pulled into memory to draw a tab. A file whose
// name ends in .png can be any size at all; past this the tab opens as text with
// a note, which is a worse view of a picture and a far better outcome than an
// out-of-memory kill of the session.
const maxImageBytes = 64 << 20

// imageView is the picture a tab is showing.
type imageView struct {
	anim  *imgprev.Animation
	frame int

	// Rendered art, cached against the width and the frame it was drawn at. A
	// resize or a frame change redraws; nothing else does, which is what keeps a
	// terminal GIF from re-sampling the whole picture on every tick.
	art      []string
	artCols  int
	artFrame int
}

// animated reports whether the tab is showing something with more than one
// frame, which is the only case that needs a timer.
func (v *imageView) animated() bool { return v != nil && v.anim.Animated() }

// frameImage is the frame currently on screen, guarding the two ways the index
// can be wrong: a reloaded file that turned out to have fewer frames, and an
// empty animation.
func (v *imageView) frameImage() image.Image {
	if v == nil || v.anim == nil || len(v.anim.Frames) == 0 {
		return nil
	}
	if v.frame < 0 || v.frame >= len(v.anim.Frames) {
		return v.anim.Frames[0]
	}
	return v.anim.Frames[v.frame]
}

// rows renders the current frame at the given width and returns the art split
// into terminal rows.
func (v *imageView) rows(cols int, profile colorprofile.Profile) []string {
	if v == nil {
		return nil
	}
	img := v.frameImage()
	if img == nil {
		return nil
	}
	if v.art != nil && v.artCols == cols && v.artFrame == v.frame {
		return v.art
	}
	art, _ := imgprev.Render(img, cols, profile)
	rows := strings.Split(art, "\n")
	v.art, v.artCols, v.artFrame = rows, cols, v.frame
	return rows
}

// caption is the line under the picture: what it is, how big it is, and where an
// animation has got to.
//
// The frame counter is here rather than in the tab bar because the tab bar is
// shared by every tab and this belongs to the one being shown; and it is here
// rather than nowhere because a GIF with no frame counter reads as a flicker.
func (v *imageView) caption(name string) string {
	if v == nil || v.anim == nil {
		return ""
	}
	kind := strings.TrimPrefix(v.anim.MIME, "image/")
	size := strconv.Itoa(v.anim.PixelWidth) + "×" + strconv.Itoa(v.anim.PixelHeight)
	if frames := len(v.anim.Frames); frames > 1 {
		return name + "  " + size + " " + kind + "  " +
			strconv.Itoa(v.frame+1) + "/" + strconv.Itoa(frames)
	}
	return name + "  " + size + " " + kind
}

// loadImage decodes a file into a picture view.
//
// A file whose extension says picture but whose bytes are not one is *not* an
// error the user needs to act on: it opens as text with the reason in the status
// line. That is the same fallback as a format this build has no decoder for, and
// it is deliberately not a failure either — a tab that refuses to open is a far
// worse answer than a tab showing text with one line of explanation.
func loadImage(data []byte) (*imageView, error) {
	if len(data) > maxImageBytes {
		return nil, errors.New("image is too large to draw (" +
			strconv.Itoa(len(data)/(1<<20)) + " MB)")
	}
	anim, err := imgprev.Decode(data)
	if err != nil {
		return nil, err
	}
	// artFrame starts at -1 so the first render is a miss: a frame index of 0 is
	// also the value the cache would hold after a render at 0 columns, and a
	// picture one column wide is narrower than this editor draws.
	return &imageView{anim: anim, artFrame: -1}, nil
}

// looksLikeImage reports whether a path is worth handing to the decoder.
func looksLikeImage(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// imgFrameMsg advances an animation by one frame.
type imgFrameMsg struct{}

// imgTick schedules the next frame of the active picture.
//
// It is a command rather than a goroutine with a timer because Bubble Tea owns
// the clock: one tick per frame, each scheduling the next, means the animation
// stops the moment nothing is scheduling — a closed tab, a switched-away tab, a
// program on its way out — with nothing left to leak and nothing to cancel.
func imgTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return imgFrameMsg{} })
}

// animCmd schedules the next frame if an animation is on screen and none is
// already pending.
//
// The pending flag is what keeps one animation from becoming two: every
// message asks for a tick, and without it each of those messages would start
// another chain and the picture would run at twice its declared speed. The flag
// is cleared by the tick itself, so a chain that has ended — the tab was closed,
// the picture switched away — starts again on its own when an animation comes
// back on screen, and one that is running is never doubled.
func (m *Model) animCmd() tea.Cmd {
	if m.imgPending {
		return nil
	}
	d := m.animationDelay()
	if d <= 0 {
		return nil
	}
	m.imgPending = true
	return imgTick(d)
}

// animationDelay is how long the active tab's current frame should stay up, or
// zero when there is nothing to animate.
//
// The tab the pane shows is the one that decides: an animation in a
// non-active tab is not on screen, and a chain ticking for nobody is a wasted
// redraw at best and an animation that refuses to stop at worst.
func (m Model) animationDelay() time.Duration {
	if len(m.tabs) == 0 {
		return 0
	}
	t := &m.tabs[m.activeTabIndex()]
	if !t.img.animated() {
		return 0
	}
	return t.img.anim.FrameDelay(t.img.frame)
}

// advanceFrame moves an animation on by one frame and wraps.
func (m *Model) advanceFrame() {
	if len(m.tabs) == 0 {
		return
	}
	t := &m.tabs[m.activeTabIndex()]
	if !t.img.animated() {
		return
	}
	t.img.frame = (t.img.frame + 1) % len(t.img.anim.Frames)
}

// imageRows draws the picture in a pane: the art, scrolled by the pane's own
// offset, over a caption on the last row.
//
// The caption takes the bottom row rather than sitting under the art because the
// art's height follows the picture's aspect ratio and the pane's does not: a
// caption placed under the art would move up and down as the terminal is
// resized, which is exactly the kind of thing that makes a resize feel broken.
// On the last row it is always in the same place.
//
// The pane keeps its vertical scroll offset, so the wheel scrolls a tall picture
// with no new plumbing — the same offset that scrolls a buffer already means
// "how far into the content are we", and here the content is a picture.
func (m Model) imageRows(t *tab, h, totalW int) []string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = strings.Repeat(" ", max(totalW, 0))
	}
	if h < 2 || totalW < 4 {
		return rows
	}
	// One column of margin either side: the art is half-blocks with no gap
	// between them, and flush against the pane edge it reads as a wall of colour
	// rather than as a picture.
	cols := totalW - 2
	art := t.img.rows(cols, m.profileOrDefault())
	if len(art) == 0 {
		// A picture that decodes to nothing is still a file the user opened, so
		// the pane says so rather than showing a blank screen with no reason.
		note := hintStyle.Render("  " + m.t("editor.image_failed", captionName(t)))
		rows[0] = padTo(ansi.Truncate(note, totalW, "…"), totalW)
		return rows
	}
	off := 0
	if p := m.curPane(); p != nil {
		off = p.offsetY
	}
	for i := 0; i < h-1; i++ {
		if idx := off + i; idx >= 0 && idx < len(art) {
			rows[i] = padTo(" "+art[idx], totalW)
		}
	}
	rows[h-1] = padTo(ansi.Truncate(hintStyle.Render(" "+t.img.caption(captionName(t))), totalW, "…"), totalW)
	return rows
}

// captionName is what the caption calls the file: its base name, not the path the
// tab holds.
//
// The caption sits on one row at the pane's width, so an absolute path is
// truncated away to nothing and the caption ends up being an ellipsis — the
// dimensions and the frame counter, the two things it exists to report, pushed
// off the end by a directory prefix the user can already see in the tab bar.
func captionName(t *tab) string {
	if t.path == "" {
		return "[image]"
	}
	return filepath.Base(t.path)
}

// imageScrollMax is how far a picture can be scrolled in a pane.
//
// It is asked of the picture rather than assumed, because the art's height
// follows the aspect ratio: a wide screenshot in a narrow pane is much taller
// than the pane, and treating it as one screenful would cut the picture in half
// with no way to reach the rest.
//
// h is the height of the pane minus the caption row it borrows, in the caller's
// units of content rows.
func (m Model) imageScrollMax(paneIdx, h int) int {
	if paneIdx < 0 || paneIdx >= len(m.panes) {
		return 0
	}
	p := &m.panes[paneIdx]
	t := &m.tabs[p.tabIdx]
	if t.img == nil || h < 1 {
		return 0
	}
	cols := m.paneTotalWidth(paneIdx) - 2
	if cols < 1 {
		return 0
	}
	if n := len(t.img.rows(cols, m.profileOrDefault())) - h; n > 0 {
		return n
	}
	return 0
}

// scrollImage moves the active pane's picture, clamped to its height.
func (m *Model) scrollImage(dir int) {
	if len(m.panes) == 0 {
		return
	}
	p := m.curPane()
	p.offsetY += dir
	if maxOff := m.imageScrollMax(m.activePane, m.paneContentHeight(m.activePane)-1); p.offsetY > maxOff {
		p.offsetY = maxOff
	}
	if p.offsetY < 0 {
		p.offsetY = 0
	}
}

// handleImageKey answers the keys that mean something on a picture tab.
//
// Everything else is dropped, and that is the point: a picture has no text to
// insert into, so a keypress that reached the buffer would type into an empty
// document the user cannot see, and the first Ctrl+S would then replace the
// picture with whatever they typed. Refusing to save is not enough on its own —
// the keys have to not arrive — so the drop happens here, ahead of the editing
// switch every other tab falls into.
//
// What survives is what is about the workspace rather than the file: switching
// tabs, scrolling a tall picture, and the global bindings handled before this
// point.
func (m *Model) handleImageKey(s string) tea.Cmd {
	switch s {
	case "alt+left":
		m.switchTab(-1)
	case "alt+right":
		m.switchTab(1)
	case "ctrl+s":
		m.msg = m.t("msg.image_readonly")
	case "alt+[":
		m.jumpHunk(-1)
	case "alt+]":
		m.jumpHunk(1)
	case "up", "k":
		m.scrollImage(-1)
	case "down", "j":
		m.scrollImage(1)
	case "pgup":
		m.scrollImage(-max(1, m.paneContentHeight(m.activePane)/2))
	case "pgdown":
		m.scrollImage(max(1, m.paneContentHeight(m.activePane)/2))
	case "home", "g":
		m.scrollImage(-1 << 30)
	case "end", "G":
		m.scrollImage(1 << 30)
	case "ctrl+v":
		// The paste verb means something on a selection and nothing here;
		// falling through to the buffer would paste text into a picture tab.
		m.msg = m.t("msg.image_readonly")
	}
	return nil
}

// profileOrDefault is the colour profile the picture is drawn in.
//
// The terminal's own answer is preferred — a truecolor picture sent as 16-colour
// escapes is a field of wrong-coloured blocks — and Detect is the fallback for a
// run whose profile nobody delivered.
func (m Model) profileOrDefault() colorprofile.Profile {
	if m.profile != colorprofile.NoTTY {
		return m.profile
	}
	return colorprofile.Detect(os.Stdout, os.Environ())
}
