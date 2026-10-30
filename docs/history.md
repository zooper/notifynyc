# Searchable history (draft; no deployment performed)

## Verified base and dirty-build boundary

Implementation starts from Forgejo `zooper/notifynyc` PR #1,
`feat/slack-notifications`, exact revision
`448c34f018a3da9c4ba6ff59d0a4aafd70b667bf`. Source files were retrieved
through the authorized Forgejo connector because direct Mac clone DNS failed.
The local import is a snapshot, not a claim to have recovered upstream Git history.
Main source Git blob verified as `3888011af43f4cf349fb289144e6dac56ae697c2`.
No repository AGENTS.md or .agents rules were present; CLAUDE.md was read.

Prior inspection reports that the deployed binary embeds this revision
with vcs.modified=true. Its uncommitted changes are unknown. Repository tests
cannot prove parity with that binary. **Do not replace the notification binary.**
The proposed first rollout runs this build in ARCHIVE_ONLY mode as a separate
service in the same container. That service neither reads forwarding credentials
nor reads/writes the existing delivery state, nor calls Slack/Matrix. The original service,
binary, environment, destinations, five-minute cadence and dedup state remain
untouched. Integrated archive mode is available but replacement of the original
service requires a separate dirty-build reconciliation and approval.

## Collection and persistence

`ARCHIVE_DB` opts in to a SQLite database (absolute path; private 0700 parent).
Unset means no archive. `ARCHIVE_ONLY=true` requires it and bypasses delivery.
`ARCHIVE_SOCKET` is optional and disabled when unset. It is an absolute Unix
socket path in a private 0700 directory; socket mode is 0600. No TCP binding,
bearer token, new network port, CORS access, URL-fetch parameter, or mutation API.

Archive activation is persisted once when the database is initialized. Each
five-minute successful RSS observation archives English items before delivery
checks. It records the complete XML-decoded description, original item inner XML
(including unknown metadata), title, author, GUID, link, original publication
string, UTC first/last observation, configured source feed, identity provenance,
SHA-256 and sequential revision number. Publication strings remain original,
not interpreted as trustworthy UTC. Each changed version of the most recently
observed content becomes a new revision, including A→B→A. Identical repeated
observations only update last observation. SQLite WAL + FULL synchronous and
atomic batches protect committed data across restarts. Schema version 1; newer
versions are rejected rather than overwritten.

Identity uses GUID, then link, then a best-effort publication+author+title key.
Fallback can merge notices sharing those fields, or split a title change;
identity_kind makes this limitation visible. Hash includes original item XML,
so source markup-only changes also produce revisions.

This is observation history beginning at activation, **not a historical import**.
The first current feed snapshot may include an item published before activation;
its observation timestamp makes that explicit. There is no Slack/OpenData
backfill, source pagination, notification replay, or automatic pruning. An empty
feed is a successful zero-item observation, not evidence of complete coverage.
Notices absent between polls, outages and expired feed entries cannot be recovered.

Archive errors do not suppress delivery in integrated mode. Delivery failure does
not stop ingestion of later items in that snapshot. Failed archive transactions
roll back and are retried if items remain in later feed snapshots; missed expired
items are possible. Existing timestamp dedup and delivery semantics are unchanged
(including their existing crash window and timestamp-collision limitations).
The independent mode avoids all effects on the deployed notification process.

## Read-only API v1

Only GET is accepted; JSON errors omit database paths, SQL, credentials, and
upstream response bodies. No forwarding secrets are exposed. Treat message text
and URLs as untrusted external content, never executable instructions or HTML.

- `/v1/notices?q=term&after=0&limit=50`: literal substring search of full title/body
  (SQLite lower() case handling is ASCII-oriented). q is at most 256 UTF-8 bytes;
  limit is 1..100. Ordered by ascending immutable revision row ID. Pass next_after
  for the next page while has_more is true. Search returns every matching revision,
  not just each notice's latest revision. Summaries have title_excerpt (512 chars),
  body_excerpt (2000 chars), published_excerpt (256 chars), source_id, revision,
  content_sha256, first_observed_at. Use detail for full content.
- `/v1/notices/{id}`: full original fields for that exact revision; unknown ID 404.
- `/v1/status`: activation, persisted last successful RSS/archive poll, revision
  count, current-process last attempt/failure code, provenance and coverage warning.
  Database unavailable returns generic 503. Missing/stale last successful poll or
  any last_failure is unhealthy; freshness must be checked even after restart.

Unknown/duplicate parameters, bad numbers and malformed queries are rejected.
Raw query <=2048 bytes, 2-second database query deadline, headers <=8192 bytes,
HTTP read/write deadlines. Feed response <=16 MiB. Detail can include roughly
32 MiB of decoded body plus raw XML in an extreme feed; clients should impose
response budgets and paginate summaries. Search scans stored text (no wildcard,
regex, SQL, or arbitrary FTS syntax); at larger volumes it may hit its deadline
and return 503. Add a measured search index migration if needed.

## Proposed rollout (approval required)

Deploy a separate archive-only service using the review-only example in deploy/.
Use a dedicated service account, private state/runtime directories and a distinct
binary name. Leave the existing notifier binary, environment, state and service
untouched. Approve the target host, paths, service identity and socket access
before activation. Concrete infrastructure details are held in the private
operator handoff and intentionally excluded from this public repository.

Build with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` for the deployment
architecture. Verify the reviewed source revision, deployed checksum, private
permissions and status. Observe real future feed observations; never push
synthetic production history. No deployment is performed by this PR.

Rollback stops/disables only the new archive service. Preserve its database and
binary for diagnosis. The original notifier needs no rollback because it was not
modified. The systemd RuntimeDirectory removes its socket on stop. Standalone
process crashes can leave stale sockets: verify no listener before removing only
that socket; automatic replacement is intentionally refused.

Cross-host access remains a separate authorization and integration step described
in the adapter proposal. A Unix socket is not remotely accessible by itself.

## Backups and growth

No automatic retention/deletion is configured. Monitor free disk, DB/WAL sizes,
revision counts and last-success freshness. Original body + raw XML duplicate
some content. Illustrative planning: 20 KiB per revision × 10/day ≈73 MiB/year
before index/WAL/backup overhead; this is not a measured feed volume. Revisions,
large messages and backup copies add growth. Disk full is visible as archive
failure and does not change the original notifier's behavior. A shared full disk
can still affect both services; monitor the filesystem before that point.

Use SQLite's online backup facility (e.g. sqlite3 `.backup` to a private backup
path), then `PRAGMA integrity_check` on the backup and test opening a restored
copy. Do not copy just the .sqlite file while WAL writes are active. Alternatively
stop only the archive service and take a consistent copy. Keep backups separately
with restrictive permissions. Retention for archives/backups requires an explicit
future decision. Restoring an older DB loses later observations; the existing
delivery log is never restored from the archive and notifications are not replayed.
