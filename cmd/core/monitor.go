package main

import (
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/monitor"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/web"
)

// backupStatus is the backup service as the monitor asks it: nil (not a nil value in a non-nil
// interface) when the backup service is not installed.
func backupStatus(b web.Backups) domain.BackupStatus {
	if b == nil {
		return nil
	}
	return b
}

// monitorLogs are the services' log sources as the monitor reads them (core's own is added there).
func monitorLogs(in map[string]web.LogSource) map[string]monitor.LogSource {
	out := make(map[string]monitor.LogSource, len(in))
	for name, src := range in {
		if src != nil {
			out[name] = src
		}
	}
	return out
}
