package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

// ASTMetadata holds extracted AST information for a single Go file.
type ASTMetadata struct {
	Objects map[string]ObjectMeta `json:"objects"`
}

// ObjectMeta holds AST metadata for a single named object (func, type, method).
type ObjectMeta struct {
	Kind       string        `json:"kind"`
	Signature  *Signature    `json:"signature,omitempty"`
	Calls      []string      `json:"calls,omitempty"`
	Fields     []FieldInfo   `json:"fields,omitempty"`
	Methods    []MethodInfo  `json:"methods,omitempty"`
	Position   PositionInfo  `json:"position"`
	Exported   bool          `json:"exported"`
	Interfaces []string      `json:"interfaces,omitempty"`
}

// Signature describes function/method parameters and return types.
type Signature struct {
	Params  []ParamInfo `json:"params"`
	Returns []string    `json:"returns"`
}

// ParamInfo holds a single parameter's info.
type ParamInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// FieldInfo holds struct field information.
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// MethodInfo holds interface/type method information.
type MethodInfo struct {
	Name    string   `json:"name"`
	Params  []string `json:"params,omitempty"`
	Returns []string `json:"returns,omitempty"`
}

// PositionInfo holds file position data.
type PositionInfo struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: go_ast_extract <go-file>\n")
		os.Exit(1)
	}

	filePath := args[0]
	result, err := extractAST(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Output JSON to stdout
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func extractAST(filePath string) (*ASTMetadata, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	result := &ASTMetadata{
		Objects: make(map[string]ObjectMeta),
	}

	// Collect all function declarations
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			extractFunc(fset, d, result)
		case *ast.GenDecl:
			extractGenDecl(fset, d, result)
		}
	}

	return result, nil
}

func extractFunc(fset *token.FileSet, fn *ast.FuncDecl, meta *ASTMetadata) {
	key := fn.Name.Name
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := typeToString(fn.Recv.List[0].Type)
		key = recv + "." + fn.Name.Name
	}

	startPos := fset.Position(fn.Pos())
	endPos := fset.Position(fn.End())

	objMeta := ObjectMeta{
		Kind:      "func",
		Position:  PositionInfo{StartLine: startPos.Line, EndLine: endPos.Line},
		Exported:  ast.IsExported(fn.Name.Name),
		Signature: extractSignature(fn.Type),
		Calls:     extractCallsFromFunc(fn),
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		objMeta.Kind = "method"
	}

	meta.Objects[key] = objMeta
}

func extractGenDecl(fset *token.FileSet, gd *ast.GenDecl, meta *ASTMetadata) {
	switch gd.Tok {
	case token.TYPE:
		for _, spec := range gd.Specs {
			ts := spec.(*ast.TypeSpec)
			startPos := fset.Position(ts.Pos())
			endPos := fset.Position(ts.End())

			objMeta := ObjectMeta{
				Position: PositionInfo{StartLine: startPos.Line, EndLine: endPos.Line},
				Exported: ast.IsExported(ts.Name.Name),
			}

			switch t := ts.Type.(type) {
			case *ast.StructType:
				objMeta.Kind = "struct"
				objMeta.Fields = extractStructFields(t)
			case *ast.InterfaceType:
				objMeta.Kind = "interface"
				objMeta.Methods = extractInterfaceMethods(t)
			default:
				objMeta.Kind = "type"
			}

			meta.Objects[ts.Name.Name] = objMeta
		}

	case token.VAR, token.CONST:
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for _, name := range vs.Names {
				startPos := fset.Position(vs.Pos())
				endPos := fset.Position(vs.End())

				objMeta := ObjectMeta{
					Kind:     "var",
					Position: PositionInfo{StartLine: startPos.Line, EndLine: endPos.Line},
					Exported: ast.IsExported(name.Name),
				}
				if gd.Tok == token.CONST {
					objMeta.Kind = "const"
				}

				meta.Objects[name.Name] = objMeta
			}
		}
	}
}

func extractSignature(sig *ast.FuncType) *Signature {
	result := &Signature{
		Params:  []ParamInfo{},
		Returns: []string{},
	}

	if sig.Params != nil {
		for _, field := range sig.Params.List {
			typeStr := typeToString(field.Type)
			if len(field.Names) == 0 {
				result.Params = append(result.Params, ParamInfo{Name: "", Type: typeStr})
			} else {
				for _, name := range field.Names {
					result.Params = append(result.Params, ParamInfo{Name: name.Name, Type: typeStr})
				}
			}
		}
	}

	if sig.Results != nil {
		for _, field := range sig.Results.List {
			typeStr := typeToString(field.Type)
			result.Returns = append(result.Returns, typeStr)
		}
	}

	return result
}

func extractStructFields(st *ast.StructType) []FieldInfo {
	var fields []FieldInfo

	if st.Fields == nil {
		return fields
	}

	for _, field := range st.Fields.List {
		typeStr := typeToString(field.Type)
		tag := ""
		if field.Tag != nil {
			tag = field.Tag.Value
		}

		if len(field.Names) == 0 {
			// Embedded field
			fields = append(fields, FieldInfo{Name: typeStr, Type: typeStr, Tag: tag})
		} else {
			for _, name := range field.Names {
				fields = append(fields, FieldInfo{Name: name.Name, Type: typeStr, Tag: tag})
			}
		}
	}

	return fields
}

func extractInterfaceMethods(it *ast.InterfaceType) []MethodInfo {
	var methods []MethodInfo

	if it.Methods == nil {
		return methods
	}

	for _, method := range it.Methods.List {
		if len(method.Names) == 0 {
			// Embedded interface
			continue
		}

		ft, ok := method.Type.(*ast.FuncType)
		if !ok {
			continue
		}

		for _, name := range method.Names {
			var params, returns []string

			if ft.Params != nil {
				for _, p := range ft.Params.List {
					params = append(params, typeToString(p.Type))
				}
			}

			if ft.Results != nil {
				for _, r := range ft.Results.List {
					returns = append(returns, typeToString(r.Type))
				}
			}

			methods = append(methods, MethodInfo{
				Name:    name.Name,
				Params:  params,
				Returns: returns,
			})
		}
	}

	return methods
}

func extractCallsFromFunc(fn *ast.FuncDecl) []string {
	calls := make(map[string]struct{})
	if fn.Body != nil {
		extractCallsFromStmt(fn.Body, calls)
	}

	// Convert to sorted slice
	var result []string
	for call := range calls {
		result = append(result, call)
	}
	sort.Strings(result)

	return result
}

func extractCallsFromStmt(node ast.Node, calls map[string]struct{}) {
	ast.Inspect(node, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.CallExpr:
			switch f := e.Fun.(type) {
			case *ast.Ident:
				calls[f.Name] = struct{}{}
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok {
					calls[id.Name+"."+f.Sel.Name] = struct{}{}
				}
			}
		}
		return true
	})
}

func typeToString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return typeToString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + typeToString(e.X)
	case *ast.ArrayType:
		return "[]" + typeToString(e.Elt)
	case *ast.MapType:
		return "map[" + typeToString(e.Key) + "]" + typeToString(e.Value)
	case *ast.ChanType:
		if e.Dir == ast.SEND {
			return "chan<- " + typeToString(e.Value)
		} else if e.Dir == ast.RECV {
			return "<-chan " + typeToString(e.Value)
		}
		return "chan " + typeToString(e.Value)
	case *ast.FuncType:
		return "func" + typeToString(e)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.StructType:
		return "struct{}"
	case *ast.IndexExpr:
		return typeToString(e.X) + "[" + typeToString(e.Index) + "]"
	case *ast.IndexListExpr:
		var indices []string
		for _, idx := range e.Indices {
			indices = append(indices, typeToString(idx))
		}
		return typeToString(e.X) + "[" + strings.Join(indices, ", ") + "]"
	default:
		return ""
	}
}
