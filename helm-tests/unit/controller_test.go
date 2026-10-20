//go:build helmtests

package unit_test

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	// kryptonAddr sets the address of Krypton; openKCMURL a real OpenKCM API.
	kryptonAddr = "krypton.grpcAddr=krypton-root.services.svc:8080"
	openKCMURL  = "openkcmAPI.url=https://openkcm.example.org"
)

// deployment is the part of the controller's Deployment that the tests check.
type deployment struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Replicas int `yaml:"replicas"`
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"selector"`
		Template struct {
			Spec struct {
				AutomountServiceAccountToken *bool `yaml:"automountServiceAccountToken"`
				EnableServiceLinks           *bool `yaml:"enableServiceLinks"`
				SecurityContext              struct {
					RunAsNonRoot   bool  `yaml:"runAsNonRoot"`
					RunAsUser      int64 `yaml:"runAsUser"`
					SeccompProfile struct {
						Type string `yaml:"type"`
					} `yaml:"seccompProfile"`
				} `yaml:"securityContext"`
				Containers []struct {
					Name            string   `yaml:"name"`
					Image           string   `yaml:"image"`
					Args            []string `yaml:"args"`
					SecurityContext struct {
						AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
						ReadOnlyRootFilesystem   bool  `yaml:"readOnlyRootFilesystem"`
						Capabilities             struct {
							Drop []string `yaml:"drop"`
						} `yaml:"capabilities"`
					} `yaml:"securityContext"`
					Env            []envVar `yaml:"env"`
					LivenessProbe  *probe   `yaml:"livenessProbe"`
					ReadinessProbe *probe   `yaml:"readinessProbe"`
					VolumeMounts   []struct {
						Name      string `yaml:"name"`
						MountPath string `yaml:"mountPath"`
					} `yaml:"volumeMounts"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type envVar struct {
	Name string `yaml:"name"`
}

type probe struct {
	HTTPGet struct {
		Path string `yaml:"path"`
		Port string `yaml:"port"`
	} `yaml:"httpGet"`
}

// rbacRole is a ClusterRole or Role.
type rbacRole struct {
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []rbacRule `yaml:"rules"`
}

type rbacRule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

// allows reports whether the role grants the verb on the resource of the API group.
func (r rbacRole) allows(group, resource, verb string) bool {
	return slices.ContainsFunc(r.Rules, func(rule rbacRule) bool {
		return slices.Contains(rule.APIGroups, group) && slices.Contains(rule.Resources, resource) &&
			slices.Contains(rule.Verbs, verb)
	})
}

var (
	controllerFlagsOnce sync.Once
	controllerFlagNames []string
	controllerFlagsErr  string
)

// controllerFlags builds the controller binary (cmd/platform-mesh-controller) and returns the
// names of its command-line flags, as `-h` lists them.
func controllerFlags(t *testing.T) []string {
	t.Helper()

	controllerFlagsOnce.Do(func() {
		dir, err := os.MkdirTemp("", "platform-mesh-controller-helm-test")
		if err != nil {
			controllerFlagsErr = err.Error()
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()

		binary := filepath.Join(dir, "manager")

		build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/platform-mesh-controller")
		if out, err := build.CombinedOutput(); err != nil {
			controllerFlagsErr = "go build: " + err.Error() + "\n" + string(out)
			return
		}

		// The flag package prints the usage to standard error and exits with 0 for -h.
		out, _ := exec.CommandContext(t.Context(), binary, "-h").CombinedOutput() //nolint:gosec // built above

		for _, match := range regexp.MustCompile(`(?m)^\s+-([a-z0-9-]+)`).FindAllStringSubmatch(string(out), -1) {
			controllerFlagNames = append(controllerFlagNames, match[1])
		}
	})

	if controllerFlagsErr != "" {
		t.Fatal(controllerFlagsErr)
	}

	if len(controllerFlagNames) == 0 {
		t.Fatal("the controller lists no flags")
	}

	return controllerFlagNames
}

// controller renders the controller chart and returns its Deployment.
func controller(t *testing.T, args ...string) deployment {
	t.Helper()

	d := object[deployment](t, render(t, controllerChart, args...), "Deployment")
	if len(d.Spec.Template.Spec.Containers) == 0 || d.Spec.Template.Spec.Containers[0].Name != "manager" {
		t.Fatalf("the first container is not the manager: %+v", d.Spec.Template.Spec.Containers)
	}

	return d
}

// TestControllerFlags checks that the controller knows every argument that the chart passes,
// with the defaults, with Krypton, and with the extraArgs example of values.yaml.
func TestControllerFlags(t *testing.T) {
	flags := controllerFlags(t)

	extraArgs := "extraArgs:\n" + indent(valuesComment(t, controllerChart, "# Example:"), "  ")

	for name, args := range map[string][]string{
		"defaults":          nil,
		"Krypton":           set(kryptonAddr),
		"extraArgs example": {"-f", writeValues(t, extraArgs)},
	} {
		t.Run(name, func(t *testing.T) {
			for _, arg := range controller(t, args...).Spec.Template.Spec.Containers[0].Args {
				flag, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
				if !slices.Contains(flags, flag) {
					t.Errorf("the controller has no flag %q (argument %q); its flags: %v", flag, arg, flags)
				}
			}
		})
	}

	// The flags documented in values.yaml exist.
	values, err := os.ReadFile(controllerChart + "/values.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, match := range regexp.MustCompile(`(?m)^#\s+--([a-z0-9-]+)=`).FindAllStringSubmatch(string(values), -1) {
		if !slices.Contains(flags, match[1]) {
			t.Errorf("values.yaml documents the flag --%s, which the controller does not have", match[1])
		}
	}
}

func TestControllerImage(t *testing.T) {
	const repository = "ghcr.io/openkcm/platform-mesh-controller/images/platform-mesh-controller"

	_, appVersion := chartMeta(t, controllerChart)

	tests := []struct {
		args  []string
		image string
	}{
		{image: repository + ":v" + appVersion},
		{args: set("image.tag="), image: repository + ":" + appVersion},
		{
			args:  set("image.digest=sha256:0123456789abcdef"),
			image: repository + ":v" + appVersion + "@sha256:0123456789abcdef",
		},
	}

	for _, tt := range tests {
		if image := controller(t, tt.args...).Spec.Template.Spec.Containers[0].Image; image != tt.image {
			t.Errorf("with %v: image %q, want %q", tt.args, image, tt.image)
		}
	}
}

func TestControllerSecurityDefaults(t *testing.T) {
	d := controller(t)
	pod := d.Spec.Template.Spec
	c := pod.Containers[0]

	if !pod.SecurityContext.RunAsNonRoot || pod.SecurityContext.RunAsUser != 65532 ||
		pod.SecurityContext.SeccompProfile.Type != "RuntimeDefault" {
		t.Errorf("pod security context: %+v", pod.SecurityContext)
	}

	sc := c.SecurityContext
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation || !sc.ReadOnlyRootFilesystem ||
		!slices.Equal(sc.Capabilities.Drop, []string{"ALL"}) {
		t.Errorf("container security context: %+v", sc)
	}

	// The mock API keeps its state in a ConfigMap of the pod's namespace.
	if pod.AutomountServiceAccountToken == nil || !*pod.AutomountServiceAccountToken ||
		!slices.ContainsFunc(c.Env, func(e envVar) bool { return e.Name == "POD_NAMESPACE" }) {
		t.Error("the mock API has no ServiceAccount token or no POD_NAMESPACE")
	}

	if pod.EnableServiceLinks == nil || *pod.EnableServiceLinks {
		t.Error("Service environment variables are enabled")
	}

	mounts := map[string]string{}
	for _, m := range c.VolumeMounts {
		mounts[m.Name] = m.MountPath
	}

	if want := map[string]string{"kcp-kubeconfig": "/etc/kcp", "tmp": "/tmp"}; !maps.Equal(mounts, want) {
		t.Errorf("volume mounts %v, want %v", mounts, want)
	}

	if !slices.Contains(c.Args, "--kcp-kubeconfig=/etc/kcp/kubeconfig") {
		t.Errorf("args %v do not read the kubeconfig of the mounted Secret", c.Args)
	}
}

func TestControllerProbes(t *testing.T) {
	c := controller(t).Spec.Template.Spec.Containers[0]
	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet.Path != "/healthz" ||
		c.LivenessProbe.HTTPGet.Port != "health" || c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Errorf("liveness %+v, readiness %+v", c.LivenessProbe, c.ReadinessProbe)
	}

	c = controller(t, set("livenessProbe.enabled=false")...).Spec.Template.Spec.Containers[0]
	if c.LivenessProbe != nil {
		t.Error("livenessProbe.enabled=false renders a liveness probe")
	}
}

// TestControllerRBAC checks that the ClusterRole covers the resources that the controller
// reconciles, and the Role the mock API's state ConfigMap in the pod's namespace.
func TestControllerRBAC(t *testing.T) {
	output := render(t, controllerChart)

	clusterRole := object[rbacRole](t, output, "ClusterRole")
	for _, resource := range []string{
		"tenants", "domainkeys", "servicekeys", "awsrootkeys", "azurerootkeys", "openbaorootkeys", "dataencryptionkeys",
	} {
		for _, check := range []struct{ resource, verb string }{
			{resource, "watch"}, {resource, "update"}, {resource + "/status", "patch"}, {resource + "/finalizers", "update"},
		} {
			if !clusterRole.allows("operations.openkcm.io", check.resource, check.verb) {
				t.Errorf("the ClusterRole does not grant %s on %s", check.verb, check.resource)
			}
		}
	}

	role := object[rbacRole](t, output, "Role")
	if role.Metadata.Namespace != namespace || !role.allows("", "configmaps", "update") ||
		!role.allows("coordination.k8s.io", "leases", "update") {
		t.Errorf("Role %+v, want configmaps and leases in %s", role, namespace)
	}

	output = render(t, controllerChart, set("global.rbac.create=false")...)
	if len(objects[rbacRole](t, output, "ClusterRole"))+len(objects[rbacRole](t, output, "Role")) != 0 {
		t.Error("global.rbac.create=false renders roles")
	}
}

// TestControllerCRDs checks that the CRDs of the chart are the generated ones (make manifests).
func TestControllerCRDs(t *testing.T) {
	generated, err := filepath.Glob("../../config/crd/bases/*.yaml")
	if err != nil || len(generated) == 0 {
		t.Fatalf("no generated CRDs: %v", err)
	}

	inChart, err := filepath.Glob(controllerChart + "/crds/*.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if len(inChart) != len(generated) {
		t.Errorf("%d CRDs in the chart, %d generated", len(inChart), len(generated))
	}

	for _, path := range generated {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(filepath.Join(controllerChart, "crds", filepath.Base(path)))
		if err != nil || string(got) != string(want) {
			t.Errorf("crds/%s differs from config/crd/bases: run make manifests", filepath.Base(path))
		}
	}
}

// TestControllerNamesAndSelectors checks the names and selector labels of chart 0.10.9: a
// Deployment's selector cannot change on upgrade.
func TestControllerNamesAndSelectors(t *testing.T) {
	d := controller(t)
	want := map[string]string{
		"app.kubernetes.io/name":      "platform-mesh-controller",
		"app.kubernetes.io/instance":  releaseName,
		"app.kubernetes.io/component": "controller",
	}

	if d.Metadata.Name != releaseName+"-platform-mesh-controller" || !maps.Equal(d.Spec.Selector.MatchLabels, want) {
		t.Errorf("Deployment %q with selector %v", d.Metadata.Name, d.Spec.Selector.MatchLabels)
	}
}

func TestControllerReleaseNotes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		warnings []string
	}{
		{name: "defaults", warnings: []string{"all reconcilers use the mock"}},
		{
			name: "Krypton and OpenKCM",
			args: set(kryptonAddr, openKCMURL),
		},
		{
			name:     "Krypton only",
			args:     set(kryptonAddr),
			warnings: []string{"the root key reconcilers (AWS, Azure, OpenBao) do not reach OpenKCM"},
		},
		{
			name:     "two replicas",
			args:     set(openKCMURL, "replicaCount=2"),
			warnings: []string{"does not enable leader election"},
		},
		{
			name:     "disruption budget with one replica",
			args:     set(openKCMURL, "podDisruptionBudget.enabled=true"),
			warnings: []string{"node drains are blocked"},
		},
		{
			name:     "no ServiceAccount token",
			args:     set(openKCMURL, "serviceAccount.automountServiceAccountToken=false"),
			warnings: []string{"loses it on restart"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notes := releaseNotes(t, controllerChart, tt.args...)
			got := warnings(notes)

			if len(got) != len(tt.warnings) {
				t.Fatalf("warnings %q, want %d:\n%s", got, len(tt.warnings), notes)
			}

			for i, want := range tt.warnings {
				if !strings.Contains(got[i], want) {
					t.Errorf("warning %q, want %q", got[i], want)
				}
			}
		})
	}
}
