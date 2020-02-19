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
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/options"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RateLimitCalicoList   = "calico-list"
	RateLimitK8s          = "k8s"
	RateLimitCalicoCreate = "calico-create"
	RateLimitCalicoDelete = "calico-delete"
	nodeLabelAnnotation   = "projectcalico.org/kube-labels"
	hepLabelAnnotation    = "projectcalico.org/node-labels"
	hepCreatedLabelKey    = "projetcalico.org/created-by"
	hepCreatedLabelValue  = "calico-kube-controllers"
)

var (
	retrySleepTime = 100 * time.Millisecond
)

// NodeController implements the Controller interface.  It is responsible for monitoring
// kubernetes nodes and responding to delete events by removing them from the Calico datastore.
type NodeController struct {
	ctx               context.Context
	informer          cache.Controller
	indexer           cache.Indexer
	calicoClient      client.Interface
	k8sClientset      *kubernetes.Clientset
	rl                workqueue.RateLimiter
	schedule          chan interface{}
	nodemapper        map[string]string
	nodemapLock       sync.Mutex
	syncer            bapi.Syncer
	config            *config.Config
	syncLabels        bool
	autoHostEndpoints bool
}

// NewNodeController Constructor for NodeController
func NewNodeController(ctx context.Context, k8sClientset *kubernetes.Clientset, calicoClient client.Interface, cfg *config.Config) controller.Controller {
	nc := &NodeController{
		ctx:               ctx,
		calicoClient:      calicoClient,
		k8sClientset:      k8sClientset,
		rl:                workqueue.DefaultControllerRateLimiter(),
		nodemapper:        map[string]string{},
		config:            cfg,
		autoHostEndpoints: cfg.AutoHostEndpoints == "enabled",
	}

	// channel used to kick the controller into scheduling a sync. It has length
	// 1 so that we coalesce multiple kicks while a sync is happening down to
	// just one additional sync.
	nc.schedule = make(chan interface{}, 1)

	// Create a Node watcher.
	listWatcher := cache.NewListWatchFromClient(k8sClientset.CoreV1().RESTClient(), "nodes", "", fields.Everything())

	// Setup event handlers
	handlers := cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			// Just kick controller to wake up and perform a sync. No need to bother what node it was
			// as we sync everything.
			kick(nc.schedule)
		}}

	// Determine if we should sync node labels.
	nc.syncLabels = cfg.SyncNodeLabels && cfg.DatastoreType != "kubernetes"

	if nc.syncLabels {
		// Add handlers for node add/update events from k8s.
		handlers.AddFunc = func(obj interface{}) {
			nc.syncNodeLabels(obj.(*v1.Node))
		}
		handlers.UpdateFunc = func(_, obj interface{}) {
			nc.syncNodeLabels(obj.(*v1.Node))
		}
	}

	// Informer handles managing the watch and signals us when nodes are deleted.
	// also syncs up labels between k8s/calico node objects
	nc.indexer, nc.informer = cache.NewIndexerInformer(listWatcher, &v1.Node{}, 0, handlers, cache.Indexers{})

	if nc.syncLabels {
		// Start the syncer.
		nc.initSyncer()
		nc.syncer.Start()
	}

	if nc.autoHostEndpoints {
		nc.syncAllHostendpoints()
	}

	return nc
}

// getK8sNodeName is a helper method that searches a calicoNode for its kubernetes nodeRef.
func getK8sNodeName(calicoNode api.Node) string {
	for _, orchRef := range calicoNode.Spec.OrchRefs {
		if orchRef.Orchestrator == "k8s" {
			return orchRef.NodeName
		}
	}
	return ""
}

// Run starts the node controller. It does start-of-day preparation
// and then launches worker threads. We ignore reconcilerPeriod and threadiness
// as this controller does not use a cache and runs only one worker thread.
func (c *NodeController) Run(threadiness int, reconcilerPeriod string, stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	log.Info("Starting Node controller")

	// Wait till k8s cache is synced
	go c.informer.Run(stopCh)
	log.Debug("Waiting to sync with Kubernetes API (Nodes)")
	for !c.informer.HasSynced() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Debug("Finished syncing with Kubernetes API (Nodes)")

	// Start Calico cache.
	go c.acceptScheduleRequests(stopCh)

	log.Info("Node controller is now running")

	// Kick off a start of day sync. Write non-blocking so that if a sync is
	// already scheduled, we don't schedule another.
	kick(c.schedule)

	<-stopCh
	log.Info("Stopping Node controller")
}

// acceptScheduleRequests monitors the schedule channel for kicks to wake up
// and schedule syncs.
func (c *NodeController) acceptScheduleRequests(stopCh <-chan struct{}) {
	for {
		// Wait until something wakes us up, or we are stopped
		select {
		case <-c.schedule:
			err := c.syncDelete()
			if err != nil {
				// Reschedule the sync since we hit an error. Note that
				// syncDelete() does its own rate limiting, so it's fine to
				// reschedule immediately.
				kick(c.schedule)
			}
		case <-stopCh:
			return
		}
	}
}

func (c *NodeController) syncDelete() error {
	// Call the appropriate cleanup logic based on whether we're using
	// Kubernetes datastore or etecdv3.
	if c.config.DatastoreType == "kubernetes" {
		return c.syncDeleteKDD()
	}
	return c.syncDeleteEtcd()
}

// isAutoHostendpoint determines if the given hostendpoint is managed by
// kube-controllers.
func isAutoHostendpoint(h *api.HostEndpoint) bool {
	v, ok := h.Labels[hepCreatedLabelKey]
	return ok && v == hepCreatedLabelValue
}

// createHostendpoint creates an auto hostendpoint for the specified node.
func (c *NodeController) createHostendpoint(n *api.Node) error {
	// Create a set of labels from the node labels and include the
	// special label marking the hep as created by us.
	hepLabels := make(map[string]string, len(n.Labels))
	hepLabels[hepCreatedLabelKey] = hepCreatedLabelValue
	for k, v := range n.Labels {
		hepLabels[k] = v
	}

	hep := &api.HostEndpoint{
		TypeMeta: metav1.TypeMeta{Kind: "HostEndpoint", APIVersion: "v3"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   n.Name,
			Labels: hepLabels,
		},
		Spec: api.HostEndpointSpec{
			Node:          n.Name,
			InterfaceName: "*",
		},
	}

	time.Sleep(c.rl.When(RateLimitCalicoCreate))
	_, err := c.calicoClient.HostEndpoints().Create(c.ctx, hep, options.SetOptions{})
	if err != nil {
		log.Warnf("could not create hostendpoint for node: %v", err)
		return err
	}
	c.rl.Forget(RateLimitCalicoCreate)
	return nil
}

// syncAllHostendpoints syncs all Calico nodes with their hostendpoints.
// If hostendpoints don't already exist, they are created. Dangling auto
// hostendpoints are deleted.
func (c *NodeController) syncAllHostendpoints() {
	log.Debug("syncing all hostendpoints")

	// On failure, we retry a certain number of times.
	for n := 0; n < 5; n++ {
		nodesList, err := c.calicoClient.Nodes().List(c.ctx, options.ListOptions{})
		if err != nil {
			log.WithError(err).Info("could not list nodes, retrying")
			time.Sleep(retrySleepTime)
			continue
		}

		hepsList, err := c.calicoClient.HostEndpoints().List(c.ctx, options.ListOptions{})
		if err != nil {
			log.WithError(err).Info("could not list hostendpoints, retrying")
			time.Sleep(retrySleepTime)
			continue
		}

		// Filter the list of hostendpoints so that we are working only with auto
		// hostendpoints created by kube-controllers.
		heps := make(map[string]api.HostEndpoint)
		for _, h := range hepsList.Items {
			if isAutoHostendpoint(&h) {
				heps[h.Name] = h
			}
		}

		// Go through current nodes, creating hostendpoints for them if they don't exist.
		for _, n := range nodesList.Items {
			if _, ok := heps[n.Name]; !ok {
				err = c.createHostendpoint(&n)
				if err != nil {
					log.WithError(err).Info("could not create hostendpoint, retrying")
					time.Sleep(retrySleepTime)
					continue
				}
			}
		}

		nodes := make(map[string]api.Node)
		for _, n := range nodesList.Items {
			nodes[n.Name] = n
		}

		// Now go through the current host endpoints. If the hep has the special
		// label signifying it was created by kube-controllers but the hep doesn't
		// correspond to a current node, then we remove it.
		for _, h := range hepsList.Items {
			_, isAutoHep := h.Labels[hepCreatedLabelKey]
			_, hepHasNode := nodes[h.Name]

			if isAutoHep && !hepHasNode {
				time.Sleep(c.rl.When(RateLimitCalicoDelete))
				_, err := c.calicoClient.HostEndpoints().Delete(c.ctx, h.Name, options.DeleteOptions{})
				if err != nil {
					log.WithError(err).Warnf("could not delete unused hostendpoint %q", h.Name)
					time.Sleep(retrySleepTime)
					continue
				}
			}
		}

		// Now sync node labels to the hostendpoints.
		for _, n := range nodesList.Items {
			c.syncHostendpointLabels(n.Name)
		}
	}

}

// kick puts an item on the channel in non-blocking write. This means if there
// is already something pending, it has no effect. This allows us to coalesce
// multiple requests into a single pending request.
func kick(c chan<- interface{}) {
	select {
	case c <- nil:
		// pass
	default:
		// pass
	}
}

// syncHostendpointLabels syncs the labels in Calico node objects to their corresponding
// host endpoints.
func (c *NodeController) syncHostendpointLabels(nodeName string) {
	// On failure, we retry a certain number of times.
	for n := 0; n < 5; n++ {
		calNode, err := c.calicoClient.Nodes().Get(c.ctx, nodeName, options.GetOptions{})
		if err != nil {
			log.WithError(err).Warnf("failed to get node, retrying")
			time.Sleep(retrySleepTime)
			continue
		}

		// Try getting the host endpoint. If it doesn't exist, create it.
		// If the host endpoint does not exist, create it.
		hep, err := c.calicoClient.HostEndpoints().Get(c.ctx, nodeName, options.GetOptions{})
		if err != nil {
			switch err.(type) {
			case errors.ErrorResourceDoesNotExist:
				log.Infof("host endpoint %q doesn't exist, creating...", nodeName)
				err = c.createHostendpoint(calNode)
				if err != nil {
					log.WithError(err).Warnf("failed to create host endpoint %q, retrying", nodeName)
					time.Sleep(retrySleepTime)
					continue
				}
			default:
				log.WithError(err).Warnf("failed to get host endpoint %q, retrying", nodeName)
				time.Sleep(retrySleepTime)
				continue
			}
		}

		// Only continue if this is an auto hostendpoint. An auto hostendpoint
		// will contain a special label. We only sync hostendpoints that we've created.
		if !isAutoHostendpoint(hep) {
			return
		}

		if hep.Labels == nil {
			hep.Labels = make(map[string]string)
		}
		if hep.Annotations == nil {
			hep.Annotations = make(map[string]string)
		}

		// Track if we need to perform an update.
		needsUpdate := false

		// If there are hostendpoint labels present, then parse them. Otherwise this is
		// a first-time sync, in which case there are no old labels.
		oldLabels := make(map[string]string)
		if a, ok := hep.Annotations[hepLabelAnnotation]; ok {
			if err = json.Unmarshal([]byte(a), &oldLabels); err != nil {
				log.WithError(err).Error("failed to unmarshal hostendpoint labels")
				return
			}
		}
		log.Debugf("determined previously synced hostendpoint labels: %s", oldLabels)

		// We've synced labels before. Determine diffs to apply.
		// For each k/v in calico node labels, if it isn't present or the value
		// differs, add it to the hep.
		for k, v := range calNode.Labels {
			if v2, ok := hep.Labels[k]; !ok || v != v2 {
				log.Debugf("adding hostendpoint label %s=%s", k, v)
				hep.Labels[k] = v
				needsUpdate = true
			}
		}

		// For each k/v that used to be in the Calico node labels, but is no longer,
		// remove it from the Calico node.
		for k, v := range oldLabels {
			if _, ok := calNode.Labels[k]; !ok {
				// The old label is no longer present. Remove it.
				log.Debugf("deleting hostendpoint label %s=%s", k, v)
				delete(hep.Labels, k)
				needsUpdate = true
			}
		}

		// Set the annotation to the correct values.
		bytes, err := json.Marshal(hep.Labels)
		if err != nil {
			log.WithError(err).Errorf("error marshalling hostendpoint labels")
			return
		}
		hep.Annotations[hepLabelAnnotation] = string(bytes)

		// Update the hostendpoint in the datastore.
		if needsUpdate {
			if _, err := c.calicoClient.HostEndpoints().Update(context.Background(), hep, options.SetOptions{}); err != nil {
				log.WithError(err).Warnf("failed to update hostendpoint, retrying")
				time.Sleep(retrySleepTime)
				continue
			}
			log.WithField("hostendpoint", calNode.ObjectMeta.Name).Info("successfully synced hostendpoint labels")
		}
		return
	}
	log.Errorf("too many retries when updating hostendpoint %q", nodeName)
}

// deleteHostendpoint deletes the auto hostendpoint associated with a node.
func (c *NodeController) deleteHostendpoint(nodeName string) {
	// On failure, we retry a certain number of times.
	for n := 0; n < 5; n++ {
		hep, err := c.calicoClient.HostEndpoints().Get(c.ctx, nodeName, options.GetOptions{})
		if err != nil {
			log.WithError(err).Warnf("failed to get host endpoint %q, retrying", nodeName)
			time.Sleep(retrySleepTime)
			continue
		}

		// If for some reason a hep exists with the node's name but it isn't
		// managed by us, log and return.
		if !isAutoHostendpoint(hep) {
			log.WithError(err).Warnf("failed to delete hostendpoint %q because it is not managed by kube-controllers", nodeName)
			return
		}

		_, err = c.calicoClient.HostEndpoints().Delete(c.ctx, nodeName, options.DeleteOptions{})
		if err != nil {
			switch err.(type) {
			// If the hostendpoint does not exist, we will retry anyways since
			// the Calico node may have been created then deleted immediately
			// afterwards such that the hostendpoint wasn't created yet.
			case errors.ErrorResourceDoesNotExist:
				log.Warnf("could not delete host endpoint for node %q because it does not exist, retrying", nodeName)
				continue
			default:
				log.Warnf("could not delete host endpoint for node %q: %v", nodeName, err)
				continue
			}
		}
		log.Infof("deleted hostendpoint for node %q", nodeName)
	}

	log.Errorf("too many retries when deleting hostendpoint %q", nodeName)
}
