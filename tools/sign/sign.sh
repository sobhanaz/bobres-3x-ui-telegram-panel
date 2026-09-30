#!/usr/bin/env bash
# Usage: sign.sh <file-to-sign> <private-key.pem>   -> writes <file>.sig (raw Ed25519 signature)
set -euo pipefail
[ "$#" -eq 2 ] || { echo "usage: $0 <file> <private-key.pem>" >&2; exit 2; }
file=$1; key=$2
[ -f "$file" ] || { echo "no such file: $file" >&2; exit 1; }
[ -f "$key" ]  || { echo "no such key: $key" >&2; exit 1; }
openssl pkeyutl -sign -inkey "$key" -rawin -in "$file" -out "${file}.sig"
echo "signed: ${file}.sig"
