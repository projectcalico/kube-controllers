// Copyright (c) 2017-2020 Tigera, Inc. All rights reserved.
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
	"os"

	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

func (c *NodeController) initSyncer() {
	resourceTypes := []watchersyncer.ResourceType{
		{
			ListInterface: model.ResourceListOptions{Kind: apiv3.KindNode},
		},
	}
	type accessor interface {
		Backend() bapi.Client
	}
	c.syncer = watchersyncer.New(c.calicoClient.(accessor).Backend(), resourceTypes, c)
}

func (c *NodeController) OnStatusUpdated(status bapi.SyncStatus) {
	logrus.Infof("Node controller syncer status updated: %s", status)
	c.syncStatus = status

	switch status {
	case bapi.ResyncInProgress:
		// Starting the resync so clear our node cache.
		c.nodeCache = make(map[string]*api.Node)

	case bapi.InSync:
		err := c.syncAutoHostendpoints()
		exitOnError(err)
	}
}

func exitOnError(err error) {
	if err != nil {
		os.Exit(1)
	}
}

func (c *NodeController) OnUpdates(updates []bapi.Update) {
	logrus.Debugf("Node controller syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			fallthrough

		case bapi.UpdateTypeKVUpdated:
			n := upd.KVPair.Value.(*apiv3.Node)

			if c.syncLabels {
				if kn := getK8sNodeName(*n); kn != "" {
					// Create a mapping from Kubernetes node -> Calico node.
					logrus.Debugf("Mapping k8s node -> calico node. %s -> %s", kn, n.Name)
					c.nodemapLock.Lock()
					c.nodemapper[kn] = n.Name
					c.nodemapLock.Unlock()

					// It has a node reference - get that Kubernetes node, and if
					// it exists perform a sync.
					obj, ok, err := c.indexer.GetByKey(kn)
					if !ok {
						logrus.Debugf("No corresponding kubernetes node")
						continue
					} else if err != nil {
						logrus.WithError(err).Warnf("Couldn't get node from indexer")
						continue
					}
					c.syncNodeLabels(obj.(*v1.Node))
				}
			}

			if c.autoHostEndpoints {
				// During resync, cache all updated nodes. If we're already in-sync,
				// sync the node's auto hostendpoint.
				if c.syncStatus == bapi.ResyncInProgress {
					c.nodeCache[n.Name] = n
				} else if c.syncStatus == bapi.InSync {
					err := c.syncHostendpoint(n)
					exitOnError(err)
				}
			}

		case bapi.UpdateTypeKVDeleted:
			if upd.KVPair.Value != nil {
				logrus.Warnf("KVPair value should be nil for Deleted UpdataType")
			}

			nodeName := upd.KVPair.Key.(model.ResourceKey).Name

			// Try to perform unmapping based on resource name (calico node name).
			if c.syncLabels {
				for kn, cn := range c.nodemapper {
					if cn == nodeName {
						// Remove it from node map.
						logrus.Debugf("Unmapping k8s node -> calico node. %s -> %s", kn, cn)
						c.nodemapLock.Lock()
						delete(c.nodemapper, kn)
						c.nodemapLock.Unlock()
						break
					}
				}
			}

			if c.autoHostEndpoints {
				hepName := c.generateHostendpointName(nodeName)
				err := c.deleteHostendpoint(hepName)
				exitOnError(err)
			}

		default:
			logrus.Errorf("Unhandled update type")
		}
	}
}
