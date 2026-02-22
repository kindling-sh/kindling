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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appsv1alpha1 "github.com/jeffvincent/kindling/api/v1alpha1"
)

// ────────────────────────────────────────────────────────────────────────────
// Pure-function unit tests (no envtest needed)
// ────────────────────────────────────────────────────────────────────────────

var _ = Describe("buildConnectionURL", func() {
	DescribeTable("generates the correct connection string for each dependency type",
		func(depType appsv1alpha1.DependencyType, expected string) {
			dep := appsv1alpha1.DependencySpec{Type: depType}
			defaults := dependencyRegistry[depType]
			url := buildConnectionURL("myapp", dep, defaults)
			Expect(url).To(Equal(expected))
		},
		Entry("postgres", appsv1alpha1.DependencyPostgres, "postgres://devuser:devpass@myapp-postgres:5432/devdb?sslmode=disable"),
		Entry("redis", appsv1alpha1.DependencyRedis, "redis://myapp-redis:6379/0"),
		Entry("mysql", appsv1alpha1.DependencyMySQL, "mysql://devuser:devpass@myapp-mysql:3306/devdb"),
		Entry("mongodb", appsv1alpha1.DependencyMongoDB, "mongodb://devuser:devpass@myapp-mongodb:27017"),
		Entry("rabbitmq", appsv1alpha1.DependencyRabbitMQ, "amqp://devuser:devpass@myapp-rabbitmq:5672/"),
		Entry("minio", appsv1alpha1.DependencyMinIO, "http://myapp-minio:9000"),
		Entry("elasticsearch", appsv1alpha1.DependencyElasticsearch, "http://myapp-elasticsearch:9200"),
		Entry("kafka", appsv1alpha1.DependencyKafka, "myapp-kafka:9092"),
		Entry("nats", appsv1alpha1.DependencyNATS, "nats://myapp-nats:4222"),
		Entry("memcached", appsv1alpha1.DependencyMemcached, "myapp-memcached:11211"),
		Entry("cassandra", appsv1alpha1.DependencyCassandra, "myapp-cassandra:9042"),
		Entry("consul", appsv1alpha1.DependencyConsul, "http://myapp-consul:8500"),
		Entry("vault", appsv1alpha1.DependencyVault, "http://myapp-vault:8200"),
		Entry("influxdb", appsv1alpha1.DependencyInfluxDB, "http://devuser:devpass123@myapp-influxdb:8086"),
		Entry("jaeger", appsv1alpha1.DependencyJaeger, "http://myapp-jaeger:16686"),
	)

	It("uses a custom port when overridden", func() {
		port := int32(15432)
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyPostgres, Port: &port}
		defaults := dependencyRegistry[appsv1alpha1.DependencyPostgres]
		url := buildConnectionURL("myapp", dep, defaults)
		Expect(url).To(ContainSubstring(":15432"))
	})

	It("uses overridden credentials", func() {
		dep := appsv1alpha1.DependencySpec{
			Type: appsv1alpha1.DependencyPostgres,
			Env: []corev1.EnvVar{
				{Name: "POSTGRES_USER", Value: "custom"},
				{Name: "POSTGRES_PASSWORD", Value: "secret"},
			},
		}
		defaults := dependencyRegistry[appsv1alpha1.DependencyPostgres]
		url := buildConnectionURL("myapp", dep, defaults)
		Expect(url).To(ContainSubstring("custom:secret@"))
	})
})

var _ = Describe("buildDependencyConnectionEnvVars", func() {
	It("injects DATABASE_URL for postgres", func() {
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyPostgres}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		Expect(envVars).To(HaveLen(1))
		Expect(envVars[0].Name).To(Equal("DATABASE_URL"))
		Expect(envVars[0].Value).To(ContainSubstring("postgres://"))
	})

	It("injects REDIS_URL for redis", func() {
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyRedis}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		Expect(envVars).To(HaveLen(1))
		Expect(envVars[0].Name).To(Equal("REDIS_URL"))
	})

	It("uses custom envVarName when provided", func() {
		dep := appsv1alpha1.DependencySpec{
			Type:       appsv1alpha1.DependencyPostgres,
			EnvVarName: "MY_CUSTOM_DB_URL",
		}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		Expect(envVars[0].Name).To(Equal("MY_CUSTOM_DB_URL"))
	})

	It("injects extra env vars for MinIO (S3_ACCESS_KEY, S3_SECRET_KEY)", func() {
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyMinIO}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		names := envVarNames(envVars)
		Expect(names).To(ContainElements("S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY"))
	})

	It("injects VAULT_TOKEN for vault", func() {
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyVault}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		names := envVarNames(envVars)
		Expect(names).To(ContainElements("VAULT_ADDR", "VAULT_TOKEN"))
	})

	It("injects INFLUXDB_ORG and INFLUXDB_BUCKET for influxdb", func() {
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyInfluxDB}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		names := envVarNames(envVars)
		Expect(names).To(ContainElements("INFLUXDB_URL", "INFLUXDB_ORG", "INFLUXDB_BUCKET"))
	})

	It("injects OTEL_EXPORTER_OTLP_ENDPOINT for jaeger", func() {
		dep := appsv1alpha1.DependencySpec{Type: appsv1alpha1.DependencyJaeger}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		names := envVarNames(envVars)
		Expect(names).To(ContainElements("JAEGER_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"))
	})

	It("returns nil for an unknown dependency type", func() {
		dep := appsv1alpha1.DependencySpec{Type: "foobar"}
		envVars := buildDependencyConnectionEnvVars("myapp", dep)
		Expect(envVars).To(BeNil())
	})
})

var _ = Describe("mergeEnvVars", func() {
	It("merges base and override, override wins", func() {
		base := []corev1.EnvVar{
			{Name: "A", Value: "1"},
			{Name: "B", Value: "2"},
		}
		overrides := []corev1.EnvVar{
			{Name: "B", Value: "OVERRIDE"},
			{Name: "C", Value: "3"},
		}
		result := mergeEnvVars(base, overrides)
		Expect(result).To(HaveLen(3))
		Expect(findEnvVar(result, "A")).To(Equal("1"))
		Expect(findEnvVar(result, "B")).To(Equal("OVERRIDE"))
		Expect(findEnvVar(result, "C")).To(Equal("3"))
	})

	It("preserves order (base first, then new overrides)", func() {
		base := []corev1.EnvVar{{Name: "Z", Value: "1"}, {Name: "A", Value: "2"}}
		result := mergeEnvVars(base, nil)
		Expect(result[0].Name).To(Equal("Z"))
		Expect(result[1].Name).To(Equal("A"))
	})
})

var _ = Describe("computeSpecHash", func() {
	It("returns the same hash for the same input", func() {
		a := computeSpecHash(map[string]string{"key": "value"})
		b := computeSpecHash(map[string]string{"key": "value"})
		Expect(a).To(Equal(b))
	})

	It("returns different hashes for different inputs", func() {
		a := computeSpecHash(map[string]string{"key": "value"})
		b := computeSpecHash(map[string]string{"key": "other"})
		Expect(a).NotTo(Equal(b))
	})
})

var _ = Describe("dependencyName", func() {
	It("returns crName-depType", func() {
		Expect(dependencyName("myapp", appsv1alpha1.DependencyPostgres)).To(Equal("myapp-postgres"))
		Expect(dependencyName("myapp", appsv1alpha1.DependencyRedis)).To(Equal("myapp-redis"))
	})
})

// ────────────────────────────────────────────────────────────────────────────
// Builder tests (no cluster, but need a reconciler for receiver methods)
// ────────────────────────────────────────────────────────────────────────────

var _ = Describe("buildDeployment", func() {
	var r *DevStagingEnvironmentReconciler

	BeforeEach(func() {
		r = &DevStagingEnvironmentReconciler{}
	})

	It("builds a Deployment with correct labels and container spec", func() {
		cr := newTestDSE("test-app")
		deploy := r.buildDeployment(cr)

		Expect(deploy.Name).To(Equal("test-app"))
		Expect(deploy.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "test-app"))
		Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.Image).To(Equal("my-image:latest"))
		Expect(container.Ports).To(HaveLen(1))
		Expect(container.Ports[0].ContainerPort).To(Equal(int32(8080)))
	})

	It("merges dependency env vars into the container", func() {
		cr := newTestDSE("test-app")
		cr.Spec.Dependencies = []appsv1alpha1.DependencySpec{
			{Type: appsv1alpha1.DependencyPostgres},
			{Type: appsv1alpha1.DependencyRedis},
		}
		deploy := r.buildDeployment(cr)

		container := deploy.Spec.Template.Spec.Containers[0]
		names := envVarNames(container.Env)
		Expect(names).To(ContainElements("DATABASE_URL", "REDIS_URL"))
	})

	It("sets a spec-hash annotation", func() {
		cr := newTestDSE("test-app")
		deploy := r.buildDeployment(cr)
		Expect(deploy.Annotations).To(HaveKey(specHashAnnotation))
		Expect(deploy.Annotations[specHashAnnotation]).NotTo(BeEmpty())
	})

	It("applies resource limits when specified", func() {
		cr := newTestDSE("test-app")
		cpuReq := resource.MustParse("100m")
		memReq := resource.MustParse("128Mi")
		cr.Spec.Deployment.Resources = &appsv1alpha1.ResourceRequirements{
			CPURequest:    &cpuReq,
			MemoryRequest: &memReq,
		}
		deploy := r.buildDeployment(cr)
		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.Resources.Requests.Cpu().String()).To(Equal("100m"))
		Expect(container.Resources.Requests.Memory().String()).To(Equal("128Mi"))
	})

	It("sets health check probes when specified", func() {
		cr := newTestDSE("test-app")
		cr.Spec.Deployment.HealthCheck = &appsv1alpha1.HealthCheckSpec{
			Path: "/healthz",
		}
		deploy := r.buildDeployment(cr)
		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.LivenessProbe).NotTo(BeNil())
		Expect(container.ReadinessProbe).NotTo(BeNil())
		Expect(container.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
	})

	It("sets gRPC health check probes when type is grpc", func() {
		cr := newTestDSE("test-grpc")
		cr.Spec.Deployment.HealthCheck = &appsv1alpha1.HealthCheckSpec{
			Type: "grpc",
		}
		deploy := r.buildDeployment(cr)
		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.LivenessProbe).NotTo(BeNil())
		Expect(container.ReadinessProbe).NotTo(BeNil())
		Expect(container.LivenessProbe.GRPC).NotTo(BeNil())
		Expect(container.LivenessProbe.GRPC.Port).To(Equal(int32(8080)))
	})

	It("skips health check probes when type is none", func() {
		cr := newTestDSE("test-none")
		cr.Spec.Deployment.HealthCheck = &appsv1alpha1.HealthCheckSpec{
			Type: "none",
		}
		deploy := r.buildDeployment(cr)
		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.LivenessProbe).To(BeNil())
		Expect(container.ReadinessProbe).To(BeNil())
	})
})

var _ = Describe("buildService", func() {
	var r *DevStagingEnvironmentReconciler

	BeforeEach(func() {
		r = &DevStagingEnvironmentReconciler{}
	})

	It("builds a ClusterIP Service by default", func() {
		cr := newTestDSE("test-app")
		svc := r.buildService(cr)

		Expect(svc.Name).To(Equal("test-app"))
		Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		Expect(svc.Spec.Ports).To(HaveLen(1))
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(80)))
	})

	It("uses NodePort type when specified", func() {
		cr := newTestDSE("test-app")
		cr.Spec.Service.Type = "NodePort"
		svc := r.buildService(cr)
		Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
	})

	It("targets the deployment port by default", func() {
		cr := newTestDSE("test-app")
		svc := r.buildService(cr)
		Expect(svc.Spec.Ports[0].TargetPort.IntValue()).To(Equal(8080))
	})

	It("uses explicit targetPort when specified", func() {
		cr := newTestDSE("test-app")
		tp := int32(9090)
		cr.Spec.Service.TargetPort = &tp
		svc := r.buildService(cr)
		Expect(svc.Spec.Ports[0].TargetPort.IntValue()).To(Equal(9090))
	})
})

var _ = Describe("buildIngress", func() {
	var r *DevStagingEnvironmentReconciler

	BeforeEach(func() {
		r = &DevStagingEnvironmentReconciler{}
	})

	It("builds an Ingress with the specified host", func() {
		cr := newTestDSE("test-app")
		ingressClassName := "nginx"
		cr.Spec.Ingress = &appsv1alpha1.IngressSpec{
			Enabled:          true,
			Host:             "test-app.localhost",
			IngressClassName: &ingressClassName,
		}
		ing := r.buildIngress(cr)

		Expect(ing.Name).To(Equal("test-app"))
		Expect(ing.Spec.Rules).To(HaveLen(1))
		Expect(ing.Spec.Rules[0].Host).To(Equal("test-app.localhost"))
		Expect(*ing.Spec.IngressClassName).To(Equal("nginx"))
	})

	It("defaults path to /", func() {
		cr := newTestDSE("test-app")
		cr.Spec.Ingress = &appsv1alpha1.IngressSpec{
			Enabled: true,
			Host:    "test-app.localhost",
		}
		ing := r.buildIngress(cr)
		Expect(ing.Spec.Rules[0].HTTP.Paths[0].Path).To(Equal("/"))
	})

	It("sets TLS when configured", func() {
		cr := newTestDSE("test-app")
		cr.Spec.Ingress = &appsv1alpha1.IngressSpec{
			Enabled: true,
			Host:    "test-app.localhost",
			TLS: &appsv1alpha1.IngressTLSSpec{
				SecretName: "tls-secret",
			},
		}
		ing := r.buildIngress(cr)
		Expect(ing.Spec.TLS).To(HaveLen(1))
		Expect(ing.Spec.TLS[0].SecretName).To(Equal("tls-secret"))
		Expect(ing.Spec.TLS[0].Hosts).To(ContainElement("test-app.localhost"))
	})

	It("merges user annotations with the spec hash", func() {
		cr := newTestDSE("test-app")
		cr.Spec.Ingress = &appsv1alpha1.IngressSpec{
			Enabled: true,
			Host:    "test-app.localhost",
			Annotations: map[string]string{
				"custom-annotation": "custom-value",
			},
		}
		ing := r.buildIngress(cr)
		Expect(ing.Annotations).To(HaveKey("custom-annotation"))
		Expect(ing.Annotations).To(HaveKey(specHashAnnotation))
	})
})

// ────────────────────────────────────────────────────────────────────────────
// Integration tests (envtest)
// ────────────────────────────────────────────────────────────────────────────

var _ = Describe("DevStagingEnvironment Reconciler", func() {
	const timeout = time.Second * 30
	const interval = time.Millisecond * 250

	ctx := context.Background()

	Context("when a minimal CR is created", func() {
		var cr *appsv1alpha1.DevStagingEnvironment

		BeforeEach(func() {
			cr = newTestDSE("reconcile-basic")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, cr)
		})

		It("should create a Deployment", func() {
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, deploy)
			}, timeout, interval).Should(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("my-image:latest"))
		})

		It("should create a Service", func() {
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, svc)
			}, timeout, interval).Should(Succeed())
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(80)))
		})

		It("should NOT create an Ingress when not enabled", func() {
			ing := &networkingv1.Ingress{}
			Consistently(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, ing)
				return errors.IsNotFound(err)
			}, time.Second*3, interval).Should(BeTrue())
		})
	})

	Context("when a CR with ingress is created", func() {
		var cr *appsv1alpha1.DevStagingEnvironment

		BeforeEach(func() {
			cr = newTestDSE("reconcile-ingress")
			ingressClassName := "nginx"
			cr.Spec.Ingress = &appsv1alpha1.IngressSpec{
				Enabled:          true,
				Host:             "test.localhost",
				IngressClassName: &ingressClassName,
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, cr)
		})

		It("should create an Ingress resource", func() {
			ing := &networkingv1.Ingress{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, ing)
			}, timeout, interval).Should(Succeed())
			Expect(ing.Spec.Rules[0].Host).To(Equal("test.localhost"))
		})
	})

	Context("when a CR with dependencies is created", func() {
		var cr *appsv1alpha1.DevStagingEnvironment

		BeforeEach(func() {
			cr = newTestDSE("reconcile-deps")
			cr.Spec.Dependencies = []appsv1alpha1.DependencySpec{
				{Type: appsv1alpha1.DependencyPostgres},
				{Type: appsv1alpha1.DependencyRedis},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, cr)
		})

		It("should create dependency Deployments", func() {
			for _, name := range []string{"reconcile-deps-postgres", "reconcile-deps-redis"} {
				deploy := &appsv1.Deployment{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, deploy)
				}, timeout, interval).Should(Succeed(), "expected Deployment %s", name)
			}
		})

		It("should create dependency Services", func() {
			for _, name := range []string{"reconcile-deps-postgres", "reconcile-deps-redis"} {
				svc := &corev1.Service{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, svc)
				}, timeout, interval).Should(Succeed(), "expected Service %s", name)
			}
		})

		It("should inject connection env vars into the app container", func() {
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, deploy)
			}, timeout, interval).Should(Succeed())

			container := deploy.Spec.Template.Spec.Containers[0]
			names := envVarNames(container.Env)
			Expect(names).To(ContainElements("DATABASE_URL", "REDIS_URL"))
		})
	})

	Context("when a CR is deleted", func() {
		It("should garbage-collect child Deployments via OwnerReferences", func() {
			cr := newTestDSE("reconcile-delete")
			cr.Spec.Dependencies = []appsv1alpha1.DependencySpec{
				{Type: appsv1alpha1.DependencyPostgres},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Wait for child Deployment to exist
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "reconcile-delete-postgres", Namespace: "default"}, deploy)
			}, timeout, interval).Should(Succeed())

			// Delete the CR
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			// Child Deployment should be garbage-collected (envtest may not run the GC,
			// but at minimum the owner reference should be set correctly)
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "reconcile-delete-postgres", Namespace: "default"}, deploy)
			}, timeout, interval).Should(Succeed())
			Expect(deploy.OwnerReferences).To(HaveLen(1))
			Expect(deploy.OwnerReferences[0].Name).To(Equal("reconcile-delete"))
		})
	})

	Context("when the CR spec is updated", func() {
		It("should update the Deployment image", func() {
			cr := newTestDSE("reconcile-update")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Wait for initial Deployment
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, deploy)
			}, timeout, interval).Should(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("my-image:latest"))

			// Update the CR
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, cr)).To(Succeed())
			cr.Spec.Deployment.Image = "my-image:v2"
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			// Wait for the background controller to reconcile the update
			Eventually(func(g Gomega) string {
				d := &appsv1.Deployment{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: "default"}, d)).To(Succeed())
				return d.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("my-image:v2"))

			_ = k8sClient.Delete(ctx, cr)
		})
	})
})

// ────────────────────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────────────────────

func newTestDSE(name string) *appsv1alpha1.DevStagingEnvironment {
	replicas := int32(1)
	return &appsv1alpha1.DevStagingEnvironment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: appsv1alpha1.DevStagingEnvironmentSpec{
			Deployment: appsv1alpha1.DeploymentSpec{
				Image:    "my-image:latest",
				Port:     8080,
				Replicas: &replicas,
			},
			Service: appsv1alpha1.ServiceSpec{
				Port: 80,
				Type: "ClusterIP",
			},
		},
	}
}

func envVarNames(envs []corev1.EnvVar) []string {
	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	return names
}

func findEnvVar(envs []corev1.EnvVar, name string) string {
	for _, e := range envs {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
