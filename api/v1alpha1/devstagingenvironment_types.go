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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DeploymentSpec defines the desired state of the application Deployment.
type DeploymentSpec struct {
	// Replicas is the desired number of pod replicas.
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Image is the container image to run (e.g. "nginx:1.25").
	//+kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Port is the container port the application listens on.
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Command overrides the container entrypoint.
	//+optional
	Command []string `json:"command,omitempty"`

	// Args are arguments passed to the container entrypoint.
	//+optional
	Args []string `json:"args,omitempty"`

	// Env is a list of environment variables to set in the container.
	//+optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Resources defines CPU and memory requests/limits for the container.
	//+optional
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// HealthCheck configures liveness and readiness probes.
	//+optional
	HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`
}

// ResourceRequirements defines compute resource requests and limits.
type ResourceRequirements struct {
	// CPURequest is the requested CPU (e.g. "100m").
	//+optional
	CPURequest *resource.Quantity `json:"cpuRequest,omitempty"`

	// CPULimit is the maximum CPU (e.g. "500m").
	//+optional
	CPULimit *resource.Quantity `json:"cpuLimit,omitempty"`

	// MemoryRequest is the requested memory (e.g. "128Mi").
	//+optional
	MemoryRequest *resource.Quantity `json:"memoryRequest,omitempty"`

	// MemoryLimit is the maximum memory (e.g. "512Mi").
	//+optional
	MemoryLimit *resource.Quantity `json:"memoryLimit,omitempty"`
}

// HealthCheckSpec configures liveness and readiness probes.
type HealthCheckSpec struct {
	// Path is the HTTP path for the health check endpoint (e.g. "/healthz").
	//+kubebuilder:default="/healthz"
	Path string `json:"path,omitempty"`

	// Port overrides the probe port. Defaults to the container port.
	//+optional
	Port *int32 `json:"port,omitempty"`

	// InitialDelaySeconds is the delay before the first probe.
	//+kubebuilder:default=5
	InitialDelaySeconds *int32 `json:"initialDelaySeconds,omitempty"`

	// PeriodSeconds is how often to perform the probe.
	//+kubebuilder:default=10
	PeriodSeconds *int32 `json:"periodSeconds,omitempty"`
}

// ServiceSpec defines the desired state of the Service.
type ServiceSpec struct {
	// Port is the port the Service exposes.
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// TargetPort is the container port traffic is routed to. Defaults to the Deployment port.
	//+optional
	TargetPort *int32 `json:"targetPort,omitempty"`

	// Type is the Kubernetes Service type.
	//+kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	//+kubebuilder:default="ClusterIP"
	Type string `json:"type,omitempty"`
}

// IngressSpec defines the desired state of the Ingress.
type IngressSpec struct {
	// Enabled controls whether an Ingress resource is created.
	//+kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Host is the fully qualified domain name for the Ingress rule (e.g. "app.example.com").
	//+optional
	Host string `json:"host,omitempty"`

	// Path is the URL path prefix for the Ingress rule.
	//+kubebuilder:default="/"
	Path string `json:"path,omitempty"`

	// PathType determines how the path is matched.
	//+kubebuilder:validation:Enum=Prefix;Exact;ImplementationSpecific
	//+kubebuilder:default="Prefix"
	PathType string `json:"pathType,omitempty"`

	// IngressClassName is the name of the IngressClass to use (e.g. "nginx").
	//+optional
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// TLS configures TLS termination for the Ingress.
	//+optional
	TLS *IngressTLSSpec `json:"tls,omitempty"`

	// Annotations are additional annotations to set on the Ingress resource.
	//+optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IngressTLSSpec configures TLS for the Ingress.
type IngressTLSSpec struct {
	// SecretName is the name of the Kubernetes Secret containing the TLS certificate.
	SecretName string `json:"secretName"`

	// Hosts is the list of hosts covered by the TLS certificate.
	//+optional
	Hosts []string `json:"hosts,omitempty"`
}

// DependencyType represents a well-known service dependency.
// +kubebuilder:validation:Enum=postgres;redis;mysql;mongodb;rabbitmq;minio
type DependencyType string

const (
	DependencyPostgres DependencyType = "postgres"
	DependencyRedis    DependencyType = "redis"
	DependencyMySQL    DependencyType = "mysql"
	DependencyMongoDB  DependencyType = "mongodb"
	DependencyRabbitMQ DependencyType = "rabbitmq"
	DependencyMinIO    DependencyType = "minio"
)

// DependencySpec declares a supporting service (database, cache, queue, etc.)
// that the operator provisions alongside the main application.
type DependencySpec struct {
	// Type is the well-known dependency kind (e.g. "postgres", "redis").
	Type DependencyType `json:"type"`

	// Version is the image tag / version to deploy (e.g. "16", "7.2").
	// Each type has a sensible default if omitted.
	//+optional
	Version string `json:"version,omitempty"`

	// Image overrides the default container image for this dependency.
	// Use this when you need a custom or private image.
	//+optional
	Image string `json:"image,omitempty"`

	// Port overrides the default service port for this dependency.
	//+optional
	Port *int32 `json:"port,omitempty"`

	// Env provides extra environment variables for the dependency container.
	// These are merged with (and can override) the operator's defaults.
	//+optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvVarName overrides the name of the connection-string env var
	// injected into the app container (e.g. "MY_DB_URL" instead of "DATABASE_URL").
	//+optional
	EnvVarName string `json:"envVarName,omitempty"`

	// StorageSize is the PVC size for stateful dependencies (default "1Gi").
	//+optional
	StorageSize *resource.Quantity `json:"storageSize,omitempty"`

	// Resources defines CPU/memory requests and limits for the dependency container.
	//+optional
	Resources *ResourceRequirements `json:"resources,omitempty"`
}

// DevStagingEnvironmentSpec defines the desired state of DevStagingEnvironment
type DevStagingEnvironmentSpec struct {
	// Deployment configures the application Deployment.
	Deployment DeploymentSpec `json:"deployment"`

	// Service configures the Service fronting the Deployment.
	Service ServiceSpec `json:"service"`

	// Ingress configures external access via an Ingress resource.
	//+optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// Dependencies declares supporting services (databases, caches, queues)
	// that the operator will provision alongside the application.
	// Connection env vars are automatically injected into the app container.
	//+optional
	Dependencies []DependencySpec `json:"dependencies,omitempty"`
}

// DevStagingEnvironmentStatus defines the observed state of DevStagingEnvironment
type DevStagingEnvironmentStatus struct {
	// AvailableReplicas is the number of ready pods.
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

	// DeploymentReady indicates whether the Deployment has reached the desired state.
	DeploymentReady bool `json:"deploymentReady,omitempty"`

	// ServiceReady indicates whether the Service is created.
	ServiceReady bool `json:"serviceReady,omitempty"`

	// IngressReady indicates whether the Ingress is created (if enabled).
	IngressReady bool `json:"ingressReady,omitempty"`

	// DependenciesReady indicates whether all declared dependencies are running.
	DependenciesReady bool `json:"dependenciesReady,omitempty"`

	// URL is the externally reachable URL if Ingress is configured.
	//+optional
	URL string `json:"url,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	//+optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.deployment.image`
//+kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.deployment.replicas`
//+kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.availableReplicas`
//+kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.deploymentReady`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DevStagingEnvironment is the Schema for the devstagingenvironments API
type DevStagingEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DevStagingEnvironmentSpec   `json:"spec,omitempty"`
	Status DevStagingEnvironmentStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DevStagingEnvironmentList contains a list of DevStagingEnvironment
type DevStagingEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DevStagingEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DevStagingEnvironment{}, &DevStagingEnvironmentList{})
}
