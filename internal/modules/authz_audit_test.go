package modules_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every exported Service method that takes a permissions.Principal must reach
// a permission gate (Checker.Require/HasAny, or a module helper such as
// s.view/s.manage), directly or through one same-file helper call. Methods
// that legitimately do not are listed below with the reason. A new ungated
// method fails this test: either gate it or add it here with a justification.
var ungatedAllowlist = map[string]string{
	"accounting/app/service.go ReverseJournalForSourceTx": "internal: called inside a gated caller's transaction",
	"accounting/app/service.go PostTx":                    "internal: called inside a gated caller's transaction",
	"identity/app/mfa.go DisableMFA":                      "self-service: acts only on the caller's own account",
	"identity/app/mfa.go EnrollMFA":                       "self-service: acts only on the caller's own account",
	"identity/app/mfa.go VerifyMFAEnroll":                 "self-service: acts only on the caller's own account",
	"identity/app/password_flow.go ChangePassword":        "self-service: acts only on the caller's own account",
	"identity/app/service.go ListSessions":                "self-service: caller's own sessions",
	"identity/app/service.go Logout":                      "self-service: caller's own session",
	"identity/app/service.go LogoutAllDevices":            "self-service: caller's own sessions",
}

func isGate(name string) bool {
	l := strings.ToLower(name)
	switch l {
	case "require", "hasany", "allowedlegalentities", "view", "manage", "canview", "canmanage":
		return true
	}
	return strings.HasPrefix(l, "require") || strings.HasPrefix(l, "authoriz")
}

func hasPrincipalParam(fd *ast.FuncDecl) bool {
	for _, p := range fd.Type.Params.List {
		if s, ok := p.Type.(*ast.SelectorExpr); ok && s.Sel.Name == "Principal" {
			return true
		}
	}
	return false
}

func calledNames(fd *ast.FuncDecl) (out []string) {
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok {
				out = append(out, s.Sel.Name)
			}
		}
		return true
	})
	return out
}

func TestServiceMethodsAreGated(t *testing.T) {
	files, err := filepath.Glob("*/app/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no service files found: %v", err)
	}
	var ungated []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		methods := map[string]*ast.FuncDecl{}
		for _, d := range af.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Body != nil {
				methods[fd.Name.Name] = fd
			}
		}
		for name, fd := range methods {
			if !ast.IsExported(name) || !hasPrincipalParam(fd) {
				continue
			}
			gated := false
			for _, c := range calledNames(fd) {
				if isGate(c) {
					gated = true
					break
				}
				if h, ok := methods[c]; ok {
					for _, c2 := range calledNames(h) {
						if isGate(c2) {
							gated = true
						}
					}
				}
			}
			if !gated {
				ungated = append(ungated, f+" "+name)
			}
		}
	}
	sort.Strings(ungated)
	seen := map[string]bool{}
	for _, u := range ungated {
		key := strings.TrimPrefix(u, "")
		if _, ok := ungatedAllowlist[key]; !ok {
			t.Errorf("service method has no permission gate and is not allowlisted: %s", u)
		}
		seen[key] = true
	}
	for k := range ungatedAllowlist {
		if !seen[k] {
			t.Errorf("stale allowlist entry (method now gated or removed): %s", k)
		}
	}
}
