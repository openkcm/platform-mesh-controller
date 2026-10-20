//go:build helmtests

package unit_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// apiExportName is the name of the APIExport, which the portal content refers to.
	apiExportName   = "operations.openkcm.io"
	contentForLabel = "ui.platform-mesh.io/content-for"
	publicHTTPS     = "publicScheme=https"
	publicHost      = "publicHost=openkcm-ui.portal.example.org"
)

// apiExport is the part of the APIExport that the tests check.
type apiExport struct {
	Metadata struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Resources []struct {
			Group  string `yaml:"group"`
			Name   string `yaml:"name"`
			Schema string `yaml:"schema"`
		} `yaml:"resources"`
	} `yaml:"spec"`
}

// schemaSpec is the spec of an APIResourceSchema, and the part of a CRD's spec it is made from.
type schemaSpec struct {
	Group string `yaml:"group"`
	Names struct {
		Kind   string `yaml:"kind"`
		Plural string `yaml:"plural"`
	} `yaml:"names"`
	Scope    string `yaml:"scope"`
	Versions []struct {
		Name   string         `yaml:"name"`
		Schema map[string]any `yaml:"schema"`
	} `yaml:"versions"`
}

type apiResourceSchema struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec schemaSpec `yaml:"spec"`
}

type contentConfiguration struct {
	Metadata struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		RemoteConfiguration struct {
			URL string `yaml:"url"`
		} `yaml:"remoteConfiguration"`
	} `yaml:"spec"`
}

// TestAPIExportSchemas checks that every resource of the APIExport has its APIResourceSchema,
// named after the chart version.
func TestAPIExportSchemas(t *testing.T) {
	version, _ := chartMeta(t, integrationChart)
	prefix := "v" + strings.ReplaceAll(version, ".", "-") + "-v1alpha1."

	output := render(t, integrationChart)
	export := object[apiExport](t, output, "APIExport")
	schemas := map[string]apiResourceSchema{}

	for _, s := range objects[apiResourceSchema](t, output, "APIResourceSchema") {
		schemas[s.Metadata.Name] = s
	}

	if export.Metadata.Name != apiExportName || export.Metadata.Labels[contentForLabel] != apiExportName {
		t.Errorf("APIExport %q with labels %v", export.Metadata.Name, export.Metadata.Labels)
	}

	if len(export.Spec.Resources) != len(schemas) {
		t.Errorf("%d resources in the APIExport, %d APIResourceSchemas", len(export.Spec.Resources), len(schemas))
	}

	for _, r := range export.Spec.Resources {
		want := prefix + r.Name + "." + r.Group
		schema, ok := schemas[r.Schema]

		if r.Schema != want || !ok || schema.Spec.Names.Plural != r.Name || schema.Spec.Group != r.Group {
			t.Errorf("resource %s.%s: schema %q (found %t), want %q", r.Name, r.Group, r.Schema, ok, want)
		}
	}
}

// TestSchemasMatchCRDs checks that the APIResourceSchemas are generated from the current CRDs
// (python3 hack/sync-api-resource-schemas.py after make manifests): the same group, names,
// scope and versions, with the OpenAPI schema of each version unwrapped from openAPIV3Schema.
func TestSchemasMatchCRDs(t *testing.T) {
	type spec = map[string]any

	crds := map[string]spec{}

	paths, err := filepath.Glob("../../config/crd/bases/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no generated CRDs: %v", err)
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		var crd struct {
			Spec spec `yaml:"spec"`
		}

		err = yaml.Unmarshal(data, &crd)
		if err != nil {
			t.Fatal(err)
		}

		versions, _ := crd.Spec["versions"].([]any)
		for _, v := range versions {
			version, _ := v.(spec)
			if schema, ok := version["schema"].(spec); ok {
				version["schema"] = schema["openAPIV3Schema"]
			}
		}

		names, _ := crd.Spec["names"].(spec)
		plural, _ := names["plural"].(string)
		crds[plural] = spec{"group": crd.Spec["group"], "names": names, "scope": crd.Spec["scope"], "versions": versions}
	}

	type rawSchema struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec spec `yaml:"spec"`
	}

	schemas := objects[rawSchema](t, render(t, integrationChart), "APIResourceSchema")

	if len(schemas) != len(crds) {
		t.Errorf("%d APIResourceSchemas, %d CRDs", len(schemas), len(crds))
	}

	for _, schema := range schemas {
		names, _ := schema.Spec["names"].(spec)
		plural, _ := names["plural"].(string)

		crd, ok := crds[plural]
		if !ok {
			t.Errorf("no CRD for the APIResourceSchema %s", schema.Metadata.Name)
			continue
		}

		if !reflect.DeepEqual(schema.Spec, crd) {
			t.Errorf("the APIResourceSchema of %s differs from its CRD: run hack/sync-api-resource-schemas.py", plural)
		}
	}
}

func TestContentConfiguration(t *testing.T) {
	configs := objects[contentConfiguration](t, render(t, integrationChart, set(publicHTTPS, publicHost)...),
		"ContentConfiguration")

	urls := make([]string, 0, len(configs))
	for _, c := range configs {
		urls = append(urls, c.Metadata.Name+"="+c.Spec.RemoteConfiguration.URL)

		if c.Metadata.Labels[contentForLabel] != apiExportName {
			t.Errorf("ContentConfiguration %s is not for operations.openkcm.io: %v", c.Metadata.Name, c.Metadata.Labels)
		}
	}

	want := []string{
		"openkcm=https://openkcm-ui.portal.example.org/pm-content.json",
		"openkcm-namespace=https://openkcm-ui.portal.example.org/pm-content-namespace.json",
	}
	if !slices.Equal(urls, want) {
		t.Errorf("content URLs %v, want %v", urls, want)
	}
}

func TestIntegrationRBAC(t *testing.T) {
	names := func(args ...string) []string {
		output := render(t, integrationChart, args...)

		var result []string
		for _, kind := range []string{"ClusterRole", "ClusterRoleBinding"} {
			for _, r := range objects[rbacRole](t, output, kind) {
				result = append(result, kind+"/"+r.Metadata.Name)
			}
		}

		return result
	}

	marketplace := []string{"ClusterRole/apiexport-bind", "ClusterRoleBinding/anonymous-view"}
	if got := names(); !slices.Equal(got, marketplace) {
		t.Errorf("RBAC %v, want %v", got, marketplace)
	}

	if got := names(set("rbac.enabled=true")...); len(got) != 4 ||
		!slices.Contains(got, "ClusterRole/syncagent-resources-access") {
		t.Errorf("RBAC with rbac.enabled: %v", got)
	}
}

func TestIntegrationReleaseNotes(t *testing.T) {
	got := warnings(releaseNotes(t, integrationChart))
	if len(got) != 1 || !strings.Contains(got[0], "only works for a local portal") {
		t.Errorf("warnings with the defaults: %q", got)
	}

	notes := releaseNotes(t, integrationChart, set(publicHTTPS, publicHost)...)
	if got := warnings(notes); len(got) != 0 {
		t.Errorf("warnings with a public host: %q", got)
	}

	if !strings.Contains(notes, "https://openkcm-ui.portal.example.org/pm-content.json") {
		t.Errorf("the notes lack the content URL:\n%s", notes)
	}
}
