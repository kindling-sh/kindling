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
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/jeffvincent/kindling/api/v1alpha1"
)

// DevStagingEnvironmentReconciler reconciles a DevStagingEnvironment object
type DevStagingEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const specHashAnnotation = "apps.example.com/spec-hash"

//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

// Reconcile reads the state of the cluster for a DevStagingEnvironment object and makes changes
// to bring the cluster state closer to the desired state defined in the CR spec.
func (r *DevStagingEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// ── Step 1: Fetch the CR (the filled-in shopping list) ─────────────
	cr := &appsv1alpha1.DevStagingEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if errors.IsNotFound(err) {
			// CR was deleted — child objects are garbage-collected via OwnerReferences
			logger.Info("DevStagingEnvironment resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ── Step 2: Reconcile the Deployment ───────────────────────────────
	if err := r.reconcileDeployment(ctx, cr); err != nil {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "DeploymentReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 3: Reconcile the Service ──────────────────────────────────
	if err := r.reconcileService(ctx, cr); err != nil {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "ServiceReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 4: Reconcile the Ingress (if enabled) ─────────────────────
	if err := r.reconcileIngress(ctx, cr); err != nil {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "IngressReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 5: Reconcile Dependencies (databases, caches, etc.) ──────
	if err := r.reconcileDependencies(ctx, cr); err != nil {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "DependenciesReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 6: Update status ──────────────────────────────────────────
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliation complete")
	return ctrl.Result{}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Deployment
// ────────────────────────────────────────────────────────────────────────────

func (r *DevStagingEnvironmentReconciler) reconcileDeployment(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment) error {
	logger := log.FromContext(ctx)
	desired := r.buildDeployment(cr)

	// Set the CR as the owner so garbage collection cleans up if the CR is deleted
	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Deployment", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	// Only update if our desired spec actually changed (compare hash annotations)
	desiredHash := desired.Annotations[specHashAnnotation]
	existingHash := existing.Annotations[specHashAnnotation]
	if desiredHash == existingHash {
		logger.V(1).Info("Deployment already up to date, skipping", "name", desired.Name)
		return nil
	}

	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[specHashAnnotation] = desiredHash
	logger.Info("Updating Deployment", "name", desired.Name)
	return r.Update(ctx, existing)
}

func (r *DevStagingEnvironmentReconciler) buildDeployment(cr *appsv1alpha1.DevStagingEnvironment) *appsv1.Deployment {
	labels := labelsForCR(cr)
	spec := cr.Spec.Deployment

	// Merge user-provided env vars with auto-injected dependency connection strings
	allEnv := append([]corev1.EnvVar{}, spec.Env...)
	for _, dep := range cr.Spec.Dependencies {
		allEnv = append(allEnv, buildDependencyConnectionEnvVars(cr.Name, dep)...)
	}

	container := corev1.Container{
		Name:    cr.Name,
		Image:   spec.Image,
		Command: spec.Command,
		Args:    spec.Args,
		Env:     allEnv,
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: spec.Port,
			Protocol:      corev1.ProtocolTCP,
		}},
	}

	// Wire up resource requests/limits if specified
	if spec.Resources != nil {
		container.Resources = buildResourceRequirements(spec.Resources)
	}

	// Wire up health checks if specified
	if spec.HealthCheck != nil {
		probe := buildHTTPProbe(spec.HealthCheck, spec.Port)
		container.LivenessProbe = probe.DeepCopy()
		container.ReadinessProbe = probe.DeepCopy()
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				specHashAnnotation: computeSpecHash(cr.Spec.Deployment),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Service
// ────────────────────────────────────────────────────────────────────────────

func (r *DevStagingEnvironmentReconciler) reconcileService(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment) error {
	logger := log.FromContext(ctx)
	desired := r.buildService(cr)

	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Service", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	// Only update if our desired spec actually changed (compare hash annotations)
	desiredHash := desired.Annotations[specHashAnnotation]
	existingHash := existing.Annotations[specHashAnnotation]
	if desiredHash == existingHash {
		logger.V(1).Info("Service already up to date, skipping", "name", desired.Name)
		return nil
	}

	// Preserve ClusterIP on update (immutable field)
	desired.Spec.ClusterIP = existing.Spec.ClusterIP

	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[specHashAnnotation] = desiredHash
	logger.Info("Updating Service", "name", desired.Name)
	return r.Update(ctx, existing)
}

func (r *DevStagingEnvironmentReconciler) buildService(cr *appsv1alpha1.DevStagingEnvironment) *corev1.Service {
	labels := labelsForCR(cr)
	spec := cr.Spec.Service

	// Default target port to the container port if not specified
	targetPort := cr.Spec.Deployment.Port
	if spec.TargetPort != nil {
		targetPort = *spec.TargetPort
	}

	svcType := corev1.ServiceTypeClusterIP
	switch spec.Type {
	case "NodePort":
		svcType = corev1.ServiceTypeNodePort
	case "LoadBalancer":
		svcType = corev1.ServiceTypeLoadBalancer
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				specHashAnnotation: computeSpecHash(cr.Spec.Service),
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       spec.Port,
				TargetPort: intstr.FromInt(int(targetPort)),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Ingress
// ────────────────────────────────────────────────────────────────────────────

func (r *DevStagingEnvironmentReconciler) reconcileIngress(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment) error {
	logger := log.FromContext(ctx)
	ingressName := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	// If Ingress is not enabled, clean up any existing one
	if cr.Spec.Ingress == nil || !cr.Spec.Ingress.Enabled {
		existing := &networkingv1.Ingress{}
		if err := r.Get(ctx, ingressName, existing); err == nil {
			logger.Info("Deleting Ingress (disabled)", "name", cr.Name)
			return r.Delete(ctx, existing)
		}
		return nil
	}

	desired := r.buildIngress(cr)
	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &networkingv1.Ingress{}
	err := r.Get(ctx, ingressName, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Ingress", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	// Only update if the spec or annotations actually changed
	desiredHash := desired.Annotations[specHashAnnotation]
	existingHash := existing.Annotations[specHashAnnotation]
	if desiredHash == existingHash {
		logger.V(1).Info("Ingress already up to date, skipping", "name", desired.Name)
		return nil
	}

	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	for k, v := range desired.Annotations {
		existing.Annotations[k] = v
	}
	logger.Info("Updating Ingress", "name", desired.Name)
	return r.Update(ctx, existing)
}

func (r *DevStagingEnvironmentReconciler) buildIngress(cr *appsv1alpha1.DevStagingEnvironment) *networkingv1.Ingress {
	labels := labelsForCR(cr)
	spec := cr.Spec.Ingress

	pathType := networkingv1.PathTypePrefix
	switch spec.PathType {
	case "Exact":
		pathType = networkingv1.PathTypeExact
	case "ImplementationSpecific":
		pathType = networkingv1.PathTypeImplementationSpecific
	}

	path := "/"
	if spec.Path != "" {
		path = spec.Path
	}

	// Merge our spec hash into user-provided annotations
	annotations := make(map[string]string)
	for k, v := range spec.Annotations {
		annotations[k] = v
	}
	annotations[specHashAnnotation] = computeSpecHash(cr.Spec.Ingress)

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cr.Name,
			Namespace:   cr.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: spec.IngressClassName,
			Rules: []networkingv1.IngressRule{{
				Host: spec.Host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     path,
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: cr.Name,
									Port: networkingv1.ServiceBackendPort{
										Number: cr.Spec.Service.Port,
									},
								},
							},
						}},
					},
				},
			}},
		},
	}

	// Wire up TLS if configured
	if spec.TLS != nil {
		hosts := spec.TLS.Hosts
		if len(hosts) == 0 && spec.Host != "" {
			hosts = []string{spec.Host}
		}
		ingress.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      hosts,
			SecretName: spec.TLS.SecretName,
		}}
	}

	return ingress
}

// ────────────────────────────────────────────────────────────────────────────
// Status
// ────────────────────────────────────────────────────────────────────────────

func (r *DevStagingEnvironmentReconciler) updateStatus(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment) error {
	// Fetch current Deployment state
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, deploy); err == nil {
		cr.Status.AvailableReplicas = deploy.Status.AvailableReplicas
		cr.Status.DeploymentReady = deploy.Status.AvailableReplicas == deploy.Status.Replicas &&
			deploy.Status.Replicas > 0
	}

	// Fetch current Service state
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, svc); err == nil {
		cr.Status.ServiceReady = true
	} else {
		cr.Status.ServiceReady = false
	}

	// Fetch current Ingress state
	if cr.Spec.Ingress != nil && cr.Spec.Ingress.Enabled {
		ing := &networkingv1.Ingress{}
		if err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, ing); err == nil {
			cr.Status.IngressReady = true
			if cr.Spec.Ingress.Host != "" {
				scheme := "http"
				if cr.Spec.Ingress.TLS != nil {
					scheme = "https"
				}
				cr.Status.URL = fmt.Sprintf("%s://%s%s", scheme, cr.Spec.Ingress.Host, cr.Spec.Ingress.Path)
			}
		} else {
			cr.Status.IngressReady = false
		}
	} else {
		cr.Status.IngressReady = false
		cr.Status.URL = ""
	}

	// Check dependency readiness
	depsReady := true
	for _, dep := range cr.Spec.Dependencies {
		depDeploy := &appsv1.Deployment{}
		depName := dependencyName(cr.Name, dep.Type)
		if err := r.Get(ctx, types.NamespacedName{Name: depName, Namespace: cr.Namespace}, depDeploy); err != nil {
			depsReady = false
			break
		}
		if depDeploy.Status.AvailableReplicas < 1 {
			depsReady = false
			break
		}
	}
	if len(cr.Spec.Dependencies) == 0 {
		depsReady = true
	}
	cr.Status.DependenciesReady = depsReady

	// Set an overall "Ready" condition
	allReady := cr.Status.DeploymentReady && cr.Status.ServiceReady && depsReady
	if allReady {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "AllResourcesReady",
			Message: "Deployment, Service, Ingress (if enabled), and Dependencies are ready",
		})
	} else {
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ResourcesNotReady",
			Message: "One or more child resources are not yet ready",
		})
	}

	return r.Status().Update(ctx, cr)
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// labelsForCR returns the standard set of labels applied to all child resources.
// This is the glue that connects Deployments → Pods → Services.
func labelsForCR(cr *appsv1alpha1.DevStagingEnvironment) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       cr.Name,
		"app.kubernetes.io/managed-by": "devstagingenvironment-operator",
		"app.kubernetes.io/instance":   cr.Name,
	}
}

// buildResourceRequirements converts our simplified resource spec into the full K8s type.
func buildResourceRequirements(res *appsv1alpha1.ResourceRequirements) corev1.ResourceRequirements {
	reqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if res.CPURequest != nil {
		reqs.Requests[corev1.ResourceCPU] = *res.CPURequest
	}
	if res.CPULimit != nil {
		reqs.Limits[corev1.ResourceCPU] = *res.CPULimit
	}
	if res.MemoryRequest != nil {
		reqs.Requests[corev1.ResourceMemory] = *res.MemoryRequest
	}
	if res.MemoryLimit != nil {
		reqs.Limits[corev1.ResourceMemory] = *res.MemoryLimit
	}
	return reqs
}

// buildHTTPProbe constructs a liveness/readiness probe from the health check spec.
func buildHTTPProbe(hc *appsv1alpha1.HealthCheckSpec, defaultPort int32) *corev1.Probe {
	port := defaultPort
	if hc.Port != nil {
		port = *hc.Port
	}

	probe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: hc.Path,
				Port: intstr.FromInt(int(port)),
			},
		},
	}

	if hc.InitialDelaySeconds != nil {
		probe.InitialDelaySeconds = *hc.InitialDelaySeconds
	}
	if hc.PeriodSeconds != nil {
		probe.PeriodSeconds = *hc.PeriodSeconds
	}

	return probe
}

// computeSpecHash returns a short SHA-256 hash of the JSON-serialized input.
// Used as an annotation to detect when the desired spec has actually changed,
// avoiding unnecessary updates that trigger reconcile loops.
func computeSpecHash(obj interface{}) string {
	data, _ := json.Marshal(obj)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

// SetupWithManager sets up the controller with the Manager.
// It watches DevStagingEnvironment (primary) and also watches Deployments, Services,
// and Ingresses that the operator owns, so changes to child resources
// trigger a reconciliation of the parent CR.
func (r *DevStagingEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.DevStagingEnvironment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}

// ────────────────────────────────────────────────────────────────────────────
// Dependencies — databases, caches, queues, object stores
// ────────────────────────────────────────────────────────────────────────────

// dependencyDefaults holds the convention-over-configuration defaults for a
// well-known dependency type.
type dependencyDefaults struct {
	Image      string          // e.g. "postgres:16"
	Port       int32           // e.g. 5432
	EnvVarName string          // injected into the app container
	Env        []corev1.EnvVar // container env vars to configure the dep itself
	Stateful   bool            // true = needs a PVC
}

// dependencyRegistry maps each supported DependencyType to its defaults.
var dependencyRegistry = map[appsv1alpha1.DependencyType]dependencyDefaults{
	appsv1alpha1.DependencyPostgres: {
		Image:      "postgres",
		Port:       5432,
		EnvVarName: "DATABASE_URL",
		Env: []corev1.EnvVar{
			{Name: "POSTGRES_USER", Value: "devuser"},
			{Name: "POSTGRES_PASSWORD", Value: "devpass"},
			{Name: "POSTGRES_DB", Value: "devdb"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyRedis: {
		Image:      "redis",
		Port:       6379,
		EnvVarName: "REDIS_URL",
		Env:        nil,
		Stateful:   false,
	},
	appsv1alpha1.DependencyMySQL: {
		Image:      "mysql",
		Port:       3306,
		EnvVarName: "DATABASE_URL",
		Env: []corev1.EnvVar{
			{Name: "MYSQL_ROOT_PASSWORD", Value: "devpass"},
			{Name: "MYSQL_DATABASE", Value: "devdb"},
			{Name: "MYSQL_USER", Value: "devuser"},
			{Name: "MYSQL_PASSWORD", Value: "devpass"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyMongoDB: {
		Image:      "mongo",
		Port:       27017,
		EnvVarName: "MONGO_URL",
		Env: []corev1.EnvVar{
			{Name: "MONGO_INITDB_ROOT_USERNAME", Value: "devuser"},
			{Name: "MONGO_INITDB_ROOT_PASSWORD", Value: "devpass"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyRabbitMQ: {
		Image:      "rabbitmq",
		Port:       5672,
		EnvVarName: "AMQP_URL",
		Env: []corev1.EnvVar{
			{Name: "RABBITMQ_DEFAULT_USER", Value: "devuser"},
			{Name: "RABBITMQ_DEFAULT_PASS", Value: "devpass"},
		},
		Stateful: false,
	},
	appsv1alpha1.DependencyMinIO: {
		Image:      "minio/minio",
		Port:       9000,
		EnvVarName: "S3_ENDPOINT",
		Env: []corev1.EnvVar{
			{Name: "MINIO_ROOT_USER", Value: "minioadmin"},
			{Name: "MINIO_ROOT_PASSWORD", Value: "minioadmin"},
		},
		Stateful: true,
	},
}

// dependencyName returns the child resource name for a given dependency.
func dependencyName(crName string, depType appsv1alpha1.DependencyType) string {
	return fmt.Sprintf("%s-%s", crName, string(depType))
}

// reconcileDependencies processes each declared dependency: creates a Secret
// (with credentials), a Deployment, and a Service.
func (r *DevStagingEnvironmentReconciler) reconcileDependencies(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment) error {
	logger := log.FromContext(ctx)

	for _, dep := range cr.Spec.Dependencies {
		defaults, ok := dependencyRegistry[dep.Type]
		if !ok {
			return fmt.Errorf("unsupported dependency type: %s", dep.Type)
		}

		// 1. Reconcile the credentials Secret
		if err := r.reconcileDependencySecret(ctx, cr, dep, defaults); err != nil {
			return fmt.Errorf("dependency %s secret: %w", dep.Type, err)
		}

		// 2. Reconcile the Deployment for this dependency
		if err := r.reconcileDependencyDeployment(ctx, cr, dep, defaults); err != nil {
			return fmt.Errorf("dependency %s deployment: %w", dep.Type, err)
		}

		// 3. Reconcile the Service for this dependency
		if err := r.reconcileDependencyService(ctx, cr, dep, defaults); err != nil {
			return fmt.Errorf("dependency %s service: %w", dep.Type, err)
		}

		logger.Info("Dependency reconciled", "type", dep.Type, "name", dependencyName(cr.Name, dep.Type))
	}

	return nil
}

// reconcileDependencySecret creates a Secret containing the dependency credentials.
// These are used both by the dependency container and by the app via env var injection.
func (r *DevStagingEnvironmentReconciler) reconcileDependencySecret(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment, dep appsv1alpha1.DependencySpec, defaults dependencyDefaults) error {
	name := dependencyName(cr.Name, dep.Type) + "-credentials"
	labels := labelsForDependency(cr, dep.Type)

	// Build the data map from defaults, allowing user overrides via dep.Env
	data := make(map[string][]byte)
	envMap := envVarsToMap(defaults.Env)
	for _, e := range dep.Env {
		envMap[e.Name] = e.Value
	}
	for k, v := range envMap {
		data[k] = []byte(v)
	}

	// Also store the connection URL for convenience
	connURL := buildConnectionURL(cr.Name, dep, defaults)
	data["CONNECTION_URL"] = []byte(connURL)

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: data,
	}

	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, existing); err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}

	// Update if data changed
	existingHash := existing.Annotations[specHashAnnotation]
	desiredHash := computeSpecHash(desired.Data)
	if existingHash == desiredHash {
		return nil
	}
	existing.Data = desired.Data
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[specHashAnnotation] = desiredHash
	return r.Update(ctx, existing)
}

// reconcileDependencyDeployment creates a Deployment for the dependency service.
func (r *DevStagingEnvironmentReconciler) reconcileDependencyDeployment(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment, dep appsv1alpha1.DependencySpec, defaults dependencyDefaults) error {
	name := dependencyName(cr.Name, dep.Type)
	labels := labelsForDependency(cr, dep.Type)

	// Resolve image
	image := defaults.Image
	if dep.Image != "" {
		image = dep.Image
	} else if dep.Version != "" {
		image = fmt.Sprintf("%s:%s", defaults.Image, dep.Version)
	}

	// Resolve port
	port := defaults.Port
	if dep.Port != nil {
		port = *dep.Port
	}

	// Build env: merge defaults + user overrides
	env := mergeEnvVars(defaults.Env, dep.Env)

	// Handle special container args (e.g. MinIO needs "server /data")
	var args []string
	if dep.Type == appsv1alpha1.DependencyMinIO {
		args = []string{"server", "/data"}
	}
	if dep.Type == appsv1alpha1.DependencyRabbitMQ {
		// Use the management tag by default for the UI
		if dep.Image == "" && dep.Version == "" {
			image = defaults.Image + ":3-management"
		}
	}

	container := corev1.Container{
		Name:  string(dep.Type),
		Image: image,
		Env:   env,
		Args:  args,
		Ports: []corev1.ContainerPort{{
			Name:          string(dep.Type),
			ContainerPort: port,
			Protocol:      corev1.ProtocolTCP,
		}},
	}

	// Apply resource requirements if provided
	if dep.Resources != nil {
		container.Resources = buildResourceRequirements(dep.Resources)
	}

	replicas := int32(1)
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				specHashAnnotation: computeSpecHash(dep),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, existing); err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}

	desiredHash := desired.Annotations[specHashAnnotation]
	existingHash := existing.Annotations[specHashAnnotation]
	if desiredHash == existingHash {
		return nil
	}

	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[specHashAnnotation] = desiredHash
	return r.Update(ctx, existing)
}

// reconcileDependencyService creates a ClusterIP Service for the dependency.
func (r *DevStagingEnvironmentReconciler) reconcileDependencyService(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment, dep appsv1alpha1.DependencySpec, defaults dependencyDefaults) error {
	name := dependencyName(cr.Name, dep.Type)
	labels := labelsForDependency(cr, dep.Type)

	port := defaults.Port
	if dep.Port != nil {
		port = *dep.Port
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				specHashAnnotation: computeSpecHash(dep),
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       string(dep.Type),
				Port:       port,
				TargetPort: intstr.FromInt(int(port)),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}

	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, existing); err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}

	desiredHash := desired.Annotations[specHashAnnotation]
	existingHash := existing.Annotations[specHashAnnotation]
	if desiredHash == existingHash {
		return nil
	}

	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[specHashAnnotation] = desiredHash
	return r.Update(ctx, existing)
}

// ────────────────────────────────────────────────────────────────────────────
// Dependency Helpers
// ────────────────────────────────────────────────────────────────────────────

// labelsForDependency returns labels for a dependency's child resources.
func labelsForDependency(cr *appsv1alpha1.DevStagingEnvironment, depType appsv1alpha1.DependencyType) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       dependencyName(cr.Name, depType),
		"app.kubernetes.io/component":  string(depType),
		"app.kubernetes.io/part-of":    cr.Name,
		"app.kubernetes.io/managed-by": "devstagingenvironment-operator",
	}
}

// buildConnectionURL constructs the connection string for a dependency using
// the in-cluster DNS name of the dependency Service.
func buildConnectionURL(crName string, dep appsv1alpha1.DependencySpec, defaults dependencyDefaults) string {
	svcName := dependencyName(crName, dep.Type)

	port := defaults.Port
	if dep.Port != nil {
		port = *dep.Port
	}

	envMap := envVarsToMap(defaults.Env)
	for _, e := range dep.Env {
		envMap[e.Name] = e.Value
	}

	switch dep.Type {
	case appsv1alpha1.DependencyPostgres:
		user := envMap["POSTGRES_USER"]
		pass := envMap["POSTGRES_PASSWORD"]
		db := envMap["POSTGRES_DB"]
		return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, pass, svcName, port, db)
	case appsv1alpha1.DependencyRedis:
		return fmt.Sprintf("redis://%s:%d/0", svcName, port)
	case appsv1alpha1.DependencyMySQL:
		user := envMap["MYSQL_USER"]
		pass := envMap["MYSQL_PASSWORD"]
		db := envMap["MYSQL_DATABASE"]
		return fmt.Sprintf("mysql://%s:%s@%s:%d/%s", user, pass, svcName, port, db)
	case appsv1alpha1.DependencyMongoDB:
		user := envMap["MONGO_INITDB_ROOT_USERNAME"]
		pass := envMap["MONGO_INITDB_ROOT_PASSWORD"]
		return fmt.Sprintf("mongodb://%s:%s@%s:%d", user, pass, svcName, port)
	case appsv1alpha1.DependencyRabbitMQ:
		user := envMap["RABBITMQ_DEFAULT_USER"]
		pass := envMap["RABBITMQ_DEFAULT_PASS"]
		return fmt.Sprintf("amqp://%s:%s@%s:%d/", user, pass, svcName, port)
	case appsv1alpha1.DependencyMinIO:
		return fmt.Sprintf("http://%s:%d", svcName, port)
	default:
		return fmt.Sprintf("%s:%d", svcName, port)
	}
}

// buildDependencyConnectionEnvVars returns the env vars that should be injected
// into the app container for a given dependency (e.g. DATABASE_URL, REDIS_URL).
func buildDependencyConnectionEnvVars(crName string, dep appsv1alpha1.DependencySpec) []corev1.EnvVar {
	defaults, ok := dependencyRegistry[dep.Type]
	if !ok {
		return nil
	}

	envVarName := defaults.EnvVarName
	if dep.EnvVarName != "" {
		envVarName = dep.EnvVarName
	}

	connURL := buildConnectionURL(crName, dep, defaults)

	envVars := []corev1.EnvVar{
		{Name: envVarName, Value: connURL},
	}

	// For MinIO, also inject access credentials so the app can authenticate.
	if dep.Type == appsv1alpha1.DependencyMinIO {
		envMap := envVarsToMap(defaults.Env)
		for _, e := range dep.Env {
			envMap[e.Name] = e.Value
		}
		envVars = append(envVars,
			corev1.EnvVar{Name: "S3_ACCESS_KEY", Value: envMap["MINIO_ROOT_USER"]},
			corev1.EnvVar{Name: "S3_SECRET_KEY", Value: envMap["MINIO_ROOT_PASSWORD"]},
		)
	}

	return envVars
}

// envVarsToMap converts a slice of EnvVar to a map for easy merging.
func envVarsToMap(envs []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(envs))
	for _, e := range envs {
		m[e.Name] = e.Value
	}
	return m
}

// mergeEnvVars merges two slices of EnvVar, with overrides taking precedence.
func mergeEnvVars(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	m := make(map[string]corev1.EnvVar, len(base)+len(overrides))
	var order []string
	for _, e := range base {
		m[e.Name] = e
		order = append(order, e.Name)
	}
	for _, e := range overrides {
		if _, exists := m[e.Name]; !exists {
			order = append(order, e.Name)
		}
		m[e.Name] = e
	}
	result := make([]corev1.EnvVar, 0, len(order))
	for _, name := range order {
		result = append(result, m[name])
	}
	return result
}

// generatePassword creates a random alphanumeric password.
// Used for auto-generating dependency credentials.
func generatePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// init is used to silence the strings import if no other code references it.
var _ = strings.TrimSpace
