## Calico Network Policy for Kubernetes 

This repository contains the Calico Kubernetes policy controller, which implements the Kubernetes network policy API.  

![calico-policy-controller](calico-policy-controller.png)

The controller uses the Kubernetes v1beta1 network policy API to configure Calico network policy.  The policy controller addon is deployed on top of Kubernetes as a set of replicated pods, and can also be installed as a static pod. 

Calico can enforce NetworkPolicy on top of:
- [Calico BGP networking](https://github.com/projectcalico/calico-containers/blob/master/docs/cni/kubernetes/KubernetesIntegration.md)
- [Flannel networking](https://github.com/tigera/canal)
- [GCE native cloud-provider networking](http://kubernetes.io/docs/getting-started-guides/gce/)

See the documentation on [network policy in Kubernetes](http://kubernetes.io/docs/user-guide/networkpolicies/) for more information on how to use NetworkPolicy. 

Calico also supports an [experimental mode](http://docs.projectcalico.org/v2.0/getting-started/kubernetes/installation/hosted/k8s-backend/) which 
uses the Kubernetes API directly without the need for its own
etcd cluster. When running in this mode, the policy controller is not required.

### Resources

* [Configuration guide](configuration.md)
