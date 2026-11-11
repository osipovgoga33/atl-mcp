# atl-mcp — Confluence + Jira Server / Data Center for Claude Desktop

[Русская версия](README.ru.md)

An MCP server in Go (standard library only). It talks to Confluence and Jira
with a personal access token (PAT) and runs locally, so it reaches internal
servers through your VPN. No Atlassian Cloud needed. One binary, no Python.

Two tools: `atl` — read, `atl_write` — write (listed only when writing is
enabled). Both take an `action` parameter.

## Read — `atl`

**Confluence**

| action | parameters | what it does |
|---|---|---|
| `conf_search` | `text` / `title` / `space` or `cql`, `limit` | page search |
| `conf_page` | `id` or `space`+`title`, `format` = `text` \| `storage`, `offset`, `find` | a page; `storage` is the source XHTML for editing, with `version`. A large page comes in parts (`offset`); `find` returns fragments around every match |
| `conf_children` | `id` | child pages |
| `conf_spaces` | `limit` | spaces |
| `conf_attachments` | `id` | attachments |
| `conf_comments` | `id` | comments with replies |
| `conf_download` | `id`, `name` | download an attachment to the downloads folder |

**Jira**

| action | parameters | what it does |
|---|---|---|
| `jira_issue` | `key`, `comments`, `fields` | an issue: fields, subtasks, links (with ids), attachments, estimate/spent |
| `jira_search` | `jql`, `limit`, `start_at`, `fields` | issue search with paging |
| `jira_projects` | `text` | projects |
| `jira_project` | `project` | issue types, components, versions |
| `jira_fields` | `text` | fields and custom field ids |
| `jira_create_meta` | `project`, `issuetype` | fields for create: required, allowed values (Jira 9+ and the old API) |
| `jira_transitions` | `key` | available transitions and their required fields |
| `jira_changelog` | `key`, `field` | change history |
| `jira_status_times` | `key` | time in each status (calendar time), transitions |
| `jira_worklog` | `key` | worklogs with totals per person |
| `jira_watchers` | `key` | watchers |
| `jira_link_types` | — | link types |
| `jira_users` | `text`, `project` | users; with `project` — who can be assigned |
| `jira_filters` | — | favourite filters with JQL |
| `jira_dashboards` | `text` | favourite dashboards, or search by name |
| `jira_dashboard` | `id` | gadgets of a dashboard and their settings, rich and smart filters by name |
| `jira_rich_filter` | `id` or `text` | rich filter (Rich Filters app): base filter, quick, dynamic and smart filters with the JQL of each clause, views |
| `jira_jql_time` | `jql` / `jqls` / `rich_filter` (+ `smart_filter`), `start_at` | time and issue count of each query or rich filter clause, one by one, 40 s budget |
| `jira_download` | `key`, `name` / `attachment_id` / `all` | download attachments to the downloads folder |
| `jira_dev_info` | `key` | commits, branches, PRs (with a Bitbucket/GitLab integration) |
| `jira_boards` | `text`, `project`, `type` | boards |
| `jira_sprints` | `board_id`, `state` | sprints |
| `jira_sprint_issues` | `sprint_id`, `jql`, `limit`, `start_at`, `fields` | sprint issues |
| `jira_board_issues` | `board_id`, `jql`, … | board issues |
| `jsm_desks` | — | service desk projects |
| `jsm_queues` | `desk_id` | queues with issue counts |
| `jsm_queue_issues` | `desk_id`, `queue_id` | queue issues |
| `jsm_sla` | `key` | request SLA |

## Write — `atl_write`

**Confluence**

| action | parameters |
|---|---|
| `conf_create` | `space` and/or `parent_id`, `title`, `body` (storage), `labels` |
| `conf_update` | `id`, `version` (as read), `body` (full body), `title`, `message`, `minor_edit`, `allow_shrink` |
| `conf_label` | `id`, `labels` |
| `conf_attach` | `id`, exactly one of `path` / `content` / `content_base64`, `name`, `message` |
| `conf_comment` | `id`, `body` (storage), `parent_comment_id` |
| `conf_move` | `id`, `parent_id` (same space), `version` |

**Jira** (texts use Jira wiki markup)

| action | parameters |
|---|---|
| `jira_create` | `project`, `issuetype`, `summary`, `description`, `priority`, `assignee`, `labels`, `components`, `fix_versions`, `due_date`, `parent`, `fields` {custom fields as is} |
| `jira_update` | `key` + the same fields, `fields` {}, `update` {} (Jira operations) |
| `jira_bulk_update` | `keys` [] or `jql` (up to 50 issues) + the same fields as `jira_update`; `dry_run` — show "was → will be" without writing |
| `jira_transition` | `key`, `transition` (id, name or target status), `comment`, `resolution`, `fields` |
| `jira_bulk_transition` | `keys` [] or `jql` (up to 50 issues), `transition`, `comment`, `resolution`, `fields`; `dry_run` — where the transition is available |
| `jira_assign` | `key`, `user` (login or `none`) |
| `jira_comment` | `key`, `body`, `comment_id` — edit |
| `jira_worklog_add` | `key`, `time_spent` (`1h 30m`), `started` (`2026-10-07 10:00`), `comment`, `adjust_estimate` (`auto` \| `leave` \| `new` + `new_estimate`) |
| `jira_worklog_update` | `key`, `worklog_id`, `time_spent`, `started`, `comment`, `adjust_estimate` — your own worklogs only |
| `jira_link` / `jira_link_remove` | `inward_key`, `outward_key`, `link_type`, `comment` / `link_id` |
| `jira_remote_link` | `key`, `url`, `title` — e.g. a link to a spec in Confluence |
| `jira_attach` | `key`, `path` / `content` / `content_base64`, `name` |
| `jira_watcher_add` / `jira_watcher_remove` | `key`, `user` |
| `jira_sprint_add` / `jira_backlog` | `sprint_id`, `keys` / `keys` (up to 50) |
| `jira_sprint_create` | `board_id`, `name`, `start_date`, `end_date`, `goal` |
| `jira_version_create` | `project`, `name`, `description`, `start_date`, `release_date` |

Bulk actions send one request per issue. Issues that already have the target
value (or are already in the target status) are skipped, so a call can be
repeated. An error on one issue does not stop the rest; the answer gives the
result for every issue. After 40 s the action stops and lists the issues it did
not reach (the client waits for an answer about a minute) — repeat the same call. One bulk call is one approval in Claude Desktop instead
of fifty.

There is no deletion of issues, pages, comments or attachments on purpose. Only
issue links and watchers can be removed — both are reversible.

## Safeguards

- Writing is off by default (`ATL_ALLOW_WRITE`). The guard sits in the HTTP
  client: without the flag only GET requests leave the server.
- Whitelists: `ATL_WRITE_SPACES` — Confluence spaces, `ATL_JIRA_WRITE_PROJECTS`
  — Jira projects (links, sprint moves and bulk actions check every issue
  involved).
- `conf_update` requires the version read before the edit, so other people's
  edits are not overwritten. A new body less than half the old one is refused
  unless `allow_shrink` is passed.
- `conf_move` — only within the space and never under the page's own descendant.
- A Jira transition that does not exist is not sent — the answer lists the
  available ones.
- Bulk actions: at most 50 issues (for JQL — by the total found), the whitelist
  is checked for every issue in the selection, a key that is not found refuses
  the whole call; all of this before the first write.
- Keys, ids, logins, fields, labels, dates, `time_spent`, URLs and file names are
  validated before the request.
- Uploads — only allowed extensions and up to 50 MB, so a hint on a page cannot
  make the server upload a key or a config file.
- Downloads — only from the same server (the token never goes to another host),
  into the downloads folder, without overwriting: a repeat lands as `name (1).ext`.

## Configuration

| variable | example |
|---|---|
| `ATL_CONF_URL` | `https://wiki.example.com` |
| `ATL_CONF_TOKEN` | Confluence PAT |
| `ATL_JIRA_URL` | `https://jira.example.com` |
| `ATL_JIRA_TOKEN` | Jira PAT |
| `ATL_ALLOW_WRITE` | `true` — enable writing |
| `ATL_WRITE_SPACES` | `DEV,SAP` — empty = any |
| `ATL_JIRA_WRITE_PROJECTS` | `ABC,SD` — empty = any |
| `ATL_DOWNLOAD_DIR` | downloads folder; empty = `Downloads/atl-mcp` |
| `ATL_CA_FILE` | path to a corporate certificate (.cer/.crt/.pem, Base-64 or DER) |
| `ATL_CONF_INSECURE_TLS` | `true` — do not verify the Confluence certificate |
| `ATL_JIRA_INSECURE_TLS` | `true` — do not verify the Jira certificate |
| `ATL_INSECURE_TLS` | old common flag: applies to both |

You can configure only Confluence or only Jira.

### "certificate signed by unknown authority"

The server certificate is issued by an internal CA. The proper fix is to point
`ATL_CA_FILE` to that CA: open the server in a browser → padlock → certificate →
"Certification path" → the top (root) one → "Export" as Base-64 `.cer`. Verification
stays on; only this certificate is added to the system roots. The quick bypass is
`ATL_JIRA_INSECURE_TLS=true` (traffic stays encrypted, the server identity is not
checked); it applies only to its own product.

### Extension — `.mcpb`

Download `atl-mcp.mcpb` from the
[latest release](https://github.com/osipovgoga33/atl-mcp/releases/latest),
double-click it (or Claude Desktop → Settings → Extensions → install from file)
and fill in the URLs and tokens. Tokens are kept in the system keychain.

### Manually — `claude_desktop_config.json`

```json
"atlassian": {
  "command": "C:/Tools/atl-mcp/atl-mcp.exe",
  "env": {
    "ATL_CONF_URL": "https://wiki.example.com",
    "ATL_CONF_TOKEN": "...",
    "ATL_JIRA_URL": "https://jira.example.com",
    "ATL_JIRA_TOKEN": "...",
    "ATL_ALLOW_WRITE": "true"
  }
}
```

Use forward slashes in the path (Windows accepts them) or doubled backslashes; a
single backslash breaks the JSON and Desktop silently skips the whole config.
A second instance (another Confluence/Jira) is a second block with the same
binary and other variables.

## Version check

`atl-mcp.exe --version`; the start line in
`%APPDATA%\Claude\logs\mcp-server-<name>.log`:
`старт atl-mcp 0.5.0: confluence=... jira=... запись=...`.

## Build

`bash build.sh` — tests, binaries in `dist/` (Windows, macOS arm64/amd64, Linux)
and `dist/atl-mcp.mcpb`.

## History

See [CHANGELOG.md](CHANGELOG.md).

## License

MIT, see [LICENSE](LICENSE).
