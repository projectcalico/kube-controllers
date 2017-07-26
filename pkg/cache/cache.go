package cache

import (
	glog "github.com/Sirupsen/logrus"
	"github.com/kylelemons/godebug/pretty"
	"github.com/patrickmn/go-cache"
	calicoClient "github.com/projectcalico/libcalico-go/lib/client"
	k8sCache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"reflect"
	"time"
)

// ResourceCache stores calico representation of kubernetes objects.
// It tries to be always in sync with kubernetes cache with the
// help of reconcilor. Adds keys of internal objects to workqueue
// if any modifications are done to them. Controller will further
// sync these keys to calico ETCD datastore.
type ResourceCache interface {

	// Sets the key to the provided value, and generates an update
	// on the queue the value has changed.
	Set(key string, value interface{})

	// Gets the value associated with the given key.  Returns nil
	// if the key is not present.
	Get(key string) (interface{}, bool)

	// Sets the key to the provided value, but does not generate
	// and update on the queue ever.
	Prime(key string, value interface{})

	// Deletes the value identified by the given key from the cache, and
	// generates an update on the queue if a value was deleted.
	Delete(key string)

	// Lists the keys currently in the cache.
	ListKeys() []string

	// Starts the cache.
	Run(stopChan chan struct{}, reconcilerPeriod string)

	// Get workqueue
	GetQueue() workqueue.RateLimitingInterface
}

// ResourceCacheArgs struct passed to constructor of ResourceCache.
// Groups togather all the arguments to pass in single struct.
type ResourceCacheArgs struct {
	// ListFunc returns a list of objects.  Responsible for filtering any
	// objects which should not be monitored by the cache / controller.
	ListFunc func() ([]interface{}, error)

	// Takes an object and returns the key string which identifies it in the cache.
	// e.g. namespace.name
	KeyFunc func(obj interface{}) string

	// The channel on which modified keys will be sent, if given.
	OutChan chan string

	// Calico Client
	Client *calicoClient.Client

	// Type of object cache will hold
	// Set() API will verfiy the object type before storing it in cache
	ObjectType reflect.Type
}

// CalicoCache implements ResourceCache interface
// Cache only stores pointer to objects instead of actual objects
type calicoCache struct {
	threadSafeCache *cache.Cache                    // Underlaying threadsafe implementation of cache
	workqueue       workqueue.RateLimitingInterface // Workqueue
	calicoClient    *calicoClient.Client            // Clinet to Calico ETCD datastore
	k8sCacheIndexer k8sCache.Indexer                // K8S cache indexer used while priming of calicoCache.
	ListFunc        func() ([]interface{}, error)   // Function that returns a list of objects.
	KeyFunc         func(obj interface{}) string    // Function that returns key string which identifies it in the cache.
	ObjectType      reflect.Type                    // Type of object cache will hold
}

// NewResourceCache Constructor for ResourceCache.
// Requires calico client to prime the cache
// Cache only allows adding objects of type `objectType`
func NewResourceCache(args ResourceCacheArgs) ResourceCache {

	return &calicoCache{
		threadSafeCache: cache.New(cache.NoExpiration, cache.DefaultExpiration),
		workqueue:       workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		calicoClient:    args.Client,
		ListFunc:        args.ListFunc,
		KeyFunc:         args.KeyFunc,
		ObjectType:      args.ObjectType,
	}
}

func (c *calicoCache) Set(key string, newObj interface{}) {

	if reflect.TypeOf(newObj)!=c.ObjectType {
		glog.Fatalf("Wrong object type recieved to store in cache. Expected: %s, Found: %s", c.ObjectType, reflect.TypeOf(newObj))
	}

	if existingObj, found := c.threadSafeCache.Get(key); found {

		glog.Debugf("%#v found in cache. comparing..", existingObj)
		diff := pretty.Compare(existingObj, newObj)
		glog.Debugf("Diff: %s", diff)
		
		if len(diff) != 0 {
			glog.Debugf("%#v and %#v do not match.Updating it in calico cache.", newObj, existingObj)

			c.threadSafeCache.Set(key, newObj, cache.NoExpiration)
			c.workqueue.Add(key)
		}
	} else {
		glog.Debugf("%#v not found in calico cache. Adding it in calico cache", newObj)

		c.threadSafeCache.Set(key, newObj, cache.NoExpiration)
		c.workqueue.Add(key)
	}
}

func (c *calicoCache) Delete(key string) {

	glog.Debug("Deleting %s in calico", key)
	c.threadSafeCache.Delete(key)
	c.workqueue.Add(key)
}

func (c *calicoCache) Get(key string) (interface{}, bool) {

	obj, found := c.threadSafeCache.Get(key)
	if found {
		return obj, true
	}
	return nil, false
}

// Prime funtion adds object to threadSafeCache.
// Only difference with Set() call is it does not queue the key
// to workqueue. Stores pointer of value object in  cache
func (c *calicoCache) Prime(key string, value interface{}) {

	c.threadSafeCache.Set(key, &value, cache.NoExpiration)
}

// ListKeys returns list of calico cache keys in the form
// of array of strings. Cache stores name field of objects as key.
func (c *calicoCache) ListKeys() []string {

	cacheItems := c.threadSafeCache.Items()
	keys := make([]string, 0, len(cacheItems))
	for k := range cacheItems {
		keys = append(keys, k)
	}

	return keys
}

func (c *calicoCache) GetQueue() workqueue.RateLimitingInterface {
	return c.workqueue
}

// Load all the calico datastore objects at the begining of run
func (c *calicoCache) Run(stopChan chan struct{}, reconcilerPeriod string) {

	// Retry priming of cache if connection to ETCD datastore fails
	for err := c.primeCache(); err != nil; {
  		glog.WithError(err).Errorf("Failed to prime Calico cache, retrying")
	}

	go c.reconcile(stopChan, reconcilerPeriod)
}

// primeCache() function populates Calico Cache with only calico objects
// that are created by policy controller.
func (c *calicoCache) primeCache() error {

	etcdObjList, err := c.ListFunc()

	if err != nil {
		glog.Error(err)
		return err
	}

	for _, profile := range etcdObjList {
		calicoKey := c.KeyFunc(profile)
		c.Prime(calicoKey, profile)
	}

	return nil
}

// reconcile() function runs every `reconcilerPeriod` and brings ETCD datastore
// in sync with Calico cache. This is to correct any manual changes done by 
// user in Calico ETCD.
func (c *calicoCache) reconcile(stopChan chan struct{}, reconcilerPeriod string) {

	duration, err := time.ParseDuration(reconcilerPeriod)
	if (err!=nil){
		glog.Fatalf("Invalid time duration format %s for reconciler. Some correct examples, 5m, 30s, 2m30s etc. Reconciler will not sync any objects.",reconcilerPeriod)
		return
	}

	// If user has set duration to 0 then disable the reconciler job.
	if(duration.Nanoseconds() == 0){
		glog.Infof("Reconciler period set to %d. Disabling reconciler.", duration.Nanoseconds())
		return
	}

	ticker := time.NewTicker(duration)
	go func() {
		for t := range ticker.C {
			glog.Info("Performing a periodic resync at ", t)
			c.performDatastoreSync()
			glog.Info("Periodic resync done")
		}
	}()

	<-stopChan
	ticker.Stop()
}

func (c *calicoCache) performDatastoreSync() {

	// Get all objects created by policy controller from ETCD datastore.
	etcdObjList, err := c.ListFunc()
	if err != nil {
		glog.Error(err)
		return
	}

	// Build a map of existing objects on ETCD datastore, plus a map including all keys that exist.
	allKeys := map[string]bool{}
	etcd := map[string]interface{}{}
	for _, etcdObj := range etcdObjList {

		k := c.KeyFunc(etcdObj)
		// Storing list of ETDC objects in map of [string]interface{} for easy lookup
		etcd[k] = etcdObj
		allKeys[k] = true
	}

	// Now, send through all existing keys across both the Kubernetes API, and
	// etcd so we can sync them if needed.
	for _, k := range c.ListKeys() {
		allKeys[k] = true
	}

	glog.Debugf("Reconcilor working on %d keys in total", len(allKeys))

	for k := range allKeys {

		cachedObj, exists := c.Get(k)
		if !exists {
			// Does not exists on kubernetes. Delete on ETCD as well.
			c.workqueue.Add(k)
			continue
		}

		if _, exists := etcd[k]; !exists {
			// Has got deleted on ETCD datastore. recreate it.
			c.workqueue.Add(k)
			continue
		}

		diff := pretty.Compare(etcd[k], cachedObj)
		glog.Debugf("Diff: %s", diff)

		if len(diff) != 0 {
			// ETCD copy of object is deviated from Calico cache.
			c.workqueue.Add(k)
			continue
		}
	}
}
