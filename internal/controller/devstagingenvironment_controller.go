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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/jeffvincent/kindling/api/v1alpha1"
)

// safeName converts a CR name (DNS-1123 subdomain, which allows dots) into a
// DNS-1035 label (lowercase alphanumeric + hyphens only). This is necessary
// because K8s Services require DNS-1035 names, while metadata.name allows dots.
func safeName(name string) string {
	return strings.ReplaceAll(name, ".", "-")
}

// DevStagingEnvironmentReconciler reconciles a DevStagingEnvironment object
type DevStagingEnvironmentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
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
		r.recordEvent(cr, "Warning", "ReconcileFailed", "Deployment reconciliation failed: %v", err)
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
		r.recordEvent(cr, "Warning", "ReconcileFailed", "Service reconciliation failed: %v", err)
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
		r.recordEvent(cr, "Warning", "ReconcileFailed", "Ingress reconciliation failed: %v", err)
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
		r.recordEvent(cr, "Warning", "ReconcileFailed", "Dependencies reconciliation failed: %v", err)
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

	// If status is not fully ready yet, requeue to pick up child resource
	// status changes (e.g. Deployment replicas becoming available).
	if !cr.Status.DeploymentReady || !cr.Status.ServiceReady || !cr.Status.DependenciesReady {
		logger.Info("Not all child resources are ready yet, requeueing")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	logger.Info("Reconciliation complete")
	r.recordEvent(cr, "Normal", "ReconcileComplete", "All resources reconciled successfully")
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

	// Merge dependency connection strings with user-provided env vars.
	// Dependency vars (DATABASE_URL, REDIS_URL, etc.) must come first so that
	// user env vars can reference them via Kubernetes $(VAR) expansion —
	// e.g. PG_DSN: "$(DATABASE_URL)" only resolves if DATABASE_URL is
	// defined earlier in the env list.
	var allEnv []corev1.EnvVar
	for _, dep := range cr.Spec.Dependencies {
		allEnv = append(allEnv, buildDependencyConnectionEnvVars(cr.Name, dep)...)
	}
	allEnv = append(allEnv, spec.Env...)

	container := corev1.Container{
		Name:    safeName(cr.Name),
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
		switch spec.HealthCheck.Type {
		case "grpc":
			probe := buildGRPCProbe(spec.HealthCheck, spec.Port)
			container.LivenessProbe = probe.DeepCopy()
			container.ReadinessProbe = probe.DeepCopy()
		case "none":
			// No probes — intentionally left empty
		default: // "http" or empty
			probe := buildHTTPProbe(spec.HealthCheck, spec.Port)
			container.LivenessProbe = probe.DeepCopy()
			container.ReadinessProbe = probe.DeepCopy()
		}
	}

	// Build init containers that wait for each dependency to accept TCP connections
	initContainers := buildDependencyWaitInitContainers(cr)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      safeName(cr.Name),
			Namespace: cr.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				specHashAnnotation: computeSpecHash(cr.Spec),
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
					InitContainers: initContainers,
					Containers:     []corev1.Container{container},
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
			Name:      safeName(cr.Name),
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
	ingressName := types.NamespacedName{Name: safeName(cr.Name), Namespace: cr.Namespace}

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
			Name:        safeName(cr.Name),
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
									Name: safeName(cr.Name),
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
	if err := r.Get(ctx, types.NamespacedName{Name: safeName(cr.Name), Namespace: cr.Namespace}, deploy); err == nil {
		cr.Status.AvailableReplicas = deploy.Status.AvailableReplicas
		cr.Status.DeploymentReady = deploy.Status.AvailableReplicas == deploy.Status.Replicas &&
			deploy.Status.Replicas > 0
	}

	// Fetch current Service state
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: safeName(cr.Name), Namespace: cr.Namespace}, svc); err == nil {
		cr.Status.ServiceReady = true
	} else {
		cr.Status.ServiceReady = false
	}

	// Fetch current Ingress state
	if cr.Spec.Ingress != nil && cr.Spec.Ingress.Enabled {
		ing := &networkingv1.Ingress{}
		if err := r.Get(ctx, types.NamespacedName{Name: safeName(cr.Name), Namespace: cr.Namespace}, ing); err == nil {
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

// buildGRPCProbe constructs a liveness/readiness probe using the gRPC health checking protocol.
func buildGRPCProbe(hc *appsv1alpha1.HealthCheckSpec, defaultPort int32) *corev1.Probe {
	port := defaultPort
	if hc.Port != nil {
		port = *hc.Port
	}

	probe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			GRPC: &corev1.GRPCAction{
				Port: port,
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
	r.Recorder = mgr.GetEventRecorderFor("devstagingenvironment-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.DevStagingEnvironment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}

// recordEvent safely emits a Kubernetes Event on the CR. It is a no-op when
// the Recorder has not been initialised (e.g. in unit tests that don't use a
// full manager).
func (r *DevStagingEnvironmentReconciler) recordEvent(cr *appsv1alpha1.DevStagingEnvironment, eventType, reason, messageFmt string, args ...interface{}) {
	if r.Recorder != nil {
		r.Recorder.Eventf(cr, eventType, reason, messageFmt, args...)
	}
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
	appsv1alpha1.DependencyElasticsearch: {
		Image:      "docker.elastic.co/elasticsearch/elasticsearch",
		Port:       9200,
		EnvVarName: "ELASTICSEARCH_URL",
		Env: []corev1.EnvVar{
			{Name: "discovery.type", Value: "single-node"},
			{Name: "xpack.security.enabled", Value: "false"},
			{Name: "ES_JAVA_OPTS", Value: "-Xms256m -Xmx256m"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyKafka: {
		Image:      "apache/kafka",
		Port:       9092,
		EnvVarName: "KAFKA_BROKER_URL",
		Env: []corev1.EnvVar{
			{Name: "KAFKA_NODE_ID", Value: "1"},
			{Name: "KAFKA_PROCESS_ROLES", Value: "broker,controller"},
			{Name: "KAFKA_CONTROLLER_QUORUM_VOTERS", Value: "1@localhost:9093"},
			{Name: "KAFKA_LISTENERS", Value: "PLAINTEXT://:9092,CONTROLLER://:9093"},
			{Name: "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP", Value: "PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT"},
			{Name: "KAFKA_CONTROLLER_LISTENER_NAMES", Value: "CONTROLLER"},
			{Name: "CLUSTER_ID", Value: "kindling-dev-kafka-cluster"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyNATS: {
		Image:      "nats",
		Port:       4222,
		EnvVarName: "NATS_URL",
		Env:        nil,
		Stateful:   false,
	},
	appsv1alpha1.DependencyMemcached: {
		Image:      "memcached",
		Port:       11211,
		EnvVarName: "MEMCACHED_URL",
		Env:        nil,
		Stateful:   false,
	},
	appsv1alpha1.DependencyCassandra: {
		Image:      "cassandra",
		Port:       9042,
		EnvVarName: "CASSANDRA_URL",
		Env: []corev1.EnvVar{
			{Name: "CASSANDRA_CLUSTER_NAME", Value: "DevCluster"},
			{Name: "CASSANDRA_DC", Value: "dc1"},
			{Name: "MAX_HEAP_SIZE", Value: "256M"},
			{Name: "HEAP_NEWSIZE", Value: "64M"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyConsul: {
		Image:      "hashicorp/consul",
		Port:       8500,
		EnvVarName: "CONSUL_HTTP_ADDR",
		Env:        nil,
		Stateful:   false,
	},
	appsv1alpha1.DependencyVault: {
		Image:      "hashicorp/vault",
		Port:       8200,
		EnvVarName: "VAULT_ADDR",
		Env: []corev1.EnvVar{
			{Name: "VAULT_DEV_ROOT_TOKEN_ID", Value: "dev-root-token"},
			{Name: "VAULT_DEV_LISTEN_ADDRESS", Value: "0.0.0.0:8200"},
		},
		Stateful: false,
	},
	appsv1alpha1.DependencyInfluxDB: {
		Image:      "influxdb",
		Port:       8086,
		EnvVarName: "INFLUXDB_URL",
		Env: []corev1.EnvVar{
			{Name: "DOCKER_INFLUXDB_INIT_MODE", Value: "setup"},
			{Name: "DOCKER_INFLUXDB_INIT_USERNAME", Value: "devuser"},
			{Name: "DOCKER_INFLUXDB_INIT_PASSWORD", Value: "devpass123"},
			{Name: "DOCKER_INFLUXDB_INIT_ORG", Value: "devorg"},
			{Name: "DOCKER_INFLUXDB_INIT_BUCKET", Value: "devbucket"},
		},
		Stateful: true,
	},
	appsv1alpha1.DependencyJaeger: {
		Image:      "jaegertracing/all-in-one",
		Port:       16686,
		EnvVarName: "JAEGER_ENDPOINT",
		Env: []corev1.EnvVar{
			{Name: "COLLECTOR_OTLP_ENABLED", Value: "true"},
		},
		Stateful: false,
	},
}

// dependencyName returns the child resource name for a given dependency.
func dependencyName(crName string, depType appsv1alpha1.DependencyType) string {
	return fmt.Sprintf("%s-%s", safeName(crName), string(depType))
}

// buildDependencyWaitInitContainers creates one init container per dependency
// that blocks until the dependency service is accepting TCP connections. This
// prevents the app container from crashing on startup because a database or
// queue isn't ready yet.
func buildDependencyWaitInitContainers(cr *appsv1alpha1.DevStagingEnvironment) []corev1.Container {
	if len(cr.Spec.Dependencies) == 0 {
		return nil
	}

	var initContainers []corev1.Container
	for _, dep := range cr.Spec.Dependencies {
		defaults, ok := dependencyRegistry[dep.Type]
		if !ok {
			continue
		}

		svcName := dependencyName(cr.Name, dep.Type)
		port := defaults.Port
		if dep.Port != nil {
			port = *dep.Port
		}

		// Use busybox to do a TCP probe in a loop until the service is reachable
		script := fmt.Sprintf(
			`echo "Waiting for %s at %s:%d..."
until nc -z -w2 %s %d; do
  echo "  %s not ready, retrying in 2s..."
  sleep 2
done
echo "%s is ready!"`,
			dep.Type, svcName, port,
			svcName, port,
			dep.Type,
			dep.Type,
		)

		initContainers = append(initContainers, corev1.Container{
			Name:    fmt.Sprintf("wait-for-%s", dep.Type),
			Image:   "busybox:1.36",
			Command: []string{"/bin/sh", "-c", script},
		})
	}

	return initContainers
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

	// 4. Prune stale dependencies — if a dep was removed from the spec,
	//    delete its Deployment, Service, and Secret.
	if err := r.pruneOrphanedDependencies(ctx, cr); err != nil {
		return fmt.Errorf("prune orphaned dependencies: %w", err)
	}

	return nil
}

// pruneOrphanedDependencies deletes Deployments, Services, and Secrets for
// dependencies that were removed from the CR spec. It finds all child
// Deployments labelled as managed by this CR and deletes any whose dependency
// type is no longer in cr.Spec.Dependencies.
func (r *DevStagingEnvironmentReconciler) pruneOrphanedDependencies(ctx context.Context, cr *appsv1alpha1.DevStagingEnvironment) error {
	logger := log.FromContext(ctx)

	// Build a set of dependency types currently declared in the spec
	wantedTypes := make(map[string]bool, len(cr.Spec.Dependencies))
	for _, dep := range cr.Spec.Dependencies {
		wantedTypes[string(dep.Type)] = true
	}

	// List all Deployments that belong to this CR's dependencies
	depDeployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, depDeployments,
		client.InNamespace(cr.Namespace),
		client.MatchingLabels{
			"app.kubernetes.io/part-of":    cr.Name,
			"app.kubernetes.io/managed-by": "devstagingenvironment-operator",
		},
	); err != nil {
		return err
	}

	for i := range depDeployments.Items {
		dep := &depDeployments.Items[i]
		component := dep.Labels["app.kubernetes.io/component"]
		if component == "" {
			continue // not a dependency resource
		}
		if wantedTypes[component] {
			continue // still declared in the spec
		}

		logger.Info("Pruning orphaned dependency Deployment", "name", dep.Name, "type", component)
		if err := r.Delete(ctx, dep); err != nil && !errors.IsNotFound(err) {
			return err
		}

		// Also delete the corresponding Service
		svc := &corev1.Service{}
		svcKey := types.NamespacedName{Name: dep.Name, Namespace: cr.Namespace}
		if err := r.Get(ctx, svcKey, svc); err == nil {
			logger.Info("Pruning orphaned dependency Service", "name", svc.Name)
			if err := r.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}

		// Also delete the corresponding credentials Secret
		secret := &corev1.Secret{}
		secretKey := types.NamespacedName{Name: dep.Name + "-credentials", Namespace: cr.Namespace}
		if err := r.Get(ctx, secretKey, secret); err == nil {
			logger.Info("Pruning orphaned dependency Secret", "name", secret.Name)
			if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
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
	if dep.Type == appsv1alpha1.DependencyConsul {
		args = []string{"agent", "-dev", "-client=0.0.0.0"}
	}
	if dep.Type == appsv1alpha1.DependencyVault {
		args = []string{"server", "-dev"}
	}
	if dep.Type == appsv1alpha1.DependencyElasticsearch {
		if dep.Image == "" && dep.Version == "" {
			image = defaults.Image + ":8.12.0"
		}
	}
	if dep.Type == appsv1alpha1.DependencyKafka {
		if dep.Image == "" && dep.Version == "" {
			image = defaults.Image + ":latest"
		}
	}
	if dep.Type == appsv1alpha1.DependencyJaeger {
		if dep.Image == "" && dep.Version == "" {
			image = defaults.Image + ":latest"
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

	// Some services expose multiple ports that the app needs to reach.
	switch dep.Type {
	case appsv1alpha1.DependencyJaeger:
		container.Ports = append(container.Ports,
			corev1.ContainerPort{Name: "otlp-grpc", ContainerPort: 4317, Protocol: corev1.ProtocolTCP},
			corev1.ContainerPort{Name: "otlp-http", ContainerPort: 4318, Protocol: corev1.ProtocolTCP},
		)
	case appsv1alpha1.DependencyKafka:
		container.Ports = append(container.Ports,
			corev1.ContainerPort{Name: "controller", ContainerPort: 9093, Protocol: corev1.ProtocolTCP},
		)
	case appsv1alpha1.DependencyRabbitMQ:
		container.Ports = append(container.Ports,
			corev1.ContainerPort{Name: "management", ContainerPort: 15672, Protocol: corev1.ProtocolTCP},
		)
	case appsv1alpha1.DependencyElasticsearch:
		container.Ports = append(container.Ports,
			corev1.ContainerPort{Name: "transport", ContainerPort: 9300, Protocol: corev1.ProtocolTCP},
		)
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
	case appsv1alpha1.DependencyElasticsearch:
		return fmt.Sprintf("http://%s:%d", svcName, port)
	case appsv1alpha1.DependencyKafka:
		return fmt.Sprintf("%s:%d", svcName, port)
	case appsv1alpha1.DependencyNATS:
		return fmt.Sprintf("nats://%s:%d", svcName, port)
	case appsv1alpha1.DependencyMemcached:
		return fmt.Sprintf("%s:%d", svcName, port)
	case appsv1alpha1.DependencyCassandra:
		return fmt.Sprintf("%s:%d", svcName, port)
	case appsv1alpha1.DependencyConsul:
		return fmt.Sprintf("http://%s:%d", svcName, port)
	case appsv1alpha1.DependencyVault:
		return fmt.Sprintf("http://%s:%d", svcName, port)
	case appsv1alpha1.DependencyInfluxDB:
		user := envMap["DOCKER_INFLUXDB_INIT_USERNAME"]
		pass := envMap["DOCKER_INFLUXDB_INIT_PASSWORD"]
		return fmt.Sprintf("http://%s:%s@%s:%d", user, pass, svcName, port)
	case appsv1alpha1.DependencyJaeger:
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

	// For Vault, inject the dev root token.
	if dep.Type == appsv1alpha1.DependencyVault {
		envMap := envVarsToMap(defaults.Env)
		for _, e := range dep.Env {
			envMap[e.Name] = e.Value
		}
		envVars = append(envVars,
			corev1.EnvVar{Name: "VAULT_TOKEN", Value: envMap["VAULT_DEV_ROOT_TOKEN_ID"]},
		)
	}

	// For InfluxDB, inject org and bucket so the app can write metrics.
	if dep.Type == appsv1alpha1.DependencyInfluxDB {
		envMap := envVarsToMap(defaults.Env)
		for _, e := range dep.Env {
			envMap[e.Name] = e.Value
		}
		envVars = append(envVars,
			corev1.EnvVar{Name: "INFLUXDB_ORG", Value: envMap["DOCKER_INFLUXDB_INIT_ORG"]},
			corev1.EnvVar{Name: "INFLUXDB_BUCKET", Value: envMap["DOCKER_INFLUXDB_INIT_BUCKET"]},
		)
	}

	// For Jaeger, inject the OTLP collector endpoint (gRPC port 4317).
	if dep.Type == appsv1alpha1.DependencyJaeger {
		svcName := dependencyName(crName, dep.Type)
		envVars = append(envVars,
			corev1.EnvVar{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: fmt.Sprintf("http://%s:4317", svcName)},
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

