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
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/datastores"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/loaders"
	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	log "github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/types"
)

type ctrl struct {
	inSync                        bool
	syncer                        bapi.Syncer
	calicoNodeClient              client.Interface
	k8sNodeClient                 k8sNodeClient
	NodeLabelKey                  string
	IncompatibleLabels            map[string]*string
	Topology                      topologies.Topology
	Datastore                     datastores.Datastore
	BGPPeer                       bgpPeer
	nodes                         map[types.UID]*apiv3.Node
	routeReflectorsUnderOperation map[types.UID]bool
}

func (c *ctrl) Run(stopCh chan struct{}) {
	// Start the syncer.
	go c.syncer.Start()

	<-stopCh
	log.Info("Stopping route reflector controller")
}

func (c *ctrl) initSyncer() {
	// TODO: Also watch BGP peers  so we can correct if necessary?
	resourceTypes := []watchersyncer.ResourceType{
		{
			ListInterface: model.ResourceListOptions{Kind: apiv3.KindNode},
		},
	}
	type accessor interface {
		Backend() bapi.Client
	}
	c.syncer = watchersyncer.New(c.calicoNodeClient.(accessor).Backend(), resourceTypes, c)
}

func (c *ctrl) OnStatusUpdated(status bapi.SyncStatus) {
	log.Infof("RR syncer status updated: %s", status)
	if status == bapi.InSync {
		log.Infof("In sync with data store, perform reconciliation")
		c.inSync = true
		for _, n := range c.nodes {
			c.update(n)
		}
	}
}

func (c *ctrl) OnUpdates(updates []bapi.Update) {
	// Update local cache.
	log.Debug("RR syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			log.Debug("New node")
			fallthrough
		case bapi.UpdateTypeKVUpdated:
			log.Debug("Node updated")
			// TODO: For some reason, syncer doesn't give revision on the KVPair.
			// So, we need to set it here.
			n := upd.KVPair.Value.(*apiv3.Node)
			n.ResourceVersion = upd.Revision
			c.nodes[n.GetUID()] = n

			if c.inSync {
				c.update(n)
			}
		case bapi.UpdateTypeKVDeleted:
			log.Debug("Node deleted")
			n := upd.KVPair.Value.(*apiv3.Node)
			c.delete(n)
			delete(c.nodes, n.GetUID())
		default:
			log.Errorf("Unhandled update type")
		}
	}
	log.Debug("Node cache: %#v", c.nodes)

	// Then, reconcile RR state if we're in sync.
	if !c.inSync {
		log.Infof("Not yet in sync - wait for reconciliation")
		return
	}
}

func (c *ctrl) delete(calicoNode *apiv3.Node) error {
	if c.Topology.IsRouteReflector(string(calicoNode.GetUID()), calicoNode.GetLabels()) && calicoNode.GetDeletionTimestamp() != nil {
		loader := c.newNodeLoader(calicoNode.GetLabels())
		kubeNode, err := loader.Get(calicoNode)
		if err != nil {
			log.Errorf("Unable to fetch kube node %s because of %s", calicoNode.GetName(), err.Error())
			return err
		}

		if !c.isNodeActive(kubeNode) {
			// Node is deleted right now or has some issues, better to remove form RRs
			if err := c.removeRRStatus(calicoNode, kubeNode); err != nil {
				log.Errorf("Unable to cleanup label on %s because of %s", calicoNode.GetName(), err.Error())
				return err
			}

			log.Infof("Label was removed from node %s time to re-reconcile", calicoNode.GetName())
		}
	}

	return nil
}

func (c *ctrl) update(calicoNode *apiv3.Node) error {
	requestCtrl := requestCtrl{
		ctrl:       c,
		nodeLoader: c.newNodeLoader(calicoNode.GetLabels()),
	}

	if err := requestCtrl.revertFailedModification(); err != nil {
		return err
	} else if err := requestCtrl.updateNodeLabels(); err != nil {
		return err
	} else if err := requestCtrl.updateBGPTopology(); err != nil {
		return err
	}

	return nil
}

func (c *ctrl) newNodeLoader(nodeLabels map[string]string) loaders.NodeLoader {
	listOpts := c.Topology.NewNodeListOptions(nodeLabels)
	log.Debugf("List options are %v", listOpts)

	return loaders.NewNodeLoader(c.k8sNodeClient, listOpts)
}
