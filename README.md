# vcluster-platform-flux-secret-controller
Watches **VirtualClusterInstances** matching a label selector and generates a vCluster Platform **kubeconfig** for that instance in designated namespaces and includes all meta.labels of the **VirtualClusterInstance**. When the **VirtualClusterInstance** is deleted the generated secrets are also cleaned up.
