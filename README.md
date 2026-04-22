# drift-checker

A Go service that connects to ArgoCD with a read-only token and tells you exactly
where your multi-cluster deployments have drifted — wrong image tag in one region,
an OutOfSync StatefulSet in another, a Rollout stuck mid-canary somewhere else.

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
| Output | Unstructured text | JSON — pipe to jq, dashboards, alerting |

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

### Run with Docker

```bash
docker build -t drift-checker .

docker run -p 8080:8080 \
  -e ARGOCD_URL=https://argocd.example.com \
  -e ARGOCD_TOKEN=eyJhbGciOiJSUzI1NiIsInR5... \
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
| `ARGOCD_TLS_SKIP_VERIFY` | `false` | Set `true` for self-signed certificates |
| `ARGOCD_HTTP_TIMEOUT` | `15s` | Timeout for ArgoCD API calls (e.g. `20s`, `1m`) |
| `ARGOCD_MAX_APPS` | `500` | Page size for application list pagination |

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

### Full analysis — POST

```
POST /api/v1/analyze
Content-Type: application/json
```

Runs a complete drift analysis for a project. Returns every AppSet with
per-region workload state and image comparison.

The `argocd_url` and `token` fields in the body are optional — they override
the server's environment variables. Useful when querying multiple ArgoCD
instances from a single drift-checker deployment.

**Request**

```json
{
  "project": "platform"
}
```

```json
{
  "project":    "platform",
  "argocd_url": "https://other-argocd.example.com",
  "token":      "eyJhbGci..."
}
```

**Response**

```json
{
  "project": "platform",
  "generated_at": "2026-04-22T10:00:00Z",
  "summary": {
    "total_appsets": 12,
    "drifted_appsets": 3,
    "total_apps": 48
  },
  "appsets": [
    {
      "name": "payment-api",
      "namespace": "argocd",
      "drift_detected": true,
      "drift_types": ["IMAGE_TAG_DRIFT", "SYNC_DRIFT"],
      "apps": [
        {
          "name": "payment-api-prod-us",
          "region": "prod-us",
          "cluster_name": "prod-us-eks",
          "sync_status": "Synced",
          "health_status": "Healthy",
          "revision": "a3f9c12b",
          "images": [
            {
              "full": "gcr.io/myproject/payment-api:v2.1.0",
              "registry": "gcr.io",
              "repository": "myproject/payment-api",
              "tag": "v2.1.0"
            }
          ],
          "resources": [
            {
              "kind": "Deployment",
              "name": "payment-api",
              "sync_status": "Synced",
              "health_status": "Healthy"
            }
          ]
        },
        {
          "name": "payment-api-prod-ir",
          "region": "prod-ir",
          "cluster_name": "prod-ir-eks",
          "sync_status": "OutOfSync",
          "health_status": "Healthy",
          "images": [
            {
              "full": "gcr.io/myproject/payment-api:v2.0.9",
              "registry": "gcr.io",
              "repository": "myproject/payment-api",
              "tag": "v2.0.9"
            }
          ],
          "resources": [
            {
              "kind": "Deployment",
              "name": "payment-api",
              "sync_status": "OutOfSync",
              "health_status": "Healthy"
            }
          ]
        }
      ],
      "drift_details": [
        {
          "type": "IMAGE_TAG_DRIFT",
          "image_repository": "gcr.io/myproject/payment-api",
          "message": "image tag mismatch across regions for gcr.io/myproject/payment-api",
          "regions": {
            "prod-us": "v2.1.0",
            "prod-ir": "v2.0.9"
          }
        },
        {
          "type": "SYNC_DRIFT",
          "resource": { "kind": "Deployment", "name": "payment-api" },
          "message": "Deployment \"payment-api\" is not Synced in one or more regions",
          "regions": {
            "prod-us": "Synced",
            "prod-ir": "OutOfSync"
          }
        }
      ]
    }
  ]
}
```

**Example**

```bash
curl -s -X POST http://localhost:8080/api/v1/analyze \
  -H "Content-Type: application/json" \
  -d '{"project": "platform"}' | jq .
```

Show only drifted AppSets:

```bash
curl -s -X POST http://localhost:8080/api/v1/analyze \
  -H "Content-Type: application/json" \
  -d '{"project": "platform"}' \
  | jq '.appsets[] | select(.drift_detected)'
```

---

### List AppSets — GET

```
GET /api/v1/analyze/{project}/appsets
```

Returns a summary of every AppSet in the project — name, drift flag, drift
types, and which regions it is deployed in. Lighter than the full POST response.

```bash
curl -s http://localhost:8080/api/v1/analyze/platform/appsets | jq .
```

```json
{
  "project": "platform",
  "appsets": [
    { "name": "payment-api", "drift_detected": true,  "drift_types": ["IMAGE_TAG_DRIFT"], "regions": ["prod-us","prod-ir","stg-us"] },
    { "name": "worker",      "drift_detected": false, "regions": ["prod-us","prod-ir"] }
  ]
}
```

---

### Single AppSet detail — GET

```
GET /api/v1/analyze/{project}/appsets/{appset}
```

Full detail for one AppSet — same shape as a single entry in the POST response.

```bash
curl -s http://localhost:8080/api/v1/analyze/platform/appsets/payment-api | jq .
```

---

### Drifted AppSets only — GET

```
GET /api/v1/analyze/{project}/drift
GET /api/v1/analyze/{project}/drift?drift_type=IMAGE_TAG_DRIFT
```

Returns only the AppSets that have drift, optionally filtered to a specific
drift type. Useful for alerting pipelines that only care about image drift,
or sync tooling that only looks at SYNC_DRIFT.

```bash
# All drifted appsets
curl -s http://localhost:8080/api/v1/analyze/platform/drift | jq .

# Only image tag mismatches
curl -s "http://localhost:8080/api/v1/analyze/platform/drift?drift_type=IMAGE_TAG_DRIFT" | jq .

# Only workloads stuck OutOfSync
curl -s "http://localhost:8080/api/v1/analyze/platform/drift?drift_type=SYNC_DRIFT" | jq .
```

Valid `drift_type` values: `IMAGE_TAG_DRIFT`, `IMAGE_DIGEST_DRIFT`,
`SYNC_DRIFT`, `HEALTH_DRIFT`.

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
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
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

## Reading the output

**`drift_details[].regions`** is always a map of `region → observed value` so
you can see exactly what each region has:

```json
"regions": {
  "prod-us": "v2.1.0",
  "prod-ir": "v2.0.9",
  "stg-us":  "v2.1.0"
}
```

For sync and health drift, the value is the ArgoCD status string verbatim:
`Synced`, `OutOfSync`, `Healthy`, `Degraded`, `Progressing`, `Missing`.

`Missing` means the workload resource did not appear in that region's
`.status.resources[]` at all — the app exists in ArgoCD for that region
but the workload was never created or was deleted from the cluster.

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
