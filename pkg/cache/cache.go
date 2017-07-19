package cache

import (
	"fmt"
	"github.com/patrickmn/go-cache"
	"k8s.io/client-go/util/workqueue"
	//"reflect"
	glog "github.com/Sirupsen/logrus"
	"k8s.io/client-go/pkg/api/v1"
	k8sCache "k8s.io/client-go/tools/cache"
	"github.com/projectcalico/libcalico-go/lib/api"
	calicoClient "github.com/projectcalico/libcalico-go/lib/client"
	"github.com/projectcalico/libcalico-go/lib/api/unversioned"
	"github.com/kylelemons/godebug/pretty"
)

type ResourceCache interface {
	// Sets the key to the provided value, and generates an update
	// on the queue the value has changed.
	Set(key string, value interface{})

	// Gets the value associated with the given key.  Returns nil
	// if the key is not present.
	Get(key string) (interface{}, bool)

	// Sets the key to the provided value, but does not generate
	// and update on the queue ever.
	// Priming should happen only at the begining and only run 
	// method should have access to it. Hence a private method.
	prime(key string, value interface{})

	// Deletes the value identified by the given key from the cache, and
	// generates an update on the queue if a value was deleted.
	Delete(key string)

	// Lists the keys currently in the cache.
	ListKeys() []string

	// Returns true when the cache has synced with the datastore,
	// false until that point.
	HasSynced() bool

	// Starts the cache.
	Run(stopChan chan struct{})

	// Sets kubernetes cache indexer
	SetIndexer(indexer k8sCache.Indexer)
}

// CalicoCache implements ResourceCache interface
type CalicoCache struct {
	threadSafeCache *cache.Cache
	workqueue       workqueue.RateLimitingInterface
	calicoClient    *calicoClient.Client
	synced          bool 							// Flag set to true when cache is synced with etcd datastore
	objType			string							// Type of object CalicoCache will hold. E.g. calico profiles, policies etc.
	k8sCacheIndexer k8sCache.Indexer				// K8S cache indexer used while priming of calicoCache.
}

func NewCache(queue workqueue.RateLimitingInterface, client *calicoClient.Client, objectType string) ResourceCache {
	
	return &CalicoCache{
		threadSafeCache: cache.New(cache.NoExpiration, cache.DefaultExpiration),
		workqueue:       queue,
		calicoClient:    client,
		synced:			 false,
		objType:		 objectType,
	}
}

func (c *CalicoCache) Set(key string, obj interface{}) {

	
	// Converting obj to calico representation
	profile := api.Profile{
		TypeMetadata: unversioned.TypeMetadata{
			Kind:  "profile",     
			APIVersion: "v1",
		}, 
		Metadata: api.ProfileMetadata{
			Name: obj.(*v1.Namespace).Name,
		},
	}
	
	if existingProfile, found := c.threadSafeCache.Get(key); found {

		glog.Infof("%s found in cache. comparing", existingProfile)
		diff := pretty.Compare(profile,existingProfile)
		glog.Info("Diff:",diff)

		//reflect.DeepEqual() is not able to correctly compare the profile objects
		if len(diff)!=0 {
			glog.Infof("%s and %s do not match.", profile, existingProfile)
			
			c.threadSafeCache.Set(key, profile, cache.NoExpiration)
			c.workqueue.Add(key)

		} else {
			// Nothing to do. Object already exists in cache
		}
	} else {
		glog.Infof("%s not found in cache. Adding in cache", obj)
	
		c.threadSafeCache.Set(key, profile, cache.NoExpiration)
		c.workqueue.Add(key)
	}
}

func (c *CalicoCache) Delete(key string) {

	c.threadSafeCache.Delete(key)
	c.workqueue.Add(key)
}

func (c *CalicoCache) Get(key string) (interface{}, bool) {
	obj, found := c.threadSafeCache.Get(key)
	if found {
		return obj, true
	}
	return nil, false
}
func (c *CalicoCache) prime(key string, value interface{}) {

	c.threadSafeCache.Set(key, value, cache.NoExpiration)
}
func (c *CalicoCache) ListKeys() []string {
	cacheItems := c.threadSafeCache.Items()
	keys := make([]string, 0, len(cacheItems))
	for k, v := range cacheItems {
		fmt.Println(k, v)
		keys = append(keys, k)
	}

	return keys

}

func (c *CalicoCache) SetIndexer(indexer k8sCache.Indexer) {

	c.k8sCacheIndexer = indexer
}
func (c *CalicoCache) HasSynced() bool {
	return c.synced
}

// Load all the calico datastore objects at the begining of run
func (c *CalicoCache) Run(stopChan chan struct{}) {

	c.primeCache()
	
}

// Function shuld populate Calico Cache with only calico objects
// for which corrosponding kubernetes objects exists.
// This is because calico cache is downstream object store for 
// kubernetes cache
func (c *CalicoCache) primeCache() {

	if (c.k8sCacheIndexer==nil){
		glog.Error("No k8s indexer set. Exiting priming.")
		return
	}
	profiles := c.calicoClient.Profiles()
	profileList, err := profiles.List(api.ProfileMetadata{})
	glog.Infof("Found profiles in calico ETCD:", len(profileList.Items))
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, profile := range profileList.Items {
		_, exists, _ := c.k8sCacheIndexer.GetByKey(profile.Metadata.Name)
		if (exists){
			glog.Infof("%s exists in kubernetes. Adding it to calico cache.",profile.Metadata.Name)
			c.prime(profile.Metadata.Name, profile)
		}else{
			glog.Infof("%s is not present on kubernetes.Not priming.",profile.Metadata.Name)
		}
	}
	
	c.synced = true
}

func handle(queue workqueue.RateLimitingInterface) error {

	for {
		obj, _ := queue.Get()

		fmt.Println("Found item in queue")
		fmt.Println(obj)

	}
	return nil

}

