# The release chain proves integrity, not authenticity — accepted on the record

Decided 2026-07-28, from the first outside review (two reviewers raised it
independently). byre's INBOUND supply chain is meticulous: packages are
digest-verified, remote manifests are HTTPS-only with same-origin redirects,
bounded reads and path containment. The outbound chain has no equivalent, and
the asymmetry — not any single gap — was the finding.

## The facts, stated plainly

`install.sh` downloads the binary and `checksums.txt` from the same GitHub
release. That detects a corrupted download. It proves nothing about
authenticity: anyone who can publish a release publishes both files. There is
no signing, no SBOM, no build attestation. The Homebrew cask strips
`com.apple.quarantine`.

## The decision

**byre's release is exactly as trustworthy as the GitHub account that
publishes it, and that is the threat model.** This is P7's third honest move
— accept the limitation on the record — rather than a gap nobody noticed.
What the review actually established is that the DOCS overclaimed; that half
shipped 2026-07-27 (install.md now says what the checksum does and does not
prove).

Taken now, because it earns its place independently: **`-trimpath`**. It keeps
the build machine's paths out of what users receive and is the precondition
for anyone rebuilding and comparing. It is not a reproducibility claim.

**The quarantine strip stays, and is not a gap to close.** Without an Apple
Developer ID signature Gatekeeper blocks the binary outright, so the strip is
what makes the cask work at all. Recorded here so it is not re-raised as an
oversight.

## Deferred, with its trigger written down

**Build attestation** (GitHub's `attest-build-provenance`: one workflow
permission, one step, no key management, verifiable with `gh attestation
verify`). Deferred not because it is expensive but because of what it buys
today: it defends against a release uploaded with a stolen token, and not
against a compromised workflow — and nobody is currently verifying, so the
practical value is near zero until someone is.

Build it when either becomes true:

- someone asks how to verify a byre download; or
- a distribution packages byre and needs provenance.

Signing and SBOMs stay out until there is a reason beyond symmetry: keys are
a standing maintenance burden, and making `install.sh` verify by default
would put a failure mode into the one-liner that is byre's best conversion
surface.
