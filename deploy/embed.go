// Package deploy embeds the files an install runs from (compose stack, Caddy
// gateway, database init script and the environment template), so the bobres
// CLI can write them on a server without a source checkout.
package deploy

import _ "embed"

// ComposeFile is docker-compose.yml.
//
//go:embed docker-compose.yml
var ComposeFile []byte

// Caddyfile is the gateway's main configuration, caddy/Caddyfile in an
// install. It is static: the HTTPS files it imports (tls/global.caddy and
// tls/sites.caddy) are rendered by internal/tlsconf.
//
//go:embed caddy/Caddyfile
var Caddyfile []byte

// PostgresInit creates one role and one schema per service.
//
//go:embed postgres-init.sh
var PostgresInit []byte

// EnvExample documents every environment variable.
//
//go:embed .env.example
var EnvExample []byte

// UpdaterPathUnit is bobres-updater.path, the systemd unit that starts the
// updater when the dashboard drops an update request. @DIR@ stands for the
// install directory; bobres install fills it in.
//
//go:embed systemd/bobres-updater.path
var UpdaterPathUnit []byte

// UpdaterServiceUnit is bobres-updater.service, which runs
// `bobres update --from-request`. @DIR@ is the install directory, @EXE@ the
// bobres binary, @EXEDIR@ its folder and @STATE@ the release state directory.
//
//go:embed systemd/bobres-updater.service
var UpdaterServiceUnit []byte

// MonitorServiceUnit is bobres-monitor.service, which runs
// `bobres monitor snapshot` as root. @DIR@ is the install directory and @EXE@
// the bobres binary.
//
//go:embed systemd/bobres-monitor.service
var MonitorServiceUnit []byte

// MonitorTimerUnit is bobres-monitor.timer, which starts the service every 30
// seconds.
//
//go:embed systemd/bobres-monitor.timer
var MonitorTimerUnit []byte
