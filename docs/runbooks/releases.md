# Runbook: releases and update channels

For the vendor: how to publish a stable or a beta release, what keeps a published release installable, and what to
do when it stops being so. The last section is for store owners who want to change channel. How an install verifies a
release: [06-installer.md](../06-installer.md), "Restore, update and rollback"; the keys: `tools/sign/README.md`.

## The two channels

| Channel | Follows | Used by |
|---|---|---|
| `stable` (default) | GitHub's latest release that is not a pre-release (`releases/latest/download`) | install.sh, `bobres update`, the bot's update check |
| `beta` | The highest version among the newest 30 releases GitHub's API lists (`api.github.com/repos/.../releases`), pre-releases included. A stable release counts too when it is the highest | `bobres update --channel beta`, the bot's update check |

A store picks its channel with `BOBRES_UPDATE_CHANNEL=stable|beta` in `/opt/bobres/.env`, set with
`sudo bobres config set BOBRES_UPDATE_CHANNEL beta` (see the last section). The bot reads it (the
dashboard's **System → Updates** tab shows the channel), `bobres update` and the menu read it, and
`bobres update --channel stable|beta` overrides it for one run. install.sh installs the latest stable release
unless `BOBRES_BASE_URL` points at another one, and refuses a release signed for beta unless it runs with
`BOBRES_UPDATE_CHANNEL=beta` (install.sh uses it only to accept that release; to keep the store on beta afterwards,
set it with `bobres config` too, see the last section):

```sh
curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh \
  | sudo env BOBRES_UPDATE_CHANNEL=beta \
    BOBRES_BASE_URL=https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/download/v1.5.0-rc.1 bash
```

Every release is checked the same way whatever the channel: the root-signed `keys.txt`, the release signature, the
expiry, `min_version`, the key list `seq` and the "never older than what this server installed" floor. On top of that:
- On `stable`, a release whose signed header says `channel beta` is refused, even when GitHub serves it as the latest
  (someone ran `gh release edit --latest`, unticked "pre-release", or published the tag by hand): by install.sh,
  `bobres update` and the bot's check alike.
- On `beta`, GitHub's API answer is not signed, so the release's signed header must name exactly the version the API
  listed, or it is refused.

## Publish a stable release

1. Merge everything for the release into `main`; CI is green.
2. Tag and push: `git tag v1.4.0 && git push origin v1.4.0`.
3. The Release workflow (`.github/workflows/release.yml`) runs the tests and govulncheck, builds and signs the five
   images (`latest` moves), builds the `bobres` binaries, signs `checksums.txt` with `--channel stable`, attaches
   `keys.txt` and its signatures, and creates the GitHub release.
4. Check it from any machine with a released `bobres`:

   ```sh
   bobres update --check --version 1.4.0      # signature verified, channel stable
   bobres update --check                      # "Latest: 1.4.0 (stable, signature verified)"
   ```

5. Put a reminder in the calendar 35 days out (see "Signatures expire after 45 days").

## Publish a beta (pre-release)

1. Tag with a pre-release suffix: `git tag v1.5.0-rc.1 && git push origin v1.5.0-rc.1`. Any tag with a `-` is a
   pre-release. Number them `rc.1`, `rc.2`, ... `rc.10`: numeric parts compare as numbers (`rc.10` comes after
   `rc.9`; semver 2.0), in Go (`release.Compare`) and in install.sh (`semver_lt`) alike.
2. The workflow signs it with `--channel beta`, marks the GitHub release as a pre-release, and does not move the
   `latest` image tag. Stable installs never see it.
3. Check it:

   ```sh
   bobres update --check --channel beta       # "Latest: 1.5.0-rc.1 (beta, signature verified)"
   ```

4. When the line is done, tag the final `v1.5.0`. It is newer than all its release candidates, so beta installs move
   to it too.

**Abandoned betas.** The beta channel points at the highest version listed. A pre-release line you give up (say
`v2.0.0-rc.3`) stays the highest, so beta installs keep pointing at it, and 45 days after it was signed their check
fails with "release metadata expired". When you abandon a line, delete its GitHub release (`gh release delete
v2.0.0-rc.3`; the tag can stay) or publish a higher version.

## Signatures expire after 45 days

`tools/sign/sign.sh` writes `# expires` 45 days after `# issued` (`--expires-days`, default 45), inside the signed
bytes, so an old release cannot be replayed to a server forever. The flip side: when no new release is published for
45 days, the latest stable release expires, and then:
- install.sh refuses to install ("release metadata expired ... (stale or replayed release)"): **no new store can be
  installed**;
- `bobres update` and `bobres update --check` refuse with the same reason;
- the dashboard's Updates tab says the latest release did not pass the signature check.

Running stores are not affected: nothing is checked at run time.

**Automatic re-sign.** `.github/workflows/resign.yml` does the re-sign by itself. It runs on the 1st and the 16th of each
month, takes the latest stable release, checks its files through the old signature, and, when fewer than 25 days are
left, signs them again with the release key (the `RELEASE_SIGNING_KEY` secret and the `RELEASE_KEY_ID` variable) and replaces
only `checksums.txt`, its signature and `keys.txt*` on the release. A failed run is emailed to the repository owner by
GitHub, and the next run comes 15 days later, still inside the 45 days. To rehearse it: Actions, "Re-sign the latest
release", Run workflow (it is a dry run by default: it verifies and signs but uploads nothing; tick "force" to rehearse
while the signature is fresh). GitHub disables scheduled workflows in a repository with no activity for 60 days: any
push or release counts, so check the Actions tab after a long quiet period.

The manual way below stays as the fallback (a failed run, a revoked key). A quiet stable channel needs a re-sign before day 45: either publish a patch release (the normal way, if
there is anything to ship), or re-sign the current one. To re-sign, on the machine that holds the release key
(`~/.bobres-secrets/release/`), from a checkout of the repository:

```sh
V=1.4.0
D=$(mktemp -d)
gh release download "v$V" --dir "$D" --pattern 'bobres_*' --pattern 'checksums.txt' --pattern 'checksums.txt.sig'
# 1. Trust the binaries only through the OLD signature (it may have expired; the signature itself still checks):
openssl pkeyutl -verify -pubin -inkey install/release-signing.pub.pem -rawin \
  -in "$D/checksums.txt" -sigfile "$D/checksums.txt.sig"
(cd "$D" && grep -v '^#' checksums.txt | shasum -a 256 -c -)   # sha256sum -c - on Linux
# 2. Sign them again, for the same version and channel:
mkdir "$D/dist" && mv "$D"/bobres_* "$D/dist/"
tools/sign/sign.sh --version "$V" --channel stable --key-id rel-2026a \
  --key ~/.bobres-secrets/release/rel-2026a.key.pem "$D/dist"
openssl pkeyutl -verify -pubin -inkey install/release-signing.pub.pem -rawin \
  -in "$D/dist/checksums.txt" -sigfile "$D/dist/checksums.txt.sig"
cp install/keys/keys.txt install/keys/keys.txt.sig.* "$D/dist/"
# 3. Replace the release's metadata (the binaries stay as they are):
gh release upload "v$V" "$D/dist/checksums.txt" "$D/dist/checksums.txt.sig" "$D"/dist/keys.txt* --clobber
bobres update --check --version "$V"
```

Use `--channel beta` and the pre-release's version to re-sign a beta. The key id is the one in `install/keys/keys.txt`
that is not revoked (`RELEASE_KEY_ID` in the repository's variables).

**Re-signing moves the release's date.** `sign.sh` writes the time it runs as `# issued`, and a license's
a license's advisory `updates_until` note is compared with that date (next section). A re-signed release looks as new
as the day it was re-signed, so a store whose license's update date fell between the release and the re-sign sees the
note on it. It is only a note: the release is installed all the same. `sign.sh` cannot keep the original date yet.

## Licenses and release dates (`updates_until`)

A license may carry `updates_until` (YYYY-MM-DD), the end of the updates it was sold with. It is **advisory**: security
updates are never withheld, so no release is ever refused or hidden for it. For a release issued after that day,
`bobres update` prints "Note: this release (issued on D) is newer than your license's update date (U). It is installed
all the same", `--check` prints the same note after "Newer: yes. Update with: sudo bobres update", the menu offers the
update, and the dashboard's Updates tab shows the update with its command and a line saying the release is newer than the
license's update date. The release's day is the `# issued` line of its signed `checksums.txt`, in UTC.

Only a license in force for this install has a date to compare (it verifies here, and is active or in its grace
period). A missing, invalid or ended license has none (the free tier and the trial have no `updates_until`): such a store
gets every release without a note, as does one whose release has no `# issued` line. If core cannot be asked (it is not
running), `bobres update` warns that the note is skipped and goes on.

## Before a release: the license key

A release carries the vendor's public key, `internal/license/keys/vendor-v1-2026.pub`, so licenses signed with it
verify (fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`). The test `TestEmbeddedRingHoldsTheVendorKeys` in
`internal/license` fails when a key changes. Check that `internal/license/keys/` still holds the vendor's `.pub` before
tagging. The license server that issues the licenses is separate and is deployed by the owner
(`docs/launch-checklist.md`).

## The key list expires too

`install/keys/keys.txt` has its own `expires` (now 2027-09-30T15:15Z) and every release carries a copy. Once it
expires, every release is refused, old and new. Renew it well before (a month ahead): build a new list with a higher
`--seq` and the same keys (`tools/sign/keylist.sh build`), sign it with both roots (`keylist.sh sign`), commit it, then
publish a release (or re-sign the latest one as above, which copies the new list).

## Change a store's channel (store owners)

```sh
sudo bobres config set BOBRES_UPDATE_CHANNEL beta   # or stable; checks it, writes .env, recreates the bot
sudo bobres update --check                          # "Channel: beta"
```

- `bobres config set` accepts only `stable` or `beta` and waits until the bot is healthy again. Do not edit `.env` by
  hand for it: the bot does not start with another value.
- Beta releases are less tested. Take a backup you keep off the server first (`sudo bobres backup`, then copy it).
- The beta channel lists releases through `api.github.com` (60 requests an hour per address, plenty for the bot's
  check every 12 hours and an occasional update). Where that host is filtered, the check says GitHub could not be
  reached; the stable channel only needs `github.com` downloads.
- Going back to stable never downgrades. A store on `1.5.0-rc.2` stays on it, and `bobres update` says "it stays on
  it until a stable release is newer", until `1.5.0` (or later) is out; then it updates as usual. To go back earlier,
  `sudo bobres rollback` undoes the last update (see [Update and roll back](update-rollback.md)).
- A beta that was tried and undone holds nothing back. The "never older than what this server installed" floor
  (`/var/lib/bobres/release.version`) is raised only once an update holds, so a beta update that rolled back on its
  own leaves it as it was, and `sudo bobres rollback` puts back the floor from before the update. Back on `1.4.0`
  after `1.5.0-rc.1` was undone, the stable `1.4.1` installs as usual.

## Escalate when

- A release you just published does not pass `bobres update --check` (the workflow's own signature check passed): look
  at the release's `checksums.txt` header and its assets before anyone installs it.
- The stable channel expired and no one with the release key is available: no new store can be installed until it is
  re-signed.
