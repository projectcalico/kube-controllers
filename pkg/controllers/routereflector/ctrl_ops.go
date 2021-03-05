// Copyright (c) 2020 IBM Corporation All rights reserved.
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

package routereflector

import (
	"context"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	cerrors "github.com/projectcalico/libcalico-go/lib/errors"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// delete is triggered when a node has deleted from the cluster
func (c *ctrl) delete(kubeNode *corev1.Node) error {
	// Only deal with non activ totally removed nodes
	if kubeNode.GetDeletionTimestamp() == nil || c.isNodeCompatible(kubeNode) {
		return nil
	}

	if c.topology.IsRouteReflector(string(kubeNode.GetUID()), kubeNode.GetLabels()) {
		// Node is deleted right now or has some issues, better to remove form RRs
		if err := c.removeRRStatus(kubeNode); err != nil {
			log.Errorf("Unable to cleanup label on %s: %s", kubeNode.GetName(), err.Error())
			return err
		}
		log.Infof("Label was removed from node %s", kubeNode.GetName())
	}

	log.Infof("Time to re-reconcile for node %s", kubeNode.GetName())
	return c.update(kubeNode)
}

// update is triggered when a node has beed updated on the cluster
func (c *ctrl) update(kubeNode *corev1.Node) error {
	affectedNodes := c.collectNodeInfo(c.topology.GetNodeFilter(kubeNode))
	log.Debug("Affected nodes: %#v", affectedNodes)

	if err := c.updateNodeLabels(affectedNodes); err != nil {
		return err
	} else if err := c.updateBGPTopology(affectedNodes); err != nil {
		return err
	}

	return nil
}

// revertFailedModifications detects prevoius execution failure and tries to revert the applied change
func (c *ctrl) revertFailedModifications() error {
	// Iterating on all known nodes
	for _, kubeNode := range c.kubeNodes {
		status, ok := c.routeReflectorsUnderOperation[kubeNode.GetUID()]
		if !ok {
			continue
		}
		// Node was under operation, better to revert it
		var err error
		if status {
			// Previous operation was add
			err = c.datastore.RemoveRRStatus(kubeNode, c.findCalicoNode(kubeNode))
		} else {
			// Previous operaion was remove
			err = c.datastore.AddRRStatus(kubeNode, c.findCalicoNode(kubeNode))
		}
		if err != nil {
			log.Errorf("Failed to revert node %s: %s", kubeNode.GetName(), err.Error())
			return err
		}

		// Update Kubernetes node
		if _, err := c.k8sNodeClient.Update(context.Background(), kubeNode, metav1.UpdateOptions{}); err != nil {
			log.Errorf("Failed to revert update node %s: %s", kubeNode.GetName(), err.Error())
			return err
		}

		delete(c.routeReflectorsUnderOperation, kubeNode.GetUID())
	}

	return nil
}

// updateNodeLabels calculates topology and updates all the necessary labels in datastore
func (c *ctrl) updateNodeLabels(affectedNodes map[*corev1.Node]bool) error {
	// on case of a zone has less nodes then required, count missing
	missingRouteReflectors := 0

	// Calculate topology
	for _, status := range c.topology.GetRouteReflectorStatuses(affectedNodes) {
		// Increment the expected with the number of missing
		status.ExpectedRRs += missingRouteReflectors
		log.Infof("Route refletor status in zone(s) %v (actual/expected) = %d/%d", status.Zones, status.ActualRRs, status.ExpectedRRs)

		for _, n := range status.Nodes {
			n := c.kubeNodes[n.GetUID()]

			if !affectedNodes[n] {
				continue
			} else if status.ExpectedRRs == status.ActualRRs {
				break
			}

			// Calculate diff to detect scale operation
			diff := status.ExpectedRRs - status.ActualRRs
			if diff == 0 {
				continue
			}

			// Update labels on node
			if updated, err := c.updateRRStatus(n, diff); err != nil {
				log.Errorf("Unable to update node %s: %s", n.GetName(), err.Error())
				return err
			} else if updated && diff > 0 {
				status.ActualRRs++
			} else if updated && diff < 0 {
				status.ActualRRs--
			}

		}

		// Recalculate missing nodes
		if status.ExpectedRRs != status.ActualRRs {
			log.Infof("Actual number %d is different than expected %d", status.ActualRRs, status.ExpectedRRs)
			missingRouteReflectors = status.ExpectedRRs - status.ActualRRs
		}
	}

	// There are still missing route reflectors
	if missingRouteReflectors != 0 {
		log.Errorf("Actual number is different than expected, missing: %d", missingRouteReflectors)
	}

	return nil
}

// updateBGPTopology creates/updates BGP peer configurations based on topology
func (c *ctrl) updateBGPTopology(affectedNodes map[*corev1.Node]bool) error {
	bgpPeers := []*apiv3.BGPPeer{}
	for _, p := range c.bgpPeers {
		bgpPeers = append(bgpPeers, p)
	}

	// Generate BGP peers
	toRefresh, toRemove := c.topology.GenerateBGPPeers(affectedNodes, bgpPeers)

	log.Infof("Number of BGPPeers to refresh: %v", len(toRefresh))
	log.Debugf("To refresh BGPeers are: %v", toRefresh)
	log.Infof("Number of BGPPeers to delete: %v", len(toRemove))
	log.Debugf("To delete BGPeers are: %v", toRemove)

	// Persist new/updated peer configs
	for _, bp := range toRefresh {
		log.Infof("Saving %s BGPPeer", bp.Name)
		if err := c.bgpPeer.save(bp); err != nil {
			log.Errorf("Unable to save BGPPeer: %s", err.Error())
			return err
		}
	}

	// Delete unnecessary peer configs
	for _, p := range toRemove {
		log.Debugf("Removing BGPPeer: %s", p.GetName())
		if err := c.bgpPeer.remove(p); err != nil {
			if _, ok := err.(cerrors.ErrorResourceDoesNotExist); !ok {
				log.Errorf("Unable to remove BGPPeer: %s", err.Error())
				return err
			}
			log.Debugf("Unable to remove BGPPeer: %s", err.Error())
		}
	}

	return nil
}

// removeRRStatus removes RR labels and persists the new state
func (c *ctrl) removeRRStatus(kubeNode *corev1.Node) error {
	c.routeReflectorsUnderOperation[kubeNode.GetUID()] = false

	if err := c.datastore.RemoveRRStatus(kubeNode, c.findCalicoNode(kubeNode)); err != nil {
		log.Errorf("Unable to cleanup RR status %s: %s", kubeNode.GetName(), err.Error())
		return err
	}

	log.Infof("Removing route reflector label from %s", kubeNode.GetName())
	// Update Kubernetes node
	if _, err := c.k8sNodeClient.Update(context.Background(), kubeNode, metav1.UpdateOptions{}); err != nil {
		log.Errorf("Unable to cleanup node %s: %s", kubeNode.GetName(), err.Error())
		return err
	}

	delete(c.routeReflectorsUnderOperation, kubeNode.GetUID())

	return nil
}

// updateRRStatus applies new RR state on node and persists the state
func (c *ctrl) updateRRStatus(kubeNode *corev1.Node, diff int) (bool, error) {
	labeled := c.topology.IsRouteReflector(string(kubeNode.GetUID()), kubeNode.GetLabels())
	// On case of increasing the number of RR and current is RR || decreasing and current is not RR need to skip
	if labeled && diff > 0 || !labeled && diff < 0 {
		return false, nil
	}

	c.routeReflectorsUnderOperation[kubeNode.GetUID()] = true

	// Remove or add RR labels based on expected number
	if diff < 0 {
		if err := c.datastore.RemoveRRStatus(kubeNode, c.findCalicoNode(kubeNode)); err != nil {
			log.Errorf("Unable to delete RR status %s: %s", kubeNode.GetName(), err.Error())
			return false, err
		}
		log.Infof("Removing route reflector label from %s", kubeNode.GetName())
	} else {
		if err := c.datastore.AddRRStatus(kubeNode, c.findCalicoNode(kubeNode)); err != nil {
			log.Errorf("Unable to add RR status %s: %s", kubeNode.GetName(), err.Error())
			return false, err
		}
		log.Infof("Adding route reflector label to %s", kubeNode.GetName())
	}

	// Update Kubernetes node
	if _, err := c.k8sNodeClient.Update(context.Background(), kubeNode, metav1.UpdateOptions{}); err != nil {
		log.Errorf("Unable to update node %s: %s", kubeNode.GetName(), err.Error())
		return false, err
	}

	delete(c.routeReflectorsUnderOperation, kubeNode.GetUID())

	return true, nil
}

// collectNodeInfo calculate
func (c *ctrl) collectNodeInfo(filter func(*corev1.Node) bool) (filtered map[*corev1.Node]bool) {
	filtered = map[*corev1.Node]bool{}

	for k, n := range c.kubeNodes {
		if filter(n) {
			n = c.kubeNodes[k]
			filtered[n] = c.isNodeCompatible(n)
		}
	}

	return
}

// isNodeCompatible is node good candidate to be RR
func (c *ctrl) isNodeCompatible(node *corev1.Node) bool {
	return isNodeReady(node) && isNodeSchedulable(node) && isNodeCompatible(node, c.incompatibleLabels)
}

// findCalicoNode finds matching pair of a Kubernetes node
func (c *ctrl) findCalicoNode(kubeNode *corev1.Node) *apiv3.Node {
	for _, cn := range c.calicoNodes {
		for _, orchRef := range cn.Spec.OrchRefs {
			if orchRef.Orchestrator == "k8s" && orchRef.NodeName == kubeNode.GetName() {
				log.Debugf("Calico node found %s for %s-%s", cn.GetName(), kubeNode.GetNamespace(), kubeNode.GetName())
				return cn
			}
		}
	}

	return nil
}
