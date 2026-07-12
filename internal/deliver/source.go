package deliver

import (
	"bytes"
	"fmt"
	"io"
)

// Source is one thing to deliver. Exactly one of Path / Data / Reader is set:
// Path is a host file or directory; Data is in-memory content (a clipboard
// capture); Reader is streamed content (stdin). Non-path sources carry the
// landing Name and a human Kind ("clipboard image", "stdin") for the
// confirmation line — which states kind and size, never content (a printed
// first line of a just-delivered password would re-disclose it).
type Source struct {
	Path   string
	Data   []byte
	Reader io.Reader
	Name   string
	Kind   string
}

// PathSources wraps plain path arguments.
func PathSources(paths []string) []Source {
	out := make([]Source, len(paths))
	for i, p := range paths {
		out[i] = Source{Path: p}
	}
	return out
}

// label names a source in errors and counts.
func (s Source) label() string {
	if s.Path != "" {
		return s.Path
	}
	if s.Kind != "" {
		return s.Kind
	}
	return s.Name
}

// countingReader counts streamed bytes so the stdin confirmation can state a
// size without buffering the stream.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// deliverSource routes one source and returns its landed path plus the
// confirmation line already printed for non-path kinds.
func deliverSource(cfg Config, sess Session, src Source) (string, error) {
	switch {
	case src.Path != "":
		return deliverPath(cfg, sess, src.Path)
	case src.Data != nil:
		landed, err := deliverStream(cfg, sess, bytes.NewReader(src.Data), src.Name, "/inbox", false)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(cfg.Err, "byre: delivered %s (%s) → %s\n", src.Kind, sizeString(int64(len(src.Data))), landed)
		return landed, nil
	case src.Reader != nil:
		cr := &countingReader{r: src.Reader}
		landed, err := deliverStream(cfg, sess, cr, src.Name, "/inbox", false)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(cfg.Err, "byre: delivered %s (%s) → %s\n", src.Kind, sizeString(cr.n), landed)
		return landed, nil
	default:
		return "", fmt.Errorf("empty source %q", src.Name)
	}
}
