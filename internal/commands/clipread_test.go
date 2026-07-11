package commands

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 7, 10, 9, 14, 12, 0, time.UTC)
}

func backend(types []string, content map[string][]byte) clipBackend {
	return clipBackend{
		listTypes: func() ([]string, error) { return types, nil },
		fetch: func(typ string) ([]byte, error) {
			b, ok := content[typ]
			if !ok {
				return nil, fmt.Errorf("no %s on the board", typ)
			}
			return b, nil
		},
	}
}

func TestReadClipboardFileRefsWinOverImageAndText(t *testing.T) {
	cb := backend(
		[]string{typeFileRefs, "image/png", "text/plain"},
		map[string][]byte{typeFileRefs: []byte("/Users/p/shot.png\n/Users/p/b c.pdf\n")},
	)
	sources, err := readClipboard(cb, fixedNow, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0].Path != "/Users/p/shot.png" || sources[1].Path != "/Users/p/b c.pdf" {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestReadClipboardImageBeatsText(t *testing.T) {
	cb := backend(
		[]string{"image/png", "text/plain"},
		map[string][]byte{"image/png": []byte("PNGBYTES")},
	)
	sources, err := readClipboard(cb, fixedNow, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "clipboard-20260710-091412.png" || sources[0].Kind != "clipboard image" {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestReadClipboardImageExtensionIsHonest(t *testing.T) {
	// A TIFF on the board must not be named .png (decisions D11).
	cb := backend([]string{"image/tiff"}, map[string][]byte{"image/tiff": []byte("TIFF")})
	sources, err := readClipboard(cb, fixedNow, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if sources[0].Name != "clipboard-20260710-091412.tiff" {
		t.Fatalf("name = %q", sources[0].Name)
	}
}

func TestReadClipboardText(t *testing.T) {
	cb := backend([]string{"text/plain"}, map[string][]byte{"text/plain": []byte("hello")})
	sources, err := readClipboard(cb, fixedNow, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "clipboard-20260710-091412.txt" || string(sources[0].Data) != "hello" {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestReadClipboardEmptyErrors(t *testing.T) {
	cb := backend(nil, nil)
	_, err := readClipboard(cb, fixedNow, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "nothing deliverable") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadClipboardEmptyFileRefsFallThrough(t *testing.T) {
	// A furl type with nothing usable behind it must fall through to text.
	cb := backend(
		[]string{typeFileRefs, "text/plain"},
		map[string][]byte{typeFileRefs: []byte("\n"), "text/plain": []byte("t")},
	)
	sources, err := readClipboard(cb, fixedNow, io.Discard)
	if err != nil || len(sources) != 1 || sources[0].Kind != "clipboard text" {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
}

func TestParseFileRefs(t *testing.T) {
	raw := "file:///home/p/a%20b.png\r\n" +
		"#comment\n" +
		"/Users/p/plain.pdf\n" +
		"https://example.com/nope\n" +
		"relative/nope\n"
	got := parseFileRefs(raw)
	want := []string{"/home/p/a b.png", "/Users/p/plain.pdf"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parseFileRefs = %v, want %v", got, want)
	}
}

func TestParseDarwinClipInfo(t *testing.T) {
	info := "«class furl», 57, «class utf8», 12, string, 12, «class PNGf», 11916"
	types := parseDarwinClipInfo(info)
	if !hasType(types, typeFileRefs) || !hasType(types, "image/png") || !hasType(types, "text/plain") {
		t.Fatalf("types = %v", types)
	}
}

func TestParseDarwinHexData(t *testing.T) {
	got, err := parseDarwinHexData("«data PNGf89504e47»\n", "PNGf")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\x89PNG" {
		t.Fatalf("decoded = %q", got)
	}
	if _, err := parseDarwinHexData("something else", "PNGf"); err == nil {
		t.Fatal("bad shape should error")
	}
}

func TestNormalizeLinuxTypes(t *testing.T) {
	listing := "TARGETS\ntext/uri-list\nimage/png\nimage/jpeg\nUTF8_STRING\ntext/plain;charset=utf-8\n"
	types := normalizeLinuxTypes(listing)
	if !hasType(types, typeFileRefs) || !hasType(types, "image/png") || !hasType(types, "image/jpeg") || !hasType(types, "text/plain") {
		t.Fatalf("types = %v", types)
	}
	if pickImageType(types) != "image/png" {
		t.Fatalf("png should win: %v", types)
	}
}

func TestParseDarwinClipInfoJPEGAndGIF(t *testing.T) {
	types := parseDarwinClipInfo("«class JPEG», 88, «class GIFf», 12, string, 4")
	if !hasType(types, "image/jpeg") || !hasType(types, "image/gif") {
		t.Fatalf("types = %v", types)
	}
	if pickImageType(types) != "image/jpeg" {
		t.Fatalf("first image type should win absent png: %v", types)
	}
}

func TestReadClipboardFetchErrorFallsThrough(t *testing.T) {
	// A type the backend advertises but can't serve must degrade to the next
	// tier, not take working text down with it (grok review finding).
	cb := clipBackend{
		listTypes: func() ([]string, error) { return []string{typeFileRefs, "image/png", "text/plain"}, nil },
		fetch: func(typ string) ([]byte, error) {
			if typ == "text/plain" {
				return []byte("survivor"), nil
			}
			return nil, fmt.Errorf("tool exploded")
		},
	}
	var warn strings.Builder
	sources, err := readClipboard(cb, fixedNow, &warn)
	if err != nil || len(sources) != 1 || string(sources[0].Data) != "survivor" {
		t.Fatalf("sources = %+v err = %v", sources, err)
	}
	if !strings.Contains(warn.String(), "trying the next representation") {
		t.Fatalf("no degrade note: %q", warn.String())
	}
}

func TestReadClipboardAllFetchesFailSurfacesFirstError(t *testing.T) {
	cb := clipBackend{
		listTypes: func() ([]string, error) { return []string{"image/png"}, nil },
		fetch:     func(string) ([]byte, error) { return nil, fmt.Errorf("boom") },
	}
	_, err := readClipboard(cb, fixedNow, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}
