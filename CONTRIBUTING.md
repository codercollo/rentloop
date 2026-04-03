# Contributing to RentLoop

Thank you for your interest in contributing. This document covers everything
you need to get a change merged cleanly.

---

## Table of Contents

- [Development Setup](#development-setup)
- [Branching Strategy](#branching-strategy)
- [Commit Messages](#commit-messages)
- [Pull Request Process](#pull-request-process)
- [Coding Standards](#coding-standards)
- [Testing Requirements](#testing-requirements)
- [Adding a New Bot Command](#adding-a-new-bot-command)
- [Adding a Database Migration](#adding-a-database-migration)
- [Reporting Bugs](#reporting-bugs)

---

## Development Setup

```bash
# 1. Fork and clone
git clone https://github.com/your-fork/rentloop.git
cd rentloop

# 2. Start infrastructure
docker-compose up -d

# 3. Configure environment
cp .env.example .env
# Edit .env — minimum: DATABASE_URL is already set for docker-compose defaults

# 4. Run migrations
make migrate

# 5. Run the server
make run

# 6. Run tests
make test
```

---

## Branching Strategy

| Branch | Purpose |
|---|---|
| `main` | Production-ready code. All merges trigger CI/CD deployment. |
| `feature/*` | New features |
| `fix/*` | Bug fixes |
| `chore/*` | Refactors, dependency updates, docs |

Branch from `main`, open a PR back to `main`.

```bash
git checkout main && git pull
git checkout -b feature/receipt-resend-command
```

---

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

[optional body]

[optional footer: Closes #123]
```

Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `perf`

Examples:
```
feat(receipt): add resend command for landlord bot
fix(mpesa): handle missing MSISDN in C2B callback
docs(readme): add deployment prerequisites section
chore(deps): upgrade gofpdf to v2
```

---

## Pull Request Process

1. Keep PRs focused — one logical change per PR.
2. All tests must pass: `make test`.
3. No linting errors: `make vet`.
4. Update relevant documentation (README, ARCHITECTURE, openapi.yaml) if your
   change affects public behaviour or API contracts.
5. New public functions must have a Go doc comment.
6. Fill in the PR template completely.

### PR Template

```markdown
## Summary
<!-- What does this PR change and why? -->

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Refactor / chore
- [ ] Documentation

## Testing
<!-- How did you verify this works? -->
- [ ] Unit tests added/updated
- [ ] Manually tested (describe steps)

## Checklist
- [ ] `make test` passes
- [ ] `make vet` passes
- [ ] Docs updated if needed
- [ ] No secrets or credentials in the diff
```

---

## Coding Standards

### General

- Go 1.22+. Use standard library where possible.
- `gofmt` formatting is enforced (CI will reject unformatted code).
- Prefer explicit errors over panics.
- Context must be the first argument of any function that does I/O.
- All exported types and functions need a doc comment.

### Package layout

Each internal package has:
- One file per logical concern (`handler.go`, `service.go`, `repository.go`)
- An interface for every external dependency (enables unit testing without
  a real DB or HTTP server)
- `*_test.go` files in the same package (white-box) or `*_test` package
  (black-box) as appropriate

### Error handling

```go
// ✅ Wrap with context
return nil, fmt.Errorf("receipt: generate pdf: %w", err)

// ❌ Don't swallow errors silently
_ = someFunc()

// ✅ Non-fatal errors get logged, not returned
slog.Error("receipt: insert row failed", "payment_id", id, "error", err)
```

### Logging

Use `log/slog` exclusively. Structured key-value pairs only:

```go
slog.Info("payment: recorded",
    "payment_id", p.ID,
    "amount",     p.Amount,
    "status",     p.Status,
)
```

Never use `fmt.Println` or `log.Printf` in application code.

---

## Testing Requirements

Every new feature must include:

1. **Unit tests** for business logic (service layer, helpers).
2. **Integration tests** are optional but encouraged for repository methods.

Tests must not require a running server, real database, or external APIs.
Use interfaces and fakes:

```go
type fakeRepo struct { ... }
func (f *fakeRepo) GetReceiptByPaymentID(...) (*models.Receipt, error) { ... }
```

Run the full test suite before opening a PR:

```bash
make test          # all tests
make test-race     # with race detector (required for concurrent code)
make test-cover    # coverage report
```

Coverage target: **> 70%** for new packages.

---

## Adding a New Bot Command

1. Add the command constant to `internal/bot/commands.go` (or wherever
   command strings are defined).
2. Add a handler function `cmd<Name>()` in `internal/bot/service.go`.
3. Add the case to the `switch` in `Handle()`.
4. Add the command to the `HELP` response.
5. Add it to the commands table in `README.md`.
6. Write a unit test in `internal/bot/service_test.go`.

---

## Adding a Database Migration

Migrations live in `internal/db/migrations/` and are run by `cmd/migrate`.

```bash
# Create a new migration file (use the next sequential number)
touch internal/db/migrations/007_add_receipt_url_index.sql
```

Migration files must be:
- **Idempotent** — use `IF NOT EXISTS`, `IF EXISTS`, `ON CONFLICT DO NOTHING`
- **Numbered** — `001_`, `002_`, … applied in order
- **Non-destructive** — never `DROP` a column that production data depends on
  without a multi-step migration plan

Example:
```sql
-- 007_add_receipt_url_index.sql
CREATE INDEX IF NOT EXISTS idx_receipts_public_url ON receipts (public_url);
```

---

## Reporting Bugs

Open a GitHub Issue with:

1. **Summary** — one-line description
2. **Steps to reproduce** — exact curl commands or WhatsApp messages
3. **Expected behaviour**
4. **Actual behaviour** — paste the relevant server logs
5. **Environment** — `APP_ENV`, Go version, OS
