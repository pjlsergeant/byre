package packages

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tlsFetcher(t *testing.T, mux http.Handler) (*Fetcher, string) {
	t.Helper()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return &Fetcher{Client: srv.Client()}, srv.URL
}

func TestFetchManifestAndPayloadHTTPS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pkg/skill.toml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[package]\nid = \"pete/x\"\n"))
	})
	mux.HandleFunc("/pkg/hooks/a.sh", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/bin/sh\n"))
	})
	f, base := tlsFetcher(t, mux)

	body, src, err := f.FetchManifest(base + "/pkg/skill.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "pete/x") || !src.IsRemote() {
		t.Fatalf("body=%q remote=%v", body, src.IsRemote())
	}
	budget := int64(MaxPayloadTotal)
	pay, err := f.FetchPayload(src, "hooks/a.sh", &budget)
	if err != nil {
		t.Fatal(err)
	}
	if string(pay) != "#!/bin/sh\n" || budget != MaxPayloadTotal-int64(len(pay)) {
		t.Fatalf("pay=%q budget=%d", pay, budget)
	}
}

func TestFetchPayloadRejectsEscapes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pkg/skill.toml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })
	f, base := tlsFetcher(t, mux)
	_, src, err := f.FetchManifest(base + "/pkg/skill.toml")
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(MaxPayloadTotal)
	// Each case pins ITS rejection message: a 404 from the stub server (or
	// any other stand-in error) must not pass for the path guard.
	for _, c := range []struct{ rel, want string }{
		{"https://evil.example/x", "absolute sources are rejected"},
		{"//evil.example/x", "absolute sources are rejected"},
		{"/abs/path", "must be relative"},
		{"../outside", "escapes the package"},
		{"a/../../b", "not clean"},
		{"", "empty path"},
		{"a\\b", "use '/' separators"},
	} {
		_, err := f.FetchPayload(src, c.rel, &budget)
		if err == nil {
			t.Errorf("payload src %q must be rejected", c.rel)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("payload src %q: rejected for the wrong reason: %v (want %q)", c.rel, err, c.want)
		}
	}
}

func TestFetchRedirectLeavingOriginRejected(t *testing.T) {
	evil := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("evil"))
	}))
	defer evil.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/pkg/skill.toml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })
	mux.HandleFunc("/pkg/leave", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/x", http.StatusFound)
	})
	f, base := tlsFetcher(t, mux)
	_, src, err := f.FetchManifest(base + "/pkg/skill.toml")
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(MaxPayloadTotal)
	if _, err := f.FetchPayload(src, "leave", &budget); err == nil ||
		!strings.Contains(err.Error(), "origin") {
		t.Fatalf("cross-origin redirect must be rejected, got %v", err)
	}
}

func TestFetchManifestSizeLimit(t *testing.T) {
	mux := http.NewServeMux()
	big := strings.Repeat("x", MaxManifestBytes+1)
	mux.HandleFunc("/pkg/skill.toml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(big)) })
	f, base := tlsFetcher(t, mux)
	if _, _, err := f.FetchManifest(base + "/pkg/skill.toml"); err == nil {
		t.Fatal("oversized manifest must be rejected")
	}
}

func TestFetchFileManifestAndContainment(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "skill.toml"), []byte("[package]\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "hooks"), 0o755)
	os.WriteFile(filepath.Join(dir, "hooks", "a.sh"), []byte("hi"), 0o755)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "link"))

	var f Fetcher
	_, src, err := f.FetchManifest(filepath.Join(dir, "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if src.IsRemote() {
		t.Fatal("file source must not be remote")
	}
	budget := int64(MaxPayloadTotal)
	if pay, err := f.FetchPayload(src, "hooks/a.sh", &budget); err != nil || string(pay) != "hi" {
		t.Fatalf("pay=%q err=%v", pay, err)
	}
	// Symlink escaping the manifest dir is rejected after resolution.
	if _, err := f.FetchPayload(src, "link", &budget); err == nil {
		t.Fatal("symlink escape must be rejected")
	}
}

func TestParseSourceURI(t *testing.T) {
	if _, err := ParseSourceURI("http://x/skill.toml"); err == nil {
		t.Fatal("http must be rejected")
	}
	if _, err := ParseSourceURI("ftp://x/skill.toml"); err == nil {
		t.Fatal("ftp must be rejected")
	}
	for raw, want := range map[string]string{
		"https://x/skill.toml":  "https",
		"file:///x/skill.toml":  "file",
		"./skill.toml":          "file",
		"/abs/pkg/skill.toml":   "file",
		"relative/template.cfg": "file",
	} {
		got, err := ParseSourceURI(raw)
		if err != nil || got != want {
			t.Errorf("ParseSourceURI(%q) = %q, %v", raw, got, err)
		}
	}
}

func TestFetchManifestRedirectRebasesPayloads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old/skill.toml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new/v2/skill.toml", http.StatusFound)
	})
	mux.HandleFunc("/new/v2/skill.toml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("m")) })
	mux.HandleFunc("/new/v2/hook.sh", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("new")) })
	mux.HandleFunc("/old/hook.sh", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OLD")) })
	f, base := tlsFetcher(t, mux)
	_, src, err := f.FetchManifest(base + "/old/skill.toml")
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(MaxPayloadTotal)
	pay, err := f.FetchPayload(src, "hook.sh", &budget)
	if err != nil {
		t.Fatal(err)
	}
	// Relative to where the manifest WAS OBTAINED (post-redirect).
	if string(pay) != "new" {
		t.Fatalf("payload must resolve against the final manifest URL, got %q", pay)
	}
}

func TestFileURIPath(t *testing.T) {
	for raw, want := range map[string]string{
		"file:///tmp/pkg/skill.toml":          "/tmp/pkg/skill.toml",
		"file://localhost/tmp/pkg/skill.toml": "/tmp/pkg/skill.toml",
		"/plain/path/skill.toml":              "/plain/path/skill.toml",
	} {
		got, err := fileURIPath(raw)
		if err != nil || got != want {
			t.Errorf("fileURIPath(%q) = %q, %v (want %q)", raw, got, err, want)
		}
	}
	if _, err := fileURIPath("file://evil.example/x"); err == nil {
		t.Fatal("non-local file host must be rejected")
	}
}
