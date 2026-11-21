package load

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type Config struct {
	Directory   string
	Patterns    []string
	GOOS        string
	GOARCH      string
	BuildFlags  []string
	ModuleCache string
	BuildCache  string
	ModuleFile  string
}

type Program struct {
	SSA       *ssa.Program
	CallGraph *callgraph.Graph
	Packages  []*packages.Package
	Initial   []*ssa.Package
	Config    Config
}

type Summary struct {
	PackageCount    int      `json:"package_count"`
	InitialPackages []string `json:"initial_packages"`
}

func Summarize(program *Program) (Summary, error) {
	if program == nil || len(program.Initial) == 0 {
		return Summary{}, errors.New("load summary requires selected packages")
	}
	initial := make([]string, 0, len(program.Initial))
	for _, pkg := range program.Initial {
		if pkg == nil || pkg.Pkg == nil {
			return Summary{}, errors.New("load summary contains an unavailable package")
		}
		initial = append(initial, pkg.Pkg.Path())
	}
	sort.Strings(initial)
	return Summary{PackageCount: len(program.Packages), InitialPackages: initial}, nil
}

func Open(ctx context.Context, config Config) (*Program, error) {
	if config.Directory == "" || len(config.Patterns) == 0 || config.GOOS == "" || config.GOARCH == "" {
		return nil, errors.New("loading requires a directory, explicit package patterns, GOOS, and GOARCH")
	}
	explicitCaches := config.ModuleCache != "" || config.BuildCache != ""
	if explicitCaches && (!filepath.IsAbs(config.ModuleCache) || !filepath.IsAbs(config.BuildCache)) {
		return nil, errors.New("offline loading requires both absolute module and build cache paths")
	}
	if config.ModuleFile != "" && (!explicitCaches || !filepath.IsAbs(config.ModuleFile)) {
		return nil, errors.New("alternate module loading requires explicit caches and an absolute module file")
	}
	environment := make([]string, 0, len(os.Environ())+4)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if explicitCaches && strings.HasPrefix(strings.ToUpper(name), "GO") {
			continue
		}
		switch strings.ToUpper(name) {
		case "GOOS", "GOARCH", "GOPROXY", "GOTOOLCHAIN":
		default:
			environment = append(environment, variable)
		}
	}
	environment = append(environment, "GOOS="+config.GOOS, "GOARCH="+config.GOARCH, "GOPROXY=off", "GOTOOLCHAIN=local")
	if explicitCaches {
		environment = append(environment,
			"GOMODCACHE="+config.ModuleCache, "GOCACHE="+config.BuildCache,
			"GOENV=off", "GOWORK=off", "GOSUMDB=off", "GOPRIVATE=", "GONOPROXY=", "GONOSUMDB=", "GOFLAGS=", "GOTELEMETRY=off",
		)
		config.BuildFlags = append(append([]string(nil), config.BuildFlags...), "-mod=readonly")
		if config.ModuleFile != "" {
			config.BuildFlags = append(config.BuildFlags, "-modfile="+config.ModuleFile)
		}
	}
	loaded, err := packages.Load(&packages.Config{
		Context:    ctx,
		Dir:        config.Directory,
		Mode:       packages.LoadSyntax,
		Env:        environment,
		BuildFlags: config.BuildFlags,
	}, config.Patterns...)
	if err != nil {
		return nil, fmt.Errorf("load package graph: %w", err)
	}
	if len(loaded) == 0 {
		return nil, errors.New("package selection matched no packages")
	}
	diagnostics := make([]string, 0)
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		for _, diagnostic := range pkg.Errors {
			diagnostics = append(diagnostics, pkg.ID+": "+diagnostic.Error())
		}
	})
	if len(diagnostics) != 0 {
		sort.Strings(diagnostics)
		return nil, fmt.Errorf("package graph is incomplete: %s", strings.Join(diagnostics, "; "))
	}
	sort.Slice(loaded, func(first, second int) bool { return loaded[first].ID < loaded[second].ID })
	program, initial := ssautil.Packages(loaded, ssa.InstantiateGenerics)
	for index, pkg := range initial {
		if pkg == nil {
			return nil, fmt.Errorf("SSA unavailable for package %s", loaded[index].ID)
		}
	}
	program.Build()
	return &Program{SSA: program, CallGraph: cha.CallGraph(program), Packages: loaded, Initial: initial, Config: config}, nil
}
