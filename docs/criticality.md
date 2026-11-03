# State Criticality

Criticality asks what would be lost if a published file did not survive a crash. It does not
follow from a name such as `cache`, `state.json`, or `manifest`. Inspect the code that reads
the file after restart and the resulting system behavior. Every publish site receives one
class; unresolved consequence boundaries are explicitly flagged.

The classes and weights below are the study's declared ordinal taxonomy, not a measured
cost scale. The examples are constructed classification examples, not findings about an
unexamined repository. Protocol conformance is a separate axis in
[conformance-levels.md](conformance-levels.md).

## C1: Coordination and Committed State

Loss can silently roll back or corrupt acknowledged data, or break cluster membership.
Weight: 4. A justification must identify the restart reader and connect loss to that consequence.

- Clear example: a persisted consensus log position that recovery uses to decide which
  acknowledged records remain committed.
- Boundary example: a file named `cache` is the only durable map of acknowledged object IDs.
  Recovery trusts it to reconstruct committed data, so it is C1, not regenerable C4.

A rebuildable index with an independent authoritative data source is not automatically C1.
Show the recovery consequence instead of inferring it from the word `index`.

## C2: Credentials and Trust Material

Loss prevents a component from authenticating or starting because required trust material is
unavailable. Weight: 3. The justification must cite the reader and the authentication/start path.

- Clear example: the only private key used by a service's authenticated startup path, with no
  automatic regeneration or replacement mechanism.
- Boundary example: a certificate file with a `cache` suffix is C2 if startup requires its
  exact credential and missing it prevents authentication. The suffix does not make it C4.

A certificate that is safely and automatically reissued before it is needed may instead be
regenerable. Record that mechanism; credentials do not receive C2 solely because of their type.
The [OpenShift incident](https://github.com/openshift/library-go/pull/2176) is a primary example
of the reported consequence of losing startup certificates, not a classification of every
certificate writer.

## C3: Durable Metadata and Configuration

Loss requires manual intervention or silently restores stale metadata/configuration without
the established C1 or C2 consequence. Weight: 2.

- Clear example: persisted routing configuration loaded on restart; loss restores stale rules
  and requires an operator to repair them.
- Boundary example: an index can be regenerated, but only by a manual repair procedure. It is
  C3 rather than C4; if losing it instead corrupts acknowledged data, the consequence is C1.

Checkpoints and snapshots need the same evidence. Kafka's
[snapshot incident](https://issues.apache.org/jira/browse/KAFKA-15312) illustrates why recovery
behavior matters; the name `snapshot` alone does not select a class.

## C4: Regenerable or Diagnostic State

Loss is self-correcting or the file is automatically rebuilt without losing authoritative
state. Weight: 1. Logs, caches, and process identifiers are examples only when their actual
use satisfies this boundary.

- Clear example: a derived lookup cache rebuilt automatically from a separate durable source
  before the application serves requests.
- Boundary example: a saved diagnostic dump read on later runs, but not needed for recovery,
  whose loss affects diagnostics rather than committed data or startup credentials.

A log that is the sole authoritative record of acknowledged operations is not diagnostic C4.
Likewise, an apparently derived cache requiring manual repair belongs at least at the C3 boundary.

## C0: Excluded by Construction

The destination is explicitly process-lifetime-scoped and never read across a process lifetime.
C0 is an eligibility exclusion, not the least severe member of the weighted scale.

- Clear example: temporary sort output consumed within one invocation and never reused after exit.
- Boundary example: a file named `state.json` is recreated before every use and is never read
  from a previous run. Evidence of that lifecycle establishes C0, not the alarming name.

A temporary directory name alone is insufficient. Recovery code may intentionally reuse files
there, and a later process may read them. Establish the lifetime boundary before excluding a site;
do not select C0 merely because its durable consequence is uncertain.

## Ambiguity

When evidence supports more than one eligible class, use the supported candidate with the
lowest weight as the primary assignment and record the highest-weight supported candidate as
the pessimistic alternative. Set the ambiguity flag and retain the evidence for both.

For a C2/C4 boundary, primary is C4 and sensitivity is C2. Do not replace the supported C2
alternative with C3 merely to promote one step. Recompute reported results with every ambiguous
site assigned its recorded pessimistic alternative. If the conclusion changes direction, report
the conclusion as unresolved. C0 requires lifecycle evidence and is not selected by this rule.

## Weighting and Exclusions

For resolved-level, eligible C1-C4 sites, summarize protocol non-conformance with fixed weights:

$$
R = \frac{\sum_{s \in E} w_s\,\mathbf{1}[L_s \in \{L0,L1\}]}{\sum_{s \in E} w_s},
\qquad w(C1)=4,\ w(C2)=3,\ w(C3)=2,\ w(C4)=1.
$$

Here $E$ is the reported set of eligible sites with resolved levels. Attach the C0 and
unresolved-level exclusion counts to the rate. An empty denominator produces no estimate,
not a zero-risk result. Report a seeded bootstrap interval and the primary and pessimistic
variants. Weights encode ordering only: C1 loss is not claimed to be four times worse than C4.

Site resampling within repositories preserves the observed repository composition. It does not
remove within-repository dependence, cloned-code correlation, or the influence of large
repositories. State these limitations rather than treating the interval as a probability-sample
inference about all Go software.

Always publish the unweighted level-by-criticality cross-tabulation alongside the summary,
including raw excluded and unresolved counts. A single weighted figure cannot replace the
distribution. No measured risk rate is asserted by this design document.

## Diagnostic Share

The separate H3 diagnostic asks how many definite protocol flags concern excluded or regenerable
state. Its numerator counts definite L0/L1 flags in C0 or C4; its denominator counts all definite
L0/L1 flags across C0-C4, once per publish site. Unresolved levels are excluded and counted
separately. An R3-only flag is not included in that level-based flagged set.

Report this unweighted diagnostic share with its numerator, denominator, Wilson interval,
and ambiguity sensitivity. It deliberately includes C0 because the question concerns flags
rather than eligible durable exposure. It must not be confused with an eligible conformance,
R3, or weighted-risk rate, all of which exclude C0. An optional control-method diagnostic uses
that method's own flagged set and is identified separately.