A lightweight Kubernetes controller that integrates **vCluster Platform** with **Flux** by automatically creating Flux Kubeconfig reference `Secrets` for VirtualClusterInstances (VCIs). These `Secrets` can then be used as references for Flux `HelmRelease` or `Kustomization` resources, enabling seamless GitOps deployments into dynamic vCluster instances.

---

## Overview

The controller:

- Watches for `VirtualClusterInstance` (VCI) resources that match a configurable **label selector** (defaults to `vcluster.com/import-fluxcd=true`.
- Generates a corresponding **kubeconfig `Secret`** in one or more designated namespaces.
- Copies all `.metadata.labels` from the `VirtualClusterInstance` into the generated `Secret` for downstream use.
- Cleans up the generated `Secret` when the associated `VirtualClusterInstance` is deleted.

This makes it possible to dynamically provision vClusters on the vCluster Platform and immediately bootstrap workloads into them using Flux.

---

## Key Features

- **Selective Sync**: Only VCIs matching the configured label selector are mirrored into `Secrets`.
- **Label Propagation**: All VCI labels are added to the generated `Secret`, making them available for Flux `ClusterGenerator` or other label-driven automation.
- **Kubeconfig Management**: Automatically manages lifecycle of kubeconfig `Secrets` for Flux.
- **Automatic Cleanup**: When a VCI is removed or no longer matches the selector, the corresponding `Secret` is deleted.

