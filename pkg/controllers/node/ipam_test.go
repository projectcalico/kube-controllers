// Copyright (c) 2021 Tigera, Inc. All rights reserved.
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
package node

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apiv3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/kube-controllers/pkg/config"
	libapiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/options"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("IPAM controller UTs", func() {

	var c *ipamController
	var cli client.Interface
	var cs kubernetes.Interface

	BeforeEach(func() {
		// Create a fake clientset with nothing in it.
		cs = fake.NewSimpleClientset()

		// Create a fake Calico client.
		cli = NewFakeCalicoClient()

		// Config for the test.
		cfg := config.NodeControllerConfig{
			LeakGracePeriod: &metav1.Duration{Duration: 5 * time.Second},
		}

		// Create a new controller. We don't register with a data feed,
		// as the tests themselves will drive the controller.
		c = NewIPAMController(cfg, cli, cs)
	})

	It("should handle node updates and maintain its node cache", func() {
		// Start the controller.
		c.Start(make(chan struct{}))

		calicoNodeName := "cname"
		key := model.ResourceKey{Name: calicoNodeName, Kind: libapiv3.KindNode}
		n := libapiv3.Node{}
		n.Name = calicoNodeName
		n.Spec.OrchRefs = []libapiv3.OrchRef{
			{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &n,
		}
		update := bapi.Update{
			KVPair:     kvp,
			UpdateType: bapi.UpdateTypeKVNew,
		}

		// Send a new Node update.
		c.onUpdate(update)

		// Check internal state is updated. Because the main loop is
		// in a different goroutine, this might take a short period of time.
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.kubernetesNodesByCalicoName[n.Name]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal("kname"), "Cache not updated after ADD")

		// Send an update that changes the Kubernetes node name.
		// This should be rare, or maybe never happen, but we should handle it anyway.
		n.Spec.OrchRefs[0].NodeName = "kname2"
		update.UpdateType = bapi.UpdateTypeKVUpdated
		c.onUpdate(update)

		// Expect the cache to be updated.
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.kubernetesNodesByCalicoName[n.Name]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal("kname2"), "Cache not updated after UPDATE")

		// Send a delete for the node, which should remove the entry from the cache.
		update.KVPair.Value = nil
		c.onUpdate(update)
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.kubernetesNodesByCalicoName[n.Name]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(""), "Cache not updated after DELETE")
	})

	It("should handle adding and deleting blocks", func() {
		// Start the controller.
		c.Start(make(chan struct{}))

		// Add a new block with no allocations.
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{nil, nil, nil, nil},
			Unallocated: []int{0, 1, 2, 3},
			Attributes:  []model.AllocationAttribute{},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Expect new entries in the internal maps.
		Eventually(func() model.KVPair {
			done := c.pause()
			defer done()
			return c.allBlocks[blockCIDR]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(kvp))
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.nodesByBlock["10.0.0.0/30"]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal("cnode"))
		Eventually(func() map[string]*allocation {
			done := c.pause()
			defer done()
			return c.allocationsByNode["cnode"]
		}, 1*time.Second, 100*time.Millisecond).Should(BeNil())

		// Now, allocate an address in the block and send it in as an update.
		idx := 0
		handle := "test-handle"
		b.Allocations[0] = &idx
		b.Unallocated = []int{1, 2, 3}
		b.Attributes = append(b.Attributes, model.AllocationAttribute{
			AttrPrimary: &handle,
			AttrSecondary: map[string]string{
				ipam.AttributeNode:      "cnode",
				ipam.AttributePod:       "test-pod",
				ipam.AttributeNamespace: "test-namespace",
			},
		})
		c.onUpdate(update)

		expectedAllocation := &allocation{
			ip:     "10.0.0.0",
			handle: handle,
			attrs:  b.Attributes[0].AttrSecondary,
		}

		// Expect new entries in the internal maps.
		Eventually(func() model.KVPair {
			done := c.pause()
			defer done()
			return c.allBlocks[blockCIDR]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(kvp))
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.nodesByBlock["10.0.0.0/30"]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal("cnode"))
		Eventually(func() map[string]*allocation {
			done := c.pause()
			defer done()
			return c.allocationsByNode["cnode"]
		}, 1*time.Second, 100*time.Millisecond).ShouldNot(BeNil())

		Eventually(func() *allocation {
			done := c.pause()
			defer done()
			return c.allocationsByBlock[blockCIDR][handle]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(expectedAllocation))

		Eventually(func() *allocation {
			done := c.pause()
			defer done()
			return c.allocationsByNode["cnode"][handle]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(expectedAllocation))

		// Release the address from above and expect original state to be restored.
		b.Allocations[0] = nil
		b.Unallocated = []int{1, 2, 3, 0}
		b.Attributes = []model.AllocationAttribute{}
		c.onUpdate(update)
		Eventually(func() model.KVPair {
			done := c.pause()
			defer done()
			return c.allBlocks[blockCIDR]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(kvp))
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.nodesByBlock["10.0.0.0/30"]
		}, 5*time.Second, time.Second).Should(Equal("cnode"))
		Eventually(func() map[string]*allocation {
			done := c.pause()
			defer done()
			return c.allocationsByNode["cnode"]
		}, 1*time.Second, 100*time.Millisecond).Should(BeNil())
		Eventually(func() *allocation {
			done := c.pause()
			defer done()
			return c.allocationsByBlock[blockCIDR][handle]
		}, 1*time.Second, 100*time.Millisecond).Should(BeNil())

		Eventually(func() *allocation {
			done := c.pause()
			defer done()
			return c.allocationsByNode["cnode"][handle]
		}, 1*time.Second, 100*time.Millisecond).Should(BeNil())

		// Delete the block and expect everything to be cleaned up.
		update.Value = nil
		c.onUpdate(update)
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeFalse())
		Eventually(func() map[string]*allocation {
			done := c.pause()
			defer done()
			return c.allocationsByNode["cnode"]
		}, 1*time.Second, 100*time.Millisecond).Should(BeNil())
		Eventually(func() map[string]*allocation {
			done := c.pause()
			defer done()
			return c.allocationsByBlock[blockCIDR]
		}, 1*time.Second, 100*time.Millisecond).Should(BeNil())
	})

	It("should handle node deletion properly", func() {
		// Start the controller.
		c.Start(make(chan struct{}))

		// Add a new block with one allocation.
		idx := 0
		handle := "test-handle"
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{&idx, nil, nil, nil},
			Unallocated: []int{1, 2, 3},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       "test-pod",
						ipam.AttributeNamespace: "test-namespace",
					},
				},
			},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Wait for internal caches to update.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Mark the syncer as InSync so that the GC will be enabled.
		c.onStatusUpdate(bapi.InSync)

		// Trigger a node deletion. The node referenced in the allocation above
		// never existed in the k8s API and neither does the pod, so this should result in a GC.
		c.OnKubernetesNodeDeleted()

		// Confirm the IP and block affinity were released.
		fakeClient := cli.IPAM().(*fakeIPAMClient)
		Eventually(func() bool {
			return fakeClient.handlesReleased[handle]
		}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
		Eventually(func() bool {
			return fakeClient.affinityReleased("cnode")
		}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
	})

	It("should clean up leaked IP addresses", func() {
		// Add a new block with one allocation - on a valid node but no corresponding pod.
		n := libapiv3.Node{}
		n.Name = "cnode"
		n.Spec.OrchRefs = []libapiv3.OrchRef{{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes}}
		_, err := cli.Nodes().Create(context.TODO(), &n, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Add the matching Kubernetes node.
		kn := v1.Node{}
		kn.Name = "kname"
		_, err = cs.CoreV1().Nodes().Create(context.TODO(), &kn, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Start the controller.
		c.Start(make(chan struct{}))

		idx := 0
		handle := "test-handle"
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{&idx, nil, nil, nil},
			Unallocated: []int{1, 2, 3},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       "test-pod",
						ipam.AttributeNamespace: "test-namespace",
					},
				},
			},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Wait for internal caches to update.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Mark the syncer as InSync so that the GC will be triggered.
		c.onStatusUpdate(bapi.InSync)

		// Confirm the IP was released. We start the controller with a 5s reconcile period,
		// so this should take at most 15s.
		fakeClient := cli.IPAM().(*fakeIPAMClient)
		Eventually(func() bool {
			return fakeClient.handlesReleased[handle]
		}, 15*time.Second, 100*time.Millisecond).Should(BeTrue())

		// The block should remain.
		Consistently(func() bool {
			return fakeClient.affinityReleased("cnode")
		}, 5*time.Second, 100*time.Millisecond).Should(BeFalse())
	})

	It("should handle blocks losing their affinity", func() {
		// Create Calico and k8s nodes for the test.
		n := libapiv3.Node{}
		n.Name = "cnode"
		n.Spec.OrchRefs = []libapiv3.OrchRef{{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes}}
		_, err := cli.Nodes().Create(context.TODO(), &n, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kn := v1.Node{}
		kn.Name = "kname"
		_, err = cs.CoreV1().Nodes().Create(context.TODO(), &kn, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create a pod for the allocation so that it doesn't get GC'd.
		pod := v1.Pod{}
		pod.Name = "test-pod"
		pod.Namespace = "test-namespace"
		_, err = cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), &pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Start the controller.
		c.Start(make(chan struct{}))

		// Add a new block with one allocation, affine to cnode.
		idx := 0
		handle := "test-handle"
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{&idx, nil, nil, nil},
			Unallocated: []int{1, 2, 3},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       pod.Name,
						ipam.AttributeNamespace: pod.Namespace,
					},
				},
			},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Wait for controller state to update. The block
		// should appear in allBlocks.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// And, the allocationsByNode map should receive an entry.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allocationsByNode["cnode"]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Now release the block's affinity with no other changes.
		b2 := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    nil,
			Allocations: []*int{&idx, nil, nil, nil},
			Unallocated: []int{1, 2, 3},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       pod.Name,
						ipam.AttributeNamespace: pod.Namespace,
					},
				},
			},
		}
		kvp2 := model.KVPair{
			Key:   key,
			Value: &b2,
		}
		update2 := bapi.Update{KVPair: kvp2, UpdateType: bapi.UpdateTypeKVUpdated}
		c.onUpdate(update2)

		// Block still exists.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// cnode still has an allocation from the block, even though it is not affine.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allocationsByNode["cnode"]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// However, the block is no longer assigned to cnode.
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.nodesByBlock["10.0.0.0/30"]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(""))

		// Finally, release the IP in the now orphaned block. Removing the IP should
		// result in the remaining state being cleaned up.
		By("releasing the IP address", func() {
			b3 := model.AllocationBlock{
				CIDR:        cidr,
				Affinity:    nil,
				Allocations: []*int{nil, nil, nil, nil},
				Unallocated: []int{1, 2, 3, 0},
				Attributes:  []model.AllocationAttribute{},
			}
			kvp3 := model.KVPair{
				Key:   key,
				Value: &b3,
			}
			update3 := bapi.Update{KVPair: kvp3, UpdateType: bapi.UpdateTypeKVUpdated}
			c.onUpdate(update3)
		})

		// Block still exists, but is empty. Note: In a real system,
		// this would trigger deletion of the block.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// cnode no longer has any allocations
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allocationsByNode["cnode"]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeFalse())

		// The block still has no affinity.
		Eventually(func() string {
			done := c.pause()
			defer done()
			return c.nodesByBlock["10.0.0.0/30"]
		}, 1*time.Second, 100*time.Millisecond).Should(Equal(""))
	})

	It("should not clean up leaked addresses if no grace period set", func() {
		// Set the controller's grace period to 0.
		c.config.LeakGracePeriod = &metav1.Duration{Duration: 0 * time.Second}

		// Add a new block with one allocation - on a valid node but no corresponding pod.
		n := libapiv3.Node{}
		n.Name = "cnode"
		n.Spec.OrchRefs = []libapiv3.OrchRef{{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes}}
		_, err := cli.Nodes().Create(context.TODO(), &n, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Add the matching Kubernetes node.
		kn := v1.Node{}
		kn.Name = "kname"
		_, err = cs.CoreV1().Nodes().Create(context.TODO(), &kn, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Start the controller.
		c.Start(make(chan struct{}))

		idx := 0
		handle := "test-handle"
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{&idx, nil, nil, nil},
			Unallocated: []int{1, 2, 3},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       "test-pod",
						ipam.AttributeNamespace: "test-namespace",
					},
				},
			},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Wait for internal caches to update.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Mark the syncer as InSync so that the GC will be triggered.
		c.onStatusUpdate(bapi.InSync)

		// Confirm the IP was NOT released. We start the controller with a 5s reconcile period,
		// so this should take at most 15s.
		fakeClient := cli.IPAM().(*fakeIPAMClient)
		Eventually(func() bool {
			return fakeClient.handlesReleased[handle]
		}, 15*time.Second, 100*time.Millisecond).Should(BeFalse())

		// The block should remain.
		Consistently(func() bool {
			return fakeClient.affinityReleased("cnode")
		}, 5*time.Second, 100*time.Millisecond).Should(BeFalse())
	})

	It("should clean up empty blocks", func() {
		// Create Calico and k8s nodes for the test.
		n := libapiv3.Node{}
		n.Name = "cnode"
		n.Spec.OrchRefs = []libapiv3.OrchRef{{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes}}
		_, err := cli.Nodes().Create(context.TODO(), &n, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kn := v1.Node{}
		kn.Name = "kname"
		_, err = cs.CoreV1().Nodes().Create(context.TODO(), &kn, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create a pod for the allocation so that it doesn't get GC'd.
		pod := v1.Pod{}
		pod.Name = "test-pod"
		pod.Namespace = "test-namespace"
		_, err = cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), &pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Start the controller.
		c.Start(make(chan struct{}))

		// Add a new block with one allocation, affine to cnode.
		idx := 0
		handle := "test-handle"
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{&idx, nil, nil, nil},
			Unallocated: []int{1, 2, 3},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       pod.Name,
						ipam.AttributeNamespace: pod.Namespace,
					},
				},
			},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Wait for controller state to update. The block
		// should appear in allBlocks.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// And, the allocationsByNode map should receive an entry.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allocationsByNode["cnode"]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Add a new block with no allocations.
		cidr2 := net.MustParseCIDR("10.0.0.4/30")
		key2 := model.BlockKey{CIDR: cidr2}
		b2 := model.AllocationBlock{
			CIDR:        cidr2,
			Affinity:    &aff,
			Allocations: []*int{nil, nil, nil, nil},
			Unallocated: []int{0, 1, 2, 3},
		}
		kvp2 := model.KVPair{
			Key:   key2,
			Value: &b2,
		}
		blockCIDR2 := kvp2.Key.(model.BlockKey).CIDR.String()
		update2 := bapi.Update{KVPair: kvp2, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update2)

		// The controller should now recognize an empty block.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.emptyBlocks[blockCIDR2]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Mark the syncer as InSync so that the GC will be enabled.
		c.onStatusUpdate(bapi.InSync)

		// The empty block should be released.
		fakeClient := cli.IPAM().(*fakeIPAMClient)
		Eventually(func() bool {
			return fakeClient.affinityReleased(fmt.Sprintf("%s/%s", blockCIDR2, "cnode"))
		}, 15*time.Second, 100*time.Millisecond).Should(BeTrue())
	})

	It("should NOT clean up empty blocks if the node is full", func() {
		// Create Calico and k8s nodes for the test.
		n := libapiv3.Node{}
		n.Name = "cnode"
		n.Spec.OrchRefs = []libapiv3.OrchRef{{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes}}
		_, err := cli.Nodes().Create(context.TODO(), &n, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kn := v1.Node{}
		kn.Name = "kname"
		_, err = cs.CoreV1().Nodes().Create(context.TODO(), &kn, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create a pod for the allocation so that it doesn't get GC'd.
		pod := v1.Pod{}
		pod.Name = "test-pod"
		pod.Namespace = "test-namespace"
		_, err = cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), &pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Start the controller.
		c.Start(make(chan struct{}))

		// Add a new block that is full.
		idx := 0
		handle := "test-handle"
		cidr := net.MustParseCIDR("10.0.0.0/30")
		aff := "host:cnode"
		key := model.BlockKey{CIDR: cidr}
		b := model.AllocationBlock{
			CIDR:        cidr,
			Affinity:    &aff,
			Allocations: []*int{&idx, &idx, &idx, &idx},
			Unallocated: []int{},
			Attributes: []model.AllocationAttribute{
				{
					AttrPrimary: &handle,
					AttrSecondary: map[string]string{
						ipam.AttributeNode:      "cnode",
						ipam.AttributePod:       pod.Name,
						ipam.AttributeNamespace: pod.Namespace,
					},
				},
			},
		}
		kvp := model.KVPair{
			Key:   key,
			Value: &b,
		}
		blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
		update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update)

		// Wait for controller state to update. The block
		// should appear in allBlocks.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allBlocks[blockCIDR]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// And, the allocationsByNode map should receive an entry.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.allocationsByNode["cnode"]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Add a new block with no allocations.
		cidr2 := net.MustParseCIDR("10.0.0.4/30")
		key2 := model.BlockKey{CIDR: cidr2}
		b2 := model.AllocationBlock{
			CIDR:        cidr2,
			Affinity:    &aff,
			Allocations: []*int{nil, nil, nil, nil},
			Unallocated: []int{0, 1, 2, 3},
		}
		kvp2 := model.KVPair{
			Key:   key2,
			Value: &b2,
		}
		blockCIDR2 := kvp2.Key.(model.BlockKey).CIDR.String()
		update2 := bapi.Update{KVPair: kvp2, UpdateType: bapi.UpdateTypeKVNew}
		c.onUpdate(update2)

		// The controller should now recognize an empty block.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			_, ok := c.emptyBlocks[blockCIDR2]
			return ok
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Mark the syncer as InSync so that the GC will be enabled.
		c.onStatusUpdate(bapi.InSync)

		// The empty block should NOT be released, because the other block on the node is full.
		fakeClient := cli.IPAM().(*fakeIPAMClient)
		Consistently(func() bool {
			return fakeClient.affinityReleased(fmt.Sprintf("%s/%s", blockCIDR2, "cnode"))
		}, 5*time.Second, 100*time.Millisecond).Should(BeFalse())
	})

	It("should NOT clean up all blocks assigned to a node", func() {
		// Create Calico and k8s nodes for the test.
		n := libapiv3.Node{}
		n.Name = "cnode"
		n.Spec.OrchRefs = []libapiv3.OrchRef{{NodeName: "kname", Orchestrator: apiv3.OrchestratorKubernetes}}
		_, err := cli.Nodes().Create(context.TODO(), &n, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())
		kn := v1.Node{}
		kn.Name = "kname"
		_, err = cs.CoreV1().Nodes().Create(context.TODO(), &kn, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create a pod for the allocation so that it doesn't get GC'd.
		pod := v1.Pod{}
		pod.Name = "test-pod"
		pod.Namespace = "test-namespace"
		_, err = cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), &pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Start the controller.
		c.Start(make(chan struct{}))

		// Add 5 empty blocks to the node.
		for i := 0; i < 5; i++ {
			unallocated := make([]int, 64)
			for i := 0; i < len(unallocated); i++ {
				unallocated[i] = i
			}
			cidr := net.MustParseCIDR(fmt.Sprintf("10.0.%d.0/24", i))
			aff := "host:cnode"
			key := model.BlockKey{CIDR: cidr}
			b := model.AllocationBlock{
				CIDR:        cidr,
				Affinity:    &aff,
				Allocations: make([]*int, 64),
				Unallocated: unallocated,
				Attributes:  []model.AllocationAttribute{},
			}
			kvp := model.KVPair{
				Key:   key,
				Value: &b,
			}
			update := bapi.Update{KVPair: kvp, UpdateType: bapi.UpdateTypeKVNew}
			c.onUpdate(update)
		}

		// Wait for controller state to update with all blocks.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			return len(c.allBlocks) == 5
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// The controller should recognize them as empty.
		Eventually(func() bool {
			done := c.pause()
			defer done()
			return len(c.emptyBlocks) == 5
		}, 1*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Mark the syncer as InSync so that the GC will be enabled.
		c.onStatusUpdate(bapi.InSync)

		// 4 out of the 5 empty blocks should be released, but not all.
		numBlocks := func() int {
			done := c.pause()
			defer done()
			return len(c.blocksByNode["cnode"])
		}
		Eventually(numBlocks, 15*time.Second, 100*time.Millisecond).Should(Equal(1))
		Consistently(numBlocks, 5*time.Second, 100*time.Millisecond).Should(Equal(1))
		numBlocks = func() int {
			done := c.pause()
			defer done()
			return len(c.emptyBlocks)
		}
		Eventually(numBlocks, 1*time.Second, 100*time.Millisecond).Should(Equal(1))
		Consistently(numBlocks, 3*time.Second, 100*time.Millisecond).Should(Equal(1))

	})

})
