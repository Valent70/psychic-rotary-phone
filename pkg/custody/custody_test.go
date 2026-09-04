package custody

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func at(h int) time.Time { return time.Date(2026, 9, 4, h, 0, 0, 0, time.UTC) }

func d(c byte) string { return strings.Repeat(string(c), 64) }

func acquisition() Link {
	return Link{HolderID: "ingest-service", Action: Acquired, At: at(9),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'),
		Authorization: "PERMIT baseline/clearance"}
}

func chain(t *testing.T) *Chain {
	t.Helper()
	c, err := New("evidenceversion:e1v1", "t-acme", acquisition())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestABreakIsDetectedFromTheRecordedDigests.
//
// This is the whole package. An implementation that recomputed digests
// from the current bytes would report every chain as intact, because
// the current bytes are consistent with themselves by definition.
func TestABreakIsDetectedFromTheRecordedDigests(t *testing.T) {
	c := chain(t)
	if err := c.Append(Link{HolderID: "vault", Action: Stored, At: at(10),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"}); err != nil {
		t.Fatal(err)
	}
	// The next holder received something else.
	if err := c.Append(Link{HolderID: "analyst-1", Action: Copied, At: at(11),
		ReceivedDigest: d('b'), ReleasedDigest: d('b'), Authorization: "PERMIT"}); err != nil {
		t.Fatal(err)
	}
	if c.Intact() {
		t.Fatal("A BROKEN CHAIN REPORTED AS INTACT")
	}
	breaks := c.Breaks()
	if len(breaks) != 1 {
		t.Fatalf("Breaks() = %v", breaks)
	}
	if breaks[0].From != "vault" || breaks[0].To != "analyst-1" {
		t.Fatalf("the break names %s -> %s", breaks[0].From, breaks[0].To)
	}
}

// TestABreakIsRecordableNotRefused. A broken chain is a finding about
// the evidence; refusing the link would mean the only representation
// of a break is the absence of a record.
func TestABreakIsRecordableNotRefused(t *testing.T) {
	c := chain(t)
	err := c.Append(Link{HolderID: "analyst-1", Action: Copied, At: at(10),
		ReceivedDigest: d('b'), ReleasedDigest: d('b'), Authorization: "PERMIT"})
	if err != nil {
		t.Fatalf("a link recording a break was refused: %v", err)
	}
	if len(c.Links) != 2 {
		t.Fatal("the link was not recorded")
	}
	// And Verify, for callers that want fail-closed behaviour, reports it.
	if err := c.Verify(); err == nil {
		t.Fatal("Verify accepted a broken chain")
	}
}

// TestABreakIsPermanent. Later links agreeing with themselves must not
// heal it.
func TestABreakIsPermanent(t *testing.T) {
	c := chain(t)
	c.Append(Link{HolderID: "analyst-1", Action: Copied, At: at(10),
		ReceivedDigest: d('b'), ReleasedDigest: d('b'), Authorization: "PERMIT"})
	for i := 11; i < 16; i++ {
		if err := c.Append(Link{HolderID: "holder-" + string(rune('a'+i)), Action: Stored,
			At: at(i), ReceivedDigest: d('b'), ReleasedDigest: d('b'),
			Authorization: "PERMIT"}); err != nil {
			t.Fatal(err)
		}
	}
	if c.Intact() {
		t.Fatal("A BREAK WAS HEALED by later links agreeing with themselves")
	}
	if len(c.Breaks()) != 1 {
		t.Fatalf("the break count changed to %d", len(c.Breaks()))
	}
}

// TestOnlyProcessedMayChangeTheDigest.
func TestOnlyProcessedMayChangeTheDigest(t *testing.T) {
	for _, a := range []Action{Stored, Transferred, Copied, Disclosed, Acquired} {
		l := Link{HolderID: "h", Action: a, At: at(10),
			ReceivedDigest: d('a'), ReleasedDigest: d('b'),
			Authorization: "PERMIT", Note: "note"}
		if err := l.Validate(); err == nil {
			t.Errorf("%s changed the digest and was accepted", a)
		}
	}
	ok := Link{HolderID: "h", Action: Processed, At: at(10),
		ReceivedDigest: d('a'), ReleasedDigest: d('b'),
		Authorization: "PERMIT", Note: "redaction produced version 2"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("PROCESSED with a changed digest was refused: %v", err)
	}
}

// TestProcessingIsNotABreak. The digest changes legitimately, so the
// chain must not report a discontinuity where a new version was made.
func TestProcessingIsNotABreak(t *testing.T) {
	c := chain(t)
	if err := c.Append(Link{HolderID: "redactor", Action: Processed, At: at(10),
		ReceivedDigest: d('a'), ReleasedDigest: d('b'),
		Authorization: "PERMIT", Note: "redaction produced version 2"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Append(Link{HolderID: "vault", Action: Stored, At: at(11),
		ReceivedDigest: d('b'), ReleasedDigest: d('b'), Authorization: "PERMIT"}); err != nil {
		t.Fatal(err)
	}
	if !c.Intact() {
		t.Fatalf("a legitimate processing step was reported as a break: %v", c.Breaks())
	}
	if c.CurrentDigest() != d('b') {
		t.Fatalf("current digest = %s", shortD(c.CurrentDigest()))
	}
}

// TestEveryLinkNeedsAnAuthorisation. A link with none records that
// something happened, not that it was allowed.
func TestEveryLinkNeedsAnAuthorisation(t *testing.T) {
	l := acquisition()
	l.Authorization = ""
	if err := l.Validate(); err == nil {
		t.Fatal("an unauthorised custody link was accepted")
	}
}

// TestProcessedAndDisclosedMustSayWhatHappened.
func TestProcessedAndDisclosedMustSayWhatHappened(t *testing.T) {
	for _, a := range []Action{Processed, Disclosed} {
		l := Link{HolderID: "h", Action: a, At: at(10),
			ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"}
		if a == Processed {
			l.ReleasedDigest = d('b')
		}
		if err := l.Validate(); err == nil {
			t.Errorf("%s was accepted with no note", a)
		}
	}
}

// TestTimeCannotRunBackwards.
func TestTimeCannotRunBackwards(t *testing.T) {
	c := chain(t)
	err := c.Append(Link{HolderID: "vault", Action: Stored, At: at(8),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"})
	if !errors.Is(err, ErrTimeInverted) {
		t.Fatalf("a link before its predecessor was accepted: %v", err)
	}
}

// TestAChainBeginsWithAnAcquisition.
func TestAChainBeginsWithAnAcquisition(t *testing.T) {
	l := acquisition()
	l.Action = Stored
	if _, err := New("evidenceversion:e1v1", "t-acme", l); err == nil {
		t.Fatal("a chain beginning with STORED was accepted; nothing says how it was obtained")
	}
}

// TestASealedChainAcceptsNothingFurther. The digest a recipient
// verifies must be the digest the chain ends on.
func TestASealedChainAcceptsNothingFurther(t *testing.T) {
	c := chain(t)
	if err := c.Seal("case-owner", "PERMIT export", at(12)); err != nil {
		t.Fatal(err)
	}
	if !c.IsSealed() {
		t.Fatal("the chain does not report itself sealed")
	}
	err := c.Append(Link{HolderID: "someone", Action: Stored, At: at(13),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"})
	if !errors.Is(err, ErrSealed) {
		t.Fatalf("a sealed chain accepted a link: %v", err)
	}
	if err := c.Seal("x", "y", at(14)); !errors.Is(err, ErrSealed) {
		t.Fatal("a sealed chain was sealed twice")
	}
	if !c.Intact() {
		t.Fatalf("sealing broke the chain: %v", c.Breaks())
	}
}

// TestTheLimitationIsEmptyWhenIntact. A limitation that is always
// present is noise, and noise in a limitations section is how the real
// ones get skipped.
func TestTheLimitationIsEmptyWhenIntact(t *testing.T) {
	c := chain(t)
	if c.Limitation() != "" {
		t.Fatalf("an intact chain produced a limitation: %q", c.Limitation())
	}
	c.Append(Link{HolderID: "analyst-1", Action: Copied, At: at(10),
		ReceivedDigest: d('b'), ReleasedDigest: d('b'), Authorization: "PERMIT"})
	lim := c.Limitation()
	if lim == "" {
		t.Fatal("a broken chain produced no limitation")
	}
	if !strings.Contains(lim, "may still be usable") {
		t.Fatalf("the limitation overstates the consequence: %q", lim)
	}
	if !strings.Contains(lim, "no longer be shown to be unaltered") {
		t.Fatalf("the limitation understates the consequence: %q", lim)
	}
}

// TestHoldersAreListedWithoutRepetition, for the "who touched this"
// question a reviewer asks first.
func TestHoldersAreListedWithoutRepetition(t *testing.T) {
	c := chain(t)
	c.Append(Link{HolderID: "vault", Action: Stored, At: at(10),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"})
	c.Append(Link{HolderID: "ingest-service", Action: Copied, At: at(11),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"})
	h := c.Holders()
	if len(h) != 2 || h[0] != "ingest-service" || h[1] != "vault" {
		t.Fatalf("Holders() = %v", h)
	}
}

// TestTheDigestCoversEveryLink.
func TestTheDigestCoversEveryLink(t *testing.T) {
	c := chain(t)
	base, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	c.Append(Link{HolderID: "vault", Action: Stored, At: at(10),
		ReceivedDigest: d('a'), ReleasedDigest: d('a'), Authorization: "PERMIT"})
	after, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if after == base {
		t.Fatal("appending a link did not change the chain digest")
	}
	// And editing a link's authorisation must change it too.
	c.Links[1].Authorization = "PERMIT something-else"
	edited, _ := c.Digest()
	if edited == after {
		t.Fatal("editing a link's authorisation did not change the chain digest")
	}
}
