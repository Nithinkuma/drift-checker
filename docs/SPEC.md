# ArgoCD Drift Checker — Technical Specification

## 1. API Contract

### 1.1 Common Headers

All requests that hit ArgoCD on behalf of the caller use the supplied token.
The server itself requires no authentication in v1 (network policy / ingress handles
access control).

```
Content-Type: application/json
```

---

### 1.2  POST /api/v1/analyze

Triggers a full analysis for a given ArgoCD project. The server fetches all
ApplicationSets and Applications belonging to the project, groups them, runs drift
detection, and returns the report.

**Request body**

```json
{
  "argocd_url": "https://argocd.example.com",
  "token":      "eyJhbGciOiJSUzI1NiIsInR5...",
  "project":    "platform"
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `argocd_url` | string | yes | Base URL of ArgoCD server, no trailing slash |
| `token`      | string | yes | ArgoCD API token (read-only role sufficient) |
| `project`    | string | yes | ArgoCD project name to scope the query |

**Response 200**

```json
{
  "project": "platform",
  "generated_at": "2026-04-22T10:00:00Z",
  "summary": {
    "total_appsets":   12,
    "drifted_appsets": 3,
    "total_apps":      48
  },
  "appsets": [ /* see AppSet object */ ]
}
```

**Error responses**

| HTTP | code | Meaning |
|------|------|---------|
| 400 | `INVALID_REQUEST` | Missing / malformed body fields |
| 401 | `ARGOCD_AUTH_FAILED` | ArgoCD rejected the token |
| 502 | `ARGOCD_UNREACHABLE` | Could not connect to ArgoCD URL |
| 500 | `INTERNAL_ERROR` | Unexpected server error |

```json
{ "error": "human-readable message", "code": "ARGOCD_AUTH_FAILED" }
```

---

### 1.3  GET /api/v1/analyze/{project}/appsets

Returns the list of AppSets for the project with top-level drift flag.
Query params: same `argocd_url` and `token` as query parameters, or via
`Authorization: Bearer <token>` + `X-ArgoCD-URL` header (for browser-friendly use).

**Response 200**

```json
{
  "project": "platform",
  "appsets": [
    { "name": "myapp", "drift_detected": true,  "regions": ["prod-us","prod-ir","stg-us"] },
    { "name": "worker","drift_detected": false, "regions": ["prod-us","prod-ir"] }
  ]
}
```

---

### 1.4  GET /api/v1/analyze/{project}/appsets/{appset}

Full drift detail for a single AppSet.

---

### 1.5  GET /api/v1/analyze/{project}/drift

Returns only drifted AppSets.

Query filter: `?drift_type=IMAGE_TAG_DRIFT|IMAGE_DIGEST_DRIFT|SYNC_DRIFT|MISSING_REGION|HEALTH_DRIFT`

---

## 2. Domain Types

### 2.1 AppSet

```json
{
  "name": "myapp",
  "namespace": "argocd",
  "drift_detected": true,
  "drift_types": ["IMAGE_TAG_DRIFT", "SYNC_DRIFT"],
  "apps": [ /* AppInstance[] */ ],
  "drift_details": [ /* DriftDetail[] */ ]
}
```

---

### 2.2 AppInstance

Represents one ArgoCD Application — i.e., one deployment of the AppSet in one
cluster/region.

```json
{
  "name":          "myapp-prod-us",
  "region":        "prod-us",
  "cluster_name":  "prod-us-eks",
  "cluster_server":"https://AABBCC.gr7.us-east-1.eks.amazonaws.com",
  "namespace":     "myapp",
  "sync_status":   "Synced",
  "health_status": "Healthy",
  "revision":      "a3f9c12b",
  "images": [ /* ImageRef[] */ ]
}
```

| Field | Source in ArgoCD |
|-------|-----------------|
| `region` | Cluster `.name` field; or label `drift-checker/region` on the cluster |
| `cluster_name` | `.spec.destination.name` |
| `cluster_server` | `.spec.destination.server` |
| `sync_status` | `.status.sync.status` |
| `health_status` | `.status.health.status` |
| `revision` | `.status.sync.revision` (Git SHA) |
| `images` | `.status.summary.images[]` (parsed) |

---

### 2.3 ImageRef

ArgoCD surfaces images as plain strings like
`registry.example.com/myapp:v1.2.3@sha256:abc123`.

```json
{
  "full":       "registry.example.com/myapp:v1.2.3@sha256:abc123def456",
  "registry":   "registry.example.com",
  "repository": "myapp",
  "tag":        "v1.2.3",
  "digest":     "sha256:abc123def456"
}
```

Parsing rules:
- Split on `@` to extract digest (optional)
- Split on `:` (last occurrence) to extract tag (optional; default `latest`)
- Remainder is `registry/repository`; if no `.` or `:` in first segment, registry
  defaults to `docker.io`

---

### 2.4 DriftDetail

```json
{
  "type":    "IMAGE_TAG_DRIFT",
  "image_repository": "registry.example.com/myapp",
  "message": "Tag mismatch across regions",
  "regions": {
    "prod-us": "v1.2.3",
    "prod-ir": "v1.2.2",
    "stg-us":  "v1.2.3"
  }
}
```

---

## 3. ArgoCD API Usage

### 3.1 Endpoints consumed

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/applicationsets?projects={p}` | List all AppSets in project |
| GET | `/api/v1/applications?projects={p}&limit=500` | List all Apps in project |
| GET | `/api/v1/clusters` | Resolve cluster name → region label |

The server makes these calls concurrently (AppSets + Applications fetched in
parallel; cluster list fetched once and cached for the request lifetime).

### 3.2 AppSet → App correlation

ArgoCD sets the label `argocd.argoproj.io/app-set-name: <appset-name>` on every
Application generated by an ApplicationSet. This is the primary correlation key.

### 3.3 Region resolution order

1. ArgoCD cluster object `metadata.labels["drift-checker/region"]` (explicit label)
2. ArgoCD cluster object `.name` field
3. Hostname extracted from `.server` URL

The first non-empty value wins.

### 3.4 Replica / offline app handling

AppSets sometimes generate replica instances with names like `myapp-prod-us-01` or
`myapp-prod-us-offline`. These share the same cluster destination. They are grouped
under the same region but reported as separate AppInstances, so drift comparisons
are always done between instances at the same region for the same image repository.

---

## 4. Drift Detection Logic

```
For each AppSet:
  base_images = map[repo -> map[region -> ImageRef]

  for each app in appset:
    for each image in app.images:
      base_images[image.repository][app.region] = image

  for each repo in base_images:
    tags    = distinct set of tags across regions
    digests = distinct set of digests across regions (non-empty only)

    if len(tags) > 1    -> IMAGE_TAG_DRIFT
    if len(digests) > 1 -> IMAGE_DIGEST_DRIFT

  for each app:
    if app.sync_status != "Synced"    -> SYNC_DRIFT
    if app.health_status != "Healthy" -> HEALTH_DRIFT

  expected_regions = union of all regions seen in appset
  for each app:
    if app.region not in expected_regions for all repos -> MISSING_REGION
```

---

## 5. Configuration

### 5.1 config.yaml (optional defaults)

```yaml
server:
  port: 8080
  read_timeout:  10s
  write_timeout: 30s

argocd:
  tls_skip_verify: false   # set true for self-signed certs
  http_timeout:    15s
  max_apps:        1000    # pagination safety limit
```

### 5.2 Environment variable overrides

| Var | Config key | Notes |
|-----|------------|-------|
| `PORT` | `server.port` | |
| `ARGOCD_TLS_SKIP_VERIFY` | `argocd.tls_skip_verify` | `"true"` / `"false"` |
| `ARGOCD_HTTP_TIMEOUT` | `argocd.http_timeout` | e.g. `"20s"` |

---

## 6. Project Layout

```
drift-checker/
├── cmd/
│   └── server/
│       └── main.go          # entry point
├── internal/
│   ├── argocd/
│   │   ├── client.go        # HTTP client wrapping ArgoCD REST
│   │   └── types.go         # raw ArgoCD response structs
│   ├── domain/
│   │   └── types.go         # AppSet, AppInstance, ImageRef, DriftDetail
│   ├── analysis/
│   │   ├── grouper.go       # groups Apps under AppSets
│   │   ├── image_parser.go  # parses image strings -> ImageRef
│   │   └── drift.go         # drift detection logic
│   └── api/
│       ├── handlers.go      # HTTP handlers
│       ├── middleware.go    # logging, recovery
│       └── router.go        # route registration
├── docs/
│   ├── PLAN.md
│   ├── SPEC.md
│   └── SKILLS.md
├── config.yaml
├── Dockerfile
├── go.mod
└── go.sum
```

---

## 7. Example curl

```bash
curl -X POST https://drift-checker.internal/api/v1/analyze \
  -H "Content-Type: application/json" \
  -d '{
    "argocd_url": "https://argocd.example.com",
    "token":      "eyJhbGci...",
    "project":    "platform"
  }' | jq '.appsets[] | select(.drift_detected)'
```

Filter to only image-tag drifts:

```bash
curl "https://drift-checker.internal/api/v1/analyze/platform/drift?drift_type=IMAGE_TAG_DRIFT" \
  -H "Authorization: Bearer eyJhbGci..." \
  -H "X-ArgoCD-URL: https://argocd.example.com"
```
