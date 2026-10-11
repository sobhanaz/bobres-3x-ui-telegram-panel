# License public keys

Each `<kid>.pub` file in this directory is one vendor public key, embedded in
every build: `<kid>` is the key id that tokens carry in `kid`, and the file
holds the 32-byte Ed25519 public key in standard base64 on one line (lines
starting with `#` are comments).

`go run ./tools/license keygen` prints the line to save here. The private key
it writes stays outside the repository and is never committed.

The directory has no key yet, so every token is refused as "unknown key"
(docs/05-license.md). To rotate, add the new key next to the old one, sign new
tokens with it, then remove the old file once its tokens have expired.
