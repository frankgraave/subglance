package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specPath is the OpenAPI document that documents this package's routes.
const specPath = "../../docs/openapi.yaml"

// openAPISpec is the subset of the document these tests reason about. The rest
// of the file is deliberately not modelled: this is a drift test, not a
// validator, and every field it parses is a field a future edit could break.
type openAPISpec struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Security []map[string][]string `yaml:"security"`
	// A path item holds operations keyed by HTTP method, but also non-method
	// keys such as a shared `parameters` list, which is why the values stay
	// raw nodes until the method keys have been filtered out.
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		SecuritySchemes map[string]struct {
			Type string `yaml:"type"`
		} `yaml:"securitySchemes"`
		Schemas map[string]yaml.Node `yaml:"schemas"`
	} `yaml:"components"`
}

type operation struct {
	OperationID string                 `yaml:"operationId"`
	Summary     string                 `yaml:"summary"`
	Description string                 `yaml:"description"`
	Tags        []string               `yaml:"tags"`
	Access      string                 `yaml:"x-subglance-access"`
	Security    *[]map[string][]string `yaml:"security"`
	Responses   map[string]yaml.Node   `yaml:"responses"`
}

// httpMethods are the keys in a path item that denote an operation. Anything
// else at that level (parameters, summary, $ref) is not one.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

func loadSpec(t *testing.T) *openAPISpec {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(specPath))
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	// KnownFields is off on purpose: the spec carries far more than this
	// struct models, and rejecting the parts we do not care about would make
	// every enrichment of the document a test failure.
	var spec openAPISpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}
	return &spec
}

// specOperations flattens the document into "METHOD /path" keys, mirroring the
// shape of Server.routes().
func specOperations(t *testing.T, spec *openAPISpec) map[string]*operation {
	t.Helper()

	ops := make(map[string]*operation)
	for path, item := range spec.Paths {
		for method, node := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			key := strings.ToUpper(method) + " " + path
			var op operation
			if err := node.Decode(&op); err != nil {
				t.Errorf("decode operation %s: %v", key, err)
				continue
			}
			if _, dup := ops[key]; dup {
				t.Errorf("duplicate operation %s in spec", key)
			}
			ops[key] = &op
		}
	}
	return ops
}

func routeKey(rt route) string { return rt.Method + " " + rt.Pattern }

// TestOpenAPIMatchesRoutes is the reason the route table exists as data.
//
// Documentation that is written once and then diverges is worse than none: it
// tells a client something confidently wrong. Tying the spec to the same table
// the mux is built from means an undocumented route, a documented route that
// no longer exists, or a route documented at the wrong privilege level all
// fail the build instead of shipping.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	spec := loadSpec(t)
	ops := specOperations(t, spec)

	var srv Server
	inCode := make(map[string]access)
	for _, rt := range srv.routes() {
		if !rt.documented() {
			continue // see route.documented: the SPA catch-all is not an API
		}
		inCode[routeKey(rt)] = rt.Access
	}

	var missing, extra []string
	for key := range inCode {
		if _, ok := ops[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range ops {
		if _, ok := inCode[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	for _, key := range missing {
		t.Errorf("route %s is served but not documented in %s", key, specPath)
	}
	for _, key := range extra {
		t.Errorf("%s documents %s, which the server does not serve", specPath, key)
	}
}

// TestOpenAPIAccessLevelsMatch checks the privilege level of every documented
// operation against the route table.
//
// This is the half of the drift problem that a route-name comparison misses: a
// spec can name every endpoint correctly and still tell a reader that deleting
// a user is something a viewer may do.
func TestOpenAPIAccessLevelsMatch(t *testing.T) {
	spec := loadSpec(t)
	ops := specOperations(t, spec)

	var srv Server
	for _, rt := range srv.routes() {
		if !rt.documented() {
			continue
		}
		key := routeKey(rt)
		op, ok := ops[key]
		if !ok {
			continue // reported by TestOpenAPIMatchesRoutes
		}
		if want := rt.Access.String(); op.Access != want {
			t.Errorf("%s: x-subglance-access is %q, route table says %q",
				key, op.Access, want)
		}
	}
}

// TestOpenAPIPublicOperationsOptOutOfSecurity guards the fail-closed default in
// the document itself.
//
// The spec applies a global security requirement, so an operation that is
// genuinely public must override it with an empty list. Forgetting that makes
// generated clients demand credentials for the login endpoint — and, worse,
// hides which endpoints are truly reachable unauthenticated from anyone
// auditing the surface.
func TestOpenAPIPublicOperationsOptOutOfSecurity(t *testing.T) {
	spec := loadSpec(t)
	ops := specOperations(t, spec)

	if len(spec.Security) == 0 {
		t.Fatal("spec has no top-level security requirement, so per-operation opt-outs mean nothing")
	}

	for key, op := range ops {
		switch op.Access {
		case accessPublic.String():
			if op.Security == nil {
				t.Errorf("%s is public but does not override the global security requirement", key)
				continue
			}
			if len(*op.Security) != 0 {
				t.Errorf("%s is public but declares security requirements %v", key, *op.Security)
			}
		default:
			if op.Security != nil && len(*op.Security) == 0 {
				t.Errorf("%s requires %s but opts out of security with an empty list",
					key, op.Access)
			}
		}
	}
}

var operationIDPattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// TestOpenAPIOperationsAreWellFormed checks the fields a code generator and a
// human reader both need. Generators derive method names from operationId, so
// a missing or duplicated one silently produces an unusable client.
func TestOpenAPIOperationsAreWellFormed(t *testing.T) {
	spec := loadSpec(t)
	ops := specOperations(t, spec)

	seen := make(map[string]string, len(ops))
	for key, op := range ops {
		switch {
		case op.OperationID == "":
			t.Errorf("%s has no operationId", key)
		case !operationIDPattern.MatchString(op.OperationID):
			t.Errorf("%s: operationId %q is not camelCase", key, op.OperationID)
		default:
			if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("%s and %s share operationId %q", prev, key, op.OperationID)
			}
			seen[op.OperationID] = key
		}

		if op.Summary == "" {
			t.Errorf("%s has no summary", key)
		}
		if len(op.Tags) == 0 {
			t.Errorf("%s has no tags, so it will not appear under any heading", key)
		}
		if len(op.Responses) == 0 {
			t.Errorf("%s documents no responses", key)
		}
	}
}

// TestOpenAPIDocumentHeader pins the parts of the document that tooling reads
// before anything else.
func TestOpenAPIDocumentHeader(t *testing.T) {
	spec := loadSpec(t)

	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Errorf("openapi version is %q, want 3.1.x", spec.OpenAPI)
	}
	if spec.Info.Title == "" || spec.Info.Version == "" {
		t.Errorf("info.title/info.version incomplete: %+v", spec.Info)
	}
	if len(spec.Components.Schemas) == 0 {
		t.Error("spec defines no component schemas")
	}

	// Both authentication mechanisms the API accepts must be described, or a
	// client author has no way to discover that API tokens exist.
	for _, name := range []string{"sessionCookie", "bearerToken"} {
		if _, ok := spec.Components.SecuritySchemes[name]; !ok {
			t.Errorf("securitySchemes is missing %q; have %v",
				name, schemeNames(spec))
		}
	}
}

func schemeNames(spec *openAPISpec) []string {
	names := make([]string, 0, len(spec.Components.SecuritySchemes))
	for name := range spec.Components.SecuritySchemes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
