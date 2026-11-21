# Methodology and Current Scope

PerstaSync is an engineering and empirical-measurement project studying durable file replacement
in explicitly selected Go source trees. The current implementation is a bounded source analyzer,
not a completed ecosystem study. There is no published corpus distribution or measured accuracy.

## Inputs and Loading

The CLI requires a source root, package patterns, and explicit target `GOOS` and `GOARCH`.
It uses `golang.org/x/tools v0.52.0`; transitive dependencies and checksums are recorded in
the module files. Go 1.27.1 is the configured toolchain. Dependency acquisition is separate from
source analysis; the loader disables proxy acquisition and automatic toolchain switching.
This is not an operating-system network sandbox: the source tree, local module replacements,
and Go configuration must be controlled inputs.

Classification accepts paired absolute `-module-cache` and `-build-cache` paths. In that mode,
the loader overrides ambient Go cache/configuration values, disables workspace discovery,
checksum-service access and telemetry, and uses read-only module loading. Missing cached
dependencies fail explicitly. Without the pair, cache locations retain the existing environment
defaults. Cache configuration is distinct from dependency prefetch and does not acquire modules.

`prefetch` requires a materialized root within the sandbox, protected source roots, target platform,
a single module proxy and explicit checksum policy. It prepares alternate module/checksum inputs
without editing the checkout and emits their context metadata without local absolute paths.
Classification may use `-dependency-context` with the same sandbox/protection configuration instead
of direct cache flags. The corpus package strictly decodes that context, reconstructs local paths,
verifies identity and source/prepared hashes, and checks the current Go version before loading.
The requested platform must match; invalid contexts produce no classification document.
`-load-only` stops after packages/SSA/call-graph construction and emits only deterministic package
accounting. It does not invoke protocol classification. This diagnostic is used to qualify prepared
inputs before a pilot run; a successful load is not a conformance or accuracy result.

The same CLI provides `pin` and `materialize` acquisition subcommands. Both require an explicit
sandbox and an OS path list of protected source roots. `pin` records the repository URL, symbolic
default branch, full commit, observation time and Git version. The sandbox must already exist.
`materialize` accepts exactly one
strict pin JSON document and emits its pin plus the tracked non-vendor Go-file count; it does not
emit the local checkout path. Failed requests emit no JSON. These commands acquire source only;
they do not classify it or publish a result.

[`packages.Load`](https://pkg.go.dev/golang.org/x/tools@v0.52.0/go/packages#Load) loads the
explicit initial selection using `LoadSyntax`. The loader rejects package errors rather than
silently returning a partially classified tree. Its current failure policy aborts the request;
per-package corpus coverage accounting is not implemented.

[`ssautil.Packages`](https://pkg.go.dev/golang.org/x/tools@v0.52.0/go/ssa/ssautil#Packages)
constructs dependency package representations but builds source bodies for the initial selection.
The loader builds SSA with generic instantiation enabled and constructs a
[CHA call graph](https://pkg.go.dev/golang.org/x/tools@v0.52.0/go/callgraph/cha).
Loading and SSA construction do not execute the target program's initialization or tests.

CHA conservatively enumerates possible targets under its documented assumptions and can produce
spurious edges. An edge does not prove that a sync executes on a particular path. Missing bodies,
unsupported language forms, and implementation limits remain separate from graph conservatism.
The source classifier follows statically identified callees with selected bodies; it does not
turn possible CHA edges into evidence of executed synchronization. No end-to-end soundness
guarantee follows from using SSA or CHA.

## Current Classification

The classifier recognizes direct `os.Rename` publication candidates and exact `os` file
operations. Ordered call frames propagate handles, literal paths and returned values through
statically identified callees in the selected packages. File synchronization and later mutations
affect the same tracked object across calls; a directory sync must follow the publication it covers.
Repeated observations of one publish coordinate merge conservatively: conflicting coverage is
unresolved, and observed unchecked errors are retained. Candidates in unvisited bodies are still
examined without caller evidence. The result is scoped to the selected source and observed call
contexts, not every possible external caller.

Linux path tracking recognizes literals, `filepath.Dir`, `os.CreateTemp` parent identity and
the tracked descriptor's `Name()`. Creating the temporary file in `filepath.Dir(destination)`
can establish same-directory placement even when the destination is non-literal. A sync of
that same parent handle after rename establishes directory coverage. Known cross-directory
moves and unrelated or unsupported dynamic paths remain `undetermined`. Empty/default
temporary-directory selection is not assumed to equal the destination parent. This tracks
path expressions, not runtime directory stability, symlinks or concurrent namespace changes.
`-disable-paths` withholds derived-parent and file-name proof while retaining literal boundaries,
candidate coordinates and observed R3.

It is not a general path-sensitive classifier. Unsupported calls, reachable multi-block control
flow, recursion and call depth beyond the bounded limit produce unresolved coverage.
Direct return chains preserve sync/close error evidence; comparing
an error is not a general proof of propagation through arbitrary control flow.

Interface/type-parameter dispatch and helpers without selected bodies remain unresolved even
when their names suggest synchronization. Observed discarded interface sync/close errors set
R3 without granting their effects any coverage. When both path and synchronization are unknown,
the record retains both reasons and uses `undetermined` rather than a favorable interpretation.

For Linux inputs, constant `os.OpenFile` flags are interpreted using the loaded target's flag
definitions. A proven non-empty `O_SYNC` write covers the preceding file state. `O_DSYNC` writes
need separate established metadata coverage, such as a preceding explicit sync with no later
metadata change; otherwise that coverage stays `L1-unknown`. Empty creation and zero-length writes
provide no barrier. Unknown write lengths preserve uncertainty when empty and non-empty outcomes
differ. A later mode, ownership, or truncate operation invalidates coverage until covered again.
Dynamic flags and unspecified or non-Linux platform semantics remain unresolved. The
`-disable-open-flags` switch withholds implicit-write proof without changing explicit syncs or R3.

The [level definitions](conformance-levels.md) describe the target classification contract.
Struct allocations have instance-specific field cells. Stores and loads propagate handles and
paths across methods; aggregate copies copy field values without aliasing the field storage.
Pointer-valued fields still refer to the same tracked object. Syncing another instance or field
does not cover the published file, and reassignment does not retain the old handle's coverage.
`-disable-struct-fields` withholds that evidence independently of call-frame tracking.

Closures bind captured cells rather than copying their current contents. Reassignment is visible
when the closure executes. Deferred calls save argument values at registration and run in reverse
order at normal-return `RunDefers` points. A file sync after rename does not repair pre-publication
coverage; a directory sync after rename can establish R2. Unreachable recovery blocks are not
executed as normal paths. Goroutines and panic/recovery behavior are not modeled as ordered proof.
`-disable-alloc-cells` withholds scalar-cell evidence without disabling struct fields.

`-disable-interprocedural` withholds argument/result proof while retaining callee candidates and
observed error handling. General error propagation, unsupported memory forms and external effects
remain limitations. Do not infer application correctness from the bounded output; a false R3 flag
is not proof that every error reaches the caller across unsupported code.

The [criticality rules](criticality.md) have executable evidence-validation and ambiguity-ordering
predicates, but automatic consequence discovery and reader adjudication are not implemented.
File names do not supply criticality evidence.

## Output and Accounting

The CLI emits raw JSON site records with a relative source file, byte offset, level, R3 flag,
and reason. Records are sorted by file and offset. Invalid records, duplicate coordinates,
and unresolved records without reasons are rejected before encoding. Input order is not used
to choose a verdict. A failed load or validation emits no classification document.

These records are not the final corpus-result artifact: repository revision, acquisition evidence,
configuration provenance, adjudication, and exclusions still need to accompany a corpus run.
No raw-site count is automatically converted to a risk estimate.

The rate type separately requires a numerator, nonzero denominator, finite ordered interval,
and explicit exclusion count. Missing values differ from explicit zeros. It validates evidence
shape, not the statistical method that produced an interval; Wilson and seeded-bootstrap
estimation are not yet implemented. The full distribution and sensitivity variants remain required
before reporting measured rates.

The corpus-bookkeeping package validates a manifest's HTTPS URLs, full commit IDs, explicit file
counts, and selection criteria. Its digest is stable under repository-list reordering and changes
when recorded pins or selection evidence change. This does not prove that a commit exists, verify
file counts, acquire a repository, establish selection representativeness, or run a corpus.

Materialization destination validation is side-effect-free. It requires a separate sandbox and
explicit protected source roots, rejects overlapping trees, Git ancestors, symlink components and
pre-existing destinations, and uses a lowercase SHA-256 directory identity. This currently validates
paths only. The internal Git runner uses argument arrays, empty sandbox-owned system/global
configuration, disabled prompts/hooks/LFS filters and explicit transport controls. Local tests
verify isolation from poisoned ambient Git variables and configuration. Full-pin checkout
verification now checks advertised default-branch identity against the recorded commit before
checkout. Tracked root `go.mod` and non-vendor Go-file counts determine mechanical eligibility;
tracked module/Go sources must be regular files without symlink traversal. Tags and submodules
are not implicitly acquired. Failed new destinations are removed, while existing destinations
are rejected rather than reused. Local integration tests cover moved pins, ambient checkout
effects and cleanup. No repository from the corpus frame has been accessed.

The internal dependency prefetch API uses alternate `.mod`/`.sum` files under the validated
sandbox, preserving the checkout's original inputs. It allows one explicit module proxy and
checksum policy, disables ambient authentication and transport overrides, and confines local
module replacements to the checkout. Module/build caches remain sandbox-owned; failed new
contexts are cleaned using Go's scoped module-cache removal. Local proxy metadata uses relative
paths, and context identity distinguishes absent from empty source checksum files. Native/Linux
fixtures verify subsequent offline loading, source preservation, replacements and failure cleanup.
The CLI uses this validated context for offline loading. These are synthetic implementation
checks, not a corpus acquisition, accuracy evaluation or crash-confirmation result.

## Reference Writer and Verification

The reference writer follows [the Linux protocol](protocol.md): same-directory creation, write,
descriptor mode update, file sync, checked close, rename, directory sync, and checked close.
It rejects non-Linux platforms before changing files. The parent directory must already exist
and be durable and stable throughout the operation; concurrent replacement of path components
is outside this reference implementation's contract.

Operation-log tests check ordering and injected failures, including short writes and cleanup
errors. The Linux round-trip test checks contents, permissions, and ordinary directory sync.
Neither kind of test injects power loss. A post-rename error does not roll back publication,
and leftover temporary-name cleanup on failure is not claimed durable.

CI configures the pinned Go version for Linux and Windows, checks module integrity and formatting,
builds, vets, and runs the platform-appropriate tests. Windows does not run the real Linux
directory-sync round trip. Portable mock tests on Windows are not evidence of Windows R2 support.
The workflow definition alone is not evidence that a hosted CI run has passed.

The required source and criticality boundary suite now passes locally on Windows and Linux,
including independent switches for all five source capabilities and preservation of candidate
coordinates and R3. The taxonomy was frozen on 2026-11-04. Hosted CI has not run. These checks
establish the instrument's declared fixture behavior, not its accuracy on real repositories.

Before a measured corpus run, the analyzer revision, input revisions and build context must be
recorded. Accuracy needs a separately adjudicated gold set and paired control comparison.
Separate labeling passes by the same annotator measure within-annotator repeatability, not
independent-rater agreement. Each precision, recall, or subgroup estimate needs its actual
denominator and interval; a total gold-set minimum does not guarantee every metric's precision.

Dynamic confirmation needs independent fault-model controls on Linux-native storage. Qualify
the injected primitive, not merely the tool: unsynced-data loss, namespace loss, mode loss, and
error propagation require distinct adverse and corrected controls. The inspected
[LazyFS rename implementation](https://raw.githubusercontent.com/dsrhaslab/lazyfs/fa7d32ee9710d55a7e529ac3c375f6616563c78b/lazyfs/src/lazyfs.cpp)
calls the backing filesystem, while its
[cache-clear path](https://raw.githubusercontent.com/dsrhaslab/lazyfs/fa7d32ee9710d55a7e529ac3c375f6616563c78b/libs/libpcache/src/cache.cpp)
does not undo that namespace change. Data-cache discard alone is not independent confirmation
of a missing-directory-sync fault. An unsupported primitive or failed control is inconclusive,
not evidence that a target is conformant or that the static result is wrong.