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

// DockerMode determines how runner pods access Docker for container builds.
// +kubebuilder:validation:Enum=socket;dind;none
type DockerMode string

const (
	// DockerModeSocket mounts the host Docker socket — lightest weight, best for Kind.
	DockerModeSocket DockerMode = "socket"
	// DockerModeDinD runs a Docker-in-Docker sidecar — fully isolated, heavier.
	DockerModeDinD DockerMode = "dind"
	// DockerModeNone disables Docker entirely — for non-build runners.
	DockerModeNone DockerMode = "none"
)

// GithubActionRunnerPoolSpec defines the desired state of GithubActionRunnerPool.
//
// Each GithubActionRunnerPool maps 1:1 to a developer. The operator and this CR
// live inside the developer's local Kind cluster. The runner pod polls GitHub for
// jobs triggered by the developer's pushes, builds the app container in-cluster,
// and spins up an ephemeral DevStagingEnvironment on the same cluster.
type GithubActionRunnerPoolSpec struct {
	// GitHubUsername is the GitHub handle of the developer who owns this runner pool.
	// The username is added as a runner label so the CI workflow can route jobs to the
	// correct developer's local Kind cluster using `runs-on: [self-hosted, <username>]`.
	//+kubebuilder:validation:MinLength=1
	GitHubUsername string `json:"githubUsername"`

	// Repository is the full GitHub repository slug (e.g. "myorg/myrepo").
	//+kubebuilder:validation:MinLength=1
	Repository string `json:"repository"`

	// GitHubURL is the base URL for GitHub API requests.
	// Defaults to "https://github.com" for github.com. Set this for GitHub Enterprise Server.
	//+kubebuilder:default="https://github.com"
	//+optional
	GitHubURL string `json:"githubURL,omitempty"`

	// TokenSecretRef is a reference to a Secret containing the GitHub PAT or App token
	// used to register self-hosted runners. The Secret must contain a key named "github-token".
	TokenSecretRef SecretKeyRef `json:"tokenSecretRef"`

	// Replicas is the number of runner pods in the pool.
	// Typically 1 for a per-developer local Kind cluster.
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// RunnerImage is the container image for the GitHub Actions runner.
	//+kubebuilder:default="ghcr.io/actions/actions-runner:latest"
	//+optional
	RunnerImage string `json:"runnerImage,omitempty"`

	// Labels are additional runner labels passed to the GitHub runner during registration.
	// The GitHubUsername is always appended automatically so workflows can target
	// this developer's cluster with `runs-on: [self-hosted, <username>]`.
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
	//+kubebuilder:default="/runner/_work"
	//+optional
	WorkDir string `json:"workDir,omitempty"`

	// DockerMode controls how the runner accesses Docker for building container images.
	//
	//   - "socket"  (default) — Mounts the host's /var/run/docker.sock into the runner pod.
	//                 Lightest weight, best layer caching, images land on the host Docker
	//                 (where `kind load` pulls from). No privileged sidecar needed.
	//   - "dind"    — Runs a Docker-in-Docker sidecar (docker:dind) as a privileged container.
	//                 Fully isolated but heavier and no layer cache between restarts.
	//   - "none"    — No Docker access. Use this if the runner only runs non-build jobs
	//                 or uses an external image build service.
	//
	//+kubebuilder:validation:Enum=socket;dind;none
	//+kubebuilder:default="socket"
	//+optional
	DockerMode DockerMode `json:"dockerMode,omitempty"`

	// VolumeMounts are additional volume mounts for the runner container.
	//+optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Volumes are additional volumes to attach to runner pods.
	//+optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`
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

// GithubActionRunnerPoolStatus defines the observed state of GithubActionRunnerPool.
type GithubActionRunnerPoolStatus struct {
	// Replicas is the desired number of runner replicas.
	Replicas int32 `json:"replicas,omitempty"`

	// ReadyRunners is the number of runner pods that are ready and polling for jobs.
	ReadyRunners int32 `json:"readyRunners,omitempty"`

	// RunnerRegistered indicates whether the runner has successfully registered with GitHub.
	RunnerRegistered bool `json:"runnerRegistered,omitempty"`

	// ActiveJob is the name/ID of the GitHub Actions workflow job currently being executed,
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

// GithubActionRunnerPool is the Schema for the githubactionrunnerpools API.
// It runs on a developer's local Kind cluster. The runner pod registers with GitHub
// as a self-hosted runner labelled with the developer's username, polls for CI jobs
// triggered by that developer's pushes, builds the app container image (via host
// Docker socket or optional DinD), and creates a DevStagingEnvironment CR to deploy it locally.
type GithubActionRunnerPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GithubActionRunnerPoolSpec   `json:"spec,omitempty"`
	Status GithubActionRunnerPoolStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GithubActionRunnerPoolList contains a list of GithubActionRunnerPool.
type GithubActionRunnerPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GithubActionRunnerPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GithubActionRunnerPool{}, &GithubActionRunnerPoolList{})
}
