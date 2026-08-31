# Internal Developer Platform

An internal developer platform that gives engineering teams a standardized way to request, deploy, operate, and observe application workloads using Azure-oriented platform services, Kubernetes, Terraform, GitOps, CI/CD, secrets management, and SRE practices.

> **Developers describe what they need. The platform handles how it is delivered.**

The platform foundation, infrastructure, Kubernetes, GitOps delivery, secrets, and observability, is implemented and working end-to-end against a reference workload. The next milestone is the developer-facing self-service layer that turns a workload request into GitOps-managed platform resources.

---

## Overview

Engineering teams need application infrastructure, Kubernetes resources, cloud services, a deployment pipeline, secrets, monitoring without manually assembling each of those per service. This platform provides that as a standardized layer.

Azure-oriented services (Resource Group, networking, registry, Kubernetes, PostgreSQL, Key Vault, Service Bus, Blob Storage) are provisioned through Terraform against Floci AZ, a local Azure-compatible service emulator, so the platform behaves like a real Azure environment without requiring a live Azure subscription. Kubernetes workloads are delivered through Argo CD, application changes ship through GitHub Actions, and Prometheus/Grafana provide metrics, dashboards, and alerting.

The long-term developer interaction is not Terraform, Kubernetes, or CLI usage: developers submit a workload request through the IDP, and the platform translates that into standardized infrastructure and deployment configuration.

---

## Platform Goals

**Self-service**: request a workload without knowing the implementation details of Deployments/Services, registries, secrets, database connectivity, queues, object storage, resource policies, monitoring, or GitOps reconciliation.

**Standardization**: every workload follows the same operational baseline for resource limits, health checks, secrets, observability, and image management.

**Optional capabilities**: teams opt into what a workload actually needs (PostgreSQL, Service Bus, Blob Storage, Key Vault, monitoring, alerting).

**Reliability**: failures are made visible and recoverable through health checks, metrics, dashboards, alert rules, GitOps reconciliation, and repeatable recovery procedures (see Design Decisions).

---

## Architecture

```text
                       INTERNAL DEVELOPERS
                               │
                               ▼
                    Internal Developer Portal
                      / Self-Service API
                               │
               workload + capability request
                               │
                               ▼
                    Platform Automation Layer
                               │
                 ┌─────────────┴─────────────┐
                 ▼                           ▼
             Terraform                    GitOps
                 │                           │
                 ▼                           ▼
         Azure-oriented services          Git repository
                 │                           │
                 │                           ▼
                 │                        Argo CD
                 └──────────────┬────────────┘
                                ▼
                            Kubernetes
                                │
                    ┌───────────┴───────────┐
                    ▼                       ▼
               API workloads            Workers
                    │                       │
                    ├──────── PostgreSQL ───┤
                    ├──────── Service Bus ──┤
                    ├──────── Blob Storage ─┤
                    └──────── Key Vault ────┘
                                │
                                ▼
                       Prometheus → Grafana → Alerts
```

The developer-facing portal at the top is the next milestone; everything below it is implemented and is what the portal will call into.

---

## Developer Experience

Intended workflow once the portal ships:

```text
Developer submits workload request
   → platform validates it
   → generates desired state (deployment, resources, capabilities, secrets, monitoring, GitOps definitions)
   → Git commit
   → Argo CD reconciliation
   → running workload
```

A developer should never need to hand-write Kubernetes YAML, run Terraform directly, wire up registry access, create queues manually, manage application passwords, or configure a pipeline from scratch: those are platform concerns.

Planned request shape:

```text
Service name, Owner/team, Runtime, Workload type, Container port, Service size, Environment
Optional capabilities: PostgreSQL, Service Bus, Blob Storage, Key Vault, Monitoring, Alerting
```

## Platform Components

**Terraform** provisions and coordinates the infrastructure lifecycle: Resource Group, networking, registry, Kubernetes cluster, PostgreSQL, and Key Vault. It also triggers the platform bootstrap when infrastructure, migrations, or bootstrap logic change.

**Kubernetes** runs Argo CD, the reference workload, Prometheus, and Grafana, split across `argocd`, `jobs`, and `monitoring` namespaces.

**Argo CD** owns deployment via an app-of-apps pattern with automated sync, pruning, and self-healing. Git is the deployment source of truth: manual changes to managed resources get reconciled back.

**Container registry (ACR)** stores images tagged by Git commit SHA, giving a direct line from source commit → image → GitOps manifest → running workload.

**PostgreSQL** stores application state under a dedicated, least-privilege application role (not the administrator credential). Schema changes are SQL migration files run through the platform bootstrap.

**Service Bus** decouples request handling from background processing: the API publishes jobs, a worker consumes and processes them independently.

**Blob Storage** holds completed job results; the result location is written back to PostgreSQL.

**Key Vault** holds credentials that shouldn't live in Git (DB and Grafana admin passwords). The cluster bootstrap pulls these into Kubernetes Secrets at runtime, so secrets never touch source, GitOps manifests, Terraform config, or images.

---

## Reference Workload

A working asynchronous Go application (`jobs-api` + `worker`) exists to exercise the platform, not as the IDP itself. The API accepts a job, persists it to PostgreSQL, and queues it via Service Bus; the worker processes it, writes the result to Blob Storage, and updates status (`queued → processing → completed`). It's a realistic workload for validating the infrastructure. In the finished IDP, developers won't interact with it directly.

---

## CI/CD

GitHub Actions (self-hosted runner, no automatic execution of untrusted PR code) tests, builds, and SHA-tags each application image, pushes it to ACR, and commits the updated image reference to the GitOps manifests. Argo CD then reconciles that change into the cluster: CI and CD are deliberately separate responsibilities.

---

## Secrets, Observability & Alerting

Secrets flow from Key Vault through the bootstrap script into Kubernetes Secrets, generating and storing values idempotently if they don't already exist. This has been verified by deleting a secret and confirming the bootstrap recreates it from the same Key Vault value.

The API and worker expose Prometheus metrics (requests, latency, jobs submitted/completed/failed, worker readiness); Grafana dashboards and Prometheus alert rules are provisioned entirely from Git. Alert rules cover target availability, worker readiness, job/API failures, and latency. The target-down rule has been tested end-to-end (break the metrics endpoint → alert fires → Argo CD restores it → alert resolves). Alertmanager routing to external notification channels is the remaining piece.

## Security Considerations

Non-root containers, secrets excluded from Git, Key Vault as the secret source, a dedicated least-privilege PostgreSQL role, ClusterIP-only internal services, no unnecessary public endpoints, a self-hosted CI runner restricted from untrusted PR execution, immutable image references, and GitOps-controlled deployment changes.

Planned: registry allow-listing, resource limit policy, namespace isolation, workload naming standards, capability-level permissions, environment access control, and deployment approvals where needed.

---

## Roadmap

1. **Finish Alertmanager integration**: route firing alerts through grouping/notification instead of stopping at Prometheus.
2. **Build the developer portal**: accept owner/team, service name, runtime, workload type, port, size, environment, and requested capabilities, with no predefined team list.
3. **Define a workload specification**, a small declarative contract between developers and the platform:
   ```yaml
   name: invoice-processor
   owner: commerce-platform
   workload: { type: api-worker, runtime: go, port: 8080, size: small }
   capabilities: { postgres: true, serviceBus: true, blobStorage: true, keyVault: true, monitoring: true, alerting: true }
   ```
4. **Generate standardized desired state from that spec**: Deployment, Service, resource limits, health checks, monitoring, and GitOps application definitions, generated rather than hand-assembled.
5. **Connect capability provisioning**: provision only the platform services (PostgreSQL, Service Bus, Blob Storage, Key Vault) a given workload actually requests.
6. **Add developer status visibility**: surface deployment/GitOps/database/queue/monitoring status in the portal instead of requiring direct Argo CD or `kubectl` access.
7. **Add controlled developer actions**: restart, image change, scale, resize, enable a capability. All of these update desired state through GitOps rather than bypassing it.

---

## Why this exists

The value isn't that developers can assemble infrastructure faster by hand, it's that they don't need to know how it's assembled at all. They declare what a workload needs, and the platform delivers it through a standardized, observable, secure, and repeatable path.
