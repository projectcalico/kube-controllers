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

package topologies

import (
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// TODO Make it configurable
	DefaultRouteReflectorMeshName   = "rrs-to-rrs"
	DefaultRouteReflectorClientName = "peer-to-rrs-%d"
)

type RouteReflectorStatus struct {
	Zones       []string
	ActualRRs   int
	ExpectedRRs int
	Nodes       []*apiv3.Node
}

type Topology interface {
	IsRouteReflector(string, map[string]string) bool
	GetClusterID(string, int64) string
	GetNodeLabel(string) (string, string)
	NewNodeListOptions(labels map[string]string) metav1.ListOptions
	GetRouteReflectorStatuses(map[*apiv3.Node]bool) []RouteReflectorStatus
	GenerateBGPPeers([]corev1.Node, map[*apiv3.Node]bool, *calicoApi.BGPPeerList) ([]calicoApi.BGPPeer, []calicoApi.BGPPeer)
}

type Config struct {
	NodeLabelKey   string
	NodeLabelValue string
	ZoneLabel      string
	ClusterID      string
	Min            int
	Max            int
	Ration         float64
}

func generateBGPPeerStub(name string) *calicoApi.BGPPeer {
	return &calicoApi.BGPPeer{
		TypeMeta: metav1.TypeMeta{
			Kind:       calicoApi.KindBGPPeer,
			APIVersion: calicoApi.GroupVersionCurrent,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func findBGPPeer(peers []calicoApi.BGPPeer, name string) *calicoApi.BGPPeer {
	for _, p := range peers {
		if p.GetName() == name {
			return &p
		}
	}

	return nil
}

func collectNodeInfo(isRouteReflector func(string, map[string]string) bool, nodes map[*apiv3.Node]bool) (readyNodes, actualRRs int) {
	for n, isReady := range nodes {
		if isReady {
			readyNodes++
			if isRouteReflector(string(n.GetUID()), n.GetLabels()) {
				actualRRs++
			}
		}
	}

	return
}
