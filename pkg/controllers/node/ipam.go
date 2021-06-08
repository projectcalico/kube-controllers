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
}

func (a *allocation) fields() log.Fields {
	return log.Fields{
		"ip":     a.ip,
		"handle": a.handle,
	}
}

func (a *allocation) node() string {
	if node, ok := a.attrs[ipam.AttributeNode]; ok {
		return node
	}
	return ""
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
	for {
		// Wait until something wakes us up, or we are stopped
		select {
		case kvp := <-c.blockUpdate:
			if kvp.Value != nil {
				// Update block map.
				c.onBlockUpdated(kvp)
			} else {
				// Remove block from storage.
				c.onBlockDeleted(kvp.Key.(model.BlockKey))
			}
		case <-c.syncChan:
			// Perform an IPAM sync.
			log.Info("Received kick over sync channel")
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
	log.Info("Synchronizing IPAM data")
	err := c.cleanup()
	if err != nil {
		return err
	}
	log.Info("Node and IPAM data is in sync")
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

	log.Infof("Updated allocationsByNode: %+v", c.allocationsByNode)
	log.Infof("Updated allocationsByBlok: %+v", c.allocationsByBlock)

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

	log.Infof("Updated allocationsByNode after del : %+v", c.allocationsByNode)
	log.Infof("Updated allocationsByBlok after del : %+v", c.allocationsByBlock)

	// Kick the sync channel to trigger a resync.
	kick(c.syncChan)
}

func (c *ipamController) updateMetrics() error {
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
	return nil
}

func (c *ipamController) cleanup() error {
	// For storing any errors encountered below.
	var storedErr error

	// For each node present in IPAM, if it doesn't exist in the Kubernetes API then we
	// should consider it a candidate for cleanup.
	for cnode, allocations := range c.allocationsByNode {
		// Lookup the corresponding Kubernetes node for each Calico node we found in IPAM.
		// In KDD mode, these are identical. However, in etcd mode its possible that the Calico node has a
		// different name from the Kubernetes node.
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
		if knode != "" {
			// Check if it exists in the Kubernetes API.
			if c.nodeExists(knode) {
				logc.Debug("Node still exists, continue")
				continue
			}
		}
		logc.Info("Checking node")

		// Node exists in IPAM but not in the Kubernetes API. Go through each IP address and
		// check to see if the pod it references exists. If all the pods on that node are gone,
		// continue with deletion. If any pod still exists, we skip this node. We want to be
		// extra sure that the node is gone before we clean it up.
		canDelete := true
		for _, a := range allocations {
			ns := a.attrs[ipam.AttributeNamespace]
			pod := a.attrs[ipam.AttributePod]
			ipip := a.attrs[ipam.AttributeType] == ipam.AttributeTypeIPIP
			vxlan := a.attrs[ipam.AttributeType] == ipam.AttributeTypeVXLAN
			wg := a.attrs[ipam.AttributeType] == ipam.AttributeTypeWireguard

			// Skip any allocations which are not either a Kubernetes pod, or a node's
			// IPIP, VXLAN or Wireguard address. In practice, we don't expect these, but they might exist.
			// When they do, they will need to be released outside of this controller in order for
			// the block to be cleaned up.
			if (ns == "" || pod == "") && !ipip && !vxlan && !wg {
				logc.Info("IP allocation on node is from an unknown source. Will be unable to cleanup block until it is removed.")
				canDelete = false
				continue
			}

			// Check to see if the pod still exists. If it does, then we shouldn't clean up
			// this node, since it might come back online.
			if knode != "" && pod != "" && c.podExistsOnNode(pod, ns, knode) {
				logc.WithFields(log.Fields{"pod": pod, "ns": ns}).Debugf("Pod still exists")
				canDelete = false
				break
			}
		}

		if !canDelete {
			// Return an error here, it will trigger a reschedule of this call.
			logc.Infof("Can't cleanup node yet - IPs still in use")
			return fmt.Errorf("Cannot clean up node yet, IPs still in use")
		}

		// Potentially ratelimit node cleanup.
		time.Sleep(c.rl.When(RateLimitCalicoDelete))
		logc.Info("Cleaning up IPAM resources for deleted node")
		if err := c.cleanupNode(cnode, allocations); err != nil {
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

func (c *ipamController) cleanupNode(cnode string, allocations map[string]*allocation) error {
	// At this point, we've verified that the node isn't in Kubernetes and that all the allocations
	// are tied to pods which don't exist any more. Clean up any allocations which may still be laying around.
	logc := log.WithField("calicoNode", cnode)
	retry := false
	for _, a := range allocations {
		logc.Info("Releasing allocation %+v", a)
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

// podExistsOnNode returns whether the given pod exists in the Kubernetes API and is on the provided Kubernetes node.
// Note that the "node" parameter is the name of the Kubernetes node in the Kubernetes API.
func (c *ipamController) podExistsOnNode(name, ns, node string) bool {
	p, err := c.clientset.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false
		}
		log.WithError(err).Warn("Failed to query pod, assume it exists")
	}
	if p.Spec.NodeName != node {
		// If the pod has been rescheduled to a new node, we can treat the old allocation as
		// gone and clean it up.
		fields := log.Fields{"old": node, "new": p.Spec.NodeName, "pod": name, "ns": ns}
		log.WithFields(fields).Info("Pod rescheduled on new node. Will clean up old allocation")
		return false
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
