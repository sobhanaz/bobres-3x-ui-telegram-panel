# Release signing

Releases are signed with an Ed25519 key. The installer verifies `checksums.txt.sig` against
the public key embedded in `install/install.sh` before trusting any checksum or binary.

- Public key: `install/release-signing.pub.pem` (committed; also embedded in install.sh).
- Private key: NEVER committed. Keep it offline (password manager / hardware token). CI signs
  with the `RELEASE_SIGNING_KEY` repository secret.

Sign locally:

    tools/sign/sign.sh dist/checksums.txt /path/to/release-signing.key.pem

Rotate: generate a new key, ship BOTH public keys in install.sh for one release, then drop the old one.
