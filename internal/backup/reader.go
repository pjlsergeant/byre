package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"

	"github.com/pjlsergeant/byre/internal/config"
)

// File is one verified backup file: its index, its config bytes, the
// credential state DERIVED from those bytes, and its volume payloads staged
// on disk with the report each one's validation produced.
//
// Nothing here is the index's word for anything except the references table.
// Every value a review prints as a fact about this restore comes from content
// that was length-checked, digest-checked and walked header by header; the
// index is the table of contents the file is checked AGAINST.
type File struct {
	Index  Index
	Config []byte
	// CredentialState is read off the config bytes, never off
	// Index.Config.Credentials. A file whose index misstates the state is
	// not refused: the review shows what the bytes say.
	CredentialState string
	Volumes         []StagedVolume
}

// StagedVolume is one carried volume, verified and staged: its logical name
// (the index row's, which the outer member name had to match exactly), the
// POSITIONAL staging member holding its bytes, and what validating it found.
type StagedVolume struct {
	Name   string
	Staged string
	Report Report
}

// Read bounds. The budget on the decompressed stream has two phases because
// the index is what declares the totals: before it is read, the only honest
// bound is the index's own (a megabyte, as for any config) plus room for the
// headers around it; after it is read, the bound is readBudget below.
//
// budgetPerEntry is three 512-byte blocks plus a block of content padding,
// rounded up. A member whose name needs a PAX path record -- which a
// 255-byte logical volume name does -- costs the extended header's block,
// the block its record is padded into, and the ordinary header block: 1536
// bytes, plus up to 511 bytes padding the member's own content. One kibibyte
// per member cannot cover a member the format itself produces: at that figure
// Write refuses to publish a backup of a couple of hundred long-named volumes,
// because it measures itself against this same budget. 4 KiB covers the worst
// case with room for a record that spills a block.
const (
	budgetSlack    = 64 << 10
	budgetPerEntry = 4 << 10
)

// readBudget is the decompressed size a backup file may have: the index's own
// measured length, the totals the index declares, slack for the archive's
// framing and trailer, and the per-member allowance above. Write stays inside
// the same figure and refuses before publishing, so a backup byre produced is
// one byre reads.
func readBudget(indexBytes, declared int64, members int) int64 {
	// Saturating, because every input is or derives from the FILE: a forged
	// index can declare totals that wrap the sum, and a wrapped (negative)
	// limit would admit everything and leave the remaining room negative.
	// Capped one below MaxInt64 so the budget's own limit+1 cannot overflow.
	const ceiling = math.MaxInt64 - 1
	total := int64(0)
	for _, part := range [...]int64{indexBytes, declared, budgetSlack, int64(members) * budgetPerEntry} {
		if part < 0 || part > ceiling-total {
			return ceiling
		}
		total += part
	}
	return total
}

// Read verifies a backup file end to end and stages its volume payloads.
//
// Every item below is a refusal with its rule named, and none of them trusts
// the file about anything it has not proved:
//
//   - gzip, one member only (Multistream(false)), with no bytes after it and
//     none after the last tar member inside it;
//   - the decompressed stream inside the budget above;
//   - members in exactly one order -- backup.toml, byre.config, then
//     volumes/<name>.tar for each index row IN ROW ORDER -- each a regular
//     file whose name, length and sha256 are the row's. A duplicate, an
//     extra, a missing or a reordered member fails one of those comparisons;
//   - the index parsed two-pass (a newer format names its minimum byre
//     version) and then checked (CheckIndex, against the DESTINATION's volume
//     prefix);
//   - the declared payload bytes must fit the staging filesystem before any
//     payload is written -- that reservation and the per-member length rule
//     are what bound what lands on disk;
//   - every staged payload walked under the nested-tar contract, its entry
//     count the row's.
//
// A refusal leaves whatever was staged where it is: staging is the caller's
// to Remove on every exit, success or failure, and a half-verified payload is
// not something to tidy away from inside the verification.
//
// prefix is the destination project's physical volume-name prefix
// (byre-<id>-): the logical names are checked against it before any of them
// is joined into a real volume name.
func Read(file io.Reader, st *Staging, prefix string) (*File, error) {
	if st == nil {
		return nil, errors.New("backup: no staging directory to verify into")
	}
	// bufio so the gzip reader reads through a buffer this function still
	// holds: that is what makes "is there anything after the gzip member?"
	// answerable at all, since gzip's own reader would otherwise have
	// swallowed the bytes.
	br := bufio.NewReader(file)
	zr, err := gzip.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("backup: not a gzip file: %w", err)
	}
	defer zr.Close()
	zr.Multistream(false)

	bud := &budget{r: zr, label: "index bound", limit: config.MaxConfigBytes + budgetSlack}
	tr := tar.NewReader(bud)

	raw, err := readMember(tr, IndexName, config.MaxConfigBytes)
	if err != nil {
		return nil, err
	}
	ix, err := ParseIndex(raw)
	if err != nil {
		return nil, err
	}
	if err := CheckIndex(ix, prefix); err != nil {
		return nil, fmt.Errorf("%s: %w", IndexName, err)
	}
	declared, err := declaredTotals(ix)
	if err != nil {
		return nil, err
	}
	if err := bud.setLimit("declared totals", readBudget(int64(len(raw)), declared, 2+len(ix.Volumes))); err != nil {
		return nil, err
	}

	cfg, err := readMember(tr, ConfigName, config.MaxConfigBytes)
	if err != nil {
		return nil, err
	}
	if int64(len(cfg)) != ix.Config.Bytes {
		return nil, fmt.Errorf("member %q is %d bytes but the index says %d", ConfigName, len(cfg), ix.Config.Bytes)
	}
	if got := DigestBytes(cfg); got != ix.Config.SHA256 {
		return nil, fmt.Errorf("member %q has sha256 %s but the index says %s", ConfigName, got, ix.Config.SHA256)
	}

	// Space before bytes: the shortfall is named BEFORE a payload is staged,
	// so a backup that cannot fit refuses instead of filling the disk and
	// then saying so. The figure is the PAYLOADS' declared bytes, which are
	// what lands in staging; the config is returned in memory.
	if err := checkSpace(st, declared-ix.Config.Bytes); err != nil {
		return nil, err
	}

	// What bounds the staged bytes is the pair above: checkSpace reserved the
	// declared payload total, and each member below must be exactly the length
	// its row states. A running total of what landed would restate their
	// identity, not add a bound.
	out := &File{Index: ix, Config: cfg}
	for i, row := range ix.Volumes {
		member := MemberName(row.Name)
		name := fmt.Sprintf("%d.tar", i)
		sum, err := stageMember(tr, st, name, member, row.Bytes)
		if err != nil {
			return nil, err
		}
		if sum != row.SHA256 {
			return nil, fmt.Errorf("member %q has sha256 %s but the index says %s", member, sum, row.SHA256)
		}
		out.Volumes = append(out.Volumes, StagedVolume{Name: row.Name, Staged: name})
	}

	// Nothing may follow the last member: not another tar entry, not loose
	// bytes inside the gzip member, not a second gzip member or any other
	// envelope around the one byre writes.
	if hdr, err := tr.Next(); err == nil {
		return nil, fmt.Errorf("unexpected extra member %q after the last volume", hdr.Name)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if n, err := io.Copy(io.Discard, bud); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	} else if n != 0 {
		return nil, fmt.Errorf("%d bytes follow the last member inside the archive", n)
	}
	if _, err := br.Peek(1); err == nil {
		return nil, errors.New("data follows the gzip member; a backup is one gzip stream and nothing else")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("backup: %w", err)
	}

	// The contract, read back out of staging. Last, so a payload is never
	// walked out of an archive that turned out to be malformed further on.
	for i := range out.Volumes {
		rep, err := validateStaged(st, out.Volumes[i].Staged)
		if err != nil {
			return nil, fmt.Errorf("volume %q: %w", out.Volumes[i].Name, err)
		}
		if rep.Entries != ix.Volumes[i].Entries {
			return nil, fmt.Errorf("volume %q holds %d entries but the index says %d", out.Volumes[i].Name, rep.Entries, ix.Volumes[i].Entries)
		}
		out.Volumes[i].Report = rep
	}

	// Derived, not trusted: the credential state is what the verified config
	// bytes hold. An index that misstates it is not a refusal -- the review
	// shows this, and the index's claim is not printed as a fact.
	state, err := CredentialState(cfg)
	if err != nil {
		return nil, fmt.Errorf("member %q: %w", ConfigName, err)
	}
	out.CredentialState = state
	return out, nil
}

// RebuildTo pours one verified volume: it reads the staged payload back and
// writes the REBUILT stream to w (the pour helper's stdin), returning that
// pass's report. The payload is walked a second time on purpose -- the pour
// replays what this pass accepts, entry by entry, so the bytes the helper
// extracts are the ones the contract just approved and never the file's own.
//
// Both of the second pass's checks must agree with what Read found: the whole
// report compared deeply (a swapped symlink target or a FIFO that became a
// regular file changes no count), and the sha256 of the staged bytes against
// the index row's. Staging is this invocation's private 0700 directory, so a
// difference means the payload changed under byre.
//
// The stream is written to w AS IT GOES, so neither check can fire before the
// writer has seen bytes -- a digest is only knowable at the end of a stream. A
// caller must therefore treat any error here as a failed pour and roll the
// volume back; restore.go's runPour/failed pair does.
func (f *File) RebuildTo(v StagedVolume, st *Staging, w io.Writer) (Report, error) {
	if st == nil {
		return Report{}, errors.New("backup: no staging directory to pour from")
	}
	row, ok := f.volumeRow(v.Name)
	if !ok {
		// Every StagedVolume Read produces comes from an index row, so this is
		// a caller handing RebuildTo a volume from another file.
		return Report{}, fmt.Errorf("volume %q has no index row to check the staged bytes against", v.Name)
	}
	src, err := st.Open(v.Staged)
	if err != nil {
		return Report{}, err
	}
	defer src.Close()
	h := NewDigest()
	tee := io.TeeReader(src, h)
	rep, err := Rebuild(tee, w)
	if err != nil {
		return rep, fmt.Errorf("volume %q: %w", v.Name, err)
	}
	if !reflect.DeepEqual(rep, v.Report) {
		return rep, fmt.Errorf("volume %q is not the payload byre verified: %s then, %s now", v.Name, describeReport(v.Report), describeReport(rep))
	}
	// The walk stops at the end-of-archive marker, and GNU tar pads the stream
	// out to its blocking factor past that. Those bytes are part of the member
	// the index hashed, so they go through the digest too.
	if _, err := io.Copy(io.Discard, tee); err != nil {
		return rep, fmt.Errorf("volume %q: %w", v.Name, err)
	}
	if got := HexDigest(h); got != row.SHA256 {
		return rep, fmt.Errorf("volume %q has sha256 %s now and the index says %s; the staged payload changed under byre", v.Name, got, row.SHA256)
	}
	return rep, nil
}

// volumeRow finds the index row for a staged volume by name. Index names are
// unique (CheckIndex refuses a repeat), so the first match is the only one.
func (f *File) volumeRow(name string) (Volume, bool) {
	for _, row := range f.Index.Volumes {
		if row.Name == name {
			return row, true
		}
	}
	return Volume{}, false
}

// describeReport renders the four numbers a report comparison turns on, for
// the refusal above: which of them moved is the useful half of the message.
func describeReport(rep Report) string {
	return fmt.Sprintf("%d entries, %d absolute and %d traversing symlinks, %d dropped",
		rep.Entries, len(rep.Absolute), len(rep.Traversing), len(rep.Dropped))
}

// readMember reads one whole outer member into memory, under limit.
func readMember(tr *tar.Reader, want string, limit int64) ([]byte, error) {
	hdr, err := nextMember(tr, want)
	if err != nil {
		return nil, err
	}
	if hdr.Size > limit {
		return nil, fmt.Errorf("member %q is %d bytes (limit %d)", want, hdr.Size, limit)
	}
	b, err := io.ReadAll(io.LimitReader(tr, limit+1))
	if err != nil {
		return nil, fmt.Errorf("member %q: %w", want, err)
	}
	if int64(len(b)) != hdr.Size {
		return nil, fmt.Errorf("member %q holds %d of its declared %d bytes", want, len(b), hdr.Size)
	}
	return b, nil
}

// stageMember streams one outer member into a POSITIONAL staging file,
// hashing on the way, and returns the digest of what landed. The member's
// length is the row's or nothing is returned at all, so a caller never has to
// re-derive how much was staged.
func stageMember(tr *tar.Reader, st *Staging, name, want string, size int64) (string, error) {
	hdr, err := nextMember(tr, want)
	if err != nil {
		return "", err
	}
	if hdr.Size != size {
		return "", fmt.Errorf("member %q is %d bytes but the index says %d", want, hdr.Size, size)
	}
	f, err := st.Create(name)
	if err != nil {
		return "", err
	}
	h := NewDigest()
	n, copyErr := io.Copy(io.MultiWriter(f, h), tr)
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return "", fmt.Errorf("member %q: %w", want, copyErr)
	case n != size:
		return "", fmt.Errorf("member %q holds %d of its declared %d bytes", want, n, size)
	case closeErr != nil:
		// Reported last and never swallowed: a close that fails is a write
		// that did not land, and the digest below would be of bytes the
		// staging file does not hold.
		return "", fmt.Errorf("member %q: %w", want, closeErr)
	}
	return HexDigest(h), nil
}

// nextMember advances to the member that must come next and refuses anything
// else. Order is enforced by asking for one name at a time: a duplicate, an
// extra, a missing or a reordered member all arrive here as the wrong name.
func nextMember(tr *tar.Reader, want string) (*tar.Header, error) {
	hdr, err := tr.Next()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the archive ends before member %q", want)
	}
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if hdr.Name != want {
		return nil, fmt.Errorf("expected member %q, found %q", want, hdr.Name)
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("member %q is not a regular file (type flag %q)", want, string(hdr.Typeflag))
	}
	return hdr, nil
}

// validateStaged walks one staged payload under the nested-tar contract.
func validateStaged(st *Staging, name string) (Report, error) {
	f, err := st.Open(name)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	return Validate(f)
}

// declaredTotals is the config plus every payload the index declares, with
// the addition checked: a forged index could otherwise name totals that
// wrap, and a negative budget admits everything.
func declaredTotals(ix Index) (int64, error) {
	total := ix.Config.Bytes
	for _, v := range ix.Volumes {
		if v.Bytes > math.MaxInt64-total {
			return 0, fmt.Errorf("%s: the declared byte totals do not add up to a real size", IndexName)
		}
		total += v.Bytes
	}
	return total, nil
}

// checkSpace refuses before any payload is staged when the staging
// filesystem cannot hold what the index declares, naming the shortfall.
func checkSpace(st *Staging, need int64) error {
	free, err := st.FreeBytes()
	if err != nil {
		return err
	}
	if need < 0 || uint64(need) > free {
		return fmt.Errorf("staging at %s has %d bytes free and this backup needs %d: short by %d", st.Path(), free, need, uint64(need)-free)
	}
	return nil
}

// budget bounds the DECOMPRESSED stream, which is the only size a gzip file
// does not declare: a few kilobytes of archive can ask a reader for terabytes
// of output. The limit is raised once, when the index has said what the
// members hold.
//
// The bound is HARD -- at most limit+1 bytes ever leave the underlying reader
// -- and it has to be, because archive/tar reads every header block with
// io.ReadFull, which DISCARDS a read's error whenever the bytes it got
// complete the buffer. A reader that merely reported the overrun alongside
// its bytes would be ignored once per header, and a stream of zero-length
// local PAX headers (which the reader consumes and loops past) would then
// expand without end from a few compressed kilobytes. So the read is
// truncated to the remaining room, and exhaustion is sticky: afterwards every
// Read returns no bytes and the refusal, which io.ReadFull cannot swallow
// because it never fills its buffer.
type budget struct {
	r     io.Reader
	label string
	limit int64
	n     int64
	over  bool
}

func (b *budget) Read(p []byte) (int, error) {
	if b.over || b.n > b.limit {
		b.over = true
		return 0, b.exceeded()
	}
	// One byte past the limit, so a stream landing exactly on it can still make
	// its final zero-byte read and see EOF -- the ordinary successful case. n <=
	// limit is established above and the limit is below MaxInt64, so room is at
	// least 1: Read never hands the underlying reader an empty buffer (which
	// would spin io.ReadAtLeast forever) and never slices with a negative bound.
	if room := b.limit + 1 - b.n; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := b.r.Read(p)
	b.n += int64(n)
	if b.n > b.limit {
		b.over = true
		return n, b.exceeded()
	}
	return n, err
}

// setLimit installs the second-phase limit. The new limit can be SMALLER
// than the first phase's -- a small archive padded with meta headers reaches
// the index having already spent more than its own index then says it may --
// and that is a refusal here rather than one read later, so it is reported
// where the limit is decided.
func (b *budget) setLimit(label string, limit int64) error {
	b.label, b.limit = label, limit
	if b.n > b.limit {
		b.over = true
		return b.exceeded()
	}
	return nil
}

func (b *budget) exceeded() error {
	return fmt.Errorf("the decompressed archive is over the %s (limit %d bytes)", b.label, b.limit)
}
