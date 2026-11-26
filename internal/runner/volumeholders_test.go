package runner

import (
	"reflect"
	"testing"
)

// The engine's volume filter is a candidate list, not the answer: docker's
// also matches a bind mount whose destination equals the name. Only a
// container whose own mounts name the volume holds it.
func TestParseVolumeHoldersKeepsOnlyRealMounts(t *testing.T) {
	out := "aaa|/byre-web-1|sha256:111|true|byre-web-1-.claude,byre-machine-u1000-zai-identity,\n" +
		"bbb|/other|sha256:222|false|unrelated,\n" +
		"ccc|byre-api-2|sha256:333|false|byre-machine-u1000-zai-identity,\n" +
		"garbage line\n"
	got := parseVolumeHolders(out, "byre-machine-u1000-zai-identity")
	want := []VolumeHolder{
		{ID: "aaa", Name: "byre-web-1", ImageID: "sha256:111", Running: true},
		{ID: "ccc", Name: "byre-api-2", ImageID: "sha256:333", Running: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("holders = %+v, want %+v", got, want)
	}
}
