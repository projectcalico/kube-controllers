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

package routereflector

import (
	"time"

	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/loaders"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

func (c *ctrl) revertFailedModification(nodeLoader loaders.NodeLoader) error {
	for _, calicoNode := range c.nodes {
		status, ok := c.routeReflectorsUnderOperation[calicoNode.GetUID()]
		if !ok {
			continue
		}
		// Node was under operation, better to revert it
		var err error
		if status {
			err = c.Datastore.RemoveRRStatus(calicoNode)
		} else {
			err = c.Datastore.AddRRStatus(calicoNode)
		}
		if err != nil {
			log.Errorf("Failed to revert node %s because of %s", calicoNode.GetName(), err.Error())
			return err
		}

		log.Infof("Revert route reflector label on %s to %t", calicoNode.GetName(), !status)
		if _, ok := c.nodes[calicoNode.GetUID()]; ok {
			kubeNode, err := nodeLoader.Get(calicoNode)
			if err != nil {
				log.Errorf("Unable to fetch kube node %s because of %s", calicoNode.GetName(), err.Error())
				return err
			}

			if _, err := c.k8sNodeClient.Update(kubeNode); err != nil {
				log.Errorf("Failed to revert update node %s because of %s", calicoNode.GetName(), err.Error())
				return err
			}
		}

		delete(c.routeReflectorsUnderOperation, calicoNode.GetUID())

		return nil
	}

	return nil
}

func (c *ctrl) updateNodeLabels(nodeLoader loaders.NodeLoader) error {
	missingRouteReflectors := 0

	nodesByReady := c.collectNodesByReady(nodeLoader)

	for _, status := range c.Topology.GetRouteReflectorStatuses(nodesByReady) {
		status.ExpectedRRs += missingRouteReflectors
		log.Infof("Route refletor status in zone(s) %v (actual/expected) = %d/%d", status.Zones, status.ActualRRs, status.ExpectedRRs)

		for _, n := range status.Nodes {
			n := c.nodes[n.GetUID()]

			if nodesByReady[n] {
				continue
			} else if status.ExpectedRRs == status.ActualRRs {
				break
			}

			if diff := status.ExpectedRRs - status.ActualRRs; diff != 0 {
				if updated, err := c.updateRRStatus(n, nodeLoader, diff); err != nil {
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

func (c *ctrl) updateBGPTopology(nodeLoader loaders.NodeLoader) error {
	existingBGPPeers, err := c.BGPPeer.list()
	if err != nil {
		log.Errorf("Unable to list BGP peers because of %s", err.Error())
		return err
	}

	log.Debugf("Existing BGPeers are: %v", existingBGPPeers.Items)

	nodesByReady := collectNodesByReady(nodeLoader)

	toRefresh, toRemove := c.Topology.GenerateBGPPeers(c.nodes, nodesByReady, existingBGPPeers)

	log.Infof("Number of BGPPeers to refresh: %v", len(toRefresh))
	log.Debugf("To refresh BGPeers are: %v", toRefresh)
	log.Infof("Number of BGPPeers to delete: %v", len(toRemove))
	log.Debugf("To delete BGPeers are: %v", toRemove)

	for _, bp := range toRefresh {
		log.Infof("Saving %s BGPPeer", bp.Name)
		if err := c.BGPPeer.save(&bp); err != nil {
			log.Errorf("Unable to save BGPPeer because of %s", err.Error())
			return err
		}
	}

	// Give some time to Calico to establish new connections before deleting old ones
	if len(toRefresh) > 0 {
		time.Sleep(2)
	}

	for _, p := range toRemove {
		log.Debugf("Removing BGPPeer: %s", p.GetName())
		if err := c.BGPPeer.remove(&p); err != nil {
			log.Errorf("Unable to remove BGPPeer because of %s", err.Error())
			return err
		}
	}

	return nil
}

func (c *ctrl) removeRRStatus(calicoNode *apiv3.Node, kubeNode *corev1.Node) error {
	c.routeReflectorsUnderOperation[calicoNode.GetUID()] = false

	if err := c.Datastore.RemoveRRStatus(calicoNode); err != nil {
		log.Errorf("Unable to cleanup RR status %s because of %s", calicoNode.GetName(), err.Error())
		return err
	}

	log.Infof("Removing route reflector label from %s", calicoNode.GetName())
	_, err := c.k8sNodeClient.Update(kubeNode)
	if err != nil {
		log.Errorf("Unable to cleanup node %s because of %s", calicoNode.GetName(), err.Error())
		return err
	}

	delete(c.routeReflectorsUnderOperation, calicoNode.GetUID())

	return nil
}

func (c *ctrl) updateRRStatus(calicoNode *apiv3.Node, nodeLoader loaders.NodeLoader, diff int) (bool, error) {
	labeled := c.Topology.IsRouteReflector(string(calicoNode.GetUID()), calicoNode.GetLabels())
	if labeled && diff > 0 || !labeled && diff < 0 {
		return false, nil
	}

	c.routeReflectorsUnderOperation[calicoNode.GetUID()] = true

	if diff < 0 {
		if err := c.Datastore.RemoveRRStatus(calicoNode); err != nil {
			log.Errorf("Unable to delete RR status %s because of %s", calicoNode.GetName(), err.Error())
			return false, err
		}
		log.Infof("Removing route reflector label to %s", calicoNode.GetName())
	} else {
		if err := c.Datastore.AddRRStatus(calicoNode); err != nil {
			log.Errorf("Unable to add RR status %s because of %s", calicoNode.GetName(), err.Error())
			return false, err
		}
		log.Infof("Adding route reflector label to %s", calicoNode.GetName())
	}

	kubeNode, err := nodeLoader.Get(calicoNode)
	if err != nil {
		log.Errorf("Unable to fetch kube node %s because of %s", calicoNode.GetName(), err.Error())
		return false, err
	}

	_, err = c.k8sNodeClient.Update(kubeNode)
	if err != nil {
		log.Errorf("Unable to update node %s because of %s", calicoNode, err.Error())
		return false, err
	}

	delete(c.routeReflectorsUnderOperation, calicoNode.GetUID())

	return true, nil
}

func (c *ctrl) collectNodesByReady(kubeNodes []corev1.Node, antyAfiinity map[string]*string) map[*apiv3.Node]bool {
	byReady := map[*apiv3.Node]bool{}

	for _, n := range kubeNodes {
		byReady[&n] = c.isNodeActive(&n)
	}

	return
}

func (c *ctrl) isNodeActive(node *corev1.Node) bool {
	return isNodeReady(node) && isNodeSchedulable(node) && isNodeCompatible(node, c.IncompatibleLabels)
}
