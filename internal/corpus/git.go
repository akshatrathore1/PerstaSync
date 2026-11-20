package corpus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type gitRunner struct {
	executable string
	controls   string
	config     string
	hooks      string
	allowLocal bool
}

func newGitRunner(sandbox string) (*gitRunner, error) {
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("Git is required for materialization: %w", err)
	}
	root, err := directoryWithoutLinks(sandbox)
	if err != nil {
		return nil, err
	}
	controls, err := os.MkdirTemp(root, ".git-controls-")
	if err != nil {
		return nil, fmt.Errorf("create Git control directory: %w", err)
	}
	config := filepath.Join(controls, "empty-config")
	if err := os.WriteFile(config, nil, 0600); err != nil {
		return nil, errors.Join(err, os.RemoveAll(controls))
	}
	hooks := filepath.Join(controls, "empty-hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		return nil, errors.Join(err, os.RemoveAll(controls))
	}
	return &gitRunner{executable: executable, controls: controls, config: config, hooks: hooks}, nil
}

func (runner *gitRunner) command(ctx context.Context, directory string, arguments ...string) *exec.Cmd {
	configured := []string{
		"-c", "core.hooksPath=" + runner.hooks,
		"-c", "init.templateDir=" + runner.hooks,
		"-c", "core.attributesFile=" + runner.config,
		"-c", "core.autocrlf=false",
		"-c", "core.symlinks=true",
		"-c", "core.fsmonitor=false",
		"-c", "credential.helper=",
		"-c", "core.askPass=",
		"-c", "submodule.recurse=false",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "filter.lfs.process=",
		"-c", "filter.lfs.smudge=",
		"-c", "filter.lfs.required=false",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
	}
	if runner.allowLocal {
		configured = append(configured, "-c", "protocol.file.allow=always")
	}
	command := exec.CommandContext(ctx, runner.executable, append(configured, arguments...)...)
	command.Dir = directory
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "GIT_") || strings.HasPrefix(name, "GCM_") || name == "SSH_ASKPASS" || name == "LC_ALL" || name == "LANG" {
			continue
		}
		command.Env = append(command.Env, variable)
	}
	command.Env = append(command.Env,
		"GIT_CONFIG_GLOBAL="+runner.config, "GIT_CONFIG_SYSTEM="+runner.config,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_LFS_SKIP_SMUDGE=1", "GCM_INTERACTIVE=never", "LC_ALL=C", "LANG=C",
	)
	return command
}

func (runner *gitRunner) close() error {
	return os.RemoveAll(runner.controls)
}

func (runner *gitRunner) run(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	command := runner.command(ctx, directory, arguments...)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("Git command failed: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	return output, nil
}
