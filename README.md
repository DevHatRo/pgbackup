# pgbackup

A single static binary replacing the classic `pg_backup_rotated.sh` /
`pg_backup.sh` shell scripts. It shells out to the standard `pg_dump` /
`pg_dumpall` / `psql` clients and does gzip in-process. The only library
dependency is a YAML parser for the config.

## Build

```bash
make build       # native binary -> bin/pgbackup (version/build-time injected)
make build-all   # cross-compile all targets -> bin/  (./scripts/build.sh)
make check       # gofmt + go vet + go test
make sec         # gosec security scan (HIGH severity gate)
./bin/pgbackup -version
```

`make build-all` / `scripts/build.sh` produce: `linux/{amd64,arm64,arm}`,
`darwin/{amd64,arm64}`, `windows/amd64`.

## Configure

Copy `pg_backup.example.yaml` to `pg_backup.yaml` and edit it. The DB password is
**never** in the config — use `PGPASSWORD` or `~/.pgpass` (mode 600), exactly
like libpq:

```
# ~/.pgpass  ->  hostname:port:database:username:password
localhost:12854:*:postgres:SECRET
```

### Configuration reference

Every option, its default, and meaning. Omitted keys take their default; unknown
keys are rejected. `//`-style comments aren't allowed (it's YAML — use `#`).

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `host` | string | `localhost` | PostgreSQL host passed to `-h`. |
| `port` | int | `5432` | PostgreSQL port passed to `-p`. Must be 1–65535. |
| `username` | string | `postgres` | Role passed to `-U` for every client. |
| `database` | string | `postgres` | Maintenance DB `psql` connects to when listing databases. |
| `backup_user` | string | `""` | If set, the process must run as this OS user or it aborts. `""` skips the check. |
| `backup_dir` | string | — (**required**) | Root dir; dated tier dirs like `2026-06-04-daily` are created under it. |
| `log_dir` | string | `/var/log/pgbackup` | Where dated log files (`pgbackup-<date>.log`) go. `""` or `-` = stderr only. The `-log-dir` flag overrides this. |
| `schema_only_list` | list of regex | `[]` | DBs whose name matches any regex get a **schema-only** dump and are excluded from full dumps. e.g. `system_log` matches `dev_system_log_2010`. |
| `enable_globals_backups` | bool | `true` | Dump cluster globals (roles, tablespaces) via `pg_dumpall -g` → `globals.sql.gz`. |
| `enable_plain_backups` | bool | `true` | Produce gzipped plain-SQL dumps (`<db>.sql.gz`, restore with `gunzip \| psql`). |
| `enable_custom_backups` | bool | `false` | Produce custom-format dumps (`<db>.custom`, restore with `pg_restore`). |
| `include_create_database` | bool | `true` | Add `pg_dump --create` to full dumps so each one recreates its database (owner, encoding, locale, DB-level grants) on restore. Does not affect schema-only dumps. |
| `day_of_week_to_keep` | int 1–7 | `7` | Weekday (1=Mon … 7=Sun) whose run becomes the **weekly** backup. |
| `days_to_keep` | int | `7` | Daily backups younger than this many days are kept; older are pruned. |
| `weeks_to_keep` | int | `4` | Weekly backups younger than this many weeks are kept. |
| `months_to_keep` | int | `0` | Monthly backups younger than this many months are kept. **`0` = keep forever.** |
| `timeout` | duration | `""` | Overall deadline for one run, e.g. `2h`, `90m`. `""` or `0` = no limit. |

At least one of `enable_plain_backups` / `enable_custom_backups` must be `true`.
The DB password is intentionally **not** a config key — see above.

## Run

```bash
./pgbackup -config pg_backup.yaml            # tier auto-selected from today's date
./pgbackup -config pg_backup.yaml -dry-run   # show what it would dump/delete
./pgbackup -config pg_backup.yaml -tier daily   # force a tier (replaces pg_backup.sh)
./pgbackup -config pg_backup.yaml -json      # JSON logs for ingestion
```

Tier auto-selection (same schedule as the bash script): the **1st of the month**
→ monthly, the configured **`day_of_week_to_keep`** → weekly, otherwise → daily.

## Logging

By default the full run is written to `/var/log/pgbackup/pgbackup-<date>.log`
(set `log_dir` in the config or pass `-log-dir`; `""`/`-` = stderr only). The
binary creates the directory if it can; if it can't (e.g. not root), it falls
back to stderr and says so.

stderr is treated by destination:

- **Interactive (TTY):** full INFO+ output also echoes to the terminal.
- **Cron / systemd (non-TTY):** only WARN+ goes to stderr — so cron emails you
  on problems, not on every successful run. Everything still lands in the file.

## Cron

No redirection needed — the binary owns its log file, and cron only sees
warnings/errors:

```cron
0 1 * * *  /var/lib/backup/scripts/pgbackup -config /var/lib/backup/scripts/pg_backup.yaml
```

## Restore

Each dated directory (`2026-06-04-daily/` etc.) is a complete, independent
snapshot. Plain dumps are `gzip(SQL)` — **not** tar/zip; use `gunzip`, never
`tar` (on Windows, 7-Zip opens `.gz`).

### Single database

With `include_create_database: true` (the default), each full dump already
contains `CREATE DATABASE`, so restore it against the maintenance DB and it
creates itself:

```bash
gunzip -c mydb.sql.gz | psql -v ON_ERROR_STOP=1 \
    -h host -p 12854 -U postgres -d postgres            # plain, self-creating
pg_restore --create -d postgres \
    -h host -p 12854 -U postgres mydb.custom            # custom (-Fc)
```

If `include_create_database` is `false`, create the DB first
(`createdb -O owner mydb`) and restore into it (`… -d mydb`). Note: with
`--create`, the target DB must **not** already exist (drop it first to replace).

### Full cluster (disaster recovery)

Restore globals **first** — roles must exist before databases that reference them:

```bash
# 1. roles, passwords, tablespaces
gunzip -c globals.sql.gz | psql -h host -p 12854 -U postgres -d postgres

# 2. each database (self-creating with the default --create)
for f in *.sql.gz; do
  [ "$f" = globals.sql.gz ] && continue
  gunzip -c "$f" | psql -v ON_ERROR_STOP=1 -h host -p 12854 -U postgres -d postgres
done
```

`globals.sql.gz` contains password **hashes** — protect it; gzip is not
encryption.
