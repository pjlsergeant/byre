package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
)

// NewDigest is the running hash a payload is streamed through on its way into
// staging. The index's digest spelling is lowercase hex sha256, and this
// package owns it for both ends of the file and for the verbs that stage
// payloads -- not shared with internal/packages' identical computation, because
// this one is part of THIS format and moves only when the format does.
func NewDigest() hash.Hash { return sha256.New() }

// HexDigest spells a running hash's sum the way the index carries it.
func HexDigest(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// DigestBytes is the whole-content form, for the config, which is small
// enough to hold.
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Payload is one carried volume's bytes on their way into the file: the
// logical volume name, the length the index declares for it, and a function
// that opens the staged tar. Open is called exactly once, and the caller
// keeps nothing open until Write asks.
type Payload struct {
	Name string
	Size int64
	Open func() (io.ReadCloser, error)
}

// Write publishes the backup file at out: one gzip-compressed tar holding the
// index, the config and one plain tar per carried volume, in that order, at
// mode 0600 through hostopen's streaming exclusive publish. An existing entry
// of any kind at out is a refusal (fs.ErrExist), never an overwrite; every
// failure before the link publishes nothing; the one failure after it is
// hostopen.ErrPublishedUnsynced, returned as it comes so errors.Is works --
// the file is complete and only its durability is unconfirmed.
//
// Before a byte is written, Write runs the bounds the READER enforces, so the
// refusal arrives before anything is published: the index's own rules
// (CheckIndex, against the source project's volume-name prefix), the config
// bound, and a payload list that matches the index row for row. The streaming
// checks finish the job -- each payload's length and digest must be what its
// row says -- and they run inside the publish, where a mismatch still publishes
// nothing.
//
// prefix is the source project's physical volume-name prefix (byre-<id>-).
// The reader checks the index against the DESTINATION's prefix; backup checks
// it against its own, so a name only the destination's longer prefix would
// overflow is the destination's refusal to make, not a file byre refuses to
// write.
//
// beforeLink is the caller's last word before the link, passed through to the
// publisher (which documents the window): nil for a caller with nothing left
// to ask, and for a cancellable one the cancellation, so a Ctrl-C that landed
// while the staged file was being fsynced publishes nothing instead of landing
// a file the caller has already given up on.
func Write(out, prefix string, ix Index, cfg []byte, payloads []Payload, beforeLink func() error) error {
	if err := CheckIndex(ix, prefix); err != nil {
		return fmt.Errorf("%s: %w", IndexName, err)
	}
	if int64(len(cfg)) > config.MaxConfigBytes {
		return fmt.Errorf("%s is %d bytes (limit %d)", ConfigName, len(cfg), config.MaxConfigBytes)
	}
	if int64(len(cfg)) != ix.Config.Bytes {
		return fmt.Errorf("%s is %d bytes but the index says %d", ConfigName, len(cfg), ix.Config.Bytes)
	}
	// The config is in hand and under a megabyte, so the one digest the reader
	// will check is checked here too: finding out at restore that the index
	// disagrees with its own config member is finding out too late.
	if got := DigestBytes(cfg); got != ix.Config.SHA256 {
		return fmt.Errorf("%s has sha256 %s but the index says %s", ConfigName, got, ix.Config.SHA256)
	}
	raw := ix.Render()
	if int64(len(raw)) > config.MaxConfigBytes {
		return fmt.Errorf("%s renders to %d bytes (limit %d)", IndexName, len(raw), config.MaxConfigBytes)
	}
	if len(payloads) != len(ix.Volumes) {
		return fmt.Errorf("%d payloads for %d index volume rows", len(payloads), len(ix.Volumes))
	}
	for i, p := range payloads {
		row := ix.Volumes[i]
		if p.Name != row.Name {
			return fmt.Errorf("payload %d is volume %q but index row %d is %q", i, p.Name, i, row.Name)
		}
		if p.Size != row.Bytes {
			return fmt.Errorf("volume %q is %d bytes but the index says %d", p.Name, p.Size, row.Bytes)
		}
		if p.Open == nil {
			return fmt.Errorf("volume %q has no payload to open", p.Name)
		}
	}
	declared, err := declaredTotals(ix)
	if err != nil {
		return err
	}
	return hostopen.PublishStreamExclusive(out, 0o600, func(w io.Writer) error {
		return writeArchive(w, raw, cfg, payloads, ix.Volumes, declared)
	}, beforeLink)
}

// writeArchive spells the outer archive: gzip around a tar whose members are
// the index, the config and the payloads, in the fixed order the reader
// demands. PAX is asked for so a long volume name rides a path record rather
// than being truncated (Go's writer spells a member that needs no record as
// plain USTAR, which is what FormatPAX means to it). Member mtimes are zero:
// the file's own timestamps are its filesystem's business, and a zero makes
// one content one archive.
//
// The decompressed bytes are counted as they go and compared, before the
// gzip stream is finished, to the budget a READER allows: that is the
// reconciliation between what this writes and what Read accepts, measured
// rather than argued. Over the budget, nothing is published.
func writeArchive(w io.Writer, raw, cfg []byte, payloads []Payload, rows []Volume, declared int64) error {
	zw := gzip.NewWriter(w)
	counted := &Counter{W: zw}
	tw := tar.NewWriter(counted)
	if err := writeMember(tw, IndexName, int64(len(raw))); err != nil {
		return err
	}
	if _, err := tw.Write(raw); err != nil {
		return err
	}
	if err := writeMember(tw, ConfigName, int64(len(cfg))); err != nil {
		return err
	}
	if _, err := tw.Write(cfg); err != nil {
		return err
	}
	for i, p := range payloads {
		if err := writePayload(tw, p, rows[i]); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if limit := readBudget(int64(len(raw)), declared, 2+len(payloads)); counted.N > limit {
		return fmt.Errorf("the archive decompresses to %d bytes, over the %d a reader allows", counted.N, limit)
	}
	return zw.Close()
}

// Counter tallies the bytes that pass through it: a length measured rather
// than asked for. Both ends of the format need one -- writeArchive meters the
// decompressed size a reader will meter, and a capture meters the payload
// bytes it stages before the index declares them.
type Counter struct {
	W io.Writer
	N int64
}

func (c *Counter) Write(p []byte) (int, error) {
	n, err := c.W.Write(p)
	c.N += int64(n)
	return n, err
}

// writePayload streams one volume payload into the archive, hashing and
// counting on the way: the length and digest the index states are what the
// bytes actually are, or nothing is published. The payload was hashed in
// staging before the index was rendered, so a mismatch here is byre
// disagreeing with itself -- which is exactly the condition worth refusing
// before the file exists.
func writePayload(tw *tar.Writer, p Payload, row Volume) error {
	if err := writeMember(tw, MemberName(p.Name), row.Bytes); err != nil {
		return err
	}
	rc, err := p.Open()
	if err != nil {
		return fmt.Errorf("volume %q: %w", p.Name, err)
	}
	defer rc.Close()
	h := NewDigest()
	n, err := io.Copy(io.MultiWriter(tw, h), rc)
	if err != nil {
		return fmt.Errorf("volume %q: %w", p.Name, err)
	}
	if n != row.Bytes {
		return fmt.Errorf("volume %q streamed %d bytes but the index says %d", p.Name, n, row.Bytes)
	}
	if got := HexDigest(h); got != row.SHA256 {
		return fmt.Errorf("volume %q has sha256 %s but the index says %s", p.Name, got, row.SHA256)
	}
	return nil
}

// writeMember emits one outer member header.
func writeMember(tw *tar.Writer, name string, size int64) error {
	return tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Size:     size,
		Mode:     0o600,
		Format:   tar.FormatPAX,
	})
}

// MemberName is the outer member a logical volume name rides: the ONE place
// the volumes/<name>.tar spelling lives, so the writer and the reader cannot
// drift. Spelled by concatenation rather than path.Join, which Cleans: the
// reader compares member names byte for byte, so the writer must not be able
// to normalise one into a different string than the comparison expects.
func MemberName(volume string) string {
	return VolumesDir + "/" + volume + ".tar"
}
