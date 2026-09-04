// Package custody is the VERIQO chain of custody.
//
// # What custody answers that provenance does not
//
// Provenance says where material came from. Custody says who has held
// it since, and whether it changed while they did. They are separate
// because they fail separately: impeccable provenance with a broken
// custody chain describes evidence that was genuine when acquired and
// cannot be shown to be genuine now.
//
// # The one rule
//
// Every transfer records the digest of what was handed over. A link
// whose received digest differs from the previous link's released
// digest is a BREAK, and a break is permanent -- it is not repaired by
// a later link agreeing with itself. Code that recomputed the chain
// from the current bytes would report every chain as intact, which is
// the failure mode this package exists to make impossible.
//
// # Why a break is not an error return
//
// A broken chain is a FINDING about the evidence, not a malfunction of
// the software. It has to be recordable, reportable and citable --
// the material may still be usable, with the break stated as a
// limitation, and that is a decision for a reviewer rather than for
// this package.
package custody

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
)

var (
	ErrNoHolder     = errors.New("custody: a link names no holder")
	ErrNoDigest     = errors.New("custody: a link records no digest")
	ErrTimeInverted = errors.New("custody: a link precedes the one it received from")
	ErrEmptyChain   = errors.New("custody: an empty chain establishes nothing")
	ErrSealed       = errors.New("custody: the chain is sealed")
)

// Action is what a holder did.
type Action string

const (
	Acquired    Action = "ACQUIRED"
	Stored      Action = "STORED"
	Transferred Action = "TRANSFERRED"
	Processed   Action = "PROCESSED"
	Copied      Action = "COPIED"
	Disclosed   Action = "DISCLOSED"
	Sealed      Action = "SEALED"
)

func (a Action) Valid() bool {
	switch a {
	case Acquired, Stored, Transferred, Processed, Copied, Disclosed, Sealed:
		return true
	}
	return false
}

// MayChangeContent reports whether this action legitimately alters the
// material. Only PROCESSED does, and a PROCESSED link must produce a
// new evidence VERSION rather than changing one in place -- the digest
// it releases is the new version's.
func (a Action) MayChangeContent() bool { return a == Processed }

// Link is one custody event.
type Link struct {
	HolderID string    `json:"holder_id"`
	Action   Action    `json:"action"`
	At       time.Time `json:"at"`

	// ReceivedDigest is what the holder took possession of.
	// ReleasedDigest is what they passed on. They differ only for
	// PROCESSED, and then only because a new version was produced.
	ReceivedDigest string `json:"received_digest"`
	ReleasedDigest string `json:"released_digest"`

	// Note states what was done. Required for PROCESSED and DISCLOSED:
	// the two actions where "what happened" is not implied by the verb.
	Note string `json:"note,omitempty"`

	// Authorization names the policy decision that permitted the
	// action. A custody link with no authorisation records that
	// something happened and not that it was allowed.
	Authorization string `json:"authorization"`
}

func (l Link) Validate() error {
	if strings.TrimSpace(l.HolderID) == "" {
		return ErrNoHolder
	}
	if !l.Action.Valid() {
		return fmt.Errorf("custody: unknown action %q", l.Action)
	}
	if l.At.IsZero() {
		return fmt.Errorf("custody: %s/%s has no instant", l.HolderID, l.Action)
	}
	if len(l.ReceivedDigest) != 64 || len(l.ReleasedDigest) != 64 {
		return fmt.Errorf("%w: %s/%s", ErrNoDigest, l.HolderID, l.Action)
	}
	if strings.TrimSpace(l.Authorization) == "" {
		return fmt.Errorf("custody: %s/%s records no authorisation; the link says something "+
			"happened, not that it was allowed", l.HolderID, l.Action)
	}
	if !l.Action.MayChangeContent() && l.ReceivedDigest != l.ReleasedDigest {
		return fmt.Errorf("custody: %s performed %s and the digest changed; only PROCESSED "+
			"may alter content, and it must produce a new evidence version",
			l.HolderID, l.Action)
	}
	if (l.Action == Processed || l.Action == Disclosed) && strings.TrimSpace(l.Note) == "" {
		return fmt.Errorf("custody: %s performed %s without saying what was done",
			l.HolderID, l.Action)
	}
	return nil
}

// Break is a discontinuity in the chain.
type Break struct {
	AtIndex  int    `json:"at_index"`
	From     string `json:"from"`
	To       string `json:"to"`
	Expected string `json:"expected"`
	Received string `json:"received"`
	Reason   string `json:"reason"`
}

func (b Break) String() string {
	return fmt.Sprintf("link %d: %s released %s, %s received %s (%s)",
		b.AtIndex, b.From, shortD(b.Expected), b.To, shortD(b.Received), b.Reason)
}

func shortD(d string) string {
	if len(d) <= 12 {
		return d
	}
	return d[:12]
}

// Chain is the custody history of one evidence version.
type Chain struct {
	EvidenceVersionID contract.ID `json:"evidence_version_id"`
	TenantID          string      `json:"tenant_id"`
	Links             []Link      `json:"links"`
	sealed            bool
}

// New starts a chain with the acquisition link.
func New(versionID contract.ID, tenantID string, acquisition Link) (*Chain, error) {
	if err := versionID.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("custody: not anchored to a tenant")
	}
	if err := acquisition.Validate(); err != nil {
		return nil, err
	}
	if acquisition.Action != Acquired {
		return nil, fmt.Errorf("custody: a chain begins with ACQUIRED, not %s", acquisition.Action)
	}
	return &Chain{EvidenceVersionID: versionID, TenantID: tenantID,
		Links: []Link{acquisition}}, nil
}

// Append adds a link.
//
// It does NOT refuse a link whose received digest disagrees with the
// previous released digest. A break is a fact about the evidence and
// has to be recordable: refusing it here would mean the only way to
// represent a broken chain is not to record the link at all, which
// hides exactly what matters.
func (c *Chain) Append(l Link) error {
	if c.sealed {
		return ErrSealed
	}
	if err := l.Validate(); err != nil {
		return err
	}
	last := c.Links[len(c.Links)-1]
	if l.At.Before(last.At) {
		return fmt.Errorf("%w: %s at %s follows %s at %s",
			ErrTimeInverted, l.HolderID, l.At.Format(time.RFC3339),
			last.HolderID, last.At.Format(time.RFC3339))
	}
	c.Links = append(c.Links, l)
	return nil
}

// Seal closes the chain. A sealed chain is what an export or a
// production carries: nothing more can be added to it, so the digest a
// recipient verifies is the digest the chain ends on.
func (c *Chain) Seal(holder, authorization string, at time.Time) error {
	if c.sealed {
		return ErrSealed
	}
	last := c.Links[len(c.Links)-1]
	l := Link{HolderID: holder, Action: Sealed, At: at,
		ReceivedDigest: last.ReleasedDigest, ReleasedDigest: last.ReleasedDigest,
		Authorization: authorization}
	if err := c.Append(l); err != nil {
		return err
	}
	c.sealed = true
	return nil
}

func (c *Chain) IsSealed() bool { return c.sealed }

// Breaks returns every discontinuity, in order.
//
// It reads the RECORDED digests. An implementation that recomputed
// them from the current bytes would report every chain as intact,
// because the current bytes are by definition consistent with
// themselves.
func (c *Chain) Breaks() []Break {
	var out []Break
	for i := 1; i < len(c.Links); i++ {
		prev, cur := c.Links[i-1], c.Links[i]
		if cur.ReceivedDigest == prev.ReleasedDigest {
			continue
		}
		out = append(out, Break{
			AtIndex: i, From: prev.HolderID, To: cur.HolderID,
			Expected: prev.ReleasedDigest, Received: cur.ReceivedDigest,
			Reason: "the material a holder received is not the material the previous " +
				"holder released",
		})
	}
	return out
}

// Intact reports whether the chain has no breaks.
func (c *Chain) Intact() bool { return len(c.Breaks()) == 0 }

// Verify checks the chain and returns the breaks as an error when
// there are any, for callers that want fail-closed behaviour.
//
// Callers that want to RECORD a break and continue use Breaks()
// instead. Both are legitimate and the choice belongs to the caller,
// not to this package.
func (c *Chain) Verify() error {
	if err := c.structure(); err != nil {
		return err
	}
	breaks := c.Breaks()
	if len(breaks) == 0 {
		return nil
	}
	msgs := make([]string, len(breaks))
	for i, b := range breaks {
		msgs[i] = b.String()
	}
	return fmt.Errorf("custody: chain for %s is broken: %s",
		c.EvidenceVersionID, strings.Join(msgs, "; "))
}

func (c *Chain) structure() error {
	if len(c.Links) == 0 {
		return ErrEmptyChain
	}
	for _, l := range c.Links {
		if err := l.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// CurrentHolder is who has it now.
func (c *Chain) CurrentHolder() string {
	return c.Links[len(c.Links)-1].HolderID
}

// CurrentDigest is what they hold.
func (c *Chain) CurrentDigest() string {
	return c.Links[len(c.Links)-1].ReleasedDigest
}

// Holders lists everyone who has held the material, in order and
// without repetition.
func (c *Chain) Holders() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range c.Links {
		if !seen[l.HolderID] {
			seen[l.HolderID] = true
			out = append(out, l.HolderID)
		}
	}
	return out
}

// Limitation renders the chain's state as a sentence for a findings
// limitations section. An intact chain returns "", because a
// limitation that is always present is noise.
func (c *Chain) Limitation() string {
	breaks := c.Breaks()
	if len(breaks) == 0 {
		return ""
	}
	msgs := make([]string, len(breaks))
	for i, b := range breaks {
		msgs[i] = b.String()
	}
	return fmt.Sprintf("the chain of custody for %s is broken in %d place(s): %s. "+
		"The material may still be usable; it can no longer be shown to be unaltered "+
		"since acquisition.",
		c.EvidenceVersionID, len(breaks), strings.Join(msgs, "; "))
}

// Digest is the chain's own hash.
func (c *Chain) Digest() (string, error) {
	if err := c.structure(); err != nil {
		return "", err
	}
	return jcs.Hash(struct {
		EvidenceVersionID contract.ID `json:"evidence_version_id"`
		TenantID          string      `json:"tenant_id"`
		Links             []Link      `json:"links"`
	}{c.EvidenceVersionID, c.TenantID, c.Links})
}
