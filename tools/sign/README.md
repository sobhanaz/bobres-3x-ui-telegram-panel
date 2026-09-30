# Release signing

Releases are signed with an Ed25519 key. The installer verifies `checksums.txt.sig` against
the public key embedded in `install/install.sh` before trusting any checksum or binary.

- Public key: `install/release-signing.pub.pem` (committed; also embedded in install.sh).
- Private key: NEVER committed. Keep it offline (password manager / hardware token). CI signs
  with the `RELEASE_SIGNING_KEY` repository secret.

Sign a release locally:

    tools/sign/sign.sh --version 1.2.3 --key-id rel-2026a --key /path/to/rel-2026a.key.pem dist

The signed `checksums.txt` header (version, channel, key_id, issued, expires) is inside the
signed bytes, which blocks replay of old releases.

Trust hierarchy: two offline root keys sign `keys.txt` (`tools/sign/keygen.sh roots`,
`tools/sign/keylist.sh build|sign`); a replaceable release key signs each release
(`tools/sign/keygen.sh release <id>`). Revoke a leaked release key by publishing a higher-`seq`
`keys.txt` with `revoked <id>`. CI needs secret `RELEASE_SIGNING_KEY` and variable `RELEASE_KEY_ID`.

Test: `bash tests/sign_test.sh`

install.sh embeds the two ROOT public keys, verifies `keys.txt` (root signature, expiry, seq
anti-rollback, revocation), then `checksums.txt.sig` with the listed release key, and enforces
release expiry, `min_version` and no-downgrade. State lives in `/var/lib/bobres`.
Tests: `bash tests/install_verify_test.sh`.

Private keys live in `~/.bobres-secrets` (roots/, release/). Back up both root keys in separate
places. Never commit or put roots in CI. `install/keys/` holds the signed `keys.txt`; renew it
before it expires (365 days from 2026-09-30) with a higher `--seq`.

Rotate: generate a new key, ship BOTH public keys in install.sh for one release, then drop the old one.
