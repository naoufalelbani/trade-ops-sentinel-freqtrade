# Contributing

Thanks for contributing to Trade Ops Sentinel.

## Development Setup

1. Fork and clone the repository.
2. Copy `.env.example` to `.env` and configure local credentials.
3. Install Go `1.22+`.
4. Run:

```bash
go mod download
go test ./...
```

## Branch and Commit Guidelines

- Create a feature/fix branch from `master`.
- Keep commits focused and descriptive.
- Use clear commit messages, for example:
  - `feat: add X`
  - `fix: handle Y`
  - `refactor: simplify Z`

## Pull Request Checklist

- Code builds locally.
- Tests pass (`go test ./...`).
- New behavior is covered with tests where practical.
- Docs are updated (`README.md` and env docs for config changes).
- PR description explains:
  - What changed
  - Why it changed
  - Any migration/config impact

## Style Notes

- Follow Go formatting (`gofmt`) and standard Go idioms.
- Keep functions focused and composable.
- Prefer explicit error handling and clear log context.

## Security and Secrets

- Do not commit real API keys, tokens, or `.env`.
- Use placeholders in examples and screenshots.
- Report security issues via `SECURITY.md` process.
