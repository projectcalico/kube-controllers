package controllers

import (
	calicocache "github.com/projectcalico/k8s-policy/pkg/cache"
	"github.com/projectcalico/k8s-policy/pkg/converter"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/client"
	"github.com/projectcalico/libcalico-go/lib/errors"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/cache"
	"reflect"
	"strings"
)

// NamespaceController Implements Controller interface
// Responsible for monitoring kubernetes namespaces and
// syncing them to Calico datastore.
type NamespaceController struct {
	BaseController
}

// NewNamespaceController Constructor for NamespaceController
func NewNamespaceController(k8sClientset *kubernetes.Clientset, calicoClient *client.Client) Controller {

	namespaceConverter := converter.NewNamespaceConverter()

	// Function returns map of profile_name:object stored by policy controller
	// on ETCD datastore. Indentifies controller writen objects by
	// their naming convention.
	listFunc := func() (map[string]interface{}, error) {
		filteredProfiles := make(map[string]interface{})

		// Get all profile objects from ETCD datastore
		calicoProfiles, err := calicoClient.Profiles().List(api.ProfileMetadata{})
		if err != nil {
			return filteredProfiles, err
		}

		// Filter out only objects that are written by policy controller
		for _, profile := range calicoProfiles.Items {

			profileName := profile.Metadata.Name
			if strings.HasPrefix(profileName, converter.ProfileNameFormat) {
				key := namespaceConverter.GetKey(profile)
				filteredProfiles[key] = profile
			}
		}
		log.Debugf("Found %d profiles in calico ETCD:", len(filteredProfiles))
		return filteredProfiles, nil
	}

	cacheArgs := calicocache.ResourceCacheArgs{
		ListFunc:   listFunc,
		ObjectType: reflect.TypeOf(api.Profile{}), // Restrict cache to store calico profiles only.
	}

	ccache := calicocache.NewResourceCache(cacheArgs)

	// create the watcher
	listWatcher := cache.NewListWatchFromClient(k8sClientset.Core().RESTClient(), "namespaces", "", fields.Everything())

	// Bind the calico cache to kubernetes cache with the help of an informer. This way we make sure that
	// whenever the kubernetes cache is updated, changes get reflected in calico cache as well.
	indexer, informer := cache.NewIndexerInformer(listWatcher, &v1.Namespace{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			log.Debugf("Got ADD event for namespace: %s\n", key)

			if err != nil {
				log.WithError(err).Error("Failed to generate key")
				return
			}

			profile, err := namespaceConverter.Convert(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to calico profile.", obj)
				return
			}

			calicoKey := namespaceConverter.GetKey(profile)
			// Add key:*profile in calicoCache
			ccache.Set(calicoKey, profile)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)

			log.Debugf("Got UPDATE event for namespace: %s\n", key)
			log.Debugf("Old object: %#v\n", oldObj)
			log.Debugf("New object: %#v\n", newObj)

			if err != nil {
				log.WithError(err).Error("Failed to generate key")
				return
			}

			if newObj.(*v1.Namespace).Status.Phase == "Terminating" {

				// If object status is updated to "Terminating", object
				// is getting deleted. Ignore this event. When deletion
				// completes another DELETE event will be raised.
				// Let DeleteFunc handle that.
				log.Debugf("Namespace %s is getting deleted.", newObj.(*v1.Namespace).ObjectMeta.GetName())
				return
			}
			profile, err := namespaceConverter.Convert(newObj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to calico profile.", newObj)
				return
			}

			calicoKey := namespaceConverter.GetKey(profile)

			// Add key:profile in calicoCache
			ccache.Set(calicoKey, profile)
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			log.Debugf("Got DELETE event for namespace: %s\n", key)

			if err != nil {
				log.WithError(err).Error("Failed to generate key")
				return
			}

			profile, err := namespaceConverter.Convert(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to calico profile.", obj)
				return
			}

			calicoKey := namespaceConverter.GetKey(profile)
			ccache.Delete(calicoKey)
		},
	}, cache.Indexers{})

	// function syncs the given update to Calico's datastore,
	datastoreWriteFunc := func(key string) error {
		// Check if it exists in our cache.
		obj, exists := ccache.Get(key)

		if !exists {

			log.Debugf("namespace %s does not exist anymore on kubernetes\n", key)
			log.Infof("Deleting namespace %s on ETCD \n", key)

			err := calicoClient.Profiles().Delete(api.ProfileMetadata{
				Name: key,
			})

			// Let Delete() operation be idompotent. Ignore the error while deletion if
			// object does not exists on ETCD already.
			if err != nil {
				if _, ok := err.(errors.ErrorResourceDoesNotExist); !ok {
					log.WithError(err).Errorf("Got error while deleting %s in datastore.", key)
					return err
				}
			}
			return nil
		} else {

			var p api.Profile
			p = obj.(api.Profile)
			log.Infof("Applying namespace %s on ETCD \n", key)
			_, err := calicoClient.Profiles().Apply(&p)

			return err
		}
	}

	return &NamespaceController{
		BaseController{
			indexer:            indexer,
			informer:           informer,
			calicoObjCache:     ccache,
			calicoClient:       calicoClient,
			k8sClientset:       k8sClientset,
			controllerType:     "NamespaceController",
			datastoreWriteFunc: datastoreWriteFunc,
		},
	}
}
