package packages

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fetch limits (D1h). Deny-by-default distribution: loosening later is
// additive.
const (
	MaxManifestBytes  = 256 << 10 // 256 KiB
	MaxPayloadTotal   = 64 << 20  // 64 MiB across all payloads of one install
	fetchTimeout      = 60 * time.Second
	maxRedirectsTotal = 5
)

// Fetcher retrieves manifests and payloads over https: and file:. The zero
// value is ready; tests inject Client (e.g. an httptest TLS client).
type Fetcher struct {
	Client *http.Client
}

// Source is a resolved manifest location: the raw URI plus what is needed to
// fetch siblings (payload sources are relative to the manifest ONLY, D5d).
type Source struct {
	URI string
	// https: origin ("https://host") payload URLs must stay within.
	origin string
	// https: directory URL payloads resolve against.
	baseURL *url.URL
	// file: directory (symlink-resolved) payloads are contained to.
	baseDir string
}

// IsRemote reports whether the source came over the network.
func (s *Source) IsRemote() bool { return s.origin != "" }

// ParseSourceURI classifies an install/inspect URI: https://... is remote;
// file://... and plain paths are local files. Other schemes are rejected.
func ParseSourceURI(raw string) (kind string, err error) {
	switch {
	case strings.HasPrefix(raw, "https://"):
		return "https", nil
	case strings.HasPrefix(raw, "http://"):
		return "", fmt.Errorf("http: is not supported (https only -- the manifest is trusted input)")
	case strings.HasPrefix(raw, "file://"):
		return "file", nil
	case strings.Contains(raw, "://"):
		return "", fmt.Errorf("unsupported scheme in %q (https:// or a file path)", raw)
	default:
		return "file", nil
	}
}

// FetchManifest retrieves manifest bytes (bounded, D1h) and returns the
// Source payloads later resolve against.
func (f *Fetcher) FetchManifest(raw string) ([]byte, *Source, error) {
	kind, err := ParseSourceURI(raw)
	if err != nil {
		return nil, nil, err
	}
	if kind == "file" {
		return f.fetchManifestFile(raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest uri: %w", err)
	}
	origin := "https://" + u.Host
	body, final, err := f.httpGet(u.String(), origin, MaxManifestBytes, "manifest")
	if err != nil {
		return nil, nil, err
	}
	// Payloads resolve relative to where the manifest WAS OBTAINED (D5d):
	// after same-origin redirects, the final response URL, not the request.
	base := *final
	base.Path = filepath.ToSlash(filepath.Dir(final.Path)) + "/"
	base.RawQuery, base.Fragment = "", ""
	return body, &Source{URI: raw, origin: origin, baseURL: &base}, nil
}

func (f *Fetcher) fetchManifestFile(raw string) ([]byte, *Source, error) {
	p, err := fileURIPath(raw)
	if err != nil {
		return nil, nil, err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if fi.Size() > MaxManifestBytes {
		return nil, nil, fmt.Errorf("manifest is %d bytes (limit %d)", fi.Size(), MaxManifestBytes)
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	// Contain payloads to the manifest's real directory (symlinks resolved).
	baseDir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, nil, err
	}
	return body, &Source{URI: raw, baseDir: baseDir}, nil
}

// FetchPayload retrieves one payload source relative to the manifest (D5d).
// budget is the remaining total-bytes allowance across the install; it is
// decremented by what was read.
func (f *Fetcher) FetchPayload(src *Source, rel string, budget *int64) ([]byte, error) {
	// Relative-only, and reject before resolution what can never be relative:
	// absolute URLs, other schemes, network-path references (//host/x).
	if strings.Contains(rel, "://") || strings.HasPrefix(rel, "//") {
		return nil, fmt.Errorf("payload src %q: absolute sources are rejected (relative to the manifest only)", rel)
	}
	if err := validRelPath(rel); err != nil {
		return nil, fmt.Errorf("payload src %q: %w", rel, err)
	}
	limit := *budget
	if limit <= 0 {
		return nil, fmt.Errorf("payload total exceeds the %d-byte budget", MaxPayloadTotal)
	}

	var body []byte
	var err error
	if src.baseDir != "" {
		body, err = f.fetchPayloadFile(src, rel, limit)
	} else {
		u := *src.baseURL
		ref, perr := url.Parse(rel)
		if perr != nil {
			return nil, fmt.Errorf("payload src %q: %w", rel, perr)
		}
		resolved := u.ResolveReference(ref)
		// Enforced after resolution, not syntax (D5d).
		if resolved.Scheme != "https" || "https://"+resolved.Host != src.origin {
			return nil, fmt.Errorf("payload src %q resolves outside the manifest origin %s", rel, src.origin)
		}
		body, _, err = f.httpGet(resolved.String(), src.origin, limit, "payload "+rel)
	}
	if err != nil {
		return nil, err
	}
	*budget -= int64(len(body))
	return body, nil
}

func (f *Fetcher) fetchPayloadFile(src *Source, rel string, limit int64) ([]byte, error) {
	p := filepath.Join(src.baseDir, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, fmt.Errorf("payload %s: %w", rel, err)
	}
	// Containment after symlink resolution (D5d).
	if real != src.baseDir && !strings.HasPrefix(real, src.baseDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("payload %s escapes the manifest directory", rel)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("payload %s is not a regular file", rel)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("payload %s is %d bytes (remaining budget %d)", rel, fi.Size(), limit)
	}
	return os.ReadFile(real)
}

// fileURIPath extracts the filesystem path from a file: URI or plain path.
// A real URL parse, not a prefix trim: file://localhost/x and file:///x both
// mean /x; any other host is rejected (a file: URI cannot name a remote).
func fileURIPath(raw string) (string, error) {
	if !strings.HasPrefix(raw, "file:") {
		return raw, nil // plain filesystem path
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("file uri: %w", err)
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("file uri %q: host %q is not local", raw, u.Host)
	}
	if u.Path == "" {
		return "", fmt.Errorf("file uri %q has no path", raw)
	}
	return filepath.FromSlash(u.Path), nil
}

// httpGet fetches a URL with the origin pinned across redirects (D5d), a
// bounded body size, and a bounded timeout (D1h). Returns the FINAL response
// URL so relative resolution follows same-origin redirects.
func (f *Fetcher) httpGet(rawURL, origin string, limit int64, what string) ([]byte, *url.URL, error) {
	base := f.Client
	if base == nil {
		base = http.DefaultClient
	}
	client := &http.Client{
		Transport: base.Transport,
		Jar:       base.Jar,
		Timeout:   fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirectsTotal {
				return fmt.Errorf("too many redirects")
			}
			// Redirects are re-validated against the origin (D5d).
			if req.URL.Scheme != "https" || "https://"+req.URL.Host != origin {
				return fmt.Errorf("redirect to %s leaves the manifest origin %s", req.URL, origin)
			}
			return nil
		},
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("fetch %s: %s", what, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", what, err)
	}
	if int64(len(body)) > limit {
		return nil, nil, fmt.Errorf("fetch %s: exceeds the %d-byte limit", what, limit)
	}
	return body, resp.Request.URL, nil
}
