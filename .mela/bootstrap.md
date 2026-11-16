# Meta-lang Bootstrap & Incremental Onboarding Guide (mygopia)

Use this guide when introducing `mela` into an **existing (brownfield) repository** or expanding `mela` coverage incrementally—without needing to annotate every file at once.

## Core Principle: Selective, Leaf-First Adoption

**Do NOT blindly annotate every trivial helper, test, migration, or generated file.** Partial adoption is a first-class workflow in `mela`: unannotated legacy modules coexist cleanly alongside governed Scopes.

## Step-by-Step Migration Workflow

### Step 1: Initialize & Define Ignore Boundaries
1. If `.mela/` is not initialized yet, initialize with ignore patterns in one command, e.g.:
   `mela init --ignore tests/ --ignore docs/ --ignore vendor/`
2. Run `mela status --untracked` to view scanned files and candidate public symbols across the repository.
3. Refine `.melaignore` (gitignore syntax) if additional legacy/generated directories exist, or use `# @scope.ignore-file` / `# @scope.ignore` for single-file or single-function exemptions. Keep `[scope] require_fn_scope = "none"` during incremental adoption.

### Step 2: Anchor High-Level Architecture First (Top-Down)
1. Read the core entry points and domain modules to understand the *actual* architecture, invariants, and boundaries.
2. Create or annotate a concise `DESIGN.md` (using `<!-- @scope.begin spec.<area> kind=spec --> ... <!-- @scope.end -->`) or module-level `@scope.file` headers capturing key architectural contracts and invariants.
3. Run `mela hash --write` so high-level Spec hashes are generated immediately.

### Step 3: Batch Annotations with Zero Hash Churn
Because **adding a new `@ref` line changes a Scope's `ScopeHash`, whereas replacing `pending` with `sha256:...` on an existing `@ref` line NEVER changes the caller's `ScopeHash`**, follow either of these zero-churn patterns:
1. **Single-Pass `pending` Batching (Fastest for Multi-File / Cyclic Code)**:
   - Write all `@scope`, `@hash pending`, `@implements spec.<area> <spec_hash>`, `Intent`, and every cross-scope `@ref [default] <target> pending` line up front across your target files.
   - Place `@scope` blocks **above** any existing JSDoc (`/** ... */`), `rustdoc` (`///`), or Go doc comments (leave a blank line before Go doc comments so `gofmt` never rewrites them; `mela` binds across blank lines and doc comments automatically).
   - Run `mela hash --write` once: every Scope immediately gets its **final** `ScopeHash`, and `mela` prints every `pending` reference with its exact `line N:` and `target is now sha256:...` hash so you can fill them in one pass without re-hashing.
2. **Leaf-First Order**:
   - Alternatively, annotate leaf/utility Scopes (including all of *their* outgoing `@ref` lines) -> run `mela hash --write` -> annotate upper callers with those final hashes -> run `mela hash --write`.

### Step 4: Verify & Handoff
1. Run `mela check --strict` and resolve any missing/mismatched hashes (heuristic `UnreferencedCall` hints remain non-blocking warnings unless configured otherwise; add `@ref` for true cross-scope dependencies or `@ref.ignore <sym_or_scope> : <reason>` for intentional exemptions—if you add a `@ref` line, run `mela hash --write` once more and update any reported caller `@ref`).
2. Run `mela context --depth 2` and `mela graph -i` to review the resulting architecture graph for clarity and accuracy.
3. Once initial onboarding is complete, follow `.mela/prompt.md` (`mela prompt`) for ongoing development.
