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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CIRunnerPoolSpec defines the desired state of CIRunnerPool.
//
// Each CIRunnerPool maps 1:1 to a developer. The operator and this CR
// live inside the developer's local Kind cluster. The runner pod polls the
// CI platform for jobs triggered by the developer's pushes. Container images
// are built in-cluster using Kaniko (no Docker daemon required) and pushed to
// an in-cluster registry.
type CIRunnerPoolSpec struct {
	// GitHubUsername is the CI platform username of the developer who owns this runner pool.
	// The username is added as a runner label so the CI workflow can route jobs to the
	// correct developer's local Kind cluster.
	//+kubebuilder:validation:MinLength=1
	GitHubUsername string `json:"githubUsername"`

	// Repository is the full repository slug (e.g. "myorg/myrepo" or "group/project").
	//+kubebuilder:validation:MinLength=1
	Repository string `json:"repository"`

	// GitHubURL is the base URL for CI platform API requests.
	// Defaults to "https://github.com" for github.com. Set to your platform's URL
	// for self-hosted instances (GitHub Enterprise, self-managed GitLab, etc.).
	//+kubebuilder:default="https://github.com"
	//+optional
	GitHubURL string `json:"githubURL,omitempty"`

	// TokenSecretRef is a reference to a Secret containing the CI platform token
	// used to register self-hosted runners.
	TokenSecretRef SecretKeyRef `json:"tokenSecretRef"`

	// Replicas is the number of runner pods in the pool.
	// Typically 1 for a per-developer local Kind cluster.
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// RunnerImage is the container image for the CI runner.
	//+kubebuilder:default="ghcr.io/actions/actions-runner:latest"
	//+optional
	RunnerImage string `json:"runnerImage,omitempty"`

	// Labels are additional runner labels passed to the CI runner during registration.
	// The username is always appended automatically so workflows can target
	// this developer's cluster.
	//+optional
	Labels []string `json:"labels,omitempty"`

	// RunnerGroup is the runner group to register the runner into.
	// Defaults to "Default".
	//+optional
	RunnerGroup string `json:"runnerGroup,omitempty"`

	// Resources defines CPU and memory requests/limits for each runner pod.
	//+optional
	Resources *RunnerResourceRequirements `json:"resources,omitempty"`

	// Env is a list of extra environment variables to set in the runner container.
	//+optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ServiceAccountName is the Kubernetes service account for runner pods.
	// This account should have permissions to create DevStagingEnvironment CRs
	// so the runner job can spin up ephemeral dev environments.
	//+optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// WorkDir is the working directory mount path inside the runner container.
	// If empty, the controller uses the CI provider's default:
	//   GitHub Actions: /home/runner/_work
	//   GitLab:         /builds
	//+optional
	WorkDir string `json:"workDir,omitempty"`

	// VolumeMounts are additional volume mounts for the runner container.
	//+optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Volumes are additional volumes to attach to runner pods.
	//+optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// CIProvider is the CI platform name ("github", "gitlab").
	// The controller uses this to select the correct runner adapter, startup
	// script, environment variables, and token exchange logic.
	// Defaults to "github" when empty.
	//+kubebuilder:validation:Enum=github;gitlab;""
	//+optional
	CIProvider string `json:"ciProvider,omitempty"`
}

// SecretKeyRef references a key within a Secret.
type SecretKeyRef struct {
	// Name is the name of the Secret.
	Name string `json:"name"`

	// Key is the key within the Secret data.
	//+kubebuilder:default="github-token"
	Key string `json:"key,omitempty"`
}

// RunnerResourceRequirements defines compute resource requests and limits for runner pods.
type RunnerResourceRequirements struct {
	// CPURequest is the requested CPU (e.g. "500m").
	//+optional
	CPURequest *resource.Quantity `json:"cpuRequest,omitempty"`

	// CPULimit is the maximum CPU (e.g. "2").
	//+optional
	CPULimit *resource.Quantity `json:"cpuLimit,omitempty"`

	// MemoryRequest is the requested memory (e.g. "512Mi").
	//+optional
	MemoryRequest *resource.Quantity `json:"memoryRequest,omitempty"`

	// MemoryLimit is the maximum memory (e.g. "4Gi").
	//+optional
	MemoryLimit *resource.Quantity `json:"memoryLimit,omitempty"`
}

// CIRunnerPoolStatus defines the observed state of CIRunnerPool.
type CIRunnerPoolStatus struct {
	// Replicas is the desired number of runner replicas.
	Replicas int32 `json:"replicas,omitempty"`

	// ReadyRunners is the number of runner pods that are ready and polling for jobs.
	ReadyRunners int32 `json:"readyRunners,omitempty"`

	// RunnerRegistered indicates whether the runner has successfully registered with the CI platform.
	RunnerRegistered bool `json:"runnerRegistered,omitempty"`

	// ActiveJob is the name/ID of the CI job currently being executed,
	// or empty if the runner is idle and waiting.
	//+optional
	ActiveJob string `json:"activeJob,omitempty"`

	// LastJobCompleted is the timestamp of the most recent completed job.
	//+optional
	LastJobCompleted *metav1.Time `json:"lastJobCompleted,omitempty"`

	// DevEnvironmentRef is the name of the DevStagingEnvironment CR created by the last job.
	//+optional
	DevEnvironmentRef string `json:"devEnvironmentRef,omitempty"`

	// Conditions represent the latest available observations of the runner pool's state.
	//+optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="User",type=string,JSONPath=`.spec.githubUsername`
//+kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repository`
//+kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
//+kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyRunners`
//+kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.activeJob`,priority=1
//+kubebuilder:printcolumn:name="DevEnv",type=string,JSONPath=`.status.devEnvironmentRef`,priority=1
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CIRunnerPool is the Schema for the cirunnerpools API.
// It runs on a developer's local Kind cluster. The runner pod registers with
// the configured CI platform as a self-hosted runner labelled with the developer's
// username, polls for CI jobs triggered by that developer's pushes, and uses
// Kaniko + an in-cluster registry to build container images without requiring
// a Docker daemon.
type CIRunnerPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CIRunnerPoolSpec   `json:"spec,omitempty"`
	Status CIRunnerPoolStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CIRunnerPoolList contains a list of CIRunnerPool.
type CIRunnerPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CIRunnerPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CIRunnerPool{}, &CIRunnerPoolList{})
}
