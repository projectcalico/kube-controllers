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

package datastores

import (
	"context"

	"github.com/projectcalico/kube-controllers/pkg/controllers/routereflector/topologies"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/options"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type mockTopology struct {
	getClusterID func() string
	getNodeLabel func() (string, string)
}

func (m mockTopology) IsRouteReflector(UID string, _ map[string]string) bool {
	return false
}

func (m mockTopology) GetClusterID(string, int64) string {
	return m.getClusterID()
}

func (m mockTopology) GetNodeLabel(string) (string, string) {
	return m.getNodeLabel()
}

func (m mockTopology) NewNodeListOptions(labels map[string]string) metav1.ListOptions {
	return metav1.ListOptions{}
}

func (m mockTopology) GetRouteReflectorStatuses(map[*apiv3.Node]bool) []topologies.RouteReflectorStatus {
	return nil
}

func (m mockTopology) GenerateBGPPeers(map[types.UID]*apiv3.Node, map[*apiv3.Node]bool, *calicoApi.BGPPeerList) ([]calicoApi.BGPPeer, []calicoApi.BGPPeer) {
	return nil, nil
}

type mockCalicoClient struct {
	update func(*calicoApi.Node) (*calicoApi.Node, error)
	list   func() (*calicoApi.NodeList, error)
}

func (m mockCalicoClient) Update(_ context.Context, node *calicoApi.Node, _ options.SetOptions) (*calicoApi.Node, error) {
	if m.update != nil {
		return m.update(node)
	}
	return nil, nil
}

func (m mockCalicoClient) List(context.Context, options.ListOptions) (*calicoApi.NodeList, error) {
	if m.list != nil {
		return m.list()
	}
	return nil, nil
}
