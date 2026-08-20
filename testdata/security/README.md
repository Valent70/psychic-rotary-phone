# testdata/security

`certificates/test-public-key.json` and `signatures/test-manifest.sig.json`
are a REAL, freshly-generated Ed25519 keypair's public half and a REAL
signature it produced over `certificates/test-manifest.json`, created in
this session with Go's standard `crypto/ed25519` (see this pack's
top-level README for how to reproduce). The signature independently
verifies (`ed25519.Verify(pub, manifest, sig) == true`, checked in this
same session). The private key was never written to disk — it existed
only in the generating process's memory and was discarded, so this
fixture is genuinely a public-verifiable artifact, not a secret checked
into the repo.

This key is a **TEST fixture only**. It has no relationship to any
production signing key (`pkg/platform/security/keys`,
`cmd/veriqo-release-keygen`) and must never be trusted by any real
verifier.
