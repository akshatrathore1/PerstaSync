package corpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Pin struct {
	URL           string `json:"url"`
	DefaultBranch string `json:"default_branch"`
	Commit        string `json:"commit"`
	ObservedUTC   string `json:"observed_utc"`
	GitVersion    string `json:"git_version"`
}

var ErrIneligible = errors.New("repository is outside the root Go module frame")

type Checkout struct {
	Pin         Pin    `json:"pin"`
	GoFileCount uint64 `json:"go_file_count"`
	Directory   string `json:"-"`
}

type AcquisitionConfig struct {
	Sandbox   string
	Protected []string
}

func ObservePin(ctx context.Context, config AcquisitionConfig, source string) (pin Pin, resultErr error) {
	root, err := validateMaterializationRoot(config.Sandbox, config.Protected)
	if err != nil {
		return Pin{}, err
	}
	runner, err := newGitRunner(root)
	if err != nil {
		return Pin{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, runner.close()) }()
	return observePin(ctx, runner, root, source)
}

func MaterializePin(ctx context.Context, config AcquisitionConfig, pin Pin) (checkout Checkout, resultErr error) {
	root, err := validateMaterializationRoot(config.Sandbox, config.Protected)
	if err != nil {
		return Checkout{}, err
	}
	if err := pin.validate(false); err != nil {
		return Checkout{}, err
	}
	runner, err := newGitRunner(root)
	if err != nil {
		return Checkout{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, runner.close()) }()
	return materializePin(ctx, runner, root, config.Protected, pin)
}

func ReadPin(reader io.Reader) (Pin, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var pin Pin
	if err := decoder.Decode(&pin); err != nil {
		return Pin{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Pin{}, fmt.Errorf("pin must contain exactly one JSON document")
	}
	if err := pin.validate(false); err != nil {
		return Pin{}, err
	}
	return pin, nil
}

func (pin Pin) validate(allowLocal bool) error {
	if !(allowLocal && filepath.IsAbs(pin.URL)) {
		if err := validateRepositoryURL(pin.URL); err != nil {
			return err
		}
	}
	decoded, err := hex.DecodeString(pin.Commit)
	if err != nil || (len(decoded) != 20 && len(decoded) != 32) || pin.Commit != strings.ToLower(pin.Commit) {
		return fmt.Errorf("pin requires a full lowercase commit")
	}
	if strings.TrimSpace(pin.DefaultBranch) == "" || !strings.HasPrefix(pin.GitVersion, "git version ") || strings.TrimSpace(strings.TrimPrefix(pin.GitVersion, "git version ")) == "" {
		return fmt.Errorf("pin requires default branch and Git version evidence")
	}
	observed, err := time.Parse(time.RFC3339Nano, pin.ObservedUTC)
	if err != nil {
		return fmt.Errorf("pin requires an RFC3339 UTC observation time")
	}
	if _, offset := observed.Zone(); offset != 0 {
		return fmt.Errorf("pin observation time must be UTC")
	}
	return nil
}

func materializePin(ctx context.Context, runner *gitRunner, sandbox string, protected []string, pin Pin) (checkout Checkout, resultErr error) {
	if err := pin.validate(runner.allowLocal); err != nil {
		return Checkout{}, err
	}
	version, err := runner.run(ctx, sandbox, "--version")
	if err != nil {
		return Checkout{}, err
	}
	if strings.TrimSpace(string(version)) != pin.GitVersion {
		return Checkout{}, fmt.Errorf("Git version differs from the pin observation")
	}
	if _, err := runner.run(ctx, sandbox, "check-ref-format", "--branch", pin.DefaultBranch); err != nil {
		return Checkout{}, err
	}
	identity := sha256.Sum256([]byte(pin.URL + "\x00" + pin.Commit))
	destination, err := materializationDestination(sandbox, protected, hex.EncodeToString(identity[:]))
	if err != nil {
		return Checkout{}, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return Checkout{}, fmt.Errorf("reserve checkout destination: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(destination))
		}
	}()
	if _, err := runner.run(ctx, sandbox,
		"clone", "--no-checkout", "--single-branch", "--no-tags", "--no-recurse-submodules", "--no-local", "--depth=1",
		"--branch="+pin.DefaultBranch, "--", pin.URL, destination,
	); err != nil {
		return Checkout{}, err
	}
	head, err := runner.run(ctx, destination, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return Checkout{}, err
	}
	if strings.TrimSpace(string(head)) != pin.Commit {
		return Checkout{}, fmt.Errorf("cloned default-branch head differs from the recorded pin")
	}
	if _, err := runner.run(ctx, destination, "checkout", "--detach", pin.Commit); err != nil {
		return Checkout{}, err
	}
	listing, err := runner.run(ctx, destination, "ls-files", "-z")
	if err != nil {
		return Checkout{}, err
	}
	var count uint64
	rootModule := false
	for _, raw := range bytes.Split(listing, []byte{0}) {
		name := string(raw)
		rootModule = rootModule || name == "go.mod"
		if name != "go.mod" && (!strings.HasSuffix(name, ".go") || vendorPath(name)) {
			continue
		}
		if err := validateTrackedSource(destination, name); err != nil {
			return Checkout{}, err
		}
		if strings.HasSuffix(name, ".go") {
			count++
		}
	}
	if !rootModule {
		return Checkout{}, fmt.Errorf("%w: no tracked root go.mod", ErrIneligible)
	}
	if count == 0 {
		return Checkout{}, fmt.Errorf("%w: no tracked non-vendor Go files", ErrIneligible)
	}
	return Checkout{Pin: pin, GoFileCount: count, Directory: destination}, nil
}

func vendorPath(name string) bool {
	for _, component := range strings.Split(name, "/") {
		if component == "vendor" {
			return true
		}
	}
	return false
}

func validateTrackedSource(directory, name string) error {
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("tracked source path is outside the checkout")
	}
	filename := filepath.Join(directory, filepath.FromSlash(name))
	if _, err := directoryWithoutLinks(filepath.Dir(filename)); err != nil {
		return err
	}
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("tracked module and Go sources must be regular files without symlinks")
	}
	return nil
}

func observePin(ctx context.Context, runner *gitRunner, directory, source string) (Pin, error) {
	if !(runner.allowLocal && filepath.IsAbs(source)) {
		if err := validateRepositoryURL(source); err != nil {
			return Pin{}, err
		}
	}
	output, err := runner.run(ctx, directory, "ls-remote", "--symref", "--", source, "HEAD")
	if err != nil {
		return Pin{}, err
	}
	branch, commit, err := parseRemoteHead(output)
	if err != nil {
		return Pin{}, err
	}
	if _, err := runner.run(ctx, directory, "check-ref-format", "--branch", branch); err != nil {
		return Pin{}, err
	}
	version, err := runner.run(ctx, directory, "--version")
	if err != nil {
		return Pin{}, err
	}
	return Pin{
		URL: source, DefaultBranch: branch, Commit: commit,
		ObservedUTC: time.Now().UTC().Format(time.RFC3339Nano), GitVersion: strings.TrimSpace(string(version)),
	}, nil
}

func parseRemoteHead(output []byte) (string, string, error) {
	var branch, commit string
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			if branch != "" || !strings.HasPrefix(fields[1], "refs/heads/") {
				return "", "", fmt.Errorf("remote HEAD does not identify one default branch")
			}
			branch = strings.TrimPrefix(fields[1], "refs/heads/")
		} else if len(fields) == 2 && fields[1] == "HEAD" {
			decoded, err := hex.DecodeString(fields[0])
			if commit != "" || err != nil || (len(decoded) != 20 && len(decoded) != 32) {
				return "", "", fmt.Errorf("remote HEAD does not identify one full commit")
			}
			commit = strings.ToLower(fields[0])
		}
	}
	if branch == "" || commit == "" {
		return "", "", fmt.Errorf("remote HEAD requires a default branch and full commit")
	}
	return branch, commit, nil
}

func materializationDestination(sandbox string, protected []string, identity string) (string, error) {
	decoded, err := hex.DecodeString(identity)
	if err != nil || len(decoded) != 32 || identity != strings.ToLower(identity) {
		return "", fmt.Errorf("materialization identity must be a lowercase SHA-256 digest")
	}
	root, err := validateMaterializationRoot(sandbox, protected)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, identity)
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("materialization destination already exists")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect materialization destination: %w", err)
	}
	return destination, nil
}

func validateMaterializationRoot(sandbox string, protected []string) (string, error) {
	if sandbox == "" || len(protected) == 0 {
		return "", fmt.Errorf("materialization requires a sandbox and protected source roots")
	}
	root, err := directoryWithoutLinks(sandbox)
	if err != nil {
		return "", err
	}
	if filepath.Dir(root) == root {
		return "", fmt.Errorf("a filesystem root cannot be a materialization sandbox")
	}
	for _, source := range protected {
		if source == "" {
			return "", fmt.Errorf("a protected source root is empty")
		}
		canonical, err := directoryWithoutLinks(source)
		if err != nil {
			return "", err
		}
		if pathWithin(root, canonical) || pathWithin(canonical, root) {
			return "", fmt.Errorf("materialization sandbox overlaps a protected source tree")
		}
	}
	for ancestor := root; ; ancestor = filepath.Dir(ancestor) {
		if _, err := os.Lstat(filepath.Join(ancestor, ".git")); err == nil {
			return "", fmt.Errorf("materialization sandbox is within a Git repository")
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect sandbox repository boundary: %w", err)
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	return root, nil
}

func directoryWithoutLinks(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve materialization path: %w", err)
	}
	for ancestor := absolute; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("materialization paths require directories without symlinks")
			}
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect materialization path: %w", err)
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	return absolute, nil
}

func pathWithin(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
