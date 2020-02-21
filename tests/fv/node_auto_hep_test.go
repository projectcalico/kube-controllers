// Copyright (c) 2017 Tigera, Inc. All rights reserved.
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
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
)

var _ = Describe("Auto Hostendpoint tests", func() {
	var (
		etcd              *containers.Container
		nodeController    *containers.Container
		apiserver         *containers.Container
		c                 client.Interface
		k8sClient         *kubernetes.Clientset
		controllerManager *containers.Container
		kconfigFile       *os.File
	)

	const kNodeName = "k8snodename"
	const cNodeName = "calinodename"

	BeforeEach(func() {
		// Run etcd.
		etcd = testutils.RunEtcd()
		c = testutils.GetCalicoClient(apiconfig.EtcdV3, etcd.IP, "")

		// Run apiserver.
		apiserver = testutils.RunK8sApiserver(etcd.IP)

		// Write out a kubeconfig file
		var err error
		kconfigFile, err = ioutil.TempFile("", "ginkgo-nodecontroller")
		Expect(err).NotTo(HaveOccurred())
		//defer os.Remove(kconfigFile.Name())
		data := fmt.Sprintf(testutils.KubeconfigTemplate, apiserver.IP)
		_, err = kconfigFile.Write([]byte(data))
		Expect(err).NotTo(HaveOccurred())

		k8sClient, err = testutils.GetK8sClient(kconfigFile.Name())
		Expect(err).NotTo(HaveOccurred())

		// Wait for the apiserver to be available.
		Eventually(func() error {
			_, err := k8sClient.CoreV1().Namespaces().List(metav1.ListOptions{})
			return err
		}, 30*time.Second, 1*time.Second).Should(BeNil())

		// Run controller manager.  Empirically it can take around 10s until the
		// controller manager is ready to create default service accounts, even
		// when the hyperkube image has already been downloaded to run the API
		// server.  We use Eventually to allow for possible delay when doing
		// initial pod creation below.
		controllerManager = testutils.RunK8sControllerManager(apiserver.IP)
	})

	AfterEach(func() {
		os.Remove(kconfigFile.Name())
		controllerManager.Stop()
		nodeController.Stop()
		apiserver.Stop()
		etcd.Stop()
	})

	runController := func() {
		nodeController = testutils.RunNodeController(apiconfig.EtcdV3, etcd.IP, kconfigFile.Name(), "")
	}

	It("should create hostendpoints for Calico nodes and sync labels", func() {
		runController()

		// Create a kubernetes node with some labels.
		kn := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: kNodeName,
				Labels: map[string]string{
					"label1": "value1",
				},
			},
		}
		_, err := k8sClient.CoreV1().Nodes().Create(kn)
		Expect(err).NotTo(HaveOccurred())

		// Create a Calico node with a reference to it.
		cn := api.NewNode()
		cn.Name = cNodeName
		cn.Labels = map[string]string{"calico-label": "calico-value", "label1": "badvalue"}
		cn.Spec = api.NodeSpec{
			BGP: &api.NodeBGPSpec{
				IPv4Address:        "172.16.1.1",
				IPv6Address:        "fe80::1",
				IPv4IPIPTunnelAddr: "192.168.100.1",
			},
			OrchRefs: []api.OrchRef{
				{
					NodeName:     kNodeName,
					Orchestrator: "k8s",
				},
			},
		}
		_, err = c.Nodes().Create(context.Background(), cn, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Expect the node label to sync.
		expected := map[string]string{"label1": "value1", "calico-label": "calico-value"}
		Eventually(func() error { return testutils.ExpectNodeLabels(c, expected, cNodeName) },
			time.Second*15, 500*time.Millisecond).Should(BeNil())

		expectedHepName := cn.Name + "-auto-hep"

		// Expect a wildcard hostendpoint to be created.
		var hep *api.HostEndpoint
		Eventually(func() *api.HostEndpoint {
			hep, _ = c.HostEndpoints().Get(context.Background(), expectedHepName, options.GetOptions{})
			return hep
		}, time.Second*2, 500*time.Millisecond).ShouldNot(BeNil())

		Expect(hep.Spec.InterfaceName).To(Equal("*"))
		Expect(hep.Spec.ExpectedIPs).To(ConsistOf([]string{"172.16.1.1", "fe80::1", "192.168.100.1"}))
		Expect(hep.Spec.Profiles).To(BeEmpty())
		Expect(hep.Spec.Ports).To(BeEmpty())

		// Expect the wildcard hostendpoint to have the same values as the node.
		Expect(hep.Labels).To(HaveKeyWithValue("label1", "value1"))
		Expect(hep.Labels).To(HaveKeyWithValue("calico-label", "calico-value"))
		Expect(hep.Labels).To(HaveKeyWithValue("projectcalico.org/created-by", "calico-kube-controllers"))
		Expect(len(hep.Labels)).To(Equal(3))

		// Update the Kubernetes node labels.
		kn, err = k8sClient.CoreV1().Nodes().Get(kn.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kn.Labels["label1"] = "value2"
		_, err = k8sClient.CoreV1().Nodes().Update(kn)
		Expect(err).NotTo(HaveOccurred())

		// Expect the node labels to sync.
		expected = map[string]string{"label1": "value2", "calico-label": "calico-value"}
		Eventually(func() error { return testutils.ExpectNodeLabels(c, expected, cNodeName) },
			time.Second*15, 500*time.Millisecond).Should(BeNil())

		// Expect the hostendpoint labels to sync.
		expected = map[string]string{
			"label1":                       "value2",
			"calico-label":                 "calico-value",
			"projectcalico.org/created-by": "calico-kube-controllers",
		}
		Eventually(func() error { return testutils.ExpectHostendpointLabels(c, expected, expectedHepName) },
			time.Second*15, 500*time.Millisecond).Should(BeNil())

		// Delete the Kubernetes node.
		err = k8sClient.CoreV1().Nodes().Delete(kNodeName, &metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() *api.Node {
			node, _ := c.Nodes().Get(context.Background(), cNodeName, options.GetOptions{})
			return node
		}, time.Second*2, 500*time.Millisecond).Should(BeNil())

		// Expect the hostendpoint for the node to be deleted.
		Eventually(func() error { return testutils.ExpectHostendpointDeleted(c, cn.Name) },
			time.Second*2, 500*time.Millisecond).Should(BeNil())
	})

	It("should clean up dangling hostendpoints and create heps for nodes without them", func() {
		// Create a wildcard HEP that matches what might have been created
		// automatically by kube-controllers. But we won't have a corresponding
		// node for this HEP.
		hep := &api.HostEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testnode-auto-hep",
				Labels: map[string]string{
					"projectcalico.org/created-by": "calico-kube-controllers",
				},
			},
			Spec: api.HostEndpointSpec{
				Node:          "testnode",
				InterfaceName: "*",
				ExpectedIPs:   []string{"192.168.1.100"},
			},
		}
		_, err := c.HostEndpoints().Create(context.Background(), hep, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create another wildcard HEP but this one isn't managed by Calico.
		hep2 := &api.HostEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "user-managed-hep",
				Labels: map[string]string{
					"env": "staging",
				},
			},
			Spec: api.HostEndpointSpec{
				Node:          "testnode",
				InterfaceName: "*",
			},
		}
		_, err = c.HostEndpoints().Create(context.Background(), hep2, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create a kubernetes node
		kn := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: kNodeName,
				Labels: map[string]string{
					"label1": "value1",
				},
			},
		}
		_, err = k8sClient.CoreV1().Nodes().Create(kn)
		Expect(err).NotTo(HaveOccurred())

		// Create a Calico node with a reference to it.
		cn := api.NewNode()
		cn.Name = cNodeName
		cn.Spec = api.NodeSpec{
			BGP: &api.NodeBGPSpec{
				IPv4Address:        "172.16.1.1",
				IPv4IPIPTunnelAddr: "192.168.100.1",
			},
			OrchRefs: []api.OrchRef{
				{
					NodeName:     kNodeName,
					Orchestrator: "k8s",
				},
			},
		}
		_, err = c.Nodes().Create(context.Background(), cn, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Run the controller now.
		runController()

		// Expect the dangling hostendpoint to be deleted.
		Eventually(func() error { return testutils.ExpectHostendpointDeleted(c, "testnode-auto-hep") },
			time.Second*2, 500*time.Millisecond).Should(BeNil())

		// Expect the user's own hostendpoints to still exist.
		var userHep *api.HostEndpoint
		Eventually(func() *api.HostEndpoint {
			userHep, _ = c.HostEndpoints().Get(context.Background(), "user-managed-hep", options.GetOptions{})
			return userHep
		}, time.Second*2, 500*time.Millisecond).ShouldNot(BeNil())
		Expect(userHep.Labels).To(BeEquivalentTo(hep2.Labels))
		Expect(userHep.Spec.Node).To(Equal(hep2.Spec.Node))
		Expect(userHep.Spec.InterfaceName).To(Equal(hep2.Spec.InterfaceName))

		var autoHep *api.HostEndpoint
		autoHepName := cNodeName + "-auto-hep"
		Eventually(func() *api.HostEndpoint {
			autoHep, _ = c.HostEndpoints().Get(context.Background(), autoHepName, options.GetOptions{})
			return autoHep
		}, time.Second*2, 500*time.Millisecond).ShouldNot(BeNil())
	})
})
