// ── Kubernetes API types (simplified for dashboard) ─────────────

export interface K8sMetadata {
  name: string;
  namespace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  creationTimestamp?: string;
  uid?: string;
}

export interface K8sCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

// ── Nodes ───────────────────────────────────────────────────────

export interface K8sNode {
  metadata: K8sMetadata;
  status: {
    conditions?: K8sCondition[];
    nodeInfo?: {
      kubeletVersion?: string;
      osImage?: string;
      containerRuntimeVersion?: string;
      architecture?: string;
    };
    addresses?: { type: string; address: string }[];
    capacity?: Record<string, string>;
    allocatable?: Record<string, string>;
  };
}

// ── Pods ────────────────────────────────────────────────────────

export interface K8sContainerStatus {
  name: string;
  ready: boolean;
  restartCount: number;
  state?: {
    running?: { startedAt?: string };
    waiting?: { reason?: string; message?: string };
    terminated?: { reason?: string; exitCode?: number };
  };
  image?: string;
}

export interface K8sPod {
  metadata: K8sMetadata;
  spec: {
    nodeName?: string;
    containers?: { name: string; image: string; ports?: { containerPort: number }[] }[];
  };
  status: {
    phase?: string;
    conditions?: K8sCondition[];
    containerStatuses?: K8sContainerStatus[];
    startTime?: string;
  };
}

// ── Deployments ─────────────────────────────────────────────────

export interface K8sDeployment {
  metadata: K8sMetadata;
  spec: {
    replicas?: number;
    selector?: { matchLabels?: Record<string, string> };
    template?: {
      spec?: {
        containers?: { name: string; image: string; ports?: { containerPort: number }[] }[];
      };
    };
  };
  status: {
    replicas?: number;
    readyReplicas?: number;
    updatedReplicas?: number;
    availableReplicas?: number;
    unavailableReplicas?: number;
    conditions?: K8sCondition[];
  };
}

// ── Services ────────────────────────────────────────────────────

export interface K8sService {
  metadata: K8sMetadata;
  spec: {
    type?: string;
    clusterIP?: string;
    ports?: { port: number; targetPort: number | string; protocol?: string; nodePort?: number }[];
    selector?: Record<string, string>;
  };
}

// ── Ingresses ───────────────────────────────────────────────────

export interface K8sIngress {
  metadata: K8sMetadata;
  spec: {
    ingressClassName?: string;
    rules?: {
      host?: string;
      http?: {
        paths?: {
          path?: string;
          pathType?: string;
          backend?: {
            service?: { name: string; port: { number?: number; name?: string } };
          };
        }[];
      };
    }[];
    tls?: { hosts?: string[]; secretName?: string }[];
  };
  status?: {
    loadBalancer?: {
      ingress?: { hostname?: string; ip?: string }[];
    };
  };
}

// ── Secrets ─────────────────────────────────────────────────────

export interface K8sSecret {
  metadata: K8sMetadata;
  type?: string;
  data?: Record<string, string>;
}

// ── Events ──────────────────────────────────────────────────────

export interface K8sEvent {
  metadata: K8sMetadata;
  type?: string;
  reason?: string;
  message?: string;
  involvedObject?: {
    kind?: string;
    name?: string;
    namespace?: string;
  };
  firstTimestamp?: string;
  lastTimestamp?: string;
  count?: number;
  source?: { component?: string };
}

// ── DSE ─────────────────────────────────────────────────────────

export interface DSE {
  metadata: K8sMetadata;
  spec: {
    deployment: {
      image: string;
      port: number;
      replicas?: number;
      env?: { name: string; value?: string }[];
      healthCheck?: { path?: string; port?: number };
    };
    service: {
      port: number;
      targetPort?: number;
      type?: string;
    };
    ingress?: {
      enabled?: boolean;
      host?: string;
      path?: string;
    };
    dependencies?: {
      type: string;
      version?: string;
      port?: number;
      envVarName?: string;
    }[];
  };
  status?: {
    availableReplicas?: number;
    deploymentReady?: boolean;
    serviceReady?: boolean;
    ingressReady?: boolean;
    dependenciesReady?: boolean;
    externalURL?: string;
    conditions?: K8sCondition[];
  };
}

// ── Runner Pool ─────────────────────────────────────────────────

export interface RunnerPool {
  metadata: K8sMetadata;
  spec: {
    githubUsername: string;
    repository: string;
    replicas?: number;
    runnerImage?: string;
    labels?: string[];
  };
  status?: {
    replicas?: number;
    readyRunners?: number;
    runnerRegistered?: boolean;
    activeJob?: string;
    lastJobCompleted?: string;
    devEnvironmentRef?: string;
    conditions?: K8sCondition[];
  };
}

// ── Cluster Info ────────────────────────────────────────────────

export interface ClusterInfo {
  name: string;
  exists: boolean;
  context: string;
  operator?: K8sDeployment;
  registry?: K8sDeployment;
}

// ── K8s List wrapper ────────────────────────────────────────────

export interface K8sList<T> {
  items: T[];
}
