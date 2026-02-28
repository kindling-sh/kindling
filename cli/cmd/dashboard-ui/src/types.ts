// ── Kubernetes API types (simplified for dashboard) ─────────────

export interface K8sMetadata {
  name: string;
  namespace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  creationTimestamp?: string;
  uid?: string;
  ownerReferences?: {
    apiVersion: string;
    kind: string;
    name: string;
    uid: string;
  }[];
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

// ── Containers ──────────────────────────────────────────────────

export interface K8sContainerSpec {
  name: string;
  image: string;
  ports?: { containerPort: number; protocol?: string }[];
  env?: { name: string; value?: string; valueFrom?: object }[];
  resources?: {
    requests?: Record<string, string>;
    limits?: Record<string, string>;
  };
  volumeMounts?: { name: string; mountPath: string; readOnly?: boolean }[];
  command?: string[];
  args?: string[];
}

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

// ── Pods ────────────────────────────────────────────────────────

export interface K8sPod {
  metadata: K8sMetadata;
  spec: {
    nodeName?: string;
    containers?: K8sContainerSpec[];
    initContainers?: K8sContainerSpec[];
    volumes?: { name: string; [key: string]: unknown }[];
    serviceAccountName?: string;
    restartPolicy?: string;
  };
  status: {
    phase?: string;
    conditions?: K8sCondition[];
    containerStatuses?: K8sContainerStatus[];
    initContainerStatuses?: K8sContainerStatus[];
    startTime?: string;
    podIP?: string;
    hostIP?: string;
  };
}

// ── ReplicaSets ─────────────────────────────────────────────────

export interface K8sReplicaSet {
  metadata: K8sMetadata;
  spec: {
    replicas?: number;
    selector?: { matchLabels?: Record<string, string> };
    template?: {
      spec?: {
        containers?: K8sContainerSpec[];
      };
    };
  };
  status: {
    replicas?: number;
    readyReplicas?: number;
    availableReplicas?: number;
  };
}

// ── Deployments ─────────────────────────────────────────────────

export interface K8sDeployment {
  metadata: K8sMetadata;
  spec: {
    replicas?: number;
    selector?: { matchLabels?: Record<string, string> };
    strategy?: { type?: string };
    template?: {
      metadata?: K8sMetadata;
      spec?: {
        containers?: K8sContainerSpec[];
        initContainers?: K8sContainerSpec[];
        volumes?: { name: string; [key: string]: unknown }[];
        serviceAccountName?: string;
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
    ports?: { port: number; targetPort: number | string; protocol?: string; nodePort?: number; name?: string }[];
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

// ── RBAC ────────────────────────────────────────────────────────

export interface K8sServiceAccount {
  metadata: K8sMetadata;
  secrets?: { name: string }[];
  automountServiceAccountToken?: boolean;
}

export interface K8sPolicyRule {
  verbs: string[];
  apiGroups?: string[];
  resources?: string[];
  resourceNames?: string[];
  nonResourceURLs?: string[];
}

export interface K8sRole {
  metadata: K8sMetadata;
  rules?: K8sPolicyRule[];
}

export interface K8sRoleBinding {
  metadata: K8sMetadata;
  roleRef: {
    apiGroup: string;
    kind: string;
    name: string;
  };
  subjects?: {
    kind: string;
    name: string;
    namespace?: string;
    apiGroup?: string;
  }[];
}

// ClusterRole and ClusterRoleBinding share the same shape
export type K8sClusterRole = K8sRole;
export type K8sClusterRoleBinding = K8sRoleBinding;

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

// ── Runtime Info (from /api/runtime/) ───────────────────────────

export interface RuntimeInfo {
  runtime: string;
  mode: string;
  sync_supported: boolean;
  strategy: string;
  language: string;
  is_frontend: boolean;
  container: string;
  default_dest: string;
}

// ── Sync Status ─────────────────────────────────────────────────

export interface SyncStatus {
  running: boolean;
  deployment?: string;
  namespace?: string;
  src?: string;
  dest?: string;
  pod?: string;
  sync_count: number;
  last_sync?: string;
  started_at?: string;
}

// ── Service Directory (from /api/load-context) ──────────────────

export interface ServiceDir {
  name: string;
  path: string;
  has_dockerfile: boolean;
  language: string;
}

// ── Intel Status ────────────────────────────────────────────────

export interface IntelStatus {
  status: 'active' | 'disabled' | 'inactive';
  files?: string[];
  last_interaction?: string;
  timeout: string;
}

// ── Topology Editor ─────────────────────────────────────────────

export const DEPENDENCY_TYPES = [
  'postgres', 'redis', 'mysql', 'mongodb', 'rabbitmq', 'minio',
  'elasticsearch', 'kafka', 'nats', 'memcached', 'cassandra',
  'consul', 'vault', 'influxdb', 'jaeger',
] as const;

export type DependencyType = typeof DEPENDENCY_TYPES[number];

export const DEP_META: Record<DependencyType, { icon: string; label: string; color: string; defaultPort: number; envVar: string }> = {
  postgres:      { icon: '🐘', label: 'PostgreSQL',    color: '#336791', defaultPort: 5432, envVar: 'DATABASE_URL' },
  redis:         { icon: '🔴', label: 'Redis',         color: '#DC382D', defaultPort: 6379, envVar: 'REDIS_URL' },
  mysql:         { icon: '🐬', label: 'MySQL',         color: '#4479A1', defaultPort: 3306, envVar: 'DATABASE_URL' },
  mongodb:       { icon: '🍃', label: 'MongoDB',       color: '#47A248', defaultPort: 27017, envVar: 'MONGO_URL' },
  rabbitmq:      { icon: '🐰', label: 'RabbitMQ',      color: '#FF6600', defaultPort: 5672, envVar: 'AMQP_URL' },
  minio:         { icon: '📦', label: 'MinIO',         color: '#C72C48', defaultPort: 9000, envVar: 'S3_ENDPOINT' },
  elasticsearch: { icon: '🔍', label: 'Elasticsearch', color: '#FEC514', defaultPort: 9200, envVar: 'ELASTICSEARCH_URL' },
  kafka:         { icon: '📡', label: 'Kafka',         color: '#231F20', defaultPort: 9092, envVar: 'KAFKA_BROKER_URL' },
  nats:          { icon: '⚡', label: 'NATS',          color: '#27AAE1', defaultPort: 4222, envVar: 'NATS_URL' },
  memcached:     { icon: '🧊', label: 'Memcached',     color: '#00875A', defaultPort: 11211, envVar: 'MEMCACHED_URL' },
  cassandra:     { icon: '👁', label: 'Cassandra',     color: '#1287B1', defaultPort: 9042, envVar: 'CASSANDRA_URL' },
  consul:        { icon: '🏛', label: 'Consul',        color: '#CA2171', defaultPort: 8500, envVar: 'CONSUL_URL' },
  vault:         { icon: '🔐', label: 'Vault',         color: '#000000', defaultPort: 8200, envVar: 'VAULT_ADDR' },
  influxdb:      { icon: '📈', label: 'InfluxDB',      color: '#22ADF6', defaultPort: 8086, envVar: 'INFLUXDB_URL' },
  jaeger:        { icon: '🔭', label: 'Jaeger',        color: '#60D0E4', defaultPort: 16686, envVar: 'JAEGER_URL' },
};

export interface TopologyNodeData {
  kind: 'service' | 'dependency';
  label: string;
  // dependency-specific
  depType?: DependencyType;
  version?: string;
  port?: number;
  envVarName?: string;
  // service-specific
  image?: string;
  path?: string;
  servicePort?: number;
  replicas?: number;
  // tracking
  dseName?: string;  // links to an existing DSE
  isNew?: boolean;
  // index signature for React Flow compatibility
  [key: string]: unknown;
}

export interface TopologyGraph {
  nodes: {
    id: string;
    type: string;
    position: { x: number; y: number };
    data: TopologyNodeData;
  }[];
  edges: {
    id: string;
    source: string;
    target: string;
  }[];
}
