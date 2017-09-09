package controllers

import (
	calicocache "github.com/projectcalico/k8s-policy/pkg/cache"
	"github.com/projectcalico/libcalico-go/lib/client"
	log "github.com/sirupsen/logrus"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"time"
)

// Controller interface
type Controller interface {
	Run(int, string, chan struct{})
}

type BaseController struct {
	indexer        cache.Indexer
	informer       cache.Controller
	calicoObjCache calicocache.ResourceCache
	calicoClient   *client.Client
	k8sClientset   *kubernetes.Clientset
	controllerType string
	// Function to call in order to sync to Calico.
	datastoreWriteFunc func(string) error
}

// Run starts controller.Internally it starts syncing
// kubernetes and calico caches.
func (c *BaseController) Run(threadiness int, reconcilerPeriod string, stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	// Let the workers stop when we are done
	workqueue := c.calicoObjCache.GetQueue()
	defer workqueue.ShutDown()

	log.Infof("Starting %s controller", c.controllerType)

	// Start Calico cache. Cache gets loaded with objects
	// from datastore.
	c.calicoObjCache.Run(reconcilerPeriod)

	go c.informer.Run(stopCh)

	// Wait till k8s cache is synced
	for !c.informer.HasSynced() {
	}

	// Start a number of worker threads to read from the queue.
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	log.Infof("Stopping %s controller", c.controllerType)
}

func (c *BaseController) runWorker() {
	for c.processNextItem() {
	}
}

func (c *BaseController) processNextItem() bool {

	// Wait until there is a new item in the working queue
	workqueue := c.calicoObjCache.GetQueue()
	key, quit := workqueue.Get()
	if quit {
		return false
	}

	// Update calico datastore
	err := c.datastoreWriteFunc(key.(string))
	if err != nil {

		// Handle the error if something went wrong while updating calico datastore
		c.handleErr(err, key.(string))
	}

	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two nodes with the same key are never processed in
	// parallel.
	workqueue.Done(key)

	return true
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *BaseController) handleErr(err error, key string) {
	workqueue := c.calicoObjCache.GetQueue()
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		workqueue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if workqueue.NumRequeues(key) < 5 {
		log.Errorf("Error syncing %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		workqueue.AddRateLimited(key)
		return
	}

	workqueue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	uruntime.HandleError(err)
	log.Errorf("Dropping %q out of the queue: %v", key, err)
}
