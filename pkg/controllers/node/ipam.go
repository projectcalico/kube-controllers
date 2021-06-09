// Copyright (c) 2019 Tigera, Inc. All rights reserved.
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
	"net"
	"strings"
	"time"

	"github.com/projectcalico/kube-controllers/pkg/config"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	cerrors "github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
)

var (
	ipsGauge      *prometheus.GaugeVec
	blocksGauge   *prometheus.GaugeVec
	borrowedGauge *prometheus.GaugeVec
)

func registerPrometheusMetrics() {
	// Total IP allocations.
	ipsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ipam_allocations_per_node",
		Help: "Number of IPs allocated",
	}, []string{"node"})
	prometheus.MustRegister(ipsGauge)

	// Borrowed IPs.
	borrowedGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ipam_allocations_borrowed_per_node",
		Help: "Number of allocated IPs that are from non-affine blocks.",
	}, []string{"node"})
	prometheus.MustRegister(borrowedGauge)

	// Blocks per-node.
	blocksGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ipam_blocks_per_node",
		Help: "Number of blocks in IPAM",
	}, []string{"node"})
	prometheus.MustRegister(blocksGauge)
}

func NewIPAMController(cfg config.NodeControllerConfig, c client.Interface, cs *kubernetes.Clientset) *ipamController {
	return &ipamController{
		client:      c,
		clientset:   cs,
		config:      cfg,
		rl:          workqueue.DefaultControllerRateLimiter(),
		syncChan:    make(chan interface{}, 1),
		blockUpdate: make(chan model.KVPair),

		allBlocks:          make(map[string]model.KVPair),
		allocationsByBlock: make(map[string]map[string]*allocation),
		allocationsByNode:  make(map[string]map[string]*allocation),
	}

}

type allocation struct {
	ip     string
	handle string
	attrs  map[string]string

	timestamp     *time.Time
	confirmedLeak bool
}

func (a *allocation) fields() log.Fields {
	f := log.Fields{
		"ip":     a.ip,
		"handle": a.handle,
		"node":   a.attrs[ipam.AttributeNode],
	}

	if a.isPodIP() {
		ns := a.attrs[ipam.AttributeNamespace]
		pod := a.attrs[ipam.AttributePod]
		f["pod"] = fmt.Sprintf("%s/%s", ns, pod)
	}

	return f
}

func (a *allocation) node() string {
	if node, ok := a.attrs[ipam.AttributeNode]; ok {
		return node
	}
	return ""
}

func (a *allocation) markLeak(leakGracePeriod time.Duration) {
	if a.timestamp == nil {
		t := time.Now()
		a.timestamp = &t
		log.WithFields(a.fields()).Infof("Candidate IP leak")
	}

	if time.Since(*a.timestamp) > leakGracePeriod && !a.isConfirmedLeak() {
		a.markConfirmedLeak()
	}
}

func (a *allocation) markConfirmedLeak() {
	if a.timestamp == nil {
		log.WithFields(a.fields()).Warnf("Confirmed IP leak")
	} else {
		log.WithFields(a.fields()).Warnf("Confirmed IP leak after %s", time.Since(*a.timestamp))
	}
	a.confirmedLeak = true
}

func (a *allocation) markValid() {
	if a.timestamp != nil {
		log.WithFields(a.fields()).Infof("Confirmed valid IP after %s", time.Since(*a.timestamp))
	}
	a.confirmedLeak = false
	a.timestamp = nil
}

func (a *allocation) isConfirmedLeak() bool {
	return a.confirmedLeak
}

func (a *allocation) isPodIP() bool {
	ns := a.attrs[ipam.AttributeNamespace]
	pod := a.attrs[ipam.AttributePod]

	return ns != "" && pod != ""
}

func (a *allocation) isTunnelAddress() bool {
	ipip := a.attrs[ipam.AttributeType] == ipam.AttributeTypeIPIP
	vxlan := a.attrs[ipam.AttributeType] == ipam.AttributeTypeVXLAN
	wg := a.attrs[ipam.AttributeType] == ipam.AttributeTypeWireguard
	return ipip || vxlan || wg
}

type ipamController struct {
	rl        workqueue.RateLimiter
	client    client.Interface
	clientset *kubernetes.Clientset
	config    config.NodeControllerConfig

	// syncChan triggers processing in response to an update.
	syncChan chan interface{}

	// For block update / deletion events.
	blockUpdate chan model.KVPair

	// Raw block storage.
	allBlocks map[string]model.KVPair

	// Store allocations broken out from the raw blocks by their handle.
	allocationsByBlock map[string]map[string]*allocation
	allocationsByNode  map[string]map[string]*allocation
}

func (c *ipamController) Start(stop chan struct{}) {
	go c.acceptScheduleRequests(stop)

	// Trigger a start-of-day sync.
	kick(c.syncChan)
}

func (c *ipamController) RegisterWith(f *DataFeed) {
	f.RegisterForNotification(model.BlockKey{}, c.onUpdate)
	f.RegisterForSyncStatus(c.onStatusUpdate)
}

func (c *ipamController) onStatusUpdate(s bapi.SyncStatus) {
	// No-op
}

func (c *ipamController) onUpdate(update bapi.Update) {
	// TODO: We need to perform a sync on node updates as well.
	switch update.UpdateType {
	case bapi.UpdateTypeKVNew:
		fallthrough
	case bapi.UpdateTypeKVUpdated:
		switch update.KVPair.Value.(type) {
		case *model.AllocationBlock:
			c.blockUpdate <- update.KVPair
		}
	case bapi.UpdateTypeKVDeleted:
		switch update.KVPair.Key.(type) {
		case model.BlockKey:
			c.blockUpdate <- update.KVPair
		}
	default:
		logrus.Errorf("Unhandled update type")
	}
}

func (c *ipamController) OnKubernetesNodeDeleted() {
	// When a Kubernetes node is deleted, trigger a sync.
	log.Info("Kubernetes node deletion event")
	kick(c.syncChan)
}

// acceptScheduleRequests is the main worker routine of the IPAM controller. It monitors
// the updates channel and triggers syncs.
func (c *ipamController) acceptScheduleRequests(stopCh <-chan struct{}) {
	// Periodic sync ticker.
	period := 5 * time.Minute
	if c.config.LeakGracePeriod != nil {
		period = c.config.LeakGracePeriod.Duration / 2
	}
	t := time.NewTicker(period)
	for {
		// Wait until something wakes us up, or we are stopped
		select {
		case kvp := <-c.blockUpdate:
			if kvp.Value != nil {
				c.onBlockUpdated(kvp)
			} else {
				c.onBlockDeleted(kvp.Key.(model.BlockKey))
			}
		case <-t.C:
			// Periodic IPAM sync.
			log.Debug("Periodic IPAM sync")
			_ = c.syncDelete()
		case <-c.syncChan:
			// Triggered IPAM sync.
			log.Debug("Received kick over sync channel")
			err := c.syncDelete()
			if err != nil {
				// We can kick ourselves on error for a retry. We have ratelimiting
				// built in to the cleanup code.
				log.WithError(err).Warn("error syncing IPAM data")
				kick(c.syncChan)
			}

			// Update prometheus metrics.
			// TODO: Do we need / want to do this every sync? Can we batch?
			c.updateMetrics()
		case <-stopCh:
			return
		}
	}
}

func (c *ipamController) syncDelete() error {
	// First, try doing an IPAM sync. This will check IPAM state and clean up any blocks
	// which don't belong based on nodes/pods in the k8s API. Don't return the error right away, since
	// even if this IPAM sync fails we shouldn't block cleaning up the node object. If we do encounter an error,
	// we'll return it after we're done.
	err := c.syncIPAMCleanup()
	if c.config.DeleteNodes {
		// If we're running in etcd mode, then we also need to delete the node resource.
		// We don't need this for KDD mode, since the Calico Node resource is backed
		// directly by the Kubernetes Node resource, so their lifecycle is identical.
		errEtcd := c.syncDeleteEtcd()
		if errEtcd != nil {
			return errEtcd
		}
	}
	return err
}

// syncIPAMCleanup cleans up any IPAM resources which should no longer exist based on nodes in the cluster.
// It returns an error if it is determined that there are resources which should be cleaned up, but is unable to do so.
// It does not return an error if it is successful, or if there is no action to take.
func (c *ipamController) syncIPAMCleanup() error {
	log.Debug("Synchronizing IPAM data")
	err := c.cleanup()
	if err != nil {
		return err
	}
	log.Debug("Node and IPAM data is in sync")
	return nil
}

func (c *ipamController) onBlockUpdated(kvp model.KVPair) {
	// Update the raw block store.
	blockCIDR := kvp.Key.(model.BlockKey).CIDR.String()
	log.WithField("block", blockCIDR).Info("Received block update")
	b := kvp.Value.(*model.AllocationBlock)
	c.allBlocks[blockCIDR] = kvp

	// Include affinity if it exists. We want to track nodes even
	// if there are no IPs actually assigned to that node.
	if b.Affinity != nil {
		n := strings.TrimPrefix(*b.Affinity, "host:")
		if _, ok := c.allocationsByNode[n]; !ok {
			c.allocationsByNode[n] = map[string]*allocation{}
		}
	}

	// Update allocations contributed from this block.
	allocatedHandles := map[string]bool{}
	for ord, idx := range b.Allocations {
		if idx == nil {
			// Not allocated.
			continue
		}
		attr := b.Attributes[*idx]

		// If there is no handle, then skip this IP. We need the handle
		// in order to release the IP below.
		if attr.AttrPrimary == nil {
			continue
		}
		handle := *attr.AttrPrimary
		allocatedHandles[handle] = true

		// Check if we already know about this allocation.
		if _, ok := c.allocationsByBlock[blockCIDR][handle]; ok {
			// Already known. TODO: Update existing allocation fields.
			continue
		}

		// This is a new allocation.
		alloc := allocation{
			ip:     ordinalToIP(b, ord).String(),
			handle: handle,
			attrs:  attr.AttrSecondary,
		}
		if _, ok := c.allocationsByBlock[blockCIDR]; !ok {
			c.allocationsByBlock[blockCIDR] = map[string]*allocation{}
		}
		c.allocationsByBlock[blockCIDR][handle] = &alloc

		// Update the allocations-by-node view.
		if node := alloc.node(); node != "" {
			if _, ok := c.allocationsByNode[node]; !ok {
				c.allocationsByNode[node] = map[string]*allocation{}
			}
			c.allocationsByNode[node][handle] = &alloc
		}
	}

	// Remove any previously assigned allocations that have since been released.
	for handle, alloc := range c.allocationsByBlock[blockCIDR] {
		if _, ok := allocatedHandles[handle]; !ok {
			// Needs release.
			delete(c.allocationsByBlock[blockCIDR], handle)

			// Also remove from the node view.
			if node := alloc.node(); node != "" {
				delete(c.allocationsByNode[node], handle)
			}
		}
	}

	// Kick the sync channel to trigger a resync.
	kick(c.syncChan)
}

func (c *ipamController) onBlockDeleted(key model.BlockKey) {
	// Update the raw block store.
	log.WithField("block", key.CIDR.String()).Info("Received block delete")
	delete(c.allBlocks, key.CIDR.String())

	// Update the node view.
	allocations := c.allocationsByBlock[key.CIDR.String()]
	for handle, alloc := range allocations {
		if node := alloc.node(); node != "" {
			delete(c.allocationsByNode[node], handle)
		}
	}

	// Delete the block.
	delete(c.allocationsByBlock, key.CIDR.String())

	// Kick the sync channel to trigger a resync.
	kick(c.syncChan)
}

func (c *ipamController) updateMetrics() {
	log.Debug("gathering latest IPAM state")

	// Keep track of various counts so that we can report them as metrics.
	blocksByNode := map[string]int{}
	borrowedIPsByNode := map[string]int{}

	// Iterate blocks to determine the correct metric values.
	for _, kvp := range c.allBlocks {
		b := kvp.Value.(*model.AllocationBlock)
		if b.Affinity != nil {
			n := strings.TrimPrefix(*b.Affinity, "host:")
			blocksByNode[n]++
		} else {
			// Count blocks with no affinity as a pseudo-node.
			blocksByNode["no_affinity"]++
		}

		// Go through each IPAM allocation, check its attributes for the node it is assigned to.
		for _, idx := range b.Allocations {
			if idx == nil {
				// Not allocated.
				continue
			}
			attr := b.Attributes[*idx]

			// Track nodes based on IP allocations.
			if node, ok := attr.AttrSecondary[ipam.AttributeNode]; ok {
				// Update metrics maps with this allocation.
				if b.Affinity == nil || node != strings.TrimPrefix(*b.Affinity, "host:") {
					// If the allocations node doesn't match the block's, then this is borrowed.
					borrowedIPsByNode[node]++
				}
			}
		}
	}

	// Update prometheus metrics.
	ipsGauge.Reset()
	for node, allocations := range c.allocationsByNode {
		if num := len(allocations); num != 0 {
			ipsGauge.WithLabelValues(node).Set(float64(num))
		}
	}
	blocksGauge.Reset()
	for node, num := range blocksByNode {
		blocksGauge.WithLabelValues(node).Set(float64(num))
	}
	borrowedGauge.Reset()
	for node, num := range borrowedIPsByNode {
		borrowedGauge.WithLabelValues(node).Set(float64(num))
	}
}

// checkDeletedNodes scanes the nodes in Calico IPAM and determines if any should have their
// block affinities released. A node's affinities should be released when:
// - The node no longer exists in the Kubernetes API.
// - There are no longer any IP allocations on the node, OR
// - The remaining IP allocations on the node are all determined to be leaked IP addresses.
// TODO: We're effectively iterating every allocation in the cluster on every execution. Can we optimize? Or at least rate-limit?
func (c *ipamController) checkDeletedNodes() ([]string, error) {
	// For each node present in IPAM, if it doesn't exist in the Kubernetes API then we
	// should consider it a candidate for cleanup.
	nodesToDelete := []string{}
	for cnode, allocations := range c.allocationsByNode {
		// Lookup the corresponding Kubernetes node for each Calico node we found in IPAM.
		// In KDD mode, these are identical. However, in etcd mode its possible that the Calico node has a
		// different name from the Kubernetes node.
		// In KDD mode, if the Node has been deleted from the Kubernetes API, this may be an empty string.
		knode, err := c.kubernetesNodeForCalico(cnode)
		if err != nil {
			// Error checking for matching k8s node. Skip for now.
			log.WithError(err).Warnf("Failed to lookup corresponding node, skipping %s", cnode)
			continue
		}
		logc := log.WithFields(log.Fields{"calicoNode": cnode, "k8sNode": knode})

		// If we found a corresponding k8s node name, check to make sure it is gone. If we
		// found no corresponding node, then we're good to clean up any allocations.
		// We'll check each allocation to make sure it comes from Kubernetes (or is a tunnel address)
		// before cleaning it up below.
		kubernetesNodeExists := false
		if knode != "" && c.nodeExists(knode) {
			logc.Debug("Node still exists")
			kubernetesNodeExists = true
		}
		logc.Debug("Checking node")

		// To increase our confidence, go through each IP address and
		// check to see if the pod it references exists. If all the pods on that node are gone,
		// we can delete it. If any pod still exists, we skip this node. We want to be
		// extra sure that the node is gone before we clean it up.
		canDelete := true
		for _, a := range allocations {
			// Skip any allocations which are not either a Kubernetes pod, or a node's
			// IPIP, VXLAN or Wireguard address. In practice, we don't expect these, but they might exist.
			// When they do, they will need to be released outside of this controller in order for
			// the block to be cleaned up.
			if !a.isPodIP() && !a.isTunnelAddress() {
				logc.Info("IP allocation on node is from an unknown source. Will be unable to cleanup block until it is removed.")
				canDelete = false
				continue
			}

			if a.isTunnelAddress() && kubernetesNodeExists {
				// We onlny GC tunnel addresses if the node has been deleted.
				continue
			}

			// Check if the allocation is still valid.
			if knode != "" && c.checkAllocation(a, knode) {
				// Allocation is still valid.
				canDelete = false
				a.markValid()
				continue
			} else if !kubernetesNodeExists {
				// The allocation is NOT valid, we can skip the candidacy stage.
				// We know this with confidence because:
				// - The node the allocation belongs to no longer exists.
				// - There pod owning this allocation no longer exists.
				a.markConfirmedLeak()
			} else if c.config.LeakGracePeriod != nil {
				// The Kubernetes node still exists, so our confidence is lower.
				// Mark as a candidate leak. If this state remains, it will switch
				// to confirmed after the grace period.
				a.markLeak(c.config.LeakGracePeriod.Duration)
			}
		}

		if !kubernetesNodeExists {
			if !canDelete {
				// There are still valid allocations on the node.
				logc.Infof("Can't cleanup node yet - IPs still in use on this node")
				continue
			}

			// The node is ready to be removed. It exists in Calico IPAM, but
			// not in the Kubernetes API. Additionally, we've checked that there are no
			// outstanding valid allocations on the node.
			nodesToDelete = append(nodesToDelete, cnode)
		}
	}
	return nodesToDelete, nil
}

// checkAllocation returns true if the allocation is still in use, and false if the allocation
// apears to be leaked.
func (c *ipamController) checkAllocation(a *allocation, knode string) bool {
	// If we don't have a Kuberetes node name for this allocation
	ns := a.attrs[ipam.AttributeNamespace]
	pod := a.attrs[ipam.AttributePod]
	logc := log.WithFields(a.fields())

	// Query the pod referenced by this allocation.
	p, err := c.clientset.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			log.WithError(err).Warn("Failed to query pod, assume it exists and alloaction is valid")
			return true
		}
		// Pod not found. Assume this is a leak.
		return false
	}

	// The pod exists - check if it is still on the original node.
	// TODO: Do we need this check?
	if p.Spec.NodeName != "" && knode != "" && p.Spec.NodeName != knode {
		// If the pod has been rescheduled to a new node, we can treat the old allocation as
		// gone and clean it up.
		fields := log.Fields{"old": knode, "new": p.Spec.NodeName}
		logc.WithFields(fields).Info("Pod rescheduled on new node. Allocation no longer valid")
		return false
	}

	// The pod exists, but does it have the correct IP address?
	// TODO: Need to figure out how this works with multus IPs.
	if p.Status.PodIP != "" && p.Status.PodIP == a.ip {
		logc.Debugf("Pod IP matches, allocation is valid")
		return true
	} else if p.Status.PodIP == "" {
		logc.Debugf("Pod IP has not yet been reported, consider allocation valid")
		return true
	}
	logc.Debugf("Allocated IP no longer in-use by pod")
	return false
}

func (c *ipamController) cleanup() error {
	// Check if any nodes in IPAM need to have affinties released.
	nodesToDelete, err := c.checkDeletedNodes()
	if err != nil {
		return err
	}

	// Release all confirmed leaks.
	err = c.garbageCollectIPs()
	if err != nil {
		return err
	}

	// Delete any nodes that we determined can be removed above.
	var storedErr error
	for _, cnode := range nodesToDelete {
		logc := log.WithField("node", cnode)

		// Potentially ratelimit node cleanup.
		time.Sleep(c.rl.When(RateLimitCalicoDelete))
		logc.Info("Cleaning up IPAM resources for deleted node")
		if err := c.cleanupNode(cnode, nil); err != nil {
			// Store the error, but continue. Storing the error ensures we'll retry.
			logc.WithError(err).Warnf("Error cleaning up node")
			storedErr = err
			continue
		}
		c.rl.Forget(RateLimitCalicoDelete)
	}
	if storedErr != nil {
		return storedErr
	}
	return nil
}

// garbageCollectIPs checks all known allocations and GCs any confirmed leaks.
func (c *ipamController) garbageCollectIPs() error {
	for _, allocations := range c.allocationsByNode {
		for _, a := range allocations {
			if a.isConfirmedLeak() {
				logc := log.WithFields(a.fields())
				logc.Info("Garbage collecting leaked IP address")
				if err := c.client.IPAM().ReleaseByHandle(context.TODO(), a.handle); err != nil {
					if _, ok := err.(cerrors.ErrorResourceDoesNotExist); ok {
						logc.WithField("handle", a.handle).Debug("IP already released")
						continue
					}
					logc.WithError(err).WithField("handle", a.handle).Warning("Failed to release leaked IP")
					return err
				}
			}
		}
	}
	return nil
}

func (c *ipamController) cleanupNode(cnode string, allocations map[string]*allocation) error {
	// At this point, we've verified that the node isn't in Kubernetes and that all the allocations
	// are tied to pods which don't exist any more. Clean up any allocations which may still be laying around.
	logc := log.WithField("calicoNode", cnode)
	retry := false
	for _, a := range allocations {
		logc.WithFields(a.fields()).Info("Releasing allocation")
		if err := c.client.IPAM().ReleaseByHandle(context.TODO(), a.handle); err != nil {
			if _, ok := err.(cerrors.ErrorResourceDoesNotExist); ok {
				// If it doesn't exist, we're OK, since we don't want it to!
				// Try to release any other allocations, but we'll still return an error
				// to retry the whole thing from the top. On the retry,
				// we should no longer see any allocations.
				logc.WithField("handle", a.handle).Debug("IP already released")
				retry = true
				continue
			}
			logc.WithError(err).WithField("handle", a.handle).Warning("Failed to release IP")
			retry = true
			break
		}
	}

	if retry {
		logc.Info("Couldn't release all IPs for stale node, schedule retry")
		return fmt.Errorf("Couldn't release all IPs")
	}

	// Release the affinities for this node, requiring that the blocks are empty.
	if err := c.client.IPAM().ReleaseHostAffinities(context.TODO(), cnode, true); err != nil {
		logc.WithError(err).Errorf("Failed to release block affinities for node")
		return err
	}
	logc.Debug("Released all affinities for node")

	return nil
}

// nodeExists returns true if the given node still exists in the Kubernetes API.
func (c *ipamController) nodeExists(knode string) bool {
	_, err := c.clientset.CoreV1().Nodes().Get(context.Background(), knode, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false
		}
		log.WithError(err).Warn("Failed to query node, assume it exists")
	}
	return true
}

// kubernetesNodeForCalico returns the name of the Kubernetes node that corresponds to this Calico node.
// This function returns an empty string if no action should be taken for this node.
func (c *ipamController) kubernetesNodeForCalico(cnode string) (string, error) {
	// TODO
	// for kn, cn := range c.nodemapper {
	// 	if cn == cnode {
	// 		return kn, nil
	// 	}
	// }

	// If we can't find a matching Kubernetes node, try looking up the Calico node explicitly,
	// since it's theoretically possible the nodemapper is just running behind the actual state of the
	// data store.
	calicoNode, err := c.client.Nodes().Get(context.TODO(), cnode, options.GetOptions{})
	if err != nil {
		if _, ok := err.(cerrors.ErrorResourceDoesNotExist); ok {
			logrus.WithError(err).Info("Calico Node referenced in IPAM data does not exist")
			return "", nil
		}
		logrus.WithError(err).Warn("failed to query Calico Node referenced in IPAM data")
		return "", err
	}

	// Try to pull the k8s name from the retrieved Calico node object. If there is no match,
	// this will return an empty string, correctly telling the calling code to ignore this allocation.
	return getK8sNodeName(*calicoNode), nil
}

func ordinalToIP(b *model.AllocationBlock, ord int) net.IP {
	return b.OrdinalToIP(ord).IP
}

func (c *ipamController) syncDeleteEtcd() error {
	// Possibly rate limit calls to Calico
	time.Sleep(c.rl.When(RateLimitCalicoList))
	cNodes, err := c.client.Nodes().List(context.TODO(), options.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Error listing Calico nodes")
		return err
	}
	c.rl.Forget(RateLimitCalicoList)

	time.Sleep(c.rl.When(RateLimitK8s))
	kNodes, err := c.clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Error listing K8s nodes")
		return err
	}
	c.rl.Forget(RateLimitK8s)
	kNodeIdx := make(map[string]bool)
	for _, node := range kNodes.Items {
		kNodeIdx[node.Name] = true
	}

	for _, node := range cNodes.Items {
		k8sNodeName := getK8sNodeName(node)
		if k8sNodeName != "" && !kNodeIdx[k8sNodeName] {
			// No matching Kubernetes node with that name
			time.Sleep(c.rl.When(RateLimitCalicoDelete))
			_, err := c.client.Nodes().Delete(context.TODO(), node.Name, options.DeleteOptions{})
			if _, doesNotExist := err.(cerrors.ErrorResourceDoesNotExist); err != nil && !doesNotExist {
				// We hit an error other than "does not exist".
				log.WithError(err).Errorf("Error deleting Calico node: %v", node.Name)
				return err
			}
			c.rl.Forget(RateLimitCalicoDelete)
		}
	}
	return nil
}
