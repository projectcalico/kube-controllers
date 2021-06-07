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
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
)

const (
	RateLimitK8s          = "k8s"
	RateLimitCalicoCreate = "calico-create"
	RateLimitCalicoList   = "calico-list"
	RateLimitCalicoUpdate = "calico-update"
	RateLimitCalicoDelete = "calico-delete"
	nodeLabelAnnotation   = "projectcalico.org/kube-labels"
	hepCreatedLabelKey    = "projectcalico.org/created-by"
	hepCreatedLabelValue  = "calico-kube-controllers"
)

var (
	retrySleepTime = 100 * time.Millisecond
)

// NodeController implements the Controller interface.  It is responsible for monitoring
// kubernetes nodes and responding to delete events by removing them from the Calico datastore.
type NodeController struct {
	ctx context.Context

	// For syncing node objects from the k8s API.
	informer     cache.Controller
	indexer      cache.Indexer
	k8sClientset *kubernetes.Clientset

	// For accessing Calico datastore.
	calicoClient client.Interface
	dataFeed     *DataFeed

	// Sub-controllers
	ipamCtrl *ipamController
}

// NewNodeController Constructor for NodeController
func NewNodeController(ctx context.Context, k8sClientset *kubernetes.Clientset, calicoClient client.Interface, cfg config.NodeControllerConfig) controller.Controller {
	nc := &NodeController{
		ctx:          ctx,
		calicoClient: calicoClient,
		k8sClientset: k8sClientset,
		dataFeed:     NewDataFeed(calicoClient),
	}

	// Create a Node watcher.
	listWatcher := cache.NewListWatchFromClient(k8sClientset.CoreV1().RESTClient(), "nodes", "", fields.Everything())

	// Create the IPAM controller.
	nc.ipamCtrl = NewIPAMController(cfg, calicoClient, k8sClientset)
	nc.ipamCtrl.RegisterWith(nc.dataFeed)

	// Setup event handlers
	handlers := cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			nc.ipamCtrl.OnKubernetesNodeDeleted()
		}}

	// Create the Auto HostEndpoint sub-controller and register it to receive data.
	// We always launch this controller, even if auto-HEPs are disabled, since the controller
	// is responsible for cleaning up after itself in case it was previously enabled.
	autoHEPController := NewAuthoHEPController(cfg, calicoClient)
	autoHEPController.RegisterWith(nc.dataFeed)

	if cfg.SyncLabels {
		// Note that the configuration code has already handled disabling this if
		// we are in KDD mode.

		// Create Label-sync controller and register it to receive data.
		nodeLabelCtrl := NewNodeLabelController(calicoClient)
		nodeLabelCtrl.RegisterWith(nc.dataFeed)

		// Hook the node label controller into the node informer so we are notified
		// when Kubernetes node labels change.
		handlers.AddFunc = func(obj interface{}) { nodeLabelCtrl.OnKubernetesNodeUpdate(obj) }
		handlers.UpdateFunc = func(_, obj interface{}) { nodeLabelCtrl.OnKubernetesNodeUpdate(obj) }
	}

	// Informer handles managing the watch and signals us when nodes are deleted.
	// also syncs up labels between k8s/calico node objects
	nc.indexer, nc.informer = cache.NewIndexerInformer(listWatcher, &v1.Node{}, 0, handlers, cache.Indexers{})

	// Start the Calico data feed.
	nc.dataFeed.Start()

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
// and then launches worker threads.
func (c *NodeController) Run(stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	log.Info("Starting Node controller")

	// Register metrics.
	registerPrometheusMetrics()

	// Wait till k8s cache is synced
	go c.informer.Run(stopCh)
	log.Debug("Waiting to sync with Kubernetes API (Nodes)")
	for !c.informer.HasSynced() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Debug("Finished syncing with Kubernetes API (Nodes)")

	// We're in-sync. Start the sub-controllers.
	c.ipamCtrl.Start(stopCh)

	<-stopCh
	log.Info("Stopping Node controller")
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
