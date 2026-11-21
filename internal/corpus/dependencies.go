package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

type PrefetchConfig struct {
	Directory string
	Sandbox   string
	Protected []string
	GOOS      string
	GOARCH    string
	Proxy     string
	SumDB     string
	Resume    bool
}

type Dependencies struct {
	ID               string `json:"id"`
	PreparedUTC      string `json:"prepared_utc"`
	GoVersion        string `json:"go_version"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
	Proxy            string `json:"proxy"`
	SumDB            string `json:"sumdb"`
	SourceModHash    string `json:"source_mod_sha256"`
	SourceSumHash    string `json:"source_sum_sha256"`
	SourceSumPresent bool   `json:"source_sum_present"`
	ModuleHash       string `json:"module_sha256"`
	SumHash          string `json:"sum_sha256"`
	ModuleFile       string `json:"-"`
	ModuleCache      string `json:"-"`
	BuildCache       string `json:"-"`
}

func ReadDependencies(reader io.Reader) (Dependencies, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var record struct {
		Dependencies
		SourceSumPresent *bool `json:"source_sum_present"`
	}
	if err := decoder.Decode(&record); err != nil {
		return Dependencies{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Dependencies{}, fmt.Errorf("dependency context must contain exactly one JSON document")
	}
	if record.SourceSumPresent == nil {
		return Dependencies{}, fmt.Errorf("dependency context requires source checksum-file presence")
	}
	dependencies := record.Dependencies
	dependencies.SourceSumPresent = *record.SourceSumPresent
	if err := dependencies.validateMetadata(); err != nil {
		return Dependencies{}, err
	}
	return dependencies, nil
}

func ResolveDependencies(ctx context.Context, config AcquisitionConfig, directory string, dependencies Dependencies) (Dependencies, error) {
	if err := dependencies.validateMetadata(); err != nil {
		return Dependencies{}, err
	}
	root, err := validateMaterializationRoot(config.Sandbox, config.Protected)
	if err != nil {
		return Dependencies{}, err
	}
	source, err := directoryWithoutLinks(directory)
	if err != nil || !pathWithin(source, root) || source == root {
		return Dependencies{}, fmt.Errorf("dependency source must be a materialized checkout within the sandbox")
	}
	moduleInput, err := regularFile(filepath.Join(source, "go.mod"), true)
	if err != nil {
		return Dependencies{}, err
	}
	sumInput, err := regularFile(filepath.Join(source, "go.sum"), false)
	if err != nil {
		return Dependencies{}, err
	}
	if hexHash(moduleInput) != dependencies.SourceModHash || hexHash(sumInput) != dependencies.SourceSumHash || (sumInput != nil) != dependencies.SourceSumPresent {
		return Dependencies{}, fmt.Errorf("source module inputs differ from the prepared context")
	}
	contextRoot, err := existingDirectoryWithoutLinks(filepath.Join(root, ".dependencies", dependencies.ID))
	if err != nil {
		return Dependencies{}, err
	}
	moduleFile := filepath.Join(contextRoot, "analysis.mod")
	moduleData, err := regularFile(moduleFile, true)
	if err != nil || hexHash(moduleData) != dependencies.ModuleHash {
		return Dependencies{}, fmt.Errorf("prepared module input differs from its metadata")
	}
	sumFile := strings.TrimSuffix(moduleFile, ".mod") + ".sum"
	sumData, err := regularFile(sumFile, false)
	if err != nil || hexHash(sumData) != dependencies.SumHash {
		return Dependencies{}, fmt.Errorf("prepared checksum input differs from its metadata")
	}
	moduleCache, err := existingDirectoryWithoutLinks(filepath.Join(contextRoot, "module-cache"))
	if err != nil {
		return Dependencies{}, err
	}
	buildCache, err := existingDirectoryWithoutLinks(filepath.Join(contextRoot, "build-cache"))
	if err != nil {
		return Dependencies{}, err
	}
	control, err := existingDirectoryWithoutLinks(filepath.Join(contextRoot, "control"))
	if err != nil {
		return Dependencies{}, err
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return Dependencies{}, err
	}
	versionConfig := PrefetchConfig{GOOS: dependencies.GOOS, GOARCH: dependencies.GOARCH, Proxy: "off", SumDB: "off"}
	version, err := runGo(ctx, goExecutable, source, isolatedGoEnvironment(versionConfig, moduleCache, buildCache, control), "version")
	if err != nil || strings.TrimSpace(string(version)) != dependencies.GoVersion {
		return Dependencies{}, fmt.Errorf("current Go version differs from the prepared context")
	}
	dependencies.ModuleFile = moduleFile
	dependencies.ModuleCache = moduleCache
	dependencies.BuildCache = buildCache
	return dependencies, nil
}

func (dependencies Dependencies) validateMetadata() error {
	for _, field := range []struct{ name, value string }{
		{"id", dependencies.ID}, {"source module hash", dependencies.SourceModHash},
		{"source checksum hash", dependencies.SourceSumHash}, {"module hash", dependencies.ModuleHash},
		{"checksum hash", dependencies.SumHash},
	} {
		decoded, err := hex.DecodeString(field.value)
		if err != nil || len(decoded) != 32 || field.value != strings.ToLower(field.value) {
			return fmt.Errorf("dependency context %s must be a lowercase SHA-256 digest", field.name)
		}
	}
	if dependencies.GOOS == "" || dependencies.GOARCH == "" || !strings.HasPrefix(dependencies.GoVersion, "go version ") {
		return fmt.Errorf("dependency context requires Go version and build platform")
	}
	prepared, err := time.Parse(time.RFC3339Nano, dependencies.PreparedUTC)
	if err != nil {
		return fmt.Errorf("dependency context requires an RFC3339 UTC preparation time")
	}
	if _, offset := prepared.Zone(); offset != 0 {
		return fmt.Errorf("dependency context preparation time must be UTC")
	}
	if dependencies.SumDB != "off" && dependencies.SumDB != "sum.golang.org" {
		return fmt.Errorf("dependency context has an unsupported checksum policy")
	}
	if err := validateRecordedProxy(dependencies.Proxy); err != nil {
		return err
	}
	if dependencyID(dependencies) != dependencies.ID {
		return fmt.Errorf("dependency context identity does not match its inputs")
	}
	return nil
}

func Prefetch(ctx context.Context, config PrefetchConfig) (dependencies Dependencies, resultErr error) {
	root, err := validateMaterializationRoot(config.Sandbox, config.Protected)
	if err != nil {
		return Dependencies{}, err
	}
	directory, err := directoryWithoutLinks(config.Directory)
	if err != nil {
		return Dependencies{}, err
	}
	if !pathWithin(directory, root) || directory == root {
		return Dependencies{}, fmt.Errorf("dependency source must be a materialized checkout within the sandbox")
	}
	if config.GOOS == "" || config.GOARCH == "" || (config.SumDB != "off" && config.SumDB != "sum.golang.org") {
		return Dependencies{}, fmt.Errorf("dependency acquisition requires GOOS, GOARCH and checksum database policy")
	}
	if err := validateModuleProxy(config.Proxy, root); err != nil {
		return Dependencies{}, err
	}
	inputsRoot, err := validateMaterializationRoot(filepath.Join(root, ".dependencies"), append(append([]string(nil), config.Protected...), directory))
	if err != nil {
		return Dependencies{}, err
	}
	proxyLocation, err := url.Parse(config.Proxy)
	if err != nil {
		return Dependencies{}, err
	}
	recordedProxy := config.Proxy
	if proxyLocation.Scheme == "file" {
		relative, err := filepath.Rel(root, moduleProxyPath(proxyLocation.Path))
		if err != nil {
			return Dependencies{}, err
		}
		recordedProxy = "file:" + filepath.ToSlash(relative)
	}
	moduleInput, err := regularFile(filepath.Join(directory, "go.mod"), true)
	if err != nil {
		return Dependencies{}, err
	}
	sumInput, err := regularFile(filepath.Join(directory, "go.sum"), false)
	if err != nil {
		return Dependencies{}, err
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return Dependencies{}, fmt.Errorf("Go is required for dependency acquisition: %w", err)
	}
	if err := validateLocalReplacements(directory, root, moduleInput); err != nil {
		return Dependencies{}, err
	}
	probe, err := os.MkdirTemp(root, ".go-controls-")
	if err != nil {
		return Dependencies{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(probe)) }()
	for _, path := range []string{filepath.Join(probe, "module-cache"), filepath.Join(probe, "build-cache"), filepath.Join(probe, "control")} {
		if err := os.Mkdir(path, 0700); err != nil {
			return Dependencies{}, err
		}
	}
	probeControl := filepath.Join(probe, "control")
	if err := os.WriteFile(filepath.Join(probeControl, "empty-netrc"), nil, 0600); err != nil {
		return Dependencies{}, err
	}
	version, err := runGo(ctx, goExecutable, directory,
		isolatedGoEnvironment(config, filepath.Join(probe, "module-cache"), filepath.Join(probe, "build-cache"), probeControl), "version")
	if err != nil {
		return Dependencies{}, err
	}
	goVersion := strings.TrimSpace(string(version))
	identityInput := Dependencies{
		GoVersion: goVersion, GOOS: config.GOOS, GOARCH: config.GOARCH, Proxy: recordedProxy, SumDB: config.SumDB,
		SourceModHash: hexHash(moduleInput), SourceSumHash: hexHash(sumInput), SourceSumPresent: sumInput != nil,
	}
	id := dependencyID(identityInput)
	preparedRoot := filepath.Join(inputsRoot, id)
	resuming := false
	if _, err := os.Lstat(preparedRoot); err == nil {
		if !config.Resume {
			return Dependencies{}, fmt.Errorf("dependency context already exists")
		}
		if _, err := existingDirectoryWithoutLinks(preparedRoot); err != nil {
			return Dependencies{}, err
		}
		resuming = true
	} else if !os.IsNotExist(err) {
		return Dependencies{}, fmt.Errorf("inspect dependency context: %w", err)
	}
	if config.Resume && !resuming {
		return Dependencies{}, fmt.Errorf("dependency resumption requires an existing matching context")
	}
	if !resuming {
		if err := os.MkdirAll(preparedRoot, 0700); err != nil {
			return Dependencies{}, fmt.Errorf("create dependency context: %w", err)
		}
	}
	moduleFile := filepath.Join(preparedRoot, "analysis.mod")
	sumFile := strings.TrimSuffix(moduleFile, ".mod") + ".sum"
	var previousSum []byte
	if resuming {
		preparedModule, err := regularFile(moduleFile, true)
		if err != nil || hexHash(preparedModule) != hexHash(moduleInput) {
			return Dependencies{}, fmt.Errorf("existing module input differs from the pinned source")
		}
		previousSum, err = regularFile(sumFile, false)
		if err != nil {
			return Dependencies{}, err
		}
	}
	defer func() {
		if resultErr != nil {
			if resuming {
				resultErr = errors.Join(resultErr, writeDependencyInput(moduleFile, moduleInput))
				if previousSum != nil {
					resultErr = errors.Join(resultErr, writeDependencyInput(sumFile, previousSum))
				} else if err := os.Remove(sumFile); err != nil && !os.IsNotExist(err) {
					resultErr = errors.Join(resultErr, err)
				}
				return
			}
			cache := filepath.Join(preparedRoot, "module-cache")
			if _, err := os.Stat(cache); err == nil {
				cleanupConfig := config
				cleanupConfig.Proxy, cleanupConfig.SumDB = "off", "off"
				output, cleanupErr := runGo(context.Background(), goExecutable, root,
					isolatedGoEnvironment(cleanupConfig, cache, filepath.Join(preparedRoot, "build-cache"), filepath.Join(preparedRoot, "control")),
					"clean", "-modcache")
				if cleanupErr != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("clean new module cache: %w: %s", cleanupErr, strings.TrimSpace(string(output))))
				}
			}
			resultErr = errors.Join(resultErr, os.RemoveAll(preparedRoot))
			_ = os.Remove(filepath.Dir(preparedRoot))
		}
	}()
	if err := writeDependencyInput(moduleFile, moduleInput); err != nil {
		return Dependencies{}, err
	}
	if len(sumInput) != 0 {
		if err := writeDependencyInput(sumFile, sumInput); err != nil {
			return Dependencies{}, err
		}
	} else if resuming {
		if err := os.Remove(sumFile); err != nil && !os.IsNotExist(err) {
			return Dependencies{}, err
		}
	}
	moduleCache := filepath.Join(preparedRoot, "module-cache")
	buildCache := filepath.Join(preparedRoot, "build-cache")
	control := filepath.Join(preparedRoot, "control")
	for _, path := range []string{moduleCache, buildCache, control} {
		if resuming {
			if _, err := existingDirectoryWithoutLinks(path); err != nil {
				return Dependencies{}, err
			}
		} else {
			if err := os.Mkdir(path, 0700); err != nil {
				return Dependencies{}, err
			}
		}
	}
	if resuming {
		credentials, err := regularFile(filepath.Join(control, "empty-netrc"), true)
		if err != nil || len(credentials) != 0 {
			return Dependencies{}, fmt.Errorf("existing dependency controls are invalid")
		}
	} else {
		if err := os.WriteFile(filepath.Join(control, "empty-netrc"), nil, 0600); err != nil {
			return Dependencies{}, err
		}
	}
	goEnv := isolatedGoEnvironment(config, moduleCache, buildCache, control)
	if output, err := runGo(ctx, goExecutable, directory, goEnv, "mod", "download", "-modfile="+moduleFile, "all"); err != nil {
		return Dependencies{}, fmt.Errorf("prefetch Go dependencies: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := runGo(ctx, goExecutable, directory, goEnv, "mod", "verify", "-modfile="+moduleFile); err != nil {
		return Dependencies{}, fmt.Errorf("verify Go dependencies: %w: %s", err, strings.TrimSpace(string(output)))
	}
	moduleAfter, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil || hexHash(moduleAfter) != hexHash(moduleInput) {
		return Dependencies{}, fmt.Errorf("dependency acquisition changed source go.mod")
	}
	sumAfter, err := regularFile(filepath.Join(directory, "go.sum"), false)
	if err != nil || hexHash(sumAfter) != hexHash(sumInput) || (sumAfter == nil) != (sumInput == nil) {
		return Dependencies{}, fmt.Errorf("dependency acquisition changed source go.sum")
	}
	preparedModule, err := os.ReadFile(moduleFile)
	if err != nil {
		return Dependencies{}, err
	}
	preparedSum, err := regularFile(sumFile, false)
	if err != nil {
		return Dependencies{}, err
	}
	return Dependencies{
		ID: id, PreparedUTC: time.Now().UTC().Format(time.RFC3339Nano), GoVersion: goVersion,
		GOOS: config.GOOS, GOARCH: config.GOARCH, Proxy: recordedProxy, SumDB: config.SumDB,
		SourceModHash: hexHash(moduleInput), SourceSumHash: hexHash(sumInput), SourceSumPresent: sumInput != nil,
		ModuleHash: hexHash(preparedModule), SumHash: hexHash(preparedSum),
		ModuleFile: moduleFile, ModuleCache: moduleCache, BuildCache: buildCache,
	}, nil
}

func writeDependencyInput(path string, data []byte) (resultErr error) {
	input, err := os.CreateTemp(filepath.Dir(path), ".input-")
	if err != nil {
		return err
	}
	name := input.Name()
	defer func() {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if _, err := input.Write(data); err != nil {
		return errors.Join(err, input.Close())
	}
	if err := input.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func dependencyID(dependencies Dependencies) string {
	identity := sha256.Sum256([]byte(strings.Join([]string{
		dependencies.SourceModHash, dependencies.SourceSumHash, strconv.FormatBool(dependencies.SourceSumPresent),
		dependencies.GOOS, dependencies.GOARCH, dependencies.GoVersion, dependencies.Proxy, dependencies.SumDB,
	}, "\x00")))
	return hex.EncodeToString(identity[:])
}

func validateRecordedProxy(raw string) error {
	if strings.HasPrefix(raw, "file:") {
		path := strings.TrimPrefix(raw, "file:")
		if path == "" || !filepath.IsLocal(filepath.FromSlash(path)) {
			return fmt.Errorf("dependency context file proxy must be sandbox-relative")
		}
		return nil
	}
	return validateModuleProxy(raw, "")
}

func validateModuleProxy(raw, sandbox string) error {
	if strings.ContainsAny(raw, ",|") {
		return fmt.Errorf("dependency acquisition requires one proxy without fallback entries")
	}
	location, err := url.Parse(raw)
	if err != nil || location.User != nil || location.RawQuery != "" || location.Fragment != "" {
		return fmt.Errorf("module proxy requires an HTTPS or sandbox-local file URL without credentials, query, or fragment")
	}
	switch location.Scheme {
	case "https":
		if location.Hostname() == "" {
			return fmt.Errorf("module proxy HTTPS URL requires a host")
		}
	case "file":
		if sandbox == "" {
			return fmt.Errorf("file module proxy requires a sandbox")
		}
		if location.Host != "" {
			return fmt.Errorf("file module proxy must not name a host")
		}
		path, err := directoryWithoutLinks(moduleProxyPath(location.Path))
		if err != nil || !pathWithin(path, sandbox) {
			return fmt.Errorf("file module proxy must be a directory within the sandbox")
		}
	default:
		return fmt.Errorf("module proxy requires HTTPS or sandbox-local file transport")
	}
	return nil
}

func moduleProxyPath(path string) string {
	converted := filepath.FromSlash(path)
	trimmed := strings.TrimPrefix(converted, string(filepath.Separator))
	if filepath.VolumeName(trimmed) != "" {
		return trimmed
	}
	return converted
}

func validateLocalReplacements(directory, sandbox string, data []byte) error {
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("parse source go.mod: %w", err)
	}
	for _, replacement := range parsed.Replace {
		if replacement.New.Version != "" {
			continue
		}
		path := filepath.FromSlash(replacement.New.Path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		path, err = directoryWithoutLinks(path)
		if err != nil || !pathWithin(path, directory) || !pathWithin(path, sandbox) {
			return fmt.Errorf("local module replacement must remain within the materialized checkout")
		}
		if _, err := regularFile(filepath.Join(path, "go.mod"), true); err != nil {
			return err
		}
	}
	return nil
}

func isolatedGoEnvironment(config PrefetchConfig, moduleCache, buildCache, control string) []string {
	environment := make([]string, 0, len(os.Environ())+16)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "GO") || strings.HasPrefix(name, "GIT_") || strings.HasPrefix(name, "GCM_") || name == "NETRC" || name == "SSH_ASKPASS" || name == "LC_ALL" || name == "LANG" || name == "HTTP_PROXY" || name == "HTTPS_PROXY" || name == "ALL_PROXY" || name == "NO_PROXY" || name == "FTP_PROXY" {
			continue
		}
		environment = append(environment, variable)
	}
	environment = append(environment,
		"GOOS="+config.GOOS, "GOARCH="+config.GOARCH, "GOMODCACHE="+moduleCache, "GOCACHE="+buildCache,
		"GOPATH="+filepath.Join(control, "gopath"), "GOBIN="+filepath.Join(control, "bin"),
		"GOENV=off", "GOWORK=off", "GOPROXY="+config.Proxy, "GOSUMDB="+config.SumDB,
		"GOPRIVATE=", "GONOPROXY=", "GONOSUMDB=", "GOINSECURE=", "GOVCS=*:off",
		"GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOAUTH=off", "GOFLAGS=", "NETRC="+filepath.Join(control, "empty-netrc"),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GCM_INTERACTIVE=never", "LC_ALL=C", "LANG=C",
	)
	return environment
}

func runGo(ctx context.Context, executable, directory string, environment []string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	if environment != nil {
		command.Env = environment
	}
	return command.CombinedOutput()
}

func regularFile(filename string, required bool) ([]byte, error) {
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) && !required {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("module input %s must be a regular file", filepath.Base(filename))
	}
	return os.ReadFile(filename)
}

func existingDirectoryWithoutLinks(directory string) (string, error) {
	path, err := directoryWithoutLinks(directory)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("prepared dependency path must be an existing directory")
	}
	return path, nil
}

func hexHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
