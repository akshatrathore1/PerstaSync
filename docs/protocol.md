# Durable File Replacement

Writing a new file and renaming it over an existing destination separates construction from
publication. On Linux, a successful replacing [rename](https://man7.org/linux/man-pages/man2/rename.2.html)
atomically changes the destination name: a concurrent reader does not encounter a gap caused
by that replacement. A reader with an already-open descriptor can continue reading the old file.
This is a namespace guarantee, not a promise that the new contents survive power loss.

PerstaSync studies the same-directory, write-then-rename protocol on Linux. The temporary file
and destination must be in the same directory, that directory must already exist, and its
identity must not change during the operation. Cross-directory moves, directory exchanges,
and storage that does not honor synchronization require separate reasoning.

## R1: Persist the File Before Publication

The final contents and required file metadata must be covered by synchronization before the
rename. Linux [fsync](https://man7.org/linux/man-pages/man2/fsync.2.html) flushes modified file
data and associated metadata and waits for the device to report completion. In Go,
[`File.Sync`](https://go.dev/src/os/file_posix.go) delegates to that platform operation.

```go
if _, err := file.Write(data); err != nil {
    return err
}
if err := file.Chmod(mode); err != nil {
    return err
}
if err := file.Sync(); err != nil {
    return err
}
```

Set the intended mode on the open descriptor before syncing it. A mode change after the final
sync is a new metadata mutation; the earlier sync cannot cover a change that has not happened.
Likewise, another write after the sync requires synchronization again before publication.

Synchronous open flags are not interchangeable with a completed file sync in every sequence.
Linux [open](https://man7.org/linux/man-pages/man2/open.2.html) specifies that `O_SYNC` writes
provide file-integrity completion, while `O_DSYNC` writes provide data-integrity completion:
data plus metadata necessary to retrieve it, such as length. Neither flag makes the open call
a persistence barrier for an empty creation or an arbitrary later metadata change. User-space
buffers must also be flushed to the descriptor. Credit a flag only for the operations it covers.

## R2: Persist the Parent After Publication

The rename changes directory entries. Syncing the file does not necessarily persist its name;
the Linux [fsync specification](https://man7.org/linux/man-pages/man2/fsync.2.html) explicitly
requires a separate directory-descriptor sync. Merely opening the directory does not do this.

```go
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

Sync the actual parent directory after the rename. A second sync of the file, or a sync of the
parent before the rename, is not evidence that the rename itself was made durable.

## R3: Preserve Errors

A failed sync must not be reported as successful publication. The
[fsync error contract](https://man7.org/linux/man-pages/man2/fsync.2.html) includes write-back
failures and exhausted storage; a successful write alone does not establish persistence.
Check the file's close error as well. Preserve the original failure instead of replacing it
with a later success or a cleanup result.

```go
syncErr := file.Sync()
closeErr := file.Close()
if err := errors.Join(syncErr, closeErr); err != nil {
    return err
}
return os.Rename(temporary, destination)
```

That fragment demonstrates error preservation, not the whole protocol: R2 must still follow.
After a successful rename, a directory-sync failure means that the new file is already visible
but persistence was not established. Returning an error does not roll the replacement back.

## Complete Linux Sequence

This reference sequence assumes a stable, already-durable parent directory and a filesystem
and device that honor file and directory synchronization. It is an illustration of the protocol,
not a general-purpose replacement library or a Windows durability guarantee.

```go
package example

import (
    "errors"
    "os"
    "path/filepath"
)

func Replace(destination string, data []byte, mode os.FileMode) (result error) {
    parent := filepath.Dir(destination)
    file, err := os.CreateTemp(parent, ".replace-*")
    if err != nil {
        return err
    }
    temporary := file.Name()
    closed := false
    defer func() {
        if !closed {
            result = errors.Join(result, file.Close())
        }
        removeErr := os.Remove(temporary)
        if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
            result = errors.Join(result, removeErr)
        }
    }()

    if _, err := file.Write(data); err != nil {
        return err
    }
    if err := file.Chmod(mode); err != nil {
        return err
    }
    if err := file.Sync(); err != nil {
        return err
    }
    closed = true
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
}
```

The order is create in the destination's directory, write, set metadata, sync the file, close
with error checking, rename, sync the parent, and close the parent with error checking. Cleanup
does not suppress an earlier error. The example does not promise durable removal of leftover
temporary names on failure, or safe operation against concurrent changes to the parent path.

## What ext4 Mitigates

The kernel's [ext4 documentation](https://www.kernel.org/doc/html/latest/admin-guide/ext4.html)
describes `auto_da_alloc`, enabled by default. It recognizes replacement-via-rename and
replacement-via-truncate patterns involving existing files. In `data=ordered` mode, it forces
delayed-allocation blocks into the ordering needed to write the new data before the rename's
next journal commit. This mitigates the documented zero-length replacement failure.

It does not turn the rename's return into a persistence barrier. The same documentation gives
`commit=nrsec` a default of five seconds and warns about loss of recent metadata changes on
power failure. Publishing a previously nonexistent name is not the documented replacement
trigger. `noauto_da_alloc` disables the mitigation, and `data=writeback` does not preserve the
same data ordering. These are configuration conditions, not a measured failure probability.

Inference: this documented ext4 mechanism is not a portable substitute for R1 or R2. Its
existence establishes no equivalent guarantee for another filesystem. A conformant writer
uses the explicit protocol rather than assuming a heuristic covers its sequence.

## Why Round-Trip Tests Miss This

A write followed by a read can observe cached contents and the currently visible name. It
does not distinguish a durable replacement from an unsynchronized one. A crash test must
actually model loss of volatile state; terminating only the application leaves the operating
system and its cache alive. Passing ordinary content tests is not evidence of crash durability.

The distinction has operational consequences. Kafka's
[snapshot incident](https://issues.apache.org/jira/browse/KAFKA-15312) reports zero-length
KRaft snapshots on ext4 and requires flushing before the atomic move. OpenShift's
[certificate and manifest fix](https://github.com/openshift/library-go/pull/2176) addresses
unflushed files and directory exchanges, not just this single-file pattern. Jepsen's
[NATS analysis](https://jepsen.io/analyses/nats-2.12.1) reports acknowledged-write loss under
simulated OS crashes with deferred flushing. That last mechanism is not a missing-directory-sync
measurement. These are the sources' reported incidents, not experiments reproduced here.

## Portability

Go's [`Rename` documentation](https://go.dev/src/os/file.go) explicitly does not promise atomic
rename on non-Unix platforms, even within one directory. Windows
[`FlushFileBuffers`](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers)
has its own handle requirements; the ordinary read-only directory-open sequence is not a
verified Windows equivalent of Linux directory sync. Do not discard its failure and claim R2.
Windows content tests cannot validate this document's Linux crash-durability contract.