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
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *ctrl) delete(kubeNode *corev1.Node) error {
	if c.topology.IsRouteReflector(string(kubeNode.GetUID()), kubeNode.GetLabels()) && kubeNode.GetDeletionTimestamp() != nil {
		if !c.isNodeActive(kubeNode) {
			// Node is deleted right now or has some issues, better to remove form RRs
			if err := c.removeRRStatus(kubeNode); err != nil {
				log.Errorf("Unable to cleanup label on %s because of %s", kubeNode.GetName(), err.Error())
				return err
			}

			log.Infof("Label was removed from node %s time to re-reconcile", kubeNode.GetName())
			return c.update(kubeNode)
		}
	}

	return nil
}

func (c *ctrl) update(kubeNode *corev1.Node) error {
	if err := c.revertFailedModification(); err != nil {
		return err
	}

	affectedNodes := c.collectNodeInfo(c.topology.GetNodeFilter(kubeNode))
	log.Debug("Affected nodes: %#v", affectedNodes)

	if err := c.updateNodeLabels(affectedNodes); err != nil {
		return err
	} else if err := c.updateBGPTopology(kubeNode, affectedNodes); err != nil {
		return err
	}

	return nil
}

func (c *ctrl) revertFailedModification() error {
	for _, kubeNode := range c.kubeNodes {
		status, ok := c.routeReflectorsUnderOperation[kubeNode.GetUID()]
		if !ok {
			continue
		}
		// Node was under operation, better to revert it
		var err error
		if status {
			err = c.datastore.RemoveRRStatus(kubeNode, c.findCalicoNode(kubeNode))
		} else {
			err = c.datastore.AddRRStatus(kubeNode, c.findCalicoNode(kubeNode))
		}
		if err != nil {
			log.Errorf("Failed to revert node %s because of %s", kubeNode.GetName(), err.Error())
			return err
		}

		log.Infof("Revert route reflector label on %s to %t", kubeNode.GetName(), !status)
		kubeNode, err := c.k8sNodeClient.Get(context.Background(), kubeNode.GetName(), metav1.GetOptions{})
		if err != nil {
			log.Errorf("Unable to fetch kube node %s because of %s", kubeNode.GetName(), err.Error())
			return err
		}

		if _, err := c.k8sNodeClient.Update(context.Background(), kubeNode, metav1.UpdateOptions{}); err != nil {
			log.Errorf("Failed to revert update node %s because of %s", kubeNode.GetName(), err.Error())
			return err
		}

		delete(c.routeReflectorsUnderOperation, kubeNode.GetUID())

		return c.update(kubeNode)
	}

	return nil
}

func (c *ctrl) updateNodeLabels(affectedNodes map[*corev1.Node]bool) error {
	missingRouteReflectors := 0

	for _, status := range c.topology.GetRouteReflectorStatuses(affectedNodes) {
		status.ExpectedRRs += missingRouteReflectors
		log.Infof("Route refletor status in zone(s) %v (actual/expected) = %d/%d", status.Zones, status.ActualRRs, status.ExpectedRRs)

		for _, n := range status.Nodes {
			n := c.kubeNodes[n.GetUID()]

			if !affectedNodes[n] {
				continue
			} else if status.ExpectedRRs == status.ActualRRs {
				break
			}

			if diff := status.ExpectedRRs - status.ActualRRs; diff != 0 {
				if updated, err := c.updateRRStatus(n, diff); err != nil {
					log.Errorf("Unable to update node %s because of %s", n.GetName(), err.Error())
					return err
				} else if updated && diff > 0 {
					status.ActualRRs++
				} else if updated && diff < 0 {
					status.ActualRRs--
				}
			}
		}

		if status.ExpectedRRs != status.ActualRRs {
			log.Infof("Actual number %d is different than expected %d", status.ActualRRs, status.ExpectedRRs)
			missingRouteReflectors = status.ExpectedRRs - status.ActualRRs
		}
	}

	if missingRouteReflectors != 0 {
		log.Errorf("Actual number is different than expected, missing: %d", missingRouteReflectors)
	}

	return nil
}

func (c *ctrl) updateBGPTopology(kubeNode *corev1.Node, affectedNodes map[*corev1.Node]bool) error {
	bgpPeers := []*apiv3.BGPPeer{}
	for _, p := range c.bgpPeers {
		bgpPeers = append(bgpPeers, p)
	}

	toRefresh, toRemove := c.topology.GenerateBGPPeers(affectedNodes, bgpPeers)

	log.Infof("Number of BGPPeers to refresh: %v", len(toRefresh))
	log.Debugf("To refresh BGPeers are: %v", toRefresh)
	log.Infof("Number of BGPPeers to delete: %v", len(toRemove))
	log.Debugf("To delete BGPeers are: %v", toRemove)

	for _, bp := range toRefresh {
		log.Infof("Saving %s BGPPeer", bp.Name)
		if err := c.bgpPeer.save(bp); err != nil {
			log.Errorf("Unable to save BGPPeer because of %s", err.Error())
			return err
		}
	}

	for _, p := range toRemove {
		log.Debugf("Removing BGPPeer: %s", p.GetName())
		if err := c.bgpPeer.remove(p); err != nil {
			log.Errorf("Unable to remove BGPPeer because of %s", err.Error())
			return err
		}
	}

	return nil
}

func (c *ctrl) removeRRStatus(kubeNode *corev1.Node) error {
	c.routeReflectorsUnderOperation[kubeNode.GetUID()] = false

	if err := c.datastore.RemoveRRStatus(kubeNode, c.findCalicoNode(kubeNode)); err != nil {
		log.Errorf("Unable to cleanup RR status %s because of %s", kubeNode.GetName(), err.Error())
		return err
	}

	log.Infof("Removing route reflector label from %s", kubeNode.GetName())
	if _, err := c.k8sNodeClient.Update(context.Background(), kubeNode, metav1.UpdateOptions{}); err != nil {
		log.Errorf("Unable to cleanup node %s because of %s", kubeNode.GetName(), err.Error())
		return err
	}

	delete(c.routeReflectorsUnderOperation, kubeNode.GetUID())

	return nil
}

func (c *ctrl) updateRRStatus(kubeNode *corev1.Node, diff int) (bool, error) {
	labeled := c.topology.IsRouteReflector(string(kubeNode.GetUID()), kubeNode.GetLabels())
	if labeled && diff > 0 || !labeled && diff < 0 {
		return false, nil
	}

	c.routeReflectorsUnderOperation[kubeNode.GetUID()] = true

	if diff < 0 {
		if err := c.datastore.RemoveRRStatus(kubeNode, c.findCalicoNode(kubeNode)); err != nil {
			log.Errorf("Unable to delete RR status %s because of %s", kubeNode.GetName(), err.Error())
			return false, err
		}
		log.Infof("Removing route reflector label to %s", kubeNode.GetName())
	} else {
		if err := c.datastore.AddRRStatus(kubeNode, c.findCalicoNode(kubeNode)); err != nil {
			log.Errorf("Unable to add RR status %s because of %s", kubeNode.GetName(), err.Error())
			return false, err
		}
		log.Infof("Adding route reflector label to %s", kubeNode.GetName())
	}

	if _, err := c.k8sNodeClient.Update(context.Background(), kubeNode, metav1.UpdateOptions{}); err != nil {
		log.Errorf("Unable to update node %s because of %s", kubeNode.GetName(), err.Error())
		return false, err
	}

	delete(c.routeReflectorsUnderOperation, kubeNode.GetUID())

	return true, nil
}

func (c *ctrl) collectNodeInfo(filter func(*corev1.Node) bool) (filtered map[*corev1.Node]bool) {
	filtered = map[*corev1.Node]bool{}

	for k, n := range c.kubeNodes {
		if filter(n) {
			n = c.kubeNodes[k]
			filtered[n] = isNodeReady(n) && isNodeSchedulable(n) && isNodeCompatible(n, c.incompatibleLabels)
		}
	}

	return
}

func (c *ctrl) findCalicoNode(kubeNode *corev1.Node) *apiv3.Node {
	for _, cn := range c.calicoNodes {
		if hostname, ok := cn.GetLabels()["kubernetes.io/hostname"]; ok && hostname == kubeNode.GetLabels()["kubernetes.io/hostname"] {
			log.Debugf("Calico node found %s for %s-%s", cn.GetName(), kubeNode.GetNamespace(), kubeNode.GetName())
			return cn
		}
	}

	return nil
}

func (c *ctrl) isNodeActive(node *corev1.Node) bool {
	return isNodeReady(node) && isNodeSchedulable(node) && isNodeCompatible(node, c.incompatibleLabels)
}
