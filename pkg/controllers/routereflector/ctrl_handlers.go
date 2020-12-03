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
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

type configSyncer struct {
	*ctrl
}

func (c *configSyncer) OnStatusUpdated(status bapi.SyncStatus) {
}

func (c *configSyncer) OnUpdates(updates []bapi.Update) {
	c.ctrl.updateMutex.Lock()
	defer c.ctrl.updateMutex.Unlock()

	// Update local cache.
	log.Debug("Controller config syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			log.Debug("New Controller config")
			fallthrough
		case bapi.UpdateTypeKVUpdated, bapi.UpdateTypeKVUnknown:
			log.Debug("Controller config updated")
			config := upd.KVPair.Value.(*apiv3.KubeControllersConfiguration)
			rrConfig := config.Spec.Controllers.RouteReflector
			// TODO find a better way to map
			if rrConfig != nil {
				c.ctrl.config.ClusterID = rrConfig.ClusterID
				c.ctrl.config.Min = rrConfig.Min
				c.ctrl.config.Max = rrConfig.Max
				c.ctrl.config.Ratio = rrConfig.Ratio
				c.ctrl.config.RouteReflectorLabelKey = rrConfig.RouteReflectorLabelKey
				c.ctrl.config.RouteReflectorLabelValue = rrConfig.RouteReflectorLabelValue
				c.ctrl.config.ZoneLabel = rrConfig.ZoneLabel
				c.ctrl.config.IncompatibleLabels = rrConfig.IncompatibleLabels
				c.ctrl.updateConfiguration()
			}
		default:
			log.Infof("Controller config unhandled update type %d", upd.UpdateType)
		}
	}
	log.Debug("RouteReflector config: %#v", c.ctrl.config)
}

type calicoNodeSyncer struct {
	*ctrl
}

func (c *calicoNodeSyncer) OnStatusUpdated(status bapi.SyncStatus) {
}

func (c *calicoNodeSyncer) OnUpdates(updates []bapi.Update) {
	c.ctrl.updateMutex.Lock()
	defer c.ctrl.updateMutex.Unlock()

	// Update local cache.
	log.Debug("RR Calico node syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			log.Debug("New Calico node")
			fallthrough
		case bapi.UpdateTypeKVUpdated, bapi.UpdateTypeKVUnknown:
			log.Debug("Calico node updated")
			// TODO: For some reason, syncer doesn't give revision on the KVPair.
			// So, we need to set it here.
			n := upd.KVPair.Value.(*apiv3.Node)
			n.ResourceVersion = upd.Revision
			c.calicoNodes[n.GetUID()] = n
		case bapi.UpdateTypeKVDeleted:
			log.Debug("Calico node deleted")
			n := upd.KVPair.Value.(*apiv3.Node)
			delete(c.calicoNodes, n.GetUID())
		default:
			log.Errorf("Calico node unhandled update type %d", upd.UpdateType)
		}
	}
	log.Debug("Calico node cache: %#v", c.calicoNodes)
}

type bgpPeerSyncer struct {
	*ctrl
}

func (c *bgpPeerSyncer) OnStatusUpdated(status bapi.SyncStatus) {
}

func (c *bgpPeerSyncer) OnUpdates(updates []bapi.Update) {
	c.ctrl.updateMutex.Lock()
	defer c.ctrl.updateMutex.Unlock()

	// Update local cache.
	log.Debug("RR BGP peer syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			log.Debug("New BGP peer")
			fallthrough
		case bapi.UpdateTypeKVUpdated, bapi.UpdateTypeKVUnknown:
			log.Debug("BGP peer updated")
			// TODO: For some reason, syncer doesn't give revision on the KVPair.
			// So, we need to set it here.
			p := upd.KVPair.Value.(*apiv3.BGPPeer)
			p.ResourceVersion = upd.Revision
			c.bgpPeers[p] = true
		case bapi.UpdateTypeKVDeleted:
			log.Debug("BGP peer deleted")
			p := upd.KVPair.Value.(*apiv3.BGPPeer)
			delete(c.bgpPeers, p)
		default:
			log.Errorf("BGP peer unhandled update type %d", upd.UpdateType)
		}
	}
	log.Debug("BGP peer cache: %#v", c.calicoNodes)
}

func (c *ctrl) OnKubeUpdate(oldObj interface{}, newObj interface{}) {
	c.updateMutex.Lock()
	defer c.updateMutex.Unlock()

	log.Debug("Kube node updated")
	newKubeNode, ok := newObj.(*corev1.Node)
	if !ok {
		log.Errorf("Given resource type can't handle %v", newObj)
		return
	}

	c.kubeNodes[newKubeNode.GetUID()] = newKubeNode

	if err := c.update(newKubeNode); err != nil {
		log.Errorf("Unable to update Kubernetes node %s because of %s", newKubeNode.GetName(), err)
	}
}

func (c *ctrl) OnKubeDelete(obj interface{}) {
	c.updateMutex.Lock()
	defer c.updateMutex.Unlock()

	kubeNode, ok := obj.(*corev1.Node)
	if !ok {
		log.Errorf("Given resource type can't handle %v", obj)
		return
	}

	if err := c.delete(kubeNode); err != nil {
		log.Errorf("Unable to delete Kubernetes node %s because of %s", kubeNode.GetName(), err)
	}

	delete(c.kubeNodes, kubeNode.GetUID())
}
