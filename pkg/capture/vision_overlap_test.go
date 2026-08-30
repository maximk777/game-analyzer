package capture

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// Hero's two hole cards are drawn overlapping, and how deeply depends on the
// client's animation state -- there is no one frame that covers the range. So
// the range is composited from the client's own pixels: the pair in
// coinpoker_hero_overlap_sample.png is taken apart and laid down again at a
// tighter pitch, by silhouette, so the result is a real fanned pair rather than
// one rectangle stamped over another.
//
// This test is the reason the recogniser stopped guessing the boundary between
// the two cards. Before it, the failure surface was invisible: the pair was cut
// at a boundary derived from the card's proportions, and the merged region's
// aspect ratio decided which of three code paths ran. Measured against this
// sweep, the old code was correct only from 3% to 40% overlap. From 41% to 52%
// the region's aspect fell between the "one card" and "two cards" bands and
// hero's whole hand was dropped with no diagnostic at all; past 52% it matched
// the single-card band and a crop straddling the seam read 6d 3d as a confident
// "Jd" -- wrong rank, wrong suit, and indistinguishable downstream from a good
// reading. Those are the live symptoms this was chasing: 10c 3s read as 10c Kc,
// and 3s 3c read as 3s and nothing.
//
// Corners are now found as ink, so nothing here depends on where the seam is.

// Geometry of hero's pair in coinpoker_hero_overlap_sample.png, measured once.
// If that file is ever replaced these need remeasuring; parse_image prints the
// region it found.
const (
	overlapSampleCardX = 992  // left card's left edge
	overlapSampleCardY = 1256 // both cards' top edge
	overlapSampleCardW = 149
	overlapSampleCardH = 216
	overlapSampleRight = 1142 // right card's left edge, i.e. the original pitch
	// Background is borrowed from beside the pair, so the vacated strip is
	// filled with real table pixels rather than a flat colour.
	overlapSampleBgShift = 320
)

// recomposeHeroPair rewrites hero's two cards at the given pitch and returns
// the path of the new frame. A pitch of 149 is no overlap at all; 60 hides 60%
// of the left card.
func recomposeHeroPair(t *testing.T, src image.Image, dir string, pitch int) string {
	t.Helper()

	bounds := src.Bounds()
	out := image.NewRGBA(bounds)
	draw.Draw(out, bounds, src, bounds.Min, draw.Src)

	// A card's silhouette, row by row: the span between its outermost white
	// pixels. Rounded corners fall out of this for free, and pasting by
	// silhouette keeps the left card's own edge from being painted over with
	// the felt that sits in the right card's corners.
	silhouette := func(cardX int) [][2]int {
		rows := make([][2]int, overlapSampleCardH)
		for y := 0; y < overlapSampleCardH; y++ {
			first, last := -1, -1
			for x := 0; x < overlapSampleCardW; x++ {
				r, g, b, _ := src.At(cardX+x, overlapSampleCardY+y).RGBA()
				if r>>8 > 190 && g>>8 > 190 && b>>8 > 190 {
					if first < 0 {
						first = x
					}
					last = x
				}
			}
			rows[y] = [2]int{first, last}
		}
		return rows
	}
	leftRows := silhouette(overlapSampleCardX)
	rightRows := silhouette(overlapSampleRight)

	for y := 0; y < overlapSampleCardH; y++ {
		for x := 0; x < overlapSampleCardW*2+2; x++ {
			out.Set(overlapSampleCardX+x, overlapSampleCardY+y,
				src.At(overlapSampleCardX+x+overlapSampleBgShift, overlapSampleCardY+y))
		}
	}
	paste := func(srcX, dstX int, rows [][2]int) {
		for y := 0; y < overlapSampleCardH; y++ {
			lo, hi := rows[y][0], rows[y][1]
			if lo < 0 {
				continue
			}
			for x := lo; x <= hi; x++ {
				out.Set(dstX+x, overlapSampleCardY+y, src.At(srcX+x, overlapSampleCardY+y))
			}
		}
	}
	paste(overlapSampleCardX, overlapSampleCardX, leftRows)
	paste(overlapSampleRight, overlapSampleCardX+pitch, rightRows)

	path := filepath.Join(dir, fmt.Sprintf("overlap-%d.png", pitch))
	w, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating composite: %v", err)
	}
	// These frames are thrown away at the end of the test, so the time is
	// better spent running the recogniser than compressing them.
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(w, out); err != nil {
		t.Fatalf("encoding composite: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing composite: %v", err)
	}
	return path
}

func TestVisionSample_HeroCardsAcrossOverlapDepths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision/ScreenCaptureKit table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

	sample := filepath.Join(root, "testdata", "coinpoker_hero_overlap_sample.png")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("testdata/coinpoker_hero_overlap_sample.png not present")
	}

	work := t.TempDir()
	f, err := os.Open(sample)
	if err != nil {
		t.Fatalf("opening sample: %v", err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatalf("decoding sample: %v", err)
	}

	bin := filepath.Join(work, "parse_image")
	build := exec.Command("swiftc", "-parse-as-library",
		filepath.Join(pkgDir, "table_vision.swift"),
		filepath.Join(pkgDir, "card_templates.swift"),
		filepath.Join(pkgDir, "parse_image_tool.swift"),
		"-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the Swift table analyser failed: %v\n%s", err, out)
	}

	// Live fanning sits around 3-40%. The range is carried further than that on
	// purpose: the point of finding corners rather than deriving a seam is that
	// nothing special happens as the overlap deepens, and a regression that
	// reintroduces a proportional split shows up here as a cliff. Below a pitch
	// of 60 the left card's corner is physically cut off by the right card, and
	// no amount of cleverness can read what is not on screen.
	want := []string{"6d", "3d"}
	for _, pitch := range []int{145, 110, 90, 80, 70, 60} {
		overlapPct := (overlapSampleCardW - pitch) * 100 / overlapSampleCardW
		frame := recomposeHeroPair(t, src, work, pitch)

		run := exec.Command(bin, frame)
		run.Env = append(os.Environ(),
			"POKER_RTA_ASSETS="+filepath.Join(root, "bin", "assets", "coinpoker"))
		out, err := run.CombinedOutput()
		if err != nil {
			t.Fatalf("pitch %d (%d%% overlap): parse_image failed: %v\n%s", pitch, overlapPct, err, out)
		}
		start := slices.Index(out, '{')
		if start < 0 {
			t.Fatalf("pitch %d (%d%% overlap): no JSON in output:\n%s", pitch, overlapPct, out)
		}
		var got parsedSample
		if err := json.Unmarshal(out[start:], &got); err != nil {
			t.Fatalf("pitch %d (%d%% overlap): decoding output: %v\n%s", pitch, overlapPct, err, out)
		}
		if !slices.Equal(got.HeroCards, want) {
			t.Errorf("pitch %d (%d%% overlap): hero cards got %v, want %v", pitch, overlapPct, got.HeroCards, want)
		}
	}
}

// The client ships two decks and only one of them is in testdata: the frames
// were captured with clubs and spades both black. On the four-colour deck a
// club is green, and green is exactly what the ink mask throws away as felt --
// so on that deck a club would have been deleted outright and its card would
// have come back with a rank and no suit. That is not a failure a two-colour
// frame can show.
//
// So the frame is repainted in the other deck's colours, taken from the
// client's own sprites in bin/assets/coinpoker: club (48,135,0), diamond
// (0,144,234). Only the ink is repainted and only inside the cards, with the
// anti-aliased edges kept proportional, so the shapes are still the client's.
func repaintHeroInk(t *testing.T, src image.Image, dir, name string, target [3]float64) string {
	t.Helper()

	bounds := src.Bounds()
	out := image.NewRGBA(bounds)
	draw.Draw(out, bounds, src, bounds.Min, draw.Src)

	for y := 0; y < overlapSampleCardH; y++ {
		// Only inside the cards: the felt and the table furniture around them
		// have to stay the colour they are, or the test would prove nothing
		// about telling a green club from green felt.
		first, last := -1, -1
		for x := 0; x < overlapSampleCardW*2+2; x++ {
			r, g, b, _ := src.At(overlapSampleCardX+x, overlapSampleCardY+y).RGBA()
			if r>>8 > 190 && g>>8 > 190 && b>>8 > 190 {
				if first < 0 {
					first = x
				}
				last = x
			}
		}
		if first < 0 {
			continue
		}
		for x := first; x <= last; x++ {
			px, py := overlapSampleCardX+x, overlapSampleCardY+y
			r32, g32, b32, _ := src.At(px, py).RGBA()
			r, g, b := float64(r32>>8), float64(g32>>8), float64(b32>>8)

			// How much ink covers this pixel, measured as its distance from
			// white rather than by its luminance. Luminance is the wrong
			// measure for a coloured glyph: a fully inked red pixel is dark on
			// its own account, and repainting by luminance diluted the core of
			// every glyph to about half strength -- which made the repainted
			// club far paler than any the client draws, and failed the test for
			// a reason that had nothing to do with the recogniser.
			cover := 1 - min(r, min(g, b))/255
			if cover < 0.05 {
				continue // the card itself
			}
			mix := 1 - cover
			out.Set(px, py, color.RGBA{
				R: uint8(target[0] + (255-target[0])*mix),
				G: uint8(target[1] + (255-target[1])*mix),
				B: uint8(target[2] + (255-target[2])*mix),
				A: 255,
			})
		}
	}

	path := filepath.Join(dir, name)
	w, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating repainted frame: %v", err)
	}
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(w, out); err != nil {
		t.Fatalf("encoding repainted frame: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing repainted frame: %v", err)
	}
	return path
}

func TestVisionSample_FourColourDeckSuits(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision/ScreenCaptureKit table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

	sample := filepath.Join(root, "testdata", "coinpoker_hero_overlap_sample.png")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("testdata/coinpoker_hero_overlap_sample.png not present")
	}

	work := t.TempDir()
	f, err := os.Open(sample)
	if err != nil {
		t.Fatalf("opening sample: %v", err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatalf("decoding sample: %v", err)
	}

	bin := filepath.Join(work, "parse_image")
	build := exec.Command("swiftc", "-parse-as-library",
		filepath.Join(pkgDir, "table_vision.swift"),
		filepath.Join(pkgDir, "card_templates.swift"),
		filepath.Join(pkgDir, "parse_image_tool.swift"),
		"-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the Swift table analyser failed: %v\n%s", err, out)
	}

	cases := []struct {
		name   string
		target [3]float64
		want   []string
	}{
		// Hero holds 6d 3d. Repainted in the four-colour deck's blue, the ranks
		// must survive and the suit must still come out as diamonds -- decided
		// by colour now rather than by the width profile.
		{"blue diamond", [3]float64{0, 144, 234}, []string{"6d", "3d"}},
		// Repainted green, the same two cards must read as clubs. The suit is
		// deliberately not the one on the card: what is under test is that
		// green ink survives the felt filter at all, and that green names a
		// club. If the felt filter ever swallows a club again, this comes back
		// as two empty strings, not as the wrong suit.
		{"green club", [3]float64{48, 135, 0}, []string{"6c", "3c"}},
	}

	for _, tc := range cases {
		frame := repaintHeroInk(t, src, work, tc.name+".png", tc.target)
		run := exec.Command(bin, frame)
		run.Env = append(os.Environ(),
			"POKER_RTA_ASSETS="+filepath.Join(root, "bin", "assets", "coinpoker"))
		out, err := run.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: parse_image failed: %v\n%s", tc.name, err, out)
		}
		start := slices.Index(out, '{')
		if start < 0 {
			t.Fatalf("%s: no JSON in output:\n%s", tc.name, out)
		}
		var got parsedSample
		if err := json.Unmarshal(out[start:], &got); err != nil {
			t.Fatalf("%s: decoding output: %v\n%s", tc.name, err, out)
		}
		if !slices.Equal(got.HeroCards, tc.want) {
			t.Errorf("%s: hero cards got %v, want %v", tc.name, got.HeroCards, tc.want)
		}
	}
}

// The blinds set the scale of everything on the table, and they come from the
// window title, which the system hands over exactly. They were being read by
// text recognition off the title bar instead, and the loop that did it
// overwrote what it had already found -- including with nothing, when a later
// fragment of the same title happened to lack the slash.
//
// Measured over a recorded session: the blinds resolved in 1028 frames of 9788,
// with the same table appearing as "NLH 1229111 - 1K/2K (320)", "NLH 1229111-
// 1K/2K (320)", "@ NLH 1229111 - 1K/2K (320)" and "© NLH 1228078 - 1K/2K (320)".
//
// A missing stake is not a cosmetic loss. Without it there is no floor on what
// counts as a wager, and the floor used to fall back to 1 -- harmless at 1K/2K
// and fatal at a big blind of 0.1, where every real wager is below 1 and was
// discarded, so pot odds were computed against a pot with no bets in it.
func TestVisionSample_BlindsComeFromTheWindowTitle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Vision/ScreenCaptureKit table analyser is macOS only")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("swiftc not available")
	}

	pkgDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving package dir: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))

	sample := filepath.Join(root, "testdata", "coinpoker_live_sample.png")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("testdata/coinpoker_live_sample.png not present")
	}

	bin := filepath.Join(t.TempDir(), "parse_image")
	build := exec.Command("swiftc", "-parse-as-library",
		filepath.Join(pkgDir, "table_vision.swift"),
		filepath.Join(pkgDir, "card_templates.swift"),
		filepath.Join(pkgDir, "parse_image_tool.swift"),
		"-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the Swift table analyser failed: %v\n%s", err, out)
	}

	cases := []struct {
		title       string
		wantSmall   float64
		wantBig     float64
		wantFromOCR bool
	}{
		{title: "NLH 1229111 - 1K/2K (320)", wantSmall: 1000, wantBig: 2000},
		// A micro table. Nothing in the repository was ever captured at this
		// stake, which is exactly why the title has to be supplied rather than
		// recognised.
		{title: "NLH 4410 - 0.05/0.1 (0.01)", wantSmall: 0.05, wantBig: 0.1},
		// A currency symbol used to end the scan before the first digit, so the
		// whole stake parsed as nothing.
		{title: "NLH 4410 - $0.05/$0.10", wantSmall: 0.05, wantBig: 0.10},
		// A comma is a decimal point in most of the world. Reading "0,10" as
		// ten is a hundredfold error; a thousands group is always three digits,
		// which is what separates the two cases.
		{title: "NLH 4410 - 0,05/0,10", wantSmall: 0.05, wantBig: 0.10},
		{title: "PLO 77 - 0.5/1", wantSmall: 0.5, wantBig: 1},
		// Not a table title at all, so recognition still gets its turn and
		// reads the stake off this frame, which is a 1K/2K table.
		{title: "Lobby", wantSmall: 1000, wantBig: 2000, wantFromOCR: true},
	}

	for _, tc := range cases {
		run := exec.Command(bin, sample, "--title", tc.title)
		run.Env = append(os.Environ(),
			"POKER_RTA_ASSETS="+filepath.Join(root, "bin", "assets", "coinpoker"))
		out, err := run.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: parse_image failed: %v\n%s", tc.title, err, out)
		}
		start := slices.Index(out, '{')
		if start < 0 {
			t.Fatalf("%s: no JSON in output:\n%s", tc.title, out)
		}
		var got struct {
			SmallBlind float64 `json:"small_blind"`
			BigBlind   float64 `json:"big_blind"`
		}
		if err := json.Unmarshal(out[start:], &got); err != nil {
			t.Fatalf("%s: decoding output: %v\n%s", tc.title, err, out)
		}
		if got.SmallBlind != tc.wantSmall || got.BigBlind != tc.wantBig {
			t.Errorf("title %q: blinds %v/%v, want %v/%v",
				tc.title, got.SmallBlind, got.BigBlind, tc.wantSmall, tc.wantBig)
		}
	}
}
