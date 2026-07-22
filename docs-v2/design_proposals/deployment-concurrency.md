# Concurrent deployments across configuration dependencies

* Author(s): Frank Spitulski
* Design Shepherd:
* Date: 2026/07/09
* Status: Draft

## Background

Skaffold resolves multi-config `requires` dependencies in dependency-first
order, then deploys every configuration serially. Independent branches cannot
deploy at the same time.

This proposal implements the deploy portion of
[#8363](https://github.com/GoogleContainerTools/skaffold/issues/8363) and
preserves cross-deployer ordering demonstrated by
[#9515](https://github.com/GoogleContainerTools/skaffold/issues/9515).

The build scheduler already provides the intended model: ready nodes are
bounded by a concurrency limit and failures cancel outstanding work.
Deployment needs the same scheduling model over Skaffold configurations.

## Goals

* Add opt-in deployment concurrency.
* Preserve direct `requires` ordering.
* Deploy independent configurations concurrently.
* Keep deployers within one configuration serial.
* Keep current serial behavior by default.

Render and cleanup remain unchanged.

## Command-line contract

Add `--deploy-concurrency` to `dev`, `run`, `debug`, and `deploy`.

| Value | Behavior |
| --- | --- |
| `1` | Serial deployment. Default. |
| `0` | Deploy every ready configuration concurrently. |
| `N > 1` | Deploy at most `N` ready configurations concurrently. |
| `N < 0` | Return a validation error. |

The concurrency unit is one resolved Skaffold configuration. A configuration's
deployers retain their existing order and lifecycle.

## Preserve the `requires` graph

The parser currently emits only a flattened dependency-first list. Deployment
also needs direct edges.

Identify each selected configuration by its source file and source index.
`metadata.name` cannot be graph identity because it is optional. Record every
direct selected dependency and carry those identities into `RunContext`, where
they map to resolved configuration indexes.

Configurations without deployers remain graph nodes so they can preserve a
dependency barrier. The existing dependency-first list determines dispatch
order when the concurrency limit is one.

## Scheduler

Model deployment scheduling on the build scheduler:

1. Scan resolved configurations in order for completed prerequisites.
2. Start ready configurations up to the concurrency limit.
3. Run each configuration's deployers in their existing order.
4. Publish completion and repeat the same scan.
5. Cancel remaining work after the first failure.

A node completes after the same hooks and status-check behavior observed by the
existing `DeployerMux`.

The scheduler validates dependency indexes and resolved order before deployment.

## Deployment isolation

Kubernetes deployers currently share one status monitor per kube-context.
Concurrent configurations would therefore wait on each other's resources.

Key status monitors by kube-context plus an in-memory configuration scope. Each
deployer registers its deployed manifests with that monitor, which limits status
checks to those resource identities. No configuration identity is written to
deployed resources.
Deployers within one configuration share the same monitor. Port forwarding
continues selecting the command-wide run ID.

Synchronize the shared deployment writer so concurrent output does not corrupt
lines while preserving existing event context.

Helm's overrides file must be isolated per invocation because concurrent Helm
configurations can otherwise overwrite it. Helm release concurrency remains
independent of configuration concurrency.

## Compatibility

Every value uses the same dependency-aware deployment scheduler. The default
value of `1` admits one ready configuration at a time and preserves the existing
serial order. Values of `0` or greater than `1` enable concurrent deployment.
No schema field is added.

Configurations that update the same Kubernetes object must express ordering
with `requires`.

## Validation

Unit tests cover:

* direct graph preservation, including unnamed and same-file configs;
* dependency ordering and independent overlap;
* bounded and unlimited concurrency;
* failure cancellation and blocked dependents;
* empty configuration nodes;
* serial deployers within one configuration; and
* cycle rejection.

An integration test covers mixed Kubectl-to-Helm ordering on Kind. Unit tests
cover the dependency graph and concurrency limit. Existing integration coverage
verifies serial ordering.
