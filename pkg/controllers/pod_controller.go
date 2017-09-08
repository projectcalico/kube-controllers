package controllers

import (
	calicocache "github.com/projectcalico/k8s-policy/pkg/cache"
	"github.com/projectcalico/k8s-policy/pkg/converter"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/client"
	"github.com/projectcalico/libcalico-go/lib/errors"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/cache"
	"reflect"
)

// PodController Implements Controller interface
// Responsible for monitoring kubernetes pods and
// updating pod labels to calico ETCD if changed.
type PodController struct {
	BaseController
	endpointCache map[string]api.WorkloadEndpoint
}

// NewPodController Constructor for PodController
func NewPodController(k8sClientset *kubernetes.Clientset, calicoClient *client.Client) Controller {
	podConverter := converter.NewPodConverter()

	// Function returns map of key->workloadEndpoint stored by policy controller
	// on ETCD datastore. Indentifies controller writen objects by
	// their naming convention.
	listFunc := func() (map[string]interface{}, error) {

		wepMap := make(map[string]interface{})

		// Get all workloadEndpoints for kubernetes orchestrator from ETCD datastore
		workloadEndpoints, err := calicoClient.WorkloadEndpoints().List(api.WorkloadEndpointMetadata{
			Orchestrator: "k8s",
		})
		if err != nil {
			return wepMap, err
		}

		for _, endpoint := range workloadEndpoints.Items {

			key := podConverter.GetKey(endpoint)
			wepMap[key] = endpoint.Metadata.Labels
		}
		log.Debugf("Found %d pods in calico ETCD:", len(wepMap))
		return wepMap, nil
	}

	cacheArgs := calicocache.ResourceCacheArgs{
		ListFunc:   listFunc,
		ObjectType: reflect.TypeOf(map[string]string{}), // Restrict cache to store pod labels only.
	}

	labelCache := calicocache.NewResourceCache(cacheArgs)
	endpointCache := map[string]api.WorkloadEndpoint{}

	// create the watcher
	listWatcher := cache.NewListWatchFromClient(k8sClientset.Core().RESTClient(), "pods", "", fields.Everything())

	// Bind the calico cache to kubernetes cache with the help of an informer. This way we make sure that
	// whenever the kubernetes cache is updated, changes get reflected in calico cache as well.
	indexer, informer := cache.NewIndexerInformer(listWatcher, &v1.Pod{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err != nil {
				log.WithError(err).Error("Failed to generate key")
				return
			}

			log.Debugf("Got ADD event for pod: %s\n", key)

			endpoint, err := podConverter.Convert(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to endpoint.", obj)
				return
			}

			workloadEndpoint := endpoint.(api.WorkloadEndpoint)
			calicoKey := podConverter.GetKey(workloadEndpoint)

			// Add workloadID:Labels in labelCache
			labelCache.Prime(calicoKey, workloadEndpoint.Metadata.Labels)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)

			if err != nil {
				log.WithError(err).Error("Failed to generate key")
				return
			}

			log.Debugf("Got UPDATE event for pod: %s\n", key)
			log.Debugf("Old object: %#v\n", oldObj)
			log.Debugf("New object: %#v\n", newObj)

			endpoint, err := podConverter.Convert(newObj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to endpoint.", newObj)
				return
			}

			workloadEndpoint := endpoint.(api.WorkloadEndpoint)
			calicoKey := podConverter.GetKey(workloadEndpoint)

			// Add workloadID:Labels in labelCache
			labelCache.Set(calicoKey, workloadEndpoint.Metadata.Labels)
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			log.Debugf("Got DELETE event for pod: %s\n", key)

			if err != nil {
				log.WithError(err).Error("Failed to generate key")
				return
			}

			endpoint, err := podConverter.Convert(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to endpoint.", obj)
				return
			}

			workloadEndpoint := endpoint.(api.WorkloadEndpoint)
			calicoKey := podConverter.GetKey(workloadEndpoint)
			labelCache.Clean(calicoKey)
			delete(endpointCache, calicoKey)
		},
	}, cache.Indexers{})

	// function syncs the given update to Calico's datastore,
	datastoreWriteFunc := func(key string) error {
		// Check if it exists in our cache.
		if labels, exists := labelCache.Get(key); exists {

			// Get workloadEndpoint from cache
			endpoint, exists := endpointCache[key]

			if !exists {
				log.Errorf("no endpoint for %s in cache, loading", key)

				// Load endpoint cache. Retry when failed.
				err := populateEndpointCache()
				if err != nil {
					log.WithError(err).Error("failed to load workload endpoint cache")
					return err
				}

				endpoint, exists = endpointCache[key]
				if !exists {

					// No endpoint in etcd - this means the pod hasn't been
					// created by the CNI plugin yet. Just wait until it has been.
					// This can only be hit when labels for a pod change before
					//  the pod has been deployed, so should be pretty uncommon.
					log.Infof("Pod %s hasn't been created by the CNI plugin yet.", key)
					return nil
				}
			}

			oldLabels := endpoint.Metadata.Labels
			newLabels := labels.(map[string]string)

			if !reflect.DeepEqual(oldLabels, newLabels) {

				// Update the labels on endpoint
				endpoint.Metadata.Labels = newLabels

				log.Infof("Updating endpoint %s with labels %#v on ETCD \n", key, newLabels)
				_, err := calicoClient.WorkloadEndpoints().Update(&endpoint)

				if err != nil {
					log.WithError(err).Errorf("failed to update workload endpoint %s", key)
					return err
				} else {

					// Update endpoint cache as well with modified endpoint.
					endpointCache[key] = endpoint
				}

				if _, ok := err.(errors.ErrorResourceDoesNotExist); ok {

					// Endpoint not yet created by CNI plugin.
					err = nil
				}

				return err
			}
		}

		return nil
	}

	return &PodController{
		BaseController: BaseController{
			indexer:            indexer,
			informer:           informer,
			calicoObjCache:     labelCache,
			calicoClient:       calicoClient,
			k8sClientset:       k8sClientset,
			controllerType:     "PodController",
			datastoreWriteFunc: datastoreWriteFunc,
		},
		endpointCache: endpointCache,
	}
}

// Run starts controller.Internally it starts syncing
// kubernetes and calico caches.
func (c *PodController) Run(threadiness int, reconcilerPeriod string, stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	// Let the workers stop when we are done
	workqueue := c.BaseController.calicoObjCache.GetQueue()
	defer workqueue.ShutDown()

	log.Info("Starting pod controller")

	// Start Calico cache. Cache gets loaded with objects
	// from Calico datastore.
	c.BaseController.calicoObjCache.Run(reconcilerPeriod)

	// Load endpoint cache. Retry when failed.
	for err := c.populateEndpointCache(); err != nil; {
		log.WithError(err).Errorf("Failed to load workload endpoint cache, retrying")
	}

	go c.informer.Run(stopCh)

	// Wait till k8s cache is synced
	for !c.informer.HasSynced() {
	}

	// Start a number of worker threads to read from the queue.
	for i := 0; i < threadiness; i++ {
		go c.runWorker()
	}

	<-stopCh
	log.Info("Stopping Pod controller")
}

// populateEndpointCache() Loads map of workload endpoint objects from ETCD datastore
func (c *PodController) populateEndpointCache() error {
	// List all workload endpoints for kubernetes orchestrator
	workloadEndpointList, err := c.calicoClient.WorkloadEndpoints().List(api.WorkloadEndpointMetadata{
		Orchestrator: "k8s",
	})
	if err != nil {
		return err
	}
	for _, endpoint := range workloadEndpointList.Items {
		c.endpointCache[endpoint.Metadata.Workload] = endpoint
	}

	return nil
}
