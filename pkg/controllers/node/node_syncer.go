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

package node

import (
	"context"
	"encoding/json"
	"time"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	"github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
}

func (c *NodeController) handleNewNode(u bapi.Update) {
	if c.config.AutoHostEndpoints != "enabled" {
		return
	}

	n := u.KVPair.Value.(*apiv3.Node)
	// Check if we have a HEP created already for this node.
	// If not, create one and log
	// If we already have one, log anyways
	_, err := c.calicoClient.HostEndpoints().Get(c.ctx, n.Name, options.GetOptions{})

	if err != nil {
		switch err.(type) {
		case errors.ErrorResourceDoesNotExist:
			logrus.Infof("host endpoint for node %q does not exist", n.Name)

			// Copy over node labels to hep
			hepLabels := make(map[string]string, len(n.Labels))
			for k, v := range n.Labels {
				hepLabels[k] = v
			}

			hep := &apiv3.HostEndpoint{
				TypeMeta: metav1.TypeMeta{Kind: "HostEndpoint", APIVersion: "v3"},
				ObjectMeta: metav1.ObjectMeta{
					Name:   n.Name,
					Labels: hepLabels,
				},
				Spec: apiv3.HostEndpointSpec{
					Node:          n.Name,
					InterfaceName: "*",
				},
			}
			h, err := c.calicoClient.HostEndpoints().Create(c.ctx, hep, options.SetOptions{})
			// TODO: we should retry here a few times.
			if err != nil {
				logrus.Warnf("error creating host endpoint for new node %q: %v", n.Name, err.Error())
				return
			}
			logrus.Debugf("created hep for new node: %#v", h)
			logrus.Infof("created hep for new node %q", n.Name)
			return
		default:
			logrus.Infof("error getting hep: %v", err.Error())
			return
		}
	}
	logrus.Infof("host endpoint for node %q already exists", n.Name)
}

func (c *NodeController) deleteHEP(nodeName string) {
	if c.config.AutoHostEndpoints != "enabled" {
		return
	}

	_, err := c.calicoClient.HostEndpoints().Delete(c.ctx, nodeName, options.DeleteOptions{})
	// TODO: retry
	if err != nil {
		switch err.(type) {
		case errors.ErrorResourceDoesNotExist:
			logrus.Warnf("could not delete host endpoint for node %q because it does not exist", nodeName)
			return
		default:
			logrus.Warnf("could not delete host endpoint for node %q: %v", nodeName, err)
			return
		}
	}

	logrus.Infof("deleted host endpoint for node %q", nodeName)
}

func (c *NodeController) OnUpdates(updates []bapi.Update) {
	logrus.Debugf("Node controller syncer received updates: %#v", updates)
	for _, upd := range updates {
		switch upd.UpdateType {
		case bapi.UpdateTypeKVNew:
			c.handleNewNode(upd)

		case bapi.UpdateTypeKVUpdated:
			n := upd.KVPair.Value.(*apiv3.Node)
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
		case bapi.UpdateTypeKVDeleted:
			if upd.KVPair.Value != nil {
				logrus.Warnf("KVPair value should be nil for Deleted UpdataType")
			}

			// Try to perform unmapping based on resource name (calico node name).
			n := upd.KVPair.Key.(model.ResourceKey).Name
			for kn, cn := range c.nodemapper {
				if cn == n {
					// Remove it from node map.
					logrus.Debugf("Unmapping k8s node -> calico node. %s -> %s", kn, cn)
					c.nodemapLock.Lock()
					delete(c.nodemapper, kn)
					c.nodemapLock.Unlock()
					break
				}
			}
			c.deleteHEP(n)

		default:
			logrus.Errorf("Unhandled update type")
		}
	}
}

// syncNodeLabels syncs the labels found in v1.Node to the Calico node object.
// It uses an annotation on the Calico node object to keep track of which labels have
// been synced from Kubernetes, so that it doesn't overwrite user provided labels (e.g.,
// via calicoctl or another Calico controller).
// sync labels from a Calico node object to HEP
func (nc *NodeController) syncHEPLabels(nodeName string) {
	// On failure, we retry a certain number of times.
	for n := 1; n < 5; n++ {
		// Get the Calico node representation.
		nc.nodemapLock.Lock()
		name, ok := nc.nodemapper[nodeName]
		nc.nodemapLock.Unlock()
		if !ok {
			// We havent learned this Calico node yet.
			log.Debugf("Skipping update for node with no Calico equivalent")
			return
		}
		calNode, err := nc.calicoClient.Nodes().Get(nc.ctx, name, options.GetOptions{})
		if err != nil {
			log.WithError(err).Warnf("Failed to get node, retrying")
			time.Sleep(retrySleepTime)
			continue
		}

		hep, err := nc.calicoClient.HostEndpoints().Get(nc.ctx, nodeName, options.GetOptions{})
		if err != nil {
			log.WithError(err).Warnf("Failed to get host endpoint, retrying")
			time.Sleep(retrySleepTime)
			continue
		}

		if hep.Labels == nil {
			hep.Labels = make(map[string]string)
		}
		if hep.Annotations == nil {
			hep.Annotations = make(map[string]string)
		}

		// Track if we need to perform an update.
		needsUpdate := false

		// If there are labels present, then parse them. Otherwise this is
		// a first-time sync, in which case there are no old labels.
		oldLabels := make(map[string]string)
		if a, ok := hep.Annotations[hepLabelAnnotation]; ok {
			if err = json.Unmarshal([]byte(a), &oldLabels); err != nil {
				log.WithError(err).Error("failed to unmarshal hep labels")
				return
			}
		}
		log.Debugf("determined previously synced labels: %s", oldLabels)

		// We've synced labels before. Determine diffs to apply.
		// For each k/v in calico node labels, if it isn't present or the value
		// differs, add it to the hep.
		for k, v := range calNode.Labels {
			if v2, ok := hep.Labels[k]; !ok || v != v2 {
				log.Debugf("adding hep label %s=%s", k, v)
				hep.Labels[k] = v
				needsUpdate = true
			}
		}

		// For each k/v that used to be in the Calico node labels, but is no longer,
		// remove it from the Calico node.
		for k, v := range oldLabels {
			if _, ok := calNode.Labels[k]; !ok {
				// The old label is no longer present. Remove it.
				log.Debugf("deleting hep label %s=%s", k, v)
				delete(hep.Labels, k)
				needsUpdate = true
			}
		}

		// Set the annotation to the correct values.
		bytes, err := json.Marshal(hep.Labels)
		if err != nil {
			log.WithError(err).Errorf("Error marshalling node labels")
			return
		}
		hep.Annotations[hepLabelAnnotation] = string(bytes)

		// Update the hep in the datastore.
		if needsUpdate {
			if _, err := nc.calicoClient.HostEndpoints().Update(context.Background(), hep, options.SetOptions{}); err != nil {
				log.WithError(err).Warnf("failed to update host endpoint, retrying")
				time.Sleep(retrySleepTime)
				continue
			}
			log.WithField("hostendpoint", calNode.ObjectMeta.Name).Info("successfully synced hostendpoint labels")
		}
		return
	}
	log.Errorf("Too many retries when updating node")
}
