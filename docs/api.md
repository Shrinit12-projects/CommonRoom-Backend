# API contract (initial increment)

The embedded web interface is available at `/` with assets under `/assets/`. Except for the UI, health probes, and login route, API routes require `Authorization: Bearer <token>`. Production supports OIDC ID tokens or administrator-provisioned local accounts, depending on `AUTH_MODE`. Password-mode tokens are signed, expire after 12 hours, and resolve the current account and role from PostgreSQL on every request. JSON mutations require `Content-Type: application/json`; unknown fields, multiple JSON values, and non-object bodies are rejected. `POST /v1/media` is a multipart upload exception and `GET /v1/media/{id}` returns authenticated image bytes.

Successful responses wrap the result in `{"data": ...}`. Collection results contain `items`, `limit`, and `offset`. Defaults are limit 20 and offset 0; maximums are 100 and 10,000. Empty collections return `[]`. Increment offset by limit to fetch the next page; no total count is supplied. Offsets can shift under concurrent changes.

Errors have this shape:

```json
{"error":{"code":"forbidden","message":"permission denied","request_id":"..."}}
```

Status codes: 400 validation failure, 401 missing/invalid/inactive identity, 403 denied action, 404 missing or unreadable content, 409 conflicting state or stale version, 429 rate limited, 500 unexpected failure, 504 request deadline. Request IDs are also returned in `X-Request-ID`. Health probes use a standalone `status` field; readiness returns 503 when unavailable. Unknown paths return 404; unsupported methods may return the standard Go HTTP 405 response.

## Routes

| Method | Path | Behavior / permission |
| --- | --- | --- |
| GET | `/healthz`, `/readyz` | Public liveness / database schema readiness. |
| POST | `/v1/auth/login` | Development or password mode. Accepts `{ "email": "person@company.com", "password": "your-password" }`; returns a bearer token and current user. Password-mode tokens expire after 12 hours. |
| GET | `/v1/admin/organizations` | Platform administrator only. Lists organizations. |
| POST | `/v1/admin/organizations` | Platform administrator only. Creates an isolated organization and its first admin. OIDC mode requires the trusted identity subject; password mode requires `admin_password`. |
| GET | `/v1/admin/organizations/{id}/users` | Platform administrator for any organization, or an organization admin for their own organization. |
| POST | `/v1/admin/users` | Organization admin for their own organization, or platform administrator with `organization_id`. Creates an employee, moderator, or organization admin. Password mode requires `password` (12–128 characters); no public signup is available. |
| GET | `/v1/me` | Current user's own profile, role, email, and active state. Provider subject and issuer are not exposed. |
| GET | `/v1/posts` | Visible blogs/questions, newest first; use `type=blog` or `type=question`. Owners see their own drafts; moderators see all posts. |
| POST | `/v1/blogs` | Create an owned draft. Returns 201. |
| POST | `/v1/questions` | Create an owned open question. Returns 201. |
| GET | `/v1/posts/{id}` | Retrieve visible post by internal UUID. |
| PUT | `/v1/posts/{id}` | Owner replaces title, summary, Markdown, and tags; current `version` required. |
| POST | `/v1/posts/{id}/publish` | Owner publishes a blog with active tags. No request body. |
| POST | `/v1/posts/{id}/unpublish` | Owner returns a blog to draft. No request body. |
| POST | `/v1/posts/{id}/close` | Owner or moderator closes a question. No request body. |
| GET | `/v1/posts/{id}/revisions` | Paginated snapshots; owner or moderator/admin only. |
| GET | `/v1/questions/{id}/answers` | Paginated visible thread answers, accepted answer first. |
| POST | `/v1/questions/{id}/answers` | Answer an open/solved visible question; returns 201. Body: `{"body_markdown":"..."}`. |
| PUT | `/v1/questions/{id}/accepted-answer` | Author or moderator/admin accepts `{"answer_id":"UUID"}`. Answer must belong to that question. Repeating the same acceptance is idempotent. Closed/hidden questions reject changes. |
| GET | `/v1/tags` | Paginated tag directory including deprecated tags and their status. |
| POST | `/v1/tags` | Moderator/admin creates a tag; returns 201. |
| PATCH | `/v1/tags/{id}` | Moderator/admin changes `{"status":"active"}` or `deprecated`; audited. |
| GET | `/v1/tag-proposals` | Own proposals; moderators/admins see all. Paginated. |
| POST | `/v1/tag-proposals` | Propose a tag with a reason; returns 201. |
| PUT | `/v1/tag-proposals/{id}/decision` | Moderator/admin submits `{"decision":"approved"}` or `rejected`. Approval creates a tag atomically; repeating a decision is idempotent. |
| GET | `/v1/search` | Published blogs/questions only; `q` required, 2â€“200 bytes. |
| PUT | `/v1/moderation/posts/{id}` | Moderator/admin hides or restores a whole post/thread using `{"hidden":true}` or `false`; audited. |
| PATCH | `/v1/admin/users/{id}` | Admin supplies both `role` (`employee`, `moderator`, `admin`) and `active` boolean. Final active admin is protected; audited. |
| GET | `/v1/admin/audit-logs` | Admin-only paginated administrative events. |

### Community, rewards, and administration routes

| Method | Path | Behavior / permission |
| --- | --- | --- |
| POST | `/v1/preview` | Authenticated Markdown preview returning sanitized HTML without persistence. |
| POST | `/v1/posts/{id}/reopen` | Owner or moderator reopens a closed, solved, or duplicate question. |
| POST | `/v1/questions/{id}/duplicate` | Owner or moderator links an open question to a canonical question using `{"target_id":"UUID"}`. |
| GET/POST | `/v1/categories` | List active categories; moderators can see all, moderators/admins can create. |
| PATCH | `/v1/categories/{id}` | Moderator/admin activates or deactivates a category with `{"active":true}`. |
| GET | `/v1/people`, `/v1/people/{id}` | Search active profiles and read visible contributions. Emails and provider identity are omitted. |
| PUT | `/v1/me/profile` | Update the current display name and bio. |
| GET | `/v1/me/bookmarks` | List the current user's saved visible posts. |
| PUT | `/v1/posts/{id}/bookmark` | Save or remove a bookmark with `{"enabled":true}`. |
| POST | `/v1/posts/{id}/votes`, `/v1/answers/{id}/votes` | Cast/remove a helpful vote; one vote per user and target, with self-votes rejected. |
| GET/POST | `/v1/posts/{id}/comments`, `/v1/answers/{id}/comments` | Read or add comments on readable content. |
| POST | `/v1/reports` | Report a post, answer, or comment with target type/id, reason, and details. |
| GET/PUT | `/v1/moderation/reports`, `/v1/moderation/reports/{id}` | Moderator/admin review queue and resolve/dismiss actions. Resolution hides the reported item. |
| PUT | `/v1/moderation/answers/{id}`, `/v1/moderation/comments/{id}` | Moderator/admin hides/restores an answer or comment. |
| PUT, GET | `/v1/tags/{id}/follow`, `/v1/me/followed-tags` | Follow/unfollow active tags and list followed tags. |
| GET/PUT | `/v1/me/notifications`, `/v1/me/notifications/{id}/read`, `/v1/me/notifications/read-all` | List current-user notifications and mark them read. |
| GET | `/v1/me/points`, `/v1/leaderboard`, `/v1/reward-rules` | Recognition ledger, period leaderboards, and reward rules. |
| PUT | `/v1/admin/reward-rules/{event}` | Admin changes points, enablement, and daily cap; writes an audit record and advances rule version. |
| GET | `/v1/tags/{id}` | Read canonical tag details. |
| POST | `/v1/tags/{id}/aliases`, `/v1/moderation/tags/{id}/merge` | Moderator/admin manages aliases and merges while preserving associations and old tag links. |
| PUT | `/v1/moderation/posts/{id}/editorial` | Moderator/admin sets featured or verified state. |
| GET | `/v1/admin/analytics`, `/v1/admin/users` | Admin usage counts and paginated people search. |
| POST/GET | `/v1/media`, `/v1/media/{id}` | Upload PNG/JPEG/GIF via multipart `post_id` and `file` (max 5 MiB), or retrieve after access checks. |

`GET /v1/posts` and `/v1/search` accept `type`, `tag_id`, `category_id`, `author_id`, `status`, `from`, `to`, `sort`, `limit`, and `offset`. Dates use `YYYY-MM-DD`. Authors can list their own private drafts with `status=draft`; other employees cannot see those drafts. The frontend keeps drafts out of Home and lists them under Stories â†’ Your drafts. Search supports relevance, newest, updated, helpful, and unanswered sorting and also matches category names, tag aliases, and employee profiles. Search analytics store a one-way query hash and result count, not query text or employee ID. Snippets are plain-text excerpts.

There are no public signup or employee-ID login routes. In password mode, an organization admin provisions accounts and sets their initial passwords; self-service password recovery and admin reset endpoints are not implemented. Operator provisioning is described in [README.md](../README.md).

## Inputs and filters

Blog/question create and update body:

```json
{
  "title": "Managing Redis connections in Go",
  "summary": "A practical guide",
  "body_markdown": "# Connection pools\n\nUse a bounded pool for service connections.",
  "tag_ids": ["11111111-1111-4111-8111-111111111111"],
  "version": 1
}
```

Omit `version` when creating. Use the latest returned version on updates; a stale version returns 409. Title length is 3â€“200 characters, summary at most 500 characters, Markdown 10â€“200,000 bytes, and distinct tags 1â€“8. New tags and all tags on publication must be active; existing deprecated associations may remain during edits. Posts return sanitized `body_html`, original `body_markdown`, tags, author ID/name, status, version, timestamps, visibility, and accepted-answer ID where applicable. Full replacements avoid ambiguous partial edits; no deletion endpoint exists yet.

Tag and proposal body:

```json
{"name":"Go","slug":"go","description":"Go programming","reason":"Used by backend teams"}
```

Tag names and slugs are limited to 60 characters. Slugs normalize to lowercase and allow letters, numbers, and single hyphens. Names are unique ignoring case; slugs are unique. Description/reason limits are 2,000 bytes each; proposals require a reason. Direct tag creation does not use the reason.

Request bodies are capped at 6 MiB; image payloads are capped at 5 MiB. Markdown input is capped at 200,000 bytes. `GET /v1/posts` and `/v1/search` accept `type`, `tag_id`, `category_id`, `author_id`, `status`, `from`, `to`, `sort`, `limit`, and `offset`. Dates use `YYYY-MM-DD`. Global search excludes drafts and hidden posts. Search defaults to relevance and accepts newest/updated/helpful/unanswered; post lists default to newest and support updated/helpful/unanswered. Search matches weighted title/summary/body terms plus author/title/tag/category/alias matches. Snippets are plain-text excerpts, not highlighted HTML.

## Local draft-to-publication example

With the development server running and the token available in this terminal:

```bash
export DEV_AUTH_TOKEN='paste-the-token-printed-by-start.sh'
BASE='http://127.0.0.1:8080'
AUTH="Authorization: Bearer $DEV_AUTH_TOKEN"

curl --fail-with-body -sS -X POST "$BASE/v1/tags" \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"name":"Go","slug":"go","description":"Go programming"}'

# Paste the tag UUID returned above.
read -r -p 'Tag UUID: ' TAG_ID
curl --fail-with-body -sS -X POST "$BASE/v1/blogs" \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"title\":\"Connection pooling in Go\",\"summary\":\"Internal service guidance\",\"body_markdown\":\"# Connections and pools\\n\\nKeep service connection pools bounded and monitor their usage.\",\"tag_ids\":[\"$TAG_ID\"]}"

# Paste the draft UUID returned above.
read -r -p 'Draft UUID: ' DRAFT_ID
curl --fail-with-body -sS -X POST "$BASE/v1/posts/$DRAFT_ID/publish" -H "$AUTH"
curl --fail-with-body -sS "$BASE/v1/search?q=pooling&type=blog" -H "$AUTH"
```

Creating the same `go` tag twice returns 409; reuse its ID from `/v1/tags` on subsequent runs.

## Feedback, appreciation, and comment threads

`POST /v1/feedback` accepts `{"mode":"request|give","people":["user UUID",...],"message":"..."}`. A request asks the selected teammates for feedback; `give` sends feedback to the selected teammates. `POST /v1/appreciation` accepts `{"people":[...],"message":"..."}` for one or more colleagues. All submissions start pending and are visible to the author and administrators. Feedback becomes visible to its selected participants only after approval; approved appreciation appears to all signed-in employees. `GET /v1/feedback` and `GET /v1/appreciation` return content the caller may see. Admins review the pending queue using `?pending=true` and decide with `PUT /v1/admin/community-submissions/{id}` and `{"decision":"approved|rejected"}`. Decisions are audited and approval notifies participants.

Comments support nested replies. Create a top-level comment with the existing post/answer comment endpoint, or reply using `POST /v1/comments/{comment_id}/replies` with `{"body":"..."}`. Comment listing includes `parent_id`; clients can render nested threads.

Question creation may omit `title`; the backend derives a searchable display title from the question body. The UI asks for the question and its details with a category and topics, without a separate title field.

Question create and update require an active category and 1–8 active topics. The question body is used to derive its searchable title when title is omitted.

## Organizations

The initial tenants are Mindorigin (@mindorigin.in) and Firstock (@firstock.in). In OIDC mode, a verified identity is provisioned into the organization matching its email domain. In password mode, an organization admin creates the account and sets its initial password; email domain matching is an additional tenant boundary, not proof of identity. Content, search results, people, taxonomy, rewards, and admin actions are scoped to that organization. Local development provides separate accounts for both tenants; use `/v1/auth/login` to receive a user-specific bearer token.
