# Azure DevOps Integration Plan

## Decisions Made

- **CLI command**: `bd ado` (short and clean)
- **Config prefix**: `ado.*`
- **Package**: `internal/ado/`
- **HTML↔Markdown**: Two-library strategy — `goldmark` (already indirect dep via glamour) for MD→HTML, `JohannesKaufmann/html-to-markdown` for HTML→MD. Sanitize with `bluemonday` (already indirect dep via glamour).
- **Process templates**: Sensible Agile defaults + override via `ado.state_map.*` / `ado.type_map.*` + startup probe to validate mappings
- **Work item links**: Full bidirectional sync of ADO links ↔ beads dependencies (Phase 2 milestone)
- **ADO-specific fields**: Preserve in Issue.Metadata for round-trip (Area Path, Iteration Path, Story Points, etc.)

---

## Architecture Context

Beads has a **plugin-based tracker framework** (`internal/tracker/`) with a clean interface.
Four trackers exist: GitHub, GitLab, Jira, Linear — all following the same pattern.

### What We Implement
1. `tracker.IssueTracker` interface (Init, FetchIssues, CreateIssue, UpdateIssue, FieldMapper, etc.)
2. `tracker.FieldMapper` interface (bidirectional field conversion)
3. Registration via `tracker.Register("ado", factory)` in `init()`
4. CLI commands in `cmd/bd/ado.go`

### What We Get for Free
- `tracker.Engine` handles all sync orchestration (pull → conflict detect → push)
- `PullHooks` / `PushHooks` for customization
- Incremental sync via `last_sync` timestamp
- Conflict resolution strategies (timestamp, local, external)
- OTel tracing integration

---

## Delivery Milestones

**Milestone 1 (MVP)**: Issue fields + status/type/priority sync + status/projects commands
**Milestone 2**: Bidirectional link sync + advanced metadata round-trip

---

## Security Architecture

### S1: PAT Credential Handling
- Create a `SecretString` wrapper type with `String()` returning `[REDACTED]` and `fmt.Stringer` implementation
- PAT is never logged, included in error messages, or serialized to JSON
- PAT read from config store or env var (same pattern as Jira: `getConfig(ctx, "ado.pat", "AZURE_DEVOPS_PAT")`)
- `maskToken()` for status display (shows first 4 chars only, matching `maskGitHubToken()` pattern)
- Test fixtures use dummy tokens only

### S2: TLS Enforcement
- Default HTTP client enforces TLS — no `InsecureSkipVerify`
- Reject non-HTTPS URLs by default (except `localhost` for testing)
- On-prem ADO Server users must use valid TLS certificates

### S3: WIQL Injection Prevention
- **Never** interpolate raw user input or timestamps into WIQL query strings
- Use parameterized WIQL approach: construct queries with Go string formatting of validated, typed values only
- Timestamps: validate as `time.Time`, format via `time.Format()` — no raw string passthrough
- Org names: validate against `^[a-zA-Z0-9._-]+$` regex (strict — ADO org names have limited charset)
- Project names: validate against `^[a-zA-Z0-9 ._'-]+$` regex (allows spaces, quotes — ADO projects support these). Single quotes escaped via `escapeWIQL()` before interpolation.
- All filter values are validated against their respective allowlists AND single-quote-escaped before WIQL use
- Example safe pattern:
  ```go
  // Safe: time.Time formatted, project validated & escaped
  // Uses >= with ID dedup to prevent missing same-second updates
  query := fmt.Sprintf(
      `SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = '%s' AND [System.ChangedDate] >= '%s' AND [System.IsDeleted] = false ORDER BY [System.ChangedDate] ASC`,
      escapeWIQL(validatedProject), since.UTC().Format("2006-01-02T15:04:05Z"),
  )
  ```
  After fetch, deduplicate against already-synced IDs using (timestamp, ID) cursor stored in `ado.last_sync_cursor`.
  This prevents missing updates that occur at the exact `last_sync` second boundary.

### S4: HTML Sanitization
- **ADO→beads (pull)**: Sanitize raw HTML with `bluemonday` (already indirect dep) BEFORE converting to Markdown. Strip script tags, event handlers, dangerous elements.
- **beads→ADO (push)**: Use goldmark with safe renderer settings. No raw HTML passthrough.
- Accept some lossy conversion for complex HTML (tables, embedded images). Document known limitations.

### S5: Input Validation
- Work item IDs: validate as positive integers, reject negative/zero/non-numeric
- Org/project names: validate against `^[a-zA-Z0-9._-]+$`, URL-encode all path components
- Config map values (`ado.state_map.*`, `ado.type_map.*`): validate no special characters that could affect JSON Patch or WIQL
- Response body size limit: 50MB max (matching GitHub client pattern) to prevent OOM

### S6: Error Message Safety
- Error messages reference config key names (`ado.org`, `ado.project`), not actual values
- Rate limit errors: generic user message, internal-only logging of retry details
- Auth errors: "authentication failed" without echoing credentials or scopes

---

## Deletion / Archive / Disappearance Lifecycle

When a previously-synced ADO work item becomes unavailable during incremental sync:

### Scenarios & Policies

| Scenario | Detection | Beads Action |
|----------|-----------|-------------|
| ADO item deleted | 404 on fetch by ID | Mark local issue `status=closed`, `close_reason="ADO item deleted"`, preserve `external_ref` |
| ADO item moved to different project | Not returned by WIQL (different project scope) | Detected by reconciliation scan (see below), then direct GET. If 404: close. If found in other project: update `external_ref` URL |
| PAT permission revoked for item | 403 on fetch | Skip with warning. Do NOT modify local issue. Log: "access denied for work item {id}" |
| ADO item archived/removed state | Returned with state "Removed" | Map to beads `deferred` status (via state mapping) |

### Reconciliation Scan (Detecting Deletions)

Incremental WIQL pull cannot detect deletions (deleted items don't appear in results). To handle this:

1. **Every Nth sync** (configurable via `ado.reconcile_interval`, default: 10 syncs), perform a full reconciliation:
   - Collect all local issue IDs with ADO external refs
   - Batch GET these IDs from ADO (200 per batch, `$expand=none` for speed)
   - For each 404 response: mark local issue closed with reason "ADO item deleted"
   - For each 403 response: skip (permission issue, not deletion)
2. **Counter stored in storage** (not config): `ado.syncs_since_reconcile` tracked via `store.SetConfig()`/`store.GetConfig()` alongside `ado.last_sync` — keeps runtime state separate from user configuration
3. **Force reconciliation**: `bd ado sync --reconcile` flag to trigger immediately

### Pull-Side Disambiguation Rules

For **priority round-trip** when beads 3/4 both map to ADO 4:
- On pull: if `metadata.beads_priority` exists AND ADO priority unchanged since push → restore original beads priority
- On pull: if `metadata.beads_priority` missing OR ADO priority was manually changed → default to beads 3 (Low)
- This ensures ADO-native edits take precedence over round-trip metadata

For **blocked status** disambiguation when ADO `Active` maps to both `in_progress` and `blocked`:
- On pull: if ADO state = `Active` AND `System.Tags` contains `beads:blocked` → map to beads `blocked`
- On pull: if ADO state = `Active` without `beads:blocked` tag → map to beads `in_progress`
- On push: `blocked` → set ADO state `Active` + add tag `beads:blocked`; `in_progress` → set ADO state `Active` + remove tag `beads:blocked` if present

### Unknown State/Type Fallback Rules

| Condition | Beads Fallback | Side Effect |
|-----------|---------------|-------------|
| Unknown ADO state | `open` | Store original in `metadata.ado_original_state`, emit warning |
| Unknown ADO type | `task` | Store original in `metadata.ado_original_type`, emit warning |
| Unknown beads status on push | ADO `New` | Emit warning |
| Unknown beads type on push | ADO `Task` | Emit warning |

Warnings are surfaced in `bd ado status` output and sync result summary.

---

## Pull Scope Filtering (Safe WIQL Construction)

Instead of raw WIQL (injection risk), pull scope is controlled via discrete, validated config fields.

**Config fields:**
- `ado.filter.area_path` — validated against `^[a-zA-Z0-9 ._\\/-]+$`
- `ado.filter.iteration_path` — validated against `^[a-zA-Z0-9 ._\\/-]+$`
- `ado.filter.work_item_types` — comma-separated, each validated against known types
- `ado.filter.states` — comma-separated, each validated against `^[a-zA-Z0-9 _]+$`

**WIQL construction** (all values validated AND single-quote-escaped before interpolation):
```go
// escapeWIQL escapes backslashes and doubles single quotes for safe WIQL string interpolation.
func escapeWIQL(s string) string {
    s = strings.ReplaceAll(s, "\\", "\\\\")
    return strings.ReplaceAll(s, "'", "''")
}

func (c *Client) buildPullWIQL(since *time.Time, filters PullFilters) string {
    clauses := []string{
        fmt.Sprintf("[System.TeamProject] = '%s'", c.validatedProject),
        "[System.IsDeleted] = false",
    }
    if since != nil {
        clauses = append(clauses, fmt.Sprintf("[System.ChangedDate] >= '%s'", since.UTC().Format("2006-01-02T15:04:05Z")))
    }
    if filters.AreaPath != "" {
        clauses = append(clauses, fmt.Sprintf("[System.AreaPath] UNDER '%s'", filters.validatedAreaPath))
    }
    if filters.IterationPath != "" {
        clauses = append(clauses, fmt.Sprintf("[System.IterationPath] UNDER '%s'", filters.validatedIterationPath))
    }
    if len(filters.WorkItemTypes) > 0 {
        typeList := strings.Join(quotedList(filters.validatedTypes), ", ")
        clauses = append(clauses, fmt.Sprintf("[System.WorkItemType] IN (%s)", typeList))
    }
    if len(filters.States) > 0 {
        stateList := strings.Join(quotedList(filters.validatedStates), ", ")
        clauses = append(clauses, fmt.Sprintf("[System.State] IN (%s)", stateList))
    }
    return "SELECT [System.Id] FROM WorkItems WHERE " +
        strings.Join(clauses, " AND ") +
        " ORDER BY [System.ChangedDate] ASC"
}
```

No raw WIQL passthrough. All values are validated and typed before use.

---

## Bootstrap / First-Sync Deduplication

When syncing for the first time, issues may already exist in both beads and ADO. Without dedup, pull creates duplicates.

**Matching policy (in priority order):**
1. **External ref match**: If a beads issue already has an `external_ref` pointing to an ADO work item URL → link (no create)
2. **ADO ID match**: If a beads issue has `source_system` starting with `ado:` → link to matching work item
3. **Heuristic match** (opt-in via `--bootstrap-match`): Match by exact title + same type + created within 24h window. If exactly 1 candidate → auto-link. If 0 or >1 candidates → skip with warning listing candidates (user must resolve via `bd ado sync --link <beads-id> <ado-id>`)
4. **Create**: If no match found → create new item in target system

**CLI flags:**
- `bd ado sync` (default): Uses external_ref and source_system matching only. Creates new items for unmatched.
- `bd ado sync --bootstrap-match`: Enables title+type+time heuristic matching for first sync
- `bd ado sync --no-create`: Pull-only mode that never creates in ADO; only links/updates existing matched items

**Acceptance tests:**
- Pre-existing issue in both systems with matching external_ref → linked, not duplicated
- Pre-existing issue with same title but no external_ref → only matched with `--bootstrap-match`
- Completely new issue → created in target system
- `--no-create` flag → unmatched issues skipped with warning

---

## Non-Goals (Explicit Scope Boundaries)

The following are **out of scope** for this integration:

- **Attachments**: File attachment sync between ADO and beads
- **Comments sync**: ADO work item comments ↔ beads comments (future enhancement)
- **Boards/Sprints**: ADO board state, sprint assignments, kanban columns
- **Identity/User mapping**: Cross-system user identity resolution (e.g., ADO email → beads user)
- **Historical audit backfill**: Importing full ADO work item history/revisions
- **Azure Pipelines**: CI/CD pipeline integration
- **Test Plans**: ADO Test Plans/Test Cases integration
- **Webhooks**: Real-time push notifications from ADO (future enhancement; currently poll-based)

---

## Acceptance Criteria

### Milestone 1 (MVP) — Must Pass

1. `bd ado status` shows configuration state (configured/not configured) without network calls
2. `bd ado projects` lists accessible projects from configured ADO org
3. `bd ado sync --pull-only` imports work items from configured project with correct field mapping
4. `bd ado sync --push-only` exports beads issues to ADO with correct field mapping
5. `bd ado sync` performs bidirectional sync with conflict resolution
6. `bd ado sync --dry-run` shows what would sync without making changes
7. Incremental sync: second sync only fetches items changed since `ado.last_sync`
8. Priority, status, type mappings are bidirectional and pass table-driven tests for all 4 process templates
9. HTML→Markdown and Markdown→HTML conversion handles: bold, italic, links, lists, code blocks, headers
10. Malicious HTML is stripped (no script tags or event handlers survive conversion)
11. PAT is never visible in logs, error messages, JSON output, or `bd ado status` (except masked first 4 chars)
12. On-prem, cloud, and legacy visualstudio.com URLs all resolve correctly
13. WIQL queries contain no unvalidated user input
14. 429/5xx errors are retried with exponential backoff
15. All unit tests pass with `go test -race ./internal/ado/...`

### Milestone 2 — Must Pass

16. ADO work item links are imported as beads dependencies with correct type mapping
17. Beads dependencies are exported as ADO work item links
18. Repeated sync converges to stable state (idempotency test)
19. Partial link failure doesn't abort full sync
20. Link direction truth table passes for all 4 beads dependency types

---

ADO has three URL patterns that must all be supported:

| Deployment | URL Pattern | API Base |
|-----------|------------|----------|
| Azure DevOps Services (cloud) | `dev.azure.com/{org}` | `https://dev.azure.com/{org}/{project}/_apis/` |
| Legacy cloud | `{org}.visualstudio.com` | `https://{org}.visualstudio.com/{project}/_apis/` |
| Azure DevOps Server (on-prem) | `{server}/{collection}` | `https://{server}/{collection}/{project}/_apis/` |

### URL Resolution Logic
```go
func (c *Client) apiBase() string {
    if c.BaseURL != "" {
        // On-prem or custom: user provides full base (e.g., "https://tfs.corp.com/DefaultCollection")
        return strings.TrimSuffix(c.BaseURL, "/") + "/" + c.Project + "/_apis"
    }
    // Cloud default
    return "https://dev.azure.com/" + c.Org + "/" + c.Project + "/_apis"
}
```

### External Ref Matching (`IsExternalRef`)
Follow Jira's pattern — use configured URL for matching, not hardcoded domain:
```go
func (t *Tracker) IsExternalRef(ref string) bool {
    // Match configured base URL (handles cloud, legacy, and on-prem)
    if t.baseURL != "" && strings.HasPrefix(ref, t.baseURL) {
        return adoWorkItemPattern.MatchString(ref)
    }
    // Also match known cloud patterns
    return (strings.Contains(ref, "dev.azure.com") || strings.Contains(ref, "visualstudio.com")) &&
        adoWorkItemPattern.MatchString(ref)
}
```

### Canonical External Ref Format
All external refs are normalized to `https://{base}/_workitems/edit/{id}` format regardless of source URL variant. This prevents duplicate issues from URL format differences.

---

## Implementation Phases

### Phase 1: Types & Client (`internal/ado/types.go`, `client.go`)

**types.go** — ADO REST API data structures:
```go
// API constants
const (
    DefaultBaseURL = "https://dev.azure.com"
    APIVersion     = "7.1"
    MaxBatchSize   = 200  // ADO batch GET limit
    MaxResponseSize = 50 * 1024 * 1024  // 50MB response body limit
)

// SecretString wraps a secret value to prevent accidental logging/serialization.
type SecretString struct{ value string }
func NewSecretString(s string) SecretString  { return SecretString{value: s} }
func (s SecretString) String() string        { return "[REDACTED]" }
func (s SecretString) Expose() string        { return s.value }
func (s SecretString) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

type Client struct {
    PAT        SecretString
    BaseURL    string  // Custom URL for on-prem; empty = cloud default
    Org        string
    Project    string
    HTTPClient *http.Client
}

type WorkItem struct {
    ID        int                    `json:"id"`
    Rev       int                    `json:"rev"`
    Fields    map[string]interface{} `json:"fields"`
    URL       string                 `json:"url"`
    Relations []WorkItemRelation     `json:"relations,omitempty"`
}

type WorkItemRelation struct {
    Rel        string                 `json:"rel"`
    URL        string                 `json:"url"`
    Attributes map[string]interface{} `json:"attributes"`
}

type PatchOperation struct {
    Op    string      `json:"op"`
    Path  string      `json:"path"`
    Value interface{} `json:"value,omitempty"`
}

type WIQLResult struct {
    WorkItems []WIQLWorkItemRef `json:"workItems"`
}

type WIQLWorkItemRef struct {
    ID  int    `json:"id"`
    URL string `json:"url"`
}

type Project struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description"`
    URL         string `json:"url"`
    State       string `json:"state"`
}
```

**client.go** — HTTP client with domain-specific methods (JSON Patch encapsulated):
- Auth: `Authorization: Basic base64(":"+PAT.Expose())` — note leading colon for empty username
- `FetchWorkItems(ctx, ids)` — GET batch, **chunks into ≤200 ID batches**, uses `$expand=relations`
- `FetchWorkItemsSince(ctx, since)` — Parameterized WIQL query, handles continuation/batching
- `CreateWorkItem(ctx, typeName, fields map[string]interface{})` — internally builds JSON Patch array
- `UpdateWorkItem(ctx, id, fields map[string]interface{})` — internally builds JSON Patch array
- `AddWorkItemLink(ctx, sourceID, targetURL, linkType)` — internally builds relation PATCH
- `RemoveWorkItemLink(ctx, sourceID, relationIndex)` — internally builds relation remove PATCH
- `ListProjects(ctx)` — GET accessible projects
- `GetWorkItemTypes(ctx)` — GET available work item types for process template validation
- `GetWorkItemStates(ctx, typeName)` — GET available states for a work item type
- Retry logic: 429 (with Retry-After), 503, 500, 502, 504 — exponential backoff (matching GitHub pattern)
  - **Idempotency guards**: Only auto-retry idempotent operations (GET, WIQL queries). For mutations (POST create, PATCH update/link):
    - On timeout/ambiguous failure: perform a dedupe lookup (GET by title+external_ref or GET by ID) before retrying
    - Use ADO `rev` field as precondition on updates: include `If-Match` header with known revision to prevent stale writes
    - Create operations: set a `beads:idempotency-key:{uuid}` tag on creation. On timeout/retry, query for existing item with that tag before creating duplicate. UUID is generated once per create intent and reused across retries.
  - No retry on 400 (bad request), 401 (auth), 403 (permission) — these are permanent failures
- Response body limit: `io.LimitReader(resp.Body, MaxResponseSize)`
- All URL path components are URL-encoded

### Phase 2: Rich Text Conversion (`richtext.go`)

Shared utility within `internal/ado/` (extracted to shared package if other trackers need it later):

**Dependencies** (promoted to direct deps for version pinning):
- `goldmark` (promote from indirect to direct) — Markdown → HTML
- `JohannesKaufmann/html-to-markdown` — HTML → Markdown (new direct dep)
- `bluemonday` (promote from indirect to direct) — HTML sanitization (using `bluemonday.UGCPolicy()` as baseline)

```go
// HTMLToMarkdown converts ADO HTML description to Markdown for beads storage.
// Sanitizes HTML first via bluemonday, then converts to Markdown.
func HTMLToMarkdown(html string) string

// MarkdownToHTML converts beads Markdown description to HTML for ADO.
// Uses goldmark with safe renderer (no raw HTML passthrough).
func MarkdownToHTML(md string) string
```

**Round-trip acceptance tests** (in `richtext_test.go`):
- Simple formatting: bold, italic, links
- Lists (ordered, unordered, nested)
- Code blocks (inline, fenced)
- Headers (h1-h6)
- Tables (known lossy — document limitation)
- Embedded images (converted to `![alt](url)`)
- Malicious HTML (script tags, event handlers — must be stripped)
- Empty/nil input handling

### Phase 3: Field Mapping (`fieldmapper.go`, `mapping.go`)

**Priority mapping** (ADO 1-4 where 1=highest, beads 0-4 where 0=highest):
| Beads | ADO | Label | Round-trip |
|-------|-----|-------|------------|
| 0 | 1 | Critical | Lossless |
| 1 | 2 | High | Lossless |
| 2 | 3 | Medium | Lossless |
| 3 | 4 | Low | Lossless |
| 4 | 4 | Low | **Lossy** — original beads priority stored in `metadata.beads_priority` |

**Status mapping** (Agile defaults, overridable via `ado.state_map.*`):
| Beads Status | ADO State (Agile) | Round-trip |
|-------------|-------------------|------------|
| open | New | Lossless |
| in_progress | Active | See below |
| blocked | Active | **Lossy** — original stored in `metadata.beads_status` + ADO tag `beads:blocked` |
| deferred | Removed | Lossless |
| closed | Closed | Lossless |

**Type mapping** (Agile defaults, overridable via `ado.type_map.*`):
| Beads Type | ADO Type (Agile) |
|-----------|------------------|
| bug | Bug |
| feature | User Story |
| task | Task |
| epic | Epic |
| chore | Task |

Case handling: ADO types are case-sensitive. Normalize to Title Case on push; accept any case on pull.

**ADO-specific fields → Metadata** (preserved for round-trip):
- `System.AreaPath` → `metadata.area_path`
- `System.IterationPath` → `metadata.iteration_path`
- `Microsoft.VSTS.Scheduling.StoryPoints` → `metadata.story_points`
- `Microsoft.VSTS.Scheduling.RemainingWork` → `metadata.remaining_work`
- `metadata.beads_priority` — original beads priority when collapsed (3 or 4 → ADO 4)
- `metadata.beads_status` — original beads status when collapsed (blocked → ADO Active)

### Phase 4: Tracker & Registration (`tracker.go`)

```go
func init() {
    tracker.Register("ado", func() tracker.IssueTracker {
        return &Tracker{}
    })
}

type Tracker struct {
    client   *Client
    store    storage.Storage
    baseURL  string                 // Resolved base URL for external ref matching
    stateMap map[string]string      // beads status → ADO state (from ado.state_map.*)
    typeMap  map[string]string      // beads type → ADO type (from ado.type_map.*)
}
```

**Init() performs:**
1. Read config (PAT, org, project, URL) from store + env vars
2. Validate required fields (no network calls)
3. Read custom state/type mappings from `ado.state_map.*` and `ado.type_map.*` config
4. Read pull filters from `ado.filter.*` config, validate against allowlists
5. Create Client

**First sync performs (lazy probe):**
- Query available work item types and states for configured project
- Warn (not fail) if configured mappings reference unavailable types/states
- Cache results for session lifetime (no repeated probes)

**Config keys:**
| Config Key | Env Variable | Purpose |
|-----------|-------------|---------|
| `ado.org` | `AZURE_DEVOPS_ORG` | Organization name |
| `ado.project` | `AZURE_DEVOPS_PROJECT` | Project name |
| `ado.pat` | `AZURE_DEVOPS_PAT` | Personal access token |
| `ado.url` | `AZURE_DEVOPS_URL` | Custom base URL (on-prem ADO Server) |
| `ado.state_map.*` | — | Custom state mappings (e.g., `ado.state_map.in_progress=Doing`) |
| `ado.type_map.*` | — | Custom type mappings (e.g., `ado.type_map.feature=Product Backlog Item`) |
| `ado.filter.area_path` | — | Filter pull to specific area path (e.g., `MyProject\Backend`) |
| `ado.filter.iteration_path` | — | Filter pull to specific iteration path (e.g., `MyProject\Sprint 5`) |
| `ado.filter.work_item_types` | — | Comma-separated work item types to pull (e.g., `Bug,Task,User Story`) |
| `ado.filter.states` | — | Comma-separated states to pull (e.g., `New,Active,Resolved`) |

**Validate()**: Checks API connectivity and project access. Note: ADO PATs do not expose scope introspection via API, so scope validation is not possible — instead, the first sync operation that fails with 403 produces a clear "insufficient PAT permissions" error message.

**Close()**: Calls `transport.CloseIdleConnections()` if the HTTP client uses a custom transport. Documented invariant: safe to call multiple times.

### Phase 5: Work Item Link Sync — Milestone 2 (ADO-specific)

**Link type mapping (direction truth table):**
| Beads | Direction | ADO Link Type | ADO Direction |
|-------|-----------|---------------|---------------|
| A `blocks` B | A must complete before B | `System.LinkTypes.Dependency-Forward` | A → B (A is predecessor of B) |
| A `parent-child` B | A is parent of B | `System.LinkTypes.Hierarchy-Forward` | A → B (B is child of A) |
| A `related` B | Symmetric | `System.LinkTypes.Related` | A ↔ B |
| A `discovered-from` B | A found during work on B | `System.LinkTypes.Related` | A → B (with `beads:discovered-from` in link comment) |

**Link Resolver** (dedicated component, not in tracker.go):
```go
// LinkResolver handles bidirectional link sync between beads dependencies and ADO work item relations.
// Separated from Tracker to prevent God object and contain graph-sync complexity.
type LinkResolver struct {
    client *Client
    store  storage.Storage
}

// PullLinks imports ADO relations as beads dependencies.
// Normalizes link directions and deduplicates against existing dependencies.
func (r *LinkResolver) PullLinks(ctx context.Context, workItem *WorkItem, beadsIssueID string) ([]tracker.DependencyInfo, error)

// PushLinks exports beads dependencies as ADO work item relations.
// Compares current ADO relations with desired state, adds missing, removes stale.
// Idempotent: repeated calls converge to stable state.
func (r *LinkResolver) PushLinks(ctx context.Context, beadsIssueID string, adoWorkItemID int) error
```

**Idempotency/conflict model:**
- On pull: normalize ADO link URL → work item ID → lookup beads issue by external ref → create dependency if not exists
- On push: fetch current ADO relations → diff against desired state → add/remove only deltas
- Direction normalization: `Dependency-Reverse` is converted to `Dependency-Forward` with swapped source/target
- Partial failure: collect errors per-link, continue processing, report all errors in summary. Never abort full sync on single link failure.

### Phase 6: CLI Command (`cmd/bd/ado.go`)

```
bd ado sync          # Bidirectional sync
bd ado sync --pull-only
bd ado sync --push-only
bd ado sync --dry-run
bd ado sync --prefer-local / --prefer-ado / --prefer-newer
bd ado status        # Show config + connection status + process template info
bd ado projects      # List accessible projects
```

Register in `init()`: `rootCmd.AddCommand(adoCmd)`
Flag patterns match existing tracker commands (`--pull-only`, `--push-only`, `--prefer-{tracker}`, `--prefer-newer`).

### Phase 7: Tests

Following existing test patterns:
- `types_test.go` — JSON marshaling/unmarshaling, SecretString redaction
- `client_test.go` — `httptest.Server` mock for: auth headers (Basic with colon prefix), pagination/batching (200 ID chunks), retry on 429/503, WIQL query construction, response size limits, URL encoding
- `mapping_test.go` — Table-driven bidirectional field mapping for all process templates (Agile, Scrum, CMMI, Basic)
- `richtext_test.go` — HTML↔Markdown round-trip fidelity, sanitization of malicious HTML, edge cases
- `tracker_test.go` — Tracker interface compliance, Init() config loading, Validate() connectivity check, external ref matching (cloud/legacy/on-prem URLs)
- `links_test.go` — Link direction truth table, idempotency (same sync twice = no changes), partial failure handling, direction normalization

**Test for deleted work items**: WIQL includes `[System.IsDeleted] = false` filter. Test verifies deleted items are excluded.

**Test for process template variance**: Verify Scrum (`Product Backlog Item`), CMMI (`Requirement`), and Basic templates with custom type maps.

---

## File Manifest

### New files to create:
```
internal/ado/
├── types.go           # ADO API types, constants, SecretString
├── types_test.go
├── client.go          # HTTP REST API client (JSON Patch encapsulated)
├── client_test.go
├── tracker.go         # IssueTracker impl + init() registration
├── tracker_test.go
├── fieldmapper.go     # FieldMapper impl
├── mapping.go         # Bidirectional field conversion
├── mapping_test.go
├── richtext.go        # HTML↔Markdown conversion + sanitization
├── richtext_test.go
├── links.go           # LinkResolver for bidirectional link sync (Milestone 2)
├── links_test.go      # (Milestone 2)

cmd/bd/
├── ado.go             # CLI commands (sync, status, projects)
```

### Dependencies to add:
```
github.com/JohannesKaufmann/html-to-markdown  # HTML→Markdown conversion
# goldmark and bluemonday already in dep tree via glamour (promote to direct if needed)
```

### Existing files requiring changes:
- `cmd/bd/main.go` — blank import `_ "github.com/steveyegge/beads/internal/ado"` for init() registration (required, same as other trackers)
- `docs/CLI_REFERENCE.md` — document `bd ado` commands
- `docs/CONFIG.md` — document `ado.*` config keys

---

## Risk Areas & Mitigations

1. **JSON Patch format**: ADO requires `[{op, path, value}]` payloads. **Mitigation**: Encapsulate in `client.go` domain methods — tracker.go never sees PatchOperation directly.
2. **WIQL complexity**: Continuation tokens for large result sets. **Mitigation**: WIQL returns IDs only; batch GET in 200-ID chunks. Include `[System.IsDeleted] = false`.
3. **Process template variance**: Agile defaults won't work for Scrum/CMMI. **Mitigation**: Startup probe warns on mismatch; `ado.type_map.*` / `ado.state_map.*` config overrides.
4. **HTML↔Markdown fidelity**: Complex HTML (tables, embedded images) may not round-trip perfectly. **Mitigation**: Accept lossy conversion, document known limitations, sanitize with bluemonday.
5. **Link sync complexity**: Bidirectional graph sync is prone to infinite loops/oscillation. **Mitigation**: Dedicated LinkResolver with canonical normalization, diff-based updates, idempotency guarantees. Shipped as Milestone 2.
6. **ADO rate limiting**: ADO returns 429 with Retry-After. **Mitigation**: Same exponential backoff pattern as GitHub client.
7. **On-prem URL variance**: Different URL patterns for cloud/legacy/on-prem. **Mitigation**: Config-based URL resolution with canonical external ref normalization.
