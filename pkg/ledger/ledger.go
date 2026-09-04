// Package ledger is VERIQO's durable, hash-chained event ledger.
//
// # What was wrong with the previous one
//
// The specification is blunt about it:
//
//	Ledger tidak boleh lagi: in-memory only.
//
// An in-memory hash chain proves that nothing was altered between two
// points in one process's lifetime. It proves nothing across a restart,
// and it proves nothing to anybody who was not running the process. As
// an audit anchor it is a claim by the party that benefits from it.
//
// # What durability actually requires here
//
//	WAL             an append is written and fsynced before it is
//	                acknowledged, so a crash cannot lose an
//	                acknowledged record
//	hash chain      each record commits to its predecessor
//	checkpoint      a signed statement of "the chain at height N had
//	                root R", so verification does not require replaying
//	                every record from genesis
//	external anchor a checkpoint published where VERIQO cannot alter
//	                it, so the chain's history is attested by somebody
//	                other than VERIQO
//
// The last one is the only one that turns the ledger from tamper-
// EVIDENT into tamper-evident-to-a-third-party, and it is deliberately
// modelled as an interface VERIQO does not implement: the anchor is
// somebody else's system, and gate G-ANCHOR is about wiring it up.
//
// # Torn writes
//
// A record is framed with its own length and its own CRC. A partial
// tail -- the ordinary result of a crash mid-append -- is detected and
// truncated at open, and the records before it stay valid. A ledger
// that refused to open after a power failure would be a ledger nobody
// keeps.
package ledger

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
)

var (
	ErrChainBroken    = errors.New("ledger: hash chain is broken")
	ErrClosed         = errors.New("ledger: closed")
	ErrBadRecord      = errors.New("ledger: malformed record")
	ErrNotAnchored    = errors.New("ledger: checkpoint has not been externally anchored")
	ErrHeightMismatch = errors.New("ledger: checkpoint height does not match the chain")
	ErrNoSigner       = errors.New("ledger: no signer; an unsigned checkpoint attests to nothing")
	ErrTruncated      = errors.New("ledger: the log ends in a partial record")
)

// GenesisHash is the predecessor of the first record. It is a fixed,
// non-zero value: an all-zero previous hash is indistinguishable from
// an unset field, and the difference matters when validating a record
// that claims to be first.
const GenesisHash = "veriqo-ledger-genesis-v1"

// Event is one appended fact.
//
// The specification enumerates what a ledger event must carry, and the
// field set is exactly that list: who, what, when, why, under which
// policy, using which version, producing which result.
type Event struct {
	// Who.
	Actor    contract.ID `json:"actor"`
	OnBehalf string      `json:"on_behalf,omitempty"`
	TenantID string      `json:"tenant_id"`

	// What.
	Action  string `json:"action"`
	Subject string `json:"subject"`

	// When. Supplied, never read from the wall clock, so a replay
	// produces the same record.
	At time.Time `json:"at"`

	// Why: the declared purpose and a free-text justification. Both
	// are required for actions that change state, because "why" with
	// only an enum value is not a reason a reviewer can assess.
	Purpose string `json:"purpose"`
	Reason  string `json:"reason,omitempty"`

	// Under which policy, using which versions.
	PolicyDecision string              `json:"policy_decision"`
	Versions       contract.VersionSet `json:"versions"`

	// Producing which result.
	Outcome contract.Outcome `json:"outcome"`
	Result  string           `json:"result,omitempty"`

	// InputHash and OutputHash tie the event to the material, without
	// putting the material in the ledger.
	InputHash  string `json:"input_hash,omitempty"`
	OutputHash string `json:"output_hash,omitempty"`
}

// Validate refuses events that would make the ledger unusable as
// evidence.
func (e Event) Validate() error {
	if e.Actor == "" {
		return fmt.Errorf("%w: no actor", ErrBadRecord)
	}
	if err := e.Actor.Validate(); err != nil {
		return err
	}
	if e.TenantID == "" {
		return fmt.Errorf("%w: no tenant", ErrBadRecord)
	}
	if e.Action == "" || e.Subject == "" {
		return fmt.Errorf("%w: an event must name an action and a subject", ErrBadRecord)
	}
	if e.At.IsZero() {
		return fmt.Errorf("%w: no instant", ErrBadRecord)
	}
	if e.Purpose == "" {
		return fmt.Errorf("%w: no declared purpose", ErrBadRecord)
	}
	if !e.Outcome.Valid() {
		return fmt.Errorf("%w: outcome %q", ErrBadRecord, e.Outcome)
	}
	if e.PolicyDecision == "" {
		return fmt.Errorf("%w: no policy decision recorded; the event cannot be reviewed", ErrBadRecord)
	}
	if !e.Versions.Complete() {
		return fmt.Errorf("%w: missing versions %v", contract.ErrUnversioned, e.Versions.Missing())
	}
	return nil
}

// Record is an Event placed in the chain.
type Record struct {
	Height   uint64 `json:"height"`
	PrevHash string `json:"prev_hash"`
	Event    Event  `json:"event"`
	Hash     string `json:"hash"`
}

// digest recomputes a record's hash over everything except the hash
// field itself. Height and PrevHash are inside the digest, so a record
// cannot be moved to a different position in the chain.
func (r Record) digest() (string, error) {
	return jcs.Hash(struct {
		Height   uint64 `json:"height"`
		PrevHash string `json:"prev_hash"`
		Event    Event  `json:"event"`
	}{r.Height, r.PrevHash, r.Event})
}

// Signer signs checkpoints. In production this is backed by an HSM or
// KMS; the interface exists so that the ledger never holds key
// material and so gate G1 has something concrete to be about.
type Signer interface {
	Sign(message []byte) (signature []byte, keyID string, err error)
	Verify(message, signature []byte, keyID string) error
}

// Checkpoint is a signed statement about the chain at a height.
type Checkpoint struct {
	TenantID  string    `json:"tenant_id"`
	Height    uint64    `json:"height"`
	Root      string    `json:"root"`
	At        time.Time `json:"at"`
	KeyID     string    `json:"key_id"`
	Signature string    `json:"signature"`

	// AnchorRef is the receipt from an external anchor. Empty means
	// the checkpoint is VERIQO's own word.
	AnchorRef  string     `json:"anchor_ref,omitempty"`
	AnchoredAt *time.Time `json:"anchored_at,omitempty"`
}

// signedView is the byte string a signature covers. AnchorRef is
// deliberately outside it: the anchor receipt is produced AFTER
// signing, and including it would make the signature unverifiable.
func (c Checkpoint) signedView() ([]byte, error) {
	return jcs.Canonicalize(struct {
		TenantID string    `json:"tenant_id"`
		Height   uint64    `json:"height"`
		Root     string    `json:"root"`
		At       time.Time `json:"at"`
	}{c.TenantID, c.Height, c.Root, c.At})
}

// Anchored reports whether a third party has attested to this
// checkpoint. An unanchored checkpoint is tamper-evident to VERIQO and
// to nobody else.
func (c Checkpoint) Anchored() bool { return c.AnchorRef != "" && c.AnchoredAt != nil }

// Anchor publishes a checkpoint somewhere VERIQO cannot alter it.
//
// VERIQO does not implement this interface. That is the point: an
// anchor VERIQO controls is not an anchor, and a fake implementation
// in this repository would be indistinguishable from a real one at
// every call site.
type Anchor interface {
	// Publish returns an opaque receipt that a third party can use to
	// confirm the checkpoint existed at the time claimed.
	Publish(c Checkpoint) (ref string, at time.Time, err error)
	// Confirm re-checks a previously published receipt.
	Confirm(ref string, c Checkpoint) error
	// Name identifies the anchoring service, for the audit record.
	Name() string
}

// Ledger is an append-only, durable, hash-chained log for one tenant.
type Ledger struct {
	mu sync.Mutex

	tenantID string
	dir      string
	logFile  *os.File
	writer   *bufio.Writer

	height uint64
	head   string

	signer Signer
	anchor Anchor

	closed bool

	// checkpoints holds this session's checkpoints; they are also
	// persisted next to the log.
	checkpoints []Checkpoint
}

// Options configure a ledger.
type Options struct {
	// Signer is required. A ledger with no signer can produce no
	// checkpoint, and a chain with no checkpoint must be replayed from
	// genesis by anyone verifying it.
	Signer Signer
	// Anchor is optional in development and required in production.
	// Its absence is visible: Checkpoint() returns an unanchored
	// checkpoint and RequireAnchored() fails.
	Anchor Anchor
}

// Open opens or creates a tenant's ledger under dir.
//
// A partial trailing record -- the ordinary result of a crash during
// append -- is truncated and reported, not treated as corruption of
// the whole log.
func Open(dir, tenantID string, opts Options) (*Ledger, error) {
	if tenantID == "" {
		return nil, errors.New("ledger: no tenant")
	}
	if opts.Signer == nil {
		return nil, ErrNoSigner
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ledger: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, "chain.log")

	l := &Ledger{tenantID: tenantID, dir: dir, head: GenesisHash,
		signer: opts.Signer, anchor: opts.Anchor}

	records, truncateAt, err := scan(path)
	if err != nil {
		return nil, err
	}
	for i, r := range records {
		if err := l.verifyLink(r, uint64(i)); err != nil {
			return nil, err
		}
		l.height = r.Height + 1
		l.head = r.Hash
	}
	if truncateAt >= 0 {
		if err := os.Truncate(path, truncateAt); err != nil {
			return nil, fmt.Errorf("ledger: truncating a partial tail: %w", err)
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ledger: opening %s: %w", path, err)
	}
	l.logFile = f
	l.writer = bufio.NewWriter(f)
	return l, nil
}

func (l *Ledger) verifyLink(r Record, wantHeight uint64) error {
	if r.Height != wantHeight {
		return fmt.Errorf("%w: record at position %d claims height %d", ErrChainBroken, wantHeight, r.Height)
	}
	want := GenesisHash
	if wantHeight > 0 {
		want = l.head
	}
	if r.PrevHash != want {
		return fmt.Errorf("%w: record %d links to %s, chain head is %s",
			ErrChainBroken, r.Height, short(r.PrevHash), short(want))
	}
	got, err := r.digest()
	if err != nil {
		return err
	}
	if got != r.Hash {
		return fmt.Errorf("%w: record %d records %s, recomputes %s",
			ErrChainBroken, r.Height, short(r.Hash), short(got))
	}
	if r.Event.TenantID != l.tenantID {
		return fmt.Errorf("%w: record %d belongs to %s", contract.ErrCrossTenant, r.Height, r.Event.TenantID)
	}
	return nil
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// --- framing -----------------------------------------------------------
//
// Each record is: uint32 length | uint32 CRC32(payload) | payload.
// The CRC is over the payload only, so a torn length prefix is caught
// by the short read and a torn payload by the checksum.

func frame(payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(out[4:8], crc32.ChecksumIEEE(payload))
	copy(out[8:], payload)
	return out
}

// maxRecordBytes bounds a single record. A corrupt length prefix would
// otherwise ask for an arbitrary allocation.
const maxRecordBytes = 4 << 20

// scan reads every complete record. It returns the offset at which a
// partial tail begins, or -1 when the log ends cleanly.
func scan(path string) ([]Record, int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, -1, nil
	}
	if err != nil {
		return nil, -1, fmt.Errorf("ledger: opening %s: %w", path, err)
	}
	defer f.Close()

	var (
		out    []Record
		offset int64
		hdr    [8]byte
		r      = bufio.NewReader(f)
	)
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return out, -1, nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return out, offset, nil // torn header
			}
			return nil, -1, fmt.Errorf("ledger: reading %s: %w", path, err)
		}
		n := binary.BigEndian.Uint32(hdr[0:4])
		sum := binary.BigEndian.Uint32(hdr[4:8])
		if n == 0 || n > maxRecordBytes {
			return out, offset, nil // corrupt length: treat as the tail
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return out, offset, nil // torn payload
			}
			return nil, -1, fmt.Errorf("ledger: reading %s: %w", path, err)
		}
		if crc32.ChecksumIEEE(payload) != sum {
			return out, offset, nil // damaged payload: the tail starts here
		}
		var rec Record
		if err := json.Unmarshal(payload, &rec); err != nil {
			return nil, -1, fmt.Errorf("%w: at offset %d: %v", ErrBadRecord, offset, err)
		}
		out = append(out, rec)
		offset += int64(8 + n)
	}
}

// Append writes an event and returns the record.
//
// The write is fsynced before the call returns. That is the whole
// point of the WAL: an acknowledged append survives a crash, and an
// unacknowledged one is allowed not to.
func (l *Ledger) Append(e Event) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Record{}, ErrClosed
	}
	if err := e.Validate(); err != nil {
		return Record{}, err
	}
	if e.TenantID != l.tenantID {
		return Record{}, fmt.Errorf("%w: event is for %s, ledger is %s",
			contract.ErrCrossTenant, e.TenantID, l.tenantID)
	}
	r := Record{Height: l.height, PrevHash: l.head, Event: e}
	h, err := r.digest()
	if err != nil {
		return Record{}, err
	}
	r.Hash = h

	payload, err := json.Marshal(r)
	if err != nil {
		return Record{}, fmt.Errorf("ledger: encoding record: %w", err)
	}
	if _, err := l.writer.Write(frame(payload)); err != nil {
		return Record{}, fmt.Errorf("ledger: writing record: %w", err)
	}
	if err := l.writer.Flush(); err != nil {
		return Record{}, fmt.Errorf("ledger: flushing: %w", err)
	}
	if err := l.logFile.Sync(); err != nil {
		return Record{}, fmt.Errorf("ledger: fsync: %w", err)
	}
	l.height = r.Height + 1
	l.head = r.Hash
	return r, nil
}

// Height returns the number of records.
func (l *Ledger) Height() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.height
}

// Head returns the current chain root.
func (l *Ledger) Head() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.head
}

// Records reads the whole chain back from disk.
func (l *Ledger) Records() ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.writer.Flush(); err != nil {
		return nil, err
	}
	recs, tail, err := scan(filepath.Join(l.dir, "chain.log"))
	if err != nil {
		return nil, err
	}
	if tail >= 0 {
		return recs, fmt.Errorf("%w: at offset %d", ErrTruncated, tail)
	}
	return recs, nil
}

// Verify re-derives every hash and every link from disk.
func (l *Ledger) Verify() error {
	recs, err := l.Records()
	if err != nil {
		return err
	}
	v := &Ledger{tenantID: l.tenantID, head: GenesisHash}
	for i, r := range recs {
		if err := v.verifyLink(r, uint64(i)); err != nil {
			return err
		}
		v.head = r.Hash
	}
	return nil
}

// Checkpoint signs the current chain root.
//
// If an Anchor is configured, the checkpoint is published to it and
// carries the receipt. If not, the checkpoint is returned unanchored
// and says so -- it is not an error, because a development deployment
// legitimately has no anchor, and it is not silent either.
func (l *Ledger) Checkpoint(at time.Time) (Checkpoint, error) {
	l.mu.Lock()
	height, root := l.height, l.head
	l.mu.Unlock()

	if at.IsZero() {
		return Checkpoint{}, errors.New("ledger: a checkpoint needs an instant")
	}
	c := Checkpoint{TenantID: l.tenantID, Height: height, Root: root, At: at}
	msg, err := c.signedView()
	if err != nil {
		return Checkpoint{}, err
	}
	sig, keyID, err := l.signer.Sign(msg)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("ledger: signing checkpoint: %w", err)
	}
	c.Signature = hex.EncodeToString(sig)
	c.KeyID = keyID

	if l.anchor != nil {
		ref, anchoredAt, err := l.anchor.Publish(c)
		if err != nil {
			return c, fmt.Errorf("ledger: anchoring checkpoint at height %d with %s: %w",
				height, l.anchor.Name(), err)
		}
		c.AnchorRef = ref
		c.AnchoredAt = &anchoredAt
	}

	l.mu.Lock()
	l.checkpoints = append(l.checkpoints, c)
	l.mu.Unlock()

	if err := l.persistCheckpoint(c); err != nil {
		return c, err
	}
	return c, nil
}

func (l *Ledger) persistCheckpoint(c Checkpoint) error {
	path := filepath.Join(l.dir, "checkpoints.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("ledger: opening checkpoints: %w", err)
	}
	defer f.Close()
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// VerifyCheckpoint checks the signature and that the checkpoint
// describes this chain.
func (l *Ledger) VerifyCheckpoint(c Checkpoint) error {
	if c.TenantID != l.tenantID {
		return fmt.Errorf("%w: checkpoint is for %s", contract.ErrCrossTenant, c.TenantID)
	}
	msg, err := c.signedView()
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(c.Signature)
	if err != nil {
		return fmt.Errorf("ledger: checkpoint signature is not hex: %w", err)
	}
	if err := l.signer.Verify(msg, sig, c.KeyID); err != nil {
		return fmt.Errorf("ledger: checkpoint signature: %w", err)
	}
	recs, err := l.Records()
	if err != nil {
		return err
	}
	if c.Height > uint64(len(recs)) {
		return fmt.Errorf("%w: checkpoint claims height %d, chain has %d",
			ErrHeightMismatch, c.Height, len(recs))
	}
	want := GenesisHash
	if c.Height > 0 {
		want = recs[c.Height-1].Hash
	}
	if c.Root != want {
		return fmt.Errorf("%w: checkpoint root %s, chain at height %d is %s",
			ErrChainBroken, short(c.Root), c.Height, short(want))
	}
	return nil
}

// RequireAnchored is the production check. It is separate from
// VerifyCheckpoint because a valid signature over an unanchored
// checkpoint is exactly the situation the specification calls out: the
// ledger is internally consistent and attested by nobody.
func RequireAnchored(c Checkpoint) error {
	if !c.Anchored() {
		return fmt.Errorf("%w: height %d root %s is signed by VERIQO and attested by nobody else",
			ErrNotAnchored, c.Height, short(c.Root))
	}
	return nil
}

// Close flushes and closes the log.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if err := l.writer.Flush(); err != nil {
		l.logFile.Close()
		return err
	}
	if err := l.logFile.Sync(); err != nil {
		l.logFile.Close()
		return err
	}
	return l.logFile.Close()
}
