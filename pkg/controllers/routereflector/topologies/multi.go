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
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strings"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	calicoApi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	log "github.com/sirupsen/logrus"
)

type MultiTopology struct {
	Config
	single SingleTopology
}

func (t *MultiTopology) IsRouteReflector(nodeID string, labels map[string]string) bool {
	_, ok := labels[t.NodeLabelKey]
	return ok
}

func (t *MultiTopology) GetClusterID(nodeID string, seed int64) string {
	count := strings.Count(t.ClusterID, "%d")
	parts := make([]interface{}, 0)

	rand1 := rand.New(rand.NewSource(int64(getRouteReflectorID(nodeID))))
	parts = append(parts, rand1.Int31n(254))

	rand2 := rand.New(rand.NewSource(seed))
	for len(parts) < count {
		parts = append(parts, rand2.Int31n(254))
	}

	return fmt.Sprintf(t.ClusterID, parts...)
}

func (t *MultiTopology) GetNodeLabel(nodeID string) (string, string) {
	return t.NodeLabelKey, t.getNodeLabel(nodeID)
}

func (t *MultiTopology) NewNodeListOptions(nodeLabels map[string]string) metav1.ListOptions {
	return metav1.ListOptions{}
}

func (t *MultiTopology) GetRouteReflectorStatuses(nodes map[*apiv3.Node]bool) (statuses []RouteReflectorStatus) {
	perZone := map[string]map[*apiv3.Node]bool{}
	for n := range nodes {
		zone := n.GetLabels()[t.ZoneLabel]
		if _, ok := perZone[zone]; !ok {
			perZone[zone] = map[*apiv3.Node]bool{}
		}
		perZone[zone][n] = nodes[n]
	}

	expRRs := t.single.calculateExpectedNumber(countActiveNodes(nodes))
	expRRsPerZone := int(math.Ceil(float64(expRRs) / float64(len(perZone))))

	for _, zoneNodes := range perZone {
		status := t.single.GetRouteReflectorStatuses(zoneNodes)[0]

		// TODO On this way it collects info in single and multi too twice
		status.ActualRRs = countActiveRouteReflectors(t.IsRouteReflector, zoneNodes)
		status.ExpectedRRs = expRRsPerZone

		statuses = append(statuses, status)
	}

	sort.Slice(statuses, func(i, j int) bool {
		return len(statuses[i].Nodes) < len(statuses[j].Nodes)
	})

	return
}

func (t *MultiTopology) GenerateBGPPeers(allNodes map[types.UID]*apiv3.Node, nodes map[*apiv3.Node]bool, existingPeers *calicoApi.BGPPeerList) (toRefresh []calicoApi.BGPPeer, toDelete []calicoApi.BGPPeer) {
	routeReflectors := t.collectRouteReflectors(allNodes)

	// Sorting RRs and Nodes for deterministic RR for Node selection
	sort.Slice(routeReflectors, func(i, j int) bool {
		return routeReflectors[i].GetCreationTimestamp().UnixNano() < routeReflectors[j].GetCreationTimestamp().UnixNano()
	})

	nodeList := []*apiv3.Node{}
	for n := range nodes {
		nodeList = append(nodeList, n)
	}
	sort.Slice(nodeList, func(i, j int) bool {
		return nodeList[i].GetCreationTimestamp().UnixNano() < nodeList[j].GetCreationTimestamp().UnixNano()
	})

	rrConfig := findBGPPeer(existingPeers.Items, DefaultRouteReflectorMeshName)
	if rrConfig == nil {
		log.Debugf("Creating new RR full-mesh BGPPeers: %s", DefaultRouteReflectorMeshName)
		rrConfig = generateBGPPeerStub(DefaultRouteReflectorMeshName)
	}

	toKeep := map[string]bool{}
	selector := fmt.Sprintf("has(%s)", t.NodeLabelKey)
	if rrConfig.Spec.NodeSelector != selector || rrConfig.Spec.PeerSelector != selector {
		rrConfig.Spec = calicoApi.BGPPeerSpec{
			NodeSelector: selector,
			PeerSelector: selector,
		}

		toRefresh = append(toRefresh, *rrConfig)
	} else {
		toKeep[rrConfig.GetName()] = true
	}

	// TODO make it configurable
	peers := int(math.Min(float64(len(routeReflectors)), 3))

	rrIndex := -1
	rrIndexPerZone := map[string]int{}
	zones := []string{}
	rrPerZone := map[string][]*apiv3.Node{}

	// Creat per zone RR lists for MZR selection
	if t.Config.ZoneLabel != "" {
		for i, rr := range routeReflectors {
			rrZone := rr.GetLabels()[t.Config.ZoneLabel]
			log.Debugf("RR:%s's zone: %s", rr.GetName(), rrZone)
			rrPerZone[rrZone] = append(rrPerZone[rrZone], routeReflectors[i])
		}

		for zone := range rrPerZone {
			zones = append(zones, zone)
		}
		sort.Strings(zones)
	}

	for _, n := range nodeList {
		if t.IsRouteReflector(string(n.GetUID()), n.GetLabels()) {
			continue
		}

		routeReflectorsForNode := []*apiv3.Node{}

		// Run MZR selection first
		if t.Config.ZoneLabel != "" {
			nodeZone := n.GetLabels()[t.Config.ZoneLabel]
			log.Debugf("Node's zone: %s", nodeZone)

			// Select the 1st RR from the same zone if there're any
			if len(rrPerZone[nodeZone]) > 0 {
				rr := selectRRfromZone(rrIndexPerZone, rrPerZone, nodeZone)
				log.Debugf("Adding %s as 1st RR for Node:%s", rr.GetName(), n.GetName())
				routeReflectorsForNode = append(routeReflectorsForNode, rr)
			}

			// Select the 2nd RR from a different zone
			for _, zone := range zones {
				if zone != nodeZone {
					rr := selectRRfromZone(rrIndexPerZone, rrPerZone, zone)
					log.Debugf("Adding %s as 2nd RR for Node:%s", rr.GetName(), n.GetName())
					routeReflectorsForNode = append(routeReflectorsForNode, rr)
					break
				}
			}
		}

		// Selecting the remaning RRs sequentially
		for len(routeReflectorsForNode) < peers {
			rrIndex++
			if rrIndex == len(routeReflectors) {
				rrIndex = 0
			}

			rr := routeReflectors[rrIndex]
			if !isAlreadySelected(routeReflectorsForNode, rr) {
				log.Debugf("Adding %s to RRs of %s", rr.GetName(), n.GetName())
				routeReflectorsForNode = append(routeReflectorsForNode, rr)
			}
		}

		// TODO make configurable
		nodeSelector := fmt.Sprintf("kubernetes.io/hostname=='%s'", n.GetLabels()["kubernetes.io/hostname"])

		for _, rr := range routeReflectorsForNode {
			rrID := getRouteReflectorID(string(rr.GetUID()))
			name := fmt.Sprintf(DefaultRouteReflectorClientName+"-%s", rrID, n.GetUID())

			clientConfig := findBGPPeer(existingPeers.Items, name)

			if clientConfig != nil {
				toKeep[clientConfig.GetName()] = true
				continue
			}

			log.Debugf("New BGPPeers: %s", name)
			clientConfig = generateBGPPeerStub(name)
			clientConfig.Spec = calicoApi.BGPPeerSpec{
				NodeSelector: nodeSelector,
				PeerSelector: fmt.Sprintf("%s=='%d'", t.NodeLabelKey, rrID),
			}

			log.Debugf("Adding %s to the BGPPeers refresh list", clientConfig.GetName())
			toRefresh = append(toRefresh, *clientConfig)
		}
	}

	for i := range existingPeers.Items {
		if _, ok := toKeep[existingPeers.Items[i].GetName()]; !ok && findBGPPeer(toRefresh, existingPeers.Items[i].GetName()) == nil {
			log.Debugf("Adding %s to the BGPPeers delete list", existingPeers.Items[i].GetName())
			toDelete = append(toDelete, existingPeers.Items[i])
		}
	}

	return
}

func (t *MultiTopology) getNodeLabel(nodeID string) string {
	if t.NodeLabelValue == "" {
		return fmt.Sprintf("%d", getRouteReflectorID(nodeID))
	}
	return fmt.Sprintf("%s-%d", t.NodeLabelValue, getRouteReflectorID(nodeID))
}

func (t *MultiTopology) collectRouteReflectors(allNodes map[types.UID]*apiv3.Node) (rrs []*apiv3.Node) {
	for _, n := range allNodes {
		if _, ok := n.GetLabels()[t.NodeLabelKey]; ok {
			rrs = append(rrs, n)
		}
	}

	return
}

func selectRRfromZone(rrIndexPerZone map[string]int, rrPerZone map[string][]*apiv3.Node, zone string) *apiv3.Node {
	rrIndexPerZone[zone]++
	if rrIndexPerZone[zone] == len(rrPerZone[zone]) {
		rrIndexPerZone[zone] = 0
	}
	return rrPerZone[zone][rrIndexPerZone[zone]]
}

func isAlreadySelected(rrs []*apiv3.Node, r *apiv3.Node) bool {
	for i := range rrs {
		if rrs[i].GetName() == r.GetName() {
			return true
		}
	}
	return false
}

func getRouteReflectorID(nodeID string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(nodeID))
	return h.Sum32()
}

func NewMultiTopology(config Config) Topology {
	t := &MultiTopology{
		Config: config,
		single: SingleTopology{
			Config: config,
		},
	}

	t.ClusterID = strings.Replace(t.ClusterID, ".0", ".%d", -1)

	return t
}
