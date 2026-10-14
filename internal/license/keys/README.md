# License public keys

Each `<kid>.pub` file in this directory is one vendor public key, embedded in
every build: `<kid>` is the key id that tokens carry in `kid`, and the file
holds the 32-byte Ed25519 public key in standard base64 on one line (lines
starting with `#` are comments).

`go run ./tools/license keygen` prints the line to save here. The private key
it writes stays outside the repository and is never committed.

Keys in this directory:

| Key id | Fingerprint | Use |
|---|---|---|
| `vendor-v1-2026` | `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7` | Signs every license token: by hand (`tools/license sign`) and in the vendor license server (`licensesrv`) |

`go run ./tools/license fingerprint internal/license/keys/vendor-v1-2026.pub`
prints the fingerprint again, to compare it with the one the vendor publishes.

To rotate, add the new key next to the old one, sign new tokens with it, then
remove the old file once its tokens have expired (docs/13-license-server.md,
"Rotating the signing key"). A build with no key at all refuses every token as
"unknown key".
