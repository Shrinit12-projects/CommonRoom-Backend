# Community backend

Go HTTP API with PostgreSQL persistence. The standalone frontend now lives in the repository's `frontend/` directory; see its README for the separate local development server. The previously embedded web interface remains available at the backend root during the transition.

Implemented flows include explicitly provisioned identities, profiles, blog drafting/preview/publishing/editing, image upload and private delivery, Q&A with duplicate links/answers/comments/votes/acceptance, tags and categories, search and filters, bookmarks and follows, notifications, recognition ledger/badges/leaderboards, reports and moderation, user administration, analytics, and audit records. Mindorigin (`@mindorigin.in`) and Firstock (`@firstock.in`) are separate organizations: authenticated content, people, taxonomy, reward rules, administration, and reporting are organization-scoped. Collections use bounded pagination; edits and reward events use transactions and idempotency controls.

## Local development (Bash)

Requires Bash, Go 1.26 or newer, Docker with Compose, and the standard `od` and `tr` commands. It works in Bash environments such as WSL, Linux, macOS, or Git Bash with Docker and Go available on `PATH`. The binary reads process environment variables; `.env.example` documents them but is not loaded automatically.

Run this from the backend directory:

```bash
bash ./start.sh
```

The script starts PostgreSQL, applies migrations, seeds organization admin accounts and a separate platform operator, and launches the API in the foreground. Employee accounts are added by the platform admin from the admin panel. Sign in with an email address and password at the frontend login page. The fixed password for the local-only platform identity `platform-admin@commonroom.local` is `Admin@123`; the seed applies it on each startup. It is only for the loopback development setup. Organization admin passwords are randomly generated once, printed once, and retained as salted PBKDF2 hashes. Set `DEV_ADMIN_PASSWORD` for the Mindorigin and Firstock organization admins; it must be 12–128 characters, is applied on every startup, and is never printed. Keep it private and out of source control. The platform operator has no organization membership, can create organizations and manage each organization's people, and cannot access tenant content. Organization admins can add and manage people only within their organization. Log in through `POST /v1/auth/login`; each successful login receives a user-specific bearer token scoped to that user's organization (platform operators receive platform-only administration access). A bootstrap bearer token is also printed for the Mindorigin administrator. Press Ctrl+C to stop the API; PostgreSQL remains running with its data. Run `docker compose stop postgres` when you want to stop the database.

Development authentication is accepted only with `APP_ENV=development`, a loopback listen address, and a secret of at least 32 characters. `POST /v1/auth/login` checks seeded local email/password credentials and returns a user-specific development bearer token. This local development mode must never be exposed through a proxy.

Production supports either company OIDC (`AUTH_MODE=oidc`) or administrator-provisioned local accounts (`AUTH_MODE=password`). Password mode requires `APP_ENV=production` and a random `AUTH_SECRET` of at least 32 characters; it signs 12-hour bearer tokens, reloads account status and role from PostgreSQL on every request, and has no public signup. Organization admins set an initial password when creating an account. Self-service recovery and an admin password-reset flow are not implemented yet. Use HTTPS, strong unique passwords, and a trusted process to communicate initial credentials. Password mode does not verify employee identity from an email suffix alone: only an authorized organization admin can create the account, and the email must match that organization's configured domain.

To create the first platform administrator in password mode, set `APP_ENV=production`, `AUTH_MODE=password`, `DATABASE_URL`, `AUTH_SECRET`, and `INITIAL_ACCOUNT_PASSWORD`, then run `go run ./cmd/community provision-user -email 'operator@your-domain.example' -name 'Platform Administrator' -role platform_admin`. The password is read from the environment, not a command-line argument. Clear `INITIAL_ACCOUNT_PASSWORD` after provisioning. The platform admin can then create the organization and its first admin. For non-platform users, the email domain must already match a configured organization.

`GET /healthz` reports process liveness. `GET /readyz` checks the database and migration checksums. Both are unauthenticated and return no employee or content data.

## Company identity integration

The production default is `AUTH_MODE=oidc`. Configure `APP_ENV=production`, `DATABASE_URL`, `HTTP_ADDR`, `OIDC_ISSUER` (HTTPS), and `OIDC_AUDIENCE` (the configured first-party client ID). The API verifies signed OIDC **ID tokens** against provider discovery/JWKS, issuer, audience, and expiry, then resolves the verified `(issuer, subject)` to an explicitly provisioned active account. Email suffixes and request headers cannot provision accounts or grant roles.

This increment supplies token verification, not a browser authorization-code/PKCE login or session service. A company-specific frontend login must obtain the ID token for the configured client. Providers requiring an OAuth access-token API contract need a dedicated access-token verifier rather than treating arbitrary access tokens as ID tokens. No employee identity provider has been configured or tested against a live company tenant.

Provision identities using trusted values from the company's identity administration system. This command requires operator database credentials; it is not an employee-facing endpoint:

```bash
export APP_ENV=production
export AUTH_MODE=oidc
export DATABASE_URL='postgres://user:password@db-host:5432/community?sslmode=verify-full'
export HTTP_ADDR='127.0.0.1:8080'
export OIDC_ISSUER='https://your-trusted-issuer.example'
export OIDC_AUDIENCE='your-first-party-client-id'
go run ./cmd/community provision-user -issuer 'https://your-trusted-issuer.example' -subject 'trusted-provider-subject' -email 'employee@your-company.example' -name 'Employee Name' -role admin
```

Use `employee` for ordinary employees, `admin` for an organization's administrator, and `platform_admin` only for a Common Room platform operator. Provision `platform_admin` only through this trusted operator command; platform operators have no organization membership and tenant admins cannot grant that role. Tenant user email domains must match an organization configured in the database; platform operator emails are independent of tenant domains. The initial organizations are `mindorigin` (`mindorigin.in`) and `firstock` (`firstock.in`). Provisioning inserts only; duplicate identities conflict rather than silently changing roles. Organization admins can manage users only in their own organization, while platform admins can create organizations and administer organization memberships without accessing tenant content. Every authenticated request reads current account status. Already running requests can finish during deactivation; automated identity-provider deprovisioning remains future work. The final active administrator in each organization cannot be demoted or deactivated through the API.

Terminate HTTPS at a trusted gateway and configure database TLS and secrets for the company environment. The API does not enable CORS; the included web UI is same-origin. Authenticated requests are limited to 600 per account per minute, failed bearer authentication to 120 attempts per socket IP per minute, and local login to 20 attempts per IP per minute. Forwarded-IP headers are not trusted; configure an appropriate gateway rate limit when running behind a proxy. Request bodies are capped at 6 MiB to allow validated image uploads up to 5 MiB; Markdown bodies are limited to 200,000 bytes and tags to eight per post.

## API and implementation

See [docs/api.md](docs/api.md) for the route contract and a complete draft-to-publication example.

```text
cmd/community/        server and operator commands
internal/config/     validated environment settings
internal/identity/   OIDC verification and active-account authentication
internal/content/    content authorization, validation, state transitions, rendering
internal/tags/       tag and proposal business rules
internal/search/     search contract and validation
internal/product/    profiles, comments, moderation, rewards, notifications, media, administration
internal/store/      PostgreSQL repositories and transactional mutations
internal/database/   embedded, checksummed migrations and readiness
internal/httpapi/    routes, transport validation, embedded HTML/CSS/JS app, errors, request IDs, rate limits
integration/         real PostgreSQL HTTP/business-rule tests
```

Migrations run explicitly through `migrate`, using a transaction and advisory lock. The server checks their checksums before accepting traffic. Do not modify applied migrations; add a new numbered SQL file. No automatic destructive rollback is provided. Use a migration-capable database role for operator commands and a least-privilege runtime role in deployment.

## Verification

```bash
go test ./...
go vet ./...
go build -o ./bin/community ./cmd/community
```

Integration tests require an explicitly selected disposable database and create a uniquely named schema, then remove that schema on completion. They do not drop the database or use production data.

```bash
docker run --detach --name company-community-test --publish 127.0.0.1:55432:5432 --env POSTGRES_PASSWORD=community_test --env POSTGRES_DB=community_test postgres:17-alpine
export TEST_DATABASE_URL='postgres://postgres:community_test@127.0.0.1:55432/community_test?sslmode=disable'
go test -tags=integration ./integration -v
docker stop company-community-test
docker rm company-community-test
```

Wait for PostgreSQL readiness before running the integration command. The tests cover real HTTP authorization, durable publishing and search, revision records, simultaneous stale edits, tag approval retries, database answer consistency, hidden content, offboarding, last-admin protection, and audits.

## Assumptions and remaining work

- Mindorigin and Firstock use strict tenant boundaries. Adding another organization/domain is currently an operator/database provisioning task; no organization-management UI or automatic identity-provider provisioning is exposed.

- Published content is visible to all active employees. Drafts are visible to their authors and moderators/admins. Hidden posts are visible only to moderators/admins. Revisions are available only to authors and moderators/admins. Team-restricted content and policies are not implemented.
- Markdown is the initial editor contract. Returned `body_html` is rendered with raw HTML disabled and sanitized using an allowlist. Render HTML only from that field; treat titles, summaries, raw Markdown, snippets, and tag text as untrusted text. Mermaid stays a fenced code block. Images are validated and stored in PostgreSQL for this initial deployment; replace this with company-approved object storage before scaling. Do not include credentials, customer data, or restricted company information in posts.
- Tag suggestions require moderator/admin approval. Moderators can create, alias, merge, and retire tags; merge preserves a canonical association and old name. Categories are admin-managed.
- Search covers published blogs and questions, author names, tag names/aliases, and categories. It supports type, tag, category, author, date, status, and relevance/newest/updated/helpful/unanswered sorting. Profiles and tags are also searchable from the same interface. Snippets currently show the first 240 Markdown characters as plain text.
- Questions support open, solved, closed, and duplicate states, reopening, and accepted-answer reversal. Moderators can hide posts, answers, and comments. Team-restricted content, answer editing, automated duplicate detection, and scheduled content freshness reviews remain future work.
- Notifications are in-app only. Recognition points and redeemable credits use separate auditable ledgers. Organization admins can configure reward rules, review full-balance credit claims, and mark approved claims fulfilled; each recognition point also earns 1.5 credits, unclaimed credits carry forward, fulfilled claims debit the credit ledger, and claim requests remain in history. External claim messaging is TODO. The requirements put helpful votes, accepted answers, and points in Phase 2 and also include them in MVP acceptance; that milestone conflict remains unresolved. AI features are not implemented.
- Production rollout still needs company identity configuration, access-policy decisions, secure media, dependency/security review, operational metrics, backup/restore procedures, and deployment configuration. See requirements sections 18–19 before declaring MVP complete.
# CommonRoom-Backend
