// Copyright (c) 2019 Tigera, Inc. All rights reserved.
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

	"github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/options"
	log "github.com/sirupsen/logrus"

	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// syncDelete is the main work routine of the controller. It queries Calico and
// K8s, and deletes any Calico nodes which do not exist in K8s.
func (c *NodeController) syncDeleteEtcd() error {
	// Possibly rate limit calls to Calico
	time.Sleep(c.rl.When(RateLimitCalicoList))
	cNodes, err := c.calicoClient.Nodes().List(c.ctx, options.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Error listing Calico nodes")
		return err
	}
	c.rl.Forget(RateLimitCalicoList)

	time.Sleep(c.rl.When(RateLimitK8s))
	kNodes, err := c.k8sClientset.CoreV1().Nodes().List(context.Background(), meta_v1.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Error listing K8s nodes")
		return err
	}
	c.rl.Forget(RateLimitK8s)
	kNodeIdx := make(map[string]bool)
	for _, node := range kNodes.Items {
		kNodeIdx[node.Name] = true
	}

	for _, node := range cNodes.Items {
		k8sNodeName := getK8sNodeName(node)
		if k8sNodeName != "" && !kNodeIdx[k8sNodeName] {
			// No matching Kubernetes node with that name
			time.Sleep(c.rl.When(RateLimitCalicoDelete))
			_, err := c.calicoClient.Nodes().Delete(c.ctx, node.Name, options.DeleteOptions{})
			if _, doesNotExist := err.(errors.ErrorResourceDoesNotExist); err != nil && !doesNotExist {
				// We hit an error other than "does not exist".
				log.WithError(err).Errorf("Error deleting Calico node: %v", node.Name)
				return err
			}
			c.rl.Forget(RateLimitCalicoDelete)
		}
	}
	return nil
}
