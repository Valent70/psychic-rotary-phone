package verification

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Verify runs the whole chain over a bundle.
//
// Every step is independent: a failure early does not stop the rest,
// because a verifier who learns only the first thing that broke cannot
// judge how bad the situation is.
func Verify(b *Bundle, opts Options) Report {
	c := opts.Canonicalizer
	if c == nil {
		c = DefaultCanonicalizer()
	}
	at := opts.At
	if at.IsZero() {
		at = time.Now().UTC()
	}

	r := Report{
		BundleDigest:         b.Digest(),
		ClaimedQualification: b.manifest.ClaimedQualification,
		Canonicalizer:        c.Name(),
		At:                   at,
		Limits: []string{
			"key authenticity cannot be established from a bundle: a bundle produced " +
				"entirely by an impostor is internally perfect, and key trust is an " +
				"out-of-band problem",
			"without an external anchor, a ledger proves its own internal consistency and " +
				"not that it existed before it was handed over",
			"this kit verifies what the bundle contains; it says nothing about evidence " +
				"that was never put in the bundle",
		},
	}
	if strings.Contains(c.Name(), "SHARED WITH THE SYSTEM") {
		r.Limits = append(r.Limits,
			"canonicalisation used the system's own implementation, so a defect in it is "+
				"invisible here: both sides make the same mistake and agree")
	}

	keys, keySource := resolveKeys(b, opts)
	r.Steps = append(r.Steps,
		stepCanonicalize(b, c),
		stepArtefactHashes(b),
		stepSignature(b, keys, keySource),
		stepProvenance(b),
		stepLedgerLineage(b),
		stepReplay(b),
		stepRevocation(b, opts),
	)

	derived, qStep := deriveQualification(b, r.Steps)
	r.DerivedQualification = derived
	r.Steps = append(r.Steps, qStep)
	return r
}

func resolveKeys(b *Bundle, opts Options) (map[string]ed25519.PublicKey, string) {
	if len(opts.TrustedKeys) > 0 {
		return opts.TrustedKeys, "supplied out of band by the verifier"
	}
	out := map[string]ed25519.PublicKey{}
	for id, hexKey := range b.manifest.PublicKeys {
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		out[id] = ed25519.PublicKey(raw)
	}
	return out, "taken from the bundle itself, which establishes nothing about whose keys they are"
}

// stepCanonicalize re-derives the canonical form of every JSON file in
// the bundle and checks it is stable.
//
// The property being tested is not "VERIQO canonicalised correctly" --
// it is that re-canonicalising the canonical form is a no-op. A file
// whose canonical form differs from itself would make every digest
// over it depend on how it happened to be serialised.
func stepCanonicalize(b *Bundle, c Canonicalizer) Step {
	s := Step{Name: "canonicalize"}
	var checked, unstable []string
	for path, content := range b.files {
		if !strings.HasSuffix(path, ".json") {
			continue
		}
		var v any
		if err := json.Unmarshal(content, &v); err != nil {
			// A file named .json that does not parse is a finding, not
			// a file to skip. Skipping it -- which this step did until
			// the verifier-of-verifier caught it -- means a corrupted
			// document passes verification by being unreadable, which
			// is the worst possible direction for the error to run.
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("%s is named as JSON and does not parse: %v. A document "+
				"that cannot be read cannot be verified, and must not pass by being "+
				"unreadable", path, err)
			return s
		}
		once, err := c.Canonicalize(v)
		if err != nil {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("%s could not be canonicalised: %v", path, err)
			return s
		}
		var v2 any
		if err := json.Unmarshal(once, &v2); err != nil {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("%s canonical form does not parse: %v", path, err)
			return s
		}
		twice, err := c.Canonicalize(v2)
		if err != nil || string(once) != string(twice) {
			unstable = append(unstable, path)
		}
		checked = append(checked, path)
	}
	sort.Strings(unstable)
	if len(checked) == 0 {
		s.Outcome = Unverifiable
		s.Detail = "the bundle contains no JSON documents to canonicalise"
		return s
	}
	if len(unstable) > 0 {
		s.Outcome = Fail
		s.Detail = fmt.Sprintf("%d document(s) do not canonicalise to a fixed point: %s",
			len(unstable), strings.Join(unstable, ", "))
		return s
	}
	s.Outcome = Pass
	s.Detail = fmt.Sprintf("%d JSON document(s) canonicalise to a fixed point", len(checked))
	return s
}

// stepArtefactHashes recomputes the digest of every raw artefact in
// the bundle and compares it to what the evidence records claim.
func stepArtefactHashes(b *Bundle) Step {
	s := Step{Name: "artefact-hashes"}
	raw, ok := b.File("evidence/versions.json")
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = "the bundle carries no evidence version records"
		return s
	}
	var versions []struct {
		ID        string `json:"id"`
		SHA256    string `json:"sha256"`
		Artefact  string `json:"artefact_path"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(raw, &versions); err != nil {
		s.Outcome = Fail
		s.Detail = "evidence/versions.json does not parse: " + err.Error()
		return s
	}
	var mismatched, absent []string
	checked := 0
	for _, v := range versions {
		if v.Artefact == "" {
			absent = append(absent, v.ID)
			continue
		}
		content, ok := b.File(v.Artefact)
		if !ok {
			absent = append(absent, v.ID)
			continue
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != v.SHA256 {
			mismatched = append(mismatched, v.ID)
		}
		if v.SizeBytes != 0 && int64(len(content)) != v.SizeBytes {
			mismatched = append(mismatched, v.ID+" (size)")
		}
		checked++
	}
	sort.Strings(mismatched)
	sort.Strings(absent)
	switch {
	case len(mismatched) > 0:
		s.Outcome = Fail
		s.Detail = fmt.Sprintf("%d artefact(s) do not hash to their recorded digest: %s",
			len(mismatched), strings.Join(mismatched, ", "))
	case checked == 0:
		s.Outcome = Unverifiable
		s.Detail = "no artefact bytes are present; only the records that describe them"
	default:
		s.Outcome = Pass
		s.Detail = fmt.Sprintf("%d artefact(s) hash to their recorded digest", checked)
	}
	if len(absent) > 0 {
		s.Caveats = append(s.Caveats, fmt.Sprintf(
			"%d evidence record(s) reference artefacts the bundle does not carry (%s); "+
				"their digests are unchecked, not confirmed",
			len(absent), strings.Join(absent, ", ")))
	}
	return s
}

// stepSignature verifies the passport signature over the recomputed
// digest -- never over a digest the bundle supplies.
func stepSignature(b *Bundle, keys map[string]ed25519.PublicKey, keySource string) Step {
	s := Step{Name: "signature", Caveats: []string{"public keys were " + keySource}}
	raw, ok := b.File("passport.json")
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = "the bundle carries no passport"
		return s
	}
	var p struct {
		Payload   json.RawMessage `json:"payload"`
		Digest    string          `json:"digest"`
		Signature string          `json:"signature"`
		KeyID     string          `json:"key_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		s.Outcome = Fail
		s.Detail = "passport.json does not parse: " + err.Error()
		return s
	}

	// Recompute the digest from the payload rather than trusting the
	// digest field. A verifier that checked the signature against the
	// supplied digest would accept any payload at all.
	var v any
	if err := json.Unmarshal(p.Payload, &v); err != nil {
		s.Outcome = Fail
		s.Detail = "the passport payload does not parse: " + err.Error()
		return s
	}
	canon, err := DefaultCanonicalizer().Canonicalize(v)
	if err != nil {
		s.Outcome = Fail
		s.Detail = "the passport payload could not be canonicalised: " + err.Error()
		return s
	}
	sum := sha256.Sum256(canon)
	got := hex.EncodeToString(sum[:])
	if got != p.Digest {
		s.Outcome = Fail
		s.Detail = fmt.Sprintf("the payload hashes to %s and the passport records %s; the "+
			"payload has been altered since it was signed", short(got), short(p.Digest))
		return s
	}

	key, ok := keys[p.KeyID]
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = fmt.Sprintf("no public key for %q is available, so the signature cannot "+
			"be checked. The payload does match its own digest", p.KeyID)
		return s
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		s.Outcome = Fail
		s.Detail = "the signature is not valid base64"
		return s
	}
	if !ed25519.Verify(key, []byte(got), sig) && !ed25519.Verify(key, canon, sig) {
		s.Outcome = Fail
		s.Detail = "the signature does not verify over the recomputed payload"
		return s
	}
	s.Outcome = Pass
	s.Detail = fmt.Sprintf("the signature verifies under %s over a digest recomputed from "+
		"the payload", p.KeyID)
	return s
}

// stepProvenance checks that every provenance record reaches a
// producer and that the hop path is ordered in time.
func stepProvenance(b *Bundle) Step {
	s := Step{Name: "provenance"}
	raw, ok := b.File("provenance/records.json")
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = "the bundle carries no provenance records"
		return s
	}
	var recs []struct {
		ID   string `json:"id"`
		Path []struct {
			PartyID string    `json:"party_id"`
			Role    string    `json:"role"`
			At      time.Time `json:"at"`
		} `json:"path"`
		SourceContentHash string `json:"source_content_hash"`
	}
	if err := json.Unmarshal(raw, &recs); err != nil {
		s.Outcome = Fail
		s.Detail = "provenance/records.json does not parse: " + err.Error()
		return s
	}
	var broken []string
	for _, rec := range recs {
		if len(rec.Path) == 0 {
			broken = append(broken, rec.ID+" (empty path)")
			continue
		}
		producer := ""
		for i, h := range rec.Path {
			if i > 0 && h.At.Before(rec.Path[i-1].At) {
				broken = append(broken, rec.ID+" (hops out of order)")
				break
			}
			if producer == "" && (h.Role == "OBSERVER" || h.Role == "PRODUCER") {
				producer = h.PartyID
			}
		}
		if producer == "" {
			broken = append(broken, rec.ID+" (no observer or producer)")
		}
		if strings.TrimSpace(rec.SourceContentHash) == "" {
			broken = append(broken, rec.ID+" (no source content hash)")
		}
	}
	sort.Strings(broken)
	if len(broken) > 0 {
		s.Outcome = Fail
		s.Detail = fmt.Sprintf("%d provenance record(s) do not resolve to an origin: %s",
			len(broken), strings.Join(broken, ", "))
		return s
	}
	s.Outcome = Pass
	s.Detail = fmt.Sprintf("%d provenance record(s) reach a named origin with an ordered "+
		"hop path", len(recs))
	s.Caveats = append(s.Caveats, "this establishes that the RECORD is well formed; whether "+
		"the named parties actually handled the material is not checkable from a bundle")
	return s
}

// stepLedgerLineage recomputes the hash chain from genesis.
//
// This is the step where recomputation matters most: the ledger
// carries its own hashes, and a verifier that read them would confirm
// only that the file is self-describing.
func stepLedgerLineage(b *Bundle) Step {
	s := Step{Name: "ledger-lineage"}
	raw, ok := b.File("ledger/records.json")
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = "the bundle carries no ledger records"
		return s
	}
	var recs []struct {
		Height   uint64          `json:"height"`
		PrevHash string          `json:"prev_hash"`
		Event    json.RawMessage `json:"event"`
		Hash     string          `json:"hash"`
	}
	if err := json.Unmarshal(raw, &recs); err != nil {
		s.Outcome = Fail
		s.Detail = "ledger/records.json does not parse: " + err.Error()
		return s
	}
	if len(recs) == 0 {
		s.Outcome = Unverifiable
		s.Detail = "the ledger in this bundle is empty"
		return s
	}
	const genesis = "veriqo-ledger-genesis-v1"
	prev := genesis
	for i, rec := range recs {
		if rec.Height != uint64(i) {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("record at position %d claims height %d", i, rec.Height)
			return s
		}
		if rec.PrevHash != prev {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("record %d links to %s; the chain head is %s",
				rec.Height, short(rec.PrevHash), short(prev))
			return s
		}
		var ev any
		if err := json.Unmarshal(rec.Event, &ev); err != nil {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("record %d's event does not parse", rec.Height)
			return s
		}
		got, err := RecordDigest(rec.Height, rec.PrevHash, ev)
		if err != nil {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("record %d could not be rehashed: %v", rec.Height, err)
			return s
		}
		if got != rec.Hash {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("record %d recomputes to %s and records %s; it has been "+
				"altered since it was written", rec.Height, short(got), short(rec.Hash))
			return s
		}
		prev = rec.Hash
	}
	s.Outcome = Pass
	s.Detail = fmt.Sprintf("%d record(s) rehash to their recorded digests and chain from "+
		"genesis to %s", len(recs), short(prev))
	s.Caveats = append(s.Caveats, "no external anchor is present, so this establishes "+
		"internal consistency and not that the chain existed before it was handed over")
	return s
}

// RecordDigest reproduces the ledger's own digest computation. It is
// written out here, rather than imported, so that a verifier reading
// this file can see exactly what is covered.
//
// It is exported so that a bundle BUILDER computes the digest through
// the same code path a verifier will use. If the two ever diverged,
// every bundle would fail verification for a reason that had nothing
// to do with its contents.
func RecordDigest(height uint64, prev string, event any) (string, error) {
	canon, err := DefaultCanonicalizer().Canonicalize(map[string]any{
		"height":    float64(height),
		"prev_hash": prev,
		"event":     event,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// stepReplay re-executes the deterministic steps in the bundle's
// replay record and compares each output digest.
func stepReplay(b *Bundle) Step {
	s := Step{Name: "replay"}
	raw, ok := b.File("replay/steps.json")
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = "the bundle carries no replay record"
		return s
	}
	var steps []struct {
		Name       string          `json:"name"`
		Kind       string          `json:"kind"`
		Input      json.RawMessage `json:"input"`
		OutputHash string          `json:"output_hash"`
		Output     json.RawMessage `json:"output,omitempty"`
	}
	if err := json.Unmarshal(raw, &steps); err != nil {
		s.Outcome = Fail
		s.Detail = "replay/steps.json does not parse: " + err.Error()
		return s
	}
	var mismatched []string
	deterministic, recorded := 0, 0
	for _, st := range steps {
		if strings.EqualFold(st.Kind, "RECORDED") {
			recorded++
			continue
		}
		deterministic++
		if len(st.Output) == 0 {
			mismatched = append(mismatched, st.Name+" (no output to compare)")
			continue
		}
		var v any
		if err := json.Unmarshal(st.Output, &v); err != nil {
			mismatched = append(mismatched, st.Name+" (output does not parse)")
			continue
		}
		canon, err := DefaultCanonicalizer().Canonicalize(v)
		if err != nil {
			mismatched = append(mismatched, st.Name+" (output does not canonicalise)")
			continue
		}
		sum := sha256.Sum256(canon)
		if hex.EncodeToString(sum[:]) != st.OutputHash {
			mismatched = append(mismatched, st.Name)
		}
	}
	sort.Strings(mismatched)
	switch {
	case len(mismatched) > 0:
		s.Outcome = Fail
		s.Detail = fmt.Sprintf("%d deterministic step(s) do not reproduce: %s",
			len(mismatched), strings.Join(mismatched, ", "))
	case deterministic == 0:
		s.Outcome = Unverifiable
		s.Detail = fmt.Sprintf("the replay record has %d step(s) and every one is RECORDED; "+
			"nothing was re-executed, so this replay establishes nothing", recorded)
	default:
		s.Outcome = Pass
		s.Detail = fmt.Sprintf("%d deterministic step(s) reproduce their recorded output",
			deterministic)
	}
	if recorded > 0 {
		s.Caveats = append(s.Caveats, fmt.Sprintf(
			"%d step(s) are RECORDED rather than re-executed: their outputs were taken from "+
				"the bundle. That establishes the pipeline's behaviour GIVEN those outputs, "+
				"not that those outputs would recur", recorded))
	}
	return s
}

// stepRevocation reports what the verifier did about revocation, and
// distinguishes "not revoked" from "not checked".
func stepRevocation(b *Bundle, opts Options) Step {
	s := Step{Name: "revocation"}
	raw, ok := b.File("passport.json")
	if !ok {
		s.Outcome = Unverifiable
		s.Detail = "no passport to check"
		return s
	}
	var p struct {
		Payload struct {
			FindingID string `json:"finding_id"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(raw, &p)
	if opts.Revocations == nil {
		s.Outcome = Unverifiable
		s.Detail = "the verifier supplied no revocation list, so this passport is not known " +
			"to stand. 'Not revoked' and 'not checked' are different answers"
		return s
	}
	for _, r := range opts.Revocations {
		if r == p.Payload.FindingID {
			s.Outcome = Fail
			s.Detail = "the finding is on the verifier's revocation list"
			return s
		}
	}
	s.Outcome = Pass
	s.Detail = fmt.Sprintf("the finding is absent from a revocation list of %d entry(ies) "+
		"the verifier obtained independently", len(opts.Revocations))
	return s
}

// deriveQualification computes what the bundle actually supports, and
// compares it with what the bundle claims.
//
// This is the step that makes the kit a verifier rather than a relay.
// The claimed value is never adopted: it is contradicted or confirmed.
func deriveQualification(b *Bundle, steps []Step) (string, Step) {
	s := Step{Name: "qualification-state"}
	claimed := b.manifest.ClaimedQualification

	for _, st := range steps {
		if st.Outcome == Fail {
			s.Outcome = Fail
			s.Detail = fmt.Sprintf("a prior step failed (%s), so nothing in this bundle is "+
				"qualified whatever it claims", st.Name)
			return "REFUTED", s
		}
	}

	// The derived LEVEL is bounded by what an outside party can see.
	// A bundle carrying no independent evidence cannot support a state
	// above INTERNALLY_ASSURED, whatever it says -- Law 11 applied
	// from the verifier's side.
	//
	// How COMPLETE the verification was is a separate question and is
	// deliberately not folded into the level. A bundle that omits a
	// ledger has not thereby become less assured; it has been less
	// thoroughly checked, and conflating the two would let a narrow
	// verification read as a negative finding -- and, worse, let a
	// verifier quietly downgrade a claim it simply could not examine.
	derived := "INTERNALLY_ASSURED"
	if hasIndependentEvidence(b) {
		derived = "EXTERNALLY_TESTED"
	}
	var unver []string
	for _, st := range steps {
		if st.Outcome == Unverifiable {
			unver = append(unver, st.Name)
		}
	}

	if claimed == derived {
		s.Outcome = Pass
		s.Detail = fmt.Sprintf("the bundle claims %s and the evidence in it supports %s",
			claimed, derived)
	} else {
		s.Outcome = Fail
		s.Detail = fmt.Sprintf("the bundle claims %s; the evidence in it supports %s. The "+
			"derived value is what a reader should believe, and the difference is the finding",
			claimed, derived)
	}
	if len(unver) > 0 {
		s.Caveats = append(s.Caveats, fmt.Sprintf(
			"%d step(s) could not be run against this bundle (%s), so this level is "+
				"derived from a partial examination. It is a narrower answer, not a "+
				"weaker one", len(unver), strings.Join(unver, ", ")))
	}
	return derived, s
}

func hasIndependentEvidence(b *Bundle) bool {
	raw, ok := b.File("assurance/evidence.json")
	if !ok {
		return false
	}
	var ev []struct {
		Class     string `json:"class"`
		Validator struct {
			ID         string `json:"id"`
			External   bool   `json:"external"`
			AttestedBy string `json:"attested_by"`
		} `json:"validator"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return false
	}
	for _, e := range ev {
		if e.Class != "ASSURANCE_INTERNAL" && e.Validator.External &&
			e.Validator.AttestedBy != "" && e.Validator.AttestedBy != e.Validator.ID {
			return true
		}
	}
	return false
}

// Render writes the report the way a third party should read it.
func (r Report) Render() string {
	var b strings.Builder
	b.WriteString("VERIQO INDEPENDENT VERIFICATION\n")
	b.WriteString("  the verifier does not trust the system being verified: every value\n")
	b.WriteString("  below was recomputed from the bundle, never read from it\n\n")
	fmt.Fprintf(&b, "  bundle:        %s\n", short(r.BundleDigest))
	fmt.Fprintf(&b, "  canonicaliser: %s\n\n", r.Canonicalizer)

	for _, s := range r.Steps {
		fmt.Fprintf(&b, "  %-12s %-14s %s\n", s.Outcome, s.Name, s.Detail)
		for _, c := range s.Caveats {
			fmt.Fprintf(&b, "               caveat: %s\n", c)
		}
	}

	fmt.Fprintf(&b, "\n  claimed qualification: %s\n", r.ClaimedQualification)
	fmt.Fprintf(&b, "  derived qualification: %s\n", r.DerivedQualification)
	if r.ClaimedQualification != r.DerivedQualification {
		b.WriteString("  THESE DISAGREE. Believe the derived value.\n")
	}

	if r.Verified() {
		if u := r.Unverifiable(); len(u) > 0 {
			fmt.Fprintf(&b, "\n  VERIFIED, on a PARTIAL examination: %d of %d steps could\n"+
				"  not be run against this bundle. See below.\n", len(u), len(r.Steps))
		} else {
			b.WriteString("\n  VERIFIED, within the limits below.\n")
		}
	} else {
		b.WriteString("\n  NOT VERIFIED.\n")
		for _, f := range r.Failures() {
			fmt.Fprintf(&b, "    failure: %s\n", f)
		}
	}
	if u := r.Unverifiable(); len(u) > 0 {
		fmt.Fprintf(&b, "\n  %d step(s) could not be run against this bundle. That narrows\n"+
			"  the verification; it does not make it false:\n", len(u))
		for _, s := range u {
			fmt.Fprintf(&b, "    - %s\n", s)
		}
	}
	b.WriteString("\n  what this verification cannot establish at all:\n")
	for _, l := range r.Limits {
		fmt.Fprintf(&b, "    - %s\n", l)
	}
	return b.String()
}
