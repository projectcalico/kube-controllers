// Copyright (c) 2020 IBM Corporation All rights reserved.
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DefaultRouteReflectorMeshName   = "rrs-to-rrs"
	DefaultRouteReflectorClientName = "peer-to-rrs-%d"
)

type RouteReflectorStatus struct {
	Zones       []string
	ActualRRs   int
	ExpectedRRs int
	Nodes       []*corev1.Node
}

type Topology interface {
	IsRouteReflector(string, map[string]string) bool
	GetClusterID(string, int64) string
	GetNodeLabel(string) (string, string)
	GetNodeFilter(*corev1.Node) func(*corev1.Node) bool
	GetRouteReflectorStatuses(map[*corev1.Node]bool) []RouteReflectorStatus
	GenerateBGPPeers(map[*corev1.Node]bool, []*apiv3.BGPPeer) ([]*apiv3.BGPPeer, []*apiv3.BGPPeer)
}

type Config struct {
	NodeLabelKey      string
	NodeLabelValue    string
	ZoneLabel         string
	HostnameLabel     string
	ClusterID         string
	Min               int
	Max               int
	Ration            float64
	ReflectorsPerNode int
}

func generateBGPPeerStub(name string) *apiv3.BGPPeer {
	return &apiv3.BGPPeer{
		TypeMeta: metav1.TypeMeta{
			Kind:       apiv3.KindBGPPeer,
			APIVersion: apiv3.GroupVersionCurrent,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func findBGPPeer(peers []*apiv3.BGPPeer, name string) *apiv3.BGPPeer {
	for _, p := range peers {
		if p.GetName() == name {
			return p
		}
	}

	return nil
}

func countActiveNodes(nodes map[*corev1.Node]bool) (actualNodes int) {
	for _, isReady := range nodes {
		if isReady {
			actualNodes++
		}
	}

	return
}

func countActiveRouteReflectors(isRouteReflector func(string, map[string]string) bool, nodes map[*corev1.Node]bool) (actualRRs int) {
	for n, isReady := range nodes {
		if isReady && isRouteReflector(string(n.GetUID()), n.GetLabels()) {
			actualRRs++
		}
	}

	return
}
