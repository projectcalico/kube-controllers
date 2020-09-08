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

package routereflector

import (
	"context"

	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoClient "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"

	log "github.com/sirupsen/logrus"
)

type bgpPeer interface {
	list() (*calicoApi.BGPPeerList, error)
	save(*calicoApi.BGPPeer) error
	remove(*calicoApi.BGPPeer) error
}

type bgpPeerImpl struct {
	calicoClient calicoClient.Interface
}

func (b *bgpPeerImpl) list() (*calicoApi.BGPPeerList, error) {
	return b.calicoClient.BGPPeers().List(context.Background(), options.ListOptions{})
}

func (b *bgpPeerImpl) save(peer *calicoApi.BGPPeer) error {
	if peer.GetUID() == "" {
		log.Debugf("Creating new BGPPeers: %s", peer.Name)
		if _, err := b.calicoClient.BGPPeers().Create(context.Background(), peer, options.SetOptions{}); err != nil {
			return err
		}
	} else {
		log.Debugf("Updating existing BGPPeers: %s", peer.Name)
		if _, err := b.calicoClient.BGPPeers().Update(context.Background(), peer, options.SetOptions{}); err != nil {
			return err
		}
	}

	return nil
}

func (b *bgpPeerImpl) remove(peer *calicoApi.BGPPeer) error {
	_, err := b.calicoClient.BGPPeers().Delete(context.Background(), peer.GetName(), options.DeleteOptions{})
	return err
}

func newBGPPeer(calicoClient calicoClient.Interface) bgpPeer {
	return &bgpPeerImpl{
		calicoClient: calicoClient,
	}
}
