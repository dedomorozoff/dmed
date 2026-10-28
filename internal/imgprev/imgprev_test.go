package imgprev

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// solid builds a one-colour image of the given size.
func solid(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// solidPNG lays down a one-colour PNG as bytes, for the Decode tests.
func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, solid(w, h, c)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// quadrants is a 2x2 image: red top-left, green top-right, blue bottom-left,
// white bottom-right. Every half-block preview of it must show all four.
func quadrants(n int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	cols := []color.RGBA{
		{R: 255, A: 255}, {G: 255, A: 255},
		{B: 255, A: 255}, {R: 255, G: 255, B: 255, A: 255},
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.SetRGBA(x, y, cols[(y/(n/2))*2+x/(n/2)])
		}
	}
	return img
}

// animGIF builds a GIF of n frames, each a solid colour, 40 hundredths of a
// second per frame.
func animGIF(t *testing.T, n int, cols []color.RGBA) []byte {
	t.Helper()
	g := &gif.GIF{}
	for i := 0; i < n; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{cols[i%len(cols)], color.RGBA{A: 255}})
		for p := range img.Pix {
			img.Pix[p] = 0
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 4)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

// TestDecodeReturnsEveryFrame is the contract the editor's animation rests on:
// a GIF decodes to all of its frames, not to frame zero. A decoder that hands
// back the first frame is a working still-image decoder and a broken animation.
func TestDecodeReturnsEveryFrame(t *testing.T) {
	colors := []color.RGBA{
		{R: 255, A: 255},
		{G: 255, A: 255},
		{B: 255, A: 255},
	}
	a, err := Decode(animGIF(t, 3, colors))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(a.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(a.Frames))
	}
	if !a.Animated() {
		t.Error("a three-frame GIF reports itself as still")
	}
	if a.MIME != "image/gif" {
		t.Errorf("MIME = %q, want image/gif", a.MIME)
	}
	if a.PixelWidth != 4 || a.PixelHeight != 4 {
		t.Errorf("dimensions = %dx%d, want 4x4", a.PixelWidth, a.PixelHeight)
	}
	// Each frame must carry the colour it was drawn in, which is only true if
	// the compositing put this frame over the canvas rather than beside it.
	for i, want := range colors {
		r, g, b, _ := a.Frames[i].At(1, 1).RGBA()
		if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B {
			t.Errorf("frame %d is %d,%d,%d, want %d,%d,%d",
				i, r>>8, g>>8, b>>8, want.R, want.G, want.B)
		}
	}
}

// TestFrameDelayIsClampedToSomethingWatchable pins the two bounds, because both
// ends are ways a GIF's own numbers turn into a broken view: a declared zero
// spins as fast as the terminal redraws, and a declared ten seconds is a picture
// that appears frozen.
func TestFrameDelayIsClampedToSomethingWatchable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hundred time.Duration
		want    time.Duration
	}{
		{"zero is floored", 0, 20 * time.Millisecond},
		{"one is floored", 1, 20 * time.Millisecond},
		{"a sane delay passes through", 5, 50 * time.Millisecond},
		{"an absurd delay is capped", 10000, 2 * time.Second},
	} {
		a := &Animation{Delays: []time.Duration{tc.hundred}}
		if got := a.FrameDelay(0); got != tc.want {
			t.Errorf("%s: FrameDelay = %v, want %v", tc.name, got, tc.want)
		}
	}
	if (&Animation{}).FrameDelay(0) != 0 {
		t.Error("an animation with no delays must report no delay, not a floored one")
	}
}

// TestAStillImageIsNotAnimated is the other half: a PNG must not be scheduled a
// tick, or the editor would redraw a photograph forever.
func TestAStillImageIsNotAnimated(t *testing.T) {
	a, err := Decode(solidPNG(t, 8, 8, color.RGBA{B: 255, A: 255}))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if a.Animated() {
		t.Error("a PNG reports itself as animated")
	}
	if a.FrameDelay(0) != 0 {
		t.Error("a still image must report no delay")
	}
}

// TestDecodeNamesWhatItCannotRead: a file that is not a picture has to produce a
// refusal that says so, because the editor's fallback opens it as text and the
// user needs to know why.
func TestDecodeNamesWhatItCannotRead(t *testing.T) {
	if _, err := Decode([]byte("not a picture at all")); !errors.Is(err, ErrUnsupportedImage) {
		t.Errorf("Decode(text) error = %v, want ErrUnsupportedImage", err)
	}
	if _, err := Decode(nil); err == nil {
		t.Error("Decode(empty) must fail")
	}
}

// TestRowsFollowTheAspectRatio pins the divisor that makes a half-block preview
// look like the image rather than twice as tall as it.
func TestRowsFollowTheAspectRatio(t *testing.T) {
	for _, tc := range []struct{ w, h, cols, wantRows int }{
		{w: 100, h: 100, cols: 20, wantRows: 10}, // square, 2 pixels per cell
		{w: 200, h: 100, cols: 20, wantRows: 5},  // twice as wide, half as many rows
		{w: 100, h: 200, cols: 20, wantRows: 20}, // twice as tall, twice as many rows
		{w: 400, h: 100, cols: 20, wantRows: 3},  // panorama, never zero
	} {
		art, rows := Render(solid(tc.w, tc.h, color.RGBA{A: 255}), tc.cols, colorprofile.TrueColor)
		if rows != tc.wantRows {
			t.Errorf("%dx%d at %d cols: rows = %d, want %d", tc.w, tc.h, tc.cols, rows, tc.wantRows)
		}
		if got := len(strings.Split(art, "\n")); got != tc.wantRows {
			t.Errorf("%dx%d: art has %d rows, want %d", tc.w, tc.h, got, tc.wantRows)
		}
	}
}

// TestArtWidthIsExactlyCols guards the property the editor depends on: a row
// wider than the pane is truncated or wrapped, so the width has to be right at
// the moment it is produced rather than trimmed later.
func TestArtWidthIsExactlyCols(t *testing.T) {
	for _, cols := range []int{1, 8, 32, 200} {
		art, rows := Render(quadrants(8), cols, colorprofile.TrueColor)
		if rows == 0 {
			t.Fatalf("Render at %d cols drew nothing", cols)
		}
		for i, row := range strings.Split(art, "\n") {
			if got := ansi.StringWidth(row); got != cols {
				t.Errorf("at %d cols, row %d is %d cells wide, want %d", cols, i, got, cols)
			}
		}
	}
}

// TestDegenerateSizesDoNotPanic: a caller that asks for zero columns, or hands a
// one-pixel image, must get an answer rather than a division by zero.
func TestDegenerateSizesDoNotPanic(t *testing.T) {
	for _, tc := range []struct{ w, h, cols int }{
		{1, 1, 0}, {1, 1, 1}, {1, 4000, 8}, {4000, 1, 8}, {3, 7, 1},
	} {
		art, _ := Render(solid(tc.w, tc.h, color.RGBA{A: 255}), tc.cols, colorprofile.TrueColor)
		if tc.cols >= 1 && art == "" {
			t.Errorf("%dx%d at %d cols drew nothing", tc.w, tc.h, tc.cols)
		}
	}
	if art, rows := Render(nil, 8, colorprofile.TrueColor); art != "" || rows != 0 {
		t.Errorf("Render(nil) = %q, %d; want empty", art, rows)
	}
}

// TestEveryQuadrantReachesThePreview is what makes this a preview and not a
// thumbnail of the wrong corner: the sampler has to visit the whole image, so
// all four of red, green, blue and white must appear.
func TestEveryQuadrantReachesThePreview(t *testing.T) {
	art, _ := Render(quadrants(8), 16, colorprofile.TrueColor)
	for _, want := range []string{
		"255;0;0",     // red
		"0;255;0",     // green
		"0;0;255",     // blue
		"255;255;255", // white
	} {
		if !strings.Contains(art, want) {
			t.Errorf("the art is missing %s:\n%q", want, art)
		}
	}
}

// TestProfileDegradation pins the three steps down. The point of each is that the
// preview stays a picture: escapes the terminal cannot read must not be sent, and
// a colourless terminal must get shape rather than a field of identical blocks.
func TestProfileDegradation(t *testing.T) {
	src := solid(8, 8, color.RGBA{R: 0x33, G: 0x66, B: 0x99, A: 255})

	t.Run("ansi256 quantises", func(t *testing.T) {
		art, _ := Render(src, 8, colorprofile.ANSI256)
		if strings.Contains(art, "51;102;153") {
			t.Error("truecolor escapes reached an ANSI256 terminal")
		}
		if !strings.Contains(art, "38;5;") {
			t.Errorf("the art carries no 256-color escapes:\n%q", art)
		}
	})

	t.Run("ansi16 quantises", func(t *testing.T) {
		art, _ := Render(src, 8, colorprofile.ANSI)
		if strings.Contains(art, "38;5;") {
			t.Error("256-color escapes reached a 16-color terminal")
		}
		if !strings.Contains(art, "\x1b[3") {
			t.Errorf("the art carries no 16-color escapes:\n%q", art)
		}
	})

	t.Run("ascii falls back to a ramp", func(t *testing.T) {
		art, _ := Render(quadrants(8), 16, colorprofile.ASCII)
		if strings.Contains(art, "\x1b") {
			t.Errorf("escape sequences reached a terminal with no colour:\n%q", art)
		}
		if strings.Contains(art, "▀") {
			t.Errorf("half blocks were used with no colour to give them:\n%q", art)
		}
		// Only the glyphs this fixture's four luminances land on are expected, so
		// the check is that brightness separates them. A ramp that returned one
		// glyph for everything would satisfy "some ramp glyph is present" and
		// still carry none of the picture.
		for _, tc := range []struct{ want, why string }{
			{"%", "blue, the darkest"},
			{"#", "red"},
			{"=", "green"},
			{" ", "white, the brightest"},
		} {
			if !strings.Contains(art, tc.want) {
				t.Errorf("%s is not shown as %q:\n%q", tc.why, tc.want, art)
			}
		}
	})

	t.Run("the ramp is as tall as the coloured path", func(t *testing.T) {
		// Half the rows would make the fallback a visibly different picture of
		// the same image, which is worse than a rougher one.
		_, coloured := Render(quadrants(8), 16, colorprofile.TrueColor)
		_, plain := Render(quadrants(8), 16, colorprofile.ASCII)
		if plain != coloured {
			t.Errorf("ramp rows = %d, coloured rows = %d: they must match", plain, coloured)
		}
	})
}

// TestEveryRowEndsWithAReset is the fix for a caption drawn in the picture's
// colour.
//
// Every cell re-states both of its colours, so the one that survives a row is
// the last cell's background. Without a reset at the end of the row that
// background stays current across the newline, and whatever the editor draws
// next — the caption under the art, or the row after it — is painted in the
// colour of the image's bottom-right pixel. It is invisible on a picture whose
// edges happen to be dark, which is why it survives a look at a screenshot and
// fails in a test.
func TestEveryRowEndsWithAReset(t *testing.T) {
	for _, profile := range []colorprofile.Profile{
		colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI,
	} {
		t.Run(profile.String(), func(t *testing.T) {
			art, _ := Render(quadrants(8), 8, profile)
			for i, row := range strings.Split(art, "\n") {
				if !strings.HasSuffix(row, ansi.Style{}.String()) {
					t.Errorf("row %d does not end in a reset: %q", i, row)
				}
			}
		})
	}
}

// TestTheResetDoesNotWidenTheRow: the reset is zero width, so adding it must not
// change the measured width of a row. A row that came out one cell too wide
// would not fit the pane that draws the art exactly.
func TestTheResetDoesNotWidenTheRow(t *testing.T) {
	art, _ := Render(quadrants(8), 8, colorprofile.TrueColor)
	for i, row := range strings.Split(art, "\n") {
		if got := ansi.StringWidth(row); got != 8 {
			t.Errorf("row %d is %d cells wide with the reset, want 8", i, got)
		}
	}
}

// TestTheRampIsLeftAlone: the no-colour fallback emits no escapes, so there is
// nothing to reset and a reset added to it would be visible on any terminal that
// shows control characters.
func TestTheRampIsLeftAlone(t *testing.T) {
	art, _ := Render(quadrants(8), 8, colorprofile.ASCII)
	if strings.Contains(art, "\x1b") {
		t.Errorf("the ramp carries escapes it has no colour to need:\n%q", art)
	}
}

// TestRampIndexEndsAtTheLightestGlyph tests the arithmetic directly, because
// the failure it guards is arithmetic that misbehaves on an architecture the
// test is not running on. Go fuses multiply-add into a single fused
// multiply-add on arm64, rounding once where an x86 build rounds three times,
// and for pure white that lands the luminance a hair under full. int()
// truncates, so the cell short of the end: the brightest thing in the picture
// drawn as the second-darkest glyph, and a white image rendered as a field of
// dots. The endpoint is only visible on the machine that has the fused
// multiply-add, so what is pinned here is the arithmetic behind it.
func TestRampIndexEndsAtTheLightestGlyph(t *testing.T) {
	first := Ramp[0]
	last := Ramp[len(Ramp)-1]
	if first == last {
		t.Fatal("the ramp's two ends are the same glyph, so brightness cannot separate")
	}

	for _, tc := range []struct {
		name string
		c    color.RGBA
		want byte
	}{
		{"black is the first glyph", color.RGBA{A: 255}, first},
		{"white is the last glyph", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 255}, last},
	} {
		if got := Ramp[rampIndex(tc.c)]; got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	// One division, one rounding, at the end. A version that rounds the
	// luminance to an integer first throws away the fraction that decides the
	// cell and moves a colour onto its neighbour's glyph, and that mistake is
	// invisible except by counting through the whole range, which is what this
	// loop is for. The sweep is one channel, because a grey's weighted sum is
	// already a whole number and would agree with the rounding version
	// everywhere; only a single channel has the fraction that gets thrown away.
	prev := -1
	for v := 0; v <= 0xff; v++ {
		got := rampIndex(color.RGBA{R: uint8(v), A: 255})
		want := (299 * v) * (len(Ramp) - 1) / (255 * 1000)
		if got != want {
			t.Errorf("rampIndex(red %d) = %d, want the exact ratio truncated, %d", v, got, want)
		}
		if got < prev {
			t.Fatalf("rampIndex goes backwards at red %d: %d after %d", v, got, prev)
		}
		prev = got
	}
}
