package protocol

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
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

type Options struct {
	GOOS                   string
	DisableOpenFlags       bool
	DisableInterprocedural bool
	DisableStructFields    bool
	DisableAllocCells      bool
	DisablePaths           bool
}

type writeMode uint8

const (
	writeUnknown writeMode = iota
	writeBuffered
	writeDataSync
	writeFileSync
)

type fileState struct {
	path     *pathValue
	coverage Coverage
	metadata Coverage
	writes   writeMode
	r3       bool
}

type publication struct {
	path   *pathValue
	facts  Facts
	reason string
	site   Site
}

type trackedValue struct {
	file      *fileState
	namedFile *fileState
	path      *pathValue
	scalar    ssa.Value
	elements  []trackedValue
	cell      *memoryCell
	fields    map[int]trackedValue
	closure   *closureValue
}

type pathValue struct {
	text     string
	literal  bool
	nonempty bool
	parent   *pathValue
}

type closureValue struct {
	function *ssa.Function
	bindings []trackedValue
}

type memoryCell struct {
	value  trackedValue
	fields map[int]*memoryCell
}

func (cell *memoryCell) read() trackedValue {
	if cell.fields == nil {
		return cell.value
	}
	fields := make(map[int]trackedValue, len(cell.fields))
	for index, field := range cell.fields {
		fields[index] = field.read()
	}
	return trackedValue{fields: fields}
}

func (cell *memoryCell) store(value trackedValue) {
	if cell.fields == nil && value.fields == nil {
		cell.value = value
		return
	}
	if cell.fields == nil {
		cell.fields = make(map[int]*memoryCell)
	}
	for index := range value.fields {
		if cell.fields[index] == nil {
			cell.fields[index] = &memoryCell{}
		}
	}
	for index, field := range cell.fields {
		field.store(value.fields[index])
	}
}

type callFrame struct {
	values   map[ssa.Value]trackedValue
	caller   *callFrame
	call     *ssa.Call
	deferred []deferredCall
}

type deferredCall struct {
	common *ssa.CallCommon
	frame  *callFrame
}

func (frame *callFrame) snapshot() *callFrame {
	values := make(map[ssa.Value]trackedValue, len(frame.values))
	for value, tracked := range frame.values {
		values[value] = tracked
	}
	return &callFrame{values: values, caller: frame.caller, call: frame.call}
}

func (frame *callFrame) value(value ssa.Value) trackedValue {
	if tracked, ok := frame.values[value]; ok {
		return tracked
	}
	return trackedValue{scalar: value}
}

type execution struct {
	program      *ssa.Program
	root         string
	options      Options
	selected     map[*ssa.Function]bool
	visited      map[*ssa.Function]bool
	active       map[*ssa.Function]bool
	files        map[*pathValue]*fileState
	literals     map[string]*pathValue
	publications []publication
	unresolved   bool
	reason       string
	unchecked    bool
}

func Analyze(program *ssa.Program, initial []*ssa.Package, root string) ([]Site, error) {
	return AnalyzeWithOptions(program, initial, root, Options{})
}

func AnalyzeWithOptions(program *ssa.Program, initial []*ssa.Package, root string, options Options) ([]Site, error) {
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
	sort.Slice(functions, func(first, second int) bool {
		if functions[first].Pos() != functions[second].Pos() {
			return functions[first].Pos() < functions[second].Pos()
		}
		return functions[first].String() < functions[second].String()
	})
	selectedFunctions := make(map[*ssa.Function]bool)
	called := make(map[*ssa.Function]bool)
	for _, function := range functions {
		selectedFunctions[function] = true
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				if call, ok := instruction.(ssa.CallInstruction); ok {
					called[call.Common().StaticCallee()] = true
				}
				if closure, ok := instruction.(*ssa.MakeClosure); ok {
					if target, ok := closure.Fn.(*ssa.Function); ok {
						called[target] = true
					}
				}
			}
		}
	}
	visited := make(map[*ssa.Function]bool)
	publications := make([]publication, 0)
	for _, fallback := range []bool{false, true} {
		for _, function := range functions {
			if (!fallback && called[function]) || (fallback && visited[function]) {
				continue
			}
			run := &execution{
				program: program, root: root, options: options, selected: selectedFunctions,
				visited: visited, active: make(map[*ssa.Function]bool), files: make(map[*pathValue]*fileState),
				literals: make(map[string]*pathValue),
			}
			if _, err := run.invoke(function, nil, nil, nil, nil); err != nil {
				return nil, err
			}
			for _, publish := range run.publications {
				if run.unresolved {
					publish.facts.FileCoverage = CoverageUnknown
					publish.facts.DirectorySync = CoverageUnknown
					publish.reason = appendReason(publish.reason, "bounded analysis cannot resolve this control flow or call")
					publish.reason = appendReason(publish.reason, run.reason)
				}
				publish.facts.R3 = publish.facts.R3 || run.unchecked
				publications = append(publications, publish)
			}
		}
	}
	sites := mergePublications(publications)
	sort.Slice(sites, func(first, second int) bool {
		if sites[first].File != sites[second].File {
			return sites[first].File < sites[second].File
		}
		return sites[first].Offset < sites[second].Offset
	})
	return sites, nil
}

func (run *execution) invoke(function *ssa.Function, arguments, bindings []trackedValue, caller *callFrame, call *ssa.Call) ([]trackedValue, error) {
	run.visited[function] = true
	if run.active[function] || len(run.active) >= 32 {
		run.unresolved = true
		return nil, nil
	}
	run.active[function] = true
	defer delete(run.active, function)
	frame := &callFrame{values: make(map[ssa.Value]trackedValue), caller: caller, call: call}
	for index, parameter := range function.Params {
		if index < len(arguments) && !run.options.DisableInterprocedural {
			frame.values[parameter] = arguments[index]
		} else if basic, ok := parameter.Type().Underlying().(*types.Basic); ok && basic.Info()&types.IsString != 0 {
			frame.values[parameter] = trackedValue{scalar: parameter, path: &pathValue{}}
		}
	}
	for index, variable := range function.FreeVars {
		if index < len(bindings) && !run.options.DisableInterprocedural {
			frame.values[variable] = bindings[index]
		}
	}
	reachable := reachableBlocks(function.Blocks[0])
	if len(reachable) != 1 {
		run.unresolved = true
	}
	var results []trackedValue
	for _, block := range function.Blocks {
		if !reachable[block] {
			continue
		}
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.Alloc:
				if pointer, ok := instruction.Type().Underlying().(*types.Pointer); ok {
					if _, structure := pointer.Elem().Underlying().(*types.Struct); structure {
						frame.values[instruction] = trackedValue{cell: &memoryCell{fields: make(map[int]*memoryCell)}}
					} else if !run.options.DisableAllocCells {
						frame.values[instruction] = trackedValue{cell: &memoryCell{}}
					} else {
						run.unresolved = true
					}
				}
			case *ssa.FieldAddr:
				object := frame.value(instruction.X).cell
				if object == nil || run.options.DisableStructFields {
					run.unresolved = true
					continue
				}
				if object.fields == nil {
					object.fields = make(map[int]*memoryCell)
				}
				if object.fields[instruction.Field] == nil {
					object.fields[instruction.Field] = &memoryCell{}
				}
				frame.values[instruction] = trackedValue{cell: object.fields[instruction.Field]}
			case *ssa.Field:
				if run.options.DisableStructFields {
					run.unresolved = true
				} else {
					frame.values[instruction] = frame.value(instruction.X).fields[instruction.Field]
				}
			case *ssa.Store:
				if cell := frame.value(instruction.Addr).cell; cell != nil {
					cell.store(frame.value(instruction.Val))
				} else {
					run.unresolved = true
				}
			case *ssa.UnOp:
				if instruction.Op == token.MUL {
					if cell := frame.value(instruction.X).cell; cell != nil {
						frame.values[instruction] = cell.read()
					} else {
						run.unresolved = true
					}
				}
			case *ssa.ChangeType:
				frame.values[instruction] = frame.value(instruction.X)
			case *ssa.MakeClosure:
				if target, ok := instruction.Fn.(*ssa.Function); ok {
					closure := &closureValue{function: target}
					for _, binding := range instruction.Bindings {
						closure.bindings = append(closure.bindings, frame.value(binding))
					}
					frame.values[instruction] = trackedValue{closure: closure}
				}
			case *ssa.Extract:
				elements := frame.value(instruction.Tuple).elements
				if instruction.Index < len(elements) {
					frame.values[instruction] = elements[instruction.Index]
				}
			case *ssa.Return:
				results = nil
				for _, result := range instruction.Results {
					results = append(results, frame.value(result))
				}
			case *ssa.Defer:
				frame.deferred = append(frame.deferred, deferredCall{common: instruction.Common(), frame: frame.snapshot()})
			case *ssa.RunDefers:
				for len(frame.deferred) != 0 {
					last := len(frame.deferred) - 1
					deferred := frame.deferred[last]
					frame.deferred = frame.deferred[:last]
					if _, err := run.evaluateCall(deferred.common, deferred.frame, nil); err != nil {
						return nil, err
					}
				}
			case *ssa.Go:
				run.unresolved = true
			case *ssa.Call:
				value, err := run.evaluateCall(instruction.Common(), frame, instruction)
				if err != nil {
					return nil, err
				}
				frame.values[instruction] = value
			}
		}
	}
	return results, nil
}

func reachableBlocks(entry *ssa.BasicBlock) map[*ssa.BasicBlock]bool {
	reachable := make(map[*ssa.BasicBlock]bool)
	pending := []*ssa.BasicBlock{entry}
	for len(pending) != 0 {
		block := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[block] {
			continue
		}
		reachable[block] = true
		pending = append(pending, block.Succs...)
	}
	return reachable
}

func (run *execution) evaluateCall(common *ssa.CallCommon, frame *callFrame, call *ssa.Call) (trackedValue, error) {
	callee := common.StaticCallee()
	closure := frame.value(common.Value).closure
	if closure != nil {
		callee = closure.function
	}
	if callee == nil {
		if _, builtin := common.Value.(*ssa.Builtin); !builtin {
			run.unresolved = true
			if common.IsInvoke() {
				run.reason = appendReason(run.reason, "interface dispatch is unresolved")
				if (common.Method.Name() == "Sync" || common.Method.Name() == "Close") && !errorObservedInFrame(call, frame) {
					run.unchecked = true
				}
			}
		}
		return trackedValue{}, nil
	}
	if exactFunction(callee, "path/filepath", "Dir") && run.options.GOOS == "linux" {
		if run.options.DisablePaths {
			return trackedValue{}, nil
		}
		return trackedValue{path: run.parentPath(run.pathOf(frame.value(common.Args[0])))}, nil
	}
	if exactFunction(callee, "os", "Rename") && len(common.Args) == 2 {
		temporaryValue := frame.value(common.Args[0])
		temporary := run.pathOf(temporaryValue)
		destination := run.pathOf(frame.value(common.Args[1]))
		facts := Facts{FileCoverage: CoverageUnknown, DirectorySync: CoverageAbsent}
		reason := ""
		state := temporaryValue.namedFile
		if state == nil && temporary != nil {
			state = run.files[temporary]
		}
		if state != nil {
			facts.R3 = state.r3
			facts.FileCoverage = state.coverage
			if state.coverage == CoverageUnknown && state.writes == writeDataSync {
				reason = "O_DSYNC required metadata coverage is unresolved"
			}
		}
		temporaryParent := run.parentPath(temporary)
		destinationParent := run.parentPath(destination)
		if temporaryParent != nil && destinationParent != nil {
			if temporaryParent == destinationParent {
				facts.SameDirectory = CoveragePresent
			} else if temporaryParent.literal && destinationParent.literal {
				facts.SameDirectory = CoverageAbsent
			}
		}
		position := run.program.Fset.Position(common.Pos())
		relative, err := filepath.Rel(run.root, position.Filename)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return trackedValue{}, fmt.Errorf("site source is outside the analysis root")
		}
		run.publications = append(run.publications, publication{
			path: destinationParent, facts: facts, reason: reason,
			site: Site{File: filepath.ToSlash(relative), Offset: position.Offset},
		})
		return trackedValue{}, nil
	}
	if exactFunction(callee, "os", "Create") || exactFunction(callee, "os", "Open") || exactFunction(callee, "os", "OpenFile") || exactFunction(callee, "os", "CreateTemp") {
		filename := run.pathOf(frame.value(common.Args[0]))
		if callee.Name() == "CreateTemp" {
			parent := filename
			filename = &pathValue{nonempty: true}
			if !run.options.DisablePaths && parent != nil && parent.nonempty {
				filename.parent = parent
			}
		}
		state := &fileState{path: filename, coverage: CoverageAbsent, metadata: CoverageUnknown, writes: writeBuffered}
		if callee.Name() == "OpenFile" {
			state.writes = openWriteMode(run.program, frame.value(common.Args[1]).scalar, run.options)
		}
		if filename != nil && callee.Name() != "Open" {
			run.files[filename] = state
		}
		return trackedValue{elements: []trackedValue{{file: state}, {}}}, nil
	}
	if fileMethod(callee) && len(common.Args) != 0 {
		state := frame.value(common.Args[0]).file
		unchecked := (callee.Name() == "Sync" || callee.Name() == "Close") && !errorObservedInFrame(call, frame)
		if state == nil {
			run.unresolved = true
			run.unchecked = run.unchecked || unchecked
			return trackedValue{}, nil
		}
		switch callee.Name() {
		case "Name":
			value := trackedValue{namedFile: state}
			if !run.options.DisablePaths {
				value.path = state.path
			}
			return value, nil
		case "Write", "WriteString", "WriteAt":
			state.write(frame.value(common.Args[1]).scalar)
		case "Chmod", "Chown", "Truncate":
			state.coverage = CoverageAbsent
			state.metadata = CoverageAbsent
		case "Sync":
			state.coverage = CoveragePresent
			state.metadata = CoveragePresent
			for index := range run.publications {
				if state.path != nil && state.path == run.publications[index].path {
					run.publications[index].facts.DirectorySync = CoveragePresent
				}
			}
		case "Close":
		default:
			run.unresolved = true
		}
		if unchecked {
			state.r3 = true
			run.unchecked = true
			for index := range run.publications {
				run.publications[index].facts.R3 = true
			}
		}
		return trackedValue{}, nil
	}
	if run.selected[callee] {
		arguments := make([]trackedValue, 0, len(common.Args))
		for _, argument := range common.Args {
			arguments = append(arguments, frame.value(argument))
		}
		var bindings []trackedValue
		if closure != nil {
			bindings = closure.bindings
		}
		if run.options.DisableInterprocedural {
			run.unresolved = true
		}
		returned, err := run.invoke(callee, arguments, bindings, frame, call)
		if err != nil {
			return trackedValue{}, err
		}
		if !run.options.DisableInterprocedural {
			if len(returned) == 1 {
				return returned[0], nil
			}
			return trackedValue{elements: returned}, nil
		}
		return trackedValue{}, nil
	}
	run.unresolved = true
	run.reason = appendReason(run.reason, "callee body or effects are outside the selected analysis")
	return trackedValue{}, nil
}

func (run *execution) literalPath(text string) *pathValue {
	if run.literals[text] == nil {
		run.literals[text] = &pathValue{text: text, literal: true, nonempty: text != ""}
	}
	return run.literals[text]
}

func (run *execution) pathOf(value trackedValue) *pathValue {
	if value.path != nil {
		return value.path
	}
	if text, known := literalString(value.scalar); known {
		return run.literalPath(text)
	}
	return nil
}

func (run *execution) parentPath(value *pathValue) *pathValue {
	if value == nil {
		return nil
	}
	if value.parent == nil {
		if value.literal {
			value.parent = run.literalPath(path.Dir(value.text))
		} else {
			value.parent = &pathValue{nonempty: true}
		}
	}
	return value.parent
}

func mergePublications(publications []publication) []Site {
	type coordinate struct {
		file   string
		offset int
	}
	merged := make(map[coordinate]publication)
	for _, publish := range publications {
		key := coordinate{publish.site.File, publish.site.Offset}
		if prior, exists := merged[key]; exists {
			publish.facts.SameDirectory = mergeCoverage(prior.facts.SameDirectory, publish.facts.SameDirectory)
			publish.facts.FileCoverage = mergeCoverage(prior.facts.FileCoverage, publish.facts.FileCoverage)
			publish.facts.DirectorySync = mergeCoverage(prior.facts.DirectorySync, publish.facts.DirectorySync)
			publish.facts.R3 = publish.facts.R3 || prior.facts.R3
			publish.reason = appendReason(prior.reason, publish.reason)
		}
		merged[key] = publish
	}
	sites := make([]Site, 0, len(merged))
	for _, publish := range merged {
		publish.site.Result = ClassifyFacts(publish.facts)
		if publish.site.Reason != "" {
			publish.site.Reason = appendReason(publish.site.Reason, publish.reason)
		}
		sites = append(sites, publish.site)
	}
	return sites
}

func mergeCoverage(first, second Coverage) Coverage {
	if first == second {
		return first
	}
	return CoverageUnknown
}

func appendReason(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" || first == second {
		return first
	}
	return first + "; " + second
}

func openWriteMode(program *ssa.Program, value ssa.Value, options Options) writeMode {
	if options.GOOS != "linux" || options.DisableOpenFlags {
		return writeUnknown
	}
	flags, known := integerConstant(value)
	if !known {
		return writeUnknown
	}
	fileSync, fileSyncKnown := packageInteger(program, "os", "O_SYNC")
	if !fileSyncKnown {
		return writeUnknown
	}
	if flags&fileSync == fileSync {
		return writeFileSync
	}
	dataSync, dataSyncKnown := packageInteger(program, "syscall", "O_DSYNC")
	if !dataSyncKnown {
		return writeUnknown
	}
	if flags&dataSync == dataSync {
		return writeDataSync
	}
	return writeBuffered
}

func packageInteger(program *ssa.Program, packagePath, name string) (int64, bool) {
	pkg := program.ImportedPackage(packagePath)
	if pkg == nil {
		return 0, false
	}
	named, ok := pkg.Members[name].(*ssa.NamedConst)
	if !ok {
		return 0, false
	}
	return integerConstant(named.Value)
}

func integerConstant(value ssa.Value) (int64, bool) {
	literal, ok := value.(*ssa.Const)
	if !ok || literal.Value == nil || literal.Value.Kind() != constant.Int {
		return 0, false
	}
	return constant.Int64Val(literal.Value)
}

func writeLength(value ssa.Value) (int64, bool) {
	if converted, ok := value.(*ssa.Convert); ok {
		value = converted.X
	}
	if text, known := literalString(value); known {
		return int64(len(text)), true
	}
	if literal, ok := value.(*ssa.Const); ok && literal.IsNil() {
		return 0, true
	}
	return 0, false
}

func (state *fileState) write(value ssa.Value) {
	length, known := writeLength(value)
	if known && length == 0 {
		return
	}
	coverage := CoverageAbsent
	switch state.writes {
	case writeUnknown:
		coverage = CoverageUnknown
	case writeFileSync:
		coverage = CoveragePresent
	case writeDataSync:
		coverage = state.metadata
	}
	if !known && coverage != state.coverage {
		coverage = CoverageUnknown
	}
	state.coverage = coverage
	if state.writes == writeFileSync {
		if known {
			state.metadata = CoveragePresent
		} else if state.metadata != CoveragePresent {
			state.metadata = CoverageUnknown
		}
	}
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

func errorObservedInFrame(call *ssa.Call, frame *callFrame) bool {
	if call == nil {
		return false
	}
	references := call.Referrers()
	if references == nil {
		return false
	}
	for _, instruction := range *references {
		if _, returned := instruction.(*ssa.Return); returned {
			return frame.caller == nil || errorObservedInFrame(frame.call, frame.caller)
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
