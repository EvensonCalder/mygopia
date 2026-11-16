# Meta-lang AI Workflow & Annotation Specification (mygopia)

This project uses `mela` to maintain structured design intent (`Intent`) and deterministic local dependency hashes (`Hash`). Follow this specification whenever reading, authoring, or modifying code.

## 1. Division of Responsibilities

1. **Scope**: Declare a `Scope` with meaningful `Intent` on key modules, types, functions, implementation regions, and high-level specs.
   - Language adapters are enabled: prefer concise header `@scope <id>` directly above declarations (functions, structs, classes, modules), or `@scope.begin <id> ... @scope.end` for arbitrary code regions.
   - **Scope Hashes are computed automatically via `mela hash --write`**. Never compute SHA-256 hashes manually.
2. **Reference**: Declare `@ref` whenever calling or depending on another `Scope` or tracked external package (and `@implements` when realizing a higher-level spec/design scope).
   - **Reference Hashes MUST be explicitly written and updated by you (the AI)**. `mela` never silently overwrites Reference Hashes.
   - Before writing or updating a Reference Hash, confirm you understand the target's contract and any upstream changes. If the target is already in your context window, update the Reference directly without re-reading files.
   - Partial adoption is supported: paths in `.melaignore` (or `[project] exclude`) and items marked `@scope.ignore` / `@scope.ignore-file` are intentionally untracked.

## 2. Annotation Syntax

### 2.1 Declaring a Scope
```python
# @scope auth.verify_token
# @hash sha256:<generated>
# @implements spec.auth sha256:<spec_hash>
# @ref default pyjwt@2.9.0
# Verify bearer token HMAC-SHA256 signature and `exp` claim; reject malformed or expired tokens.
def verify_token(token: str, secret: str) -> str:
    ...
```
- Explicit wrapped block (any language): `@scope.begin <id> [kind=<k>] ... @scope.end [<id>]`.
- High-level Markdown specs (`DESIGN.md`, `SPEC.md`): wrap sections in `<!-- @scope.begin spec.auth kind=spec --> ... <!-- @scope.end -->`.
- Place `@implements` on the top-level realizing class/module/entry function rather than repeating it on every child method inside the same class.
- **Coexisting with JSDoc / Go doc / `rustdoc`**: When a declaration already has a doc comment (`/** ... */`, `///`, or Go `// Name ...`), place the `@scope` header block **above** the doc comment (separated by a blank line in Go so `gofmt` and doc tooling stay untouched; `mela` still binds across doc comments and blank lines to the declaration below).

### 2.2 Declaring References & Hash-Cascade Invariance
- **Key Hash Rule**: Adding or removing a `@ref` line (or editing its `: <reason>`) changes that Scope's `ScopeHash`, whereas **updating only the hash token on an existing `@ref` / `@implements` line (`pending` -> `sha256:...`, or `old` -> `new`) NEVER changes the caller's `ScopeHash`**. Always declare all known `@ref` lines up front (using `pending` if a target hash is not computed yet) before running `mela hash --write`.
- You can place `@ref` either **grouped in the Scope header** (ideal when a function has several dependencies and you want an uncluttered body) or **at the call site**:
- **Single-line with rationale**:
  ```python
  # @ref auth.verify_token sha256:<target_hash> : Authenticate caller before reading private profile
  claims = verify_token(token)
  ```
- **Multi-line block or wrapped `@ref.begin ... @ref.end`**:
  ```python
  # @ref auth.verify_token sha256:<target_hash>
  # Authenticate caller before reading private profile.
  claims = verify_token(token)
  ```
- **`default` shorthand** (when call purpose is self-evident; hash is still mandatory):
  ```python
  # @ref default storage.load_profile sha256:<target_hash>
  ```
- **External dependency**: `# @ref default pyjwt@2.9.0`
- **Suppressing intentional call hints**: `# @ref.ignore <symbol_or_scope> [: <reason>]` silences heuristic `UnreferencedCall` warnings without adding artificial edges to the architecture graph (`UnreferencedCall` stays a non-blocking warning by default even under `--strict`).

## 3. Intent Quality Guidelines

- Write all Intent comments in concise, clear **English**.
- **High signal, zero filler**: Explain *what* responsibility the unit owns, *why* a design/abstraction/algorithm was chosen, and non-obvious invariants, edge cases, security properties, or performance trade-offs.
- **Never restate syntax**: Do not repeat facts obvious from the function signature or code lines. Prefer `@ref default` when a call's purpose is obvious.
### Project Focus Areas
- Correctness invariants and edge-case assumptions
- Architectural rationale and abstraction boundaries
- Security, resource, and performance constraints

## 4. Standard CLI Workflow

1. **Explore Surgically (Save Tokens)**:
   - Run `mela graph -i` (topology + `file:start-end` + 1-line Intent/reason), `mela context` (architecture overview), or `mela search "termA|termB"` (case-insensitive regex, `|` alternation, `camelCase`<->`snake_case` matching, and inner AST symbol spans inside `@scope.file` / class Scopes) BEFORE broad `grep` or full-file reads.
   - `mela graph`, `mela context`, and `mela search` emit workspace `Root:` plus exact `file:start-end` spans: read only those targeted line ranges (`StartLine`..`EndLine`) instead of loading entire large files.
   - Drill into callers/impact with `mela context <scope> --reverse` or `mela inspect <s1> [<s2>...]`.
2. **Create Code**: Write implementation + Scope Intent + `@ref` -> run `mela hash --write` -> fill in any newly created target hashes -> run `mela check`.
3. **Modify Existing Code (Zero-Waste Sync)**:
   - Edit code, update Scope Intent if behavior or invariants changed, and include any newly added `@ref` lines up front (use `pending` if a new target hash does not exist yet).
   - Run your language formatter / compiler / test suite FIRST (e.g., `gofmt`, `tsc`, `cargo check`, unit tests) so code edits and formatting are completely settled before syncing hashes.
   - Run `mela hash --write` once: `mela` refreshes modified Scope hashes and prints both any scope-local issues (`EmptyScopeIntent`, `UnreferencedCall`) and affected caller `line N:` `@ref` snippets. If `mela hash --write` reports scope-local issues on a Scope, fix those first and re-run `mela hash --write` before finalizing caller `@ref` hashes; otherwise update each stale `@ref` hash directly in one pass and run `mela check` once at the end (avoid chaining `mela hash --write && mela check --strict` before updating stale `@ref`s).
