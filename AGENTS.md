# AI Agent Instructions

These instructions are the project-wide source of truth for AI coding agents working on Beacon.

## 1. Project Overview

Beacon is a lightweight, self-hosted service discovery and service registry solution written in Go.

Its purpose is to let services register themselves, discover healthy service instances, and keep registrations up to date through heartbeats and expiration.

The project is intended to be practical, understandable, and suitable for real-world use. Prefer a small, reliable design over unnecessary abstraction.

## 2. Read Before Changing Code

Before making changes:

1. Read this file and `docs/engineering-standards.md`.
2. Read `docs/code-style.md` and follow its current rules.
3. Inspect the existing implementation and tests in the affected area.
4. Identify the package boundaries and existing conventions before proposing a redesign.

Do not assume that planned features or documented target structure have already been implemented. Treat the code as the source of truth for current behavior.

## 3. Core Engineering Principles

- Prefer simple, idiomatic, maintainable Go.
- Keep changes focused and proportional to the task.
- Avoid speculative abstractions, premature generalization, and unnecessary layers.
- Prefer the Go standard library. Add dependencies only when they provide clear value.
- Do not add comments that merely repeat what the code already says.
- Do not introduce unrelated refactoring while implementing a feature or fix.
- Preserve existing behavior unless the task explicitly requires a behavior change.
- Do not silently change public APIs, configuration formats, or compatibility expectations.

## 4. Architecture Boundaries

Follow these intended boundaries as the project evolves:

- `cmd/` contains executable entry points and startup wiring.
- `internal/` contains private application implementation.
- `internal/registry/` owns registry state and registry operations. It must not depend on HTTP transport.
- HTTP handlers translate requests and responses. They must not own core business rules.
- `client/` contains the public Go client/SDK intended for use by other Go applications.
- Configuration, lifecycle, health, and observability concerns should remain separate where that separation provides real value.
- Dependencies should point inward toward core behavior. Avoid circular dependencies.

Do not create packages or layers solely to match a proposed directory tree. Introduce them when there is a clear responsibility for them.

## 5. Go Implementation Rules

- Handle errors explicitly; do not ignore errors without a clear reason.
- Wrap errors with context when useful, using `%w` when callers may need `errors.Is` or `errors.As`.
- Do not use `panic` for expected runtime or input errors.
- Use `context.Context` for cancellation and deadlines across operations that need them.
- Every long-running goroutine must have a clear shutdown mechanism.
- Protect shared mutable state and consider race conditions.
- Keep constructors and exported APIs small and unsurprising.
- Validate external input at system boundaries.
- Avoid global mutable state where practical.

See `docs/code-style.md` for the detailed, evolving code-style rules.

## 6. Testing and Quality

For relevant changes:

- Add or update tests for observable behavior.
- Prefer focused unit tests for core logic.
- Use table-driven tests when they improve clarity, not by default for every test.
- Add HTTP or integration tests when behavior crosses package or process boundaries.
- Do not delete or weaken tests merely to make a change pass.
- Run `gofmt` on changed Go files.
- Run `go test ./...`.
- Run `go test -race ./...` when concurrency or shared state is affected, and before significant releases when practical.
- Review the final diff for unrelated changes, dead code, misleading comments, and accidental API changes.

Do not claim that a command or test passed unless it was actually run.

## 7. Dependencies and Tooling

Before adding a dependency:

1. Check whether the standard library or an existing dependency already solves the problem.
2. Consider maintenance, security, compatibility, and transitive dependency cost.
3. Explain why the dependency is needed.

Keep build, test, formatting, linting, and container workflows reproducible.

## 8. Git and Commit Conventions

Use Conventional Commits:

`<type>(<scope>): <short description>`

Examples:

- `feat(registry): add service registration`
- `fix(api): return 404 for unknown service IDs`
- `refactor(server): introduce graceful shutdown`
- `test(registry): cover concurrent registration`
- `docs(api): document registration endpoints`
- `build(deps): update router dependency`
- `ci(github): add Go test workflow`

Use English, lowercase types/scopes, imperative descriptions, and no trailing period. Keep each commit focused on one logical change. See `CONTRIBUTING.md` for the full convention.

AI agents must not create commits, tags, branches, or pull requests unless the user explicitly asks.

## 9. Required Workflow

For implementation tasks:

1. Understand the request and inspect relevant code.
2. State any important assumptions or design trade-offs before making a non-trivial change.
3. Implement the smallest complete solution.
4. Add or update tests.
5. Format and run relevant checks.
6. Inspect the diff.
7. Summarize what changed, which checks were run, and any remaining limitations.

If requirements are ambiguous and the ambiguity affects architecture, compatibility, data handling, or security, ask before proceeding.

## 10. Scope and Approval

Do not do any of the following without explicit approval:

- Replace or redesign the overall architecture.
- Introduce a database, message broker, distributed consensus, or another major infrastructure dependency.
- Add a new third-party dependency that materially affects the project.
- Change public API contracts or compatibility guarantees.
- Remove existing functionality or tests.
- Create commits, tags, releases, branches, or pull requests.
- Add features that were not requested merely because they seem useful.

When a request is limited to analysis or review, do not modify files.
