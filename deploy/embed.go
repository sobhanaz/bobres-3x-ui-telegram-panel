// Package deploy embeds the files an install runs from (compose stack, Caddy
// gateway, database init script and the environment template), so the bobres
// CLI can write them on a server without a source checkout.
package deploy

import _ "embed"

// ComposeFile is docker-compose.yml.
//
//go:embed docker-compose.yml
var ComposeFile []byte

// Caddyfile is the gateway configuration.
//
//go:embed Caddyfile
var Caddyfile []byte

// PostgresInit creates one role and one schema per service.
//
//go:embed postgres-init.sh
var PostgresInit []byte

// EnvExample documents every environment variable.
//
//go:embed .env.example
var EnvExample []byte
