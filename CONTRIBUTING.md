# Contributing to Beacon

Thank you for your interest in contributing to Beacon.

Beacon aims to remain lightweight, understandable, and dependable. Contributions should solve a clear problem without adding unnecessary complexity.

## Development Principles

- Keep changes focused and easy to review.
- Prefer idiomatic Go and the standard library.
- Avoid unrelated refactoring.
- Add tests for behavior changes and bug fixes.
- Update documentation when externally observable behavior changes.
- Discuss architectural or public API changes before implementing them.

## Development Workflow

1. Check the existing issues and discussions for related work.
2. For substantial changes, open an issue or discussion before implementation.
3. Create a focused branch from the main development branch.
4. Implement the change and add or update tests.
5. Format Go code with `gofmt`.
6. Run the relevant tests and checks.
7. Review the complete diff before opening a pull request.
8. Open a pull request with a clear explanation of the problem and solution.

Suggested branch names:

- `feat/automatic-cleanup`
- `feat/go-client`
- `fix/heartbeat-timeout`
- `refactor/registry-interface`
- `docs/api-documentation`
- `test/concurrent-registration`
- `chore/docker-setup`

Use short, descriptive names. Branch prefixes should describe the type of work.

## Commit Messages

Beacon follows the Conventional Commits format:

`<type>(<scope>): <description>`

### Types

| Type | Purpose |
|---|---|
| `feat` | Add user-facing functionality |
| `fix` | Fix a bug |
| `refactor` | Change internal implementation without changing behavior |
| `test` | Add, update, or improve tests |
| `docs` | Change documentation |
| `build` | Change build system or dependencies |
| `ci` | Change CI/CD workflows |
| `perf` | Improve performance |
| `chore` | Maintenance that does not fit another type |
| `style` | Formatting-only changes with no logic impact |
| `revert` | Revert a previous commit |

### Scopes

Use a scope when it makes the affected area clearer. Suggested scopes include:

`registry`, `api`, `client`, `config`, `health`, `discovery`, `docker`, `observability`, `server`, `deps`, `github`, `readme`.

Omit the scope when the change spans the project or no scope adds useful information.

### Rules

- Write commit messages in English.
- Use lowercase type and scope.
- Use an imperative description: `add`, `fix`, `remove`, `update`.
- Do not end the subject with a period.
- Keep the subject concise and specific.
- Keep one logical change per commit.
- Use `!` for a breaking change, for example `feat(api)!: change registration contract`.
- Explain breaking changes in the commit body and release notes where appropriate.

### Examples

```text
feat(registry): add service registration
fix(api): return 404 for unknown service IDs
refactor(server): introduce graceful shutdown
test(registry): cover concurrent registration
docs(api): document service registration endpoints
build(deps): update router dependency
ci(github): add Go test workflow
```

## Pull Requests

A pull request should:

- Explain what changed and why.
- Describe relevant design decisions and trade-offs.
- Include test instructions and their results.
- Mention configuration, API, or compatibility changes.
- Link related issues where applicable.
- Avoid bundling unrelated changes.

Use the repository pull request template when available.

## Tests and Checks

At minimum, run the checks relevant to your change. Common Go checks include:

```bash
mise run check
```

This formats the code and runs `go vet`, `golangci-lint`, the tests with the race detector and `govulncheck`. CI runs the same checks on every pull request.

Do not report checks as passing unless they were actually executed. If a check cannot be run, explain why.

## Scope of Contributions

Large architectural changes, new infrastructure dependencies, public API changes, and compatibility changes should be discussed before implementation.

The maintainers may request changes that improve clarity, reduce complexity, or keep Beacon aligned with its goals.
