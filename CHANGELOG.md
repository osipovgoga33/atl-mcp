# Changelog

## [0.4.1] — 2026-11-09

- Bulk actions have a time budget of 40 s. The client waits for an answer about
  a minute: a 22-issue update over VPN wrote everything but its answer came too
  late. Now the action stops in time and lists the issues it did not reach;
  repeating the same call skips what is already done.
- Compact container fields in `jira_search` and `jira_issue`: `worklog` is shown
  as a per-person summary, `comment` as a count. Other object fields lose
  `self`, `avatarUrls` and `iconUrl` and are cut at 1000 characters. One issue's
  worklog used to take about 10 KB of links and avatars.

## [0.4.0] — 2026-11-09

- Jira bulk actions in `atl_write`:
  - `jira_bulk_update` — one field change for a list of issues (`keys`) or a JQL
    selection, up to 50 issues per call. Same fields as `jira_update`.
  - `jira_bulk_transition` — one status change for a list or a JQL selection.
  - `dry_run=true` shows "was → will be" (for transitions: where the transition
    is available) and sends no writes.
  - Issues that already have the target value or status are skipped, so a call
    can be repeated safely. An error on one issue does not stop the rest; the
    answer lists every issue with its result.
  - All checks run before the first write: key format, the 50-issue limit
    (also for JQL), the project whitelist for every issue in the selection,
    keys that were not found.
- `jira_transition` shares the transition lookup with the bulk action.
- First public release: module path `github.com/osipovgoga33/atl-mcp`, English
  README, CI and release workflows.

## [0.3.1] — 2026-10-07

- `format=text` for Confluence: a table row is one line with cells separated by
  `|`; the status macro is shown as `[статус: …]`, macro parameters do not leak
  into the text; users on a page are shown by name.
- `jira_status_times`: when there is no resolution date but the status is final,
  the time is counted up to the transition into it, not up to today.
- TLS: separate `ATL_CONF_INSECURE_TLS` / `ATL_JIRA_INSECURE_TLS`, `ATL_CA_FILE`
  (PEM/DER, added to the system roots), a hint in the certificate error.

## [0.3.0] — 2026-10-07

- Wide Jira coverage: projects, fields, create metadata (Jira 9+ and the old API),
  transitions, history, time in status, worklogs, watchers, users, filters,
  Agile (boards, sprints, issues), Dev Info, Service Desk (queues, SLA),
  attachment downloads for Jira and Confluence.
- Jira writes: create, update, transition, assign, comment edit, worklog,
  links, remote links, attachments, watchers, sprints, backlog, versions.
- Jira project whitelist. Attachment file names are sent in UTF-8 like browsers do.

## [0.2.0] — 2026-10-07

- Confluence attachments, comments, page move.

## [0.1.0] — 2026-10-07

First version: Confluence and Jira read (issue, JQL), page create and update with
version control and a shrink guard, labels, Jira comment, space whitelist.
