# Changelog

## [0.4.2] — 2026-11-09

- `jira_worklog_update` — edit your own worklog: time, start, comment. The server
  reads the worklog and the current user first and refuses someone else's
  worklog before any write. Until now a wrong worklog could only be fixed in
  the Jira UI.
- `adjust_estimate` for `jira_worklog_add` and `jira_worklog_update`: `auto`
  (Jira reduces the remaining estimate, the default), `leave`, or `new` with
  `new_estimate`. A lead's overhead hours on someone else's issue should not
  always eat the developer's remaining estimate.
- `conf_page` reads large pages in parts: `offset` — from which character, the
  answer names the next offset; `find` — fragments around every match (an issue
  key, or a change colour like `rgb(255,0,0)` in `storage`). A 110 KB spec used
  to be cut at 60 000 characters with no way to read the rest.
- `jira_bulk_update` compares array fields (components, versions) the way Jira
  returns them. Before, `components` from the arguments never matched the stored
  value, so a repeated call rewrote the same components.
- Issue links (`issuelinks` in `jira_search`) are shown in one line —
  `[id] parent ERP-1 | status | summary` — instead of ~1.5 KB of JSON each.

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
