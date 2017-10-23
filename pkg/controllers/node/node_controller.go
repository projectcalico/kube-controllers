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
	"reflect"
	"sync"

	"time"

	calicocache "github.com/projectcalico/k8s-policy/pkg/cache"
	"github.com/projectcalico/k8s-policy/pkg/controllers/controller"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/client"
	"github.com/projectcalico/libcalico-go/lib/errors"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	corecache "k8s.io/client-go/tools/cache"

	"fmt"
)

// This cache maps a calicoNodeName to its kubernetesNodeName.
type cache struct {
	sync.RWMutex
	nodes map[string]nodeData
}

type nodeData struct {
	Name string
}

// NodeController implements the Controller interface.  It is responsible for monitoring
// kubernetes nodes and responding to delete events by removing them from the Calico datastore.
// It keeps a cache of known calico nodes and their corresponding kubernetes nodes to
// accomplish this.
type NodeController struct {
	informer                corecache.Controller
	k8sResourceCache calicocache.ResourceCache
	nodeLookupCache   *cache
	calicoClient            *client.Client
	k8sClientset            *kubernetes.Clientset
}


// NewNodeController Constructor for NodeController
func NewNodeController(k8sClientset *kubernetes.Clientset, calicoClient *client.Client) controller.Controller {
	cacheArgs := calicocache.ResourceCacheArgs{
		ObjectType: reflect.TypeOf(""),
		ListFunc:   func() (map[string]interface{}, error) {
			// Get all nodes from the Calico datastore
			calicoNodes, err := calicoClient.Nodes().List(api.NodeMetadata{})
			if err != nil {
				return nil, err
			}

			// Store the relevant information in our cache.
			m := make(map[string]interface{})
			for _, calicoNode := range calicoNodes.Items {
				// find its kubernetes orchRef
				k8sNodeName := getK8sNodeRef(calicoNode)
				if err != nil {
					m[k8sNodeName] = calicoNode.Metadata.Name
				}
			}

			log.Debugf("Found %d nodes in Calico datastore:", len(m))
			return m, nil
		},
		ReconcilerConfig: calicocache.ReconcilerConfig{
			DisableMissingInDatastore: true,
			DisableMissingInCache: false,
			DisableUpdateOnChange: false,
		},
	}

	k8sResourceCache := calicocache.NewResourceCache(cacheArgs)
	nodeLookupCache := cache{nodes: make(map[string]nodeData)}

	// Create a Node watcher.
	listWatcher := corecache.NewListWatchFromClient(k8sClientset.Core().RESTClient(), "nodes", "", fields.Everything())

	// Bind the Calico cache to kubernetes cache with the help of an informer. This way we make sure that
	// whenever the kubernetes cache is updated, changes get reflected in the Calico cache as well.
	_, informer := corecache.NewIndexerInformer(listWatcher, &v1.Node{}, 0, corecache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			nodeName, err := extractNodeName(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to k8s node", nodeName)
			}
			log.Debugf("Got DELETE event for node: %s", nodeName)
			k8sResourceCache.Delete(nodeName)
		},

		AddFunc: func(obj interface{}) {
			log.Debug("Add func.")
			nodeName, err := extractNodeName(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to k8s node", nodeName)
			}
			// Use an empty value here because the only thing we care about is the kuberneteNodeName,
			// so there's no other relevant information we want to store in the cache besides the name (which
			// is unavailable at this time because the calicoNode is created after the k8sNode).
			k8sResourceCache.Set(nodeName, "")
		},
	}, corecache.Indexers{})

	return &NodeController{ informer, k8sResourceCache, &nodeLookupCache, calicoClient, k8sClientset}
}

// getK8sNodeRef is a helper method that searches an calicoNode for its kubernetes nodeRef.
func getK8sNodeRef(calicoNode api.Node) string {
	for _, orchRef := range calicoNode.Spec.OrchRefs {
		if orchRef.Orchestrator == "k8s" {
			return orchRef.NodeName
		}
	}
	return ""
}

// Run starts the node controller. It does start-of-day preparation
// and then launches worker threads.
func (c *NodeController) Run(threadiness int, reconcilerPeriod string, stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	// Let the workers stop when we are done
	workqueue := c.k8sResourceCache.GetQueue()
	defer workqueue.ShutDown()

	log.Info("Starting Node controller")

	// Load node cache. Retry when failed.
	log.Debug("Loading node cache at start of day")
	for err := c.populateCache(); err != nil; {
		log.WithError(err).Errorf("Failed to load Node cache, retrying in 5s")
		time.Sleep(5 * time.Second)
	}

	// Wait till k8s cache is synced
	go c.informer.Run(stopCh)
	log.Debug("Waiting to sync with Kubernetes API (Nodes)")
	for !c.informer.HasSynced() {
	}
	log.Debug("Finished syncing with Kubernetes API (Nodes)")

	// Start Calico cache.
	c.k8sResourceCache.Run(reconcilerPeriod)

	// Start a number of worker threads to read from the queue.
	for i := 0; i < threadiness; i++ {
		go c.runWorker()
	}
	log.Info("Node controller is now running")

	<-stopCh
	log.Info("Stopping Node controller")
}

func (c *NodeController) runWorker() {
	for c.processNextItem() {}
}

func (c *NodeController) processNextItem() bool {
	// Wait until there is a new item in the work queue.
	workqueue := c.k8sResourceCache.GetQueue()
	key, quit := workqueue.Get()
	if quit {
		return false
	}

	// Sync the object to the Calico datastore.
	if err := c.syncToCalico(key.(string)); err != nil {
		c.handleErr(err, key.(string))
	}

	// Indicate that we're done processing this key, allowing for safe parallel processing such that
	// two objects with the same key are never processed in parallel.
	workqueue.Done(key)
	return true
}

// populateCache fills the nodeLookupCache with initial data
// by querying the existing data stored in Calico.
func (c *NodeController) populateCache() error {
	nodes, err := c.calicoClient.Nodes().List(api.NodeMetadata{})
	if err != nil {
		return err
	}

	c.nodeLookupCache.Lock()
	for _, node := range nodes.Items {
		c.nodeLookupCache.nodes[getK8sNodeRef(node)] = nodeData{node.Metadata.Name}
	}
	log.Debugf("Current node lookup cache: %v", c.nodeLookupCache.nodes)
	c.nodeLookupCache.Unlock()
	return nil
}

// syncToCalico syncs the given update to the Calico datastore.
func (c *NodeController) syncToCalico(key string) error {
	// Check if it exists in the controller's cache.
	_, exists := c.k8sResourceCache.Get(key)
	if !exists {
		// The object no longer exists - delete from the datastore.
		log.Infof("Node %s no longer exists.", key)
		calicoNode, exists := c.nodeLookupCache.nodes[key]
		if !exists {
			log.Infof("Repopulating nodeLookupCache due to miss: %v", calicoNode)
			c.populateCache()
			calicoNode, exists = c.nodeLookupCache.nodes[key]
		}

		if exists {
			log.WithFields(log.Fields{
				"CalicoNodeName": calicoNode.Name,
				"K8sNodeName": key,
			}).Infof("Deleting node from Calico datastore.")
			err := c.calicoClient.Nodes().Delete(api.NodeMetadata{Name: calicoNode.Name})
			if _, ok := err.(errors.ErrorResourceDoesNotExist); !ok {
				// We hit an error other than "does not exist".
				return err
			}
		}

	}
	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *NodeController) handleErr(err error, key string) {
	workqueue := c.k8sResourceCache.GetQueue()
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		workqueue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if workqueue.NumRequeues(key) < 5 {
		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		log.WithError(err).Errorf("Error syncing Policy %v: %v", key, err)
		workqueue.AddRateLimited(key)
		return
	}
	workqueue.Forget(key)

	// Report to an external entity that, even after several retries, we could not successfully process this key
	uruntime.HandleError(err)
	log.WithError(err).Errorf("Dropping Policy %q out of the queue: %v", key, err)
}

func extractNodeName(k8sObj interface{}) (string, error) {
	node, ok := k8sObj.(*v1.Node)

	if !ok {
		tombstone, ok := k8sObj.(corecache.DeletedFinalStateUnknown)
		if !ok {
			return "", fmt.Errorf("couldn't get object from tombstone %+v", k8sObj)
		}
		node, ok = tombstone.Obj.(*v1.Node)
		if !ok {
			return "", fmt.Errorf("tombstone contained object that is not a Node %+v", k8sObj)
		}
	}
	return node.ObjectMeta.Name, nil
}