package protocol

import (
	"fmt"
	"go/constant"
	"go/token"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type Site struct {
	File   string `json:"file"`
	Offset int    `json:"offset"`
	Result
}

type fileState struct {
	path             string
	wrote            bool
	coverage         Coverage
	openFlagsUnknown bool
	r3               bool
}

type publication struct {
	path  string
	facts Facts
	site  Site
}

func Analyze(program *ssa.Program, initial []*ssa.Package, root string) ([]Site, error) {
	if program == nil || len(initial) == 0 || root == "" {
		return nil, fmt.Errorf("analysis requires a built program, selected packages, and root")
	}
	selected := make(map[*ssa.Package]bool)
	for _, pkg := range initial {
		selected[pkg] = true
	}
	functions := make([]*ssa.Function, 0)
	for function := range ssautil.AllFunctions(program) {
		if selected[function.Pkg] && len(function.Blocks) != 0 {
			functions = append(functions, function)
		}
	}
	sort.Slice(functions, func(first, second int) bool { return functions[first].Pos() < functions[second].Pos() })
	sites := make([]Site, 0)
	for _, function := range functions {
		found, err := analyzeFunction(program, function, root)
		if err != nil {
			return nil, err
		}
		sites = append(sites, found...)
	}
	sort.Slice(sites, func(first, second int) bool {
		if sites[first].File != sites[second].File {
			return sites[first].File < sites[second].File
		}
		return sites[first].Offset < sites[second].Offset
	})
	return sites, nil
}

func analyzeFunction(program *ssa.Program, function *ssa.Function, root string) ([]Site, error) {
	handles := make(map[ssa.Value]*fileState)
	files := make(map[string]*fileState)
	publications := make([]publication, 0)
	unresolved := len(function.Blocks) != 1
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if extracted, ok := instruction.(*ssa.Extract); ok && extracted.Index == 0 {
				if state := handles[extracted.Tuple]; state != nil {
					handles[extracted] = state
				}
			}
			call, ok := instruction.(*ssa.Call)
			if !ok {
				if _, deferred := instruction.(*ssa.Defer); deferred {
					unresolved = true
				}
				if _, concurrent := instruction.(*ssa.Go); concurrent {
					unresolved = true
				}
				continue
			}
			common := call.Common()
			callee := common.StaticCallee()
			if callee == nil {
				if _, builtin := common.Value.(*ssa.Builtin); !builtin {
					unresolved = true
				}
				continue
			}
			if exactFunction(callee, "os", "Rename") && len(common.Args) == 2 {
				temporary, temporaryOK := literalString(common.Args[0])
				destination, destinationOK := literalString(common.Args[1])
				facts := Facts{FileCoverage: CoverageUnknown, DirectorySync: CoverageAbsent}
				if temporaryOK {
					if state := files[temporary]; state != nil {
						facts.R3 = state.r3
					}
				}
				if temporaryOK && destinationOK {
					facts.SameDirectory = CoverageAbsent
					if path.Dir(temporary) == path.Dir(destination) {
						facts.SameDirectory = CoveragePresent
					}
					if state := files[temporary]; state != nil && state.wrote {
						facts.R3 = state.r3
						facts.FileCoverage = state.coverage
					}
				}
				position := program.Fset.Position(common.Pos())
				relative, err := filepath.Rel(root, position.Filename)
				if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
					return nil, fmt.Errorf("site source is outside the analysis root")
				}
				publications = append(publications, publication{
					path: path.Dir(destination), facts: facts,
					site: Site{File: filepath.ToSlash(relative), Offset: position.Offset},
				})
				continue
			}
			if exactFunction(callee, "os", "Create") || exactFunction(callee, "os", "Open") || exactFunction(callee, "os", "OpenFile") {
				if len(common.Args) != 0 {
					filename, known := literalString(common.Args[0])
					state := &fileState{path: filename, openFlagsUnknown: callee.Name() == "OpenFile"}
					if !known {
						state.path = ""
					}
					handles[call] = state
					if known && (callee.Name() == "Create" || callee.Name() == "OpenFile") {
						files[filename] = state
					}
				}
				continue
			}
			if fileMethod(callee) && len(common.Args) != 0 {
				state := handles[common.Args[0]]
				if state == nil {
					unresolved = true
					continue
				}
				switch callee.Name() {
				case "Write", "WriteString", "WriteAt":
					state.wrote = true
					state.coverage = CoverageAbsent
					if state.openFlagsUnknown {
						state.coverage = CoverageUnknown
					}
				case "Chmod", "Chown", "Truncate":
					state.coverage = CoverageAbsent
				case "Sync":
					state.coverage = CoveragePresent
					for index := range publications {
						if state.path != "" && state.path == publications[index].path {
							publications[index].facts.DirectorySync = CoveragePresent
						}
					}
					if !errorObserved(call) {
						state.r3 = true
						for index := range publications {
							publications[index].facts.R3 = true
						}
					}
				case "Close":
					if !errorObserved(call) {
						state.r3 = true
						for index := range publications {
							publications[index].facts.R3 = true
						}
					}
				default:
					unresolved = true
				}
				continue
			}
			unresolved = true
		}
	}
	sites := make([]Site, 0, len(publications))
	for _, publish := range publications {
		if unresolved {
			publish.facts.FileCoverage = CoverageUnknown
			publish.facts.DirectorySync = CoverageUnknown
		}
		publish.site.Result = ClassifyFacts(publish.facts)
		if unresolved {
			publish.site.Reason = "bounded direct-path analysis cannot resolve this control flow or call"
		}
		sites = append(sites, publish.site)
	}
	return sites, nil
}

func exactFunction(function *ssa.Function, packagePath, name string) bool {
	return function.Pkg != nil && function.Pkg.Pkg.Path() == packagePath && function.Name() == name && function.Signature.Recv() == nil
}

func fileMethod(function *ssa.Function) bool {
	if function.Pkg == nil || function.Pkg.Pkg.Path() != "os" || function.Signature.Recv() == nil {
		return false
	}
	return function.Signature.Recv().Type().String() == "*os.File"
}

func literalString(value ssa.Value) (string, bool) {
	literal, ok := value.(*ssa.Const)
	if !ok || literal.Value == nil || literal.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(literal.Value), true
}

func errorObserved(call *ssa.Call) bool {
	references := call.Referrers()
	if references == nil {
		return false
	}
	for _, instruction := range *references {
		if _, returned := instruction.(*ssa.Return); returned {
			return true
		}
		comparison, compared := instruction.(*ssa.BinOp)
		if compared && (comparison.Op == token.EQL || comparison.Op == token.NEQ) {
			for _, use := range *comparison.Referrers() {
				if _, branch := use.(*ssa.If); branch {
					return true
				}
			}
		}
	}
	return false
}
