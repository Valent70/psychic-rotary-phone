package raftlite

import (
	"veriqo/pkg/kernel/distributed"
)

// DistributedAdapter implements raftlite.FSM by handing each committed
// LogEntry's raw Command bytes to a distributed.Registry — this is the
// "mechanical, same shape as kg.Adapter" wiring that pkg/kernel/distributed's
// docstring named as the explicit open item. With this adapter, a real
// raftlite.Node (driven by the real mTLS rafttcp.Transport, across real OS
// processes) replicates cluster membership and Engine placement decisions
// with the same guarantees as the KG adapter: committed log entries are
// applied in order, exactly once, and hash-chained by the Registry itself
// for tamper-evidence independent of raft's own log.
type DistributedAdapter struct {
	Registry *distributed.Registry
}

func NewDistributedAdapter(r *distributed.Registry) *DistributedAdapter {
	return &DistributedAdapter{Registry: r}
}

// Apply decodes nothing itself — distributed.Registry.ApplyCommand already
// accepts the exact wire format Command.Encode() produces, so the adapter's
// only job is threading the raft log index through and treating a heartbeat
// / no-op entry (empty Command) as a successful no-op, matching KGAdapter's
// convention.
func (a *DistributedAdapter) Apply(entry LogEntry) error {
	if len(entry.Command) == 0 {
		return nil
	}
	return a.Registry.ApplyCommand(entry.Index, entry.Command)
}

// EncodeDistributedCommand is the Propose()-side convenience helper,
// mirroring EncodeUpsertNode/EncodeUpsertEdge for the KG adapter.
func EncodeDistributedCommand(cmd distributed.Command) []byte {
	return cmd.Encode()
}
