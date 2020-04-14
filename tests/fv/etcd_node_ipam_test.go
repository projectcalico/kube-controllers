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
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/options"
)

var _ = Describe("kube-controllers IPAM FV tests (etcd mode)", func() {
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

		// Create an IP pool with room for 4 blocks.
		p := api.NewIPPool()
		p.Name = "test-ippool"
		p.Spec.CIDR = "192.168.0.0/24"
		p.Spec.BlockSize = 26
		p.Spec.NodeSelector = "all()"
		p.Spec.Disabled = false
		_, err = c.IPPools().Create(context.Background(), p, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		// Delete the IP pool.
		_, err := c.IPPools().Delete(context.Background(), "test-ippool", options.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		os.Remove(kconfigFile.Name())
		controllerManager.Stop()
		nodeController.Stop()
		apiserver.Stop()
		etcd.Stop()
	})

	// This test makes sure our IPAM garbage collection properly handles when the Kubernetes node name
	// does not match the Calico node name in etcd.
	It("should properly garbage collect IP addresses for mismatched node names", func() {
		// Run controller.
		nodeController = testutils.RunNodeController(apiconfig.EtcdV3, etcd.IP, kconfigFile.Name(), false)

		// Create a kubernetes node.
		kn := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: kNodeName}}
		_, err := k8sClient.CoreV1().Nodes().Create(kn)
		Expect(err).NotTo(HaveOccurred())

		// Create a Calico node with a reference to it.
		cn := calicoNode(c, cNodeName, kNodeName, map[string]string{})
		_, err = c.Nodes().Create(context.Background(), cn, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Allocate an IP address on the Calico node.
		// Note: it refers to a pod that doesn't exist, but this is OK since we only clean up addressses
		// when their node goes away, and the node exists.
		handleA := "handleA"
		attrs := map[string]string{"node": cNodeName, "pod": "pod-a", "namespace": "default"}
		err = c.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
			IP: net.MustParseIP("192.168.0.1"), HandleID: &handleA, Attrs: attrs, Hostname: cNodeName,
		})
		Expect(err).NotTo(HaveOccurred())

		// Create and delete an unrelated node. This should trigger the controller
		// to do a sync.
		kn2 := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "other-node"}}
		_, err = k8sClient.CoreV1().Nodes().Create(kn2)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.CoreV1().Nodes().Delete(kn2.Name, nil)
		Expect(err).NotTo(HaveOccurred())

		// The IPAM allocation should be untouched, since the Kubernetes node which is bound to
		// the Calico node is still present.
		Consistently(func() error {
			return assertIPsWithHandle(c.IPAM(), handleA, 1)
		}, 5*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

		// Delete the Kubernetes node with the allocation.
		err = k8sClient.CoreV1().Nodes().Delete(kn.Name, nil)
		Expect(err).NotTo(HaveOccurred())

		// Now the IP should have been cleaned up.
		Eventually(func() error {
			return assertIPsWithHandle(c.IPAM(), handleA, 0)
		}, 5*time.Second, 1*time.Second).ShouldNot(HaveOccurred())
	})
})
