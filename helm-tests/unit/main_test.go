//go:build helmtests

// Package unit_test renders the charts of this repository (charts/platform-mesh-controller and
// charts/pm-integration) with `helm template` and checks the output; it runs the controller
// binary to check the arguments that the chart passes. It needs the helm command, but no
// cluster. Run from the repository root:
//
//	make helm-test
package unit_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	controllerChart  = "../../charts/platform-mesh-controller"
	integrationChart = "../../charts/pm-integration"
	releaseName      = "pmc"
	namespace        = "platform-mesh-controller-system"
)

// TestMain renders the charts into the namespace of the tests, whatever the current
// kubeconfig context is.
func TestMain(m *testing.M) {
	err := os.Setenv("HELM_NAMESPACE", namespace)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// runHelm runs helm with the arguments and returns its output.
func runHelm(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "helm", args...) //nolint:gosec // arguments come from the tests

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// render runs `helm template` for the chart with the given --set/-f arguments and returns the
// output.
func render(t *testing.T, chart string, args ...string) string {
	t.Helper()

	out, err := runHelm(t, append([]string{"template", releaseName, chart}, args...)...)
	if err != nil {
		t.Fatalf("helm template failed: %v", err)
	}

	return out
}

// set returns the --set arguments of helm for the values, which have the form key=value.
func set(values ...string) []string {
	args := make([]string, 0, 2*len(values))
	for _, value := range values {
		args = append(args, "--set", value)
	}

	return args
}

// releaseNotes renders templates/NOTES.txt of the chart with the arguments, without a cluster.
// `helm install --dry-run=client` needs a reachable cluster with Helm 3, and `helm template`
// does not render the notes, so the test renders a copy of the chart in which NOTES.txt is a
// named template that a test-only ConfigMap writes out. The notes get the same values and
// helpers as in an install.
func releaseNotes(t *testing.T, chart string, args ...string) string {
	t.Helper()

	chartCopy := filepath.Join(t.TempDir(), filepath.Base(chart))

	err := os.CopyFS(chartCopy, os.DirFS(chart))
	if err != nil {
		t.Fatal(err)
	}

	templates := filepath.Join(chartCopy, "templates")

	notes, err := os.ReadFile(filepath.Join(templates, "NOTES.txt"))
	if err == nil {
		err = os.Remove(filepath.Join(templates, "NOTES.txt"))
	}

	if err != nil {
		t.Fatal(err)
	}

	for name, content := range map[string]string{
		"_notes_under_test.tpl": "{{- define \"notesUnderTest\" -}}\n" + string(notes) + "\n{{- end -}}\n",
		"notes-under-test.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: notes-under-test\n" +
			"data:\n  notes: {{ include \"notesUnderTest\" . | toJson }}\n",
	} {
		err = os.WriteFile(filepath.Join(templates, name), []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	out, err := runHelm(t, append([]string{"template", releaseName, chartCopy,
		"--show-only", "templates/notes-under-test.yaml"}, args...)...)
	if err != nil {
		t.Fatalf("rendering the release notes failed: %v", err)
	}

	var cm struct {
		Data struct {
			Notes string `yaml:"notes"`
		} `yaml:"data"`
	}

	err = yaml.Unmarshal([]byte(out), &cm)
	if err != nil || cm.Data.Notes == "" {
		t.Fatalf("no release notes in the output (%v):\n%s", err, out)
	}

	return cm.Data.Notes
}

// warnings returns the warnings of the release notes, one per line.
func warnings(notes string) []string {
	_, text, found := strings.Cut(notes, "WARNINGS:\n")
	if !found {
		return nil
	}

	text, _, _ = strings.Cut(text, "\n\n")

	var result []string
	for line := range strings.SplitSeq(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "  - "); ok {
			result = append(result, rest)
		}
	}

	return result
}

// objects decodes every rendered document of the given kind into a value of type T and
// returns them.
func objects[T any](t *testing.T, output, kind string) []T {
	t.Helper()

	var result []T

	dec := yaml.NewDecoder(strings.NewReader(output))

	for {
		var doc yaml.Node

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("decode the rendered chart: %v", err)
		}

		var header struct {
			Kind string `yaml:"kind"`
		}

		err = doc.Decode(&header)
		if err != nil {
			t.Fatalf("decode the kind: %v", err)
		}

		if header.Kind != kind {
			continue
		}

		var obj T

		err = doc.Decode(&obj)
		if err != nil {
			t.Fatalf("decode %s: %v", kind, err)
		}

		result = append(result, obj)
	}

	return result
}

// object decodes the only rendered document of the given kind.
func object[T any](t *testing.T, output, kind string) T {
	t.Helper()

	objs := objects[T](t, output, kind)
	if len(objs) != 1 {
		t.Fatalf("want one %s in the rendered chart, got %d", kind, len(objs))
	}

	return objs[0]
}

// chartMeta returns the version and appVersion of the chart.
func chartMeta(t *testing.T, chart string) (string, string) {
	t.Helper()

	data, err := os.ReadFile(chart + "/Chart.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var meta struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}

	err = yaml.Unmarshal(data, &meta)
	if err != nil || meta.Version == "" || meta.AppVersion == "" {
		t.Fatalf("Chart.yaml of %s has no version or appVersion: %v", chart, err)
	}

	return meta.Version, meta.AppVersion
}

// writeValues writes a values file to the test's temporary directory and returns its path.
func writeValues(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "values.yaml")

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// valuesComment returns the commented example of the chart's values.yaml below the line that
// ends with marker: the following comment lines that are indented by at least three spaces
// after the comment sign, without the comment sign and these three spaces.
func valuesComment(t *testing.T, chart, marker string) string {
	t.Helper()

	values, err := os.ReadFile(chart + "/values.yaml")
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(string(values), "\n")

	var example []string

	for i, line := range lines {
		if !strings.HasSuffix(line, marker) {
			continue
		}

		before, _, found := strings.Cut(line, "#")
		if !found {
			t.Fatalf("%q is not in a comment of values.yaml", marker)
		}

		for _, next := range lines[i+1:] {
			text, ok := strings.CutPrefix(next, before+"#   ")
			if !ok {
				break
			}

			example = append(example, text)
		}
	}

	if len(example) == 0 {
		t.Fatalf("no example below %q in values.yaml of %s", marker, chart)
	}

	return strings.Join(example, "\n") + "\n"
}

// indent prefixes every non-empty line of text with prefix.
func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}

	return strings.Join(lines, "\n") + "\n"
}
