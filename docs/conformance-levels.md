# Conformance Levels

PerstaSync assigns one level to each write-then-rename publish site, not to a function name
or every file handle. The level describes synchronization placement for the Linux protocol in
[protocol.md](protocol.md). It does not certify application intent, I/O success, or behavior on
an unsupported platform. The error-handling flag R3 is independent of the level.

## Decision Order

1. Establish that the temporary file and destination share a directory. If that relationship
   cannot be established, use `undetermined`; a known cross-directory move is outside this
   same-directory protocol too. Record the reason rather than calling it conformant.
2. Resolve file synchronization coverage, including writes and required metadata mutations.
   Unresolved interface/type-parameter dispatch, unknown external helpers, or unresolved
   flag-only metadata coverage yield `L1-unknown`.
3. A demonstrated lack of complete pre-rename file coverage is L0. Otherwise, no established
   post-rename parent-directory sync is L1; established file and directory coverage is L2.
4. When directory-sync behavior is itself unresolved, use `L1-unknown`, not L1 or L2. When
   both path and synchronization are unresolved, use `undetermined` and retain both reasons.

The examples below show the relevant function-body fragments. Unless otherwise stated, the
file was created in `parent`, has received its complete data, and its rename stays in that
directory. Application-specific setup and failure cleanup are omitted from these fragments;
the complete sequence is in [protocol.md](protocol.md#complete-linux-sequence).

## L0: Unsynchronized

Full R1 coverage is demonstrably absent before publication.

```go
if _, err := file.Write(data); err != nil {
    return err
}
if err := file.Close(); err != nil {
    return err
}
return os.Rename(temporary, destination)
```

There is no file sync or verified synchronous-write coverage. Closing a file is not a
substitute for [fsync](https://man7.org/linux/man-pages/man2/fsync.2.html).

A file sync followed by an unsynchronized required mode change is also L0, even if the
parent directory is later synced: the final required file metadata is uncovered.

```go
if err := file.Sync(); err != nil {
    return err
}
if err := file.Chmod(mode); err != nil {
    return err
}
if err := file.Close(); err != nil {
    return err
}
return os.Rename(temporary, destination)
```

## L1: Atomic-Only

File coverage is established before the rename, but the parent is not synced afterward.

```go
if err := file.Sync(); err != nil {
    return err
}
if err := file.Close(); err != nil {
    return err
}
return os.Rename(temporary, destination)
```

This omits R2. Linux [fsync](https://man7.org/linux/man-pages/man2/fsync.2.html) explicitly
distinguishes file persistence from directory-entry persistence. Default ext4 does not change
the classification: its [replacement heuristic](https://www.kernel.org/doc/html/latest/admin-guide/ext4.html)
orders data against a later journal commit, rather than making rename return a durability
barrier. The consequences depend on the filesystem, options, crash timing, and file's role.
L1 is neither a universal critical-bug label nor a claim that loss is harmless.

## L2: Crash-Durable Placement

File coverage precedes the rename and parent-directory sync follows it.

```go
if err := file.Sync(); err != nil {
    return err
}
if err := file.Close(); err != nil {
    return err
}
if err := os.Rename(temporary, destination); err != nil {
    return err
}
directory, err := os.Open(parent)
if err != nil {
    return err
}
syncErr := directory.Sync()
closeErr := directory.Close()
return errors.Join(syncErr, closeErr)
```

Assuming no uncovered later file mutation, this places R1 and R2 correctly. It is conditional
on successful synchronization and the storage stack honoring its contract; the level is not
an unconditional assertion that the application can never lose data.

## L1-unknown: Unresolved Synchronization

An interface method named `Sync` does not establish the actual implementation or effects.

```go
type Synchronizer interface {
    Sync() error
}

func Publish(syncer Synchronizer, temporary, destination string) error {
    if err := syncer.Sync(); err != nil {
        return err
    }
    return os.Rename(temporary, destination)
}
```

If the target or its effects are unresolved, classify `L1-unknown`, even though the method
name is suggestive. The same treatment applies to unresolved type-parameter dispatch and
helpers outside the analyzed tree without a verified implementation summary. This label
does not imply that R1 has been established; its historical name is not a lower bound.

## undetermined: Unresolved Path Relationship

```go
func Move(temporary, destination string) error {
    return os.Rename(temporary, destination)
}
```

Without caller evidence relating those paths, same-directory placement cannot be established.
This is `undetermined`. A known move from one directory to another is also outside the studied
protocol and records that reason. Atomic [rename restrictions](https://man7.org/linux/man-pages/man2/rename.2.html)
and cross-filesystem errors must not be replaced with an assumed copy-based equivalent.

## Synchronous Writes

On a verified Linux context, a non-empty `O_SYNC` write may establish file coverage when
it covers the final required state and no later mutation remains uncovered. `O_DSYNC` covers
data integrity, not arbitrary full-file metadata. The distinction comes from
[open(2)](https://man7.org/linux/man-pages/man2/open.2.html), not from the flag's spelling.

An `O_SYNC` open with no write does not establish persistence of empty creation. A mode change
after the last covered write needs another synchronization event. Unresolved `O_DSYNC`
metadata coverage is `L1-unknown`; a known uncovered mutation is L0. A final explicit file
sync after those mutations can establish R1 regardless of whether an open flag was used.

## R3: Unchecked Errors

```go
_ = file.Sync()
_ = file.Close()
if err := os.Rename(temporary, destination); err != nil {
    return err
}
_ = directory.Sync()
return directory.Close()
```

With established same-directory and descriptor identities, this has L2 placement but R3 is
set: sync and file-close failures are discarded. It must not be described as a reliable
durable-write implementation. A returned error must reach an outcome distinct from success;
merely assigning, logging, or overwriting it does not satisfy error handling.

## Accounting

Report all five labels with counts. Exclude `L1-unknown` and `undetermined` from resolved-level
rates and attach their counts; never fold them into compliance or definite violations.
C0 process-lifetime sites are excluded from eligible durability rates but retained in raw
accounting. The separate flagged-site diagnostic share includes C0 as described in
[criticality.md](criticality.md#diagnostic-share). R3 has its own reported rate and is not merged
into the level distribution. No corpus distribution or analyzer accuracy is claimed here.