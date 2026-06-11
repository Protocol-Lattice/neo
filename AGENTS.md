# AGENTS.md

## Purpose

This repository uses agent-assisted development with a disciplined workflow.

## Agent Stack

Use this stack together, in this priority order:

1. **Superpowers** — stay disciplined, verify assumptions, avoid overengineering.
2. **RTK** — inspect, navigate, and execute repository changes safely.
3. **grill-me** — stress-test the implementation plan before coding.
4. **cc-golang-skills** — write idiomatic, tested, production-quality Go.
5. **Caveman** — compress communication and summaries without losing facts, commands, paths, errors, or test results.

`grill-me` is the plan-review layer. It must challenge weak assumptions, missing edge cases, risky scope, vague requirements, and untested behavior before implementation starts.

Caveman is the communication layer, not a shortcut. It must never override Superpowers, RTK, grill-me, cc-golang-skills, tests, safety, or correctness.

---

The agent must use:

* **Superpowers** for disciplined agent behavior and careful execution.
* **RTK** as the runtime/tooling layer for repository inspection, navigation, and task execution.
* **grill-me** before implementation to critique the plan, risks, assumptions, edge cases, and test strategy.
* **cc-golang-skills** for idiomatic, tested, production-quality Go development.
* **Caveman** for terse, token-efficient agent communication when it does not reduce correctness, safety, or useful technical detail.

The goal is to produce small, correct, maintainable changes without inventing behavior or rewriting unrelated code.

---

## Core Rules

1. Do not jump directly into coding.
2. Read this `AGENTS.md` before making changes.
3. Inspect the current repository before proposing implementation.
4. Do not invent files, APIs, packages, or behavior.
5. Prefer simple, boring, maintainable Go.
6. Make small, reviewable changes.
7. Keep public APIs stable unless the task explicitly requires breaking changes.
8. Add or update tests for every behavior change.
9. Run formatting and tests before finishing.
10. Be honest about what was changed, tested, or not tested.
11. Use `grill-me` to challenge plans before implementation, especially for non-trivial changes.
12. Use Caveman only to reduce output noise; never to reduce engineering rigor.

---

## Required Workflow

For every non-trivial task, follow this order:

1. **Read project instructions**

   * Read `AGENTS.md`.
   * Check existing README, docs, examples, tests, and package layout.

2. **Inspect with RTK**

   * Use RTK to inspect the repository structure.
   * Identify relevant packages, commands, tests, examples, and existing conventions.
   * Do not assume architecture from memory.

3. **Plan**

   * Produce a short implementation plan before coding.
   * Include the problem, goals, constraints, risks, and acceptance criteria when useful.
   * Prefer minimal changes.
   * Avoid broad rewrites unless explicitly requested.

4. **Review with grill-me**

   * Stress-test the implementation plan before coding.
   * Challenge unclear requirements, hidden assumptions, missing edge cases, risky scope, and weak tests.
   * Revise the plan if `grill-me` finds a real gap.
   * Do not use `grill-me` as an excuse to overcomplicate the change.

5. **Implement**

   * Use idiomatic Go.
   * Keep interfaces small.
   * Use explicit error handling.
   * Use `context.Context` where cancellation, deadlines, or request scope matter.
   * Avoid unnecessary abstraction.
   * Avoid global mutable state unless clearly justified.
   * Preserve existing behavior unless the task requires changing it.

6. **Test**

   * Add or update tests.
   * Prefer table-driven tests where useful.
   * Cover success paths and failure paths.
   * Do not fake test results.

7. **Verify**

   * Run:

     ```sh
     gofmt -w .
     go test ./...
     go vet ./...
     ```

   * If the repository uses additional commands, run them too.

8. **Report**

   * Summarize:

     * What changed
     * Why it changed
     * Files changed
     * Tests run
     * Known limitations or follow-ups

---

## RTK Usage

Use RTK as the execution and repository-navigation layer.

The agent should use RTK to:

* Inspect the work tree.
* Read relevant files.
* Understand current architecture.
* Locate tests and examples.
* Apply focused changes.
* Avoid accidental unrelated edits.

RTK must not be used as an excuse to skip planning or testing.

Before editing files, the agent must understand the current code path being changed.

---

## grill-me Usage

Use `grill-me` after repository inspection and planning, before implementation.

The agent should use `grill-me` to ask:

* Is the plan too broad for the requested change?
* Are there hidden assumptions that need verification from the repository?
* Are edge cases, failure paths, and compatibility concerns covered?
* Are tests specific enough to prove the behavior changed correctly?
* Could the same goal be achieved with a smaller diff?
* Is the plan preserving existing public APIs and behavior unless explicitly changed?

`grill-me` must not be used to:

* Replace repository inspection.
* Delay simple fixes with unnecessary process.
* Add speculative features.
* Expand scope beyond the user request.
* Skip tests or verification.

Recommended activation language for agent prompts:

```text
Use grill-me to challenge the plan before coding.
Keep the critique focused on assumptions, edge cases, scope, and tests.
Do not expand scope beyond the task.
```

---

## Superpowers Usage

Use Superpowers for disciplined behavior.

The agent must:

* Slow down before changing architecture.
* Verify assumptions from the repository.
* Prefer reversible changes.
* Avoid overengineering.
* Keep changes understandable.
* Stop and report uncertainty instead of inventing facts.
* Treat tests as part of the implementation, not as an afterthought.

---

## cc-golang-skills Usage

Use cc-golang-skills for all Go implementation work.

Go code must be:

* Idiomatic
* Formatted with `gofmt`
* Tested
* Explicit about errors
* Minimal in dependencies
* Clear in package boundaries
* Safe for concurrent use where applicable
* Easy to read and maintain

Prefer the Go standard library unless a dependency is already present or clearly necessary.

---

## Caveman Usage

Use Caveman as an output-compression and communication style layer.

Caveman is allowed when the task benefits from shorter agent messages, lower token usage, or faster review. It must not weaken the engineering workflow.

The agent should use Caveman to:

* Remove filler, repetition, and unnecessary explanations.
* Keep status updates short and action-focused.
* Prefer compact bullets, diffs, tables, and exact commands over long prose.
* Compress summaries while preserving important facts, file paths, commands, and test results.
* Keep code, tests, errors, and API names precise.

Caveman must not be used to:

* Skip planning, repository inspection, tests, or verification.
* Hide uncertainty or omit important risks.
* Make code comments, public documentation, error messages, or user-facing API text unclear.
* Replace technical accuracy with jokes or vague shorthand.
* Compress away security, correctness, migration, or compatibility notes.

When Caveman conflicts with correctness, safety, tests, or maintainability, correctness wins.

Recommended activation language for agent prompts:

```text
Use Caveman mode for concise output only.
Keep reasoning, code, tests, and verification precise.
No filler. No invented facts. No skipped workflow.
```

---

## Go Engineering Standards

### Package Design

* Keep package responsibilities clear.
* Avoid circular dependencies.
* Do not expose internals unnecessarily.
* Use `internal/` for private application or framework implementation.
* Use `pkg/` only for stable public packages intended for external use.
* Keep `cmd/` packages thin.

### Error Handling

* Return errors explicitly.
* Wrap errors with useful context.
* Do not leak unsafe internal errors to users or clients.
* Do not use panic for normal control flow.

### Context

Use `context.Context` when functions involve:

* Network calls
* I/O
* Long-running work
* Request-scoped operations
* Cancellation
* Deadlines

Do not store context in structs unless there is a strong reason.

### Testing

* Prefer table-driven tests.
* Test public behavior.
* Include edge cases.
* Include error cases.
* Avoid brittle tests tied to implementation details.
* Use benchmarks only when performance is part of the task.

### Concurrency

* Avoid shared mutable state when possible.
* Protect shared state with synchronization.
* Use channels only when they simplify ownership or coordination.
* Avoid goroutine leaks.
* Respect context cancellation.

---

## Architecture Rules

Do not perform broad architecture changes unless explicitly requested.

When architecture changes are requested:

1. Inspect the current layout.
2. Identify existing package responsibilities.
3. Propose a target layout.
4. Explain migration steps.
5. Move code in small increments.
6. Keep tests passing at each step.

For Go projects, prefer this general structure when appropriate:

```text
cmd/
  neo-gen/          # CLI tools and entry points

pkg/
  router/           # Public API (stable, for external use)
  client/

internal/
  transport/        # Internal transport implementations
    http/
    websocket/
    binary/
  codec/            # Internal codec/serialization
    json/
  broker/           # Internal message broker
  errors/           # Internal error handling
  middleware/       # Internal middleware chains
  procedure/        # Internal RPC procedure logic

examples/           # Runnable examples
docs/               # Documentation
.github/
  workflows/        # CI/CD pipelines

go.mod
go.sum
Makefile            # Common: build, test, lint, format
```

Only use this structure when it fits the project. Do not force it blindly.

---

## Dependency Rules

* Do not add dependencies without justification.
* Prefer existing dependencies already used by the project.
* Prefer the standard library.
* Avoid large frameworks unless the project already depends on them.
* Update `go.mod` and `go.sum` only when necessary.

---

## Git and Change Hygiene

The agent must:

* Avoid unrelated formatting churn.
* Avoid rewriting files unnecessarily.
* Keep diffs focused.
* Preserve comments unless they are wrong or obsolete.
* Do not remove tests to make failures disappear.
* Do not silently delete features.

---

## Final Response Format

After completing a task, respond with:

````md
## Summary

- ...

## Files Changed

- ...

## Tests Run

```sh
...
```

## Notes

- ...
````

If tests were not run, say why.

Do not claim tests passed unless they were actually run.

---

## Forbidden Behavior

The agent must not:

- Invent missing APIs.
- Invent test results.
- Ignore failing tests.
- Rewrite unrelated code.
- Add unnecessary abstractions.
- Change module paths without instruction.
- Remove features silently.
- Hide uncertainty.
- Treat generated code as hand-written code unless required.
- Make large architecture changes without a short plan.

---

## Default Task Prompt

When asked to work on this repository, use this operating mode:

```text
Use AGENTS.md as the primary development instruction file.

Use Superpowers for disciplined agent behavior.
Use RTK for repository inspection and execution.
Use grill-me to challenge the implementation plan before coding.
Use cc-golang-skills for idiomatic, tested Go.
Use Caveman mode for concise, low-token output only; do not skip Superpowers, RTK inspection, grill-me review, cc-golang-skills standards, tests, or precision.

Do not jump directly into coding.
Inspect the repository first.
Create a minimal implementation plan.
Run grill-me against the plan.
Make small, reviewable changes.
Add or update tests.
Run gofmt, go test ./..., and go vet ./....
Summarize changes honestly.
```

<!-- code-review-graph MCP tools -->
## MCP Tools: code-review-graph

**IMPORTANT: This project has a knowledge graph. ALWAYS use the
code-review-graph MCP tools BEFORE using Grep/Glob/Read to explore
the codebase.** The graph is faster, cheaper (fewer tokens), and gives
you structural context (callers, dependents, test coverage) that file
scanning cannot.

### When to use graph tools FIRST

- **Exploring code**: `semantic_search_nodes` or `query_graph` instead of Grep
- **Understanding impact**: `get_impact_radius` instead of manually tracing imports
- **Code review**: `detect_changes` + `get_review_context` instead of reading entire files
- **Finding relationships**: `query_graph` with callers_of/callees_of/imports_of/tests_for
- **Architecture questions**: `get_architecture_overview` + `list_communities`

Fall back to Grep/Glob/Read **only** when the graph doesn't cover what you need.

### Key Tools

| Tool | Use when |
| ------ | ---------- |
| `detect_changes` | Reviewing code changes — gives risk-scored analysis |
| `get_review_context` | Need source snippets for review — token-efficient |
| `get_impact_radius` | Understanding blast radius of a change |
| `get_affected_flows` | Finding which execution paths are impacted |
| `query_graph` | Tracing callers, callees, imports, tests, dependencies |
| `semantic_search_nodes` | Finding functions/classes by name or keyword |
| `get_architecture_overview` | Understanding high-level codebase structure |
| `refactor_tool` | Planning renames, finding dead code |

### Workflow

1. The graph auto-updates on file changes (via hooks).
2. Use `detect_changes` for code review.
3. Use `get_affected_flows` to understand impact.
4. Use `query_graph` pattern="tests_for" to check coverage.
