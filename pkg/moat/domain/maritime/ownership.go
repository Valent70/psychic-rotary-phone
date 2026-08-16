// ownership.go extends the maritime ontology with the entity/relation
// vocabulary CRITICAL_FINDINGS.docx names under "Missing Ownership
// Intelligence": expanded entity types beyond the original 11 (shell
// companies, beneficial owners, correspondent banks, trade-finance
// instruments) and a beneficial-ownership piercing traversal that
// multiplies ownership percentages along a chain to find the ultimate
// controlling party — the concrete "pierce corporate veils" capability
// the audit says VERIQO lacks.
//
// Honest scope: this is structural graph algebra over ownership
// percentages the caller supplies (from filings, registries, or other
// evidence sources already arbitrated by pkg/moat/fusion). It does NOT
// source real beneficial-ownership registry data — no such feed is
// available in this environment. What it closes is the *algorithm*:
// given typed ownership edges with percentages, correctly compute
// effective ultimate ownership through arbitrarily deep, possibly
// cyclic (a known evasion technique) chains.
package maritime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

// Additional EntityKinds closing the "7 not 100+" gap the audit names.
// Not a claim of "100+" — an honest, purpose-built expansion covering
// the entities a TBML / sanctions-evasion investigation actually needs
// beyond the original vessel-voyage-cargo core.
const (
	KindShellCompany           EntityKind = "SHELL_COMPANY"
	KindBeneficialOwner        EntityKind = "BENEFICIAL_OWNER"
	KindManagingCompany        EntityKind = "MANAGING_COMPANY"
	KindCharterer              EntityKind = "CHARTERER"
	KindConsignee              EntityKind = "CONSIGNEE"
	KindShipper                EntityKind = "SHIPPER"
	KindCorrespondentBank      EntityKind = "CORRESPONDENT_BANK"
	KindTradeFinanceInstrument EntityKind = "TRADE_FINANCE_INSTRUMENT" // e.g. Letter of Credit
	KindInvoice                EntityKind = "INVOICE"
	KindPandIClub              EntityKind = "PANDI_CLUB"
	KindClassificationSociety  EntityKind = "CLASSIFICATION_SOCIETY"
	KindCustomsDeclaration     EntityKind = "CUSTOMS_DECLARATION"
	KindJurisdiction           EntityKind = "JURISDICTION" // distinct from Country: a registry/regulatory domain (e.g. a flag-of-convenience registry)
)

// Additional RelationKinds for ownership-chain and trade-finance
// traversal.
const (
	RelBeneficialOwnerOf RelationKind = "BENEFICIAL_OWNER_OF" // BeneficialOwner -> ShellCompany/Vessel
	RelControlledBy      RelationKind = "CONTROLLED_BY"       // Entity -> Entity (non-equity control, e.g. proxy/nominee)
	RelManagedBy         RelationKind = "MANAGED_BY"          // Vessel -> ManagingCompany
	RelCharteredBy       RelationKind = "CHARTERED_BY"        // Voyage -> Charterer
	RelConsignedTo       RelationKind = "CONSIGNED_TO"        // Cargo -> Consignee
	RelShippedBy         RelationKind = "SHIPPED_BY"          // Cargo -> Shipper
	RelIssuedBy          RelationKind = "ISSUED_BY"           // TradeFinanceInstrument -> CorrespondentBank
	RelReferencedBy      RelationKind = "REFERENCED_BY"       // Invoice -> TradeFinanceInstrument
	RelClassedBy         RelationKind = "CLASSED_BY"          // Vessel -> ClassificationSociety
	RelRegisteredIn      RelationKind = "REGISTERED_IN"       // ShellCompany -> Jurisdiction
	RelDeclaredIn        RelationKind = "DECLARED_IN"         // Cargo -> CustomsDeclaration
)

var (
	ErrUnknownOwnershipEdge = errors.New("maritime: ownership edge references unregistered entity")
	ErrInvalidPercent       = errors.New("maritime: ownership percent must be in (0,1]")
	ErrNonFiniteOwnership   = errors.New("maritime: ownership percent is NaN or +/-Inf")
)

// OwnershipStatus is derived (recomputed from ledger replay), never
// stored mutably on a ledger record — v7.9 audit P0 "Ownership temporal
// semantics": "ledger = immutable history, graph = current effective
// state."
type OwnershipStatus string

const (
	OwnershipStatusActive     OwnershipStatus = "ACTIVE"
	OwnershipStatusSuperseded OwnershipStatus = "SUPERSEDED"
)

// ownershipRecord is one hash-chained, append-only ownership-percentage
// fact. Kept in a separate ledger from Registry's entity/relation log
// because percentage is a quantitative attribute of a relation, not an
// identity fact — and ownership stakes are the one class of fact this
// domain expects to be amended over time (a filing correction, a stake
// sale).
//
// v7.9 audit P0 "Ownership temporal semantics": the ledger itself
// remains immutable and append-only (no field of an existing record is
// ever mutated after append — DARI invariant preserved exactly). What
// changed is that a repeat RecordOwnership call for the SAME (from,to)
// pair is now recorded as an explicit correction (SupersedesIndex set,
// part of the hashed content, itself immutable) instead of silently
// becoming a second concurrent stake that traversal would sum against
// the first — the exact "60%+20%=80%" bug the audit demonstrated.
// EffectiveTo is likewise never mutated on the superseded record;
// instead it is DERIVED at read time (OwnershipHistory) as the Tick of
// whichever later record supersedes it — "ledger = immutable history,
// graph = current effective state" implemented literally: the log
// never changes, only what CurrentOwners()/UBO methods materialize
// from replaying it does.
type ownershipRecord struct {
	Index           uint64
	FromID          string // owner
	ToID            string // owned entity
	Percent         float64
	Type            OwnershipType
	Tick            uint64
	SupersedesIndex int64 // -1 if this is a new fact, else the ledger Index it corrects
	PrevHash        string
	Hash            string
}

// OwnershipType distinguishes what KIND of ownership/control an edge
// represents — v7.9 audit P1 "Ownership totality juga belum
// divalidasi": a LEGAL 60% stake and a VOTING 60% stake are not the
// same claim and must not be summed together when checking whether
// total ownership of an entity exceeds 100%.
type OwnershipType string

const (
	OwnershipLegal      OwnershipType = "LEGAL"
	OwnershipBeneficial OwnershipType = "BENEFICIAL"
	OwnershipVoting     OwnershipType = "VOTING"
	OwnershipEconomic   OwnershipType = "ECONOMIC"
	OwnershipControl    OwnershipType = "CONTROL"
)

// OwnershipEdge is one materialized (from, percent, type) fact —
// exported (v7.9 P1 "UBO != intermediate owner" API cleanup) so callers
// can consume DirectOwners()/ControlChains() results without reaching
// into package-private types.
type OwnershipEdge struct {
	FromID  string
	Percent float64
	Type    OwnershipType
}

// OwnershipFact is one entry in a (from,to) pair's full immutable
// history, with Status/EffectiveTo DERIVED from ledger replay (never
// stored mutably) — the "current effective ownership graph" the audit
// asks for, expressed per-pair.
type OwnershipFact struct {
	Percent       float64
	Type          OwnershipType
	Status        OwnershipStatus
	EffectiveFrom uint64
	EffectiveTo   uint64 // 0 means "still open" (ACTIVE has no EffectiveTo)
	Supersedes    int64  // -1 if none
}

// TotalityViolation reports that the sum of direct ownership edges of
// one OwnershipType into a target exceeds 1.0 (100%) — v7.9 P1 "120%
// ownership": the audit's explicit point that silently capping at 1.0
// is not validation. This type surfaces the finding instead of hiding
// it.
type TotalityViolation struct {
	ToID  string
	Type  OwnershipType
	Total float64 // the actual, uncapped sum
}

// CircularOwnershipFinding is a detected ownership cycle treated as
// INTELLIGENCE (v7.9 §16), not merely a traversal hazard to guard
// against.
type CircularOwnershipFinding struct {
	Cycle      []string // entity IDs in cycle order, first repeated at end
	Confidence float64  // product of edge percents around the cycle
}

// OwnershipGraph tracks weighted ownership/control edges layered on top
// of a maritime Registry and provides ultimate-beneficial-owner (UBO)
// piercing. The immutable ledger (g.log) and the materialized current-
// effective-state view (g.edges, derived by replay) are kept
// explicitly distinct per the audit's P0 finding.
type OwnershipGraph struct {
	mu   sync.RWMutex
	reg  *Registry
	log  []ownershipRecord
	head string
	// edges[toID] = current ACTIVE materialized edges (one per
	// (fromID,toID,Type) triple after supersession) — "Current
	// Effective Ownership Graph", always re-derivable from log.
	edges map[string][]OwnershipEdge
	// activeIndex[(fromID,toID,Type)] = ledger Index of the currently
	// active record for that triple, for O(1) supersession lookup.
	activeIndex map[string]uint64
}

func edgeKey(fromID, toID string, t OwnershipType) string {
	return fromID + "|" + toID + "|" + string(t)
}

// NewOwnershipGraph wraps an existing entity Registry. Ownership edges
// must reference entities already registered in reg.
func NewOwnershipGraph(reg *Registry) *OwnershipGraph {
	return &OwnershipGraph{
		reg:         reg,
		edges:       make(map[string][]OwnershipEdge),
		activeIndex: make(map[string]uint64),
	}
}

// RecordOwnership appends a hash-chained fact "fromID owns `percent`
// (0,1] of toID" of the given type. A repeat call for the SAME
// (fromID,toID,Type) triple is recorded as a CORRECTION: the ledger
// gains a new immutable entry (SupersedesIndex pointing at the prior
// active record for that triple — itself never mutated), and the
// materialized "current effective" edge for that triple is replaced,
// not summed — the concrete fix for the audit's "old filing 60% + new
// filing 20% = effective 80%" bug. A genuinely NEW owner (different
// FromID) for the same ToID/Type is unaffected and simply adds a
// second concurrent edge, as fractional ownership requires.
func (g *OwnershipGraph) RecordOwnership(fromID, toID string, percent float64, ownType OwnershipType, tick uint64) error {
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return ErrNonFiniteOwnership
	}
	if percent <= 0 || percent > 1 {
		return ErrInvalidPercent
	}
	if _, ok := g.reg.GetEntity(fromID); !ok {
		return ErrUnknownOwnershipEdge
	}
	if _, ok := g.reg.GetEntity(toID); !ok {
		return ErrUnknownOwnershipEdge
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	key := edgeKey(fromID, toID, ownType)
	supersedes := int64(-1)
	if prevIdx, ok := g.activeIndex[key]; ok {
		supersedes = int64(prevIdx) // #nosec G115 -- prevIdx is a bounded in-process slice/version index, never near int64 range
	}

	rec := ownershipRecord{
		Index: uint64(len(g.log)), FromID: fromID, ToID: toID,
		Percent: percent, Type: ownType, Tick: tick,
		SupersedesIndex: supersedes, PrevHash: g.head,
	}
	rec.Hash = hashOwnershipRecord(rec)
	g.log = append(g.log, rec)
	g.head = rec.Hash
	g.activeIndex[key] = rec.Index

	// Materialize: replace the prior active edge for this triple (if
	// any) rather than appending alongside it.
	list := g.edges[toID]
	replaced := false
	for i, e := range list {
		if e.FromID == fromID && e.Type == ownType {
			list[i] = OwnershipEdge{FromID: fromID, Percent: percent, Type: ownType}
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, OwnershipEdge{FromID: fromID, Percent: percent, Type: ownType})
	}
	g.edges[toID] = list
	return nil
}

func hashOwnershipRecord(rec ownershipRecord) string {
	raw := fmt.Sprintf("index=%d|from=%s|to=%s|pct=%.10f|type=%s|tick=%d|supersedes=%d|prev=%s",
		rec.Index, rec.FromID, rec.ToID, rec.Percent, rec.Type, rec.Tick, rec.SupersedesIndex, rec.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyChain re-derives every hash in the ownership ledger, detecting
// tampering — same discipline as Registry.VerifyChain. The IMMUTABLE
// log is verified exactly as recorded; supersession is a fact ABOUT a
// record (SupersedesIndex), never a mutation OF a prior record, so
// verifying the chain and replaying current state are independent
// concerns by construction.
func (g *OwnershipGraph) VerifyChain() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	prev := ""
	for _, rec := range g.log {
		want := hashOwnershipRecord(ownershipRecord{
			Index: rec.Index, FromID: rec.FromID, ToID: rec.ToID, Percent: rec.Percent,
			Type: rec.Type, Tick: rec.Tick, SupersedesIndex: rec.SupersedesIndex, PrevHash: prev,
		})
		if want != rec.Hash {
			return ErrTamperDetected
		}
		prev = rec.Hash
	}
	return nil
}

// OwnershipHistory returns the FULL immutable history for one
// (fromID,toID,Type) triple, oldest first, with Status/EffectiveTo
// DERIVED from ledger replay (never stored mutably) — the "Immutable
// Ownership Ledger" view the audit asks for, as distinct from
// CurrentOwners' "Current Effective Ownership Graph" view.
func (g *OwnershipGraph) OwnershipHistory(fromID, toID string, ownType OwnershipType) []OwnershipFact {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var chain []ownershipRecord
	for _, rec := range g.log {
		if rec.FromID == fromID && rec.ToID == toID && rec.Type == ownType {
			chain = append(chain, rec)
		}
	}
	sort.Slice(chain, func(i, j int) bool { return chain[i].Index < chain[j].Index })
	// supersededBy[Index] = Tick of the record that supersedes it.
	supersededByTick := map[uint64]uint64{}
	for _, rec := range chain {
		if rec.SupersedesIndex >= 0 {
			supersededByTick[uint64(rec.SupersedesIndex)] = rec.Tick
		}
	}
	out := make([]OwnershipFact, 0, len(chain))
	for _, rec := range chain {
		f := OwnershipFact{
			Percent: rec.Percent, Type: rec.Type, EffectiveFrom: rec.Tick,
			Supersedes: rec.SupersedesIndex, Status: OwnershipStatusActive,
		}
		if to, superseded := supersededByTick[rec.Index]; superseded {
			f.Status = OwnershipStatusSuperseded
			f.EffectiveTo = to
		}
		out = append(out, f)
	}
	return out
}

// UBOResult is one ultimate-beneficial-owner finding: an owner reached
// via one or more chains, with the cumulative effective percentage and
// an explanation of the shortest chain that reached it.
type UBOResult struct {
	OwnerID          string
	EffectivePercent float64 // sum across all distinct chains
	ChainCount       int
	ExamplePath      []string // entity IDs, owner -> ... -> target
	// IsIntermediate is true when OwnerID itself has at least one
	// registered owner further up the chain — v7.9 §14 "SHELL bukan
	// ultimate beneficial owner" fix: AllOwnersInChain sets this so a
	// caller can filter, and UltimateBeneficialOwners does that
	// filtering for you.
	IsIntermediate bool
	// TotalityExceeded is true when EffectivePercent would exceed 1.0
	// before any capping — v7.9 §15 "Capping bukan validation": this
	// package no longer silently caps at 1.0 without telling the
	// caller a structurally-impossible ownership total was found.
	TotalityExceeded bool
}

// allOwnersInChain is the shared traversal walking the CURRENT
// materialized edges (post-supersession) backward from targetID up to
// maxHops for the given OwnershipType, multiplying percentages along
// each chain. Cycle-safe: a chain revisiting an entity already on its
// own path is truncated there rather than followed infinitely — cyclic
// cross-ownership is itself intelligence (see CircularOwnershipFindings),
// not merely a traversal hazard.
func (g *OwnershipGraph) allOwnersInChain(targetID string, ownType OwnershipType, maxHops int) map[string]*UBOResult {
	type chainState struct {
		ownerID string
		effect  float64
		path    []string
		visited map[string]bool
	}
	acc := make(map[string]*UBOResult)
	var walk func(cs chainState, hop int)
	walk = func(cs chainState, hop int) {
		if hop > maxHops {
			return
		}
		for _, e := range g.edges[cs.ownerID] {
			if e.Type != ownType {
				continue
			}
			if cs.visited[e.FromID] {
				continue
			}
			nextEffect := cs.effect * e.Percent
			nextPath := append(append([]string{}, cs.path...), e.FromID)
			nextVisited := make(map[string]bool, len(cs.visited)+1)
			for k := range cs.visited {
				nextVisited[k] = true
			}
			nextVisited[e.FromID] = true

			if r, ok := acc[e.FromID]; ok {
				r.EffectivePercent += nextEffect
				r.ChainCount++
				if r.EffectivePercent > 1 {
					r.TotalityExceeded = true
				}
			} else {
				acc[e.FromID] = &UBOResult{
					OwnerID: e.FromID, EffectivePercent: nextEffect,
					ChainCount: 1, ExamplePath: nextPath,
				}
			}
			walk(chainState{ownerID: e.FromID, effect: nextEffect, path: nextPath, visited: nextVisited}, hop+1)
		}
	}
	walk(chainState{ownerID: targetID, effect: 1.0, path: []string{targetID}, visited: map[string]bool{targetID: true}}, 0)
	// Mark intermediate owners: anyone who THEMSELVES has a registered
	// owner of the same type is not ultimate.
	for id, r := range acc {
		if len(g.edges[id]) > 0 {
			for _, e := range g.edges[id] {
				if e.Type == ownType {
					r.IsIntermediate = true
					break
				}
			}
		}
	}
	return acc
}

// AllOwnersInChain returns every owner reachable backward from
// targetID (direct AND intermediate AND ultimate) whose cumulative
// effective ownership is >= thresholdPercent — the FULL-chain view
// (the pre-v7.10 behavior of what was called FindUltimateBeneficialOwners,
// renamed for honesty: this includes intermediate/shell owners, which
// are NOT ultimate beneficial owners — see v7.9 audit §14).
func (g *OwnershipGraph) AllOwnersInChain(targetID string, ownType OwnershipType, thresholdPercent float64, maxHops int) []UBOResult {
	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.allOwnersInChain(targetID, ownType, maxHops)
	out := make([]UBOResult, 0, len(acc))
	for _, r := range acc {
		if r.EffectivePercent >= thresholdPercent {
			out = append(out, *r)
		}
	}
	sortUBOResults(out)
	return out
}

// UltimateBeneficialOwners returns ONLY owners with NO further
// registered owner of their own — i.e. genuinely terminal/ultimate
// owners, per the standard UBO definition — closing v7.9 §14's exact
// finding that the prior FindUltimateBeneficialOwners incorrectly
// returned intermediate shells (a "SHELL" owning 100% of a vessel is
// NOT a UBO; the PERSON who ultimately owns the shell is). Use
// AllOwnersInChain (or DirectOwners for one-hop) to see intermediate
// owners.
func (g *OwnershipGraph) UltimateBeneficialOwners(targetID string, ownType OwnershipType, thresholdPercent float64, maxHops int) []UBOResult {
	_, span := telemetry.StartSpan(context.Background(), "maritime.UltimateBeneficialOwners",
		telemetry.Attribute{Key: "target_id", Value: targetID})
	defer span.End()

	g.mu.RLock()
	defer g.mu.RUnlock()
	acc := g.allOwnersInChain(targetID, ownType, maxHops)
	out := make([]UBOResult, 0, len(acc))
	for _, r := range acc {
		if r.IsIntermediate {
			continue
		}
		if r.EffectivePercent >= thresholdPercent {
			out = append(out, *r)
		}
	}
	sortUBOResults(out)
	return out
}

func sortUBOResults(out []UBOResult) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectivePercent != out[j].EffectivePercent {
			return out[i].EffectivePercent > out[j].EffectivePercent
		}
		return out[i].OwnerID < out[j].OwnerID
	})
}

// ControlChain is one explicit owner->...->target path — v7.9 P1's
// "ControlChains()" ask: full path enumeration distinct from the
// aggregated-by-owner UBOResult view.
type ControlChain struct {
	Path    []string // owner..target order, same convention as UBOResult.ExamplePath
	Percent float64  // effective ownership along THIS specific path
}

// ControlChains enumerates every distinct owner->...->target path (not
// aggregated by owner) up to maxHops — lets a caller see MULTIPLE
// paths between the same two entities explicitly (v7.9 §15's "multi-
// path ownership" concern), rather than only the summed/capped view.
func (g *OwnershipGraph) ControlChains(targetID string, ownType OwnershipType, maxHops int) []ControlChain {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []ControlChain
	type chainState struct {
		ownerID string
		effect  float64
		path    []string
		visited map[string]bool
	}
	var walk func(cs chainState, hop int)
	walk = func(cs chainState, hop int) {
		if hop > maxHops {
			return
		}
		for _, e := range g.edges[cs.ownerID] {
			if e.Type != ownType || cs.visited[e.FromID] {
				continue
			}
			nextEffect := cs.effect * e.Percent
			nextPath := append(append([]string{}, cs.path...), e.FromID)
			out = append(out, ControlChain{Path: nextPath, Percent: nextEffect})
			nextVisited := make(map[string]bool, len(cs.visited)+1)
			for k := range cs.visited {
				nextVisited[k] = true
			}
			nextVisited[e.FromID] = true
			walk(chainState{ownerID: e.FromID, effect: nextEffect, path: nextPath, visited: nextVisited}, hop+1)
		}
	}
	walk(chainState{ownerID: targetID, effect: 1.0, path: []string{targetID}, visited: map[string]bool{targetID: true}}, 0)
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Path) != len(out[j].Path) {
			return len(out[i].Path) < len(out[j].Path)
		}
		return fmt.Sprint(out[i].Path) < fmt.Sprint(out[j].Path)
	})
	return out
}

// CircularOwnershipFindings scans every entity with at least one
// outgoing/incoming ownership edge for cycles of the given type — v7.9
// §16 "cycle bukan hanya jangan hang": treats a detected cycle as an
// explicit intelligence finding (ownership opacity elevated), not
// merely a traversal hazard silently truncated.
func (g *OwnershipGraph) CircularOwnershipFindings(ownType OwnershipType, maxHops int) []CircularOwnershipFinding {
	g.mu.RLock()
	defer g.mu.RUnlock()
	seen := map[string]bool{}
	var findings []CircularOwnershipFinding
	var starts []string
	for to := range g.edges {
		starts = append(starts, to)
	}
	sort.Strings(starts)
	for _, start := range starts {
		var walk func(cur string, path []string, effect float64, hop int)
		walk = func(cur string, path []string, effect float64, hop int) {
			if hop > maxHops {
				return
			}
			for _, e := range g.edges[cur] {
				if e.Type != ownType {
					continue
				}
				if e.FromID == start {
					cycle := append(append([]string{}, path...), e.FromID)
					key := fmt.Sprint(cycle)
					if !seen[key] {
						seen[key] = true
						findings = append(findings, CircularOwnershipFinding{Cycle: cycle, Confidence: effect * e.Percent})
					}
					continue
				}
				var alreadyOnPath bool
				for _, p := range path {
					if p == e.FromID {
						alreadyOnPath = true
						break
					}
				}
				if alreadyOnPath {
					continue
				}
				walk(e.FromID, append(append([]string{}, path...), e.FromID), effect*e.Percent, hop+1)
			}
		}
		walk(start, []string{start}, 1.0, 0)
	}
	sort.Slice(findings, func(i, j int) bool { return fmt.Sprint(findings[i].Cycle) < fmt.Sprint(findings[j].Cycle) })
	return findings
}

// ValidateTotality reports every (toID, OwnershipType) whose direct
// (one-hop) edges sum to more than 1.0 — v7.9 §13/§15's explicit ask
// that structurally-impossible ownership totals be surfaced, not
// silently accepted or silently capped.
func (g *OwnershipGraph) ValidateTotality() []TotalityViolation {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []TotalityViolation
	var toIDs []string
	for to := range g.edges {
		toIDs = append(toIDs, to)
	}
	sort.Strings(toIDs)
	for _, to := range toIDs {
		sums := map[OwnershipType]float64{}
		for _, e := range g.edges[to] {
			sums[e.Type] += e.Percent
		}
		var types []string
		for t := range sums {
			types = append(types, string(t))
		}
		sort.Strings(types)
		for _, ts := range types {
			t := OwnershipType(ts)
			if sums[t] > 1.0+1e-9 {
				out = append(out, TotalityViolation{ToID: to, Type: t, Total: sums[t]})
			}
		}
	}
	return out
}

// DirectOwners returns the immediate (one-hop) CURRENT (post-
// supersession) owners of targetID across all OwnershipTypes, sorted
// deterministically by owner ID then type.
func (g *OwnershipGraph) DirectOwners(targetID string) []OwnershipEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]OwnershipEdge, len(g.edges[targetID]))
	copy(out, g.edges[targetID])
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromID != out[j].FromID {
			return out[i].FromID < out[j].FromID
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// DirectOwnersAsOf returns the CURRENT-AT-TICK (0(1) log replay,
// deterministic) set of active owners of targetID as of a given
// historical tick — the auditor's explicit §19 ask: "ownership graph
// harus G(t), bukan hanya G(current)." Re-derives the answer entirely
// from the immutable ledger (never touches the live g.edges
// materialization), so a query for an old tick is bit-reproducible
// from ledger history alone — the same replay discipline the rest of
// this kernel (WAL, evidence store, raft snapshots) already applies.
func (g *OwnershipGraph) DirectOwnersAsOf(targetID string, ownType OwnershipType, tick uint64) []OwnershipEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	// active[(from,to,type)] -> most recent record with Tick<=tick.
	type key struct {
		from, to string
		t        OwnershipType
	}
	active := map[key]ownershipRecord{}
	for _, rec := range g.log {
		if rec.Tick > tick || rec.ToID != targetID || rec.Type != ownType {
			continue
		}
		k := key{rec.FromID, rec.ToID, rec.Type}
		if cur, ok := active[k]; !ok || rec.Index > cur.Index {
			active[k] = rec
		}
	}
	out := make([]OwnershipEdge, 0, len(active))
	for _, rec := range active {
		out = append(out, OwnershipEdge{FromID: rec.FromID, Percent: rec.Percent, Type: rec.Type})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FromID < out[j].FromID })
	return out
}

// Head returns the current ownership-ledger hash chain head.
func (g *OwnershipGraph) Head() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.head
}
