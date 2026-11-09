# Code Style Guide

This document defines Beacon's code-style rules. It is intentionally maintained as an evolving project document.

When adding a new rule, make it specific, actionable, and consistent with idiomatic Go. Prefer documenting decisions that prevent recurring ambiguity over documenting obvious language syntax.

## 1. General Style

- Write clear, idiomatic Go.
- Prefer readability over cleverness.
- Keep functions focused and reasonably small.
- Avoid unnecessary abstractions and indirection.
- Avoid comments that restate the code.
- Keep naming consistent with the surrounding package.
- Do not introduce unrelated formatting or refactoring changes.

## 2. Naming

- Use short, lowercase package names.
- Use `MixedCaps` or `mixedCaps`, not underscores, in Go identifiers.
- Exported identifiers use `PascalCase`.
- Unexported identifiers use `camelCase`.
- Names should describe responsibility and intent.
- Avoid stuttering names when the package name already provides context.
- Use common initialisms consistently, such as `ID`, `HTTP`, `URL`, and `JSON`.

Examples:

```go
// Good
type Registry struct{}

func (r *Registry) Register() error

// Avoid
type ServiceRegistryManager struct{}

func (r *Registry) RegisterServiceInstanceToRegistry() error
```

## 3. Errors

- Handle errors explicitly.
- Do not discard errors without a documented reason.
- Add context when returning an error from a lower-level operation.
- Use `%w` when callers may need to inspect the underlying error.
- Use `errors.Is` and `errors.As` for error inspection.
- Avoid using strings as the only mechanism for classifying errors.
- Do not panic for expected operational or input errors.

## 4. Context and Cancellation

- Pass `context.Context` as the first parameter when an operation needs cancellation or a deadline.
- Do not store request contexts in long-lived structs.
- Propagate cancellation to child operations and goroutines.
- Ensure background loops have a clear stop mechanism.

## 5. Concurrency

- Make ownership of shared mutable state clear.
- Protect shared state with appropriate synchronization.
- Keep lock scope as small as practical without sacrificing correctness.
- Do not start goroutines without considering how and when they stop.
- Avoid sleeps as the primary way to synchronize tests.
- Run race detection when changing concurrent behavior.

## 6. Interfaces and Abstractions

- Define interfaces where they are useful to consumers, boundaries, or tests.
- Prefer small interfaces.
- Avoid creating interfaces speculatively for every struct.
- Do not add a wrapper or factory when it provides no meaningful benefit.
- Keep package dependencies acyclic.

## 7. Functions and Structs

- Keep constructors straightforward.
- Avoid constructors that perform surprising network or filesystem operations.
- Make zero-value behavior clear where practical.
- Validate inputs at system boundaries.
- Avoid exposing mutable internal state unnecessarily.
- Prefer explicit options when configuration genuinely has multiple independent settings.

## 8. Comments and Documentation

- Comments should explain why, constraints, or non-obvious behavior.
- Exported declarations should have useful doc comments.
- Keep comments accurate when code changes.
- Do not add comments solely to make code appear more extensively documented.
- Document public client behavior and externally visible API contracts.

## 9. Tests

- Test behavior, edge cases, and failure paths.
- Use table-driven tests when they make related cases easier to understand.
- Keep test names descriptive.
- Avoid testing private implementation details unless necessary.
- Avoid excessive mocking.
- Ensure tests are deterministic and do not depend on arbitrary timing.
- Add concurrency tests when shared state or goroutine behavior changes.

## 10. Formatting and Static Checks

- Run `gofmt` on Go source files.
- Keep imports formatted and grouped consistently with Go conventions.
- Run `go test ./...`.
- Run `go vet ./...` where applicable.
- Run `go test -race ./...` for concurrency-sensitive changes.

---

## Project-Specific Rules — Maintainer Notes

Add Beacon-specific preferences below. These rules should be precise enough that another contributor or coding agent can apply them consistently.

### Go Style Decisions

_Add rules here._

### Naming Preferences

_Add rules here._

### Error Handling Preferences

_Add rules here._

### API and JSON Conventions

_Add rules here._

### Concurrency and Lifecycle Rules

_Add rules here._

### Testing Preferences

_Add rules here._

### Comments and Documentation

_Add rules here._

### Things to Avoid

_Add anti-patterns, unwanted patterns, or project-specific restrictions here._
