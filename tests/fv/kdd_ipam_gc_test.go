// Copyright (c) 2020 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fv_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/fv/containers"
	"github.com/projectcalico/kube-controllers/tests/testutils"
	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	backend "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/options"
)

var _ = Describe("IPAM garbage collection FV tests with short leak grace period", func() {
	var (
		etcd              *containers.Container
		controller        *containers.Container
		apiserver         *containers.Container
		calicoClient      client.Interface
		bc                backend.Client
		k8sClient         *kubernetes.Clientset
		controllerManager *containers.Container
		kconfigfile       *os.File
	)

	BeforeEach(func() {
		// Run etcd.
		etcd = testutils.RunEtcd()

		// Run apiserver.
		apiserver = testutils.RunK8sApiserver(etcd.IP)

		// Write out a kubeconfig file
		var err error
		kconfigfile, err = ioutil.TempFile("", "ginkgo-policycontroller")
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(kconfigfile.Name())
		data := fmt.Sprintf(testutils.KubeconfigTemplate, apiserver.IP)
		_, err = kconfigfile.Write([]byte(data))
		Expect(err).NotTo(HaveOccurred())

		// Make the kubeconfig readable by the container.
		Expect(kconfigfile.Chmod(os.ModePerm)).NotTo(HaveOccurred())

		k8sClient, err = testutils.GetK8sClient(kconfigfile.Name())
		Expect(err).NotTo(HaveOccurred())

		// Wait for the apiserver to be available.
		Eventually(func() error {
			_, err := k8sClient.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
			return err
		}, 30*time.Second, 1*time.Second).Should(BeNil())

		// Apply the necessary CRDs. There can somtimes be a delay between starting
		// the API server and when CRDs are apply-able, so retry here.
		apply := func() error {
			out, err := apiserver.ExecOutput("kubectl", "apply", "-f", "/crds/")
			if err != nil {
				return fmt.Errorf("%s: %s", err, out)
			}
			return nil
		}
		Eventually(apply, 10*time.Second).ShouldNot(HaveOccurred())

		// Make a Calico client and backend client.
		type accessor interface {
			Backend() backend.Client
		}
		calicoClient = testutils.GetCalicoClient(apiconfig.Kubernetes, "", kconfigfile.Name())
		bc = calicoClient.(accessor).Backend()

		// Create an IP pool with room for 4 blocks.
		p := api.NewIPPool()
		p.Name = "test-ippool"
		p.Spec.CIDR = "192.168.0.0/24"
		p.Spec.BlockSize = 26
		p.Spec.NodeSelector = "all()"
		p.Spec.Disabled = false
		_, err = calicoClient.IPPools().Create(context.Background(), p, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Set the leak grace period to a short duration to speed up the test.
		kcc := v3.NewKubeControllersConfiguration()
		kcc.Name = "default"
		kcc.Spec.Controllers.Node = &v3.NodeControllerConfig{LeakGracePeriod: &metav1.Duration{Duration: 5 * time.Second}}
		_, err = calicoClient.KubeControllersConfiguration().Create(context.Background(), kcc, options.SetOptions{})
		Expect(err).ToNot(HaveOccurred())

		// Start the controller.
		controller = testutils.RunPolicyController(apiconfig.Kubernetes, "", kconfigfile.Name(), "node")

		// Run controller manager.
		controllerManager = testutils.RunK8sControllerManager(apiserver.IP)
	})

	AfterEach(func() {
		// Delete the IP pool.
		_, err := calicoClient.IPPools().Delete(context.Background(), "test-ippool", options.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		controllerManager.Stop()
		controller.Stop()
		apiserver.Stop()
		etcd.Stop()
	})

	It("should clean up leaked IP addresses even if the node is still valid", func() {
		nodeA := "node-a"

		// Create the nodes in the Kubernetes API.
		_, err := k8sClient.CoreV1().Nodes().Create(context.Background(),
			&v1.Node{
				TypeMeta:   metav1.TypeMeta{Kind: "Node", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: nodeA},
				Spec:       v1.NodeSpec{},
			},
			metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("creating a serviceaccount for the test", func() {
			sa := &v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: "default",
				},
			}
			Eventually(func() error {
				_, err := k8sClient.CoreV1().ServiceAccounts("default").Create(
					context.Background(),
					sa,
					metav1.CreateOptions{},
				)
				return err
			}, time.Second*10, 500*time.Millisecond).ShouldNot(HaveOccurred())
		})

		handleA := "handle-missing-pod"
		By("allocation an IP address to simulate a leak", func() {
			// Allocate a pod IP address and thus a block and affinity to NodeA.
			// We won't create a corresponding pod API object, thus simulating an IP address leak.
			attrs := map[string]string{"node": nodeA, "pod": "missing-pod", "namespace": "default"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.1"), HandleID: &handleA, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		handleA1 := "handle-valid-ip"
		By("allocating an IP address to simulate a valid IP allocation", func() {
			// We will create a pod API object for this IP, simulating a valid IP that is NOT a leak.
			// We do not expect this IP address to be GC'd.
			attrs := map[string]string{"node": nodeA, "pod": "pod-with-valid-ip", "namespace": "default"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.2"), HandleID: &handleA1, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		var pod *v1.Pod
		By("creating a Pod for the valid IP address", func() {
			pod = &v1.Pod{
				TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "pod-with-valid-ip", Namespace: "default"},
				Spec: v1.PodSpec{
					NodeName: nodeA,
					Containers: []v1.Container{
						{
							Name:    "container1",
							Image:   "busybox",
							Command: []string{"sleep", "3600"},
						},
					},
				},
			}
			pod, err = k8sClient.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		By("updating the Pod to be running", func() {
			pod.Status.PodIP = "192.168.0.2"
			pod.Status.Phase = v1.PodRunning
			_, err = k8sClient.CoreV1().Pods("default").UpdateStatus(context.Background(), pod, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		handleA2 := "handle-valid-ip-not-in-api"
		By("allocating an IP address to simulate a second valid IP allocation", func() {
			// We will create a pod API object for this IP, but we will not update the status,
			// simulating a valid IP that is NOT a leak but has yet to be reported to the k8s API.
			// We do not expect this IP address to be GC'd.
			attrs := map[string]string{"node": nodeA, "pod": "pod-with-valid-ip-not-in-api", "namespace": "default"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.3"), HandleID: &handleA2, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		By("creating a Pod for the valid IP address", func() {
			pod = &v1.Pod{
				TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "pod-with-valid-ip-not-in-api", Namespace: "default"},
				Spec: v1.PodSpec{
					NodeName: nodeA,
					Containers: []v1.Container{
						{
							Name:    "container1",
							Image:   "busybox",
							Command: []string{"sleep", "3600"},
						},
					},
				},
			}
			pod, err = k8sClient.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		handleA3 := "handle-ip-not-match"
		By("allocating an IP address to simulate a second leaked IP allocation", func() {
			// We will create a pod API object for this IP, but the pod will have a different IP reported.
			// This simulates the scenario where the IP address for the pod has changed.
			attrs := map[string]string{"node": nodeA, "pod": "pod-mismatched-ip", "namespace": "default"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.4"), HandleID: &handleA3, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		By("creating a Pod for the mismatched test case", func() {
			pod = &v1.Pod{
				TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "pod-mismatched-ip", Namespace: "default"},
				Spec: v1.PodSpec{
					NodeName: nodeA,
					Containers: []v1.Container{
						{
							Name:    "container1",
							Image:   "busybox",
							Command: []string{"sleep", "3600"},
						},
					},
				},
			}
			pod, err = k8sClient.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		By("updating the Pod to be running, with the wrong IP", func() {
			pod.Status.PodIP = "192.168.30.5"
			pod.Status.Phase = v1.PodRunning
			_, err = k8sClient.CoreV1().Pods("default").UpdateStatus(context.Background(), pod, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		handleAIPIP := "handleAIPIP"
		handleAVXLAN := "handleAVXLAN"
		handleAWG := "handleAWireguard"
		By("allocating tunnel addresses to the node", func() {
			// Allocate an IPIP, VXLAN and WG address to NodeA as well.
			// These only get cleaned up if the node is deleted, so for this test should never be GC'd.
			attrs := map[string]string{"node": nodeA, "type": "ipipTunnelAddress"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.10"), HandleID: &handleAIPIP, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())

			attrs = map[string]string{"node": nodeA, "type": "vxlanTunnelAddress"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.11"), HandleID: &handleAVXLAN, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())

			attrs = map[string]string{"node": nodeA, "type": "wireguardTunnelAddress"}
			err = calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
				IP: net.MustParseIP("192.168.0.12"), HandleID: &handleAWG, Attrs: attrs, Hostname: nodeA,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		// Expect the correct blocks to exist as a result of the IPAM allocations above.
		blocks, err := bc.List(context.Background(), model.BlockListOptions{}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(blocks.KVPairs)).To(Equal(1))
		affs, err := bc.List(context.Background(), model.BlockAffinityListOptions{Host: nodeA}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(affs.KVPairs)).To(Equal(1))

		// Eventually, garbage collection will notice that "missing-pod" does not exist, and the IP addresses
		// will be deleted. The GC takes some time, so wait up to a full minute.
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			// Pod doesn't exist.
			if err := assertIPsWithHandle(calicoClient.IPAM(), handleA, 0); err != nil {
				return err
			}

			// Pod IP mismatch.
			if err := assertIPsWithHandle(calicoClient.IPAM(), handleA3, 0); err != nil {
				return err
			}

			if err := assertNumBlocks(bc, 1); err != nil {
				return err
			}
			return nil
		}, time.Minute, 2*time.Second).Should(BeNil())

		// Expect that the non-leaked addresses are still present.
		err = assertIPsWithHandle(calicoClient.IPAM(), handleA1, 1)
		Expect(err).NotTo(HaveOccurred())
		err = assertIPsWithHandle(calicoClient.IPAM(), handleA2, 1)
		Expect(err).NotTo(HaveOccurred())

		// Expect the tunnel IP to be present as well.
		err = assertIPsWithHandle(calicoClient.IPAM(), handleAIPIP, 1)
		Expect(err).NotTo(HaveOccurred())
	})
})
