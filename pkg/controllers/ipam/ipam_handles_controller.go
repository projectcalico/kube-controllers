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

package ipam

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	cerrors "github.com/projectcalico/libcalico-go/lib/errors"
)

const (
	RateLimitSyncHandles = "sync-handles"
)

type IPAMController struct {
	ctx          context.Context
	informer     cache.Controller
	indexer      cache.Indexer
	calicoClient client.Interface
	rl           workqueue.RateLimiter
	schedule     chan interface{}
}

func NewController(ctx context.Context, calicoClient client.Interface) controller.Controller {
	nc := &IPAMController{
		ctx:          ctx,
		calicoClient: calicoClient,
		rl:           workqueue.DefaultControllerRateLimiter(),
	}

	// Channel used to kick the controller for resyncs.
	nc.schedule = make(chan interface{}, 1)

	// Create a listwatcher.
	crdClient := extractCustomResourceClient(calicoClient)
	listWatcher := cache.NewListWatchFromClient(crdClient, "ipamhandles", "", fields.Everything())

	// Setup event handlers
	handlers := cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Kick to start a resync.
			kick(nc.schedule)
		},
	}

	nc.indexer, nc.informer = cache.NewIndexerInformer(listWatcher, &v3.IPAMHandle{}, 0, handlers, cache.Indexers{})
	return nc
}

// extractCustomResourceClient uses the provided Calico client and returns
// a rest client appropriate for interacting with crd.projectcalico.org API resources.
func extractCustomResourceClient(c client.Interface) *rest.RESTClient {
	// Get the backend client.
	type accessor interface {
		Backend() bapi.Client
	}
	bc := c.(accessor).Backend()

	// Get the CRD client.
	return bc.(*k8s.KubeClient).GetCRDClient()
}

func (c *IPAMController) Run(stopCh chan struct{}) {
	defer uruntime.HandleCrash()

	log.Info("Starting IPAM handle garbage collector")

	// Wait till k8s cache is synced
	go c.informer.Run(stopCh)
	log.Debug("Waiting to sync with Kubernetes API (Nodes)")
	for !c.informer.HasSynced() {
		time.Sleep(100 * time.Millisecond)
	}
	log.Debug("Finished syncing with Kubernetes API (Nodes)")

	// Start Calico cache.
	go c.acceptScheduleRequests(stopCh)
	log.Info("IPAM handle garbage collector is now running")

	// Kick off a start of day sync. Write non-blocking so that if a sync is
	// already scheduled, we don't schedule another.
	kick(c.schedule)

	<-stopCh
	log.Info("Stopping IPAM handle GC")
}

func (c *IPAMController) acceptScheduleRequests(stopCh <-chan struct{}) {
	for {
		// Wait until something wakes us up, or we are stopped
		select {
		case <-c.schedule:
			// Mark for rate limiting so we don't do this too often.
			err := c.syncDelete()
			if err != nil {
				// Reschedule the sync since we hit an error. Note that
				// syncDelete() does its own rate limiting, so it's fine to
				// reschedule immediately.
				kick(c.schedule)
			}

			// Simple batching. We might receive lots of kicks on the schedule queue
			// at once if lots of pods are deleted. Rather than call syncDelete for each,
			// we can condense the events slightly.
			time.Sleep(1 * time.Second)
		case <-stopCh:
			return
		}
	}
}

func (c *IPAMController) syncDelete() error {
	time.Sleep(c.rl.When(RateLimitSyncHandles))
	handles := c.indexer.List()
	logrus.Debugf("Found %d IPAM handles", len(handles))
	for _, h := range handles {
		u := h.(*v3.IPAMHandle)
		if u.GetDeletionTimestamp() == nil {
			continue
		}

		// Release any IPs with this handle.
		handleID := u.Spec.HandleID
		log.Infof("Found orphaned IPAM handle: %s", handleID)
		if err := c.calicoClient.IPAM().ReleaseByHandle(c.ctx, handleID); err != nil {
			if _, ok := err.(cerrors.ErrorResourceDoesNotExist); !ok {
				logrus.WithError(err).Error("Failed to release IP")
				return err
			}
		}
	}
	logrus.Debugf("Finished ipam handle GC cycle")
	c.rl.Forget(RateLimitSyncHandles)
	return nil
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
