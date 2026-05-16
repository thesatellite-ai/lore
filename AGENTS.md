# AI Agent Guidelines

Go workspace with heavy **code generation**. Never modify `gen/`, `generated/`, or `ent/` directories.

## Stack

Go 1.22 | Gin | Ent ORM | gqlgen | PostgreSQL | Redis | NATS | PKL Config | OpenTelemetry

## Workspace Modules

| Module | Purpose |
|--------|---------|
| `saas/` | Core app: CLI, API bootstrap, business logic |
| `apidash/` | GraphQL API (`/api/sa`) |
| `dbent/` | Ent ORM schemas & migrations |
| `lace/` | Shared utility library (30+ packages) |
| `config/` | PKL configuration |

## Where to Make Changes

| Task | Edit | Run | Avoid |
|------|------|-----|-------|
| Database schema | `dbent/schema/*.go` | `task dbent:entg && task dbent:migrate` | `dbent/gen/ent/*` |
| GraphQL API | `apidash/internal/graph/*.graphqls` | `task apidash:gql` | `apidash/internal/graph/generated/*` |
| Configuration | `config/*.pkl` | `task config:gen` | `config/gen/*` |
| Shared utility | `lace/` | - | - |
| Business logic | `saas/pkg/` | - | - |
| CLI commands | `saas/cmd/cli/` | - | - |

## Task Commands

| Command | Purpose |
|---------|---------|
| `task docker:up` | Start Docker infrastructure |
| `task docker:down` | Stop Docker containers |
| `task dbent:entg` | Generate Ent ORM code |
| `task dbent:migrate` | Run database migrations |
| `task apidash:gql` | Generate GraphQL resolvers |
| `task config:gen` | Generate PKL config bindings |
| `task workvendor` | Sync workspace dependencies |
| `task lint` | Format and vet code |
| `task gitgen` | Stage generated files (commit as `gen`) |
| `task scripts:gh:secrets:update` | Sync .env.prod to GitHub |
| `task scripts:gh:secrets:push` | Push all secrets to GitHub |

## Context-Specific Rules

Read before performing these actions:

| Action | Read First |
|--------|------------|
| Git commit | `.claude/rules/git-commit.md` |
| New GraphQL endpoint | `.claude/rules/new-endpoint.md` |
| New Ent schema/field | `.claude/rules/new-schema.md` |
| New service | `.claude/rules/new-service.md` |
| New CLI command | `.claude/rules/new-cli-command.md` |
| New cron job | `.claude/rules/new-cron-job.md` |
| New lace package | `.claude/rules/new-lace-package.md` |
| Configuration | `.claude/rules/configuration.md` |
| NATS handler | `.claude/rules/nats-handler.md` |
| Migrations | `.claude/rules/migrations.md` |
| Writing tests | `.claude/rules/testing.md` |
| Error handling | `.claude/rules/error-handling.md` |
| Logging | `.claude/rules/logging.md` |
| Tracing | `.claude/rules/tracing.md` |
| Validation | `.claude/rules/validation.md` |
| Adding dependencies | `.claude/rules/libraries.md` |
| Authentication | `.claude/rules/authentication.md` |
| Secrets | `.claude/rules/secrets.md` |
| Debugging | `.claude/rules/debugging.md` |
| Docker/local dev | `.claude/rules/docker-dev.md` |
| Deployment | `.claude/rules/deployment.md` |
| GitHub secrets | `.claude/rules/github-secrets.md` |

## Code Conventions

```go
// Context first, error last
func DoThing(ctx context.Context, id string) (*Result, error)

// Wrap errors with context
return fmt.Errorf("doThing: %w", err)

// Import order: stdlib, third-party, local
import (
    "context"

    "github.com/gin-gonic/gin"

    "saas/pkg/app"
)
```

## Tags

We use some tag system in comments like `@tag('dnd', 'skip')` so each tag has special meaning

* `dnd` - means never remove the commented out code block

## Critical Rules

* **Never** modify generated files directly
* **Never** create helpers in `resolver/` directories (gqlgen will overwrite)
* **Never** use `graphql.Upload` for file uploads (use base64 instead)
* **Always** run migrations after schema changes
* **Always** pass `context.Context` as first param to I/O functions
* **Never** modify any file or create any file in these go modules directories `core`, `lace`, `scripts`

## Detailed Documentation

| Topic | Location |
|-------|----------|
| Architecture | `.ai/ARCHITECTURE.md` |
| Backend packages | `.claude/docs/backend/` |
| CLI reference | `.claude/docs/cli/` |
| Templates | `.claude/templates/` |

## Ignore Files and Folders

Never include these files and folders in your agentic flow or decisions or grep anything at all.

* docs/framework
