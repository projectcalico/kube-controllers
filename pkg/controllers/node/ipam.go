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

	"github.com/projectcalico/libcalico-go/lib/backend/model"
	cerrors "github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	cnet "github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// syncIPAMCleanup cleans up any IPAM resources which should no longer exist based on nodes in the cluster.
// It returns an error if it is determined that there are resources which should be cleaned up, but is unable to do so.
// It does not return an error if it is successful, or if there is no action to take.
func (c *NodeController) syncIPAMCleanup() error {
	log.Info("Synchronizing IPAM data")
	data, err := c.gatherIPAMData()
	if err != nil {
		return err
	}
	err = c.cleanupNew(data)
	if err != nil {
		return err
	}
	log.Info("Node and IPAM data is in sync")
	return nil
}

func (c *NodeController) gatherIPAMData() (map[string][]model.AllocationAttribute, error) {
	log.Debug("gathering latest IPAM state")

	// Query all IPAM blocks in the cluster, ratelimiting calls.
	time.Sleep(c.rl.When(RateLimitCalicoList))
	blocks, err := c.listBlocks()
	if err != nil {
		return nil, err
	}
	c.rl.Forget(RateLimitCalicoList)

	// Keep track of various counts so that we can report them as metrics.
	allocationsByNode := map[string]int{}
	blocksByNode := map[string]int{}
	borrowedIPsByNode := map[string]int{}

	// Build a list of all the nodes in the cluster based on IPAM allocations across all
	// blocks, plus affinities. Entries are Calico node names.
	calicoNodes := map[string][]model.AllocationAttribute{}
	for _, kvp := range blocks {
		b := kvp.Value.(*model.AllocationBlock)

		// Include affinity if it exists. We want to track nodes even
		// if there are no IPs actually assigned to that node.
		if b.Affinity != nil {
			n := strings.TrimPrefix(*b.Affinity, "host:")
			if _, ok := calicoNodes[n]; !ok {
				calicoNodes[n] = []model.AllocationAttribute{}
			}

			// Increment number of blocks on this node.
			blocksByNode[n]++
		} else {
			// Count blocks with no affinity as a pseudo-node.
			blocksByNode["no_affinity"]++
		}

		// To reduce log spam.
		firstSkip := true

		// Go through each IPAM allocation, check its attributes for the node it is assigned to.
		for ord, idx := range b.Allocations {
			if idx == nil {
				// Not allocated.
				continue
			}
			attr := b.Attributes[*idx]

			// Track nodes based on IP allocations.
			if val, ok := attr.AttrSecondary[ipam.AttributeNode]; ok {
				if _, ok := calicoNodes[val]; !ok {
					calicoNodes[val] = []model.AllocationAttribute{}
				}

				// Update metrics maps with this allocation.
				allocationsByNode[val]++
				if b.Affinity == nil || val != strings.TrimPrefix(*b.Affinity, "host:") {
					// If the allocations node doesn't match the block's, then this is borrowed.
					borrowedIPsByNode[val]++
				}

				// Extract the IP and hack it into the attributes - this is a hack
				// by this controller to track the actual IP respresented.
				// TODO
				ip := ordinalToIP(b, ord)
				attr.AttrSecondary["controller.projectcalico.org/ip"] = ip.String()

				// If there is no handle, then skip this IP. We need the handle
				// in order to release the IP below.
				if attr.AttrPrimary == nil {
					logc := log.WithFields(log.Fields{"ip": ip, "block": b.CIDR.String()})
					if firstSkip {
						logc.Warnf("Skipping IP with no handle")
						firstSkip = false
					} else {
						logc.Debugf("Skipping IP with no handle")
					}
					continue
				}

				// Add this allocation to the node, so we can release it later if
				// we need to.
				calicoNodes[val] = append(calicoNodes[val], attr)
			}
		}
	}
	log.Debugf("Calico nodes found in IPAM: %v", calicoNodes)

	// Update prometheus metrics.
	ipsGauge.Reset()
	for node, num := range allocationsByNode {
		ipsGauge.WithLabelValues(node).Set(float64(num))
	}
	blocksGauge.Reset()
	for node, num := range blocksByNode {
		blocksGauge.WithLabelValues(node).Set(float64(num))
	}
	borrowedGauge.Reset()
	for node, num := range borrowedIPsByNode {
		borrowedGauge.WithLabelValues(node).Set(float64(num))
	}
	return calicoNodes, nil
}

type candidate struct {
	ip     string
	pod    string
	ns     string
	podIP  string
	handle string
	node   string
}

func (c *candidate) fields() log.Fields {
	return log.Fields{
		"ip":     c.ip,
		"podIP":  c.podIP,
		"pod":    c.pod,
		"ns":     c.ns,
		"handle": c.handle,
		"node":   c.node,
	}

}

func (c *NodeController) cleanupNew(calicoNodes map[string][]model.AllocationAttribute) error {
	allHandles := []string{}
	leakCandidates := map[string]candidate{}
	for cnode, allocations := range calicoNodes {
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

		// Go through every allocation and build a map of candidate leaked addresses.
		for _, a := range allocations {
			ns := a.AttrSecondary[ipam.AttributeNamespace]
			pod := a.AttrSecondary[ipam.AttributePod]
			ipip := a.AttrSecondary[ipam.AttributeType] == ipam.AttributeTypeIPIP
			vxlan := a.AttrSecondary[ipam.AttributeType] == ipam.AttributeTypeVXLAN
			wg := a.AttrSecondary[ipam.AttributeType] == ipam.AttributeTypeWireguard
			ip := a.AttrSecondary["controller.projectcalico.org/ip"]

			// Can't release IPs without a handle.
			// TODO: Release by value?
			if a.AttrPrimary == nil {
				logc.WithFields(log.Fields{"pod": pod, "ns": ns}).Warnf("IP has no handle")
				continue
			}
			handle := *a.AttrPrimary

			// We'll use this to clean up later.
			allHandles = append(allHandles, handle)

			// Skip any allocations which are not either a Kubernetes pod, or a node's
			// IPIP, VXLAN or Wireguard address. In practice, we don't expect these, but they might exist.
			// When they do, they will need to be released outside of this controller.
			if (ns == "" || pod == "") && !ipip && !vxlan && !wg {
				logc.Info("IP allocation on node is from an unknown source. Will be unable to cleanup block until it is removed.")
				continue
			}

			// For now, skip node IPIP / VXLAN / WG addresses. We need to add code to check these.
			// TODO
			if ipip || vxlan || wg {
				continue
			}

			// If we don't know the node or pod, we can't proceed.
			if knode == "" || pod == "" {
				logc.WithFields(log.Fields{"knod": knode, "pod": pod}).Warnf("Missing node/pod info")
				continue
			}

			// Check to see if the pod still exists. If it does, then we shouldn't clean up
			// this node, since it might come back online.
			var podIP string
			if c.podExistsOnNode(pod, ns, knode) {
				logc.WithFields(log.Fields{"pod": pod, "ns": ns}).Debugf("Pod still exists")

				// The pod exists, but does it have the correct IP address?
				// TODO: Need to figure out how this works with multus IPs.
				p, err := c.k8sClientset.CoreV1().Pods(ns).Get(context.Background(), pod, v1.GetOptions{})
				if err != nil {
					log.WithError(err).Warn("Failed to query pod, assume it has correct IP")
					continue
				}

				if p.Status.PodIP != "" && p.Status.PodIP == ip {
					logc.WithFields(log.Fields{"pod": pod, "ns": ns}).Debugf("Pod IP is still valid")
					continue
				} else if p.Status.PodIP == "" {
					logc.WithFields(log.Fields{"pod": pod, "ns": ns}).Debugf("Pod IP has not yet been reported")
					continue
				}

				podIP = p.Status.PodIP
				logc.WithFields(log.Fields{"pod": pod, "ns": ns, "podIP": podIP, "ip": ip}).Debugf("Allocated IP no longer in-use by pod")
			}

			leakCandidates[handle] = candidate{
				ip:     ip,
				podIP:  podIP,
				ns:     ns,
				pod:    pod,
				handle: handle,
				node:   knode,
			}
		}
	}

	// Remove any handles which no longer exist.
	for h, _ := range c.leakCandidates {
		if !containsString(allHandles, h) {
			delete(c.leakCandidates, h)
			log.WithFields(log.Fields{"handle": h}).Infof("IP address is no longer a leak candidate")
		}
	}
	for h, _ := range c.confirmedLeaks {
		if !containsString(allHandles, h) {
			delete(c.confirmedLeaks, h)
			log.WithFields(log.Fields{"handle": h}).Infof("IP address is no longer a confirmed leak")
		}
	}

	// Use the same timestamp so it's easier to tell which IPs were done in which batch.
	now := time.Now()

	// Go through the candidates and update state.
	for handle, candy := range leakCandidates {
		if ts, ok := c.leakCandidates[handle]; ok {
			// We have already seen this leak candidate.
			if _, ok := c.confirmedLeaks[handle]; !ok {
				// Not already a confirmed leak.
				if time.Since(ts) > 2*time.Minute {
					// It's time is up - this is a confirmed leak.
					log.WithFields(candy.fields()).Warnf("IP is a confirmed leak")
					c.confirmedLeaks[handle] = now
					delete(c.leakCandidates, handle)
				}
			}
		} else {
			// Either a known leak or a new candidate.
			if _, ok := c.confirmedLeaks[handle]; !ok {
				// New candidate.
				log.WithFields(candy.fields()).Warnf("IP is a new leak candidate")
				c.leakCandidates[handle] = now
			}
		}
	}

	log.WithFields(log.Fields{"leakCandidates": len(c.leakCandidates), "confirmedLeaks": len(c.confirmedLeaks)}).Info("Analyzed IP leaks")

	for h, _ := range c.confirmedLeaks {
		// Release each confirmed leak.
		// TODO: Release by handle is race-prone!
		if err := c.calicoClient.IPAM().ReleaseByHandle(c.ctx, h); err != nil {
			if _, ok := err.(cerrors.ErrorResourceDoesNotExist); ok {
				// If it doesn't exist, we're OK, since we don't want it to!
				log.WithField("handle", h).Debug("IP was already released")
				continue
			}
			log.WithError(err).WithField("handle", h).Warning("Failed to release IP")
			continue
		}
		// Success.
		delete(c.confirmedLeaks, h)
	}
	return nil
}

func containsString(l []string, s string) bool {
	for _, ss := range l {
		if ss == s {
			return true
		}
	}
	return false
}

func normaliseIP(addr string) (string, error) {
	ip, _, err := cnet.ParseCIDROrIP(addr)
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

func (c *NodeController) cleanup(calicoNodes map[string][]model.AllocationAttribute) error {
	// For storing any errors encountered below.
	var storedErr error

	// For each node present in IPAM, if it doesn't exist in the Kubernetes API then we
	// should consider it a candidate for cleanup.
	for cnode, allocations := range calicoNodes {
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
			ns := a.AttrSecondary[ipam.AttributeNamespace]
			pod := a.AttrSecondary[ipam.AttributePod]
			ipip := a.AttrSecondary[ipam.AttributeType] == ipam.AttributeTypeIPIP
			vxlan := a.AttrSecondary[ipam.AttributeType] == ipam.AttributeTypeVXLAN
			wg := a.AttrSecondary[ipam.AttributeType] == ipam.AttributeTypeWireguard

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

func (c *NodeController) cleanupNode(cnode string, allocations []model.AllocationAttribute) error {
	// At this point, we've verified that the node isn't in Kubernetes and that all the allocations
	// are tied to pods which don't exist any more. Clean up any allocations which may still be laying around.
	logc := log.WithField("calicoNode", cnode)
	retry := false
	for _, a := range allocations {
		if err := c.calicoClient.IPAM().ReleaseByHandle(c.ctx, *a.AttrPrimary); err != nil {
			if _, ok := err.(cerrors.ErrorResourceDoesNotExist); ok {
				// If it doesn't exist, we're OK, since we don't want it to!
				// Try to release any other allocations, but we'll still return an error
				// to retry the whole thing from the top. On the retry,
				// we should no longer see any allocations.
				logc.WithField("handle", *a.AttrPrimary).Debug("IP already released")
				retry = true
				continue
			}
			logc.WithError(err).WithField("handle", *a.AttrPrimary).Warning("Failed to release IP")
			retry = true
			break
		}
	}

	if retry {
		logc.Info("Couldn't release all IPs for stale node, schedule retry")
		return fmt.Errorf("Couldn't release all IPs")
	}

	// Release the affinities for this node, requiring that the blocks are empty.
	if err := c.calicoClient.IPAM().ReleaseHostAffinities(c.ctx, cnode, true); err != nil {
		logc.WithError(err).Errorf("Failed to release block affinities for node")
		return err
	}
	logc.Debug("Released all affinities for node")

	return nil
}

// nodeExists returns true if the given node still exists in the Kubernetes API.
func (c *NodeController) nodeExists(knode string) bool {
	_, err := c.k8sClientset.CoreV1().Nodes().Get(context.Background(), knode, v1.GetOptions{})
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
func (c *NodeController) podExistsOnNode(name, ns, node string) bool {
	p, err := c.k8sClientset.CoreV1().Pods(ns).Get(context.Background(), name, v1.GetOptions{})
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
func (c *NodeController) kubernetesNodeForCalico(cnode string) (string, error) {
	c.Lock()
	defer c.Unlock()

	for kn, cn := range c.nodemapper {
		if cn == cnode {
			return kn, nil
		}
	}

	// If we can't find a matching Kubernetes node, try looking up the Calico node explicitly,
	// since it's theoretically possible the nodemapper is just running behind the actual state of the
	// data store.
	calicoNode, err := c.calicoClient.Nodes().Get(context.TODO(), cnode, options.GetOptions{})
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
