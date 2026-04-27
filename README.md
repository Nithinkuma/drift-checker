# drift-checker

A Go service that connects to ArgoCD with a read-only token and tells you exactly
where your multi-cluster deployments have drifted — wrong image tag in one region,
an OutOfSync StatefulSet in another, a Rollout stuck mid-canary somewhere else.

Data is fetched once via `POST /sync`, stored locally in SQLite, and served
from the database on every subsequent read. A built-in web UI is available at `GET /`.

---

## Why

Multi-cluster deployments should be identical across environments. In practice they
drift: a deployment bot misses one region, auto-sync is disabled on one cluster,
an image tag gets bumped in prod-us but not prod-ir. The usual fix is a Python
script that SSHes into each cluster, grabs image SHAs from Deployments and
StatefulSets, and dumps them into a spreadsheet for someone to eyeball.

That approach has no structure. There is no grouping, no direct correlation between
the same application across clusters, and no single command that shows the full
picture.

ArgoCD already models this correctly. An **ApplicationSet** is the canonical
"this application deployed everywhere" unit. Each member **Application** carries
live resource state — sync status, health status, running images — per cluster.
drift-checker reads that model and returns a structured diff.

**What you get vs the script approach:**

| | Script approach | drift-checker |
|--|----------------|---------------|
| Grouping | Raw k8s objects | Grouped by ApplicationSet |
| Image comparison | SHA grep per cluster | Parsed image ref with tag + digest per region |
| Sync awareness | None | OutOfSync flagged per workload per region |
| Silent re-tags | Invisible | IMAGE_DIGEST_DRIFT catches same tag, different digest |
| Access required | k8s credentials per cluster | One ArgoCD read-only token |
| Output | Unstructured text | JSON, CSV, or web UI |

---

## Drift types detected

| Type | Trigger |
|------|---------|
| `IMAGE_TAG_DRIFT` | Same image repository, different tag across regions |
| `IMAGE_DIGEST_DRIFT` | Same tag, different digest — image was silently replaced |
| `SYNC_DRIFT` | A workload is OutOfSync in one or more regions |
| `HEALTH_DRIFT` | A workload is not Healthy in one or more regions |

Workloads tracked: `Deployment`, `StatefulSet`, `Rollout` (Argo Rollouts),
`CronJob`, `Job`. Any resource outside this set is ignored.

Health states surface verbatim from ArgoCD — `Degraded`, `Missing`,
`Progressing`, etc. — so you see the real state, not a boolean.

`Missing` means the workload did not appear in that region's resource list at
all — the ArgoCD Application exists for that region but the workload was never
created or was deleted from the cluster.

---

## Prerequisites

- Go 1.25+ (or Docker)
- ArgoCD >= 2.0 (requires `.status.summary.images` and `.status.resources[]`)
- A read-only ArgoCD API token (see [ArgoCD setup](#argocd-setup))

---

## Quick start

### Build and run locally

```bash
git clone https://github.com/nithinkuma/drift-checker
cd drift-checker

go build -o drift-checker ./cmd/server

ARGOCD_URL=https://argocd.example.com \
ARGOCD_TOKEN=eyJhbGciOiJSUzI1NiIsInR5... \
./drift-checker
# Server listening on :8080
```

Open `http://localhost:8080` in your browser, enter your project name, and click
**Sync from ArgoCD**. After the initial sync, data is served from the local
SQLite database — no ArgoCD call is made on reads.

### Run with Docker

```bash
docker build -t drift-checker .

docker run -p 8080:8080 \
  -e ARGOCD_URL=https://argocd.example.com \
  -e ARGOCD_TOKEN=eyJhbGciOiJSUzI1NiIsInR5... \
  -v /data/drift-checker:/data \
  -e DB_PATH=/data/drift-checker.db \
  drift-checker
```

---

## Configuration

All configuration is via environment variables. No config file is required.

| Variable | Default | Description |
|----------|---------|-------------|
| `ARGOCD_URL` | — | ArgoCD base URL, no trailing slash. **Required.** |
| `ARGOCD_TOKEN` | — | Read-only ArgoCD API token. **Required.** |
| `PORT` | `8080` | HTTP port to listen on |
| `DB_PATH` | `drift-checker.db` | Path to the SQLite database file |
| `ARGOCD_TLS_SKIP_VERIFY` | `false` | Set `true` for self-signed certificates |
| `ARGOCD_HTTP_TIMEOUT` | `15s` | Timeout for ArgoCD API calls (e.g. `20s`, `1m`) |
| `ARGOCD_MAX_APPS` | `500` | Page size for application list pagination |

---

## How it works

```
POST /api/v1/{project}/sync
        │
        ▼
  ArgoCD API ──► grouper ──► drift detector ──► SQLite
                                                    │
GET /api/v1/{project}/diff/builds  ◄────────────────┘
GET /api/v1/{project}/diff/resources
GET /api/v1/{project}/regions
GET /api/v1/{project}/builds
GET /api/v1/{project}/resources
```

1. **Sync** — `POST /api/v1/{project}/sync` fetches all Applications for the
   project from ArgoCD, groups them by ApplicationSet, runs drift detection, and
   writes the result to SQLite. This is the only call that touches ArgoCD.
2. **Query** — all `GET` endpoints read from the SQLite database. Fast, no
   ArgoCD dependency at read time.
3. **Refresh** — call sync again whenever you want fresh data. The previous
   snapshot is replaced atomically.

---

## API

### Health check

```
GET /healthz
```

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

---

### Web UI

```
GET /
```

Opens the embedded single-page UI in a browser. The UI provides:
- Project selector (pre-populated with synced projects)
- Sync button to fetch fresh data from ArgoCD
- Summary cards: Regions, AppSets, Build Drifts, Resource Drifts, Last Synced
- **Build Diffs** tab — image tag comparison across regions, with CSV download
- **Resource Diffs** tab — sync and health state per workload per region, with CSV download
- **Regions** tab — full region/cluster inventory

---

### List synced projects

```
GET /api/v1/projects
```

```bash
curl http://localhost:8080/api/v1/projects
```

```json
{ "projects": ["platform", "data-platform"] }
```

---

### Sync a project

```
POST /api/v1/{project}/sync
```

Fetches live data from ArgoCD and writes it to the database. Returns a summary.
This is the only endpoint that contacts ArgoCD.

```bash
curl -X POST http://localhost:8080/api/v1/platform/sync
```

```json
{
  "project": "platform",
  "synced_at": "2026-04-27T14:00:00Z",
  "summary": {
    "total_appsets": 12,
    "drifted_appsets": 3,
    "total_apps": 48
  }
}
```

---

### Build diff

```
GET /api/v1/{project}/diff/builds
GET /api/v1/{project}/diff/builds?all=true          # include non-drifted rows
GET /api/v1/{project}/diff/builds?appset=payment-api
GET /api/v1/{project}/diff/builds?format=csv
```

Cross-region image tag comparison. By default only rows with `has_diff=true`
are returned.

```bash
curl http://localhost:8080/api/v1/platform/diff/builds | jq .
```

```json
{
  "project": "platform",
  "diffs": [
    {
      "appset": "payment-api",
      "repository": "gcr.io/myproject/payment-api",
      "has_diff": true,
      "regions": {
        "prod-us": "v2.1.0",
        "prod-ir": "v2.0.9",
        "stg-us":  "v2.1.0"
      }
    }
  ]
}
```

CSV download (region names become dynamic columns):

```bash
curl "http://localhost:8080/api/v1/platform/diff/builds?format=csv" -o build-diff.csv
```

```
appset,repository,has_diff,prod-ir,prod-us,stg-us
payment-api,gcr.io/myproject/payment-api,true,v2.0.9,v2.1.0,v2.1.0
```

---

### Resource diff

```
GET /api/v1/{project}/diff/resources
GET /api/v1/{project}/diff/resources?all=true
GET /api/v1/{project}/diff/resources?appset=payment-api
GET /api/v1/{project}/diff/resources?format=csv
```

Cross-region workload sync and health state. By default only rows with
`has_diff=true` are returned.

```bash
curl http://localhost:8080/api/v1/platform/diff/resources | jq .
```

```json
{
  "project": "platform",
  "diffs": [
    {
      "appset": "payment-api",
      "kind": "Deployment",
      "name": "payment-api",
      "has_diff": true,
      "sync": {
        "prod-us": "Synced",
        "prod-ir": "OutOfSync",
        "stg-us":  "Synced"
      },
      "health": {
        "prod-us": "Healthy",
        "prod-ir": "Healthy",
        "stg-us":  "Healthy"
      }
    }
  ]
}
```

---

### Region table

```
GET /api/v1/{project}/regions
```

One row per Application (cluster deployment) — good for getting an inventory
of what is deployed where and its overall ArgoCD health.

```bash
curl http://localhost:8080/api/v1/platform/regions | jq .
```

```json
{
  "project": "platform",
  "rows": [
    {
      "appset": "payment-api",
      "app_name": "payment-api-prod-us",
      "region": "prod-us",
      "cluster_name": "prod-us-eks",
      "namespace": "payments",
      "sync_status": "Synced",
      "health_status": "Healthy"
    }
  ]
}
```

---

### Build table

```
GET /api/v1/{project}/builds
```

One row per image per region per AppSet. Shows all running images across all
clusters.

---

### Resource table

```
GET /api/v1/{project}/resources
```

One row per workload per region per AppSet. Shows raw sync and health state
without cross-region comparison.

---

## ArgoCD setup

drift-checker only needs read access. Create a dedicated account with minimal
permissions.

**1. Create the account** (in `argocd-cm` ConfigMap):

```yaml
data:
  accounts.drift-checker: apiKey
```

**2. Bind read-only permissions** (in `argocd-rbac-cm` ConfigMap):

```yaml
data:
  policy.csv: |
    p, role:drift-checker, applications,    get, */*, allow
    p, role:drift-checker, applicationsets, get, */*, allow
    p, role:drift-checker, clusters,        get, *,   allow
    g, drift-checker, role:drift-checker
```

**3. Generate the token**:

```bash
argocd account generate-token --account drift-checker
# eyJhbGciOiJSUzI1NiIsInR5...
```

---

## Deploy to Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: drift-checker
  namespace: tools
spec:
  replicas: 1
  selector:
    matchLabels:
      app: drift-checker
  template:
    metadata:
      labels:
        app: drift-checker
    spec:
      containers:
        - name: drift-checker
          image: drift-checker:latest
          ports:
            - containerPort: 8080
          env:
            - name: ARGOCD_URL
              value: "https://argocd.example.com"
            - name: ARGOCD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: drift-checker-secret
                  key: token
            - name: DB_PATH
              value: /data/drift-checker.db
          volumeMounts:
            - name: data
              mountPath: /data
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: drift-checker-data
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: drift-checker-data
  namespace: tools
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Service
metadata:
  name: drift-checker
  namespace: tools
spec:
  selector:
    app: drift-checker
  ports:
    - port: 80
      targetPort: 8080
```

Store the token as a Secret:

```bash
kubectl create secret generic drift-checker-secret \
  --from-literal=token=eyJhbGci... \
  -n tools
```

---

## Region labels

By default, region names are taken from the ArgoCD cluster object's `.name`
field (e.g. `prod-us-eks`). To use a shorter label, add a custom label to
the cluster in ArgoCD:

```bash
argocd cluster set https://prod-us.example.com \
  --label drift-checker/region=prod-us
```

---

## Run tests

```bash
go test ./...
```
