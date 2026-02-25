/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1alpha1 "github.com/jeffvincent/kindling/api/v1alpha1"
	"github.com/jeffvincent/kindling/pkg/ci"
)

// ────────────────────────────────────────────────────────────────────────────
// DSE helper unit tests (pure functions – stdlib testing, no Ginkgo/envtest)
// ────────────────────────────────────────────────────────────────────────────

func TestLabelsForCR(t *testing.T) {
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app"},
	}
	labels := labelsForCR(cr)
	if len(labels) != 3 {
		t.Fatalf("expected 3 labels, got %d", len(labels))
	}
	expect := map[string]string{
		"app.kubernetes.io/name":       "my-app",
		"app.kubernetes.io/managed-by": "devstagingenvironment-operator",
		"app.kubernetes.io/instance":   "my-app",
	}
	for k, v := range expect {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
}

func TestBuildResourceRequirements_Full(t *testing.T) {
	cpuReq := resource.MustParse("100m")
	cpuLim := resource.MustParse("500m")
	memReq := resource.MustParse("128Mi")
	memLim := resource.MustParse("512Mi")

	res := buildResourceRequirements(&appsv1alpha1.ResourceRequirements{
		CPURequest:    &cpuReq,
		CPULimit:      &cpuLim,
		MemoryRequest: &memReq,
		MemoryLimit:   &memLim,
	})

	if !res.Requests[corev1.ResourceCPU].Equal(cpuReq) {
		t.Errorf("cpu request mismatch")
	}
	if !res.Limits[corev1.ResourceCPU].Equal(cpuLim) {
		t.Errorf("cpu limit mismatch")
	}
	if !res.Requests[corev1.ResourceMemory].Equal(memReq) {
		t.Errorf("memory request mismatch")
	}
	if !res.Limits[corev1.ResourceMemory].Equal(memLim) {
		t.Errorf("memory limit mismatch")
	}
}

func TestBuildResourceRequirements_Nil(t *testing.T) {
	res := buildResourceRequirements(&appsv1alpha1.ResourceRequirements{})
	if len(res.Requests) != 0 {
		t.Errorf("expected empty requests, got %v", res.Requests)
	}
	if len(res.Limits) != 0 {
		t.Errorf("expected empty limits, got %v", res.Limits)
	}
}

func TestBuildResourceRequirements_Partial(t *testing.T) {
	cpuReq := resource.MustParse("250m")
	res := buildResourceRequirements(&appsv1alpha1.ResourceRequirements{
		CPURequest: &cpuReq,
	})
	if len(res.Requests) != 1 {
		t.Errorf("expected 1 request, got %d", len(res.Requests))
	}
	if len(res.Limits) != 0 {
		t.Errorf("expected empty limits, got %v", res.Limits)
	}
}

func TestBuildHTTPProbe_DefaultPort(t *testing.T) {
	hc := &appsv1alpha1.HealthCheckSpec{Path: "/healthz"}
	probe := buildHTTPProbe(hc, 8080)

	if probe.HTTPGet.Path != "/healthz" {
		t.Errorf("path = %q, want /healthz", probe.HTTPGet.Path)
	}
	if probe.HTTPGet.Port.IntValue() != 8080 {
		t.Errorf("port = %d, want 8080", probe.HTTPGet.Port.IntValue())
	}
}

func TestBuildHTTPProbe_CustomPort(t *testing.T) {
	port := int32(9090)
	hc := &appsv1alpha1.HealthCheckSpec{Path: "/ready", Port: &port}
	probe := buildHTTPProbe(hc, 8080)

	if probe.HTTPGet.Port.IntValue() != 9090 {
		t.Errorf("port = %d, want 9090", probe.HTTPGet.Port.IntValue())
	}
}

func TestBuildHTTPProbe_Delays(t *testing.T) {
	delay := int32(30)
	period := int32(10)
	hc := &appsv1alpha1.HealthCheckSpec{
		Path:                "/",
		InitialDelaySeconds: &delay,
		PeriodSeconds:       &period,
	}
	probe := buildHTTPProbe(hc, 8080)

	if probe.InitialDelaySeconds != 30 {
		t.Errorf("InitialDelaySeconds = %d, want 30", probe.InitialDelaySeconds)
	}
	if probe.PeriodSeconds != 10 {
		t.Errorf("PeriodSeconds = %d, want 10", probe.PeriodSeconds)
	}
}

func TestBuildHTTPProbe_ZeroDelays(t *testing.T) {
	hc := &appsv1alpha1.HealthCheckSpec{Path: "/health"}
	probe := buildHTTPProbe(hc, 3000)

	if probe.InitialDelaySeconds != 0 {
		t.Errorf("InitialDelaySeconds = %d, want 0", probe.InitialDelaySeconds)
	}
	if probe.PeriodSeconds != 0 {
		t.Errorf("PeriodSeconds = %d, want 0", probe.PeriodSeconds)
	}
}

func TestBuildGRPCProbe_DefaultPort(t *testing.T) {
	hc := &appsv1alpha1.HealthCheckSpec{Type: "grpc"}
	probe := buildGRPCProbe(hc, 50051)

	if probe.GRPC == nil {
		t.Fatal("expected GRPC probe handler, got nil")
	}
	if probe.GRPC.Port != 50051 {
		t.Errorf("port = %d, want 50051", probe.GRPC.Port)
	}
}

func TestBuildGRPCProbe_CustomPort(t *testing.T) {
	port := int32(9555)
	hc := &appsv1alpha1.HealthCheckSpec{Type: "grpc", Port: &port}
	probe := buildGRPCProbe(hc, 50051)

	if probe.GRPC.Port != 9555 {
		t.Errorf("port = %d, want 9555", probe.GRPC.Port)
	}
}

func TestBuildGRPCProbe_Delays(t *testing.T) {
	delay := int32(15)
	period := int32(5)
	hc := &appsv1alpha1.HealthCheckSpec{
		Type:                "grpc",
		InitialDelaySeconds: &delay,
		PeriodSeconds:       &period,
	}
	probe := buildGRPCProbe(hc, 50051)

	if probe.InitialDelaySeconds != 15 {
		t.Errorf("InitialDelaySeconds = %d, want 15", probe.InitialDelaySeconds)
	}
	if probe.PeriodSeconds != 5 {
		t.Errorf("PeriodSeconds = %d, want 5", probe.PeriodSeconds)
	}
}

func TestBuildDependencyWaitInitContainers_Nil(t *testing.T) {
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       appsv1alpha1.DevStagingEnvironmentSpec{Dependencies: nil},
	}
	initC := buildDependencyWaitInitContainers(cr)
	if initC != nil {
		t.Errorf("expected nil, got %d containers", len(initC))
	}
}

func TestBuildDependencyWaitInitContainers_KnownDeps(t *testing.T) {
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec: appsv1alpha1.DevStagingEnvironmentSpec{
			Dependencies: []appsv1alpha1.DependencySpec{
				{Type: appsv1alpha1.DependencyPostgres},
				{Type: appsv1alpha1.DependencyRedis},
			},
		},
	}
	initC := buildDependencyWaitInitContainers(cr)
	if len(initC) != 2 {
		t.Fatalf("expected 2 init containers, got %d", len(initC))
	}
	if initC[0].Name != "wait-for-postgres" {
		t.Errorf("first container name = %q, want wait-for-postgres", initC[0].Name)
	}
	if initC[1].Name != "wait-for-redis" {
		t.Errorf("second container name = %q, want wait-for-redis", initC[1].Name)
	}
}

func TestBuildDependencyWaitInitContainers_BusyboxImage(t *testing.T) {
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec: appsv1alpha1.DevStagingEnvironmentSpec{
			Dependencies: []appsv1alpha1.DependencySpec{
				{Type: appsv1alpha1.DependencyPostgres},
			},
		},
	}
	initC := buildDependencyWaitInitContainers(cr)
	if initC[0].Image != "busybox:1.36" {
		t.Errorf("image = %q, want busybox:1.36", initC[0].Image)
	}
}

func TestBuildDependencyWaitInitContainers_DefaultPort(t *testing.T) {
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec: appsv1alpha1.DevStagingEnvironmentSpec{
			Dependencies: []appsv1alpha1.DependencySpec{
				{Type: appsv1alpha1.DependencyRedis},
			},
		},
	}
	initC := buildDependencyWaitInitContainers(cr)
	cmd := initC[0].Command[2]
	if !strings.Contains(cmd, "myapp-redis") {
		t.Errorf("command should reference myapp-redis, got %q", cmd)
	}
	if !strings.Contains(cmd, "6379") {
		t.Errorf("command should reference port 6379, got %q", cmd)
	}
}

func TestBuildDependencyWaitInitContainers_CustomPort(t *testing.T) {
	port := int32(16379)
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec: appsv1alpha1.DevStagingEnvironmentSpec{
			Dependencies: []appsv1alpha1.DependencySpec{
				{Type: appsv1alpha1.DependencyRedis, Port: &port},
			},
		},
	}
	initC := buildDependencyWaitInitContainers(cr)
	if !strings.Contains(initC[0].Command[2], "16379") {
		t.Errorf("command should reference port 16379, got %q", initC[0].Command[2])
	}
}

func TestBuildDependencyWaitInitContainers_SkipUnknown(t *testing.T) {
	cr := &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec: appsv1alpha1.DevStagingEnvironmentSpec{
			Dependencies: []appsv1alpha1.DependencySpec{
				{Type: "unknown-db"},
				{Type: appsv1alpha1.DependencyPostgres},
			},
		},
	}
	initC := buildDependencyWaitInitContainers(cr)
	if len(initC) != 1 {
		t.Fatalf("expected 1 init container (unknown skipped), got %d", len(initC))
	}
	if initC[0].Name != "wait-for-postgres" {
		t.Errorf("container name = %q, want wait-for-postgres", initC[0].Name)
	}
}

func TestGeneratePassword_Length(t *testing.T) {
	pw := generatePassword(16)
	if len(pw) != 16 {
		t.Errorf("len = %d, want 16", len(pw))
	}
}

func TestGeneratePassword_Uniqueness(t *testing.T) {
	a := generatePassword(32)
	b := generatePassword(32)
	if a == b {
		t.Error("two generated passwords should differ")
	}
}

func TestGeneratePassword_Charset(t *testing.T) {
	pw := generatePassword(200)
	for i, c := range pw {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			t.Errorf("character %d (%c) is not alphanumeric", i, c)
		}
	}
}

func TestGeneratePassword_ZeroLength(t *testing.T) {
	pw := generatePassword(0)
	if pw != "" {
		t.Errorf("expected empty string, got %q", pw)
	}
}

func TestEnvVarsToMap(t *testing.T) {
	envs := []corev1.EnvVar{
		{Name: "A", Value: "1"},
		{Name: "B", Value: "2"},
	}
	m := envVarsToMap(envs)
	if len(m) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m))
	}
	if m["A"] != "1" {
		t.Errorf("A = %q, want 1", m["A"])
	}
	if m["B"] != "2" {
		t.Errorf("B = %q, want 2", m["B"])
	}
}

func TestEnvVarsToMap_Nil(t *testing.T) {
	m := envVarsToMap(nil)
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Runner pool helper unit tests
// ────────────────────────────────────────────────────────────────────────────

func TestRunnerClusterRoleName(t *testing.T) {
	adapter := ci.Default().Runner()
	if got := adapter.ClusterRoleName("jeff"); got != "jeff-runner" {
		t.Errorf("ClusterRoleName = %q, want jeff-runner", got)
	}
}

func TestRunnerClusterRoleBindingName(t *testing.T) {
	adapter := ci.Default().Runner()
	if got := adapter.ClusterRoleBindingName("jeff"); got != "jeff-runner" {
		t.Errorf("ClusterRoleBindingName = %q, want jeff-runner", got)
	}
}

func TestComputeRunnerPoolHash_Stable(t *testing.T) {
	a := computeRunnerPoolHash(map[string]string{"x": "y"})
	b := computeRunnerPoolHash(map[string]string{"x": "y"})
	if a != b {
		t.Errorf("same input should give same hash: %q vs %q", a, b)
	}
}

func TestComputeRunnerPoolHash_Different(t *testing.T) {
	a := computeRunnerPoolHash(map[string]string{"x": "y"})
	b := computeRunnerPoolHash(map[string]string{"x": "z"})
	if a == b {
		t.Error("different input should give different hash")
	}
}

func TestComputeRunnerPoolHash_Format(t *testing.T) {
	h := computeRunnerPoolHash("test-input")
	if len(h) != 16 {
		t.Errorf("hash length = %d, want 16", len(h))
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(h) {
		t.Errorf("hash %q is not 16 hex chars", h)
	}
}

func TestInt64Ptr(t *testing.T) {
	p := int64Ptr(42)
	if p == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *p != 42 {
		t.Errorf("*p = %d, want 42", *p)
	}
}

func TestBuildRunnerResourceRequirements_Full(t *testing.T) {
	cpuReq := resource.MustParse("500m")
	cpuLim := resource.MustParse("2")
	memReq := resource.MustParse("1Gi")
	memLim := resource.MustParse("4Gi")

	res := buildRunnerResourceRequirements(&appsv1alpha1.RunnerResourceRequirements{
		CPURequest:    &cpuReq,
		CPULimit:      &cpuLim,
		MemoryRequest: &memReq,
		MemoryLimit:   &memLim,
	})

	if !res.Requests[corev1.ResourceCPU].Equal(cpuReq) {
		t.Errorf("cpu request mismatch")
	}
	if !res.Limits[corev1.ResourceCPU].Equal(cpuLim) {
		t.Errorf("cpu limit mismatch")
	}
	if !res.Requests[corev1.ResourceMemory].Equal(memReq) {
		t.Errorf("memory request mismatch")
	}
	if !res.Limits[corev1.ResourceMemory].Equal(memLim) {
		t.Errorf("memory limit mismatch")
	}
}

func TestBuildRunnerResourceRequirements_Nil(t *testing.T) {
	res := buildRunnerResourceRequirements(&appsv1alpha1.RunnerResourceRequirements{})
	if len(res.Requests) != 0 {
		t.Errorf("expected empty requests, got %v", res.Requests)
	}
	if len(res.Limits) != 0 {
		t.Errorf("expected empty limits, got %v", res.Limits)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Dependency registry coverage
// ────────────────────────────────────────────────────────────────────────────

func TestDependencyRegistry_AllTypes(t *testing.T) {
	expectedTypes := []appsv1alpha1.DependencyType{
		appsv1alpha1.DependencyPostgres,
		appsv1alpha1.DependencyRedis,
		appsv1alpha1.DependencyMySQL,
		appsv1alpha1.DependencyMongoDB,
		appsv1alpha1.DependencyRabbitMQ,
		appsv1alpha1.DependencyMinIO,
		appsv1alpha1.DependencyElasticsearch,
		appsv1alpha1.DependencyKafka,
		appsv1alpha1.DependencyNATS,
		appsv1alpha1.DependencyMemcached,
		appsv1alpha1.DependencyCassandra,
		appsv1alpha1.DependencyConsul,
		appsv1alpha1.DependencyVault,
		appsv1alpha1.DependencyInfluxDB,
		appsv1alpha1.DependencyJaeger,
	}
	for _, dt := range expectedTypes {
		if _, ok := dependencyRegistry[dt]; !ok {
			t.Errorf("missing registry entry for %s", dt)
		}
	}
}

func TestDependencyRegistry_Validity(t *testing.T) {
	for depType, defaults := range dependencyRegistry {
		if defaults.Image == "" {
			t.Errorf("empty image for %s", depType)
		}
		if defaults.Port <= 0 {
			t.Errorf("invalid port for %s: %d", depType, defaults.Port)
		}
		if defaults.EnvVarName == "" {
			t.Errorf("empty env var name for %s", depType)
		}
	}
}
