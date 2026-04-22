# ArgoCD Drift Checker — Skills & Domain Knowledge

This file captures the domain knowledge, ArgoCD concepts, and Go patterns
that anyone (human or AI) working on this codebase needs to understand.

---

## 1. ArgoCD Concepts

### ApplicationSet (AppSet)

An `ApplicationSet` is an ArgoCD CRD that uses a generator (cluster, list, git, etc.)
to produce multiple `Application` objects from a single template. In a multi-cluster
setup, one AppSet typically produces one `Application` per target cluster.

Key facts:
- Lives in the `argocd` namespace
- Contains a `.spec.generators[]` list and a `.spec.template`
- Every Application it creates carries the label:
  `argocd.argoproj.io/app-set-name: <appset-name>`
- AppSet name is the "product name" — it is the grouping key for drift comparison

### Application

An ArgoCD `Application` represents a single deployment of a workload to a single
cluster. One AppSet → N Applications (one per cluster/region).

Fields used by this tool:

```
.metadata.name                         # e.g. myapp-prod-us
.metadata.labels
  argocd.argoproj.io/app-set-name      # parent AppSet name — primary grouping key
.spec.destination.name                 # cluster friendly name (preferred for region)
.spec.destination.server               # k8s API server URL (fallback)
.spec.destination.namespace            # target namespace
.status.sync.status                    # app-level aggregate: Synced | OutOfSync | Unknown
.status.sync.revision                  # Git commit SHA currently applied
.status.health.status                  # app-level aggregate: Healthy | Progressing | Degraded | Missing
.status.summary.images[]               # list of image strings currently running (live cluster state)
.status.resources[]                    # per-resource sync + health — see below
```

The `.status.summary.images[]` field is populated from live cluster state.
It reflects what is **actually running**, not what Git says should run — the right
source of truth for image drift detection.

### App-level vs Resource-level sync and health

ArgoCD surfaces state at **two levels**:

**App-level** (`.status.sync.status`, `.status.health.status`):
- These are **aggregates** rolled up from all managed resources.
- If any resource is OutOfSync → app is OutOfSync.
- If any resource is Degraded → app is Degraded.
- Useful as a quick summary, but gives no information about **which** resource is the problem.

**Resource-level** (`.status.resources[]`):
- One entry per Kubernetes object managed by the Application.
- Each entry has its own `.status` (sync) and `.health.status`.
- This is the source of truth used by this tool for drift detection.

```
.status.resources[]:
  group      # API group, e.g. "apps", "argoproj.io", "keda.sh"
  version    # API version
  kind       # Deployment | StatefulSet | Rollout | PodDisruptionBudget | ...
  namespace
  name
  status     # sync state: Synced | OutOfSync | Unknown
  health:
    status   # Healthy | Progressing | Degraded | Missing | Unknown
    message  # human-readable detail (e.g. pod crash reason)
```

### Sync vs Health — at resource level

These are independent axes, evaluated per workload:

| Resource sync | Resource health | Meaning |
|---------------|-----------------|---------|
| Synced | Healthy | Running the version Git specifies, pods healthy |
| Synced | Degraded | Running desired version but pods are crashing |
| OutOfSync | Healthy | Still running old version; Git has a newer spec |
| OutOfSync | Degraded | Old version AND unhealthy |
| Synced | Progressing | Rollout / canary in flight — transient, not flagged as drift |

### Workload kinds (sync + health tracked)

`Deployment`, `StatefulSet`, `DaemonSet`, `Rollout` (Argo Rollouts), `Job`

Sync state is only meaningful for these kinds. A ConfigMap being OutOfSync
is informational; a Deployment being OutOfSync means the wrong image is running.

### Supporting resource kinds (health only tracked)

`PodDisruptionBudget`, `ScaledObject` (KEDA), `HorizontalPodAutoscaler`

These don't have meaningful sync state from a drift perspective, but their
health state directly affects application behaviour:
- A degraded PDB means Kubernetes cannot safely evict pods (blocks upgrades/node drains).
- A degraded ScaledObject means KEDA cannot scale the workload.

### Cluster Object

ArgoCD maintains a list of registered clusters accessible at `/api/v1/clusters`.
Each cluster has:

```
.name    # human-readable name, often matches the environment (prod-us, stg-eu)
.server  # k8s API server URL
.info.connectionState.status  # Connected | Failed
```

The `.name` is the preferred way to derive a region label. Teams can also add
a custom label `drift-checker/region` to the cluster object for explicit mapping.

---

## 2. Image String Format

ArgoCD surfaces image references as plain strings. The canonical format is:

```
[registry/]repository[:tag][@digest]
```

Examples:
```
nginx:1.25.3
docker.io/library/nginx:1.25.3
gcr.io/myproject/myapp:v2.1.0@sha256:4f53cda18...
registry.k8s.io/kube-proxy:v1.29.0
myapp:latest              # implicit docker.io registry
```

Parsing rules (in order):
1. If `@` is present, split — right part is the digest
2. Find the last `:` in the left part; if it exists and nothing after it contains `/`,
   the right part is the tag
3. If the first path segment contains a `.` or `:`, it is the registry;
   otherwise default to `docker.io`
4. Remaining path is the repository

**Important:** Two images with the same tag but different digests represent a
"silent re-tag" — the image was replaced without a version bump. This is a
dangerous form of drift that digest comparison catches.

---

## 3. Multi-Cluster Deployment Patterns

### Standard pattern

One AppSet per application, one Application per cluster:

```
AppSet: myapp
  ├── Application: myapp-prod-us   (cluster: prod-us-eks)
  ├── Application: myapp-prod-ir   (cluster: prod-ir-eks)
  └── Application: myapp-stg-us   (cluster: stg-us-eks)
```

### Replica / Offline pattern

Some services are deployed multiple times per cluster (sharding, blue/green,
offline workers). These appear as separate Applications in the same cluster:

```
AppSet: payment-worker
  ├── Application: payment-worker-prod-us-01    (cluster: prod-us-eks)
  ├── Application: payment-worker-prod-us-02    (cluster: prod-us-eks)  <- same cluster
  └── Application: payment-worker-prod-ir-01    (cluster: prod-ir-eks)
```

Drift comparison for replicas should compare across regions, not across replicas
within the same region (replicas of the same version in the same region are not drift).

Grouping strategy: `(appset_name, region)` is the drift comparison unit.
Within a region, all replicas should run the same image — if they don't, that is
also drift but of a different kind (intra-region drift, lower priority).

---

## 4. ArgoCD API Authentication

ArgoCD uses JWT tokens. To get a read-only token:

```bash
# Via CLI
argocd account generate-token --account readonly-user

# Via API
curl -X POST https://argocd.example.com/api/v1/session \
  -d '{"username":"readonly","password":"..."}'
# Returns: {"token": "eyJ..."}
```

The token is passed as:
```
Authorization: Bearer eyJ...
```

Read-only RBAC policy in ArgoCD:
```csv
p, role:readonly, applications, get, */*, allow
p, role:readonly, applicationsets, get, */*, allow
p, role:readonly, clusters, get, *, allow
```

---

## 5. Go Patterns Used in This Project

### Concurrent ArgoCD fetches

```go
var (
    appsets []argocd.ApplicationSet
    apps    []argocd.Application
    eg      errgroup.Group
)
eg.Go(func() error { appsets, err = client.ListAppSets(ctx, project); return err })
eg.Go(func() error { apps, err = client.ListApps(ctx, project); return err })
if err := eg.Wait(); err != nil { ... }
```

### Structured error types

```go
type ArgoError struct {
    Code    string
    Message string
    Status  int
}
```

### Image parsing

The parser must handle edge cases:
- Images with no tag (implicitly `latest`)
- Images with no registry (implicitly `docker.io`)
- Images with port in registry (`registry.example.com:5000/myapp:v1`)
- Digests only, no tag (`myapp@sha256:abc`)

---

## 6. Operational Notes

### TLS

In many on-prem or self-hosted ArgoCD installations, the TLS certificate is
self-signed. The client must support `tls_skip_verify: true` without baking it
in as a default.

### Pagination

ArgoCD's `/api/v1/applications` endpoint is paginated. For large projects use:
```
GET /api/v1/applications?projects=myproject&limit=500&continue=<token>
```
Follow the `metadata.continue` field until empty.

### Rate limiting

ArgoCD has no built-in rate limiting on its API, but the underlying k8s API server
does. In very large clusters (500+ apps), parallel fan-out to individual app detail
endpoints can cause pressure. Prefer the list endpoint and avoid per-app detail
calls unless necessary.

### ArgoCD version compatibility

The `.status.summary.images` field was introduced in ArgoCD v2.0. For v1.x
installations, image data must be fetched from the live k8s manifests via
`/api/v1/applications/{name}/resource` — substantially more expensive.

This tool targets ArgoCD >= 2.0.

---

## 7. Testing Strategy

| Layer | Approach |
|-------|----------|
| Image parser | Table-driven unit tests with edge case strings |
| Drift engine | Unit tests with mock AppSet/App data |
| ArgoCD client | Interface + mock; integration test against live ArgoCD optional |
| HTTP handlers | `httptest.NewRecorder` + JSON assertion |
| End-to-end | Optional: real ArgoCD instance in CI via kind + ArgoCD helm chart |

Fixtures live in `testdata/` as JSON files matching real ArgoCD API responses.
