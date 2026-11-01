// Package imgprev draws a small picture of an image in the terminal, so the user
// can tell what they are opening without leaving the editor.
//
// It is deliberately separate from the editor. What it produces is a string of
// ANSI-coloured rows that the editor's image view paints into a pane; the
// renderer knows nothing about buffers, tabs or keybindings — it turns pixels
// into glyphs and stops there.
package imgprev

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The blank imports register the formats dmed accepts: image.Decode dispatches
// on what those register, so the import is the whole mechanism — there is no
// registry to consult and no sniff to write.
//
// WebP is absent on purpose: it needs golang.org/x/image, and a decoder is a
// dependency kept alive for as long as this feature exists. A user with a WebP
// gets a named error rather than a silent no-op.
var (
	_ = png.Decode
	_ = jpeg.Decode
	_ = gif.Decode
)

// ErrUnsupportedImage is what Decode reports for a format dmed cannot read. The
// message names the format so the user is not left wondering whether the file
// was missing.
var ErrUnsupportedImage = errors.New("unsupported image format")

// Ramp is the ASCII fallback: density from dark to light. Used when the terminal
// has no colour at all, where half-blocks would still draw *something* but the
// colour carries none of the image, so shape has to carry all of it.
const Ramp = "@%#*+=-:. "

// Render draws one already-decoded picture as half-blocks at the given width
// and returns the art with the number of terminal rows it occupies.
//
// The editor's image view needs no wire path — it has the file already and
// sends nothing — so it only needs this renderer, and the decision about sizing
// stays with the caller: a full-pane view wants the width it was given.
func Render(img image.Image, cols int, profile colorprofile.Profile) (art string, rows int) {
	if img == nil || cols < 1 {
		return "", 0
	}
	return render(img, cols, profile)
}

// Decode reads one image, animated or not. An animated GIF comes back as every
// frame with its own delay, because a still of frame zero is a preview of a GIF
// in the same way a screenshot of a video is a preview of the video: the motion
// is the content.
//
// The frame count is capped. A GIF can declare thousands of frames, and holding
// every composited one is a memory cost the user did not ask for by opening a
// file; past the cap the animation simply stops there.
func Decode(data []byte) (*Animation, error) {
	if len(data) == 0 {
		return nil, errors.New("the file is empty")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, unsupported(format, err)
	}
	if format == "gif" {
		return decodeGIF(data, cfg)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, unsupported(format, err)
	}
	// A still image has no delays at all rather than a zero one: FrameDelay
	// floors a declared zero to something watchable, which is right for a GIF
	// frame and wrong here — it would make a photograph look like it is meant to
	// be advanced. No delays is how "there is nothing to time" is said.
	return &Animation{
		Frames:      []image.Image{img},
		PixelWidth:  cfg.Width,
		PixelHeight: cfg.Height,
		MIME:        mimeFor(format),
	}, nil
}

// MaxFrames bounds how much of an animation is decoded. It is deliberately
// generous: a real GIF over this is either a mistake or something no terminal
// preview could show anyway.
const MaxFrames = 240

// Animation is a decoded picture: one frame for a still image, many for a GIF.
type Animation struct {
	Frames      []image.Image
	Delays      []time.Duration
	PixelWidth  int
	PixelHeight int
	MIME        string
}

// FrameDelay is how long frame i is shown, clamped to something a person can
// see.
//
// Delays arrive in hundredths of a second and a GIF is allowed to declare zero,
// which would otherwise spin the frames as fast as the terminal redraws — a
// strobe where the picture should be. The floor is 20ms and the ceiling 2s: the
// first is below what reads as motion, the second is above what anyone waits.
func (a *Animation) FrameDelay(i int) time.Duration {
	if a == nil || len(a.Delays) == 0 {
		return 0
	}
	if i < 0 || i >= len(a.Delays) {
		return 0
	}
	d := a.Delays[i] * 10 * time.Millisecond
	if d < 20*time.Millisecond {
		d = 20 * time.Millisecond
	}
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

// Animated reports whether there is more than one frame to show.
func (a *Animation) Animated() bool { return a != nil && len(a.Frames) > 1 }

// decodeGIF composites an animation frame by frame.
//
// The frames of a GIF are not whole pictures: each one is the rectangle that
// changed, drawn over what came before, and the disposal method says whether
// the canvas is put back afterwards. Rendering frame i without compositing
// shows a transparent frame — literally nothing, since the art is drawn from
// the decoded pixels — for every frame that is not full-size, which is most of
// them in a real animation. So each frame is drawn onto the canvas the previous
// one left behind, and DisposalPrevious keeps a copy to restore, because it is
// the one disposal that says "put back what was there before this frame".
func decodeGIF(data []byte, cfg image.Config) (*Animation, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, unsupported("gif", err)
	}
	if len(g.Image) == 0 {
		return nil, errors.New("the gif has no frames")
	}
	bounds := image.Rect(0, 0, cfg.Width, cfg.Height)
	if cfg.Width < 1 || cfg.Height < 1 {
		bounds = g.Image[0].Bounds()
	}
	canvas := image.NewRGBA(bounds)
	var prev *image.RGBA
	out := &Animation{
		PixelWidth:  cfg.Width,
		PixelHeight: cfg.Height,
		MIME:        "image/gif",
	}
	limit := min(len(g.Image), MaxFrames)
	for i := 0; i < limit; i++ {
		frame := g.Image[i]
		if i > 0 {
			switch g.Disposal[i] {
			case gif.DisposalPrevious:
				if prev != nil {
					draw.Draw(canvas, bounds, prev, bounds.Min, draw.Src)
				}
			case gif.DisposalBackground:
				draw.Draw(canvas, bounds, image.Transparent, image.Point{}, draw.Src)
			}
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		// The frame is copied, not aliased: the next iteration draws onto the
		// same canvas, and an aliased frame would change under the copy.
		snapshot := image.NewRGBA(bounds)
		draw.Draw(snapshot, bounds, canvas, bounds.Min, draw.Src)
		out.Frames = append(out.Frames, snapshot)
		// Delays are kept in hundredths of a second, the unit the GIF format
		// itself uses, so FrameDelay is the single place that converts.
		delay := 0
		if i < len(g.Delay) {
			delay = g.Delay[i]
		}
		out.Delays = append(out.Delays, time.Duration(delay))
		if g.Disposal[i] == gif.DisposalPrevious {
			prev = snapshot
		} else {
			prev = nil
		}
	}
	return out, nil
}

func unsupported(format string, err error) error {
	if format == "" {
		// DecodeConfig failed before it could name a format, so the bytes are
		// not an image this build reads. Saying so beats passing the decoder's
		// "unknown format" up as-is, which does not name the editor as the limit.
		return fmt.Errorf("%w: this build reads png, jpeg and gif", ErrUnsupportedImage)
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedImage, format)
}

func mimeFor(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	}
	return ""
}

// render draws the picture as half-blocks.
//
// The upper half block ▀ paints the cell's foreground over its top half and its
// background over the bottom, so one cell carries two vertical pixels. That is
// the whole trick: two pixels per cell is what makes a wide preview legible
// as a picture instead of a smear, and it needs no block characters that some
// terminals render as emoji.
//
// The sample grid is cols wide and 2*rows tall, and rows follows the source
// aspect ratio through a cell that is twice as tall as it is wide. Getting that
// divisor wrong is the classic bug here: sample one pixel per cell and every
// preview comes out twice as tall as the image.
func render(img image.Image, cols int, profile colorprofile.Profile) (string, int) {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 1 || sh < 1 || cols < 1 {
		return "", 0
	}

	// Two pixels of height per cell, and a cell is twice as tall as it is wide.
	rows := max(1, int(math.Round(float64(cols)*float64(sh)/float64(sw)/2)))
	pixH := rows * 2

	if profile <= colorprofile.ASCII {
		return renderRamp(img, cols, rows), rows
	}

	var out strings.Builder
	for row := 0; row < rows; row++ {
		for x := 0; x < cols; x++ {
			top := sample(img, sw, sh, x, row*2, cols, pixH)
			bot := sample(img, sw, sh, x, row*2+1, cols, pixH)
			out.WriteString(paint(top, bot, profile))
		}
		// Reset at the end of the row, not once at the end of the picture. Every
		// cell re-states both of its colours, so the one that matters is the last
		// cell's: without this its background stays current across the newline
		// and paints whatever the editor draws next — the caption under the
		// picture, or the row after it — in the colour of the image's
		// bottom-right pixel.
		out.WriteString(ansi.Style{}.String())
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n"), rows
}

// sample returns the average colour of the source rectangle covering the target
// pixel. Integer edges, no nearest-neighbour: a nearest-neighbour downscale of
// high-contrast content drops whole rows of glyphs and leaves a legible-looking
// but wrong picture.
func sample(img image.Image, sw, sh, tx, ty, tw, th int) color.RGBA {
	x0 := tx * sw / tw
	x1 := (tx + 1) * sw / tw
	y0 := ty * sh / th
	y1 := (ty + 1) * sh / th
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	b := img.Bounds()
	var r, g, bl, a, n int64
	for sy := y0; sy < y1; sy++ {
		for sx := x0; sx < x1; sx++ {
			cr, cg, cb, ca := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
			r += int64(cr >> 8)
			g += int64(cg >> 8)
			bl += int64(cb >> 8)
			a += int64(ca >> 8)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{}
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(a / n)}
}

// paint writes one ▀ with the top pixel in the foreground and the bottom in the
// background.
//
// Each colour is converted to the profile's palette here rather than handed to
// lipgloss, because the two halves of a cell are two separate colours and a style
// cannot carry "this glyph, foreground one thing and background another" per
// glyph. ANSI 16 loses most of an image, but a 16-colour half-block preview is
// still a picture; without the conversion it would be truecolor escapes sent to a
// terminal that cannot read them.
func paint(top, bot color.RGBA, profile colorprofile.Profile) string {
	fg, bg := color.Color(top), color.Color(bot)
	switch profile {
	case colorprofile.TrueColor:
		// Nothing to do: RGBColor is what the wire already wants.
	case colorprofile.ANSI256:
		fg, bg = ansi.Convert256(fg), ansi.Convert256(bg)
	default:
		fg, bg = ansi.Convert16(fg), ansi.Convert16(bg)
	}
	return ansi.Style{}.ForegroundColor(fg).BackgroundColor(bg).String() + "▀"
}

// renderRamp is the no-colour fallback: one ramp character per cell, by
// luminance.
//
// Half-blocks would still be *drawn* on such a terminal, but with the colour
// stripped out what is left is a field of identical blocks — the shape of a
// rectangle, carrying none of the picture. The ramp puts the information back
// into the glyph, which is the only channel left.
//
// The grid is one sample per cell, cols by rows, covering the whole image. rows
// already carries the 2:1 cell aspect, so sampling rows*2 deep would read only
// the top of the picture — the bug this comment exists to prevent, and the one
// that makes a ramp preview show the wrong part of the image.
func renderRamp(img image.Image, cols, rows int) string {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	var out strings.Builder
	for row := 0; row < rows; row++ {
		for x := 0; x < cols; x++ {
			c := sample(img, sw, sh, x, row, cols, rows)
			out.WriteByte(Ramp[rampIndex(c)])
		}
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// rampIndex is where a colour sits on the ramp, and it is integer arithmetic
// because the obvious float version was not the same function everywhere. Go
// fuses multiply-add into a single fused multiply-add on arm64, rounding once
// where an x86 build rounds three times, and for pure white that lands the sum a
// hair under 255. int() truncates rather than rounds, so the cell short of the
// ramp's end: the brightest thing in the picture drawn as the second-darkest
// glyph, and an all-white image rendered as a field of dots.
//
// The weights are the usual 0.299/0.587/0.114 scaled by 1000 so they sum to a
// round thousand. The ramp length is folded into the numerator rather than
// dividing twice, because dividing the luminance to an integer first throws away
// the fractional part that decides the cell — it is the same one-rounding
// mistake in a different place, and it moves greys to the neighbouring glyph.
func rampIndex(c color.RGBA) int {
	const maxLuminance = 255 * 1000 // 0.299 and 0.587 and 0.114, of 255
	i := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) * (len(Ramp) - 1) / maxLuminance
	return min(i, len(Ramp)-1)
}
