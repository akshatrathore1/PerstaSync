# Methodology and Current Scope

PerstaSync is an engineering and empirical-measurement project studying durable file replacement
in explicitly selected Go source trees. The current implementation is a bounded analyzer scaffold,
not a completed ecosystem study. There is no published corpus distribution or measured accuracy.

## Inputs and Loading

The CLI requires a source root, package patterns, and explicit target `GOOS` and `GOARCH`.
It uses `golang.org/x/tools v0.52.0`; transitive dependencies and checksums are recorded in
the module files. Go 1.27.1 is the configured toolchain. Dependency acquisition is separate from
source analysis; the loader disables proxy acquisition and automatic toolchain switching.
This is not an operating-system network sandbox: the source tree, local module replacements,
and Go configuration must be controlled inputs.

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
The current source classifier does not traverse this graph to implement inter-procedural analysis.
No end-to-end soundness guarantee follows from using SSA or CHA.

## Current Classification

The current classifier recognizes direct `os.Rename` publication candidates and a narrow set of
exact `os` file operations within a single SSA block. It tracks literal paths and direct handles,
file synchronization, subsequent writes or mode changes, and observed unchecked sync/close errors.
It is not a general path-sensitive classifier. Non-literal paths produce `undetermined`;
unsupported calls and multi-block control flow can produce `L1-unknown`.

Writes through `os.OpenFile` retain unknown implicit synchronization coverage because the current
classifier does not interpret its flags. Without an explicit file sync, these cases are
`L1-unknown`, not definite L0. An explicit sync establishes file coverage, and a later required
mode change invalidates it. This conservative handling does not implement positive flag coverage.

The [level definitions](conformance-levels.md) describe the target classification contract.
Synchronous-open coverage, callee effects, struct-held handles, captured heap cells, and general
error propagation are not implemented capabilities. Do not use the scaffold's output to evaluate
those cases or to infer application correctness. In particular, a false R3 flag is not proof that
every error reaches the caller across unsupported code.

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

Before a measured corpus run, all required capability fixtures must discriminate correct and
incorrect sequences, the taxonomy must be frozen, and the input revisions and build context
must be recorded. Accuracy needs a separately adjudicated gold set and paired control comparison.
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