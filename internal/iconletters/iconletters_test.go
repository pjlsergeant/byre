package iconletters

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTable(t *testing.T) {
	ok := map[string]string{"eu": "EU", "Eu": "EU", "1a": "1A", "é": "É", "Q": "Q"}
	for in, want := range ok {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := map[string]string{
		"":      "takes one or two letters or digits",
		"abc":   "takes one or two letters or digits",
		"a-":    "takes one or two letters or digits",
		"a b":   "takes one or two letters or digits",
		"a\xff": "takes one or two letters or digits",
		"☃":     "takes one or two letters or digits",
		"東":     "has no glyph",
	}
	for in, frag := range bad {
		_, err := Normalize(in)
		if err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("Normalize(%q) err = %v; want fragment %q", in, err, frag)
		}
	}
}

func TestFromLabelTable(t *testing.T) {
	cases := map[string]string{
		"studio":       "ST",
		"far":          "FA",
		"prod-eu":      "PR",
		"10.0.0.5":     "10",
		"x":            "X",
		"---":          "",
		"[fe80::1]:22": "FE",
		"東京":           "",
		"東x":           "X",
	}
	for in, want := range cases {
		if got := FromLabel(in); got != want {
			t.Errorf("FromLabel(%q) = %q; want %q", in, got, want)
		}
	}
}

// basePNG is an opaque mid-grey square with transparent corners.
func basePNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if (x < side/16 || x >= side-side/16) && (y < side/16 || y >= side-side/16) {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{100, 120, 140, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRenderIsPackableDistinctAndMarked(t *testing.T) {
	for _, side := range []int{1024, 128} {
		base := basePNG(t, side)
		out, err := Render(base, "eu")
		if err != nil {
			t.Fatalf("%d: %v", side, err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("%d: rendered icon does not decode: %v", side, err)
		}
		if img.Bounds() != image.Rect(0, 0, side, side) {
			t.Fatalf("%d: bounds %v", side, img.Bounds())
		}
		if bytes.Equal(out, base) {
			t.Fatalf("%d: render left the art unchanged", side)
		}
		if !IsByreRendered(out) || IsByreRendered(base) {
			t.Fatalf("%d: marker: rendered=%v base=%v", side, IsByreRendered(out), IsByreRendered(base))
		}
		again, _ := Render(base, "EU")
		if !bytes.Equal(out, again) {
			t.Fatalf("%d: render is not deterministic", side)
		}
		// Corners keep the art's transparency.
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatalf("%d: transparent corner became alpha %d", side, a)
		}
		white := false
		for x := 0; x < side && !white; x++ {
			r, g, b, _ := img.At(x, side/2).RGBA()
			white = r>>8 > 200 && g>>8 > 200 && b>>8 > 200
		}
		if !white {
			t.Fatalf("%d: no letter pixels in the central band", side)
		}
		// The white ink stands capHeightFrac of the side tall ("EU" has no
		// descender; antialiased edges and the U's overshoot stay inside
		// the band).
		top, bottom := side, -1
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r>>8 > 200 && g>>8 > 200 && b>>8 > 200 {
					top, bottom = min(top, y), max(bottom, y)
				}
			}
		}
		want := capHeightFrac * float64(side)
		if h := float64(bottom - top + 1); h < 0.85*want || h > 1.15*want {
			t.Fatalf("%d: ink height %.0f px; want about %.0f (capHeightFrac of the side)", side, h, want)
		}
	}
}

func TestRenderRejects(t *testing.T) {
	if _, err := Render([]byte("not a png"), "EU"); err == nil || !strings.Contains(err.Error(), "must be a PNG") {
		t.Errorf("non-PNG: %v", err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	if _, err := Render(b.Bytes(), "EU"); err == nil || !strings.Contains(err.Error(), "must be square") {
		t.Errorf("non-square: %v", err)
	}
	if _, err := Render(basePNG(t, 128), "abc"); err == nil || !strings.Contains(err.Error(), "takes one or two letters or digits") {
		t.Errorf("bad letters: %v", err)
	}
}

func TestIsByreRenderedRejectsGarbage(t *testing.T) {
	out, err := Render(basePNG(t, 128), "AB")
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string][]byte{
		"not png":   []byte("not png"),
		"plain png": basePNG(t, 128),
		"truncated": out[:40], // signature + IHDR + part of the marker chunk
		"empty":     nil,
	} {
		if IsByreRendered(p) {
			t.Errorf("%s: read as byre-rendered", name)
		}
	}
}

// chunk is one PNG chunk with a valid CRC.
func chunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

func TestIsByreRenderedRequiresWellFormedMarkedPNG(t *testing.T) {
	out, err := Render(basePNG(t, 128), "AB")
	if err != nil {
		t.Fatal(err)
	}
	if !IsByreRendered(out) {
		t.Fatal("genuine render not recognized")
	}
	const ihdrEnd = 8 + 12 + 13
	marker := chunk("tEXt", append([]byte(markerKeyword+"\x00"), markerText...))
	markerEnd := ihdrEnd + len(marker)

	badCRC := bytes.Clone(out)
	badCRC[markerEnd-1] ^= 0xff

	// The marker moved to just before IEND: valid chunks, wrong place.
	plain := append(bytes.Clone(out[:ihdrEnd]), out[markerEnd:]...)
	iend := len(plain) - 12
	moved := append(append(bytes.Clone(plain[:iend]), marker...), plain[iend:]...)

	badIDAT := bytes.Clone(out)
	badIDAT[markerEnd+8] ^= 0xff // first data byte of the chunk after the marker

	// Structurally broken but every CRC valid and the header decodable:
	// only a full decode rejects these.
	noIDAT := append(bytes.Clone(out[:markerEnd]), chunk("IEND", nil)...)
	longIEND := append(bytes.Clone(out[:len(out)-12]), chunk("IEND", []byte{0})...)
	for name, p := range map[string][]byte{"no IDAT": noIDAT, "nonzero-length IEND": longIEND} {
		if _, err := png.DecodeConfig(bytes.NewReader(p)); err != nil {
			t.Fatalf("%s: fixture header undecodable, so it would not exercise the structural rule: %v", name, err)
		}
	}

	for name, p := range map[string][]byte{
		"marker CRC corrupted":        badCRC,
		"marker not right after IHDR": moved,
		"later chunk CRC corrupted":   badIDAT,
		"IEND missing":                out[:len(out)-12],
		"truncated mid-chunk":         out[:len(out)-20],
		"trailing bytes after IEND":   append(bytes.Clone(out), 0),
		"no IDAT":                     noIDAT,
		"nonzero-length IEND":         longIEND,
	} {
		if IsByreRendered(p) {
			t.Errorf("%s: read as byre-rendered", name)
		}
	}
}

func TestIsByreRenderedHugeLengthTerminates(t *testing.T) {
	out, err := Render(basePNG(t, 128), "AB")
	if err != nil {
		t.Fatal(err)
	}
	// 12+0xFFFFFFF4 wraps to 0 in uint32: a walk computing the advance
	// there would never move.
	p := bytes.Clone(out[:8+12+13])
	p = binary.BigEndian.AppendUint32(p, 0xFFFFFFF4)
	p = append(p, "tEXt\x00\x00\x00\x00\x00\x00\x00\x00"...)
	done := make(chan bool, 1)
	go func() { done <- IsByreRendered(p) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("huge-length chunk read as byre-rendered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IsByreRendered did not return on a wrapping chunk length")
	}
}

// markedPNG is a marked, CRC-valid 8-bit RGBA PNG whose IHDR declares
// w x h and whose single IDAT holds raw, zlib-compressed.
func markedPNG(t *testing.T, w, h uint32, raw []byte) []byte {
	t.Helper()
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit, RGBA, deflate, adaptive, no interlace
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := append([]byte(nil), pngSig...)
	p = append(p, chunk("IHDR", ihdr)...)
	p = append(p, chunk("tEXt", append([]byte(markerKeyword+"\x00"), markerText...))...)
	p = append(p, chunk("IDAT", z.Bytes())...)
	return append(p, chunk("IEND", nil)...)
}

func TestIsByreRenderedBoundsDeclaredSize(t *testing.T) {
	// The builder is sound: a full-data 128x128 fixture is recognized.
	if !IsByreRendered(markedPNG(t, 128, 128, make([]byte, 128*(1+128*4)))) {
		t.Fatal("well-formed 128x128 fixture not recognized; the cases below would prove nothing")
	}
	for name, p := range map[string][]byte{
		"non-square 1024x512":   markedPNG(t, 1024, 512, make([]byte, 512*(1+1024*4))),
		"square, not icon size": markedPNG(t, 100, 100, make([]byte, 100*(1+100*4))),
	} {
		if IsByreRendered(p) {
			t.Errorf("%s: read as byre-rendered", name)
		}
	}

	// A decompression bomb: a tiny file declaring 100000x100000 RGBA,
	// which png.Decode would allocate (~40GB) before reading any pixels.
	bomb := markedPNG(t, 100000, 100000, make([]byte, 16))
	if _, err := png.DecodeConfig(bytes.NewReader(bomb)); err != nil {
		t.Fatalf("bomb header undecodable, so it would not reach the size bound: %v", err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	done := make(chan bool, 1)
	go func() { done <- IsByreRendered(bomb) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("huge declared size read as byre-rendered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IsByreRendered did not return on a huge declared size")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Fatalf("IsByreRendered allocated %d bytes on a %d-byte file: the declared size was decoded", grew, len(bomb))
	}
}
