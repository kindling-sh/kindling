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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appsv1alpha1 "github.com/jeffvincent/kindling/api/v1alpha1"
	"github.com/jeffvincent/kindling/pkg/ci"
)

// ────────────────────────────────────────────────────────────────────────────
// Pure-function unit tests
// ────────────────────────────────────────────────────────────────────────────

var _ = Describe("RunnerAdapter.APIBaseURL", func() {
	var adapter ci.RunnerAdapter
	BeforeEach(func() {
		adapter = ci.Default().Runner()
	})

	It("returns api.github.com for github.com", func() {
		Expect(adapter.APIBaseURL("https://github.com")).To(Equal("https://api.github.com"))
	})

	It("returns api.github.com for empty string", func() {
		Expect(adapter.APIBaseURL("")).To(Equal("https://api.github.com"))
	})

	It("returns /api/v3 for GHE", func() {
		Expect(adapter.APIBaseURL("https://git.corp.com")).To(Equal("https://git.corp.com/api/v3"))
	})

	It("trims trailing slashes", func() {
		Expect(adapter.APIBaseURL("https://github.com/")).To(Equal("https://api.github.com"))
	})
})

var _ = Describe("RunnerAdapter.RunnerLabels", func() {
	It("includes the github username", func() {
		adapter := ci.Default().Runner()
		labels := adapter.RunnerLabels("testuser", "test-pool")
		Expect(labels).To(HaveKeyWithValue("apps.example.com/github-username", "testuser"))
		Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", "github-actions-runner"))
	})
})

var _ = Describe("RunnerAdapter.ServiceAccountName", func() {
	It("returns username-runner", func() {
		adapter := ci.Default().Runner()
		Expect(adapter.ServiceAccountName("jeff")).To(Equal("jeff-runner"))
	})
})

var _ = Describe("buildRunnerDeployment", func() {
	var r *GithubActionRunnerPoolReconciler

	BeforeEach(func() {
		r = &GithubActionRunnerPoolReconciler{}
	})

	It("builds a deployment named username-runner", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		Expect(deploy.Name).To(Equal("jeff-runner"))
	})

	It("has two containers: runner and build-agent", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		containers := deploy.Spec.Template.Spec.Containers
		Expect(containers).To(HaveLen(2))
		Expect(containers[0].Name).To(Equal("runner"))
		Expect(containers[1].Name).To(Equal("build-agent"))
	})

	It("uses the default runner image when not specified", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("ghcr.io/actions/actions-runner:latest"))
	})

	It("uses a custom runner image when specified", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		cr.Spec.RunnerImage = "my-runner:v1"
		deploy := r.buildRunnerDeployment(cr)
		Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("my-runner:v1"))
	})

	It("injects GITHUB_PAT from the secret ref", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		container := deploy.Spec.Template.Spec.Containers[0]

		var patEnv *corev1.EnvVar
		for i, e := range container.Env {
			if e.Name == "GITHUB_PAT" {
				patEnv = &container.Env[i]
				break
			}
		}
		Expect(patEnv).NotTo(BeNil())
		Expect(patEnv.ValueFrom.SecretKeyRef.Name).To(Equal("test-secret"))
		Expect(patEnv.ValueFrom.SecretKeyRef.Key).To(Equal("github-token"))
	})

	It("sets RUNNER_LABELS with self-hosted and username", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		container := deploy.Spec.Template.Spec.Containers[0]

		labelsEnv := findEnvVar(container.Env, "RUNNER_LABELS")
		Expect(labelsEnv).To(ContainSubstring("self-hosted"))
		Expect(labelsEnv).To(ContainSubstring("jeff"))
	})

	It("mounts a shared /builds volume", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)

		volumes := deploy.Spec.Template.Spec.Volumes
		var buildsVol *corev1.Volume
		for i, v := range volumes {
			if v.Name == "builds" {
				buildsVol = &volumes[i]
				break
			}
		}
		Expect(buildsVol).NotTo(BeNil())
		Expect(buildsVol.VolumeSource.EmptyDir).NotTo(BeNil())

		// Both containers should mount it
		for _, c := range deploy.Spec.Template.Spec.Containers {
			var found bool
			for _, vm := range c.VolumeMounts {
				if vm.Name == "builds" && vm.MountPath == "/builds" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "container %s should mount /builds", c.Name)
		}
	})

	It("sets the service account name", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal("jeff-runner"))
	})

	It("has a spec-hash annotation", func() {
		cr := newTestRunnerPool("test-pool", "jeff", "jeff/repo")
		deploy := r.buildRunnerDeployment(cr)
		Expect(deploy.Annotations).To(HaveKey(runnerPoolHashAnnotation))
	})
})

// ────────────────────────────────────────────────────────────────────────────
// Integration tests (envtest)
// ────────────────────────────────────────────────────────────────────────────

var _ = Describe("GithubActionRunnerPool Reconciler", func() {
	const timeout = time.Second * 30
	const interval = time.Millisecond * 250

	ctx := context.Background()

	Context("when a valid CR with a matching secret is created", func() {
		var cr *appsv1alpha1.GithubActionRunnerPool
		var secret *corev1.Secret

		BeforeEach(func() {
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "runner-token-integ",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"github-token": []byte("ghp_fake_token_for_test"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			cr = newTestRunnerPool("integ-pool", "integuser", "integuser/repo")
			cr.Spec.TokenSecretRef.Name = "runner-token-integ"
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, cr)
			_ = k8sClient.Delete(ctx, secret)
			// Clean up cluster-scoped RBAC
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "integuser-runner"}})
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "integuser-runner"}})
		})

		It("should create a runner Deployment", func() {
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "integuser-runner", Namespace: "default"}, deploy)
			}, timeout, interval).Should(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(2))
		})

		It("should create a ServiceAccount", func() {
			sa := &corev1.ServiceAccount{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "integuser-runner", Namespace: "default"}, sa)
			}, timeout, interval).Should(Succeed())
		})

		It("should create a ClusterRole with required permissions", func() {
			role := &rbacv1.ClusterRole{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "integuser-runner"}, role)
			}, timeout, interval).Should(Succeed())

			// Should have rules for pods, deployments, DSE CRs, and ingresses
			Expect(len(role.Rules)).To(BeNumerically(">=", 3))
		})

		It("should create a ClusterRoleBinding", func() {
			crb := &rbacv1.ClusterRoleBinding{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "integuser-runner"}, crb)
			}, timeout, interval).Should(Succeed())
			Expect(crb.Subjects[0].Name).To(Equal("integuser-runner"))
			Expect(crb.RoleRef.Name).To(Equal("integuser-runner"))
		})
	})

	Context("when the secret is missing", func() {
		var cr *appsv1alpha1.GithubActionRunnerPool

		BeforeEach(func() {
			cr = newTestRunnerPool("nosecret-pool", "nosecretuser", "nosecretuser/repo")
			cr.Spec.TokenSecretRef.Name = "nonexistent-secret"
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, cr)
		})

		It("should set a SecretNotFound condition", func() {
			Eventually(func() string {
				updated := &appsv1alpha1.GithubActionRunnerPool{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, updated); err != nil {
					return ""
				}
				for _, c := range updated.Status.Conditions {
					if c.Type == "Ready" && c.Reason == "SecretNotFound" {
						return c.Reason
					}
				}
				return ""
			}, timeout, interval).Should(Equal("SecretNotFound"))
		})
	})
})

// ────────────────────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────────────────────

func newTestRunnerPool(name, username, repo string) *appsv1alpha1.GithubActionRunnerPool {
	replicas := int32(1)
	return &appsv1alpha1.GithubActionRunnerPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: appsv1alpha1.GithubActionRunnerPoolSpec{
			GitHubUsername: username,
			Repository:     repo,
			Replicas:       &replicas,
			TokenSecretRef: appsv1alpha1.SecretKeyRef{
				Name: "test-secret",
				Key:  "github-token",
			},
		},
	}
}
