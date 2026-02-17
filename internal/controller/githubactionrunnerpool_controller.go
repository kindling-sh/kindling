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

// TODO: Implement idle cluster scaling. Runner pods should scale down (e.g. to 0 replicas)
// after a period of inactivity to free up resources when not in use, then scale back up
// on demand when a workflow triggers. This is important now that Kaniko layer caching is
// enabled (--cache=true, --cache-repo=registry:5000/cache) — the in-cluster registry
// retains cached layers across pod restarts, so scale-down doesn't lose build cache.
// Approach: watch for idle runners with no active jobs for N minutes, scale the
// StatefulSet/Deployment to 0, and use a webhook or polling mechanism to scale back up
// when a new workflow_dispatch or push event arrives.

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/jeffvincent/kindling/api/v1alpha1"
)

// GithubActionRunnerPoolReconciler reconciles a GithubActionRunnerPool object.
type GithubActionRunnerPoolReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

const runnerPoolHashAnnotation = "apps.example.com/runner-pool-spec-hash"

//+kubebuilder:rbac:groups=apps.example.com,resources=githubactionrunnerpools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.example.com,resources=githubactionrunnerpools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps.example.com,resources=githubactionrunnerpools/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods;pods/log;pods/exec;pods/attach;pods/portforward,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete;bind;escalate

// Reconcile reads the state of the cluster for a GithubActionRunnerPool object and makes changes
// to bring the cluster state closer to the desired state.
//
// The controller creates a Deployment whose pods run the GitHub Actions runner image.
// Each pod is configured with the necessary environment variables to self-register with
// the target GitHub repository or organization using the provided token.
func (r *GithubActionRunnerPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// ── Step 1: Fetch the CR ───────────────────────────────────────────
	cr := &appsv1alpha1.GithubActionRunnerPool{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("GithubActionRunnerPool resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ── Step 2: Validate the CR ────────────────────────────────────────
	if cr.Spec.GitHubUsername == "" || cr.Spec.Repository == "" {
		r.recordEvent(cr, "Warning", "InvalidSpec", "Both spec.githubUsername and spec.repository must be set")
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSpec",
			Message: "Both spec.githubUsername and spec.repository must be set",
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, nil
	}

	// ── Step 3: Verify the referenced Secret exists ────────────────────
	tokenSecret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      cr.Spec.TokenSecretRef.Name,
		Namespace: cr.Namespace,
	}
	if err := r.Get(ctx, secretKey, tokenSecret); err != nil {
		r.recordEvent(cr, "Warning", "SecretNotFound", "Token secret %q not found", cr.Spec.TokenSecretRef.Name)
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "SecretNotFound",
			Message: fmt.Sprintf("Token secret %q not found: %v", cr.Spec.TokenSecretRef.Name, err),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 4: Reconcile RBAC for the runner pod ──────────────────────
	// The runner pod needs kubectl access to create Kaniko pods, apply CRs, etc.
	if err := r.reconcileRunnerRBAC(ctx, cr); err != nil {
		r.recordEvent(cr, "Warning", "RBACFailed", "Failed to reconcile runner RBAC: %v", err)
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "RBACFailed",
			Message: fmt.Sprintf("Failed to reconcile runner RBAC: %v", err),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 5: Reconcile the runner Deployment ────────────────────────
	if err := r.reconcileRunnerDeployment(ctx, cr); err != nil {
		r.recordEvent(cr, "Warning", "ReconcileFailed", "Runner deployment reconciliation failed: %v", err)
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "DeploymentReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: err.Error(),
		})
		_ = r.Status().Update(ctx, cr)
		return ctrl.Result{}, err
	}

	// ── Step 6: Update status ──────────────────────────────────────────
	if err := r.updateRunnerPoolStatus(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliation complete for runner pool")
	r.recordEvent(cr, "Normal", "ReconcileComplete", "Runner pool reconciled successfully for %s", cr.Spec.GitHubUsername)
	return ctrl.Result{}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Runner RBAC — ServiceAccount, ClusterRole, ClusterRoleBinding
// ────────────────────────────────────────────────────────────────────────────

// reconcileRunnerRBAC ensures the runner pod has a ServiceAccount with the
// permissions it needs: creating Kaniko pods, applying DevStagingEnvironment
// CRs, watching rollouts, port-forwarding, etc.
func (r *GithubActionRunnerPoolReconciler) reconcileRunnerRBAC(ctx context.Context, cr *appsv1alpha1.GithubActionRunnerPool) error {
	logger := log.FromContext(ctx)

	saName := runnerServiceAccountName(cr)
	crName := runnerClusterRoleName(cr)
	crbName := runnerClusterRoleBindingName(cr)

	// ── ServiceAccount ────────────────────────────────────────────────
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cr.Namespace,
			Labels:    labelsForRunnerPool(cr),
		},
	}
	if err := controllerutil.SetControllerReference(cr, sa, r.Scheme); err != nil {
		return err
	}
	existingSA := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: saName, Namespace: cr.Namespace}, existingSA); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating runner ServiceAccount", "name", saName)
			if err := r.Create(ctx, sa); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// ── ClusterRole ───────────────────────────────────────────────────
	// The runner needs broad access: it runs Kaniko pods, applies CRDs,
	// monitors rollouts, and port-forwards for smoke tests.
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   crName,
			Labels: labelsForRunnerPool(cr),
		},
		Rules: []rbacv1.PolicyRule{
			{
				// Kaniko pods + watching/debugging workloads
				APIGroups: []string{""},
				Resources: []string{"pods", "pods/log", "pods/exec", "pods/attach", "pods/portforward", "services", "configmaps", "secrets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				// Deployments, ReplicaSets for rollout status
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "replicasets", "statefulsets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				// DevStagingEnvironment CRs
				APIGroups: []string{"apps.example.com"},
				Resources: []string{"devstagingenvironments", "githubactionrunnerpools"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				// Ingresses for the UI
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"ingresses"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				// Events for debugging
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
	existingCR := &rbacv1.ClusterRole{}
	if err := r.Get(ctx, types.NamespacedName{Name: crName}, existingCR); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating runner ClusterRole", "name", crName)
			if err := r.Create(ctx, clusterRole); err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		existingCR.Rules = clusterRole.Rules
		existingCR.Labels = clusterRole.Labels
		if err := r.Update(ctx, existingCR); err != nil {
			return err
		}
	}

	// ── ClusterRoleBinding ────────────────────────────────────────────
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   crbName,
			Labels: labelsForRunnerPool(cr),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: cr.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     crName,
		},
	}
	existingCRB := &rbacv1.ClusterRoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: crbName}, existingCRB); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating runner ClusterRoleBinding", "name", crbName)
			if err := r.Create(ctx, crb); err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		existingCRB.Subjects = crb.Subjects
		existingCRB.RoleRef = crb.RoleRef
		existingCRB.Labels = crb.Labels
		if err := r.Update(ctx, existingCRB); err != nil {
			return err
		}
	}

	return nil
}

func runnerServiceAccountName(cr *appsv1alpha1.GithubActionRunnerPool) string {
	return fmt.Sprintf("%s-runner", cr.Spec.GitHubUsername)
}

func runnerClusterRoleName(cr *appsv1alpha1.GithubActionRunnerPool) string {
	return fmt.Sprintf("%s-runner", cr.Spec.GitHubUsername)
}

func runnerClusterRoleBindingName(cr *appsv1alpha1.GithubActionRunnerPool) string {
	return fmt.Sprintf("%s-runner", cr.Spec.GitHubUsername)
}

// ────────────────────────────────────────────────────────────────────────────
// Runner Deployment
// ────────────────────────────────────────────────────────────────────────────

func (r *GithubActionRunnerPoolReconciler) reconcileRunnerDeployment(ctx context.Context, cr *appsv1alpha1.GithubActionRunnerPool) error {
	logger := log.FromContext(ctx)
	desired := r.buildRunnerDeployment(cr)

	if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating runner Deployment", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	// Only update if the desired spec actually changed
	desiredHash := desired.Annotations[runnerPoolHashAnnotation]
	existingHash := existing.Annotations[runnerPoolHashAnnotation]
	if desiredHash == existingHash {
		logger.V(1).Info("Runner Deployment already up to date, skipping", "name", desired.Name)
		return nil
	}

	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[runnerPoolHashAnnotation] = desiredHash
	logger.Info("Updating runner Deployment", "name", desired.Name)
	return r.Update(ctx, existing)
}

func (r *GithubActionRunnerPoolReconciler) buildRunnerDeployment(cr *appsv1alpha1.GithubActionRunnerPool) *appsv1.Deployment {
	labels := labelsForRunnerPool(cr)
	spec := cr.Spec

	// Default replica count
	replicas := int32(1)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}

	// Default work directory — must be under /home/runner (the runner user's
	// home in the official actions-runner image) so the non-root process can
	// create it at runtime.
	workDir := spec.WorkDir
	if workDir == "" || workDir == "/runner/_work" {
		workDir = "/home/runner/_work"
	}

	// Default service account — use the auto-created one if not specified
	saName := spec.ServiceAccountName
	if saName == "" {
		saName = runnerServiceAccountName(cr)
	}

	githubURL := spec.GitHubURL
	if githubURL == "" {
		githubURL = "https://github.com"
	}

	// ── Build environment variables for the runner container ───────────
	env := []corev1.EnvVar{
		{
			// The GitHub PAT (from the referenced Secret) is used at startup to
			// obtain a short-lived runner registration token via the GitHub API.
			// It is NOT passed directly to config.sh.
			Name: "GITHUB_PAT",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: spec.TokenSecretRef.Name,
					},
					Key: spec.TokenSecretRef.Key,
				},
			},
		},
		{
			// Runner name includes the username so it's identifiable in the GH UI
			Name:  "RUNNER_NAME_PREFIX",
			Value: fmt.Sprintf("%s-%s", spec.GitHubUsername, cr.Name),
		},
		{
			Name:  "RUNNER_WORKDIR",
			Value: workDir,
		},
		{
			// Repository URL for runner registration
			Name:  "RUNNER_REPOSITORY_URL",
			Value: fmt.Sprintf("%s/%s", githubURL, spec.Repository),
		},
		{
			// API base URL for token exchange (handles GHE vs github.com)
			Name:  "GITHUB_API_URL",
			Value: githubAPIURL(githubURL),
		},
		{
			// Repo slug for API calls (e.g. "jeff-vincent/kindling")
			Name:  "RUNNER_REPO_SLUG",
			Value: spec.Repository,
		},
		{
			// Expose the GitHub username to workflow steps so the job knows
			// whose local cluster it is running on
			Name:  "GITHUB_USERNAME",
			Value: spec.GitHubUsername,
		},
	}

	// Build runner labels: always include "self-hosted" and the username so
	// the workflow can do `runs-on: [self-hosted, <username>]`
	runnerLabels := []string{"self-hosted", spec.GitHubUsername}
	runnerLabels = append(runnerLabels, spec.Labels...)
	env = append(env, corev1.EnvVar{
		Name:  "RUNNER_LABELS",
		Value: strings.Join(runnerLabels, ","),
	})

	if spec.RunnerGroup != "" {
		env = append(env, corev1.EnvVar{
			Name:  "RUNNER_GROUP",
			Value: spec.RunnerGroup,
		})
	}

	// The runner stays alive between jobs (non-ephemeral) so it keeps
	// polling GitHub for the developer's next push
	env = append(env, corev1.EnvVar{
		Name:  "RUNNER_EPHEMERAL",
		Value: "false",
	})

	// Append user-supplied extra env vars
	env = append(env, spec.Env...)

	// ── Build the runner container ────────────────────────────────────
	runnerImage := spec.RunnerImage
	if runnerImage == "" {
		runnerImage = "ghcr.io/actions/actions-runner:latest"
	}

	// The official actions-runner image ships config.sh and run.sh but has
	// no entrypoint that reads environment variables.  We provide a small
	// inline startup script that:
	//   1. Exchanges the GitHub PAT for a short-lived registration token
	//   2. Calls config.sh to register the runner with GitHub
	//   3. Sets up a SIGTERM trap so the runner de-registers on pod shutdown
	//   4. Execs run.sh to start polling for jobs
	startupScript := `#!/bin/bash
set -uo pipefail

# ── Exchange PAT for a short-lived runner registration token ──────
echo "🔑 Exchanging PAT for runner registration token..."
echo "   API: ${GITHUB_API_URL}/repos/${RUNNER_REPO_SLUG}/actions/runners/registration-token"

HTTP_CODE=$(curl -sS -o /tmp/reg_response.json -w '%{http_code}' -X POST \
  -H "Authorization: Bearer ${GITHUB_PAT}" \
  -H "Accept: application/vnd.github+json" \
  "${GITHUB_API_URL}/repos/${RUNNER_REPO_SLUG}/actions/runners/registration-token") || true

echo "   HTTP status: ${HTTP_CODE}"

if [ "${HTTP_CODE}" != "201" ]; then
  echo "❌ GitHub API returned HTTP ${HTTP_CODE}:"
  cat /tmp/reg_response.json 2>/dev/null || echo "(no response body)"
  echo ""
  echo "Make sure your PAT has the 'repo' scope (classic) or"
  echo "'administration:write' permission (fine-grained)."
  exit 1
fi

RUNNER_TOKEN=$(grep -o '"token": *"[^"]*"' /tmp/reg_response.json | head -1 | cut -d'"' -f4)
rm -f /tmp/reg_response.json

if [ -z "${RUNNER_TOKEN}" ]; then
  echo "❌ Could not parse registration token from response"
  exit 1
fi
echo "✅ Registration token obtained (expires in ~1 hour)"

# De-register the runner on shutdown so it doesn't leave a ghost entry.
# Obtain a fresh removal token since the registration token may have expired.
cleanup() {
  echo "🛑 Removing runner..."
  REMOVE_TOKEN=$(curl -sS -X POST \
    -H "Authorization: Bearer ${GITHUB_PAT}" \
    -H "Accept: application/vnd.github+json" \
    "${GITHUB_API_URL}/repos/${RUNNER_REPO_SLUG}/actions/runners/remove-token" 2>/dev/null \
    | grep -o '"token": *"[^"]*"' | head -1 | cut -d'"' -f4) || true
  ./config.sh remove --token "${REMOVE_TOKEN:-${RUNNER_TOKEN}}" || true
}
trap cleanup SIGTERM SIGINT

# Build a runner name that fits GitHub's 64-char limit
RUNNER_NAME="${RUNNER_NAME_PREFIX}-$(hostname | rev | cut -d- -f1,2 | rev)"
RUNNER_NAME="${RUNNER_NAME:0:64}"

# Configure the runner (non-interactive)
./config.sh \
  --url "${RUNNER_REPOSITORY_URL}" \
  --token "${RUNNER_TOKEN}" \
  --name "${RUNNER_NAME}" \
  --labels "${RUNNER_LABELS}" \
  --work "${RUNNER_WORKDIR}" \
  --unattended \
  --replace

# Start the runner (exec so PID 1 gets signals)
exec ./run.sh
`

	container := corev1.Container{
		Name:    "runner",
		Image:   runnerImage,
		Command: []string{"/bin/bash", "-c", startupScript},
		Env:     env,
		VolumeMounts: append([]corev1.VolumeMount{
			{
				Name:      "builds",
				MountPath: "/builds",
			},
		}, spec.VolumeMounts...),
	}

	if spec.Resources != nil {
		container.Resources = buildRunnerResourceRequirements(spec.Resources)
	}

	// ── Build-agent sidecar ───────────────────────────────────────────
	// This sidecar has kubectl pre-installed and watches /builds for build
	// requests. The GH Actions workflow writes tarballs + trigger files
	// there; the sidecar pipes them into one-shot Kaniko executor pods.
	// No permissions juggling in the runner container required.
	buildAgentScript := `#!/bin/bash
set -uo pipefail

BUILDS_DIR=/builds
echo "🔧 Build-agent sidecar ready, watching ${BUILDS_DIR}..."

while true; do
  # ── Handle image build requests (.request) ─────────────────────
  for req in ${BUILDS_DIR}/*.request; do
    [ -f "$req" ] || continue
    SERVICE="$(basename "$req" .request)"
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "📦 Build request: ${SERVICE}"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    DEST=$(cat "${BUILDS_DIR}/${SERVICE}.dest")
    echo "   destination: ${DEST}"

    mv "$req" "${req}.processing"

    kubectl delete pod "kaniko-${SERVICE}" 2>/dev/null || true

    echo "   launching kaniko pod..."
    cat "${BUILDS_DIR}/${SERVICE}.tar.gz" | kubectl run "kaniko-${SERVICE}" \
      --rm -i --restart=Never \
      --image=gcr.io/kaniko-project/executor:latest \
      -- --context=tar://stdin \
         --destination="${DEST}" \
         --insecure \
         --cache=true \
         --cache-repo=registry:5000/cache \
      > "${BUILDS_DIR}/${SERVICE}.log" 2>&1
    EXIT_CODE=$?

    echo "${EXIT_CODE}" > "${BUILDS_DIR}/${SERVICE}.exitcode"
    touch "${BUILDS_DIR}/${SERVICE}.done"
    rm -f "${req}.processing"

    if [ ${EXIT_CODE} -eq 0 ]; then
      echo "   ✅ ${SERVICE} → ${DEST}"
    else
      echo "   ❌ ${SERVICE} build failed (exit ${EXIT_CODE}):"
      tail -20 "${BUILDS_DIR}/${SERVICE}.log"
    fi
  done

  # ── Handle kubectl apply requests (.apply) ─────────────────────
  for req in ${BUILDS_DIR}/*.apply; do
    [ -f "$req" ] || continue
    NAME="$(basename "$req" .apply)"
    echo ""
    echo "📋 Apply request: ${NAME}"

    mv "$req" "${req}.processing"

    kubectl apply -f "${BUILDS_DIR}/${NAME}.yaml" \
      > "${BUILDS_DIR}/${NAME}.apply-log" 2>&1
    EXIT_CODE=$?

    echo "${EXIT_CODE}" > "${BUILDS_DIR}/${NAME}.apply-exitcode"
    touch "${BUILDS_DIR}/${NAME}.apply-done"
    rm -f "${req}.processing"

    if [ ${EXIT_CODE} -eq 0 ]; then
      echo "   ✅ ${NAME} applied"
    else
      echo "   ❌ ${NAME} apply failed (exit ${EXIT_CODE}):"
      cat "${BUILDS_DIR}/${NAME}.apply-log"
    fi
  done

  # ── Handle kubectl exec requests (.kubectl) ────────────────────
  for req in ${BUILDS_DIR}/*.kubectl; do
    [ -f "$req" ] || continue
    NAME="$(basename "$req" .kubectl)"
    echo ""
    echo "🔧 kubectl request: ${NAME}"

    mv "$req" "${req}.processing"

    bash "${BUILDS_DIR}/${NAME}.sh" \
      > "${BUILDS_DIR}/${NAME}.kubectl-log" 2>&1
    EXIT_CODE=$?

    echo "${EXIT_CODE}" > "${BUILDS_DIR}/${NAME}.kubectl-exitcode"
    touch "${BUILDS_DIR}/${NAME}.kubectl-done"
    rm -f "${req}.processing"

    if [ ${EXIT_CODE} -eq 0 ]; then
      echo "   ✅ ${NAME} completed"
    else
      echo "   ❌ ${NAME} failed (exit ${EXIT_CODE}):"
      cat "${BUILDS_DIR}/${NAME}.kubectl-log"
    fi
  done

  sleep 1
done
`

	buildAgent := corev1.Container{
		Name:    "build-agent",
		Image:   "bitnami/kubectl:latest",
		Command: []string{"/bin/bash", "-c", buildAgentScript},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "builds",
				MountPath: "/builds",
			},
		},
	}

	// ── Build the pod spec (single-node Kind, no anti-affinity needed) ─
	// The runner container handles GH Actions jobs. The build-agent
	// sidecar handles all kubectl/Kaniko work via a shared /builds volume.
	podSpec := corev1.PodSpec{
		Containers:         []corev1.Container{container, buildAgent},
		ServiceAccountName: saName,
		Volumes: append([]corev1.Volume{
			{
				Name: "builds",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}, spec.Volumes...),
		TerminationGracePeriodSeconds: int64Ptr(30),
	}

	// Name the deployment after the username so it's obvious in `kubectl get deploy`
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-runner", spec.GitHubUsername),
			Namespace: cr.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				runnerPoolHashAnnotation: computeRunnerPoolHash(cr.Spec),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Status
// ────────────────────────────────────────────────────────────────────────────

func (r *GithubActionRunnerPoolReconciler) updateRunnerPoolStatus(ctx context.Context, cr *appsv1alpha1.GithubActionRunnerPool) error {
	deploy := &appsv1.Deployment{}
	deployName := fmt.Sprintf("%s-runner", cr.Spec.GitHubUsername)
	deployKey := types.NamespacedName{Name: deployName, Namespace: cr.Namespace}
	if err := r.Get(ctx, deployKey, deploy); err == nil {
		cr.Status.Replicas = *deploy.Spec.Replicas
		cr.Status.ReadyRunners = deploy.Status.AvailableReplicas

		deployReady := deploy.Status.AvailableReplicas > 0 &&
			deploy.Status.AvailableReplicas == deploy.Status.Replicas

		if deployReady {
			cr.Status.RunnerRegistered = true
			meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "RunnerReady",
				Message: fmt.Sprintf("Runner for %s is online and polling for jobs", cr.Spec.GitHubUsername),
			})
		} else {
			cr.Status.RunnerRegistered = false
			meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "RunnerNotReady",
				Message: fmt.Sprintf("Runner for %s is starting up (%d/%d ready)", cr.Spec.GitHubUsername, deploy.Status.AvailableReplicas, *deploy.Spec.Replicas),
			})
		}
	} else {
		cr.Status.Replicas = 0
		cr.Status.ReadyRunners = 0
		cr.Status.RunnerRegistered = false
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "DeploymentMissing",
			Message: "Runner deployment does not exist yet",
		})
	}

	return r.Status().Update(ctx, cr)
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func labelsForRunnerPool(cr *appsv1alpha1.GithubActionRunnerPool) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":           cr.Name,
		"app.kubernetes.io/component":      "github-actions-runner",
		"app.kubernetes.io/managed-by":     "githubactionrunnerpool-operator",
		"app.kubernetes.io/instance":       cr.Name,
		"apps.example.com/github-username": cr.Spec.GitHubUsername,
	}
}

func buildRunnerResourceRequirements(res *appsv1alpha1.RunnerResourceRequirements) corev1.ResourceRequirements {
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

func computeRunnerPoolHash(obj interface{}) string {
	data, _ := json.Marshal(obj)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

func int64Ptr(v int64) *int64 {
	return &v
}

// githubAPIURL returns the REST API base URL for a given GitHub instance.
// For github.com it returns "https://api.github.com".
// For GitHub Enterprise Server (e.g. "https://git.corp.com") it returns
// "https://git.corp.com/api/v3".
func githubAPIURL(githubURL string) string {
	githubURL = strings.TrimRight(githubURL, "/")
	if githubURL == "https://github.com" || githubURL == "" {
		return "https://api.github.com"
	}
	return githubURL + "/api/v3"
}

// SetupWithManager sets up the controller with the Manager.
// It watches GithubActionRunnerPool (primary) and Deployments that it owns.
func (r *GithubActionRunnerPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("githubactionrunnerpool-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.GithubActionRunnerPool{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

// recordEvent safely emits a Kubernetes Event on the CR.
func (r *GithubActionRunnerPoolReconciler) recordEvent(cr *appsv1alpha1.GithubActionRunnerPool, eventType, reason, messageFmt string, args ...interface{}) {
	if r.Recorder != nil {
		r.Recorder.Eventf(cr, eventType, reason, messageFmt, args...)
	}
}
