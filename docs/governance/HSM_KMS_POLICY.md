# Production HSM/KMS Integration — Spec

Status: **specification against existing code**, not a new
implementation. Closes the `hsm_kms` gate's engineering column; the
gate itself stays `BLOCKED_EXTERNAL` because a real HSM or cloud KMS
**tenancy** is a procurement action this document cannot substitute
for (see `docs/governance/EXTERNAL_QUALIFICATION_POLICY.md`).

## 1. What already exists (do not rebuild)

`pkg/platform/security/keys.KeyProvider` is the audit's required
abstraction, already implemented and tested:

```go
type KeyProvider interface {
    Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error)
    PublicKey(ctx context.Context, keyID string) ([]byte, error)
}
```

This interface is already the correct shape for a real HSM/KMS
adapter, and already satisfies the audit's hardest requirement by
construction: **`Sign` takes a digest and returns a signature — no
method on this interface can return raw private key material.** A
conforming adapter therefore cannot leak the key through its own API
surface no matter how it's implemented; the private key never enters
this process's memory as exportable material as long as the adapter
calls out to the real HSM/KMS for the `Sign` operation rather than
caching key bytes locally.

`keys.Manager` (wrapping any `KeyProvider`) already implements the
full lifecycle: `PENDING → ACTIVE → RETIRED → REVOKED`, rotation with
predecessor chaining, expiry, and the "a signature made while a key
was ACTIVE stays valid after rotation; a signature made by a REVOKED
key is invalid retroactively" distinction the audit specifically
named. `MemoryKeyProvider` and `FileKeyProvider` are the reference
software implementations; a real adapter plugs in beside them without
`Manager` changing at all.

## 2. What a real adapter must do

Implement `KeyProvider` against one of:

| Provider | Sign endpoint | Auth |
|---|---|---|
| AWS KMS | `kms:Sign` (`POST` to `kms.<region>.amazonaws.com`, `X-Amz-Target: TrentService.Sign`) | SigV4 |
| GCP Cloud KMS | `POST https://cloudkms.googleapis.com/v1/{name=projects/*/locations/*/keyRings/*/cryptoKeys/*/cryptoKeyVersions/*}:asymmetricSign` | OAuth2 bearer |
| Azure Key Vault | `POST https://{vault}.vault.azure.net/keys/{key}/{version}/sign?api-version=7.4` | OAuth2 bearer |
| PKCS#11 HSM | vendor's PKCS#11 library, `C_Sign` | slot PIN / token login |

Each adapter's `Sign(ctx, keyID, digest)`:
1. Maps `keyID` (this system's logical key name, e.g.
   `"release-key-1"`) to the provider's real key resource path — via a
   small local config map, never a naming convention that lets a
   caller-supplied `keyID` string reach the provider's API unvalidated.
2. Calls the provider's real signing API with the digest (pre-hashed
   here — providers are told the digest algorithm, e.g. SHA-256, and
   asked to sign the digest directly, not re-hash it).
3. Returns the raw signature bytes, or a typed error distinguishing
   `unavailable` / `timeout` / `permission_denied` / `key_not_found`
   / `key_disabled` from a generic failure (`keys.ErrUnknownKey` etc.
   already cover the local-state half of this; the adapter owns
   translating the provider's real error into these).

`PublicKey(ctx, keyID)` calls the provider's `GetPublicKey` /
`GetKey` equivalent and returns the DER- or raw-encoded public key —
never a private key, never a key material export (all three cloud
providers refuse to export HSM-backed private key material by design;
a PKCS#11 HSM adapter must never call `C_GetAttributeValue` for
`CKA_VALUE` on a private key handle).

## 3. Fail-closed rule (non-negotiable)

If the real HSM/KMS cannot sign — unavailable, timeout, permission
denied, network partition — `Sign` returns an error. **There is no
fallback to a software key inside a production adapter.** A
deployment that wants a fallback path implements it explicitly, above
this interface, as a conscious operational decision with its own
audit trail — never inside the adapter silently.

## 4. Required tests before this gate can move past `BLOCKED_EXTERNAL`

Sign · verify · rotation (old-key signatures still verify, new key
signs) · revocation (revoked key's new signatures rejected, but
historical ones from before revocation are handled per the retention
policy) · KMS unavailable · timeout · permission denied · key
unavailable · network partition · process restart (adapter
re-authenticates cleanly) · multi-node concurrent signing (no state
race in the local `keyID → resource` map).

## 5. Closure evidence

Real closure requires: a procured tenancy (AWS/GCP/Azure account or
HSM allocation), the adapter above wired to it, the test list in §4
run for real against that tenancy, and a submission through
`pkg/governance/qualification` — `evidence/external/hsm_kms.json`,
signed by the cloud provider's account owner (`ProviderID` registered
in `docs/governance/TRUSTED_EVIDENCE_PROVIDERS.json` as
`provider_type: cloud_provider` or `hsm_vendor`) and an internal
reviewer, bound to the exact release commit under test.
