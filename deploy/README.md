# Running La Mordida on the Raspberry Pi

The target is a Raspberry Pi 4 with a 64-bit Debian (aarch64), systemd and
SQLite from the binary itself. The binary is static (`CGO_ENABLED=0`), so the
Pi needs no Go toolchain and no shared SQLite library.

Everything that changes lives outside the binary and outside the checkout:

| Path | What | Owner |
| --- | --- | --- |
| `/opt/la-mordida/contratos` | the static aarch64 binary | root |
| `/opt/la-mordida/*.sh` | backup, restore, report and historical loader | root |
| `/etc/la-mordida/contratos.env` | database, objects, listen address, backup root | root |
| `/etc/la-mordida/channels/<channel>.env` | the feed of each channel | root |
| `/var/lib/la-mordida/contratos.db` | SQLite in WAL mode (+ `-wal`, `-shm`) | `contratos` |
| `/var/lib/la-mordida/objects/<2>/<sha256>` | the original documents | `contratos` |
| `/var/lib/la-mordida/archives/` | the downloaded PLACSP ZIPs and their manifest | `contratos` |
| `/var/backups/la-mordida/<stamp>/` | one coherent backup | `contratos` |

The service runs as the unprivileged `contratos` user with `ProtectSystem=strict`
and only `/var/lib/la-mordida` writable. The binary is read-only at runtime.

## Build and install

```sh
make build-pi                         # bin/contratos-linux-arm64, static
scp bin/contratos-linux-arm64 pi4:/tmp/contratos
scp -r deploy pi4:/tmp/deploy
ssh pi4 'sudo CONTRATOS_BINARY=/tmp/contratos /tmp/deploy/install.sh'
```

`install.sh` is idempotent. It never overwrites an existing
`/etc/la-mordida/contratos.env`, the database or the objects; re-running it
replaces the binary and the unit files. It enables `contratos-serve.service`,
`contratos-archive.timer` and `contratos-backup.timer`. The import timers are
installed but not enabled: they need a cursor, and the cursor comes from the
first historical window (below).

## One base, rebuilt when the selection changes

The importer keeps only the units that carry a CPV of the selected families
(`302`, `48`, `72`): an expediente with no selected lot leaves no row, no
amount and no document, and the count of what was left out and why is kept per
span. That selection changed the schema and the meaning of a base, so a base
loaded before it is **not** migrated: `install.sh` keeps the old tree and the
card that changed the selection rebuilds it by loading the windows again. The
old tree stays where it was until the new one covers it; nothing is deleted by
`install.sh`.

## First batch, then windows from 2020

Do not start with the whole history. A small window proves the reader, the
archive format and the database before hours of download:

```sh
# One month of the main channel, downloaded once and kept with its hash.
sudo -u contratos /opt/la-mordida/load-history.sh \
    -channel sindicacion_643 -from 2020-01 -until 2020-02 \
    -dir /var/lib/la-mordida/archives

# The same command for the next month, and so on. The manifest records the URL,
# the hash and the bytes of every archive that was read.
sudo -u contratos /opt/la-mordida/load-history.sh \
    -channel sindicacion_643 -from 2020-02 -until 2020-03
```

The loader builds `https://contrataciondelestado.es/sindicacion/<channel>/<family>_<YYYYMM>.zip`,
verifies the ZIP, records its SHA-256 and imports the half-open window
`[month-01, next-month-01)` with the normal `import` command. Re-running an
interrupted month reuses the archive and re-imports the window: the store's
revision identity makes the overlap a no-op.

`load-history.sh` is only a loop around `contratos import -archive`. It is
resumable by construction: an interrupted run leaves a `partial` span and the
last whole page, and the next invocation starts there.

Enable the scheduled incremental update only when the historical windows have
reached the cutover date. The feed is a recent window, not a history: with a
cursor months old, `--since-last` would read the head of the feed and record a
complete span over a period it never walked.

```sh
sudo systemctl enable --now contratos-import@sindicacion_643.timer
sudo systemctl enable --now contratos-import@sindicacion_1044.timer
sudo systemctl enable --now contratos-import@sindicacion_1143.timer
```

Each timer runs `contratos import -db ... -channel <channel> -feed <feed>
-since-last` once a day, with a 24-hour overlap. `Persistent=true` catches up
after the Pi was off; the overlap reprocesses the boundary and any late
revision.

## Interrupt, resume, restart

* `import` writes one transaction per publication and records a page only when
  it is whole. `SIGTERM` (or `systemctl stop contratos-import@...`) leaves the
  last whole page as the resume point; the span is `partial`, not lost.
* A run that dies without closing is retired after ten minutes as `partial`, so
  the next attempt is not blocked forever.
* `contratos import -coverage` prints every span of one source: `complete`,
  `partial` or `gap`, with publications, errors and pages.
* `systemctl restart contratos-serve` is safe while an import writes: SQLite
  runs in WAL mode with a bounded busy timeout and reads do not wait for the
  writer. The ranking is computed from committed rows, so a restart returns the
  same rows as before it.
* `serve` handles `SIGTERM` with a five-second graceful shutdown.

## Backup

`backup.sh` runs nightly as `contratos-backup.timer` (04:45) and can be run by
hand:

```sh
sudo -u contratos /opt/la-mordida/backup.sh
```

It:

1. copies SQLite with the online backup API into a temporary directory, checks
   `PRAGMA integrity_check` on the copy and hashes it;
2. reads the `objeto` hashes from that copy and copies exactly those files from
   the object tree, checking that each file hashes to its name and has the
   recorded size;
3. writes `manifest.txt` with the database hash and size, the object count and
   bytes, and any missing hash;
4. moves the temporary directory to `<backup>/<UTC stamp>/` and atomically
   repoints `latest`.

A writer can be active during the copy: the database snapshot is consistent and
objects are immutable and content-addressed, so an object added after the
snapshot is an extra file, never a wrong one. The manifest refuses a backup
with a missing referenced object.

## Restore into an empty path

```sh
sudo install -d -o contratos -g contratos /var/lib/la-mordida-restored
sudo -u contratos /opt/la-mordida/restore.sh \
    /var/backups/la-mordida/latest /var/lib/la-mordida-restored
```

The target must be empty. Run it as `contratos` (or as root: the script keeps
the ownership of the backup files). The script verifies the database hash and
`integrity_check`, verifies every referenced object byte for byte, copies the
database and the object tree, and prints the recovered counts. Point a second
`serve` at it to compare the rankings before replacing the live tree:

```sh
/opt/la-mordida/contratos serve \
    -db /var/lib/la-mordida-restored/contratos.db -addr 127.0.0.1:8771
```

To replace the live tree, stop `contratos-serve`, move the old tree aside, move
the restored tree into place, start the service and compare both rankings.

## Reading while an import writes

The web service and the importer are separate processes over the same database.
WAL keeps readers on the last committed state. A query during a load is answered
without waiting for the whole load; the coverage indicator may show the span as
`partial` until it closes. The evidence for this is a ranking captured before
and during an import, plus the same ranking after a restart.

## Scheduled units

| Unit | When | What |
| --- | --- | --- |
| `contratos-serve.service` | always | the two rankings over HTTP |
| `contratos-import@<channel>.timer` | 03:30 daily | `import --since-last` for one channel |
| `contratos-archive.timer` | 04:30 daily | download the pending originals |
| `contratos-backup.timer` | 04:45 daily | database and referenced objects |

`systemctl list-timers --all 'contratos-*'` shows the next run. The importer and
the archiver take the same `flock` on `/var/lib/la-mordida/write.lock`: two
writers do not starve each other into `SQLITE_BUSY`, while readers keep going
without the lock.

## Coverage report

```sh
sudo -u contratos /opt/la-mordida/report.sh
```

Prints one row per span (channel, period, kind, state, seen, accepted,
excluded, errors, pages, detail), one row per exclusion reason, one row per
channel with its totals and its last publication and import, the
month-by-month coverage of every channel, the spans that are not complete, and
the size of SQLite and the object tree. It also checks the identity
`seen = accepted + excluded + errors` span by span and says so. A period that
was never imported has no span: it is missing coverage, not a measured zero.

## What the Pi still has to state

* The platform starts some channels later than 2020 (`1044` in 2016, `1143` in
  2018), so a 2020 start is inside their published range; bodies joined
  progressively, so a body with no rows is unknown coverage, not zero.
* PLACSP serves a document only to a browser-like user agent: its WAF answers
  `Request Rejected` (a 244-byte `text/html` page) to anything else, including
  an honest `Mozilla/5.0 (compatible; la-mordida/1)` agent. The archiver
  presents a browser agent for that reason. A document that is still rejected
  is not recorded as evidence: it stays pending with its reason and the daily
  timer retries it.
* The selection is what makes the load fit: the same January 2020 archive that
  used to leave 53.447 expedientes and 195 MB leaves a few thousand rows and a
  few MB, because what is not technological is counted and dropped. The report
  shows the numbers of both sides.
* Some 2020 minor-contract entries publish no buyer identifier at all. They are
  stored under the `NAME` pseudo-scheme with the published name and the gap is
  in `testdata/placsp/gaps.md`; they are not an accredited identity.
* The full 2020-to-date load is limited by PLACSP's download speed from the
  Pi's network. Measure it before promising a deadline; the loader is built to
  be stopped and restarted one month at a time.
