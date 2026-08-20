package scale

// NodeIdentity is external audit item A7's required 9-field node
// identity record — deliberately a separate type from Node (the
// transport-level identity NodeProvider already uses and which many
// existing call sites depend on unchanged): NodeIdentity is the richer,
// audit-facing record a real deployment's provisioning system would
// populate, keyed by the same Node.ID.
type NodeIdentity struct {
	NodeID          string
	InstanceID      string
	Region          string
	AZ              string
	ImageDigest     string
	ReleaseVersion  string
	SourceHash      string
	EnvironmentHash string
	// LaunchTick is the logical tick the node was provisioned at — no
	// time.Now() anywhere in this package, matching the repository-wide
	// determinism discipline (see internal/determinism.Check).
	LaunchTick uint64
}
