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
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"reflect"
	"strings"
)

// PolicyController Implements Controller interface
// Responsible for monitoring kubernetes network policies and
// syncing them to Calico datastore.
type PolicyController struct {
	BaseController
}

// NewPolicyController Constructor for PolicyController
func NewPolicyController(k8sClientset *kubernetes.Clientset, calicoClient *client.Client) Controller {
	policyConverter := converter.NewPolicyConverter()

	// Function returns map of policyName:policy stored by policy controller
	// in datastore.
	listFunc := func() (map[string]interface{}, error) {
		npMap := make(map[string]interface{})

		// Get all policies from datastore
		calicoPolicies, err := calicoClient.Policies().List(api.PolicyMetadata{})
		if err != nil {
			return npMap, err
		}

		// Filter out only objects that are written by policy controller
		for _, policy := range calicoPolicies.Items {
			policyName := policyConverter.GetKey(policy)
			if strings.HasPrefix(policyName, "knp.default.") {
				npMap[policyName] = policy
			}
		}

		log.Debugf("Found %d policies in calico datastore:", len(npMap))
		return npMap, nil
	}

	cacheArgs := calicocache.ResourceCacheArgs{
		ListFunc:   listFunc,
		ObjectType: reflect.TypeOf(api.Policy{}),
	}

	ccache := calicocache.NewResourceCache(cacheArgs)

	// create the watcher
	listWatcher := cache.NewListWatchFromClient(k8sClientset.Extensions().RESTClient(), "networkpolicies", "", fields.Everything())

	// Bind the calico cache to kubernetes cache with the help of an informer. This way we make sure that
	// whenever the kubernetes cache is updated, changes get reflected in calico cache as well.
	indexer, informer := cache.NewIndexerInformer(listWatcher, &v1beta1.NetworkPolicy{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Debugf("Got ADD event for network policy: %#v\n", obj)

			policy, err := policyConverter.Convert(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to calico network policy.", obj)
				return
			}

			calicoKey := policyConverter.GetKey(policy)

			// Add policyName:policy in calicoCache
			ccache.Set(calicoKey, policy)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			log.Debugf("Got UPDATE event for network policy: %#v\n", oldObj)
			log.Debugf("Old object: %#v\n", oldObj)
			log.Debugf("New object: %#v\n", newObj)

			policy, err := policyConverter.Convert(newObj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to calico network policy.", newObj)
				return
			}

			calicoKey := policyConverter.GetKey(policy)

			// Add policyName:policy in calicoCache
			ccache.Set(calicoKey, policy)
		},
		DeleteFunc: func(obj interface{}) {
			log.Debugf("Got DELETE event for namespace: %#v\n", obj)

			policy, err := policyConverter.Convert(obj)
			if err != nil {
				log.WithError(err).Errorf("Error while converting %#v to calico network policy.", obj)
				return
			}

			calicoKey := policyConverter.GetKey(policy)

			ccache.Delete(calicoKey)
		},
	}, cache.Indexers{})

	// function syncs the given update to Calico's datastore,
	datastoreWriteFunc := func(key string) error {

		// Check if it exists in our cache.
		obj, exists := ccache.Get(key)

		if !exists {
			log.Debugf("Network policy %s does not exist anymore on kubernetes\n", key)
			log.Debugf("Deleting policy %s on datastore \n", key)

			err := calicoClient.Policies().Delete(api.PolicyMetadata{
				Name: key,
			})

			// Let Delete() operation be idompotent. Ignore the error while deletion if
			// object does not exists on datastore already.
			if err != nil {
				if _, ok := err.(errors.ErrorResourceDoesNotExist); !ok {
					log.WithError(err).Errorf("Got error while deleting %s in datastore.", key)
					return err
				}
			}
			return nil
		} else {
			p := obj.(api.Policy)
			log.Infof("Applying network policy %s on datastore \n", key)
			_, err := calicoClient.Policies().Apply(&p)
			return err
		}
	}

	return &PolicyController{
		BaseController{
			indexer:            indexer,
			informer:           informer,
			calicoObjCache:     ccache,
			calicoClient:       calicoClient,
			k8sClientset:       k8sClientset,
			controllerType:     "PolicyController",
			datastoreWriteFunc: datastoreWriteFunc,
		},
	}
}
