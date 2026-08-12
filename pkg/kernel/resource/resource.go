// Package resource implements Veriqo's Resource Manager — gap #8 in
// "Kritik_terbesar_nomor_1.docx": "Saya ingin nanti Memory quota, CPU
// quota, Priority, Bandwidth, Storage, Worker pool, Engine quota, GPU.
// Semua bisa dikontrol Kernel."
//
// Honest scope: this is a deterministic, in-process accounting layer —
// it tracks and enforces declared quotas per engine (reserve/release
// against a budget) so the Kernel Runtime can refuse to start an engine
// that would exceed cluster capacity, and can rank engines by Priority
// under contention. It does NOT enforce real OS-level limits (no
// cgroups/rlimit syscalls) — that is explicitly a separate, later
// infra-layer integration and is documented as open in the report.
package resource

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrOverBudget   = errors.New("resource: reservation exceeds available budget")
	ErrUnknownGrant = errors.New("resource: no active grant for this holder+kind")
)

// Kind is a resource dimension the Manager tracks.
type Kind string

const (
	Memory    Kind = "MEMORY"    // abstract units, e.g. MB
	CPU       Kind = "CPU"       // abstract shares
	Worker    Kind = "WORKER"    // worker pool slots
	Storage   Kind = "STORAGE"   // abstract units, e.g. MB
	Bandwidth Kind = "BANDWIDTH" // abstract units, e.g. KB/s
	GPU       Kind = "GPU"       // GPU slots
)

// Budget is the cluster-wide capacity for one Kind.
type Budget struct {
	Kind  Kind
	Total uint64
}

// Grant records one holder's active reservation of one Kind.
type Grant struct {
	Holder   string
	Kind     Kind
	Amount   uint64
	Priority int // higher = more important; used to rank under contention
}

// Manager tracks budgets and active grants, enforcing that the sum of
// active grants for a Kind never exceeds its budget.
type Manager struct {
	mu      sync.Mutex
	budgets map[Kind]uint64
	used    map[Kind]uint64
	grants  map[string]Grant // key: holder|kind
}

func NewManager() *Manager {
	return &Manager{
		budgets: make(map[Kind]uint64),
		used:    make(map[Kind]uint64),
		grants:  make(map[string]Grant),
	}
}

// SetBudget declares (or updates) the total capacity for a Kind.
func (m *Manager) SetBudget(k Kind, total uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.budgets[k] = total
}

func grantKey(holder string, k Kind) string { return holder + "|" + string(k) }

// Reserve grants `amount` of `k` to `holder` if the remaining budget
// allows it, recording `priority` for contention ranking. Idempotent
// per (holder,kind): calling Reserve again for the same pair replaces
// the prior grant (releasing it first), so callers can resize a
// reservation without a manual Release+Reserve pair.
func (m *Manager) Reserve(holder string, k Kind, amount uint64, priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if amount == 0 {
		// A zero-amount grant reserves nothing but occupies a grant slot
		// and reports as an active holder, which makes accounting and
		// eviction ranking lie. Refusing it is cheaper than special-casing
		// it everywhere downstream.
		return fmt.Errorf("%w: holder=%s kind=%s amount=0", ErrOverBudget, holder, k)
	}
	key := grantKey(holder, k)
	if existing, ok := m.grants[key]; ok {
		m.used[k] -= existing.Amount
		delete(m.grants, key)
	}
	total := m.budgets[k]
	if m.used[k]+amount > total {
		// Restore accounting state on rejection is a no-op since we
		// haven't added the new amount yet; nothing to roll back.
		return fmt.Errorf("%w: holder=%s kind=%s want=%d used=%d total=%d",
			ErrOverBudget, holder, k, amount, m.used[k], total)
	}
	m.used[k] += amount
	m.grants[key] = Grant{Holder: holder, Kind: k, Amount: amount, Priority: priority}
	return nil
}

// Release frees a holder's grant for one Kind.
func (m *Manager) Release(holder string, k Kind) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := grantKey(holder, k)
	g, ok := m.grants[key]
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrUnknownGrant, holder, k)
	}
	m.used[k] -= g.Amount
	delete(m.grants, key)
	return nil
}

// ReleaseAll frees every grant held by holder, across all Kinds.
func (m *Manager) ReleaseAll(holder string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, g := range m.grants {
		if g.Holder == holder {
			m.used[g.Kind] -= g.Amount
			delete(m.grants, key)
		}
	}
}

// Available returns the remaining unreserved budget for a Kind.
func (m *Manager) Available(k Kind) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.budgets[k]
	used := m.used[k]
	if used >= total {
		return 0
	}
	return total - used
}

// Utilization returns used/total as a fraction in [0,1] for a Kind
// (0 if no budget is set).
func (m *Manager) Utilization(k Kind) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.budgets[k]
	if total == 0 {
		return 0
	}
	return float64(m.used[k]) / float64(total)
}

// RankByPriority returns holders that currently hold a grant of Kind
// k, sorted by descending Priority (ties broken by holder name) — used
// by the Mission Controller to decide who gets preempted first under
// contention.
func (m *Manager) RankByPriority(k Kind) []Grant {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Grant
	for _, g := range m.grants {
		if g.Kind == k {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Holder < out[j].Holder
	})
	return out
}

// GrantOf returns a holder's active grant for a Kind, if any.
func (m *Manager) GrantOf(holder string, k Kind) (Grant, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.grants[grantKey(holder, k)]
	return g, ok
}
