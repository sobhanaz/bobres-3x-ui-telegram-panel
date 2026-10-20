package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

const (
	restoreBackupName = "bobres-20261010T033000Z.bkp"
	restoreUploadID   = "0192f9e0-7a1b-7c3d-8e4f-aaaaaaaaaaaa"
)

// restoreReq is a restore request as core writes it, changed by mods.
func restoreReq(mods ...func(map[string]any)) []byte {
	m := map[string]any{
		"v": 1, "id": reqID, "action": "restore", "source": "backup", "file": restoreBackupName, "upload": "",
		"sha256": strings.Repeat("ab", 32), "size": 1234, "by": "owner", "at": reqNow.Add(-time.Minute).Format(time.RFC3339),
	}
	for _, f := range mods {
		f(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func TestParseRestoreRequestAccepts(t *testing.T) {
	for name, tc := range map[string]struct {
		mods []func(map[string]any)
		want restoreRequest
	}{
		"a backup of the server": {nil, restoreRequest{ID: reqID, Source: "backup", File: restoreBackupName, SHA256: strings.Repeat("ab", 32), Size: 1234, By: "owner", At: reqNow.Add(-time.Minute)}},
		"a safety backup": {[]func(map[string]any){set("file", "bobres-20261010T033000Z-before-restore.bkp")},
			restoreRequest{ID: reqID, Source: "backup", File: "bobres-20261010T033000Z-before-restore.bkp", SHA256: strings.Repeat("ab", 32), Size: 1234, By: "owner", At: reqNow.Add(-time.Minute)}},
		"an upload with a Persian name": {[]func(map[string]any){set("source", "upload"), set("file", "پشتیبان من.bkp"), set("upload", restoreUploadID)},
			restoreRequest{ID: reqID, Source: "upload", File: "پشتیبان من.bkp", Upload: restoreUploadID, SHA256: strings.Repeat("ab", 32), Size: 1234, By: "owner", At: reqNow.Add(-time.Minute)}},
		"the largest backup": {[]func(map[string]any){set("size", int64(maxRestoreBytes))},
			restoreRequest{ID: reqID, Source: "backup", File: restoreBackupName, SHA256: strings.Repeat("ab", 32), Size: maxRestoreBytes, By: "owner", At: reqNow.Add(-time.Minute)}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseRestoreRequest(restoreReq(tc.mods...), reqNow)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if *got != tc.want {
				t.Errorf("got %+v, want %+v", *got, tc.want)
			}
		})
	}
}

func TestParseRestoreRequestRefuses(t *testing.T) {
	up := func(m map[string]any) { m["source"], m["upload"] = "upload", restoreUploadID }
	for name, tc := range map[string]struct {
		body []byte
		why  string
	}{
		"not JSON":                    {[]byte("restore please"), "not the JSON object"},
		"an update's fields":          {reqJSON(set("action", "restore")), "not the JSON object"},
		"a field more":                {restoreReq(set("path", "/etc/passwd")), "not the JSON object"},
		"two objects":                 {append(restoreReq(), restoreReq()...), "more in the file"},
		"over the size":               {append(restoreReq(), []byte(strings.Repeat(" ", maxRequestBytes))...), "larger than"},
		"no source":                   {restoreReq(drop("source")), `"source" is missing`},
		"no upload field":             {restoreReq(drop("upload")), `"upload" is missing`},
		"no sha256":                   {restoreReq(drop("sha256")), `"sha256" is missing`},
		"no size":                     {restoreReq(drop("size")), `"size" is missing`},
		"another action":              {restoreReq(set("action", "update")), "is not restore"},
		"another protocol":            {restoreReq(set("v", 2)), "protocol version 2"},
		"not a UUID":                  {restoreReq(set("id", "1")), "not a UUID"},
		"a path":                      {restoreReq(set("file", "../../etc/passwd")), "not a backup file name"},
		"a path in the folder":        {restoreReq(set("file", "sub/"+restoreBackupName)), "not a backup file name"},
		"a link's name":               {restoreReq(set("file", ".bobres-20261010T033000Z.bkp")), "not a backup file name"},
		"a sidecar":                   {restoreReq(set("file", "bobres-20261010T033000Z.json")), "not a backup file name"},
		"upper case tag":              {restoreReq(set("file", "bobres-20261010T033000Z-Before.bkp")), "not a backup file name"},
		"a backup with an upload id":  {restoreReq(set("upload", restoreUploadID)), "has no upload"},
		"an upload without an id":     {restoreReq(up, set("upload", "")), "not a UUID"},
		"an upload id that is a path": {restoreReq(up, set("upload", "../../x")), "not a UUID"},
		"an upload id in capitals":    {restoreReq(up, set("upload", strings.ToUpper(restoreUploadID))), "not a UUID"},
		"an empty upload name":        {restoreReq(up, set("file", "")), "file name is empty"},
		"an upload name with a break": {restoreReq(up, set("file", "a\nb.bkp")), "control character"},
		"an upload name with bidi":    {restoreReq(up, set("file", "a\u202eb.bkp")), "control character"},
		"an upload name too long":     {restoreReq(up, set("file", strings.Repeat("a", 121))), "longer than"},
		"another source":              {restoreReq(set("source", "url")), "neither backup nor upload"},
		"a short hash":                {restoreReq(set("sha256", "abcd")), "sha256"},
		"a hash in capitals":          {restoreReq(set("sha256", strings.Repeat("AB", 32))), "sha256"},
		"size zero":                   {restoreReq(set("size", 0)), "size"},
		"a negative size":             {restoreReq(set("size", -5)), "size"},
		"over the limit":              {restoreReq(set("size", int64(maxRestoreBytes)+1)), "size"},
		"a size that is text":         {restoreReq(set("size", "12")), "not the JSON object"},
		"stale":                       {restoreReq(set("at", reqNow.Add(-16*time.Minute).Format(time.RFC3339))), "older than"},
		"from the future":             {restoreReq(set("at", reqNow.Add(2*time.Minute).Format(time.RFC3339))), "future"},
		"no name":                     {restoreReq(set("by", "")), "user name"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRestoreRequest(tc.body, reqNow); err == nil || !strings.Contains(err.Error(), tc.why) {
				t.Fatalf("got %v, want an error with %q", err, tc.why)
			}
		})
	}
}

func TestRequestActionPeeksOnly(t *testing.T) {
	if requestAction(restoreReq()) != actionRestore || requestAction(reqJSON()) != actionUpdate || requestAction([]byte("x")) != actionUpdate {
		t.Fatal("wrong action")
	}
	// An update request never becomes a restore by another field's content.
	if requestAction(reqJSON(set("by", `"action":"restore"`))) != actionUpdate {
		t.Fatal("the action was read from a text")
	}
}

// restoreTest is a dashboard restore over the update tests' install.
type restoreTest struct {
	*helperTest
	n int
}

func newRestoreTest(t *testing.T) *restoreTest {
	t.Helper()
	ht := newHelperTest(t)
	ht.h.backupOwner = os.Getuid()
	return &restoreTest{helperTest: ht}
}

// putBackup writes a backup made with key into the install's backups folder and returns what core
// would send for it: the SHA-256 and the size.
func (rt *restoreTest) putBackup(path, key string, keys map[string]string, dump string) (string, int64) {
	rt.tb.Helper()
	src := writeBackup(rt.tb, key, keys, dump)
	b, err := os.ReadFile(src)
	if err != nil {
		rt.tb.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		rt.tb.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil { //nolint:gosec // the test's own temp directory
		rt.tb.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), int64(len(b))
}

// request writes a restore request for the backup in the backups folder, or (upload) for the
// upload in the upload folder, and returns its id.
func (rt *restoreTest) request(sum string, size int64, mods ...func(map[string]any)) string {
	rt.n++
	id := fmt.Sprintf("0192f9e0-7a1b-7c3d-8e4f-%012d", 1000+rt.n)
	all := append([]func(map[string]any){set("id", id), set("at", opsNow.Add(-time.Minute).Format(time.RFC3339)), set("sha256", sum), set("size", size)}, mods...)
	rt.write(restoreReq(all...))
	return id
}

func (rt *restoreTest) backupPath() string {
	return filepath.Join(rt.dir, "backups", restoreBackupName)
}

func (rt *restoreTest) uploadDir() string {
	rt.tb.Helper()
	d := uploadDirPath(rt.dir)
	if err := os.MkdirAll(d, 0o700); err != nil {
		rt.tb.Fatal(err)
	}
	return d
}

func (rt *restoreTest) unhealthyUntilRolledBack() {
	rt.db.ps = func() string {
		if len(rt.db.restored) == 1 { // the backup's data is in, the data from before is not back yet
			return strings.Replace(healthyPS, `{"Service":"core","State":"running","Health":"healthy"}`, `{"Service":"core","State":"running","Health":"starting"}`, 1)
		}
		return healthyPS
	}
}

func TestHelperRestoresABackupFromTheServerAndReportsEachStep(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP OF THE BACKUP")
	id := rt.request(sum, size)

	if code := rt.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, rt.out, rt.errOut)
	}
	rt.gone()
	st := rt.status()
	if st.State != helperDone || st.Step != stepFinished || st.ID != id || st.Action != actionRestore || st.Subject != restoreBackupName ||
		st.Source != "backup" || st.By != "owner" || st.RequestedAt == nil || *st.RequestedAt != "2026-10-11T11:59:00Z" || !st.Restore || !st.Installed {
		t.Errorf("final status: %+v", st)
	}
	if !strings.Contains(st.Message, "BOBRES now runs the data of "+restoreBackupName) || !strings.Contains(st.Message, "before-restore.bkp") {
		t.Errorf("message %q", st.Message)
	}
	if want := []string{"verify", "stop", "safety", "restore", "start", "health", "done"}; !reflect.DeepEqual(rt.steps(), want) {
		t.Errorf("steps %v, want %v", rt.steps(), want)
	}
	// The same restore as `bobres restore --yes`: stop, the safety backup, one load, up.
	if want := []string{"stop core payments provisioner bot", "dump", "restore", "up -d"}; !reflect.DeepEqual(rt.db.steps, want) {
		t.Errorf("steps %q, want %q", rt.db.steps, want)
	}
	if len(rt.db.restored) != 1 || rt.db.restored[0] != "DUMP OF THE BACKUP" {
		t.Errorf("restored %q", rt.db.restored)
	}
	// The safety backup of the current data is there, and the master keys are the backup's.
	safety := filepath.Join(rt.dir, "backups", "bobres-20261011T120000Z-before-restore.bkp")
	if _, dump := openBackup(t, safety, installKey); dump != "DUMP BEFORE THE UPDATE" {
		t.Errorf("safety backup holds %q", dump)
	}
	if env, _, _ := readEnv(filepath.Join(rt.dir, ".env")); env["BOBRES_CORE_MASTER_KEY"] != backupKeys()["BOBRES_CORE_MASTER_KEY"] {
		t.Errorf("core master key %q", env["BOBRES_CORE_MASTER_KEY"])
	}
	// The helper's private copy is gone, and the original is as it was.
	if m, _ := filepath.Glob(filepath.Join(rt.dir, "backups", ".dashboard-restore-*")); len(m) != 0 {
		t.Errorf("the private copy is left: %v", m)
	}
	if b, _ := os.ReadFile(rt.backupPath()); int64(len(b)) != size {
		t.Error("the backup changed")
	}
	if b, _ := os.ReadFile(filepath.Join(rt.dir, handledFileName)); !strings.Contains(string(b), id) {
		t.Errorf("the id was not recorded: %q", b)
	}
	log := strings.Join(st.Log, "\n")
	for _, w := range []string{"Restore requested by owner from the dashboard", "checked: key and contents are good", "Database restored", "All services are healthy"} {
		if !strings.Contains(log, w) {
			t.Errorf("the log lacks %q:\n%s", w, log)
		}
	}
	for _, secret := range []string{installKey, "backup-core-master-key", "install-core-master-key"} {
		if strings.Contains(log+rt.out.String()+rt.errOut.String(), secret) {
			t.Errorf("a secret is shown (%s)", secret)
		}
	}
	if len(rt.snaps) == 0 || rt.snaps[0].Step != stepVerifying || rt.snaps[0].State != helperRunning || rt.snaps[0].Action != actionRestore {
		t.Errorf("the first status: %+v", rt.snaps)
	}
}

func TestHelperRestoresAnUploadAndRemovesIt(t *testing.T) {
	rt := newRestoreTest(t)
	path := filepath.Join(rt.uploadDir(), restoreUploadID+".bkp")
	sum, size := rt.putBackup(path, installKey, backupKeys(), "DUMP OF THE UPLOAD")
	rt.request(sum, size, set("source", "upload"), set("upload", restoreUploadID), set("file", "my backup.bkp"))

	if code := rt.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, rt.out, rt.errOut)
	}
	if st := rt.status(); st.State != helperDone || st.Source != "upload" || st.Subject != "my backup.bkp" {
		t.Errorf("final status: %+v", st)
	}
	if len(rt.db.restored) != 1 || rt.db.restored[0] != "DUMP OF THE UPLOAD" {
		t.Errorf("restored %q", rt.db.restored)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the upload is still there: %v", err)
	}
}

// What core does to its upload after the helper has copied it changes nothing: the copy is what is
// checked and restored. (The hash is of the bytes the helper read.)
func TestHelperRestoresTheCopyItMade(t *testing.T) {
	rt := newRestoreTest(t)
	path := filepath.Join(rt.uploadDir(), restoreUploadID+".bkp")
	sum, size := rt.putBackup(path, installKey, backupKeys(), "DUMP OF THE UPLOAD")
	rt.request(sum, size, set("source", "upload"), set("upload", restoreUploadID))
	// core replaces the file the moment the settle wait begins, after the copy was made
	rt.h.settle = time.Second
	rt.h.u.sleep = func(time.Duration) {
		rt.putBackup(path+".other", installKey, backupKeys(), "DUMP THAT CORE SWAPPED IN")
		if err := os.Rename(path+".other", path); err != nil {
			t.Error(err)
		}
	}
	if code := rt.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, rt.out, rt.errOut)
	}
	if len(rt.db.restored) != 1 || rt.db.restored[0] != "DUMP OF THE UPLOAD" {
		t.Errorf("restored %q", rt.db.restored)
	}
}

// Everything that is wrong with a backup is found before the first service is stopped.
func TestHelperRefusesBeforeStoppingAnything(t *testing.T) {
	good := func(rt *restoreTest) (string, int64) {
		return rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP")
	}
	for name, tc := range map[string]struct {
		prep func(rt *restoreTest) (sum string, size int64, mods []func(map[string]any))
		why  string
	}{
		"a backup of another server (another key)": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			s, n := rt.putBackup(rt.backupPath(), otherKey, backupKeys(), "DUMP")
			return s, n, nil
		}, "wrong backup key"},
		"not a backup": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			b := []byte(strings.Repeat("not a backup ", 50))
			if err := os.MkdirAll(filepath.Dir(rt.backupPath()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rt.backupPath(), b, 0o600); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			return hex.EncodeToString(sum[:]), int64(len(b)), nil
		}, "not a BOBRES backup"},
		"a hash that is not the file's": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			_, n := good(rt)
			return strings.Repeat("00", 32), n, nil
		}, "SHA-256 differs"},
		"a size that is not the file's": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			s, n := good(rt)
			return s, n + 1, nil
		}, "not the"},
		"no such file": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			return strings.Repeat("ab", 32), 10, nil
		}, "no such file"},
		"a link in the file's place": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			s, n := good(rt)
			other := filepath.Join(t.TempDir(), "other.bkp")
			if err := os.Rename(rt.backupPath(), other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, rt.backupPath()); err != nil {
				t.Fatal(err)
			}
			return s, n, nil
		}, "symbolic link"},
		"a pipe in the file's place": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			if err := os.MkdirAll(filepath.Dir(rt.backupPath()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(rt.backupPath(), 0o600); err != nil {
				t.Skip("no fifo here:", err)
			}
			return strings.Repeat("ab", 32), 10, nil
		}, "not a regular file"},
		"a directory in the file's place": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			if err := os.MkdirAll(rt.backupPath(), 0o700); err != nil {
				t.Fatal(err)
			}
			return strings.Repeat("ab", 32), 10, nil
		}, "not a regular file"},
		"a backup that belongs to someone else": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			s, n := good(rt)
			rt.h.backupOwner = os.Getuid() + 1
			return s, n, nil
		}, "does not belong"},
		"an upload in a folder others can write": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			path := filepath.Join(rt.uploadDir(), restoreUploadID+".bkp")
			s, n := rt.putBackup(path, installKey, backupKeys(), "DUMP")
			if err := os.Chmod(rt.uploadDir(), 0o770); err != nil {
				t.Fatal(err)
			}
			return s, n, []func(map[string]any){set("source", "upload"), set("upload", restoreUploadID)}
		}, "can be written by others"},
		"an upload that is not there": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			rt.uploadDir()
			return strings.Repeat("ab", 32), 10, []func(map[string]any){set("source", "upload"), set("upload", restoreUploadID)}
		}, "no such file"},
		"an upload that is a link": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			s, n := good(rt)
			if err := os.Symlink(rt.backupPath(), filepath.Join(rt.uploadDir(), restoreUploadID+".bkp")); err != nil {
				t.Fatal(err)
			}
			return s, n, []func(map[string]any){set("source", "upload"), set("upload", restoreUploadID)}
		}, "symbolic link"},
		"an upload folder that is a link": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			real := filepath.Join(t.TempDir(), "real")
			if err := os.MkdirAll(real, 0o700); err != nil {
				t.Fatal(err)
			}
			s, n := rt.putBackup(filepath.Join(real, restoreUploadID+".bkp"), installKey, backupKeys(), "DUMP")
			if err := os.Symlink(real, uploadDirPath(rt.dir)); err != nil {
				t.Fatal(err)
			}
			return s, n, []func(map[string]any){set("source", "upload"), set("upload", restoreUploadID)}
		}, "not a directory"},
		"no room on the disk": {func(rt *restoreTest) (string, int64, []func(map[string]any)) {
			s, n := good(rt)
			rt.h.u.freeGB = func(string) float64 { return 0.1 }
			return s, n, nil
		}, "not enough free disk space"},
	} {
		t.Run(name, func(t *testing.T) {
			rt := newRestoreTest(t)
			sum, size, mods := tc.prep(rt)
			id := rt.request(sum, size, mods...)
			done := make(chan int, 1)
			go func() { done <- rt.h.exec() }() // a pipe must not hold the helper
			select {
			case code := <-done:
				if code != 1 {
					t.Fatalf("exit %d", code)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the helper waited on the file")
			}
			rt.gone()
			st := rt.status()
			if st.State != helperFailed || st.Step != stepVerifying || st.ID != id || st.Action != actionRestore || !strings.Contains(st.Message, tc.why) {
				t.Errorf("status %+v, want a failure at verify with %q", st, tc.why)
			}
			// Nothing was touched: no service stopped, no backup made, nothing restored.
			if len(rt.db.steps) != 0 || len(rt.db.restored) != 0 {
				t.Errorf("steps %q restored %q", rt.db.steps, rt.db.restored)
			}
			if m, _ := filepath.Glob(filepath.Join(rt.dir, "backups", "*before-restore*")); len(m) != 0 {
				t.Errorf("a safety backup was made: %v", m)
			}
			if m, _ := filepath.Glob(filepath.Join(rt.dir, "backups", ".dashboard-restore-*")); len(m) != 0 {
				t.Errorf("the private copy is left: %v", m)
			}
			if env, _, _ := readEnv(filepath.Join(rt.dir, ".env")); env["BOBRES_CORE_MASTER_KEY"] != installMasterKeys["BOBRES_CORE_MASTER_KEY"] {
				t.Error(".env changed")
			}
		})
	}
}

func TestHelperExplainsABackupFromAnotherServer(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), otherKey, backupKeys(), "DUMP")
	rt.request(sum, size)
	rt.h.exec()
	if msg := rt.status().Message; !strings.Contains(msg, "wrong backup key") {
		t.Errorf("message %q", msg)
	}
	if !strings.Contains(strings.Join(rt.status().Log, "\n"), "--key-file") {
		t.Errorf("the log does not say how to restore a backup of another server:\n%s", strings.Join(rt.status().Log, "\n"))
	}
}

func TestHelperRestoreNeedsTheInstallsBackupKey(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP")
	env, _, _ := readEnv(filepath.Join(rt.dir, ".env"))
	delete(env, "BOBRES_BACKUP_KEY")
	if err := os.WriteFile(filepath.Join(rt.dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.request(sum, size)
	if code := rt.h.exec(); code != 1 || len(rt.db.steps) != 0 || !strings.Contains(rt.status().Message, "BOBRES_BACKUP_KEY") {
		t.Fatalf("exit %d, steps %q, %+v", code, rt.db.steps, rt.status())
	}
}

// The restored data does not come up healthy: the helper goes back to the backup of the data from
// before, which also puts the master keys back.
func TestHelperGoesBackWhenTheRestoredDataIsNotHealthy(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP OF THE BACKUP")
	id := rt.request(sum, size)
	rt.unhealthyUntilRolledBack()

	if code := rt.h.exec(); code != 1 {
		t.Fatalf("exit %d\n%s\n%s", code, rt.out, rt.errOut)
	}
	st := rt.status()
	if st.State != helperRolledBack || st.Step != stepRollingBk || st.ID != id || st.Action != actionRestore ||
		!strings.Contains(st.Message, "runs the data from before it again") {
		t.Errorf("final status: %+v", st)
	}
	if steps := rt.steps(); len(steps) < 7 || !reflect.DeepEqual(steps[:6], []string{"verify", "stop", "safety", "restore", "start", "health"}) || !contains(steps, "rollback") {
		t.Errorf("steps %v", steps)
	}
	// The backup's data went in, then the data from before (the safety backup's) came back.
	if len(rt.db.restored) != 2 || rt.db.restored[0] != "DUMP OF THE BACKUP" || rt.db.restored[1] != "DUMP BEFORE THE UPDATE" {
		t.Errorf("restored %q", rt.db.restored)
	}
	if env, _, _ := readEnv(filepath.Join(rt.dir, ".env")); !reflect.DeepEqual(masterKeys(env), installMasterKeys) {
		t.Errorf("the master keys are not this install's again: %v", masterKeys(env))
	}
	log := strings.Join(st.Log, "\n")
	if !strings.Contains(log, "Going back to the data from before the restore") || !strings.Contains(log, "back and the store is healthy") {
		t.Errorf("the log:\n%s", log)
	}
}

func TestHelperSaysWhenGoingBackDidNotFinish(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP OF THE BACKUP")
	rt.request(sum, size)
	rt.db.ps = func() string { // never healthy
		return strings.Replace(healthyPS, `{"Service":"core","State":"running","Health":"healthy"}`, `{"Service":"core","State":"running","Health":"starting"}`, 1)
	}
	if code := rt.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	st := rt.status()
	if st.State != helperFailed || st.Step != stepRollingBk || !strings.Contains(st.Message, "going back did not finish") ||
		!strings.Contains(st.Message, "sudo bobres restore backups/bobres-20261011T120000Z-before-restore.bkp --yes") {
		t.Errorf("final status: %+v", st)
	}
}

func TestHelperRestoreThatFailsLeavesTheDatabaseAndStartsTheStore(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP OF THE BACKUP")
	rt.request(sum, size)
	rt.db.restoreErr = errors.New("psql: syntax error")
	if code := rt.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	st := rt.status()
	if st.State != helperFailed || st.Step != stepRestoring || !strings.Contains(st.Message, "the restore failed") {
		t.Errorf("final status: %+v", st)
	}
	// The services are started again; the keys are the install's.
	if last := rt.db.steps[len(rt.db.steps)-1]; last != "up -d" {
		t.Errorf("steps %q", rt.db.steps)
	}
	if env, _, _ := readEnv(filepath.Join(rt.dir, ".env")); !reflect.DeepEqual(masterKeys(env), installMasterKeys) {
		t.Errorf("master keys %v", masterKeys(env))
	}
}

// A stop of the service in the middle of the restore starts the store again.
func TestHelperRestoreStoppedByASignalStartsTheStoreAgain(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP OF THE BACKUP")
	rt.request(sum, size)
	ctx, cancel := context.WithCancel(context.Background())
	rt.h.ctx = ctx
	rt.db.restoreErr = context.Canceled
	rt.h.u.pipe = func(c context.Context, d string, r io.Reader, w io.Writer, args ...string) error {
		if args[len(args)-1] == restoreScript {
			cancel()
			return c.Err()
		}
		return rt.db.pipe(c, d, r, w, args...)
	}
	if code := rt.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if last := rt.db.steps[len(rt.db.steps)-1]; last != "up -d" {
		t.Errorf("the store was not started again: %q", rt.db.steps)
	}
	if st := rt.status(); st.State != helperFailed {
		t.Errorf("status %+v", st)
	}
}

func TestHelperRestoreRefusedWhileAnotherRunHoldsTheLock(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP")
	unlock, err := lockInstall(rt.dir, opsNow)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	id := rt.request(sum, size)
	// A status that says another run is going on is not replaced by this request's.
	running := helperStatus{V: 1, Installed: true, Helper: "1.2.3", State: helperRunning, ID: "0192f9e0-7a1b-7c3d-8e4f-0000000000ff", Step: stepBackingUp}
	if err := writeHelperStatus(statusPath(rt.dir), &running); err != nil {
		t.Fatal(err)
	}
	if code := rt.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	rt.gone()
	if st := rt.status(); st.State != helperRunning || st.ID == id {
		t.Errorf("the other run's status was replaced: %+v", st)
	}
	if len(rt.db.steps) != 0 {
		t.Errorf("steps %q", rt.db.steps)
	}
}

func TestHelperRestoreRequestRunsOnce(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP")
	id := rt.request(sum, size)
	body, err := os.ReadFile(rt.requestPath())
	if err != nil {
		t.Fatal(err)
	}
	if code := rt.h.exec(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	steps := len(rt.db.steps)
	rt.write(body) // the same request comes back
	if code := rt.h.exec(); code != 0 {
		t.Fatalf("second run exit %d", code)
	}
	if len(rt.db.steps) != steps || len(rt.db.restored) != 1 {
		t.Errorf("the request ran twice: %q", rt.db.steps)
	}
	rt.gone()
	if st := rt.status(); st.ID != id || st.State != helperDone {
		t.Errorf("status %+v", st)
	}
}

func TestHelperRestoreSwitchedOffWithTheDashboardUpdates(t *testing.T) {
	rt := newRestoreTest(t)
	sum, size := rt.putBackup(rt.backupPath(), installKey, backupKeys(), "DUMP")
	rt.request(sum, size)
	env, _, _ := readEnv(filepath.Join(rt.dir, ".env"))
	env[dashboardUpdatesKey] = "off"
	if err := os.WriteFile(filepath.Join(rt.dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := rt.h.exec(); code != 1 || len(rt.db.steps) != 0 || !strings.Contains(rt.status().Message, "switched off") {
		t.Fatalf("exit %d, steps %q, %+v", code, rt.db.steps, rt.status())
	}
}

// The upload goes whatever becomes of the request that named it.
func TestHelperRemovesTheUploadOfARequestItRefuses(t *testing.T) {
	rt := newRestoreTest(t)
	path := filepath.Join(rt.uploadDir(), restoreUploadID+".bkp")
	sum, size := rt.putBackup(path, installKey, backupKeys(), "DUMP")
	rt.request(sum, size, set("source", "upload"), set("upload", restoreUploadID), set("at", opsNow.Add(-time.Hour).Format(time.RFC3339)))
	if code := rt.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the upload of a stale request is still there: %v", err)
	}
	if !strings.Contains(rt.status().Message, "The restore request was refused") {
		t.Errorf("message %q", rt.status().Message)
	}
}

func TestCommandLineRestoreHasNoAutomaticRollback(t *testing.T) {
	dir := restoreInstall(t)
	file := writeBackup(t, installKey, backupKeys(), "DUMP OF THE BACKUP")
	f := &fakeDB{dump: "CURRENT"}
	f.ps = func() string {
		return strings.Replace(healthyPS, `{"Service":"core","State":"running","Health":"healthy"}`, `{"Service":"core","State":"running","Health":"starting"}`, 1)
	}
	o, _, errOut := dbOps("", f)
	if code := o.restoreCmd([]string{"--dir", dir, "--yes", file}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if len(f.restored) != 1 || !strings.Contains(errOut.String(), "To go back to the data from before: bobres restore") {
		t.Errorf("restored %q\n%s", f.restored, errOut)
	}
}

func TestSafetyBackupNameIsOneTheDashboardMayRestore(t *testing.T) {
	for _, tag := range []string{"before-restore", "before-rotation"} {
		if name := safetyName(opsNow, tag); !backup.ValidFileName(name) {
			t.Errorf("%s is not a name the dashboard lists and restores", name)
		}
	}
}
