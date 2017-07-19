package main

import (
	"flag"
	"fmt"
	"time"

	calicocache "github.com/girishshilamkar/k8s-policy-go/pkg/cache"
	//"github.com/projectcalico/node-controller/pkg/config"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/client"
	glog "github.com/Sirupsen/logrus"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"github.com/kylelemons/godebug/pretty"

)

func main() {

	var kubeconfig string
	var master string

	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	fmt.Println("kubeconfig:",kubeconfig)
	flag.StringVar(&master, "master", "", "master url")
	flag.Parse()

	// creates the connection
	k8sconfig, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		glog.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		panic(err.Error())
	}
	
	// create the watcher
	nodeListWatcher := cache.NewListWatchFromClient(clientset.Core().RESTClient(), "namespaces", "", fields.Everything())

	// create the workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	cconfig, err := client.LoadClientConfig("")
	if err != nil {
		panic(err)
	}
	cclient, err := client.New(*cconfig)
	if err != nil {
		panic(err)
	}

	// Create a dummy profile in ETCD datastore which will be used to test calico Cache priming
	profiles := cclient.Profiles()

	_, err = profiles.Create(&api.Profile{
		Metadata: api.ProfileMetadata{
			Labels:       map[string]string{"tier": "uat"},
			Name:         "newProfile",
			Tags:	    	[]string{"vinshin"},
		},
		Spec: api.ProfileSpec{
			IngressRules : []api.Rule{},
			EgressRules  : []api.Rule{},
		},
	})

	if err != nil {
		glog.Error("Profile newProfile already exists on calico", err)
	}

	ccache := calicocache.NewCache(queue,cclient,"Profile")
	
	stop := make(chan struct{})
	defer close(stop)
	
	
	
	
	
	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the node key is added to the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Node than the version which was responsible for triggering the update.
	indexer, informer := cache.NewIndexerInformer(nodeListWatcher, &v1.Namespace{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			
			key, err := cache.MetaNamespaceKeyFunc(obj)
			glog.Infof("Got Add for namespace %s\n", key)
			
			if err == nil {
				ccache.Set(key,obj)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			glog.Infof("Got update for namespace Old Ob: %#v\n", old)
			glog.Info("\n------\n")
			glog.Infof("New object %#v\n",new)
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				ccache.Set(key,new)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			glog.Infof("Got delete for namespace %s\n", key)
			if err == nil {
				ccache.Delete(key)
			}
		},
	}, cache.Indexers{})

	ccache.SetIndexer(indexer)
	controller := NewController(queue, ccache, cclient, indexer, informer)

	// Now let's start the Kubernetes controller
	fmt.Println("Starting controller")
	
	go controller.Run(5, stop)

	
	

	// Wait forever.
	select {}
}

type Controller struct {
	indexer        cache.Indexer
	queue          workqueue.RateLimitingInterface
	informer       cache.Controller
	calicoObjCache calicocache.ResourceCache
	calicoClient   *client.Client
}

type QueueUpdate struct {
	Key   string
	Force bool
}

func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	glog.Info("Starting Node controller")

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		uruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}
	c.calicoObjCache.Run(stopCh)
	for !c.calicoObjCache.HasSynced(){}

	go c.periodicReconciler()
	// Start a number of worker threads to read from the queue.
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	glog.Info("Stopping Node controller")
}

func (c *Controller) periodicReconciler() {
	fmt.Println("Starting periodic resync thread")
	for {
		fmt.Println("Performing a periodic resync")
		c.performDatastoreSync()
		fmt.Println("Periodic resync done")
		time.Sleep(20 * time.Second)
	}
}

func (c *Controller) performDatastoreSync() {
	// First, let's bring the Calico cache in-sync with what's actually in etcd.
	calicoProfiles, err := c.calicoClient.Profiles().List(api.ProfileMetadata{})
	if err != nil {
		panic(err)
	}

	// Build a map of existing objects on ETCD datastore, plus a map including all keys that exist.
	allKeys := map[string]bool{}
	etcd := map[string]interface{}{}
	for _, profile := range calicoProfiles.Items {
		k := profile.Metadata.Name
		etcd[k] = profile
		allKeys[k] = true
	}


	// Now, send through all existing keys across both the Kubernetes API, and
	// etcd so we can sync them if needed.
	for _, k := range c.calicoObjCache.ListKeys() {
		allKeys[k] = true
	}
	fmt.Printf("Refreshing %d keys in total", len(allKeys))
	for k, _ := range allKeys {

		cachedObj,exists := c.calicoObjCache.Get(k)

		if(!exists){
			// Does not exists on kubernetes. Delete on ETCD as well.
			c.queue.Add(k)
			continue
		}
		if(etcd[k] == nil){
			// Has got deleted on ETCD datastore. recreate it.
			c.queue.Add(k)
			continue
		}

		diff := pretty.Compare(cachedObj,etcd[k])
		glog.Info("Diff:",diff)

		//reflect.DeepEqual() is not able to correctly compare the profile objects
		if len(diff)!=0 {
			// ETCD copy of object is deviated from Calico cache.
			c.queue.Add(k)
			continue
		} else {
			// ETCD and calico cache versions match. nothing to do.
		}

	}
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	upd, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two nodes with the same key are never processed in
	// parallel.
	defer c.queue.Done(upd)

	// Invoke the method containing the business logic
	err := c.syncToCalico(upd.(string))

	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, upd)
	return true
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, upd interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(upd)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(upd) < 5 {
		glog.Infof("Error syncing node %v: %v", upd, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(upd)
		return
	}

	c.queue.Forget(upd)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	uruntime.HandleError(err)
	glog.Infof("Dropping node %q out of the queue: %v", upd, err)
}

// syncToCalico syncs the given update to Calico's etcd, as well as the in-memory cache
// of Calico objects.
func (c *Controller) syncToCalico(key string) error {
	
	// Check if it exists in our cache.
	obj,exists := c.calicoObjCache.Get(key)

	if !exists {
		glog.Infof("Profile %s does not exist anymore\n", key)
		glog.Infof("Deleting Profile on ETCD %s\n", key)
		
		result := c.calicoClient.Profiles().Delete(api.ProfileMetadata{
			Name: key,
		})

		profileList, _ := c.calicoClient.Profiles().List(api.ProfileMetadata{})
		glog.Info("Total profiles in calico ETCD now:", len(profileList.Items))

		return result
			
	} else {

		// Only apply an update if it's:
		// - Not in the cache
		// - Different from what's in the cache.
		// - This is a forced udpate.
		var p api.Profile
		p = obj.(api.Profile)
		glog.Info("Applying changes for ",key)
		_, err := c.calicoClient.Profiles().Apply(&p)
		profileList, err := c.calicoClient.Profiles().List(api.ProfileMetadata{})
		glog.Info("Total profiles in calico ETCD now:", len(profileList.Items))

		return err
	}
	return nil
}

func NewController(queue workqueue.RateLimitingInterface, calicoCache calicocache.ResourceCache, cclient *client.Client, indexer cache.Indexer, informer cache.Controller) *Controller {
	

	return &Controller{
		informer:       informer,
		indexer:        indexer,
		queue:          queue,
		calicoObjCache: calicoCache,
		calicoClient:   cclient,
	}
}